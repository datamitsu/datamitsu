package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"github.com/datamitsu/datamitsu/internal/ui"

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

		if err := execApp(commandContext(cmd), appName, appArgs); err != nil {
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

	typeOrder := []string{"binary", "uv", "node", "jvm", "go", "shell"}
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

	return strings.Join(parts, "  ")
}

func execApp(ctx context.Context, appName string, args []string) error {
	c, _, _, err := loadConfigWithPaths(ctx, BeforeConfigPaths, NoAutoConfig, ConfigPaths)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	rm := runtimemanager.New(c.Runtimes)
	b := binmanager.New(c.Apps, c.Bundles, rm)

	// Render install/download progress on a scoped display, then tear it down
	// BEFORE handing the terminal to the executed tool. The tool may be
	// long-running or interactive (a dev server, watch mode), so it must own a
	// clean terminal with no progress container ticking underneath it.
	d := ui.New(term.DetectMode())
	restore := ui.Activate(d)
	cmd, err := b.GetExecCmd(ctx, appName, args)
	d.Close()
	restore()
	if err != nil {
		return fmt.Errorf("failed to prepare %s: %w", appName, err)
	}

	// A link-app installed lazily by GetExecCmd (e.g. slidev, whose Links
	// materialize its bundled theme symlink under .datamitsu/) needs its links
	// created now — init defers them until the app exists on disk. Best-effort:
	// a link refresh failure must never stop the requested tool from running.
	if len(c.Apps[appName].Links) > 0 {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			if root, rootErr := traverser.GetGitRoot(ctx, cwd); rootErr == nil {
				if _, linkErr := materializeInstalledLinks(root, c, b, false); linkErr != nil {
					fmt.Fprintf(os.Stderr, "warning: could not refresh .datamitsu links for %s: %v\n", appName, linkErr)
				}
			}
		}
	}

	// Shell apps download/install nothing, so there is no progress to show; run
	// them through the binmanager's shell-aware path.
	if cmd == nil {
		return b.Exec(ctx, appName, args)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to execute %s: %w", appName, err)
	}
	return nil
}
