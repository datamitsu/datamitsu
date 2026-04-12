package cmd

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/ldflags"
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

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configTypesCmd)
	rootCmd.AddCommand(configCmd)
}
