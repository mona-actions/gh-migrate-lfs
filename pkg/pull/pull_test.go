package pull

import (
	"strings"
	"testing"
)

func TestGitAuthEnvironmentScopesCredentialsToHost(t *testing.T) {
	t.Parallel()

	env, err := gitAuthEnvironment("https://github.example.com/org/repo.git", "secret-token")
	if err != nil {
		t.Fatalf("gitAuthEnvironment() error = %v", err)
	}
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "secret-token") {
		t.Fatal("environment contains the raw token")
	}
	if !strings.Contains(joined, "GIT_CONFIG_KEY_0=http.https://github.example.com/.extraheader") {
		t.Fatalf("environment does not contain host-scoped header config: %s", joined)
	}
	if !strings.Contains(joined, "GIT_CONFIG_VALUE_0=Authorization: Basic ") {
		t.Fatal("environment does not contain an authorization header")
	}
}

func TestGitAuthEnvironmentRejectsUnsafeURLs(t *testing.T) {
	t.Parallel()

	for _, rawURL := range []string{
		"http://github.example.com/org/repo.git",
		"https://token@github.example.com/org/repo.git",
		"not-a-url",
	} {
		if _, err := gitAuthEnvironment(rawURL, "secret"); err == nil {
			t.Errorf("gitAuthEnvironment(%q) returned no error", rawURL)
		}
	}
}
