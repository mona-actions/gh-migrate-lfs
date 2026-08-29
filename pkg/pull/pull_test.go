package pull

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mona-actions/gh-migrate-lfs/internal/output"
)

func TestRunPrintsEveryRepositoryResult(t *testing.T) {
	t.Parallel()

	manifestPath := filepath.Join(t.TempDir(), "repositories.csv")
	manifest := "Repository,GitAttributesPaths,CloneURL\n" +
		"alpha,.gitattributes,https://github.com/source/alpha.git\n" +
		"beta,.gitattributes,https://github.com/source/beta.git\n" +
		"gamma,.gitattributes,https://github.com/source/gamma.git\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	renderer := output.New(output.Options{Out: &stdout, ErrOut: &stderr})
	wantErr := errors.New("fetch failed")
	err := run(context.Background(), Config{
		InputFile: manifestPath,
		Token:     "token",
		WorkDir:   "work",
		Workers:   3,
		Output:    renderer,
	}, func(_ context.Context, name, _, _ string, _ []string) error {
		if name == "beta" {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("run() error = %v", err)
	}

	transcript := stderr.String()
	for _, result := range []string{"Complete alpha  ", "Failed beta  ", "Complete gamma  "} {
		if count := strings.Count(transcript, result); count != 1 {
			t.Fatalf("%q count = %d in %q", result, count, transcript)
		}
	}
	if !strings.Contains(transcript, "Repositories 3/3 complete") {
		t.Fatalf("final progress missing from %q", transcript)
	}
	if !strings.Contains(stdout.String(), "Repositories succeeded: 2\nRepositories failed:    1\n") {
		t.Fatalf("summary = %q", stdout.String())
	}
}

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
