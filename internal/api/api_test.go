package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/go-github/v90/github"
)

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
