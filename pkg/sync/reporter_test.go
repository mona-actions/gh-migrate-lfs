package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mona-actions/gh-migrate-lfs/pkg/lfs"
)

func TestRunReporterPersistsDetailedState(t *testing.T) {
	t.Parallel()

	stateRoot := filepath.Join(t.TempDir(), "state")
	reporter, err := newRunReporter(stateRoot, "github.example.com", "target", false)
	if err != nil {
		t.Fatal(err)
	}
	reporter.forRepository("repo").ReportIssue(lfs.Issue{OID: strings.Repeat("a", 64), Stage: "upload", Message: "HTTP 500\nretry exhausted"})
	reporter.record(repositoryResult{
		Repository: "repo",
		Duration:   "1s",
		Stats: lfs.Stats{
			Objects:        3,
			Uploaded:       1,
			AlreadyPresent: 1,
			UploadFailures: 1,
		},
		Complete: false,
		Error:    "upload failed",
	})
	summary, err := reporter.finish("github.example.com", "target")
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if summary.Complete || summary.Issues != 1 || summary.Stats.UploadFailures != 1 {
		t.Fatalf("Finish() summary = %#v", summary)
	}

	current := readReportFile(t, filepath.Join(reporter.stateDir, "errors-current.tsv"))
	history := readReportFile(t, filepath.Join(reporter.stateDir, "errors-history.tsv"))
	if !strings.Contains(current, "repo\t"+strings.Repeat("a", 64)+"\tupload\tHTTP 500 retry exhausted") {
		t.Fatalf("current error log = %q", current)
	}
	if history != current {
		t.Fatalf("history = %q, current = %q", history, current)
	}

	var persisted runSummary
	data := readReportFile(t, filepath.Join(reporter.stateDir, "last-run.json"))
	if err := json.Unmarshal([]byte(data), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Stats.Objects != 3 || len(persisted.Results) != 1 || persisted.Results[0].Repository != "repo" {
		t.Fatalf("persisted summary = %#v", persisted)
	}

	second, err := newRunReporter(stateRoot, "github.example.com", "target", false)
	if err != nil {
		t.Fatal(err)
	}
	second.record(repositoryResult{Repository: "repo", Complete: true})
	if _, err := second.finish("github.example.com", "target"); err != nil {
		t.Fatal(err)
	}
	if current := readReportFile(t, filepath.Join(second.stateDir, "errors-current.tsv")); current != "" {
		t.Fatalf("second current error log = %q", current)
	}
	if got := readReportFile(t, filepath.Join(second.stateDir, "errors-history.tsv")); got != history {
		t.Fatalf("second history = %q, want %q", got, history)
	}
}

func TestRunReporterDryRunDoesNotWriteState(t *testing.T) {
	t.Parallel()

	stateRoot := filepath.Join(t.TempDir(), "state")
	reporter, err := newRunReporter(stateRoot, "", "target", true)
	if err != nil {
		t.Fatal(err)
	}
	reporter.forRepository("repo").ReportIssue(lfs.Issue{Stage: "server", Message: "observed"})
	reporter.record(repositoryResult{Repository: "repo", Complete: false})
	summary, err := reporter.finish("", "target")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Issues != 1 || summary.Complete {
		t.Fatalf("Finish() summary = %#v", summary)
	}
	if _, err := os.Stat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("dry run state path exists or returned unexpected error: %v", err)
	}
}

func TestRunReporterLocksDestination(t *testing.T) {
	t.Parallel()

	stateRoot := filepath.Join(t.TempDir(), "state")
	first, err := newRunReporter(stateRoot, "github.example.com", "target", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newRunReporter(stateRoot, "https://github.example.com/api/v3", "target", false); err == nil || !strings.Contains(err.Error(), "another sync") {
		t.Fatalf("concurrent newRunReporter() error = %v", err)
	}
	first.record(repositoryResult{Repository: "repo", Complete: true})
	if _, err := first.finish("github.example.com", "target"); err != nil {
		t.Fatal(err)
	}

	second, err := newRunReporter(stateRoot, "github.example.com", "target", false)
	if err != nil {
		t.Fatalf("newRunReporter() after finish error = %v", err)
	}
	second.record(repositoryResult{Repository: "repo", Complete: true})
	if _, err := second.finish("github.example.com", "target"); err != nil {
		t.Fatal(err)
	}
}

func TestRunReporterRecoversAfterProcessExit(t *testing.T) {
	if os.Getenv("GHMLFS_LOCK_HELPER") == "1" {
		if _, err := newRunReporter(os.Getenv("GHMLFS_LOCK_STATE"), "github.example.com", "target", false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}

	stateRoot := filepath.Join(t.TempDir(), "state")
	command := exec.Command(os.Args[0], "-test.run=^TestRunReporterRecoversAfterProcessExit$")
	command.Env = append(os.Environ(), "GHMLFS_LOCK_HELPER=1", "GHMLFS_LOCK_STATE="+stateRoot)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock helper failed: %v: %s", err, output)
	}
	locks, err := filepath.Glob(filepath.Join(stateRoot, "targets", "*", "sync.lock"))
	if err != nil || len(locks) != 1 {
		t.Fatalf("stale lock files = %v, error = %v", locks, err)
	}

	reporter, err := newRunReporter(stateRoot, "github.example.com", "target", false)
	if err != nil {
		t.Fatalf("newRunReporter() after process exit error = %v", err)
	}
	reporter.record(repositoryResult{Repository: "repo", Complete: true})
	if _, err := reporter.finish("github.example.com", "target"); err != nil {
		t.Fatal(err)
	}
}

func readReportFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
