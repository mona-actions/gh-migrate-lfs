package sync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/mona-actions/gh-migrate-lfs/internal/manifest"
	"github.com/mona-actions/gh-migrate-lfs/internal/output"
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
	Output         *output.Renderer
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
	Progress       func(lfs.Stats)
}

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

	workers := max(cfg.Workers, 1)
	cfg.Output.Line("Syncing Git LFS objects to %s with %d workers", cfg.TargetOrg, workers)
	stats := worker.NewStats()
	var completed atomic.Int32
	var active atomic.Int32
	err = worker.Run(ctx, jobChannel, workers, stats, func(repository manifest.Repository) error {
		active.Add(1)
		updateStatus := func(repositoryStats lfs.Stats) {
			cfg.Output.Status(
				"Repositories %d/%d complete | %d active | %s: %d objects, %d uploaded, %d present, %d failed",
				completed.Load(),
				len(repositories),
				active.Load(),
				repository.Name,
				repositoryStats.Objects,
				repositoryStats.Uploaded,
				repositoryStats.AlreadyPresent,
				operationFailures(repositoryStats),
			)
		}
		updateStatus(lfs.Stats{})
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
			Progress:       updateStatus,
		})
		active.Add(-1)
		completed.Add(1)
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
		if cfg.DryRun {
			cfg.Output.Line(
				"%s %s  %d would upload | %d present | %d failed | %s",
				repositoryStatus(repositoryErr),
				repository.Name,
				repositoryStats.WouldUpload,
				repositoryStats.AlreadyPresent,
				operationFailures(repositoryStats),
				result.Duration,
			)
		} else {
			cfg.Output.Line(
				"%s %s  %d uploaded | %d present | %d failed | %s",
				repositoryStatus(repositoryErr),
				repository.Name,
				repositoryStats.Uploaded,
				repositoryStats.AlreadyPresent,
				operationFailures(repositoryStats),
				result.Duration,
			)
		}
		if repositoryErr != nil {
			cfg.Output.Error("  %v", repositoryErr)
		}
		cfg.Output.Status("Repositories %d/%d complete | %d active", completed.Load(), len(repositories), active.Load())
		return repositoryErr
	})
	cfg.Output.FinishStatus("Repositories %d/%d complete", completed.Load(), len(repositories))
	summary, reportErr := reporter.finish(cfg.TargetHostname, cfg.TargetOrg)
	cfg.Output.Record("sync", summary)
	printRunSummary(cfg.Output, summary, reporter.stateDir)
	return errors.Join(err, reportErr, cfg.Output.Err())
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
			if cfg.Progress != nil {
				cfg.Progress(repositoryStats)
			}
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
		return repositoryStats, nil
	}
	if cfg.Progress != nil {
		cfg.Progress(repositoryStats)
	}
	if firstOperationError != nil {
		return repositoryStats, fmt.Errorf("%d operations failed; first failure: %w", failedOperations, firstOperationError)
	}
	return repositoryStats, nil
}

func reportIssue(reporter lfs.IssueReporter, stage string, err error) {
	if reporter != nil {
		reporter.ReportIssue(lfs.Issue{Stage: stage, Message: err.Error()})
	}
}

func printRunSummary(renderer *output.Renderer, summary runSummary, stateDir string) {
	status := "complete"
	if !summary.Complete {
		status = "incomplete"
	}
	renderer.Result("\nSync %s\n\n", status)
	renderer.Result("Repositories succeeded: %d\n", summary.Succeeded)
	renderer.Result("Repositories failed:    %d\n", summary.Failed)
	renderer.Result("Issues:                 %d\n", summary.Issues)
	renderer.Result("Local objects:          %d\n", summary.Stats.Objects)
	if summary.DryRun {
		renderer.Result("Would upload:           %d\n", summary.Stats.WouldUpload)
	} else {
		renderer.Result("Uploaded:               %d\n", summary.Stats.Uploaded)
	}
	renderer.Result("Already present:        %d\n", summary.Stats.AlreadyPresent)
	renderer.Result("Server errors:          %d\n", summary.Stats.ServerErrors)
	renderer.Result("Upload failures:        %d\n", summary.Stats.UploadFailures)
	renderer.Result("Batch failures:         %d\n", summary.Stats.BatchFailures)
	renderer.Result("Unexpected replies:     %d\n", summary.Stats.Unexpected)
	if !summary.DryRun {
		renderer.Result("Remote present:         %d\n", summary.Stats.RemotePresent)
		renderer.Result("Remote missing:         %d\n", summary.Stats.RemoteMissing)
		renderer.Result("Remote errors:          %d\n", summary.Stats.RemoteErrors)
		renderer.Result("Duration:               %s\n", summary.Duration)
		renderer.Result("Current errors:         %s\n", filepath.Join(stateDir, "errors-current.tsv"))
		renderer.Result("Error history:          %s\n", filepath.Join(stateDir, "errors-history.tsv"))
		renderer.Result("Report:                 %s\n", filepath.Join(stateDir, "last-run.json"))
	}
}

func operationFailures(stats lfs.Stats) int {
	return stats.UploadFailures + stats.ServerErrors + stats.BatchFailures + stats.Unexpected + stats.RemoteMissing + stats.RemoteErrors
}

func repositoryStatus(err error) string {
	if err != nil {
		return "Failed"
	}
	return "Complete"
}
