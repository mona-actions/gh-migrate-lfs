package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestConfigResolutionPrecedence(t *testing.T) {
	command := &cobra.Command{Use: "test"}
	command.Flags().String("value", "default", "")
	command.Flags().Int("count", 1, "")
	command.Flags().Bool("enabled", false, "")

	viper.Set("TEST_VALUE", "environment")
	viper.Set("TEST_COUNT", 2)
	viper.Set("TEST_ENABLED", true)
	t.Cleanup(func() {
		viper.Set("TEST_VALUE", nil)
		viper.Set("TEST_COUNT", nil)
		viper.Set("TEST_ENABLED", nil)
	})

	if got := stringConfig(command, "value", "TEST_VALUE"); got != "environment" {
		t.Fatalf("stringConfig() = %q", got)
	}
	if got := intConfig(command, "count", "TEST_COUNT"); got != 2 {
		t.Fatalf("intConfig() = %d", got)
	}
	if got := boolConfig(command, "enabled", "TEST_ENABLED"); !got {
		t.Fatal("boolConfig() = false")
	}

	if err := command.Flags().Set("value", "flag"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("count", "3"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if got := stringConfig(command, "value", "TEST_VALUE"); got != "flag" {
		t.Fatalf("flag stringConfig() = %q", got)
	}
	if got := intConfig(command, "count", "TEST_COUNT"); got != 3 {
		t.Fatalf("flag intConfig() = %d", got)
	}
	if got := boolConfig(command, "enabled", "TEST_ENABLED"); got {
		t.Fatal("flag boolConfig() = true")
	}
}
