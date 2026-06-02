package cmd

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management commands",
	Long:  `Commands for viewing and managing datamitsu configuration`,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration as JSON",
	Long:  `Display the current datamitsu configuration as JSON (result of executing config.ts)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _, _, err := loadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Convert to JSON with pretty printing
		jsonData, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal config to JSON: %w", err)
		}

		fmt.Println(string(jsonData))
		return nil
	},
}

var configTypesCmd = &cobra.Command{
	Use:   "types",
	Short: fmt.Sprintf("Show TypeScript type definitions (%s)", ldflags.ConfigDTSFilename),
	Long:  fmt.Sprintf(`Display the TypeScript type definitions file (%s) for configuring datamitsu`, ldflags.ConfigDTSFilename),
	RunE: func(cmd *cobra.Command, args []string) error {
		dts := config.GetDefaultConfigDTS()
		fmt.Print(dts)
		return nil
	},
}

// runtimeConfigJSON serializes the effective runtime config as indented JSON.
// Shared by the command handler and tests so env-override behavior can be
// verified without depending on the idempotent, cached Get() state.
func runtimeConfigJSON(eff runtimeconfig.Effective) (string, error) {
	data, err := json.MarshalIndent(eff, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal runtime config to JSON: %w", err)
	}
	return string(data), nil
}

var configRuntimeCmd = &cobra.Command{
	Use:   "runtime",
	Short: "Show the effective runtime configuration as JSON",
	Long: `Display the full effective runtime configuration snapshot as JSON.

This is the env-resolved view of execution limits and runtime policy (the
runtimeconfig.Effective struct), intended for introspection and debugging — for
example verifying that an environment override took effect:

  datamitsu config runtime | jq .minimumReleaseAgeMinutes

It is NOT injected into the config JS VM; the JS layer only sees the minimal
allowlisted datamitsuConfigInputs surface.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		eff, err := runtimeconfig.Get()
		if err != nil {
			return err
		}
		out, err := runtimeConfigJSON(eff)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), out)
		return err
	},
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configTypesCmd)
	configCmd.AddCommand(configRuntimeCmd)
	rootCmd.AddCommand(configCmd)
}
