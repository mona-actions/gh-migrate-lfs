package cmd

import (
	"fmt"

	"github.com/mona-actions/gh-migrate-lfs/pkg/export"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Exports a list of repositories with LFS files to a CSV file",
	Long:  "Exports a list of repositories with LFS files to a CSV file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		sourceOrganization := stringConfig(cmd, "source-organization", "GHMLFS_SOURCE_ORGANIZATION")
		sourceToken := stringConfig(cmd, "source-token", "GHMLFS_SOURCE_TOKEN")
		sourceHostname := stringConfig(cmd, "source-hostname", "GHMLFS_SOURCE_HOSTNAME")
		if err := requireValues(map[string]string{
			"source-organization": sourceOrganization,
			"source-token":        sourceToken,
		}); err != nil {
			return err
		}

		showConnectionStatus(sourceHostname)
		if err := export.Run(cmd.Context(), export.Config{
			Organization: sourceOrganization,
			Token:        sourceToken,
			Hostname:     sourceHostname,
			Depth:        intConfig(cmd, "search-depth", "GHMLFS_SEARCH_DEPTH"),
		}); err != nil {
			return fmt.Errorf("export LFS repositories: %w", err)
		}
		return nil
	},
}

func init() {
	exportCmd.Flags().StringP("source-hostname", "n", "", "GitHub Enterprise Server hostname URL (optional)")
	exportCmd.Flags().StringP("source-organization", "o", "", "Organization (required)")
	exportCmd.Flags().StringP("source-token", "t", "", "GitHub token (required)")
	exportCmd.Flags().IntP("search-depth", "s", 1, "Search depth for .gitattributes file")

}
