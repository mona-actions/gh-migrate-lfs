package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v90/github"
	"github.com/mona-actions/gh-migrate-lfs/internal/output"
	"github.com/spf13/viper"
)

type retryableNetworkError struct{}

func (retryableNetworkError) Error() string   { return "temporary network error" }
func (retryableNetworkError) Timeout() bool   { return false }
func (retryableNetworkError) Temporary() bool { return true }

func TestEnterpriseURLs(t *testing.T) {
	t.Parallel()

	for _, hostname := range []string{"github.example.com", "https://github.example.com", "https://github.example.com/api/v3"} {
		apiURL, uploadURL, err := enterpriseURLs(hostname)
		if err != nil {
			t.Fatalf("enterpriseURLs(%q) error = %v", hostname, err)
		}
		if apiURL != "https://github.example.com/api/v3/" || uploadURL != "https://github.example.com/uploads/" {
			t.Fatalf("enterpriseURLs(%q) = %q, %q", hostname, apiURL, uploadURL)
		}
	}
}

func TestEnterpriseURLsRejectsUnsafeValues(t *testing.T) {
	t.Parallel()

	for _, hostname := range []string{
		"http://github.example.com",
		"https://token@github.example.com",
		"https://github.example.com/other",
	} {
		if _, _, err := enterpriseURLs(hostname); err == nil {
			t.Errorf("enterpriseURLs(%q) returned no error", hostname)
		}
	}
}

func TestRetryableError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   bool
	}{
		{status: http.StatusUnauthorized, want: false},
		{status: http.StatusForbidden, want: false},
		{status: http.StatusRequestTimeout, want: true},
		{status: http.StatusTooManyRequests, want: true},
		{status: http.StatusInternalServerError, want: true},
	}
	for _, test := range tests {
		err := &github.ErrorResponse{Response: &http.Response{StatusCode: test.status}}
		if got := retryableError(err); got != test.want {
			t.Errorf("retryableError(status %d) = %t, want %t", test.status, got, test.want)
		}
	}
	if retryableError(errors.New("plain error")) {
		t.Fatal("plain errors must not be retried")
	}
}

func TestRepositoryCloneURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hostname string
		want     string
	}{
		{want: "https://github.com/octo/repo.git"},
		{hostname: "github.example.com", want: "https://github.example.com/octo/repo.git"},
		{hostname: "https://github.example.com/api/v3", want: "https://github.example.com/octo/repo.git"},
	}
	for _, test := range tests {
		got, err := RepositoryCloneURL(test.hostname, "octo", "repo")
		if err != nil {
			t.Fatalf("RepositoryCloneURL() error = %v", err)
		}
		if got != test.want {
			t.Fatalf("RepositoryCloneURL() = %q, want %q", got, test.want)
		}
	}
}

func TestListRepositoriesPaginates(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/api/v3/orgs/octo/repos" {
			http.NotFound(response, request)
			return
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer token" {
			t.Errorf("Authorization = %q", authorization)
		}
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Query().Get("page") == "2" {
			fmt.Fprint(response, `[{"name":"second"},{"name":""}]`)
			return
		}
		response.Header().Set("Link", fmt.Sprintf("<%s/api/v3/orgs/octo/repos?page=2>; rel=\"next\"", serverURL(request)))
		fmt.Fprint(response, `[{"name":"first"}]`)
	}))
	defer server.Close()

	repositories, err := ListRepositories(context.Background(), "octo", "token", localhostURL(server.URL), output.New(output.Options{}))
	if err != nil {
		t.Fatalf("ListRepositories() error = %v", err)
	}
	if got, want := strings.Join(repositories, ","), "first,second"; got != want {
		t.Fatalf("repositories = %q, want %q", got, want)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestFindLFSAttributesSearchesToConfiguredDepth(t *testing.T) {
	content := base64.StdEncoding.EncodeToString([]byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v3/repos/octo/repo/contents/":
			fmt.Fprint(response, `[{"type":"dir","name":"nested","path":"nested"}]`)
		case "/api/v3/repos/octo/repo/contents/nested":
			fmt.Fprint(response, `[{"type":"file","name":".gitattributes","path":"nested/.gitattributes"}]`)
		case "/api/v3/repos/octo/repo/contents/nested/.gitattributes":
			fmt.Fprintf(response, `{"type":"file","name":".gitattributes","path":"nested/.gitattributes","encoding":"base64","content":%q}`, content)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	renderer := output.New(output.Options{})
	matched, path, err := FindLFSAttributes(context.Background(), "octo", "repo", "token", 2, localhostURL(server.URL), renderer)
	if err != nil {
		t.Fatalf("FindLFSAttributes() error = %v", err)
	}
	if !matched || path != "nested/.gitattributes" {
		t.Fatalf("matched=%t path=%q", matched, path)
	}

	matched, path, err = FindLFSAttributes(context.Background(), "octo", "repo", "token", 1, localhostURL(server.URL), renderer)
	if err != nil {
		t.Fatalf("depth-limited FindLFSAttributes() error = %v", err)
	}
	if matched || path != "" {
		t.Fatalf("depth-limited matched=%t path=%q", matched, path)
	}
}

func TestFindLFSAttributesTreatsMissingRootAsNoMatch(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	matched, path, err := FindLFSAttributes(context.Background(), "octo", "missing", "token", 1, localhostURL(server.URL), output.New(output.Options{}))
	if err != nil {
		t.Fatalf("FindLFSAttributes() error = %v", err)
	}
	if matched || path != "" {
		t.Fatalf("matched=%t path=%q", matched, path)
	}
}

func TestRetryOperationRetriesAndReportsVerboseStatus(t *testing.T) {
	setRetryConfig(t, 2, time.Millisecond)
	var stderr bytes.Buffer
	renderer := output.New(output.Options{ErrOut: &stderr, Verbose: true})
	attempts := 0
	err := retryOperation(context.Background(), renderer, func() error {
		attempts++
		if attempts == 1 {
			return retryableNetworkError{}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retryOperation() error = %v", err)
	}
	if attempts != 2 || !strings.Contains(stderr.String(), "Attempt 1 failed, retrying") {
		t.Fatalf("attempts=%d stderr=%q", attempts, stderr.String())
	}
}

func TestRetryOperationStopsWhenContextIsCanceled(t *testing.T) {
	setRetryConfig(t, 3, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	err := retryOperation(ctx, output.New(output.Options{}), func() error {
		cancel()
		return retryableNetworkError{}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retryOperation() error = %v", err)
	}
}

func TestProxyFuncHonorsSchemeAndNoProxy(t *testing.T) {
	httpProxy, _ := url.Parse("http://http-proxy.example:8080")
	httpsProxy, _ := url.Parse("http://https-proxy.example:8443")
	proxy := proxyFunc(&proxyConfig{HTTPProxy: httpProxy.String(), HTTPSProxy: httpsProxy.String(), NoProxy: "internal.example,.local"})

	tests := []struct {
		rawURL string
		want   string
	}{
		{rawURL: "http://public.example", want: httpProxy.String()},
		{rawURL: "https://public.example", want: httpsProxy.String()},
		{rawURL: "https://internal.example", want: ""},
		{rawURL: "https://api.local", want: ""},
	}
	for _, test := range tests {
		request, _ := http.NewRequest(http.MethodGet, test.rawURL, nil)
		got, err := proxy(request)
		if err != nil {
			t.Fatalf("proxy(%q) error = %v", test.rawURL, err)
		}
		gotURL := ""
		if got != nil {
			gotURL = got.String()
		}
		if gotURL != test.want {
			t.Errorf("proxy(%q) = %q, want %q", test.rawURL, gotURL, test.want)
		}
	}
}

func TestNewGitHubClientValidatesConfiguration(t *testing.T) {
	if _, err := newGitHubClient("", ""); err == nil {
		t.Fatal("newGitHubClient() accepted an empty token")
	}
	if _, err := newGitHubClient("token", "https://example.com/path"); err == nil {
		t.Fatal("newGitHubClient() accepted an invalid enterprise URL")
	}
	if _, err := newGitHubClient("token", ""); err != nil {
		t.Fatalf("newGitHubClient() error = %v", err)
	}
}

func TestRetryableErrorRecognizesNetworkErrors(t *testing.T) {
	var networkError net.Error = retryableNetworkError{}
	if !retryableError(networkError) {
		t.Fatal("network error must be retried")
	}
}

func setRetryConfig(t *testing.T, attempts int, delay time.Duration) {
	t.Helper()
	viper.Set("GHMLFS_RETRY_MAX", attempts)
	viper.Set("GHMLFS_RETRY_DELAY", delay.String())
	t.Cleanup(func() {
		viper.Set("GHMLFS_RETRY_MAX", nil)
		viper.Set("GHMLFS_RETRY_DELAY", nil)
	})
}

func localhostURL(rawURL string) string {
	return strings.Replace(rawURL, "127.0.0.1", "localhost", 1)
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}
