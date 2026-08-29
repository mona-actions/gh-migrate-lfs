package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mona-actions/gh-migrate-lfs/internal/output"
	"github.com/mona-actions/gh-migrate-lfs/pkg/lfs"
)

func TestSyncRepositoryContinuesPastCorruptObject(t *testing.T) {
	t.Parallel()

	repositoryPath := t.TempDir()
	validContent := []byte("valid object")
	validOID := writeSyncObject(t, repositoryPath, validContent, "")
	corruptOID := strings.Repeat("0", 64)
	writeSyncObject(t, repositoryPath, []byte("corrupt object"), corruptOID)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/target/repo.git/info/lfs/objects/batch" {
			http.NotFound(response, request)
			return
		}
		fmt.Fprintf(response, `{"objects":[{"oid":%q}]}`, validOID)
	}))
	defer server.Close()
	serverURL := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)

	var issues []lfs.Issue
	stats, err := syncRepository(context.Background(), repositoryConfig{
		Name:           "repo",
		Path:           repositoryPath,
		TargetOrg:      "target",
		TargetHostname: serverURL,
		BatchSize:      100,
		Parallel:       2,
		RetryMax:       1,
		CheckHashes:    true,
		DryRun:         true,
		Reporter: lfs.IssueReporterFunc(func(issue lfs.Issue) {
			issues = append(issues, issue)
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("syncRepository() error = %v", err)
	}
	if stats.Objects != 2 || stats.AlreadyPresent != 1 {
		t.Fatalf("syncRepository() stats = %#v", stats)
	}
	if len(issues) != 1 || issues[0].OID != corruptOID || issues[0].Stage != "local-hash" {
		t.Fatalf("syncRepository() issues = %#v", issues)
	}
}

func TestRunDryRunNegotiatesAndPrintsRepository(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	manifestPath := writeSyncManifest(t, workDir, "repo")
	oid := writeSyncObject(t, filepath.Join(workDir, "repo"), []byte("object"), "")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/target/repo.git/info/lfs/objects/batch" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/vnd.git-lfs+json")
		fmt.Fprintf(response, `{"objects":[{"oid":%q,"size":6}]}`, oid)
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	renderer := output.New(output.Options{Out: &stdout, ErrOut: &stderr})
	err := Run(context.Background(), Config{
		InputFile:      manifestPath,
		WorkDir:        workDir,
		TargetOrg:      "target",
		TargetHostname: strings.Replace(server.URL, "127.0.0.1", "localhost", 1),
		Token:          "token",
		Workers:        1,
		BatchSize:      100,
		UploadParallel: 1,
		RetryMax:       1,
		CheckHashes:    true,
		DryRun:         true,
		Output:         renderer,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "Complete repo  0 would upload | 1 present | 0 failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Sync complete") || !strings.Contains(stdout.String(), "Already present:        1") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunPersistsCompleteReportForEmptyRepository(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	manifestPath := writeSyncManifest(t, workDir, "empty")
	if err := os.MkdirAll(filepath.Join(workDir, "empty", "lfs", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(t.TempDir(), "state")
	var stdout bytes.Buffer
	renderer := output.New(output.Options{Out: &stdout})
	err := Run(context.Background(), Config{
		InputFile:      manifestPath,
		WorkDir:        workDir,
		TargetOrg:      "target",
		Token:          "token",
		Workers:        1,
		BatchSize:      100,
		UploadParallel: 1,
		RetryMax:       1,
		StateRoot:      stateRoot,
		Output:         renderer,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	reports, err := filepath.Glob(filepath.Join(stateRoot, "targets", "*", "last-run.json"))
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports=%v error=%v", reports, err)
	}
	var summary runSummary
	data, err := os.ReadFile(reports[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if !summary.Complete || summary.Succeeded != 1 || summary.Stats.Objects != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if !strings.Contains(stdout.String(), "Remote present:         0") || !strings.Contains(stdout.String(), "Report:") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunReportsMissingLocalRepository(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	manifestPath := writeSyncManifest(t, workDir, "missing")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	renderer := output.New(output.Options{Out: &stdout, ErrOut: &stderr})
	err := Run(context.Background(), Config{
		InputFile:      manifestPath,
		WorkDir:        workDir,
		TargetOrg:      "target",
		Token:          "token",
		Workers:        1,
		BatchSize:      100,
		UploadParallel: 1,
		RetryMax:       1,
		DryRun:         true,
		Output:         renderer,
	})
	if err == nil {
		t.Fatal("Run() returned no error")
	}
	if !strings.Contains(stderr.String(), "Failed missing") || !strings.Contains(stderr.String(), "LFS object store not found") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Sync incomplete") || !strings.Contains(stdout.String(), "Repositories failed:    1") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func writeSyncObject(t *testing.T, repositoryPath string, content []byte, oid string) string {
	t.Helper()
	if oid == "" {
		hash := sha256.Sum256(content)
		oid = hex.EncodeToString(hash[:])
	}
	path := filepath.Join(repositoryPath, "lfs", "objects", oid[:2], oid[2:4], oid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return oid
}

func writeSyncManifest(t *testing.T, directory, repository string) string {
	t.Helper()
	path := filepath.Join(directory, "repositories.csv")
	content := fmt.Sprintf("Repository,GitAttributesPaths,CloneURL\n%s,.gitattributes,https://github.com/source/%s.git\n", repository, repository)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
