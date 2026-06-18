package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"

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
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), out); err != nil {
			return fmt.Errorf("failed to write runtime config: %w", err)
		}
		return nil
	},
}

// selectChainHashes filters all chain-hash entries down to the requested files,
// preserving the (sorted) input order. With no args it returns every entry. Any
// requested name not present in all is reported as an error listing the unknowns,
// so a typo never silently prints nothing.
func selectChainHashes(all []config.ChainHashEntry, args []string) ([]config.ChainHashEntry, error) {
	if len(args) == 0 {
		return all, nil
	}
	index := make(map[string]config.ChainHashEntry, len(all))
	for _, e := range all {
		index[e.FileName] = e
	}
	var selected []config.ChainHashEntry
	var missing []string
	for _, name := range args {
		if e, ok := index[name]; ok {
			selected = append(selected, e)
		} else {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("no setup config named: %s (run without arguments to list all)", strings.Join(missing, ", "))
	}
	return selected, nil
}

// formatChainHashTable renders entries as left-aligned "file  hash" rows.
func formatChainHashTable(entries []config.ChainHashEntry) string {
	width := 0
	for _, e := range entries {
		if len(e.FileName) > width {
			width = len(e.FileName)
		}
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%-*s  %s\n", width, e.FileName, e.Hash)
	}
	return b.String()
}

var configChainHashCmd = &cobra.Command{
	Use:   "chain-hash [file...]",
	Short: "Print the expectChainHash value for setup files",
	Long: `Print the XXH3-128 chain hash that ` + "`datamitsu setup`" + ` verifies for managed
config files — the hash of the content entering each file's root (topmost) config
layer. Copy it into a setup entry's ` + "`expectChainHash`" + ` to pin the upstream
baseline your overrides were written against.

The value is the input to the TOPMOST layer, so declare your own entry for the
file first (a placeholder ` + "`expectChainHash`" + ` is enough), then read the hash
here. With no arguments every setup file is listed; with exactly one file only its
bare hash is printed, which is handy for scripting:

  pin=$(datamitsu config chain-hash eslint.config.mjs)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, layerMap, _, err := loadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		selected, err := selectChainHashes(config.ChainHashes(*layerMap), args)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		// Single explicit file → bare hash, so it can be captured directly.
		if len(args) == 1 && len(selected) == 1 {
			if _, err := fmt.Fprintln(out, selected[0].Hash); err != nil {
				return fmt.Errorf("failed to write chain hash: %w", err)
			}
			return nil
		}
		if _, err := fmt.Fprint(out, formatChainHashTable(selected)); err != nil {
			return fmt.Errorf("failed to write chain hashes: %w", err)
		}
		return nil
	},
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configTypesCmd)
	configCmd.AddCommand(configRuntimeCmd)
	configCmd.AddCommand(configChainHashCmd)
	rootCmd.AddCommand(configCmd)
}
