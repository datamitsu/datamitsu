package cmd

import (
	"github.com/datamitsu/datamitsu/internal/registry"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
)

var (
	fnmUpdateFlag bool
	fnmDryRunFlag bool
)

var pullFNMCmd = &cobra.Command{
	Use:   "pull-fnm <file>",
	Short: "Pull latest versions for npm packages from the registry",
	Long: `Query the npm registry for latest versions of all FNM apps in a JSON file.

Reads the specified JSON file directly, fetches latest versions and descriptions
from the npm registry, and prints a summary.
With --update: updates the JSON file with latest versions and descriptions.
If the file does not exist, an empty JSON file is created.

Example:
  datamitsu devtools pull-fnm config/src/fnmApps.json
  datamitsu devtools pull-fnm config/src/fnmApps.json --update`,
	Args: cobra.ExactArgs(1),
	RunE: runPullFNM,
}

func init() {
	devtoolsCmd.AddCommand(pullFNMCmd)
	pullFNMCmd.Flags().BoolVar(&fnmUpdateFlag, "update", false,
		"Update versions in the JSON file with latest from npm")
	pullFNMCmd.Flags().BoolVar(&fnmDryRunFlag, "dry-run", false,
		"Show results without writing to file")
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

func runPullFNM(cmd *cobra.Command, args []string) error {
	file := args[0]

	if err := ensureFNMAppsJSONExists(file); err != nil {
		return fmt.Errorf("failed to ensure file exists: %w", err)
	}

	apps, err := readFNMAppsJSON(file)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", file, err)
	}

	if len(apps) == 0 {
		fmt.Println("No FNM (npm) apps found in JSON file.")
		return nil
	}

	names := make([]string, 0, len(apps))
	for name := range apps {
		names = append(names, name)
	}
	sort.Strings(names)

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

		info, err := registry.GetNPMPackageInfo(entry.PackageName)
		if err != nil {
			result.Error = err.Error()
			fmt.Printf("  %-*s  %s  -> error: %v\n", maxNameLen, name, result.CurrentVersion, err)
		} else {
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

	if fnmUpdateFlag && !fnmDryRunFlag {
		if err := updateFNMAppsJSON(file, results); err != nil {
			return fmt.Errorf("error updating %s: %w", file, err)
		}
	}

	printFNMSummary(results)

	for _, r := range results {
		if r.Error != "" {
			return fmt.Errorf("some packages failed to fetch from registry")
		}
	}
	return nil
}

func printFNMSummary(results []npmVersionResult) {
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

type fnmAppsJSON = map[string]fnmAppEntry

type fnmAppEntry struct {
	PackageName string `json:"packageName"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

func ensureFNMAppsJSONExists(path string) error {
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

func readFNMAppsJSON(path string) (fnmAppsJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var apps fnmAppsJSON
	if err := json.Unmarshal(data, &apps); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return apps, nil
}

func writeFNMAppsJSON(path string, apps fnmAppsJSON) error {
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

func updateFNMAppsJSON(path string, results []npmVersionResult) error {
	existing, err := readFNMAppsJSON(path)
	if err != nil {
		return fmt.Errorf("failed to read existing %s: %w", path, err)
	}
	apps := make(fnmAppsJSON, len(results))
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
		apps[r.Name] = fnmAppEntry{
			PackageName: r.PackageName,
			Version:     version,
			Description: desc,
		}
	}

	if err := writeFNMAppsJSON(path, apps); err != nil {
		return err
	}
	if updatedCount > 0 {
		fmt.Printf("\n✓ Updated %d versions in %s\n", updatedCount, path)
	} else {
		fmt.Printf("\nNo updates to write to %s\n", path)
	}
	return nil
}
