package cmd

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
)

var configLockfileCmd = &cobra.Command{
	Use:   "lockfile [appName]",
	Short: "Generate lock file content for a runtime-managed app",
	Long: `Reinstalls a runtime-managed app (fnm/uv) and outputs its lock file content
as a JSON-escaped string ready to paste into configuration.

When called without arguments, lists all apps that support lock files (fnm/uv).

This command:
1. Deletes the app's cache directory
2. Reinstalls the app from scratch
3. Reads the generated lock file (pnpm-lock.yaml or uv.lock)
4. Outputs the content as a JSON string for use in lockFile config field`,
	Args: cobra.RangeArgs(0, 1),
	RunE: runConfigLockfile,
}

func init() {
	configCmd.AddCommand(configLockfileCmd)
}

func runConfigLockfile(cmd *cobra.Command, args []string) error {
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

	if app.Fnm == nil && app.Uv == nil {
		return fmt.Errorf("app %q has no valid runtime configuration", appName)
	}

	// Clear lock fields so the reinstall generates a fresh lock file
	// instead of reusing the existing one with --locked mode.
	freshApps := make(binmanager.MapOfApps, len(cfg.Apps))
	for k, v := range cfg.Apps {
		freshApps[k] = v
	}
	appCopy := freshApps[appName]
	if appCopy.Fnm != nil {
		fnmCopy := *appCopy.Fnm
		fnmCopy.LockFile = ""
		appCopy.Fnm = &fnmCopy
	}
	if appCopy.Uv != nil {
		uvCopy := *appCopy.Uv
		uvCopy.LockFile = ""
		appCopy.Uv = &uvCopy
	}
	freshApps[appName] = appCopy

	// Delete old cache (computed from original config with lock fields)
	rm := runtimemanager.New(cfg.Runtimes)
	origBinMgr := binmanager.New(cfg.Apps, cfg.Bundles, rm)
	if origInstallPath, err := origBinMgr.ComputeInstallPath(appName); err == nil {
		_ = os.RemoveAll(origInstallPath)
	}

	// Also delete cache for the fresh (lockfile-cleared) config
	freshRM := runtimemanager.New(cfg.Runtimes)
	freshBinMgr := binmanager.New(freshApps, cfg.Bundles, freshRM)

	freshInstallPath, err := freshBinMgr.ComputeInstallPath(appName)
	if err != nil {
		return fmt.Errorf("failed to compute install path for %q: %w", appName, err)
	}

	fmt.Fprintf(os.Stderr, "Removing cache at %s...\n", freshInstallPath)
	if err := os.RemoveAll(freshInstallPath); err != nil {
		return fmt.Errorf("failed to remove cache directory: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Reinstalling %s...\n", appName)

	if _, err := freshBinMgr.GetCommandInfo(appName); err != nil {
		return fmt.Errorf("failed to reinstall %q: %w", appName, err)
	}

	lockContent, err := readLockFile(freshInstallPath, app)
	if err != nil {
		return err
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

func printAppInfo(appName string, app binmanager.App) {
	fmt.Fprintf(os.Stderr, "App: %s\n", appName)

	if app.Fnm != nil {
		fmt.Fprintf(os.Stderr, "  Runtime:      fnm\n")
		fmt.Fprintf(os.Stderr, "  Package:      %s\n", app.Fnm.PackageName)
		fmt.Fprintf(os.Stderr, "  Version:      %s\n", app.Fnm.Version)
		if len(app.Fnm.Dependencies) > 0 {
			fmt.Fprintf(os.Stderr, "  Dependencies:\n")
			keys := make([]string, 0, len(app.Fnm.Dependencies))
			for k := range app.Fnm.Dependencies {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(os.Stderr, "    %s: %s\n", k, app.Fnm.Dependencies[k])
			}
		}
	} else if app.Uv != nil {
		fmt.Fprintf(os.Stderr, "  Runtime:      uv\n")
		fmt.Fprintf(os.Stderr, "  Package:      %s\n", app.Uv.PackageName)
		fmt.Fprintf(os.Stderr, "  Version:      %s\n", app.Uv.Version)
	} else if app.Jvm != nil {
		fmt.Fprintf(os.Stderr, "  Runtime:      jvm\n")
		fmt.Fprintf(os.Stderr, "  Version:      %s\n", app.Jvm.Version)
	} else if app.Binary != nil {
		fmt.Fprintf(os.Stderr, "  Runtime:      binary\n")
	} else if app.Shell != nil {
		fmt.Fprintf(os.Stderr, "  Runtime:      shell\n")
		fmt.Fprintf(os.Stderr, "  Command:      %s\n", app.Shell.Name)
	}

	fmt.Fprintln(os.Stderr)
}

func listLockfileApps(apps binmanager.MapOfApps) {
	var fnmApps, uvApps []string

	for name, app := range apps {
		if app.Fnm != nil {
			fnmApps = append(fnmApps, name)
		} else if app.Uv != nil {
			uvApps = append(uvApps, name)
		}
	}

	sort.Strings(fnmApps)
	sort.Strings(uvApps)

	if len(fnmApps) == 0 && len(uvApps) == 0 {
		fmt.Fprintln(os.Stderr, "No apps with lock file support found.")
		return
	}

	fmt.Fprintln(os.Stderr, "Apps with lock file support:")

	if len(fnmApps) > 0 {
		fmt.Fprintln(os.Stderr, "\n  fnm:")
		for _, name := range fnmApps {
			fmt.Fprintf(os.Stderr, "    %s\n", name)
		}
	}

	if len(uvApps) > 0 {
		fmt.Fprintln(os.Stderr, "\n  uv:")
		for _, name := range uvApps {
			fmt.Fprintf(os.Stderr, "    %s\n", name)
		}
	}

	fmt.Fprintf(os.Stderr, "\nUsage: datamitsu config lockfile <appName>\n")
}

func readLockFile(installPath string, app binmanager.App) (string, error) {
	var lockFilePath string

	if app.Fnm != nil {
		lockFilePath = filepath.Join(installPath, "pnpm-lock.yaml")
	} else if app.Uv != nil {
		lockFilePath = filepath.Join(installPath, "uv.lock")
	} else {
		return "", fmt.Errorf("unsupported app type for lock file generation")
	}

	fmt.Fprintf(os.Stderr, "Lock file: %s\n", lockFilePath)

	data, err := os.ReadFile(lockFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to read lock file at %s: %w", lockFilePath, err)
	}

	return string(data), nil
}
