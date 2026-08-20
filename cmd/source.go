package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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

	root, err := sourceProjectRoot(ctx)
	if err != nil {
		return err
	}

	plan, fresh := freshSourcePlan(root)
	if !fresh {
		res, bakeErr := bakeSourceFarm(ctx, stderr)
		if bakeErr != nil {
			// A bake can fail before it ever reaches materialization — a config
			// that does not evaluate on this branch, a remote config that cannot
			// be fetched offline. That is the same situation materialization
			// already survives, and it gets the same answer: activate the farm
			// already on disk rather than emitting nothing. Emitting nothing
			// exits 0 with a shell that was never activated, and every declared
			// tool then resolves through the rest of PATH to whatever the system
			// has.
			previous, ok := previousSourcePlan(root)
			if !ok {
				return bakeErr
			}
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", ldflags.PackageName, bakeErr)
			reportPreviousFarm(stderr, root)
			plan = previous
		} else {
			reportBakeFailure(stderr, res)
			plan = res.Plan
		}
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
func freshSourcePlan(root string) (sourcefarm.Plan, bool) {
	if !sourceManifestDecides() {
		return sourcefarm.Plan{}, false
	}
	return freshSourcePlanFor(root)
}

// freshSourcePlanFor is freshSourcePlan once the flag question has been settled.
//
// It cannot fail, which is why it returns no error. Every state that is not
// "fresh, with the farm it describes still on disk" — no manifest, one that will
// not decode, an aged-out watch set, a deleted farm — means the same thing to the
// caller and gets the same answer: false, go resolve the config and bake.
func freshSourcePlanFor(root string) (sourcefarm.Plan, bool) {
	return loadSourcePlan(root, true)
}

// previousSourcePlan returns the farm already on disk for root, freshness aside.
//
// It is the fallback for a bake that could not be computed at all: the farm it
// describes is what every already-activated shell for this root is running, so
// it is a strictly better answer than activating nothing. The chain check still
// applies — serving a farm baked from a different config chain would activate a
// toolchain this invocation never asked for.
func previousSourcePlan(root string) (sourcefarm.Plan, bool) {
	return loadSourcePlan(root, false)
}

// loadSourcePlan reads back the farm the on-disk manifest describes for root.
//
// requireFresh distinguishes the two callers: activation's fast path only trusts
// a manifest whose watch set still matches the tree, while the failed-bake
// fallback takes a stale farm over no farm. Both reject a manifest whose farm
// directory is gone — putting a directory that does not exist on PATH activates
// nothing while reporting success — and both reject one that is not safely
// owned: neither path materializes anything, so sourcefarm's ownership and mode
// refusal would never run, and the unsafe farm would go on PATH unexamined.
//
// "Stale is fine" is not "anything is fine": the fallback still goes through
// sourcefarm.UsableStale, which is the subset of the freshness check that
// survives retirement — the schema version, the platform, and a shim target
// that is still there. Skipping it wholesale would let a manifest written in a
// format this build reads differently go on PATH precisely when nothing else
// could correct it.
func loadSourcePlan(root string, requireFresh bool) (sourcefarm.Plan, bool) {
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		return sourcefarm.Plan{}, false
	}
	m, err := sourcefarm.Load(manifestPath)
	if err != nil || !manifestChainMatches(m) {
		return sourcefarm.Plan{}, false
	}
	if requireFresh {
		if !sourcefarm.Validate(m) {
			return sourcefarm.Plan{}, false
		}
	} else if !sourcefarm.UsableStale(m) {
		return sourcefarm.Plan{}, false
	}
	if !sourcefarm.FarmUsable(m.FarmDir, "") || !farmEntriesPresent(m) {
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

// farmEntriesPresent reports whether the farm still holds a file for every entry
// the manifest describes.
//
// The manifest's freshness check watches the *tree*, not the farm, so a farm
// missing one entry — a stray `rm`, a partially restored backup, an editor that
// cleaned a directory it should not have — reads as perfectly fresh. Activating
// it exits 0 while that one name resolves through the rest of PATH to whatever
// the system has, which is the silent wrong-binary failure the farm exists to
// prevent. Nothing else can catch it: the missing entry means no shim ever runs
// for that name, so activation is the only moment it is observable.
//
// The cost is one lstat per declared app, once per shell activation, against a
// path already in the directory cache from the FarmUsable check.
func farmEntriesPresent(m sourcefarm.Manifest) bool {
	for _, e := range m.Entries {
		if _, err := os.Lstat(filepath.Join(m.FarmDir, e.Name)); err != nil {
			return false
		}
	}
	return true
}

// sourceManifestDecides reports whether the on-disk manifest may answer for the
// current invocation.
//
// It may not when the config came from a flag. The manifest's watch set records
// the files of the config chain that baked it, so an explicit --config naming a
// different file compares equal against a watch set that never mentioned it —
// the freshness check would report "unchanged" for a config it has never seen.
// Those invocations always re-resolve.
//
// --before-config is on the same footing as --config: it prepends files to the
// chain, so a manifest baked without them describes a farm missing whatever they
// declare. Serving it back as fresh would activate the wrong toolchain silently.
func sourceManifestDecides() bool {
	return len(ConfigPaths) == 0 && len(BeforeConfigPaths) == 0 && !NoAutoConfig
}

// manifestChainMatches reports whether m was baked from the same config chain
// this invocation selected.
//
// sourceManifestDecides covers one direction — a flagged invocation must not be
// answered by a manifest baked without those flags — and this covers the
// inverse, which is the one that fails silently. A flagged bake writes the farm
// at the *root's* path (the farm's identity is the git root, not the chain) and
// records a watch set that includes the flag's own config file, so every stat
// tuple in it compares equal afterwards. Without this check a later plain
// `source bash` in the same repository is served that other chain's farm and
// reports success, plain `source refresh` answers "already up to date", and the
// shim keeps replaying the recorded flags forever.
//
// Comparing the rendered argv fragment rather than the flag slices is what makes
// the two sides commensurable: both go through configChainArgs, so the recorded
// absolute paths are compared against absolute paths.
func manifestChainMatches(m sourcefarm.Manifest) bool {
	return slices.Equal(m.ConfigArgs, configChainArgs())
}

// configChainArgs reconstructs the global flags that selected this invocation's
// config chain, as an argv fragment the shim can replay.
//
// Paths are made absolute because the fragment is replayed from whatever
// directory the shim happened to be invoked in, which is rarely the one that
// baked the farm. A path that cannot be made absolute is passed through
// unchanged: os.Getwd fails only when the working directory has been removed,
// and the relative path is a strictly better answer than dropping the flag and
// re-baking a different chain.
func configChainArgs() []string {
	args := make([]string, 0, 2*(len(BeforeConfigPaths)+len(ConfigPaths))+1)
	if NoAutoConfig {
		args = append(args, "--no-auto-config")
	}
	for _, p := range BeforeConfigPaths {
		args = append(args, "--before-config", absOrSelf(p))
	}
	for _, p := range ConfigPaths {
		args = append(args, "--config", absOrSelf(p))
	}
	return args
}

// absOrSelf returns path made absolute, or path unchanged when it cannot be.
func absOrSelf(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// reportBakeFailure tells the user that the farm they are activating is the
// previous one, not the one this command just tried to write.
//
// sourcefarm has already put the failure itself on stderr through Options.Warn,
// and it words that line more precisely than this can — it is the only side that
// knows whether the new farm was swapped in before the failure. So this adds the
// consequence and does not repeat the error.
func reportBakeFailure(stderr io.Writer, res bakeResult) {
	if res.MaterializeErr == nil {
		return
	}
	reportPreviousFarm(stderr, res.Plan.Root)
}

// reportPreviousFarm is the one line that tells the user which farm they ended
// up with when the intended bake did not happen.
func reportPreviousFarm(stderr io.Writer, root string) {
	_, _ = fmt.Fprintf(stderr, "%s: activating the farm already on disk for %s\n", ldflags.PackageName, root)
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
	// Snapshot the watch set before the config is read, not only after. An edit
	// that lands while the config is being evaluated — a `git checkout` in
	// another terminal, an editor saving — is otherwise stat'ed *after* it
	// happened and recorded as the state this farm was built from, so the next
	// freshness check compares equal and the farm serves the previous branch's
	// toolchain forever, silently. Taking the earlier tuple makes that case read
	// as stale and re-bake, which is the failure direction that self-corrects.
	// The git root is cached by facts, so asking for it again costs nothing.
	var prior []sourcefarm.WatchFile
	if root, rootErr := sourceProjectRoot(ctx); rootErr == nil {
		prior = sourcefarm.WatchSet(sourcefarm.WatchPaths(root, nil))
	}

	plan, err := resolveSourcePlan(ctx)
	if err != nil {
		return bakeResult{}, err
	}

	watch := watchSetSince(prior, sourcefarm.WatchSet(sourcefarm.WatchPaths(plan.Root, ConfigChainFiles())))
	m := sourcefarm.BuildManifest(plan, sourcefarm.OriginGitRoot, watch)
	m.ConfigArgs = configChainArgs()

	// sourcefarm already reports the failure on the writer it was given.
	matErr := sourcefarm.MaterializeWithOptions(plan, m, sourcefarm.Options{
		Warn: func(line string) { _, _ = fmt.Fprintln(stderr, line) },
	})
	if matErr != nil && !farmOnDisk(plan) {
		return bakeResult{}, fmt.Errorf("failed to bake the source farm for %s, and no previous farm is usable: %w", plan.Root, matErr)
	}

	return bakeResult{Plan: plan, MaterializeErr: matErr}, nil
}

// watchSetSince returns current, with any tuple prior recorded differently
// replaced by prior's.
//
// prior is the pre-load snapshot and current the post-load one, so a path they
// disagree about is a file that changed while the config was being evaluated.
// Recording the older tuple is what makes the manifest report itself stale on
// the next check; recording the newer one would claim the farm already reflects
// an edit it never saw. Paths prior does not cover — chain files discovered by
// the load itself — pass through unchanged; they are the smaller window, since
// the load has to find a file before it can read it.
func watchSetSince(prior, current []sourcefarm.WatchFile) []sourcefarm.WatchFile {
	if len(prior) == 0 {
		return current
	}
	byPath := make(map[string]sourcefarm.WatchFile, len(prior))
	for _, w := range prior {
		byPath[w.Path] = w
	}
	out := make([]sourcefarm.WatchFile, len(current))
	for i, w := range current {
		if earlier, ok := byPath[w.Path]; ok {
			out[i] = earlier
			continue
		}
		out[i] = w
	}
	return out
}

// farmOnDisk reports whether a previously baked farm for this plan is still
// usable: the farm directory passes the same ownership and mode checks
// materialization enforces, and its manifest still decodes.
//
// The safety half is not decoration. This function is what decides whether a
// materialization failure is survivable, and one of the failures it must not
// survive is materialization's own refusal to touch a group-writable or
// foreign-owned farm. Answering "usable" on directory-exists alone would turn
// that refusal into "activate it anyway", which is the one outcome the refusal
// exists to prevent.
func farmOnDisk(plan sourcefarm.Plan) bool {
	if !sourcefarm.FarmUsable(plan.FarmDir, "") {
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
	return sourcefarm.BuildPlan(root, farmDir, cfg.Apps, b, sourcefarm.SystemLookPath(farmDir)), nil
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
