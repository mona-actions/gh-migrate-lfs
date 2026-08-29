package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/mona-actions/gh-migrate-lfs/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func requireValues(fields map[string]string) error {
	var missing []string
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	sort.Strings(missing)
	return fmt.Errorf("missing required values: %s", strings.Join(missing, ", "))
}

func stringConfig(cmd *cobra.Command, flagName, key string) string {
	if cmd.Flags().Changed(flagName) {
		value, _ := cmd.Flags().GetString(flagName)
		return value
	}
	if viper.IsSet(key) {
		return viper.GetString(key)
	}
	value, _ := cmd.Flags().GetString(flagName)
	return value
}

func intConfig(cmd *cobra.Command, flagName, key string) int {
	if cmd.Flags().Changed(flagName) {
		value, _ := cmd.Flags().GetInt(flagName)
		return value
	}
	if viper.IsSet(key) {
		return viper.GetInt(key)
	}
	value, _ := cmd.Flags().GetInt(flagName)
	return value
}

func boolConfig(cmd *cobra.Command, flagName, key string) bool {
	if cmd.Flags().Changed(flagName) {
		value, _ := cmd.Flags().GetBool(flagName)
		return value
	}
	if viper.IsSet(key) {
		return viper.GetBool(key)
	}
	value, _ := cmd.Flags().GetBool(flagName)
	return value
}

func durationConfig(cmd *cobra.Command, flagName, key string) (time.Duration, error) {
	value := stringConfig(cmd, flagName, key)
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", flagName, value, err)
	}
	return duration, nil
}

func newRenderer(cmd *cobra.Command) *output.Renderer {
	quiet, _ := cmd.Flags().GetBool("quiet")
	verbose, _ := cmd.Flags().GetBool("verbose")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	tty := false
	if errOut, ok := cmd.ErrOrStderr().(*os.File); ok {
		tty = isatty.IsTerminal(errOut.Fd()) || isatty.IsCygwinTerminal(errOut.Fd())
	}
	if os.Getenv("TERM") == "dumb" {
		tty = false
	}
	if _, forced := os.LookupEnv("GH_FORCE_TTY"); forced {
		tty = true
	}
	return output.New(output.Options{
		Out:     cmd.OutOrStdout(),
		ErrOut:  cmd.ErrOrStderr(),
		TTY:     tty,
		Quiet:   quiet,
		Verbose: verbose,
		JSON:    jsonOutput,
	})
}

func finishCommand(renderer *output.Renderer, command string, runErr error) error {
	renderer.FlushJSON(command, runErr)
	return errors.Join(runErr, renderer.Err())
}

func showConnectionStatus(renderer *output.Renderer, hostname string) {
	hostname = normalizedAPIEndpoint(hostname)
	renderer.Line("%s", hostnameMessage(hostname))
	renderer.Line("%s", proxyStatus())
}

func normalizedAPIEndpoint(hostname string) string {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return ""
	}
	hostname = strings.TrimPrefix(hostname, "http://")
	hostname = strings.TrimPrefix(hostname, "https://")
	hostname = strings.TrimSuffix(hostname, "/api/v3")
	hostname = strings.TrimSuffix(hostname, "/")
	return "https://" + hostname + "/api/v3"
}

func hostnameMessage(hostname string) string {
	if hostname != "" {
		return fmt.Sprintf("Using GitHub Enterprise Server: %s", hostname)
	}
	return "Using GitHub.com"
}

func proxyStatus() string {
	if viper.GetString("HTTP_PROXY") != "" || viper.GetString("HTTPS_PROXY") != "" {
		return "Proxy: configured"
	}
	return "Proxy: not configured"
}
