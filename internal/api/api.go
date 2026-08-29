package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-github/v90/github"
	"github.com/mona-actions/gh-migrate-lfs/internal/output"
	"github.com/spf13/viper"
)

type proxyConfig struct {
	HTTPProxy  string
	HTTPSProxy string
	NoProxy    string
}

func newGitHubClient(token, hostname string) (*github.Client, error) {
	if token == "" {
		return nil, errors.New("GitHub token is required")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxyFunc(proxyConfigFromEnvironment())
	httpClient := &http.Client{Transport: transport, Timeout: 60 * time.Second}
	options := []github.ClientOptionsFunc{
		github.WithHTTPClient(httpClient),
		github.WithAuthToken(token),
	}
	if strings.TrimSpace(hostname) != "" {
		apiURL, uploadURL, err := enterpriseURLs(hostname)
		if err != nil {
			return nil, err
		}
		options = append(options, github.WithEnterpriseURLs(apiURL, uploadURL))
	}
	client, err := github.NewClient(options...)
	if err != nil {
		return nil, fmt.Errorf("configure GitHub client: %w", err)
	}
	return client, nil
}

func enterpriseURLs(hostname string) (string, string, error) {
	hostname = strings.TrimSpace(hostname)
	if !strings.Contains(hostname, "://") {
		hostname = "https://" + hostname
	}
	parsed, err := url.Parse(hostname)
	if err != nil || parsed.Host == "" {
		return "", "", fmt.Errorf("invalid GitHub Enterprise hostname %q", hostname)
	}
	if parsed.User != nil {
		return "", "", errors.New("GitHub Enterprise hostname must not contain credentials")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" {
		return "", "", errors.New("GitHub Enterprise hostname must use HTTPS")
	}

	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/api/v3")
	if path != "" {
		return "", "", fmt.Errorf("GitHub Enterprise hostname must not contain path %q", path)
	}
	origin := parsed.Scheme + "://" + parsed.Host
	return origin + "/api/v3/", origin + "/uploads/", nil
}

func RepositoryCloneURL(hostname, organization, repository string) (string, error) {
	if organization == "" || repository == "" || strings.ContainsAny(organization+repository, "/\\") {
		return "", errors.New("organization and repository must each be one path segment")
	}
	origin := "https://github.com"
	if strings.TrimSpace(hostname) != "" {
		apiURL, _, err := enterpriseURLs(hostname)
		if err != nil {
			return "", err
		}
		parsed, err := url.Parse(apiURL)
		if err != nil {
			return "", err
		}
		origin = parsed.Scheme + "://" + parsed.Host
	}
	return fmt.Sprintf("%s/%s/%s.git", origin, organization, repository), nil
}

func proxyFunc(config *proxyConfig) func(*http.Request) (*url.URL, error) {
	return func(request *http.Request) (*url.URL, error) {
		if config == nil {
			return http.ProxyFromEnvironment(request)
		}
		requestHost := request.URL.Hostname()
		for _, excluded := range strings.Split(config.NoProxy, ",") {
			excluded = strings.TrimSpace(excluded)
			if excluded != "" && (requestHost == excluded || strings.HasSuffix(requestHost, "."+strings.TrimPrefix(excluded, "."))) {
				return nil, nil
			}
		}
		if request.URL.Scheme == "https" && config.HTTPSProxy != "" {
			return url.Parse(config.HTTPSProxy)
		}
		if request.URL.Scheme == "http" && config.HTTPProxy != "" {
			return url.Parse(config.HTTPProxy)
		}
		return nil, nil
	}
}

func proxyConfigFromEnvironment() *proxyConfig {
	return &proxyConfig{
		HTTPProxy:  viper.GetString("HTTP_PROXY"),
		HTTPSProxy: viper.GetString("HTTPS_PROXY"),
		NoProxy:    viper.GetString("NO_PROXY"),
	}
}

func retryOperation(ctx context.Context, renderer *output.Renderer, operation func() error) error {
	maxRetries := viper.GetInt("GHMLFS_RETRY_MAX")
	if maxRetries <= 0 {
		maxRetries = 3
	}
	retryDelay, err := time.ParseDuration(viper.GetString("GHMLFS_RETRY_DELAY"))
	if err != nil || retryDelay <= 0 {
		retryDelay = time.Second
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = operation()
		if lastErr == nil || !retryableError(lastErr) {
			return lastErr
		}
		if attempt == maxRetries {
			break
		}
		delay := min(retryDelay*time.Duration(1<<(attempt-1)), 16*time.Second)
		renderer.Verbose("Attempt %d failed, retrying in %v: %v", attempt, delay, lastErr)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func retryableError(err error) bool {
	var responseError *github.ErrorResponse
	if errors.As(err, &responseError) && responseError.Response != nil {
		status := responseError.Response.StatusCode
		return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
	}
	var rateLimitError *github.RateLimitError
	if errors.As(err, &rateLimitError) {
		return true
	}
	var abuseRateLimitError *github.AbuseRateLimitError
	if errors.As(err, &abuseRateLimitError) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func FindLFSAttributes(ctx context.Context, org, repo, token string, depth int, hostname string, renderer *output.Renderer) (bool, string, error) {
	client, err := newGitHubClient(token, hostname)
	if err != nil {
		return false, "", fmt.Errorf("initialize GitHub client: %w", err)
	}
	if depth < 1 {
		depth = 1
	}

	var searchDir func(string, int) (bool, string, error)
	searchDir = func(path string, currentDepth int) (bool, string, error) {
		if currentDepth > depth {
			return false, "", nil
		}

		var fileContent *github.RepositoryContent
		var dirContent []*github.RepositoryContent
		var response *github.Response
		err := retryOperation(ctx, renderer, func() error {
			var requestErr error
			fileContent, dirContent, response, requestErr = client.Repositories.GetContents(ctx, org, repo, path, nil)
			return requestErr
		})
		if err != nil {
			if response != nil && response.StatusCode == http.StatusNotFound {
				return false, "", nil
			}
			return false, "", fmt.Errorf("fetch contents of %q: %w", path, err)
		}

		if fileContent != nil && fileContent.GetName() == ".gitattributes" {
			matched, err := gitAttributesUsesLFS(ctx, client, org, repo, fileContent.GetPath(), renderer)
			return matched, fileContent.GetPath(), err
		}
		for _, item := range dirContent {
			if item.GetType() != "file" || item.GetName() != ".gitattributes" {
				continue
			}
			matched, err := gitAttributesUsesLFS(ctx, client, org, repo, item.GetPath(), renderer)
			if err != nil || matched {
				return matched, item.GetPath(), err
			}
		}
		for _, item := range dirContent {
			if item.GetType() != "dir" {
				continue
			}
			matched, foundPath, err := searchDir(item.GetPath(), currentDepth+1)
			if err != nil || matched {
				return matched, foundPath, err
			}
		}
		return false, "", nil
	}

	matched, foundPath, err := searchDir("", 1)
	if err != nil {
		return false, "", fmt.Errorf("search repository: %w", err)
	}
	return matched, foundPath, nil
}

func gitAttributesUsesLFS(ctx context.Context, client *github.Client, org, repo, path string, renderer *output.Renderer) (bool, error) {
	var content string
	err := retryOperation(ctx, renderer, func() error {
		reader, _, err := client.Repositories.DownloadContents(ctx, org, repo, path, nil)
		if err != nil {
			return err
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		content = string(data)
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return strings.Contains(content, "filter=lfs"), nil
}

func ListRepositories(ctx context.Context, org, token, hostname string, renderer *output.Renderer) ([]string, error) {
	if org == "" {
		return nil, errors.New("organization name is required")
	}
	client, err := newGitHubClient(token, hostname)
	if err != nil {
		return nil, fmt.Errorf("initialize GitHub client: %w", err)
	}

	var repositories []string
	opts := &github.RepositoryListByOrgOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		var page []*github.Repository
		var response *github.Response
		err := retryOperation(ctx, renderer, func() error {
			var requestErr error
			page, response, requestErr = client.Repositories.ListByOrg(ctx, org, opts)
			return requestErr
		})
		if err != nil {
			return nil, fmt.Errorf("list repositories for %s: %w", org, err)
		}
		for _, repository := range page {
			if repository.GetName() != "" {
				repositories = append(repositories, repository.GetName())
			}
		}
		if response == nil || response.NextPage == 0 {
			return repositories, nil
		}
		opts.Page = response.NextPage
	}
}
