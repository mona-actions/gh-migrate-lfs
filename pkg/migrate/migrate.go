package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	exportpkg "github.com/mona-actions/gh-migrate-lfs/pkg/export"
	"github.com/mona-actions/gh-migrate-lfs/pkg/pull"
	syncpkg "github.com/mona-actions/gh-migrate-lfs/pkg/sync"
)

const (
	maxBatchSize      = 10000
	maxUploadParallel = 512
)

type Config struct {
	Manifest           string
	SourceOrganization string
	SourceHostname     string
	SourceToken        string
	TargetOrganization string
	TargetHostname     string
	TargetToken        string
	WorkDir            string
	StateRoot          string
	SearchDepth        int
	Workers            int
	BatchSize          int
	UploadParallel     int
	RetryMax           int
	RetryDelay         time.Duration
	CheckHashes        bool
	DryRun             bool
	FinalCheck         bool
}

type phaseRunner struct {
	export func(context.Context, exportpkg.Config) error
	pull   func(context.Context, pull.Config) error
	sync   func(context.Context, syncpkg.Config) error
}

var defaultPhases = phaseRunner{
	export: exportpkg.Run,
	pull:   pull.Run,
	sync:   syncpkg.Run,
}

func Run(ctx context.Context, cfg Config) error {
	return run(ctx, cfg, defaultPhases)
}

func run(ctx context.Context, cfg Config, phases phaseRunner) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return fmt.Errorf("create migration work directory: %w", err)
	}

	manifestPath := cfg.Manifest
	if manifestPath == "" {
		manifestPath = filepath.Join(cfg.WorkDir, cfg.SourceOrganization+"_lfs.csv")
		fmt.Println("\n=== Export LFS repositories ===")
		if err := phases.export(ctx, exportpkg.Config{
			Organization: cfg.SourceOrganization,
			Token:        cfg.SourceToken,
			Hostname:     cfg.SourceHostname,
			Depth:        cfg.SearchDepth,
			OutputFile:   manifestPath,
		}); err != nil {
			return fmt.Errorf("export phase: %w", err)
		}
		fmt.Printf("Manifest: %s\n", manifestPath)
	}

	fmt.Println("\n=== Pull source LFS objects ===")
	if err := phases.pull(ctx, pull.Config{
		InputFile: manifestPath,
		Token:     cfg.SourceToken,
		WorkDir:   cfg.WorkDir,
		Workers:   cfg.Workers,
	}); err != nil {
		return fmt.Errorf("pull phase: %w", err)
	}

	fmt.Println("\n=== Sync destination LFS objects ===")
	if err := phases.sync(ctx, syncpkg.Config{
		InputFile:      manifestPath,
		WorkDir:        cfg.WorkDir,
		TargetOrg:      cfg.TargetOrganization,
		TargetHostname: cfg.TargetHostname,
		Token:          cfg.TargetToken,
		Workers:        cfg.Workers,
		BatchSize:      cfg.BatchSize,
		UploadParallel: cfg.UploadParallel,
		RetryMax:       cfg.RetryMax,
		RetryDelay:     cfg.RetryDelay,
		CheckHashes:    cfg.CheckHashes,
		DryRun:         cfg.DryRun,
		FinalCheck:     cfg.FinalCheck,
		StateRoot:      cfg.StateRoot,
	}); err != nil {
		return fmt.Errorf("sync phase: %w", err)
	}

	fmt.Println("\nMigration completed successfully!")
	return nil
}

func validateConfig(cfg Config) error {
	var validationErrors []error
	if cfg.Manifest == "" && cfg.SourceOrganization == "" {
		validationErrors = append(validationErrors, errors.New("source organization is required when no manifest is provided"))
	}
	if cfg.SourceToken == "" {
		validationErrors = append(validationErrors, errors.New("source token is required"))
	}
	if cfg.TargetOrganization == "" {
		validationErrors = append(validationErrors, errors.New("target organization is required"))
	}
	if cfg.TargetToken == "" {
		validationErrors = append(validationErrors, errors.New("target token is required"))
	}
	if cfg.WorkDir == "" {
		validationErrors = append(validationErrors, errors.New("work directory is required"))
	}
	if cfg.BatchSize != 0 && (cfg.BatchSize < 1 || cfg.BatchSize > maxBatchSize) {
		validationErrors = append(validationErrors, fmt.Errorf("batch size must be between 1 and %d", maxBatchSize))
	}
	if cfg.UploadParallel != 0 && (cfg.UploadParallel < 1 || cfg.UploadParallel > maxUploadParallel) {
		validationErrors = append(validationErrors, fmt.Errorf("upload parallelism must be between 1 and %d", maxUploadParallel))
	}
	return errors.Join(validationErrors...)
}
