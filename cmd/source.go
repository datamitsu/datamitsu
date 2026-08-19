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
	ctx := commandContext(cmd)
	stderr := cmd.ErrOrStderr()

	plan, fresh, err := freshSourcePlan(ctx)
	if err != nil {
		return err
	}
	if !fresh {
		res, err := bakeSourceFarm(ctx, stderr)
		if err != nil {
			return err
		}
		reportBakeFailure(stderr, res)
		plan = res.Plan
	}

	warnSourceFarm(stderr, plan)
	if _, err := fmt.Fprint(cmd.OutOrStdout(), render(plan)); err != nil {
		return fmt.Errorf("write activation code: %w", err)
	}
	return nil
}

// freshSourcePlan returns the farm the on-disk manifest already describes, when
// that manifest still matches the tree.
//
// Activation lives in a shell rc file, so every new shell and every tmux pane
// pays it. Re-resolving the whole config there costs an order of magnitude more
// than reading the manifest back, for an answer that is identical whenever the
// manifest is fresh — and fresh is the steady state, because a tool invocation
// re-bakes on its own as soon as the tree changes.
//
// Installed flags come from bake time rather than a fresh stat, so a store entry
// deleted since the bake is reported as present. That only affects the "not
// downloaded yet" warning: the shim stats the target itself and installs on
// demand. `source status` is the command that answers with what is true now, and
// it deliberately keeps resolving.
func freshSourcePlan(ctx context.Context) (sourcefarm.Plan, bool, error) {
	root, err := sourceProjectRoot(ctx)
	if err != nil {
		return sourcefarm.Plan{}, false, err
	}
	if !sourceManifestDecides() {
		return sourcefarm.Plan{}, false, nil
	}
	plan, fresh := freshSourcePlanFor(root)
	return plan, fresh, nil
}

// freshSourcePlanFor is freshSourcePlan once the root is known.
//
// It cannot fail, which is why it returns no error. Every state that is not
// "fresh, with the farm it describes still on disk" — no manifest, one that will
// not decode, an aged-out watch set, a deleted farm — means the same thing to the
// caller and gets the same answer: false, go resolve the config and bake.
func freshSourcePlanFor(root string) (sourcefarm.Plan, bool) {
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		return sourcefarm.Plan{}, false
	}
	m, err := sourcefarm.Load(manifestPath)
	if err != nil || !sourcefarm.Validate(m) {
		return sourcefarm.Plan{}, false
	}
	// A manifest whose farm has been deleted out from under it is fresh by the
	// watch set and useless in practice: activating it would put a directory that
	// does not exist on PATH.
	if info, err := os.Stat(m.FarmDir); err != nil || !info.IsDir() {
		return sourcefarm.Plan{}, false
	}

	return sourcefarm.Plan{
		Root:     m.Root,
		FarmDir:  m.FarmDir,
		Entries:  m.Entries,
		Excluded: m.Excluded,
		Shadowed: m.Shadowed,
	}, true
}

// sourceManifestDecides reports whether the on-disk manifest may answer for the
// current invocation.
//
// It may not when the config came from a flag. The manifest's watch set records
// the files of the config chain that baked it, so an explicit --config naming a
// different file compares equal against a watch set that never mentioned it —
// the freshness check would report "unchanged" for a config it has never seen.
// Those invocations always re-resolve.
func sourceManifestDecides() bool {
	return len(ConfigPaths) == 0 && !NoAutoConfig
}

// reportBakeFailure tells the user that the farm they are activating is the
// previous one, not the one this command just tried to write.
func reportBakeFailure(stderr io.Writer, res bakeResult) {
	if res.MaterializeErr == nil {
		return
	}
	_, _ = fmt.Fprintf(stderr, "%s: could not re-bake the farm, activating the one already on disk: %v\n",
		ldflags.PackageName, res.MaterializeErr)
}

// bakeResult is what a bake produced: the plan, plus a materialization failure
// that was survivable.
type bakeResult struct {
	Plan sourcefarm.Plan

	// MaterializeErr is a materialization failure the caller survived because a
	// usable farm was already on disk. It is reported, never fatal — when there
	// was nothing to fall back on, bakeSourceFarm returns an error instead.
	MaterializeErr error
}

// bakeSourceFarm resolves the project's declared apps and materializes the farm,
// returning the plan it wrote.
//
// A materialization failure is not fatal *when a previous farm is still on disk*:
// that farm still works, so activation continues after one line on stderr. That
// is the same rule sourcefarm itself follows — an empty farm would turn every
// declared tool into an exit-127 across every shell on the machine.
//
// With no farm to fall back on the rule inverts and the failure is fatal.
// Emitting activation code for a farm directory that does not exist would exit 0
// after prepending nothing, and every declared tool would then resolve through
// the rest of PATH to whatever the system happens to have — the silent
// wrong-binary failure the farm exists to prevent, arriving through the
// activation door. In fish it is quieter still: fish_add_path skips a
// non-existent directory without a word.
func bakeSourceFarm(ctx context.Context, stderr io.Writer) (bakeResult, error) {
	plan, err := resolveSourcePlan(ctx)
	if err != nil {
		return bakeResult{}, err
	}

	m := sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot,
		sourcefarm.WatchPaths(plan.Root, ConfigChainFiles()))

	// sourcefarm already reports the failure on the writer it was given.
	matErr := sourcefarm.MaterializeWithOptions(plan, m, sourcefarm.Options{
		Warn: func(line string) { _, _ = fmt.Fprintln(stderr, line) },
	})
	if matErr != nil && !farmOnDisk(plan) {
		return bakeResult{}, fmt.Errorf("failed to bake the source farm for %s, and no previous farm is usable: %w", plan.Root, matErr)
	}

	return bakeResult{Plan: plan, MaterializeErr: matErr}, nil
}

// farmOnDisk reports whether a previously baked farm for this plan is still
// usable: the farm directory is there and its manifest still decodes.
func farmOnDisk(plan sourcefarm.Plan) bool {
	info, err := os.Stat(plan.FarmDir)
	if err != nil || !info.IsDir() {
		return false
	}
	manifestPath, err := env.GetProjectManifestPath(plan.Root)
	if err != nil {
		return false
	}
	_, err = sourcefarm.Load(manifestPath)
	return err == nil
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

	// --no-auto-config with nothing to replace it does not mean "activate the
	// embedded default config". It would bake five built-in apps into the farm
	// this root's real config owns, replacing it at the same path — every
	// already-activated shell for this root would then get exit 127 for every
	// tool the project actually declares.
	if NoAutoConfig {
		return "", fmt.Errorf("%s source cannot use --no-auto-config without a config.\n"+
			"Drop the flag to use the config at %s, or pass one explicitly:\n"+
			"  %s --config /path/to/%s.config.ts source bash",
			ldflags.PackageName, root, ldflags.PackageName, ldflags.PackageName)
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
