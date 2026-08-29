package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/mona-actions/gh-migrate-lfs/pkg/lfs"
)

type repositoryResult struct {
	Repository string    `json:"repository"`
	Duration   string    `json:"duration"`
	Stats      lfs.Stats `json:"stats"`
	Complete   bool      `json:"complete"`
	Error      string    `json:"error,omitempty"`
}

type runSummary struct {
	Timestamp          time.Time          `json:"timestamp"`
	Duration           string             `json:"duration"`
	TargetHostname     string             `json:"target_hostname"`
	TargetOrganization string             `json:"target_organization"`
	Repositories       int                `json:"repositories"`
	Succeeded          int                `json:"succeeded"`
	Failed             int                `json:"failed"`
	Issues             int                `json:"issues"`
	DryRun             bool               `json:"dry_run"`
	Complete           bool               `json:"complete"`
	Stats              lfs.Stats          `json:"stats"`
	Results            []repositoryResult `json:"results"`
}

type runReporter struct {
	mu        sync.Mutex
	startedAt time.Time
	stateDir  string
	dryRun    bool
	current   *os.File
	history   *os.File
	lock      *flock.Flock
	lockPath  string
	issues    int
	results   []repositoryResult
	writeErr  error
}

func newRunReporter(stateRoot, hostname, organization string, dryRun bool) (*runReporter, error) {
	if strings.TrimSpace(stateRoot) == "" {
		stateRoot = ".lfs-migrate"
	}
	reporter := &runReporter{
		startedAt: time.Now().UTC(),
		dryRun:    dryRun,
	}
	target, err := lfs.EndpointURL(hostname, organization, "_state")
	if err != nil {
		return nil, fmt.Errorf("identify sync destination: %w", err)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(target)))
	reporter.stateDir = filepath.Join(stateRoot, "targets", hex.EncodeToString(sum[:]))
	if dryRun {
		return reporter, nil
	}
	if err := os.MkdirAll(reporter.stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create sync state directory: %w", err)
	}
	reporter.lockPath = filepath.Join(reporter.stateDir, "sync.lock")
	lock := flock.New(reporter.lockPath)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire sync lock: %w", err)
	}
	if !locked {
		lock.Close()
		return nil, fmt.Errorf("another sync is using this destination: %s", reporter.lockPath)
	}
	reporter.lock = lock

	current, err := os.OpenFile(filepath.Join(reporter.stateDir, "errors-current.tsv"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		reporter.releaseLock()
		return nil, fmt.Errorf("open current error log: %w", err)
	}
	history, err := os.OpenFile(filepath.Join(reporter.stateDir, "errors-history.tsv"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		current.Close()
		reporter.releaseLock()
		return nil, fmt.Errorf("open error history: %w", err)
	}
	reporter.current = current
	reporter.history = history
	if err := current.Chmod(0o600); err != nil {
		current.Close()
		history.Close()
		reporter.releaseLock()
		return nil, fmt.Errorf("set current error log permissions: %w", err)
	}
	if err := history.Chmod(0o600); err != nil {
		current.Close()
		history.Close()
		reporter.releaseLock()
		return nil, fmt.Errorf("set error history permissions: %w", err)
	}
	return reporter, nil
}

func (reporter *runReporter) forRepository(repository string) lfs.IssueReporter {
	return lfs.IssueReporterFunc(func(issue lfs.Issue) {
		reporter.report(repository, issue)
	})
}

func (reporter *runReporter) report(repository string, issue lfs.Issue) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.issues++
	if reporter.dryRun {
		return
	}
	line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\n",
		time.Now().UTC().Format(time.RFC3339),
		cleanField(repository),
		cleanField(issue.OID),
		cleanField(issue.Stage),
		cleanField(issue.Message),
	)
	if _, err := reporter.current.WriteString(line); err != nil {
		reporter.writeErr = errors.Join(reporter.writeErr, fmt.Errorf("write current error log: %w", err))
	}
	if _, err := reporter.history.WriteString(line); err != nil {
		reporter.writeErr = errors.Join(reporter.writeErr, fmt.Errorf("write error history: %w", err))
	}
}

func (reporter *runReporter) record(result repositoryResult) {
	reporter.mu.Lock()
	reporter.results = append(reporter.results, result)
	reporter.mu.Unlock()
}

func (reporter *runReporter) finish(hostname, organization string) (runSummary, error) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()

	summary := runSummary{
		Timestamp:          reporter.startedAt,
		Duration:           time.Since(reporter.startedAt).Round(time.Millisecond).String(),
		TargetHostname:     hostname,
		TargetOrganization: organization,
		Repositories:       len(reporter.results),
		Issues:             reporter.issues,
		DryRun:             reporter.dryRun,
		Results:            append([]repositoryResult(nil), reporter.results...),
	}
	sort.Slice(summary.Results, func(i, j int) bool {
		return summary.Results[i].Repository < summary.Results[j].Repository
	})
	for _, result := range summary.Results {
		summary.Stats.Add(result.Stats)
		if result.Complete {
			summary.Succeeded++
		} else {
			summary.Failed++
		}
	}
	summary.Complete = summary.Failed == 0 && summary.Issues == 0

	var finishErrors []error
	if !reporter.dryRun {
		finishErrors = append(finishErrors, reporter.writeErr)
		if err := writeJSONAtomic(filepath.Join(reporter.stateDir, "last-run.json"), summary); err != nil {
			finishErrors = append(finishErrors, err)
		}
		if err := reporter.current.Close(); err != nil {
			finishErrors = append(finishErrors, fmt.Errorf("close current error log: %w", err))
		}
		if err := reporter.history.Close(); err != nil {
			finishErrors = append(finishErrors, fmt.Errorf("close error history: %w", err))
		}
		if err := reporter.releaseLock(); err != nil {
			finishErrors = append(finishErrors, err)
		}
	}
	return summary, errors.Join(finishErrors...)
}

func (reporter *runReporter) releaseLock() error {
	if reporter.lock == nil {
		return nil
	}
	lock := reporter.lock
	reporter.lock = nil
	if err := lock.Unlock(); err != nil {
		lock.Close()
		return fmt.Errorf("release sync lock: %w", err)
	}
	if err := lock.Close(); err != nil {
		return fmt.Errorf("close sync lock: %w", err)
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run summary: %w", err)
	}
	data = append(data, '\n')
	temporary := path + ".tmp"
	defer os.Remove(temporary)
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write run summary: %w", err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return fmt.Errorf("set run summary permissions: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("remove previous run summary: %w", removeErr)
		}
		if renameErr := os.Rename(temporary, path); renameErr != nil {
			return fmt.Errorf("replace run summary: %w", renameErr)
		}
	}
	return nil
}

func cleanField(value string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(value)
}
