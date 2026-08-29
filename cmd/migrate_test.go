package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestMigrateConfigRejectsExplicitFileAndSourceOrganization(t *testing.T) {
	command := testMigrateCommand(t)
	setRequiredMigrateFlags(t, command)
	setFlag(t, command, "file", "repositories.csv")
	setFlag(t, command, "source-organization", "source")

	_, err := migrateConfigFromCommand(command)
	if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("migrateConfigFromCommand() error = %v", err)
	}
}

func TestMigrateConfigExplicitSourceOverridesConfiguredManifest(t *testing.T) {
	viper.Set("GHMLFS_FILE", "stale.csv")
	t.Cleanup(func() { viper.Set("GHMLFS_FILE", nil) })

	command := testMigrateCommand(t)
	setRequiredMigrateFlags(t, command)
	setFlag(t, command, "source-organization", "source")

	cfg, err := migrateConfigFromCommand(command)
	if err != nil {
		t.Fatalf("migrateConfigFromCommand() error = %v", err)
	}
	if cfg.Manifest != "" || cfg.SourceOrganization != "source" {
		t.Fatalf("migrate config manifest=%q source=%q", cfg.Manifest, cfg.SourceOrganization)
	}
}

func testMigrateCommand(t *testing.T) *cobra.Command {
	t.Helper()
	command := &cobra.Command{Use: "migrate"}
	addMigrateFlags(command)
	command.Flags().String("retry-delay", "1s", "")
	command.Flags().Int("retry-max", 3, "")
	return command
}

func setRequiredMigrateFlags(t *testing.T, command *cobra.Command) {
	t.Helper()
	setFlag(t, command, "source-token", "source-token")
	setFlag(t, command, "target-organization", "target")
	setFlag(t, command, "target-token", "target-token")
	setFlag(t, command, "work-dir", "work")
}

func setFlag(t *testing.T, command *cobra.Command, name, value string) {
	t.Helper()
	if err := command.Flags().Set(name, value); err != nil {
		t.Fatal(err)
	}
}
