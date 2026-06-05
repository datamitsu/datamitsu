package cmd

import (
	"github.com/datamitsu/datamitsu/internal/registry"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
)

var (
	nodeUpdateFlag bool
	nodeDryRunFlag bool
	pullNodeMinAge *int
)

var pullNodeCmd = &cobra.Command{
	Use:   "pull-node <file>",
	Short: "Pull latest versions for npm packages from the registry",
	Long: `Query the npm registry for latest versions of all node apps in a JSON file.

Reads the specified JSON file directly, fetches latest versions and descriptions
from the npm registry, and prints a summary.
With --update: updates the JSON file with latest versions and descriptions.
If the file does not exist, an empty JSON file is created.

Example:
  datamitsu devtools pull-node config/src/nodeApps.json
  datamitsu devtools pull-node config/src/nodeApps.json --update`,
	Args: cobra.ExactArgs(1),
	RunE: runPullNode,
}

func init() {
	devtoolsCmd.AddCommand(pullNodeCmd)
	pullNodeCmd.Flags().BoolVar(&nodeUpdateFlag, "update", false,
		"Update versions in the JSON file with latest from npm")
	pullNodeCmd.Flags().BoolVar(&nodeDryRunFlag, "dry-run", false,
		"Show results without writing to file")
	pullNodeMinAge = addMinAgeFlag(pullNodeCmd)
}

type npmVersionResult struct {
	Name           string `json:"name"`
	PackageName    string `json:"packageName"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion,omitempty"`
	Description    string `json:"description,omitempty"`
	UpdateNeeded   bool   `json:"updateNeeded"`
	Error          string `json:"error,omitempty"`
}

func runPullNode(cmd *cobra.Command, args []string) error {
	file := args[0]

	// Resolve the effective minimum release age from runtime config + flag.
	eff, err := runtimeconfig.Get()
	if err != nil {
		return fmt.Errorf("failed to read runtime config: %w", err)
	}
	minAge := resolveMinAge(*pullNodeMinAge, eff)

	if err := ensureNodeAppsJSONExists(file); err != nil {
		return fmt.Errorf("failed to ensure file exists: %w", err)
	}

	apps, err := readNodeAppsJSON(file)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", file, err)
	}

	if len(apps) == 0 {
		fmt.Println("No node (npm) apps found in JSON file.")
		return nil
	}

	names := make([]string, 0, len(apps))
	for name := range apps {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Printf("Minimum release age: %s\n", minAgeBanner(minAge))
	fmt.Printf("Checking %d npm packages...\n\n", len(names))

	var results []npmVersionResult
	maxNameLen := 0
	for _, name := range names {
		if len(name) > maxNameLen {
			maxNameLen = len(name)
		}
	}

	for _, name := range names {
		entry := apps[name]
		result := npmVersionResult{
			Name:           name,
			PackageName:    entry.PackageName,
			CurrentVersion: entry.Version,
		}

		info, err := registry.GetNPMPackageInfoWithMinAge(entry.PackageName, minAge)
		switch {
		case err != nil:
			result.Error = err.Error()
			fmt.Printf("  %-*s  %s  -> error: %v\n", maxNameLen, name, result.CurrentVersion, err)
		case info == nil:
			// No version is old enough under the active min-age cutoff: skip with
			// a warning and keep the current version (no error, no update).
			fmt.Fprintf(os.Stderr,
				"  %-*s  %s  -> warning: no version at least %d minutes old; keeping current\n",
				maxNameLen, name, result.CurrentVersion, minAge)
		default:
			result.LatestVersion = info.Version
			result.UpdateNeeded = info.Version != entry.Version
			result.Description = info.Description

			status := "up-to-date"
			if result.UpdateNeeded {
				status = fmt.Sprintf("-> %s", info.Version)
			}
			line := fmt.Sprintf("  %-*s  %s  %s", maxNameLen, name, result.CurrentVersion, status)
			if info.Description != "" {
				line += fmt.Sprintf("  %s", info.Description)
			}
			fmt.Println(line)
		}

		results = append(results, result)
	}

	if nodeUpdateFlag && !nodeDryRunFlag {
		if err := updateNodeAppsJSON(file, results); err != nil {
			return fmt.Errorf("error updating %s: %w", file, err)
		}
	}

	printNodeSummary(results)

	for _, r := range results {
		if r.Error != "" {
			return fmt.Errorf("some packages failed to fetch from registry")
		}
	}
	return nil
}

func printNodeSummary(results []npmVersionResult) {
	updated := 0
	errors := 0
	for _, r := range results {
		if r.Error != "" {
			errors++
		} else if r.UpdateNeeded {
			updated++
		}
	}
	fmt.Printf("\nSummary: %d packages, %d updates available, %d errors\n",
		len(results), updated, errors)
}

type nodeAppsJSON = map[string]nodeAppEntry

type nodeAppEntry struct {
	PackageName string `json:"packageName"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

func ensureNodeAppsJSONExists(path string) error {
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking file: %w", err)
	}
	if os.IsNotExist(err) {
		emptyJSON := []byte("{}\n")
		tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
		if err != nil {
			return fmt.Errorf("failed to create temp file: %w", err)
		}
		tmpPath := tmpFile.Name()
		if _, err := tmpFile.Write(emptyJSON); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to write temp file: %w", err)
		}
		if err := tmpFile.Close(); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to close temp file: %w", err)
		}
		if err := os.Chmod(tmpPath, 0644); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to chmod temp file: %w", err)
		}
		if err := os.Rename(tmpPath, path); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to rename temp file: %w", err)
		}
	}
	return nil
}

func readNodeAppsJSON(path string) (nodeAppsJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var apps nodeAppsJSON
	if err := json.Unmarshal(data, &apps); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return apps, nil
}

func writeNodeAppsJSON(path string, apps nodeAppsJSON) error {
	data, err := json.MarshalIndent(apps, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling: %w", err)
	}
	data = append(data, '\n')
	tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

func updateNodeAppsJSON(path string, results []npmVersionResult) error {
	existing, err := readNodeAppsJSON(path)
	if err != nil {
		return fmt.Errorf("failed to read existing %s: %w", path, err)
	}
	apps := make(nodeAppsJSON, len(results))
	updatedCount := 0
	for _, r := range results {
		version := r.CurrentVersion
		if r.Error == "" && r.UpdateNeeded {
			version = r.LatestVersion
			updatedCount++
		}
		desc := r.Description
		if desc == "" && existing != nil {
			if e, ok := existing[r.Name]; ok {
				desc = e.Description
			}
		}
		apps[r.Name] = nodeAppEntry{
			PackageName: r.PackageName,
			Version:     version,
			Description: desc,
		}
	}

	if err := writeNodeAppsJSON(path, apps); err != nil {
		return err
	}
	if updatedCount > 0 {
		fmt.Printf("\n✓ Updated %d versions in %s\n", updatedCount, path)
	} else {
		fmt.Printf("\nNo updates to write to %s\n", path)
	}
	return nil
}
