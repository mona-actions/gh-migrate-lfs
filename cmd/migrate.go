package cmd

import (
	"fmt"

	"github.com/mona-actions/gh-migrate-lfs/pkg/migrate"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Export, pull, and sync LFS objects end to end",
	Long:  "Discover source repositories, clone and fetch their LFS objects, then upload missing objects directly to the destination LFS Batch API.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := migrateConfigFromCommand(cmd)
		if err != nil {
			return err
		}
		if err := checkGitLFS(cmd); err != nil {
			return fmt.Errorf("git lfs command not found; install Git LFS from https://git-lfs.com: %w", err)
		}
		cfg.Output = newRenderer(cmd)
		runErr := migrate.Run(cmd.Context(), cfg)
		return finishCommand(cfg.Output, "migrate", runErr)
	},
}

func migrateConfigFromCommand(cmd *cobra.Command) (migrate.Config, error) {
	manifestPath := stringConfig(cmd, "file", "GHMLFS_FILE")
	sourceOrganization := stringConfig(cmd, "source-organization", "GHMLFS_SOURCE_ORGANIZATION")
	if cmd.Flags().Changed("file") && cmd.Flags().Changed("source-organization") {
		return migrate.Config{}, fmt.Errorf("--file and --source-organization cannot be used together")
	}
	if cmd.Flags().Changed("source-organization") {
		manifestPath = ""
	}

	cfg := migrate.Config{
		Manifest:           manifestPath,
		SourceOrganization: sourceOrganization,
		SourceHostname:     stringConfig(cmd, "source-hostname", "GHMLFS_SOURCE_HOSTNAME"),
		SourceToken:        stringConfig(cmd, "source-token", "GHMLFS_SOURCE_TOKEN"),
		TargetOrganization: stringConfig(cmd, "target-organization", "GHMLFS_TARGET_ORGANIZATION"),
		TargetHostname:     stringConfig(cmd, "target-hostname", "GHMLFS_TARGET_HOSTNAME"),
		TargetToken:        stringConfig(cmd, "target-token", "GHMLFS_TARGET_TOKEN"),
		WorkDir:            stringConfig(cmd, "work-dir", "GHMLFS_WORK_DIR"),
		StateRoot:          stringConfig(cmd, "state", "GHMLFS_STATE_DIR"),
		SearchDepth:        intConfig(cmd, "search-depth", "GHMLFS_SEARCH_DEPTH"),
		Workers:            intConfig(cmd, "workers", "GHMLFS_WORKERS"),
		BatchSize:          intConfig(cmd, "batch-size", "GHMLFS_BATCH_SIZE"),
		UploadParallel:     intConfig(cmd, "upload-parallel", "GHMLFS_UPLOAD_PARALLEL"),
		RetryMax:           intConfig(cmd, "retry-max", "GHMLFS_RETRY_MAX"),
		CheckHashes:        boolConfig(cmd, "check-hashes", "GHMLFS_CHECK_HASHES"),
		DryRun:             boolConfig(cmd, "dry-run", "GHMLFS_DRY_RUN"),
		FinalCheck:         !boolConfig(cmd, "no-final-check", "GHMLFS_NO_FINAL_CHECK"),
	}
	retryDelay, err := durationConfig(cmd, "retry-delay", "GHMLFS_RETRY_DELAY")
	if err != nil {
		return migrate.Config{}, err
	}
	cfg.RetryDelay = retryDelay

	required := map[string]string{
		"source-token":        cfg.SourceToken,
		"target-organization": cfg.TargetOrganization,
		"target-token":        cfg.TargetToken,
		"work-dir":            cfg.WorkDir,
	}
	if cfg.Manifest == "" {
		required["source-organization"] = cfg.SourceOrganization
	}
	if err := requireValues(required); err != nil {
		return migrate.Config{}, err
	}
	return cfg, nil
}

func init() {
	addMigrateFlags(migrateCmd)
}

func addMigrateFlags(command *cobra.Command) {
	command.Flags().Int("batch-size", 100, "Objects per LFS Batch API request (1-10000)")
	command.Flags().Bool("check-hashes", false, "Verify local object hashes before uploading")
	command.Flags().Bool("dry-run", false, "Run export and pull, then negotiate destination objects without uploading")
	command.Flags().StringP("file", "f", "", "Existing repository manifest; skips export when set")
	command.Flags().Bool("no-final-check", false, "Skip final remote reconciliation")
	command.Flags().Int("search-depth", 1, "Search depth for source .gitattributes files")
	command.Flags().String("source-hostname", "", "GitHub Enterprise Server source hostname (optional)")
	command.Flags().String("source-organization", "", "Source organization; required without --file")
	command.Flags().String("source-token", "", "Source GitHub token with repo scope (required)")
	command.Flags().String("state", ".lfs-migrate", "Directory for run summaries and error history")
	command.Flags().String("target-hostname", "", "GitHub Enterprise Server target hostname (optional)")
	command.Flags().String("target-organization", "", "Target organization (required)")
	command.Flags().String("target-token", "", "Target GitHub token with repo scope (required)")
	command.Flags().Int("upload-parallel", 8, "Concurrent object uploads per repository")
	command.Flags().StringP("work-dir", "d", "", "Directory for repository mirrors and generated manifest (required)")
	command.Flags().IntP("workers", "w", 1, "Number of repositories to process concurrently")
}
