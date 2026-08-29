package export

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mona-actions/gh-migrate-lfs/internal/output"
	"github.com/spf13/viper"
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

func TestRunWritesHealthyRepositoriesAfterInspectionFailure(t *testing.T) {
	viper.Set("GHMLFS_RETRY_MAX", 1)
	t.Cleanup(func() { viper.Set("GHMLFS_RETRY_MAX", nil) })

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v3/orgs/octo/repos":
			fmt.Fprint(response, `[{"name":"lfs-repo"},{"name":"plain-repo"},{"name":"failed-repo"}]`)
		case "/api/v3/repos/octo/lfs-repo/contents/":
			fmt.Fprint(response, `[{"type":"file","name":".gitattributes","path":".gitattributes"}]`)
		case "/api/v3/repos/octo/lfs-repo/contents/.gitattributes":
			fmt.Fprint(response, `{"type":"file","name":".gitattributes","path":".gitattributes","encoding":"base64","content":"Ki5iaW4gZmlsdGVyPWxmcyAtdGV4dAo="}`)
		case "/api/v3/repos/octo/plain-repo/contents/":
			fmt.Fprint(response, `[]`)
		case "/api/v3/repos/octo/failed-repo/contents/":
			http.Error(response, `{"message":"server failure"}`, http.StatusInternalServerError)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	manifestPath := filepath.Join(t.TempDir(), "repos.csv")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	renderer := output.New(output.Options{Out: &stdout, ErrOut: &stderr})
	err := Run(context.Background(), Config{
		Organization: "octo",
		Token:        "token",
		Hostname:     strings.Replace(server.URL, "127.0.0.1", "localhost", 1),
		Depth:        1,
		OutputFile:   manifestPath,
		Output:       renderer,
	})
	if err == nil || !strings.Contains(err.Error(), "failed to inspect 1 repositories") {
		t.Fatalf("Run() error = %v", err)
	}

	file, err := os.Open(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, readErr := csv.NewReader(file).ReadAll()
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("readErr=%v closeErr=%v", readErr, closeErr)
	}
	if len(records) != 2 || records[1][0] != "lfs-repo" || records[1][1] != ".gitattributes" {
		t.Fatalf("records = %#v", records)
	}
	if !strings.Contains(stderr.String(), "Found lfs-repo  .gitattributes") || !strings.Contains(stderr.String(), "Failed failed-repo:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Export incomplete") || !strings.Contains(stdout.String(), "Repositories using LFS: 1") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestWriteToCSVReportsCreateFailure(t *testing.T) {
	err := writeToCSV(filepath.Join(t.TempDir(), "missing", "repos.csv"), nil)
	if err == nil || !strings.Contains(err.Error(), "create output file") {
		t.Fatalf("writeToCSV() error = %v", err)
	}
}
