package cmd

import (
	"fmt"

	"github.com/mona-actions/gh-migrate-lfs/pkg/sync"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync LFS objects to migrated repositories",
	Long:  "Sync LFS objects to migrated repositories",
	Run: func(cmd *cobra.Command, args []string) {
		GetFlagOrEnv(cmd, map[string]bool{
			"GHMLFS_FILE":                true,
			"GHMLFS_TARGET_HOSTNAME":     false,
			"GHMLFS_TARGET_ORGANIZATION": true,
			"GHMLFS_TARGET_TOKEN":        true,
			"GHMLFS_WORK_DIR":            true,
			"GHMLFS_WORKERS":             false,
		})

		ShowConnectionStatus("sync")
		if err := sync.SyncFromCSV(); err != nil {
			fmt.Printf("failed to sync repositories: %v\n", err)
		}
	},
}

func init() {
	syncCmd.Flags().StringP("file", "f", "", "Exported LFS repos file path, csv format (required)")
	syncCmd.Flags().StringP("target-hostname", "n", "", "GitHub Enterprise Server hostname URL (optional)")
	syncCmd.Flags().StringP("target-organization", "o", "", "Organization (required)")
	syncCmd.Flags().StringP("target-token", "t", "", "GitHub token with repo scope (required)")
	syncCmd.Flags().StringP("work-dir", "d", "", "Working directory with cloned repositories (required)")
	syncCmd.Flags().IntP("workers", "w", 1, "Number of concurrent GIT workers to use")

	viper.BindPFlag("GHMLFS_FILE", syncCmd.Flags().Lookup("file"))
	viper.BindPFlag("GHMLFS_TARGET_HOSTNAME", syncCmd.Flags().Lookup("target-hostname"))
	viper.BindPFlag("GHMLFS_TARGET_ORGANIZATION", syncCmd.Flags().Lookup("target-organization"))
	viper.BindPFlag("GHMLFS_TARGET_TOKEN", syncCmd.Flags().Lookup("target-token"))
	viper.BindPFlag("GHMLFS_WORK_DIR", syncCmd.Flags().Lookup("work-dir"))
	viper.BindPFlag("GHMLFS_WORKERS", syncCmd.Flags().Lookup("workers"))
}
