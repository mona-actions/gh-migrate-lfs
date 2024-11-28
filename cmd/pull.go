package cmd

import (
	"fmt"

	"github.com/mona-actions/gh-migrate-lfs/pkg/pull"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Does a git clone and lfs pull on exported repositories",
	Long:  "Does a git clone and lfs pull on exported repositories",
	Run: func(cmd *cobra.Command, args []string) {
		GetFlagOrEnv(cmd, map[string]bool{
			"GHMLFS_FILE":            true,
			"GHMLFS_SOURCE_HOSTNAME": false,
			"GHMLFS_SOURCE_TOKEN":    true,
			"GHMLFS_WORK_DIR":        true,
			"GHMLFS_WORKERS":         false,
		})

		ShowConnectionStatus("export")
		if err := pull.PullLFSFromCSV(); err != nil {
			fmt.Printf("failed to export variables: %v\n", err)
		}
	},
}

func init() {
	pullCmd.Flags().StringP("file", "f", "", "Exported LFS repos file path, csv format (required)")
	pullCmd.Flags().StringP("source-hostname", "n", "", "GitHub Enterprise Server hostname URL (optional)")
	pullCmd.Flags().StringP("source-token", "t", "", "GitHub token with repo scope (required)")
	pullCmd.Flags().StringP("work-dir", "d", "", "Working directory with cloned repositories (required)")
	pullCmd.Flags().IntP("workers", "w", 1, "Number of concurrent GIT workers to use")

	viper.BindPFlag("GHMLFS_FILE", pullCmd.Flags().Lookup("file"))
	viper.BindPFlag("GHMLFS_SOURCE_HOSTNAME", pullCmd.Flags().Lookup("source-hostname"))
	viper.BindPFlag("GHMLFS_SOURCE_TOKEN", pullCmd.Flags().Lookup("source-token"))
	viper.BindPFlag("GHMLFS_WORK_DIR", pullCmd.Flags().Lookup("work-dir"))
	viper.BindPFlag("GHMLFS_WORKERS", pullCmd.Flags().Lookup("workers"))
}
