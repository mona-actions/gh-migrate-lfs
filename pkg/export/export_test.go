package export

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteToCSV(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "repos.csv")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	repositories := []repositoryInfo{{Name: "repo", GitAttributesPath: ".gitattributes", CloneURL: "https://github.com/octo/repo.git"}}
	if err := writeToCSV(path, repositories); err != nil {
		t.Fatalf("writeToCSV() error = %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1][0] != "repo" || records[1][2] != repositories[0].CloneURL {
		t.Fatalf("CSV records = %#v", records)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("CSV permissions = %o", info.Mode().Perm())
		}
	}
}

func TestExportRejectsInvalidOrganization(t *testing.T) {
	t.Parallel()

	if err := Run(context.Background(), Config{Organization: "../outside"}); err == nil {
		t.Fatal("Run() returned no error")
	}
}
