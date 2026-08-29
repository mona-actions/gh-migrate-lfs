package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, "Repository,GitAttributesPaths,CloneURL\nrepo,a,https://github.com/o/repo.git\nrepo,b,https://github.com/o/repo.git\n")
	repositories, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(repositories) != 1 || repositories[0].Name != "repo" || repositories[0].CloneURL != "https://github.com/o/repo.git" {
		t.Fatalf("Load() repositories = %#v", repositories)
	}
}

func TestLoadAcceptsUTF8BOM(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, "\ufeffRepository,GitAttributesPaths,CloneURL\nrepo,a,https://github.com/o/repo.git\n")
	repositories, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(repositories) != 1 || repositories[0].Name != "repo" {
		t.Fatalf("Load() repositories = %#v", repositories)
	}
}

func TestLoadRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		message string
	}{
		{name: "header", content: "name,path,url\n", message: "invalid manifest header"},
		{name: "record", content: "Repository,GitAttributesPaths,CloneURL\nrepo,only-two\n", message: "read manifest record"},
		{name: "path traversal", content: "Repository,GitAttributesPaths,CloneURL\n../repo,a,url\n", message: "must be one path segment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(writeManifest(t, test.content))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}
}

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repositories.csv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
