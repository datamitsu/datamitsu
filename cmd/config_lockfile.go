package cmd

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"sort"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"

	"github.com/spf13/cobra"
)

var configLockfileCmd = &cobra.Command{
	Use:   "lockfile [appName]",
	Short: "Generate lock file content for a runtime-managed app",
	Long: `Generates a runtime-managed app's lock file in an isolated workspace and outputs
it as a JSON-escaped string ready to paste into configuration.

When called without arguments, lists all apps that support lock files (bun/node/uv/go).
Existing installed apps are never removed or replaced by lockfile generation.`,
	Args: usageArgs(cobra.RangeArgs(0, 1)),
	RunE: runConfigLockfile,
}

func init() {
	configCmd.AddCommand(configLockfileCmd)
}

func runConfigLockfile(cmd *cobra.Command, args []string) error {
	ctx := commandContext(cmd)

	cfg, _, _, err := loadConfigForLockfileGen()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if len(args) == 0 {
		listLockfileApps(cfg.Apps)
		return nil
	}

	appName := args[0]

	app, ok := cfg.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found in configuration", appName)
	}

	printAppInfo(appName, app)

	if app.Binary != nil || app.Shell != nil || app.Jvm != nil {
		appType := "binary"
		if app.Shell != nil {
			appType = "shell"
		} else if app.Jvm != nil {
			appType = "jvm"
		}
		return fmt.Errorf("app %q does not support lock files (%s apps have no dependency manifest)", appName, appType)
	}

	if app.Bun == nil && app.Node == nil && app.Uv == nil && app.Go == nil {
		return fmt.Errorf("app %q has no valid runtime configuration", appName)
	}

	freshApps := clearAppLockFile(cfg.Apps, appName)
	rm := runtimemanager.New(cfg.Runtimes)
	lockContent, err := rm.GenerateLock(ctx, appName, freshApps[appName])
	if err != nil {
		return fmt.Errorf("failed to generate lock file for %q: %w", appName, err)
	}

	compressed, err := runtimemanager.CompressLockFile(lockContent)
	if err != nil {
		return fmt.Errorf("failed to compress lock file: %w", err)
	}

	jsonBytes, err := json.Marshal(compressed)
	if err != nil {
		return fmt.Errorf("failed to marshal lock file content: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nLock file content for %q:\n\n", appName)
	fmt.Println(string(jsonBytes))

	return nil
}

// clearAppLockFile returns a shallow copy of apps where the named app has its
// Bun/Node/UV/Go LockFile field cleared. The original map and runtime configs are not
// mutated. App.Files (including any "pnpm-workspace.yaml" entry used to
// configure allowBuilds) and App.Archives are preserved so the reinstall can
// generate a fresh lock file under the same workspace policy as a normal run.
func clearAppLockFile(apps binmanager.MapOfApps, appName string) binmanager.MapOfApps {
	fresh := make(binmanager.MapOfApps, len(apps))
	maps.Copy(fresh, apps)
	appCopy, ok := fresh[appName]
	if !ok {
		return fresh
	}
	if appCopy.Node != nil {
		nodeCopy := *appCopy.Node
		nodeCopy.LockFile = ""
		appCopy.Node = &nodeCopy
	}
	if appCopy.Bun != nil {
		bunCopy := *appCopy.Bun
		bunCopy.LockFile = ""
		appCopy.Bun = &bunCopy
	}
	if appCopy.Uv != nil {
		uvCopy := *appCopy.Uv
		uvCopy.LockFile = ""
		appCopy.Uv = &uvCopy
	}
	if appCopy.Go != nil {
		goCopy := *appCopy.Go
		goCopy.LockFile = ""
		appCopy.Go = &goCopy
	}
	fresh[appName] = appCopy
	return fresh
}

func printAppInfo(appName string, app binmanager.App) {
	fmt.Fprintf(os.Stderr, "App: %s\n", appName)

	switch {
	case app.Bun != nil:
		fmt.Fprintf(os.Stderr, "  Runtime:      bun\n")
		fmt.Fprintf(os.Stderr, "  Package:      %s\n", app.Bun.PackageName)
		fmt.Fprintf(os.Stderr, "  Version:      %s\n", app.Bun.Version)
		if len(app.Bun.Dependencies) > 0 {
			fmt.Fprintf(os.Stderr, "  Dependencies:\n")
			keys := make([]string, 0, len(app.Bun.Dependencies))
			for k := range app.Bun.Dependencies {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(os.Stderr, "    %s: %s\n", k, app.Bun.Dependencies[k])
			}
		}
	case app.Node != nil:
		fmt.Fprintf(os.Stderr, "  Runtime:      node\n")
		fmt.Fprintf(os.Stderr, "  Package:      %s\n", app.Node.PackageName)
		fmt.Fprintf(os.Stderr, "  Version:      %s\n", app.Node.Version)
		if len(app.Node.Dependencies) > 0 {
			fmt.Fprintf(os.Stderr, "  Dependencies:\n")
			keys := make([]string, 0, len(app.Node.Dependencies))
			for k := range app.Node.Dependencies {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(os.Stderr, "    %s: %s\n", k, app.Node.Dependencies[k])
			}
		}
	case app.Uv != nil:
		fmt.Fprintf(os.Stderr, "  Runtime:      uv\n")
		fmt.Fprintf(os.Stderr, "  Package:      %s\n", app.Uv.PackageName)
		fmt.Fprintf(os.Stderr, "  Version:      %s\n", app.Uv.Version)
	case app.Go != nil:
		fmt.Fprintf(os.Stderr, "  Runtime:      go\n")
		fmt.Fprintf(os.Stderr, "  Package:      %s\n", app.Go.PackageName)
		fmt.Fprintf(os.Stderr, "  Version:      %s\n", app.Go.Version)
	case app.Jvm != nil:
		fmt.Fprintf(os.Stderr, "  Runtime:      jvm\n")
		fmt.Fprintf(os.Stderr, "  Version:      %s\n", app.Jvm.Version)
	case app.Binary != nil:
		fmt.Fprintf(os.Stderr, "  Runtime:      binary\n")
	case app.Shell != nil:
		fmt.Fprintf(os.Stderr, "  Runtime:      shell\n")
		fmt.Fprintf(os.Stderr, "  Command:      %s\n", app.Shell.Name)
	}

	fmt.Fprintln(os.Stderr)
}

func listLockfileApps(apps binmanager.MapOfApps) {
	var bunApps, nodeApps, uvApps, goApps []string

	for name, app := range apps {
		switch {
		case app.Bun != nil:
			bunApps = append(bunApps, name)
		case app.Node != nil:
			nodeApps = append(nodeApps, name)
		case app.Uv != nil:
			uvApps = append(uvApps, name)
		case app.Go != nil:
			goApps = append(goApps, name)
		}
	}

	sort.Strings(bunApps)
	sort.Strings(nodeApps)
	sort.Strings(uvApps)
	sort.Strings(goApps)

	if len(bunApps) == 0 && len(nodeApps) == 0 && len(uvApps) == 0 && len(goApps) == 0 {
		fmt.Fprintln(os.Stderr, "No apps with lock file support found.")
		return
	}

	fmt.Fprintln(os.Stderr, "Apps with lock file support:")
	if len(bunApps) > 0 {
		fmt.Fprintln(os.Stderr, "\n  bun:")
		for _, name := range bunApps {
			fmt.Fprintf(os.Stderr, "    %s\n", name)
		}
	}

	if len(nodeApps) > 0 {
		fmt.Fprintln(os.Stderr, "\n  node:")
		for _, name := range nodeApps {
			fmt.Fprintf(os.Stderr, "    %s\n", name)
		}
	}

	if len(uvApps) > 0 {
		fmt.Fprintln(os.Stderr, "\n  uv:")
		for _, name := range uvApps {
			fmt.Fprintf(os.Stderr, "    %s\n", name)
		}
	}

	if len(goApps) > 0 {
		fmt.Fprintln(os.Stderr, "\n  go:")
		for _, name := range goApps {
			fmt.Fprintf(os.Stderr, "    %s\n", name)
		}
	}

	fmt.Fprintf(os.Stderr, "\nUsage: datamitsu config lockfile <appName>\n")
}
