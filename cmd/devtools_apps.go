package cmd

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var appsCmd = &cobra.Command{
	Use:   "apps",
	Short: "Inspect installed applications",
	Long:  `Commands for listing, inspecting, and locating installed applications.`,
}

var appsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured apps with their type and install status",
	Args:  cobra.NoArgs,
	RunE:  runAppsList,
}

var appsInspectCmd = &cobra.Command{
	Use:   "inspect <name>",
	Short: "Show install path and file tree for an installed app",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppsInspect,
}

var appsPathCmd = &cobra.Command{
	Use:   "path <name>",
	Short: "Print the install directory path for an app",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppsPath,
}

func init() {
	devtoolsCmd.AddCommand(appsCmd)
	appsCmd.AddCommand(appsListCmd)
	appsCmd.AddCommand(appsInspectCmd)
	appsCmd.AddCommand(appsPathCmd)
}

func runAppsList(cmd *cobra.Command, args []string) error {
	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	bm := binmanager.New(c.Apps, c.Bundles, runtimemanager.New(c.Runtimes))

	appInfos := bm.GetAppsList()
	sort.Slice(appInfos, func(i, j int) bool {
		return appInfos[i].Name < appInfos[j].Name
	})

	clrInstalled := color.New(color.FgGreen).SprintFunc()
	clrNotInstalled := color.New(color.Faint).SprintFunc()

	for _, info := range appInfos {
		_, installErr := bm.GetInstallRoot(info.Name)
		installed := installErr == nil

		status := clrNotInstalled("not installed")
		if installed {
			status = clrInstalled("installed")
		}

		line := fmt.Sprintf("%s (%s)", info.Name, info.Type)
		if info.Version != "" {
			line += fmt.Sprintf(" %s", info.Version)
		}
		if info.Description != "" {
			line += fmt.Sprintf(" - %s", info.Description)
		}
		line += fmt.Sprintf(" - %s", status)

		fmt.Println(line)
	}
	return nil
}

func runAppsInspect(cmd *cobra.Command, args []string) error {
	appName := args[0]

	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if _, ok := c.Apps[appName]; !ok {
		return fmt.Errorf("app %q not found in config", appName)
	}

	bm := binmanager.New(c.Apps, c.Bundles, runtimemanager.New(c.Runtimes))

	installPath, err := bm.GetInstallRoot(appName)
	if err != nil {
		return fmt.Errorf("app %q is not installed. Run 'datamitsu init' to install apps", appName)
	}

	appConfig := c.Apps[appName]
	header := formatInspectHeader(installPath, &appConfig)
	fmt.Print(header)
	fmt.Printf("Files:\n")

	lines := buildFileTree(installPath)
	for _, line := range lines {
		fmt.Printf("  %s\n", line)
	}
	return nil
}

func runAppsPath(cmd *cobra.Command, args []string) error {
	appName := args[0]

	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if _, ok := c.Apps[appName]; !ok {
		return fmt.Errorf("app %q not found in config", appName)
	}

	bm := binmanager.New(c.Apps, c.Bundles, runtimemanager.New(c.Runtimes))

	installPath, err := bm.GetInstallRoot(appName)
	if err != nil {
		return fmt.Errorf("app %q is not installed. Run 'datamitsu init' to install apps", appName)
	}

	fmt.Println(installPath)
	return nil
}

func formatInspectHeader(installPath string, app *binmanager.App) string {
	s := fmt.Sprintf("Install path: %s\n", installPath)
	if app.Description != "" {
		s += fmt.Sprintf("Description: %s\n", app.Description)
	}
	return s
}

// collapsedDirs are directories whose contents are collapsed into a count.
var collapsedDirs = map[string]bool{
	"node_modules": true,
	".venv":        true,
	"__pycache__":  true,
}

func buildFileTree(root string) []string {
	type dirEntry struct {
		name  string
		isDir bool
	}

	entries, err := os.ReadDir(root)
	if err != nil || len(entries) == 0 {
		return nil
	}

	sorted := make([]dirEntry, 0, len(entries))
	for _, e := range entries {
		// Skip symlinks to avoid infinite loops from symlink cycles
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		sorted = append(sorted, dirEntry{name: e.Name(), isDir: e.IsDir()})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].name < sorted[j].name
	})

	var lines []string
	for i, e := range sorted {
		isLast := i == len(sorted)-1
		prefix := "├── "
		if isLast {
			prefix = "└── "
		}

		if e.isDir {
			if collapsedDirs[e.name] {
				count := countFiles(filepath.Join(root, e.name))
				lines = append(lines, fmt.Sprintf("%s%s/ (%d files)", prefix, e.name, count))
			} else {
				lines = append(lines, fmt.Sprintf("%s%s/", prefix, e.name))
				childPrefix := "│   "
				if isLast {
					childPrefix = "    "
				}
				children := buildFileTree(filepath.Join(root, e.name))
				for _, child := range children {
					lines = append(lines, childPrefix+child)
				}
			}
		} else {
			lines = append(lines, prefix+e.name)
		}
	}

	return lines
}

func countFiles(dir string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}
