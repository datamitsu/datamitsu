package cmd

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"fmt"
	"sort"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var bundlesCmd = &cobra.Command{
	Use:   "bundles",
	Short: "Inspect installed bundles",
	Long:  `Commands for listing, inspecting, and locating installed bundles.`,
}

var bundlesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured bundles with their version and install status",
	Args:  cobra.NoArgs,
	RunE:  runBundlesList,
}

var bundlesInspectCmd = &cobra.Command{
	Use:   "inspect <name>",
	Short: "Show install path and file tree for an installed bundle",
	Args:  cobra.ExactArgs(1),
	RunE:  runBundlesInspect,
}

var bundlesPathCmd = &cobra.Command{
	Use:   "path <name>",
	Short: "Print the install directory path for a bundle",
	Args:  cobra.ExactArgs(1),
	RunE:  runBundlesPath,
}

func init() {
	devtoolsCmd.AddCommand(bundlesCmd)
	bundlesCmd.AddCommand(bundlesListCmd)
	bundlesCmd.AddCommand(bundlesInspectCmd)
	bundlesCmd.AddCommand(bundlesPathCmd)
}

type bundleEntry struct {
	name    string
	version string
}

func collectBundleEntries(bundles binmanager.MapOfBundles) []bundleEntry {
	entries := make([]bundleEntry, 0, len(bundles))
	for name, b := range bundles {
		version := ""
		if b != nil {
			version = b.Version
		}
		entries = append(entries, bundleEntry{
			name:    name,
			version: version,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})
	return entries
}

func runBundlesList(cmd *cobra.Command, args []string) error {
	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	bm := binmanager.New(c.Apps, c.Bundles, runtimemanager.New(c.Runtimes))

	entries := collectBundleEntries(c.Bundles)

	clrInstalled := color.New(color.FgGreen).SprintFunc()
	clrNotInstalled := color.New(color.Faint).SprintFunc()

	for _, e := range entries {
		_, installErr := bm.GetBundleRoot(e.name)
		installed := installErr == nil

		status := clrNotInstalled("not installed")
		if installed {
			status = clrInstalled("installed")
		}

		versionStr := ""
		if e.version != "" {
			versionStr = fmt.Sprintf(" (%s)", e.version)
		}

		fmt.Printf("%s%s - %s\n", e.name, versionStr, status)
	}
	return nil
}

func runBundlesInspect(cmd *cobra.Command, args []string) error {
	bundleName := args[0]

	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if _, ok := c.Bundles[bundleName]; !ok {
		return fmt.Errorf("bundle %q not found in config", bundleName)
	}

	bm := binmanager.New(c.Apps, c.Bundles, runtimemanager.New(c.Runtimes))

	installPath, err := bm.GetBundleRoot(bundleName)
	if err != nil {
		return fmt.Errorf("bundle %q is not installed. Run 'datamitsu init' to install bundles", bundleName)
	}

	fmt.Printf("Install path: %s\n", installPath)
	fmt.Printf("Files:\n")

	lines := buildFileTree(installPath)
	for _, line := range lines {
		fmt.Printf("  %s\n", line)
	}
	return nil
}

func runBundlesPath(cmd *cobra.Command, args []string) error {
	bundleName := args[0]

	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if _, ok := c.Bundles[bundleName]; !ok {
		return fmt.Errorf("bundle %q not found in config", bundleName)
	}

	bm := binmanager.New(c.Apps, c.Bundles, runtimemanager.New(c.Runtimes))

	installPath, err := bm.GetBundleRoot(bundleName)
	if err != nil {
		return fmt.Errorf("bundle %q is not installed. Run 'datamitsu init' to install bundles", bundleName)
	}

	fmt.Println(installPath)
	return nil
}
