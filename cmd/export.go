package cmd

import (
	"fmt"

	"github.com/mona-actions/gh-migrate-lfs/pkg/export"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Exports a list of repositories with LFS files to a CSV file",
	Long:  "Exports a list of repositories with LFS files to a CSV file",
	Run: func(cmd *cobra.Command, args []string) {
		GetFlagOrEnv(cmd, map[string]bool{
			"GHMLFS_SOURCE_HOSTNAME":     false,
			"GHMLFS_SOURCE_ORGANIZATION": true,
			"GHMLFS_SOURCE_TOKEN":        true,
			"GHMLFS_SEARCH_DEPTH":        false,
		})

		ShowConnectionStatus("export")
		if err := export.ExportLFSRepos(); err != nil {
			fmt.Printf("failed to export lfs: %v\n", err)
		}
	},
}

func init() {
	exportCmd.Flags().StringP("source-hostname", "n", "", "GitHub Enterprise Server hostname URL (optional)")
	exportCmd.Flags().StringP("source-organization", "o", "", "Organization (required)")
	exportCmd.Flags().StringP("source-token", "t", "", "GitHub token (required)")
	exportCmd.Flags().StringP("search-depth", "s", "", "Search depth for .gitattributes file")

	viper.BindPFlag("GHMLFS_SOURCE_HOSTNAME", exportCmd.Flags().Lookup("source-hostname"))
	viper.BindPFlag("GHMLFS_SOURCE_ORGANIZATION", exportCmd.Flags().Lookup("source-organization"))
	viper.BindPFlag("GHMLFS_SOURCE_TOKEN", exportCmd.Flags().Lookup("source-token"))
	viper.BindPFlag("GHMLFS_SEARCH_DEPTH", exportCmd.Flags().Lookup("search-depth"))
}
