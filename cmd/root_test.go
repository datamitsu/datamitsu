package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSilenceUsageAndErrors(t *testing.T) {
	t.Run("SilenceUsage is set", func(t *testing.T) {
		if !rootCmd.SilenceUsage {
			t.Error("rootCmd.SilenceUsage should be true")
		}
	})

	t.Run("SilenceErrors is set", func(t *testing.T) {
		if !rootCmd.SilenceErrors {
			t.Error("rootCmd.SilenceErrors should be true")
		}
	})
}

func TestSilenceUsagePreventsUsageOnRuntimeError(t *testing.T) {
	// Create a test command that mimics our setup
	testRoot := &cobra.Command{
		Use:           "test",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	testRoot.AddCommand(&cobra.Command{
		Use: "failing-sub",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("runtime error occurred")
		},
	})

	var stdout, stderr bytes.Buffer
	testRoot.SetOut(&stdout)
	testRoot.SetErr(&stderr)
	testRoot.SetArgs([]string{"failing-sub"})

	err := testRoot.Execute()
	if err == nil {
		t.Fatal("expected error from failing subcommand")
	}

	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "Usage:") {
		t.Errorf("SilenceUsage should prevent 'Usage:' from appearing on runtime errors, got: %q", combined)
	}
	if strings.Contains(combined, "runtime error occurred") {
		t.Errorf("SilenceErrors should prevent error message from appearing in cobra output, got: %q", combined)
	}
}
