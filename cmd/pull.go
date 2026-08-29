package cmd

import (
	"fmt"
	"os/exec"

	"github.com/mona-actions/gh-migrate-lfs/pkg/pull"
	"github.com/spf13/cobra"
)

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Does a git clone and lfs pull on exported repositories",
	Long:  "Does a git clone and lfs pull on exported repositories",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkGitLFS(cmd); err != nil {
			return fmt.Errorf("git lfs command not found; install Git LFS from https://git-lfs.com: %w", err)
		}
		manifestPath := stringConfig(cmd, "file", "GHMLFS_FILE")
		sourceToken := stringConfig(cmd, "source-token", "GHMLFS_SOURCE_TOKEN")
		workDir := stringConfig(cmd, "work-dir", "GHMLFS_WORK_DIR")
		if err := requireValues(map[string]string{
			"file":         manifestPath,
			"source-token": sourceToken,
			"work-dir":     workDir,
		}); err != nil {
			return err
		}

		if err := pull.Run(cmd.Context(), pull.Config{
			InputFile: manifestPath,
			Token:     sourceToken,
			WorkDir:   workDir,
			Workers:   intConfig(cmd, "workers", "GHMLFS_WORKERS"),
		}); err != nil {
			return fmt.Errorf("pull LFS repositories: %w", err)
		}
		return nil
	},
}

func checkGitLFS(cmd *cobra.Command) error {
	return exec.CommandContext(cmd.Context(), "git", "lfs", "--version").Run()
}

func init() {
	pullCmd.Flags().StringP("file", "f", "", "Exported LFS repos file path, csv format (required)")
	pullCmd.Flags().StringP("source-token", "t", "", "GitHub token with repo scope (required)")
	pullCmd.Flags().StringP("work-dir", "d", "", "Working directory with cloned repositories (required)")
	pullCmd.Flags().IntP("workers", "w", 1, "Number of repositories to pull concurrently")

}
