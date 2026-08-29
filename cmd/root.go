package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:          "migrate-lfs",
	Short:        "gh cli extension to migrate LFS files between git repositories",
	Long:         "gh cli extension to migrate LFS files between git repositories",
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return configErr
	},
}

var configErr error

func ExecuteContext(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	cobra.OnInitialize(initConfig)

	// Define root command flags
	rootCmd.PersistentFlags().String("http-proxy", "", "HTTP proxy")
	rootCmd.PersistentFlags().String("https-proxy", "", "HTTPS proxy")
	rootCmd.PersistentFlags().String("no-proxy", "", "No proxy list")
	rootCmd.PersistentFlags().Int("retry-max", 3, "Maximum retry attempts")
	rootCmd.PersistentFlags().String("retry-delay", "1s", "Delay between retries")

	// Bind flags to viper
	viper.BindPFlag("HTTP_PROXY", rootCmd.PersistentFlags().Lookup("http-proxy"))
	viper.BindPFlag("HTTPS_PROXY", rootCmd.PersistentFlags().Lookup("https-proxy"))
	viper.BindPFlag("NO_PROXY", rootCmd.PersistentFlags().Lookup("no-proxy"))
	viper.BindPFlag("GHMLFS_RETRY_MAX", rootCmd.PersistentFlags().Lookup("retry-max"))
	viper.BindPFlag("GHMLFS_RETRY_DELAY", rootCmd.PersistentFlags().Lookup("retry-delay"))

	// Add subcommands
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(migrateCmd)
	rootCmd.AddCommand(pullCmd)
	rootCmd.AddCommand(syncCmd)

	// hide -h, --help from global/proxy flags
	rootCmd.Flags().BoolP("help", "h", false, "")
	rootCmd.Flags().Lookup("help").Hidden = true
}

func initConfig() {
	viper.SetConfigType("env")
	viper.AddConfigPath(".")
	viper.SetConfigName(".env")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			configErr = fmt.Errorf("read configuration: %w", err)
		}
	}
	viper.AutomaticEnv()
}
