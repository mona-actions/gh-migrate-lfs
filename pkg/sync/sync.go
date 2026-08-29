package sync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/mona-actions/gh-migrate-lfs/internal/manifest"
	"github.com/mona-actions/gh-migrate-lfs/internal/worker"
	"github.com/mona-actions/gh-migrate-lfs/pkg/lfs"
)

type Config struct {
	InputFile      string
	WorkDir        string
	TargetOrg      string
	TargetHostname string
	Token          string
	Workers        int
	BatchSize      int
	UploadParallel int
	RetryMax       int
	RetryDelay     time.Duration
	CheckHashes    bool
	DryRun         bool
	FinalCheck     bool
	StateRoot      string
}

type repositoryConfig struct {
	Name           string
	Path           string
	TargetOrg      string
	TargetHostname string
	Token          string
	BatchSize      int
	Parallel       int
	RetryMax       int
	RetryDelay     time.Duration
	CheckHashes    bool
	DryRun         bool
	FinalCheck     bool
	Reporter       lfs.IssueReporter
}

var progressMu sync.Mutex

func Run(ctx context.Context, cfg Config) error {
	repositories, err := manifest.Load(cfg.InputFile)
	if err != nil {
		return err
	}
	reporter, err := newRunReporter(cfg.StateRoot, cfg.TargetHostname, cfg.TargetOrg, cfg.DryRun)
	if err != nil {
		return err
	}

	jobChannel := make(chan manifest.Repository, len(repositories))
	for _, repository := range repositories {
		jobChannel <- repository
	}
	close(jobChannel)

	stats := worker.NewStats()
	err = worker.Run(ctx, jobChannel, max(cfg.Workers, 1), stats, func(repository manifest.Repository) error {
		startedAt := time.Now()
		repositoryStats, repositoryErr := syncRepository(ctx, repositoryConfig{
			Name:           repository.Name,
			Path:           filepath.Join(cfg.WorkDir, repository.Name),
			TargetOrg:      cfg.TargetOrg,
			TargetHostname: cfg.TargetHostname,
			Token:          cfg.Token,
			BatchSize:      cfg.BatchSize,
			Parallel:       cfg.UploadParallel,
			RetryMax:       cfg.RetryMax,
			RetryDelay:     cfg.RetryDelay,
			CheckHashes:    cfg.CheckHashes,
			DryRun:         cfg.DryRun,
			FinalCheck:     cfg.FinalCheck,
			Reporter:       reporter.forRepository(repository.Name),
		})
		result := repositoryResult{
			Repository: repository.Name,
			Duration:   time.Since(startedAt).Round(time.Millisecond).String(),
			Stats:      repositoryStats,
			Complete:   repositoryErr == nil,
		}
		if repositoryErr != nil {
			result.Error = repositoryErr.Error()
		}
		reporter.record(result)
		return repositoryErr
	})
	stats.PrintSummary(cfg.WorkDir)
	summary, reportErr := reporter.finish(cfg.TargetHostname, cfg.TargetOrg)
	printRunSummary(summary, reporter.stateDir)
	if err := errors.Join(err, reportErr); err != nil {
		return err
	}

	fmt.Println("\nSync completed successfully!")
	return nil
}

func syncRepository(ctx context.Context, cfg repositoryConfig) (lfs.Stats, error) {
	if err := ctx.Err(); err != nil {
		reportIssue(cfg.Reporter, "context", err)
		return lfs.Stats{}, err
	}

	endpoint, err := lfs.EndpointURL(cfg.TargetHostname, cfg.TargetOrg, cfg.Name)
	if err != nil {
		reportIssue(cfg.Reporter, "configuration", err)
		return lfs.Stats{}, fmt.Errorf("%s: %w", cfg.Name, err)
	}
	uploader, err := lfs.NewUploader(lfs.Config{
		Endpoint:   endpoint,
		Token:      cfg.Token,
		BatchSize:  cfg.BatchSize,
		Parallel:   cfg.Parallel,
		RetryMax:   cfg.RetryMax,
		RetryDelay: cfg.RetryDelay,
		Reporter:   cfg.Reporter,
	})
	if err != nil {
		reportIssue(cfg.Reporter, "configuration", err)
		return lfs.Stats{}, fmt.Errorf("%s: configure uploader: %w", cfg.Name, err)
	}

	var repositoryStats lfs.Stats
	var firstOperationError error
	failedOperations := 0
	lastProgress := time.Now()
	printProgress("%s: scanning and syncing local LFS objects\n", cfg.Name)
	totalObjects, err := lfs.WalkObjectBatches(ctx, cfg.Path, cfg.BatchSize, func(objects []lfs.Object) error {
		repositoryStats.Objects += len(objects)
		objectsToUpload := objects
		if cfg.CheckHashes {
			verified, verifyErr := lfs.VerifyObjects(ctx, objects, cfg.Parallel, cfg.Reporter)
			objectsToUpload = verified
			if verifyErr != nil {
				failedOperations++
				if firstOperationError == nil {
					firstOperationError = fmt.Errorf("local object verification failed: %w", verifyErr)
				}
			}
		}
		if len(objectsToUpload) == 0 {
			return nil
		}

		batchStats, uploadErr := uploader.Upload(ctx, objectsToUpload, cfg.DryRun)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		batchStats.Objects = 0
		if !cfg.DryRun && cfg.FinalCheck {
			reconcileStats, reconcileErr := uploader.Reconcile(ctx, objectsToUpload)
			batchStats.RemotePresent = reconcileStats.RemotePresent
			batchStats.RemoteMissing = reconcileStats.RemoteMissing
			batchStats.RemoteErrors = reconcileStats.RemoteErrors
			if reconcileErr != nil {
				failedOperations++
				if firstOperationError == nil {
					firstOperationError = fmt.Errorf("final reconciliation failed: %w", reconcileErr)
				}
			}
		}
		repositoryStats.Add(batchStats)
		if uploadErr != nil {
			failedOperations++
			if firstOperationError == nil {
				firstOperationError = fmt.Errorf("direct LFS upload failed: %w", uploadErr)
			}
		}
		if time.Since(lastProgress) >= 5*time.Second {
			printProgress("%s: processed %d objects (%d uploaded, %d already present, %d failures)\n",
				cfg.Name,
				repositoryStats.Objects,
				repositoryStats.Uploaded,
				repositoryStats.AlreadyPresent,
				repositoryStats.UploadFailures+repositoryStats.ServerErrors+repositoryStats.BatchFailures+repositoryStats.Unexpected,
			)
			lastProgress = time.Now()
		}
		return nil
	})
	if err != nil {
		stage := "local-scan"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			stage = "context"
		}
		reportIssue(cfg.Reporter, stage, err)
		return repositoryStats, fmt.Errorf("%s: %w", cfg.Name, err)
	}
	if totalObjects == 0 {
		printProgress("%s: no local LFS objects\n", cfg.Name)
		return repositoryStats, nil
	}

	if cfg.DryRun {
		printProgress("%s: dry run: %d would upload, %d already present, %d server errors, %d unexpected replies\n",
			cfg.Name, repositoryStats.WouldUpload, repositoryStats.AlreadyPresent, repositoryStats.ServerErrors, repositoryStats.Unexpected)
	} else {
		printProgress("%s: %d uploaded, %d already present, %d upload failures, %d server errors\n",
			cfg.Name, repositoryStats.Uploaded, repositoryStats.AlreadyPresent, repositoryStats.UploadFailures, repositoryStats.ServerErrors)
	}
	if firstOperationError != nil {
		return repositoryStats, fmt.Errorf("%d operations failed; first failure: %w", failedOperations, firstOperationError)
	}
	return repositoryStats, nil
}

func printProgress(format string, args ...any) {
	progressMu.Lock()
	fmt.Printf(format, args...)
	progressMu.Unlock()
}

func reportIssue(reporter lfs.IssueReporter, stage string, err error) {
	if reporter != nil {
		reporter.ReportIssue(lfs.Issue{Stage: stage, Message: err.Error()})
	}
}

func printRunSummary(summary runSummary, stateDir string) {
	fmt.Println("\nLFS object summary:")
	fmt.Printf("local objects:       %d\n", summary.Stats.Objects)
	if summary.DryRun {
		fmt.Printf("would upload:        %d\n", summary.Stats.WouldUpload)
	} else {
		fmt.Printf("uploaded:            %d\n", summary.Stats.Uploaded)
	}
	fmt.Printf("already present:     %d\n", summary.Stats.AlreadyPresent)
	fmt.Printf("server errors:       %d\n", summary.Stats.ServerErrors)
	fmt.Printf("upload failures:     %d\n", summary.Stats.UploadFailures)
	fmt.Printf("batch failures:      %d\n", summary.Stats.BatchFailures)
	fmt.Printf("unexpected replies:  %d\n", summary.Stats.Unexpected)
	if !summary.DryRun {
		fmt.Printf("remote present:      %d\n", summary.Stats.RemotePresent)
		fmt.Printf("remote missing:      %d\n", summary.Stats.RemoteMissing)
		fmt.Printf("remote errors:       %d\n", summary.Stats.RemoteErrors)
		fmt.Printf("current errors:      %s\n", filepath.Join(stateDir, "errors-current.tsv"))
		fmt.Printf("error history:       %s\n", filepath.Join(stateDir, "errors-history.tsv"))
		fmt.Printf("last run:            %s\n", filepath.Join(stateDir, "last-run.json"))
	}
}
