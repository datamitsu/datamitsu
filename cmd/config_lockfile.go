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
	Long: `Reinstalls a runtime-managed app (fnm/uv/go) and outputs its lock file content
as a JSON-escaped string ready to paste into configuration.

When called without arguments, lists all apps that support lock files (fnm/uv/go).

This command:
1. Deletes the app's cache directory
2. Reinstalls the app from scratch (for go, resolves deps with go mod init + go get)
3. Reads the generated lock file (pnpm-lock.yaml, uv.lock, or go.mod + go.sum)
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

	if app.Fnm == nil && app.Uv == nil && app.Go == nil {
		return fmt.Errorf("app %q has no valid runtime configuration", appName)
	}

	freshApps := clearAppLockFile(cfg.Apps, appName)

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

	var lockContent string
	if app.Go != nil {
		// Go apps cannot regenerate a lockfile via reinstall: the build is
		// mandatory-lockfile and refuses without one. Resolve dependencies with
		// `go mod init` + `go get` in an isolated temp workdir — generation pulls
		// 100+MiB of module cache we must not leave behind in the install path —
		// then read go.mod + go.sum back from there.
		lockContent, err = generateGoLockContent(appName, app, func(workDir string) error {
			return freshRM.GenerateGoLockFiles(appName, freshApps[appName].Go, workDir)
		})
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "Reinstalling %s...\n", appName)
		if _, err := freshBinMgr.GetCommandInfo(appName); err != nil {
			return fmt.Errorf("failed to reinstall %q: %w", appName, err)
		}
		lockContent, err = readLockFile(freshInstallPath, app)
		if err != nil {
			return err
		}
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

// generateGoLockContent resolves a Go app's dependencies in an isolated temp
// workdir via the supplied generate function, reads the produced go.mod + go.sum
// back as a lockfile, and removes the workdir before returning. Generation pulls
// a large module cache into the workdir, so the cleanup runs on both the success
// and failure paths (via defer) rather than leaking it into the cache directory.
func generateGoLockContent(appName string, app binmanager.App, generate func(workDir string) error) (string, error) {
	workDir, err := os.MkdirTemp("", "datamitsu-go-lockfile-*")
	if err != nil {
		return "", fmt.Errorf("failed to allocate temp workdir: %w", err)
	}
	// Generation sets GOMODCACHE under workDir, which `go get` fills with
	// read-only files; a plain os.RemoveAll fails on those, so use
	// ForceRemoveAll and surface (rather than swallow) any cleanup failure.
	defer func() {
		if err := runtimemanager.ForceRemoveAll(workDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove temp workdir %s: %v\n", workDir, err)
		}
	}()

	fmt.Fprintf(os.Stderr, "Generating lock file for %s...\n", appName)
	if err := generate(workDir); err != nil {
		return "", fmt.Errorf("failed to generate lock file for %q: %w", appName, err)
	}

	return readLockFile(workDir, app)
}

// clearAppLockFile returns a shallow copy of apps where the named app has its
// FNM/UV/Go LockFile field cleared. The original map and runtime configs are not
// mutated. App.Files (including any "pnpm-workspace.yaml" entry used to
// configure allowBuilds) and App.Archives are preserved so the reinstall can
// generate a fresh lock file under the same workspace policy as a normal run.
func clearAppLockFile(apps binmanager.MapOfApps, appName string) binmanager.MapOfApps {
	fresh := make(binmanager.MapOfApps, len(apps))
	for k, v := range apps {
		fresh[k] = v
	}
	appCopy, ok := fresh[appName]
	if !ok {
		return fresh
	}
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
	} else if app.Go != nil {
		fmt.Fprintf(os.Stderr, "  Runtime:      go\n")
		fmt.Fprintf(os.Stderr, "  Package:      %s\n", app.Go.PackageName)
		fmt.Fprintf(os.Stderr, "  Version:      %s\n", app.Go.Version)
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
	var fnmApps, uvApps, goApps []string

	for name, app := range apps {
		if app.Fnm != nil {
			fnmApps = append(fnmApps, name)
		} else if app.Uv != nil {
			uvApps = append(uvApps, name)
		} else if app.Go != nil {
			goApps = append(goApps, name)
		}
	}

	sort.Strings(fnmApps)
	sort.Strings(uvApps)
	sort.Strings(goApps)

	if len(fnmApps) == 0 && len(uvApps) == 0 && len(goApps) == 0 {
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

	if len(goApps) > 0 {
		fmt.Fprintln(os.Stderr, "\n  go:")
		for _, name := range goApps {
			fmt.Fprintf(os.Stderr, "    %s\n", name)
		}
	}

	fmt.Fprintf(os.Stderr, "\nUsage: datamitsu config lockfile <appName>\n")
}

func readLockFile(installPath string, app binmanager.App) (string, error) {
	// Go apps have no single lock file: the lockfile is a JSON wrapper carrying
	// both go.mod and go.sum, so read the two files and assemble the wrapper.
	if app.Go != nil {
		goModPath := filepath.Join(installPath, "go.mod")
		goSumPath := filepath.Join(installPath, "go.sum")
		fmt.Fprintf(os.Stderr, "Lock files: %s, %s\n", goModPath, goSumPath)

		goMod, err := os.ReadFile(goModPath)
		if err != nil {
			return "", fmt.Errorf("failed to read go.mod at %s: %w", goModPath, err)
		}
		goSum, err := os.ReadFile(goSumPath)
		if err != nil {
			return "", fmt.Errorf("failed to read go.sum at %s: %w", goSumPath, err)
		}

		return runtimemanager.BuildGoLockFileJSON(string(goMod), string(goSum))
	}

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
