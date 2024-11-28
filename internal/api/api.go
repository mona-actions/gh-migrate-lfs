package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-github/v66/github"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

type ProxyConfig struct {
	HTTPProxy  string
	HTTPSProxy string
	NoProxy    string
}

// Helper function to handle optional hostname parameter
func getHostname(hostname ...string) string {
	if len(hostname) > 0 {
		return hostname[0]
	}
	return ""
}

func newGitHubClientWithHostname(token string, hostname string) (*github.Client, error) {
	client, err := newGitHubClientWithProxy(token, GetProxyConfigFromEnv())
	if err != nil {
		return nil, err
	}

	if hostname == "" {
		return client, nil
	}

	baseURL, err := url.Parse(hostname)
	if err != nil {
		return nil, fmt.Errorf("invalid hostname URL provided (%s): %w", baseURL, err)
	}

	enterpriseClient, err := client.WithEnterpriseURLs(hostname, hostname)
	if err != nil {
		return nil, fmt.Errorf("failed to configure enterprise URLs for %s: %w", hostname, err)
	}

	return enterpriseClient, nil
}

func newGitHubClientWithProxy(token string, proxyConfig *ProxyConfig) (*github.Client, error) {
	if token == "" {
		return nil, fmt.Errorf("GitHub token is required")
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)

	transport := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if proxyConfig != nil && proxyConfig.NoProxy != "" {
				noProxyURLs := strings.Split(proxyConfig.NoProxy, ",")
				reqHost := req.URL.Host
				for _, noProxy := range noProxyURLs {
					if strings.TrimSpace(noProxy) == reqHost {
						return nil, nil
					}
				}
			}

			if proxyConfig != nil {
				if req.URL.Scheme == "https" && proxyConfig.HTTPSProxy != "" {
					return url.Parse(proxyConfig.HTTPSProxy)
				}
				if req.URL.Scheme == "http" && proxyConfig.HTTPProxy != "" {
					return url.Parse(proxyConfig.HTTPProxy)
				}
			}
			return nil, nil
		},
	}

	tc := oauth2.NewClient(ctx, ts)
	tc.Transport = &oauth2.Transport{
		Base:   transport,
		Source: ts,
	}

	return github.NewClient(tc), nil
}

func GetProxyConfigFromEnv() *ProxyConfig {
	return &ProxyConfig{
		HTTPProxy:  viper.GetString("HTTP_PROXY"),
		HTTPSProxy: viper.GetString("HTTPS_PROXY"),
		NoProxy:    viper.GetString("NO_PROXY"),
	}
}

func retryOperation(operation func() error) error {
	maxRetries := viper.GetInt("MAX_RETRIES")
	if maxRetries <= 0 {
		maxRetries = 3 // fallback default
	}

	retryDelay, err := time.ParseDuration(viper.GetString("RETRY_DELAY"))
	if err != nil {
		retryDelay = time.Second // fallback default
	}

	var apiErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		apiErr = operation()
		if apiErr == nil {
			return nil
		}

		if attempt < maxRetries {
			waitTime := retryDelay * time.Duration(1<<uint(attempt-1))
			fmt.Printf("Attempt %d failed, retrying in %v: %v\n", attempt, waitTime, apiErr)
			time.Sleep(waitTime)
		}
	}
	return apiErr
}

func readContent(rc io.ReadCloser) (string, error) {
	defer rc.Close()
	content, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func CheckGitAttributes(org, repo, token string, depth int, hostname ...string) (bool, string, error) {
	client, err := newGitHubClientWithHostname(token, getHostname(hostname...))
	if err != nil {
		return false, "", fmt.Errorf("failed to initialize GitHub client: %w", err)
	}

	ctx := context.Background()
	var foundPath string
	var hasLFS bool

	visited := make(map[string]bool)

	var searchDir func(path string, currentDepth int) error
	searchDir = func(path string, currentDepth int) error {
		if currentDepth > depth {
			return nil
		}

		if visited[path] {
			return nil
		}
		visited[path] = true

		err := retryOperation(func() error {
			opts := &github.RepositoryContentGetOptions{}

			fileContent, dirContent, resp, err := client.Repositories.GetContents(ctx, org, repo, path, opts)
			if err != nil {
				if resp != nil && resp.StatusCode == http.StatusNotFound {
					return nil
				}
				return fmt.Errorf("error fetching contents of %s: %w", path, err)
			}

			// Check single file
			if fileContent != nil && fileContent.GetName() == ".gitattributes" {
				rawContent, _, err := client.Repositories.DownloadContents(ctx, org, repo, fileContent.GetPath(), opts)
				if err != nil {
					return fmt.Errorf("error reading content: %w", err)
				}

				content, err := readContent(rawContent)
				if err != nil {
					return fmt.Errorf("error reading raw content: %w", err)
				}

				if strings.Contains(content, "filter=lfs") {
					hasLFS = true
					foundPath = fileContent.GetPath()
					return nil // Successfully found LFS, not an error
				}
			}

			// Check directory
			if dirContent != nil {
				for _, item := range dirContent {
					if item.GetType() == "file" && item.GetName() == ".gitattributes" {
						rawContent, _, err := client.Repositories.DownloadContents(ctx, org, repo, item.GetPath(), opts)
						if err != nil {
							return fmt.Errorf("error reading content: %w", err)
						}

						content, err := readContent(rawContent)
						if err != nil {
							return fmt.Errorf("error reading raw content: %w", err)
						}

						if strings.Contains(content, "filter=lfs") {
							hasLFS = true
							foundPath = item.GetPath()
							return nil // Successfully found LFS, not an error
						}
					}
				}

				// Only continue searching if we haven't found LFS yet
				if !hasLFS {
					for _, item := range dirContent {
						if item.GetType() == "dir" {
							if err := searchDir(item.GetPath(), currentDepth+1); err != nil {
								return err
							}
							// If we found LFS in a subdirectory, stop searching
							if hasLFS {
								return nil
							}
						}
					}
				}
			}

			return nil
		})

		return err
	}

	err = searchDir("", 1)
	if err != nil {
		return false, "", fmt.Errorf("error searching repository: %w", err)
	}

	return hasLFS, foundPath, nil
}

func GetRepositories(org, token string, hostname ...string) ([]string, error) {
	if org == "" {
		return nil, fmt.Errorf("organization name is required")
	}

	client, err := newGitHubClientWithHostname(token, getHostname(hostname...))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GitHub client: %w", err)
	}

	var allRepos []string
	opts := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	err = retryOperation(func() error {
		for {
			repos, resp, apiErr := client.Repositories.ListByOrg(context.Background(), org, opts)
			if apiErr != nil {
				return apiErr
			}

			if repos == nil {
				return fmt.Errorf("no repositories data returned for organization %s", org)
			}

			for _, repo := range repos {
				if repo != nil && repo.Name != nil {
					allRepos = append(allRepos, *repo.Name)
				}
			}

			if resp == nil || resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list repositories for %s: %w", org, err)
	}

	return allRepos, nil
}
