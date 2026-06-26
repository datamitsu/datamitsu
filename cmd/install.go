package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ocibundle"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/ui"

	"github.com/spf13/cobra"
)

var (
	installRuntimeNames []string
	installNoVerify     bool
)

var installCmd = &cobra.Command{
	Use:   "install [app...]",
	Short: "Install managed apps and/or runtimes without executing them",
	Long: `Install one or more managed apps (and optionally runtimes) into the store
without running them.

Unlike "exec", which prepares and then runs a tool, "install" materializes a
tool's files and exits. It is the building block for the per-tool and per-runtime
stages of a generated Dockerfile (see "devtools dockerfile"), where each stage
installs exactly one target so it becomes an independently cacheable layer.

By default each installed app's version check is run after installation, so a
broken or non-functional install fails the command (and, in a Docker build, the
build) rather than silently producing a broken artifact. Pass --no-verify to skip
this check.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runInstall(commandContext(cmd), args, installRuntimeNames, !installNoVerify)
	},
}

func init() {
	installCmd.Flags().StringSliceVar(&installRuntimeNames, "runtime", nil,
		"Install the named runtime(s) only, without any app (repeatable)")
	installCmd.Flags().BoolVar(&installNoVerify, "no-verify", false,
		"Skip the post-install version check (verification runs by default)")
	rootCmd.AddCommand(installCmd)
}

func runInstall(ctx context.Context, apps, runtimes []string, verify bool) error {
	if len(apps) == 0 && len(runtimes) == 0 {
		return errors.New("specify at least one app name or --runtime <name>")
	}

	cfg, _, _, err := loadConfigWithPaths(ctx, BeforeConfigPaths, NoAutoConfig, ConfigPaths)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	rm := runtimemanager.New(cfg.Runtimes)
	binMgr := binmanager.New(cfg.Apps, cfg.Bundles, rm)

	// Seed the requested targets from the declared OCI bundle first so the
	// installs below hit seeded store content instead of the network.
	if err := ocibundle.AutoSeed(ctx, cfg, apps, runtimes); err != nil {
		return err
	}

	if err := installUnderDisplay(ctx, rm, binMgr, cfg.Apps, apps, runtimes); err != nil {
		return err
	}

	// Verification runs after the progress display is torn down so each tool's
	// version command gets a clean terminal (its output is discarded anyway).
	if verify && len(apps) > 0 {
		return verifyInstalledApps(ctx, binMgr, cfg.Apps, apps)
	}
	return nil
}

// installUnderDisplay installs the requested runtimes and apps while the shared
// ui progress display is active, tearing it down before returning.
func installUnderDisplay(ctx context.Context, rm *runtimemanager.RuntimeManager, binMgr *binmanager.BinManager, allApps binmanager.MapOfApps, apps, runtimes []string) error {
	d := ui.New(term.DetectMode())
	restore := ui.Activate(d)
	defer func() {
		d.Close()
		restore()
	}()

	if len(runtimes) > 0 {
		stats, rErr := rm.InstallRuntimes(ctx, runtimes, env.GetConcurrency())
		if rErr != nil {
			return fmt.Errorf("failed to install runtimes: %w", rErr)
		}
		if len(stats.Failed) > 0 {
			first := stats.Failed[0]
			return fmt.Errorf("failed to install runtime %q: %w", first.Name, first.Error)
		}
	}

	if len(apps) > 0 {
		warnShellApps(allApps, apps)
		if err := installSmartInitApps(ctx, binMgr, apps); err != nil {
			return err
		}
	}
	return nil
}

// verifyInstalledApps runs each app's version check and fails on the first app
// whose command cannot be prepared or exits non-zero.
func verifyInstalledApps(ctx context.Context, binMgr *binmanager.BinManager, allApps binmanager.MapOfApps, names []string) error {
	for _, name := range names {
		app, ok := allApps[name]
		if !ok {
			continue // unknown app already surfaced by the install pass
		}
		args, verifiable := versionCheckArgs(app)
		if !verifiable {
			if !ui.Quiet() {
				fmt.Fprintf(os.Stderr, "Note: %q has no runnable version check; skipping verify\n", name)
			}
			continue
		}

		cmd, err := binMgr.GetExecCmd(ctx, name, args)
		if err != nil {
			return fmt.Errorf("verify %s: %w", name, err)
		}
		if cmd == nil {
			continue // shell-resolved app, nothing to run
		}

		var stderr bytes.Buffer
		cmd.Stdout = io.Discard
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return fmt.Errorf("verify %s: %q failed: %w: %s", name, strings.Join(args, " "), err, msg)
			}
			return fmt.Errorf("verify %s: %q failed: %w", name, strings.Join(args, " "), err)
		}
	}
	return nil
}

// versionCheckArgs returns the args to run for an app's version check and whether
// the app is verifiable at all. It mirrors verify-all's defaulting: shell apps
// and disabled checks are not verifiable; otherwise VersionCheck.Args (if set)
// or "--version".
func versionCheckArgs(app binmanager.App) (args []string, verifiable bool) {
	if app.Shell != nil {
		return nil, false
	}
	if app.VersionCheck != nil && app.VersionCheck.Disabled {
		return nil, false
	}
	if app.VersionCheck != nil && len(app.VersionCheck.Args) > 0 {
		return app.VersionCheck.Args, true
	}
	return []string{"--version"}, true
}

// warnShellApps notes that shell apps resolve to a system command and install
// nothing, so naming one in `install` is a likely mistake.
func warnShellApps(allApps binmanager.MapOfApps, names []string) {
	for _, name := range names {
		if app, ok := allApps[name]; ok && app.Shell != nil && !ui.Quiet() {
			fmt.Fprintf(os.Stderr, "Warning: %q is a shell command; nothing to install\n", name)
		}
	}
}
