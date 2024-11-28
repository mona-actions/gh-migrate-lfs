package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "migrate-lfs",
	Short: "gh cli extension to migrate LFS files between git repositories",
	Long:  "gh cli extension to migrate LFS files between git repositories",
}

func Execute() error {
	return rootCmd.Execute()
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
	viper.BindPFlag("RETRY_MAX", rootCmd.PersistentFlags().Lookup("retry-max"))
	viper.BindPFlag("RETRY_DELAY", rootCmd.PersistentFlags().Lookup("retry-delay"))

	// Add subcommands
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(pullCmd)
	rootCmd.AddCommand(syncCmd)

	// hide -h, --help from global/proxy flags
	rootCmd.Flags().BoolP("help", "h", false, "")
	rootCmd.Flags().Lookup("help").Hidden = true
}

func initConfig() {
	// Allow .env file
	viper.SetConfigType("env")
	viper.AddConfigPath(".")
	viper.SetConfigName(".env")

	// Read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Printf("Error reading config file: %v\n", err)
		}
	}

	// Read from environment
	viper.AutomaticEnv()
}
