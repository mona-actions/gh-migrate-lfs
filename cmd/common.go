package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

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

func showConnectionStatus(hostname string) {
	hostname = normalizedAPIEndpoint(hostname)
	fmt.Println(hostnameMessage(hostname))
	fmt.Println(proxyStatus())
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
		return fmt.Sprintf("\nUsing GitHub Enterprise Server: %s", hostname)
	}
	return "\nUsing GitHub.com"
}

func proxyStatus() string {
	if viper.GetString("HTTP_PROXY") != "" || viper.GetString("HTTPS_PROXY") != "" {
		return "Proxy: configured\n"
	}
	return "Proxy: not configured\n"
}
