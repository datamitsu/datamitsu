package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/facts"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/shellquote"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"

	"github.com/spf13/cobra"
)

var sourceCmd = &cobra.Command{
	Use:   "source",
	Short: "Put the project's toolchain on PATH for this shell",
	Long: `Prints shell code that puts every tool this project declares on PATH for the
current shell session, so they can be run as ordinary commands:

  eval "$(` + ldflags.PackageName + ` source bash)"   # bash / zsh
  ` + ldflags.PackageName + ` source fish | source     # fish

Activation downloads nothing: a tool is fetched the first time it is actually
run. Switching branches needs no re-activation — the next tool invocation
notices the config changed and re-resolves itself.

stdout carries ONLY shell code. Warnings — tools not downloaded yet, system
binaries this shadows — go to stderr, so the output is always safe to eval.`,
	// Force JSON-L quiet mode on stderr for the whole process, exactly as the
	// LSP server does. stdout is a shell script being piped into eval, so no
	// progress line, no log line and no config-JS console.log may reach it.
	// Running here rather than in cobra.OnInitialize makes it unconditional:
	// it overrides --log-format.
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		ui.SetEventSink(uievent.NewJSONLSink(os.Stderr), true)
	},
	// A group command with no Run of its own prints help and exits 0, which for
	// this command means `eval "$(datamitsu source powershell)"` would evaluate a help
	// screen. Both the missing shell and the unsupported one are errors, and
	// neither writes a byte to stdout.
	Args: cobra.ArbitraryArgs,
	RunE: func(_ *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("unsupported shell %q: %s source supports bash, zsh and fish", args[0], ldflags.PackageName)
		}
		return fmt.Errorf("%s source needs a shell: bash, zsh or fish", ldflags.PackageName)
	},
}

var sourceBashCmd = &cobra.Command{
	Use:   "bash",
	Short: "Print bash activation code",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, _ []string) error { return runSource(cmd, renderBash) },
}

var sourceZshCmd = &cobra.Command{
	Use:   "zsh",
	Short: "Print zsh activation code",
	Args:  cobra.NoArgs,
	// zsh implements every construct the bash renderer uses (the ${var//pat/rep}
	// substitution with a quoted pattern, and `hash -r`), so it shares the
	// renderer rather than carrying a near-copy that can drift.
	RunE: func(cmd *cobra.Command, _ []string) error { return runSource(cmd, renderBash) },
}

var sourceFishCmd = &cobra.Command{
	Use:   "fish",
	Short: "Print fish activation code",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, _ []string) error { return runSource(cmd, renderFish) },
}

func init() {
	sourceCmd.AddCommand(sourceBashCmd)
	sourceCmd.AddCommand(sourceZshCmd)
	sourceCmd.AddCommand(sourceFishCmd)
	sourceCmd.AddCommand(sourceStatusCmd)
	sourceCmd.AddCommand(sourceRefreshCmd)
	rootCmd.AddCommand(sourceCmd)
}

// runSource bakes the farm for the current project and prints the activation
// code for one shell.
//
// It never installs anything and is safe to run repeatedly: baking is idempotent
// and the renderers remove an existing farm entry from PATH before prepending
// it, so re-activation cannot grow PATH.
func runSource(cmd *cobra.Command, render func(sourcefarm.Plan) string) error {
	plan, err := bakeSourceFarm(commandContext(cmd), cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	warnSourceFarm(cmd.ErrOrStderr(), plan)
	if _, err := fmt.Fprint(cmd.OutOrStdout(), render(plan)); err != nil {
		return fmt.Errorf("write activation code: %w", err)
	}
	return nil
}

// bakeSourceFarm resolves the project's declared apps and materializes the farm,
// returning the plan it wrote.
//
// A materialization failure is not fatal: the previous farm and manifest are
// still on disk and still work, so activation continues after one line on
// stderr. That is the same rule sourcefarm itself follows — an empty farm would
// turn every declared tool into an exit-127 across every shell on the machine.
func bakeSourceFarm(ctx context.Context, stderr io.Writer) (sourcefarm.Plan, error) {
	plan, err := resolveSourcePlan(ctx)
	if err != nil {
		return sourcefarm.Plan{}, err
	}

	m := sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot,
		sourcefarm.WatchPaths(plan.Root, ConfigChainFiles()))

	// sourcefarm already reports the failure on the writer it was given; the
	// activation itself proceeds against whatever farm is on disk.
	_ = sourcefarm.MaterializeWithOptions(plan, m, sourcefarm.Options{
		Warn: func(line string) { _, _ = fmt.Fprintln(stderr, line) },
	})

	return plan, nil
}

// resolveSourcePlan computes what the farm for the current project should
// contain, without writing a byte of it.
//
// It is the read-only half of bakeSourceFarm, split out because `source status`
// must be able to describe the farm — including apps whose store entry has since
// been deleted, which show up as Installed=false — without materializing
// anything. Resolution is side-effect free by construction: the resolver
// binmanager exposes to sourcefarm never downloads.
func resolveSourcePlan(ctx context.Context) (sourcefarm.Plan, error) {
	root, err := sourceProjectRoot(ctx)
	if err != nil {
		return sourcefarm.Plan{}, err
	}

	cfg, _, _, err := loadConfigWithPaths(ctx, BeforeConfigPaths, NoAutoConfig, ConfigPaths)
	if err != nil {
		return sourcefarm.Plan{}, fmt.Errorf("failed to load config: %w", err)
	}

	// Every app name becomes a filename in a directory that goes on PATH, so the
	// name rule is re-checked here rather than trusted from the load. Config
	// validation already applied it, which is exactly why a failure at this
	// point means the plan is about to write something the validator never saw.
	names := make([]string, 0, len(cfg.Apps))
	for name := range cfg.Apps {
		names = append(names, name)
	}
	if err := config.ValidateAppNames(names); err != nil {
		return sourcefarm.Plan{}, err
	}

	farmDir, err := env.GetProjectBinPath(root)
	if err != nil {
		return sourcefarm.Plan{}, fmt.Errorf("determine the farm directory: %w", err)
	}

	b := binmanager.New(cfg.Apps, cfg.Bundles, runtimemanager.New(cfg.Runtimes))
	return sourcefarm.BuildPlan(root, farmDir, cfg.Apps, b, exec.LookPath), nil
}

// sourceProjectRoot returns the git root whose config the farm is built from, or
// an actionable error.
//
// Config discovery is git-root-only: datamitsu stats three filenames at the git
// root and nowhere else. Outside a repository, or inside one that declares no
// config, a load would silently succeed against the embedded default config and
// activate a handful of built-in apps — output that looks like it worked and is
// not the project's toolchain. Failing here is deliberate; making that case
// produce a real farm is a separate piece of work.
func sourceProjectRoot(ctx context.Context) (string, error) {
	root, err := facts.GetGitRoot(ctx)
	if err != nil || root == "" {
		return "", fmt.Errorf("%s source needs a project: no git repository was found from this directory.\n"+
			"Run it inside a repository that has a %s config, or pass one explicitly:\n"+
			"  %s --config /path/to/%s.config.ts source bash",
			ldflags.PackageName, ldflags.PackageName, ldflags.PackageName, ldflags.PackageName)
	}

	if len(ConfigPaths) > 0 {
		// An explicit --config is the user saying which config to activate, so
		// the git root only supplies the farm's identity.
		return root, nil
	}

	discovered, err := discoverAutoConfig(root)
	if err != nil {
		return "", err
	}
	if discovered == "" {
		return "", fmt.Errorf("%s source found no config at %s.\n"+
			"Create a %s.config.ts there (see `%s init`), or pass one explicitly:\n"+
			"  %s --config /path/to/%s.config.ts source bash",
			ldflags.PackageName, root, ldflags.PackageName, ldflags.PackageName,
			ldflags.PackageName, ldflags.PackageName)
	}
	return root, nil
}

// warnSourceFarm reports what the user cannot see in the emitted shell code.
//
// It writes with fmt.Fprintf rather than through internal/ui on purpose: ui is
// in JSON-L mode for this command, and these are messages a human is meant to
// read next to their prompt.
func warnSourceFarm(stderr io.Writer, plan sourcefarm.Plan) {
	var pending []string
	for _, e := range plan.Entries {
		if !e.Installed {
			pending = append(pending, e.Name)
		}
	}
	if len(pending) > 0 {
		sort.Strings(pending)
		_, _ = fmt.Fprintf(stderr, "%s: %d tool(s) not downloaded yet, they install on first use: %s\n",
			ldflags.PackageName, len(pending), strings.Join(pending, ", "))
	}
	for _, s := range plan.Shadowed {
		_, _ = fmt.Fprintf(stderr, "%s: %s now runs this project's version, shadowing %s\n",
			ldflags.PackageName, s.Name, s.Path)
	}
}

// renderBash renders activation code for bash and zsh.
//
// Every construct is bash 3.2 — the version macOS still ships — so there are no
// associative arrays and no namerefs. PATH is assigned exactly once: the current
// value is edited in a scratch variable, the farm removed from it wherever it
// already appears, and the result written back with the farm in front. That is
// what makes re-activation idempotent instead of PATH-growing.
//
// The removal pattern is quoted inside the expansion (${p//:"$farm":/:}) so a
// farm path containing a glob character is matched literally.
func renderBash(plan sourcefarm.Plan) string {
	farm := shellquote.Bash(plan.FarmDir)
	rootVar := env.SourceRootVarName()
	farmVar := env.SourceFarmVarName()

	var b strings.Builder
	fmt.Fprintf(&b, "export %s=%s\n", rootVar, shellquote.Bash(plan.Root))
	fmt.Fprintf(&b, "export %s=%s\n", farmVar, farm)
	b.WriteString("__datamitsu_path=\":$PATH:\"\n")
	fmt.Fprintf(&b, "__datamitsu_path=\"${__datamitsu_path//:\"$%s\":/:}\"\n", farmVar)
	b.WriteString("__datamitsu_path=\"${__datamitsu_path#:}\"\n")
	b.WriteString("__datamitsu_path=\"${__datamitsu_path%:}\"\n")
	fmt.Fprintf(&b, "PATH=\"$%s${__datamitsu_path:+:$__datamitsu_path}\"\n", farmVar)
	b.WriteString("export PATH\n")
	b.WriteString("unset __datamitsu_path\n")
	// The shell caches the location of every command it has run. Without this a
	// name that resolved to /usr/local/bin before activation keeps resolving
	// there for the rest of the session — the exact silent-wrong-binary failure
	// the farm exists to prevent.
	b.WriteString("hash -r\n")
	return b.String()
}

// renderFish renders activation code for fish.
//
// fish's PATH is a list, not a colon-joined string, so the edit goes through
// fish_add_path. --move is not optional: without it, re-activating a shell that
// already has the farm on PATH silently does nothing, which would be fine today
// and wrong the moment the farm directory changes.
//
// There is no `hash -r` equivalent to emit. fish resolves commands against PATH
// per invocation and rebuilds its autoload caches when PATH is assigned, so the
// bash cache-flush line has no counterpart here.
func renderFish(plan sourcefarm.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "set -gx %s %s\n", env.SourceRootVarName(), shellquote.Fish(plan.Root))
	fmt.Fprintf(&b, "set -gx %s %s\n", env.SourceFarmVarName(), shellquote.Fish(plan.FarmDir))
	fmt.Fprintf(&b, "fish_add_path --global --move --path %s\n", shellquote.Fish(plan.FarmDir))
	return b.String()
}
