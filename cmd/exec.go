package cmd

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
)

var execCmd = &cobra.Command{
	Use:   "exec [appName] [args...]",
	Short: "Execute a managed binary",
	Long:  `Execute a managed binary with all environment variables passed through. If no appName is provided, lists all available tools.`,
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			if err := listTools(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		appName := args[0]
		appArgs := args[1:]

		if err := execApp(appName, appArgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(execCmd)
}

func listTools() error {
	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	b := binmanager.New(c.Apps, c.Bundles, nil)

	apps := b.GetAppsList()

	sort.Slice(apps, func(i, j int) bool {
		return apps[i].Name < apps[j].Name
	})

	fmt.Println("Available tools:")
	fmt.Println()

	maxNameLen := 0
	for _, app := range apps {
		if len(app.Name) > maxNameLen {
			maxNameLen = len(app.Name)
		}
	}

	byType := make(map[string][]binmanager.AppInfo)
	for _, app := range apps {
		byType[app.Type] = append(byType[app.Type], app)
	}

	typeOrder := []string{"binary", "uv", "fnm", "jvm", "shell"}
	for _, appType := range typeOrder {
		appInfos, ok := byType[appType]
		if !ok || len(appInfos) == 0 {
			continue
		}

		fmt.Printf("%s\n", clr.Bold(fmt.Sprintf("[%s]", appType)))
		for _, appInfo := range appInfos {
			detail := buildAppDetail(appInfo)
			if detail != "" {
				fmt.Printf("  %-*s  %s\n", maxNameLen, appInfo.Name, clr.Faint(detail))
			} else {
				fmt.Printf("  %s\n", appInfo.Name)
			}
		}
		fmt.Println()
	}

	return nil
}

func buildAppDetail(app binmanager.AppInfo) string {
	parts := []string{}

	if app.Version != "" {
		parts = append(parts, app.Version)
	}

	if app.PackageName != "" && app.PackageName != app.Name {
		parts = append(parts, fmt.Sprintf("(%s)", app.PackageName))
	}

	if app.Command != "" {
		parts = append(parts, app.Command)
	}

	if app.Description != "" {
		parts = append(parts, app.Description)
	}

	if len(parts) == 0 {
		return ""
	}

	result := parts[0]
	for _, p := range parts[1:] {
		result += "  " + p
	}
	return result
}

func execApp(appName string, args []string) error {
	c, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	rm := runtimemanager.New(c.Runtimes)
	b := binmanager.New(c.Apps, c.Bundles, rm)

	return b.Exec(appName, args)
}
