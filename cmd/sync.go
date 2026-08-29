package cmd

import (
	"fmt"

	syncpkg "github.com/mona-actions/gh-migrate-lfs/pkg/sync"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Upload local LFS objects directly to migrated repositories",
	Long:  "Scan local LFS object stores and upload missing objects through the Git LFS Batch API without traversing Git refs.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		manifestPath := stringConfig(cmd, "file", "GHMLFS_FILE")
		workDir := stringConfig(cmd, "work-dir", "GHMLFS_WORK_DIR")
		targetOrganization := stringConfig(cmd, "target-organization", "GHMLFS_TARGET_ORGANIZATION")
		targetHostname := stringConfig(cmd, "target-hostname", "GHMLFS_TARGET_HOSTNAME")
		targetToken := stringConfig(cmd, "target-token", "GHMLFS_TARGET_TOKEN")
		if err := requireValues(map[string]string{
			"file":                manifestPath,
			"target-organization": targetOrganization,
			"target-token":        targetToken,
			"work-dir":            workDir,
		}); err != nil {
			return err
		}
		retryDelay, err := durationConfig(cmd, "retry-delay", "GHMLFS_RETRY_DELAY")
		if err != nil {
			return fmt.Errorf("invalid retry delay: %w", err)
		}

		showConnectionStatus(targetHostname)
		err = syncpkg.Run(cmd.Context(), syncpkg.Config{
			InputFile:      manifestPath,
			WorkDir:        workDir,
			TargetOrg:      targetOrganization,
			TargetHostname: targetHostname,
			Token:          targetToken,
			Workers:        intConfig(cmd, "workers", "GHMLFS_WORKERS"),
			BatchSize:      intConfig(cmd, "batch-size", "GHMLFS_BATCH_SIZE"),
			UploadParallel: intConfig(cmd, "upload-parallel", "GHMLFS_UPLOAD_PARALLEL"),
			RetryMax:       intConfig(cmd, "retry-max", "GHMLFS_RETRY_MAX"),
			RetryDelay:     retryDelay,
			CheckHashes:    boolConfig(cmd, "check-hashes", "GHMLFS_CHECK_HASHES"),
			DryRun:         boolConfig(cmd, "dry-run", "GHMLFS_DRY_RUN"),
			FinalCheck:     !boolConfig(cmd, "no-final-check", "GHMLFS_NO_FINAL_CHECK"),
			StateRoot:      stringConfig(cmd, "state", "GHMLFS_STATE_DIR"),
		})
		if err != nil {
			return fmt.Errorf("sync repositories: %w", err)
		}
		return nil
	},
}

func init() {
	syncCmd.Flags().Int("batch-size", 100, "Objects per LFS Batch API request (1-10000)")
	syncCmd.Flags().Bool("check-hashes", false, "Verify local object hashes before uploading")
	syncCmd.Flags().Bool("dry-run", false, "Negotiate objects without uploading")
	syncCmd.Flags().StringP("file", "f", "", "Exported LFS repos file path, csv format (required)")
	syncCmd.Flags().Bool("no-final-check", false, "Skip final remote reconciliation")
	syncCmd.Flags().String("state", ".lfs-migrate", "Directory for run summaries and error history")
	syncCmd.Flags().StringP("target-hostname", "n", "", "GitHub Enterprise Server hostname URL (optional)")
	syncCmd.Flags().StringP("target-organization", "o", "", "Organization (required)")
	syncCmd.Flags().StringP("target-token", "t", "", "GitHub token with repo scope (required)")
	syncCmd.Flags().Int("upload-parallel", 8, "Concurrent object uploads per repository")
	syncCmd.Flags().StringP("work-dir", "d", "", "Working directory with cloned repositories (required)")
	syncCmd.Flags().IntP("workers", "w", 1, "Number of repositories to process concurrently")

}
