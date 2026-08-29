package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
