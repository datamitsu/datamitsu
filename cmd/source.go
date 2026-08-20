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

With --config the toolchain comes from a config you name instead of from a
project, so it works outside any repository — a machine-level toolchain for a
shell rc file:

  ` + ldflags.PackageName + ` source fish --config ~/.config/` + ldflags.PackageName + `/` + ldflags.PackageName + `.config.ts | source

Such a farm never evaluates a project's config: entering a repository's
toolchain stays an explicit ` + ldflags.PackageName + ` source in that repository.

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
func runSource(cmd *cobra.Command, render func(sourceActivation) string) error {
	ctx := commandContext(cmd)
	stderr := cmd.ErrOrStderr()

	target, err := resolveSourceTarget(ctx)
	if err != nil {
		return err
	}

	plan, fresh := freshSourcePlan(target)
	if !fresh {
		res, bakeErr := bakeSourceFarm(ctx, stderr, target)
		if bakeErr != nil {
			// A bake can fail before it ever reaches materialization — a config
			// that does not evaluate on this branch, a remote config that cannot
			// be fetched offline. That is the same situation materialization
			// already survives, and it gets the same answer: activate the farm
			// already on disk rather than emitting nothing. Emitting nothing
			// exits 0 with a shell that was never activated, and every declared
			// tool then resolves through the rest of PATH to whatever the system
			// has.
			previous, ok := previousSourcePlan(target)
			if !ok {
				return bakeErr
			}
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", ldflags.PackageName, bakeErr)
			reportPreviousFarm(stderr, target)
			plan = previous
		} else {
			reportBakeFailure(stderr, res, target)
			plan = res.Plan
		}
	}

	warnSourceFarm(stderr, plan)
	if _, err := fmt.Fprint(cmd.OutOrStdout(), render(activationFor(target, plan))); err != nil {
		return fmt.Errorf("write activation code: %w", err)
	}
	return nil
}

// sourceTarget is the farm one `source` invocation acts on: which origin it
// has, where its identity comes from, and the two paths derived from that
// identity.
//
// It exists because the two origins answer "which farm?" from different inputs —
// a git root discovered from the working directory, or the config chain the user
// named — while every step afterwards (freshness, bake, activation, status,
// refresh) is identical. Resolving the question once, at the top of each
// command, is what keeps that difference from being re-decided in five places.
type sourceTarget struct {
	// Origin is the origin recorded in the manifest this target writes.
	Origin sourcefarm.Origin

	// Root is the authoritative git root, and is empty for an explicit-config
	// target. Nothing synthetic is put here; see sourcefarm.Manifest.Root.
	Root string

	// ConfigPaths is the resolved, absolute, ordered config chain an
	// explicit-config target is identified by, and is empty for a git-root
	// target.
	ConfigPaths []string

	// FarmDir and ManifestPath are derived from the identity above.
	FarmDir      string
	ManifestPath string
}

// explicitConfig reports whether this target is a farm named by --config rather
// than discovered from a git root.
func (t sourceTarget) explicitConfig() bool {
	return t.Origin == sourcefarm.OriginExplicitConfig
}

// label names the target in a message a human reads: the git root they would cd
// to, or the config chain they would pass to --config again.
func (t sourceTarget) label() string {
	if t.Root != "" {
		return t.Root
	}
	return strings.Join(t.ConfigPaths, ", ")
}

// resolveSourceTarget decides which farm this invocation acts on.
//
// An explicit --config takes the whole decision away from git: the farm's
// identity is the resolved chain (env.ConfigFarmIdentity), so the same chain
// names one farm from every directory on the machine, and no git root is
// consulted, discovered or required. That is what makes activation work outside
// a repository at all, and it is the same branch the shim takes when it refuses
// to walk up for .git.
//
// Without --config the behaviour is unchanged: the git root is the identity, and
// a missing root or a missing config there is the error that points at --config.
func resolveSourceTarget(ctx context.Context) (sourceTarget, error) {
	if len(ConfigPaths) > 0 {
		return explicitConfigTarget()
	}

	root, err := sourceProjectRoot(ctx)
	if err != nil {
		return sourceTarget{}, err
	}
	farmDir, err := env.GetProjectBinPath(root)
	if err != nil {
		return sourceTarget{}, fmt.Errorf("determine the farm directory: %w", err)
	}
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		return sourceTarget{}, fmt.Errorf("determine the farm manifest path: %w", err)
	}
	return sourceTarget{
		Origin:       sourcefarm.OriginGitRoot,
		Root:         root,
		FarmDir:      farmDir,
		ManifestPath: manifestPath,
	}, nil
}

// explicitConfigTarget builds the target for a chain named on the command line.
//
// --before-config files are part of the identity, not a modifier of it: they are
// loaded before the named configs and change what the farm contains, so two
// invocations that differ only in them must be two farms rather than one farm
// baked twice from different inputs.
func explicitConfigTarget() (sourceTarget, error) {
	chain := make([]string, 0, len(BeforeConfigPaths)+len(ConfigPaths))
	chain = append(chain, BeforeConfigPaths...)
	chain = append(chain, ConfigPaths...)

	resolved, err := env.ResolveConfigChain(chain)
	if err != nil {
		return sourceTarget{}, fmt.Errorf("resolve the config chain: %w", err)
	}
	farmDir, err := env.GetConfigFarmBinPath(resolved)
	if err != nil {
		return sourceTarget{}, fmt.Errorf("determine the farm directory: %w", err)
	}
	manifestPath, err := env.GetConfigFarmManifestPath(resolved)
	if err != nil {
		return sourceTarget{}, fmt.Errorf("determine the farm manifest path: %w", err)
	}
	return sourceTarget{
		Origin:       sourcefarm.OriginExplicitConfig,
		ConfigPaths:  resolved,
		FarmDir:      farmDir,
		ManifestPath: manifestPath,
	}, nil
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
func freshSourcePlan(target sourceTarget) (sourcefarm.Plan, bool) {
	if !sourceManifestDecides(target) {
		return sourcefarm.Plan{}, false
	}
	return freshSourcePlanFor(target)
}

// freshSourcePlanFor is freshSourcePlan once the flag question has been settled.
//
// It cannot fail, which is why it returns no error. Every state that is not
// "fresh, with the farm it describes still on disk" — no manifest, one that will
// not decode, an aged-out watch set, a deleted farm — means the same thing to the
// caller and gets the same answer: false, go resolve the config and bake.
func freshSourcePlanFor(target sourceTarget) (sourcefarm.Plan, bool) {
	return loadSourcePlan(target, true)
}

// previousSourcePlan returns the farm already on disk for root, freshness aside.
//
// It is the fallback for a bake that could not be computed at all: the farm it
// describes is what every already-activated shell for this root is running, so
// it is a strictly better answer than activating nothing. The chain check still
// applies — serving a farm baked from a different config chain would activate a
// toolchain this invocation never asked for.
func previousSourcePlan(target sourceTarget) (sourcefarm.Plan, bool) {
	return loadSourcePlan(target, false)
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
func loadSourcePlan(target sourceTarget, requireFresh bool) (sourcefarm.Plan, bool) {
	if target.ManifestPath == "" {
		return sourcefarm.Plan{}, false
	}
	m, err := sourcefarm.Load(target.ManifestPath)
	if err != nil || m.Origin != target.Origin || !manifestChainMatches(m, target) {
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
//
// An explicit-config farm is the exception, and it is an exception by
// construction rather than by permission: its manifest does not live at a git
// root's path but at the path the chain itself hashes to, so the only manifest
// this invocation can find is one baked from these very files. That matters
// here more than anywhere else — a machine-level activation runs in a shell rc
// file, so every shell and every tmux pane would otherwise pay a full config
// resolution.
func sourceManifestDecides(target sourceTarget) bool {
	if target.explicitConfig() {
		return true
	}
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
func manifestChainMatches(m sourcefarm.Manifest, target sourceTarget) bool {
	return slices.Equal(m.ConfigArgs, sourceConfigArgs(target))
}

// sourceConfigArgs is the argv fragment recorded in the manifest this target
// writes, and the one every reader of an existing manifest compares against.
//
// For an explicit-config farm it is rebuilt from the *resolved* chain rather
// than from the flag values as typed, so `--config ./cfg.ts`, `--config
// /abs/cfg.ts` and a symlink to the same file — which already resolve to one
// farm — also record one fragment, instead of re-baking each other in turn.
// --before-config paths are replayed as --config: with discovery off there is
// no auto config for them to precede, so chain order alone carries their
// meaning.
//
// --no-auto-config is unconditional there, and it is the trust boundary in its
// bake form. Without it, `datamitsu source fish --config ~/tools.ts` run from
// inside a repository would merge that repository's config into a machine-level
// farm — and the first rebake, which the shim always spawns with the flag,
// would silently drop everything it contributed.
func sourceConfigArgs(target sourceTarget) []string {
	if !target.explicitConfig() {
		return configChainArgs()
	}
	args := make([]string, 0, 1+2*len(target.ConfigPaths))
	args = append(args, "--no-auto-config")
	for _, p := range target.ConfigPaths {
		args = append(args, "--config", p)
	}
	return args
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
func reportBakeFailure(stderr io.Writer, res bakeResult, target sourceTarget) {
	if res.MaterializeErr == nil {
		return
	}
	reportPreviousFarm(stderr, target)
}

// reportPreviousFarm is the one line that tells the user which farm they ended
// up with when the intended bake did not happen. A farm with no git root is
// named by its config chain: telling someone a directory that does not exist is
// what they are left with is worse than saying nothing.
func reportPreviousFarm(stderr io.Writer, target sourceTarget) {
	_, _ = fmt.Fprintf(stderr, "%s: activating the farm already on disk for %s\n", ldflags.PackageName, target.label())
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
func bakeSourceFarm(ctx context.Context, stderr io.Writer, target sourceTarget) (bakeResult, error) {
	// Snapshot the watch set before the config is read, not only after. An edit
	// that lands while the config is being evaluated — a `git checkout` in
	// another terminal, an editor saving — is otherwise stat'ed *after* it
	// happened and recorded as the state this farm was built from, so the next
	// freshness check compares equal and the farm serves the previous branch's
	// toolchain forever, silently. Taking the earlier tuple makes that case read
	// as stale and re-bake, which is the failure direction that self-corrects.
	// The git root is cached by facts, so asking for it again costs nothing.
	prior := sourcefarm.WatchSet(targetWatchPaths(target, nil))

	plan, err := resolveSourcePlanFor(ctx, target)
	if err != nil {
		return bakeResult{}, err
	}

	watch := watchSetSince(prior, sourcefarm.WatchSet(targetWatchPaths(target, ConfigChainFiles())))
	m := sourcefarm.BuildManifest(plan, target.Origin, watch)
	if target.explicitConfig() {
		// The recorded chain is the resolved one, so the manifest describes the
		// exact input its own farm path was hashed from.
		m = sourcefarm.BuildConfigManifest(plan, target.ConfigPaths, watch)
	}
	m.ConfigArgs = sourceConfigArgs(target)

	// sourcefarm already reports the failure on the writer it was given.
	matErr := sourcefarm.MaterializeWithOptions(plan, m, sourcefarm.Options{
		Warn: func(line string) { _, _ = fmt.Fprintln(stderr, line) },
	})
	if matErr != nil && !farmOnDisk(plan, target) {
		return bakeResult{}, fmt.Errorf("failed to bake the source farm for %s, and no previous farm is usable: %w", target.label(), matErr)
	}

	return bakeResult{Plan: plan, MaterializeErr: matErr}, nil
}

// targetWatchPaths returns the files whose state can make this target's farm
// stale.
//
// A git-root farm watches the config chain plus the repository tripwires —
// .git/HEAD, the lockfile, every auto-config candidate — because a checkout can
// change the toolchain without touching any file the chain named. An
// explicit-config farm watches the chain and nothing else: it has no repository,
// and borrowing the tripwires of whichever one the shell happened to be standing
// in would rebake a machine-level farm on every unrelated checkout.
//
// The chain an explicit-config farm watches is the target's resolved one, not
// the files the load reported: discovery is off for such a bake, so the two name
// the same files, and the resolved spelling is the one the farm's own identity
// was computed from. Recording the other would make a farm reached through a
// symlink watch a path its identity never mentioned.
func targetWatchPaths(target sourceTarget, configFiles []string) []string {
	if target.explicitConfig() {
		return sourcefarm.ConfigWatchPaths(target.ConfigPaths)
	}
	return sourcefarm.WatchPaths(target.Root, configFiles)
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
func farmOnDisk(plan sourcefarm.Plan, target sourceTarget) bool {
	if !sourcefarm.FarmUsable(plan.FarmDir, "") {
		return false
	}
	if target.ManifestPath == "" {
		return false
	}
	_, err := sourcefarm.Load(target.ManifestPath)
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
func resolveSourcePlanFor(ctx context.Context, target sourceTarget) (sourcefarm.Plan, error) {
	// An explicit-config bake never discovers: the farm is the chain the user
	// named and nothing else, whatever directory the command was run in. See
	// sourceConfigArgs, which records the same decision for the shim to replay.
	noAutoConfig := NoAutoConfig || target.explicitConfig()

	cfg, _, _, err := loadConfigWithPaths(ctx, BeforeConfigPaths, noAutoConfig, ConfigPaths)
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

	b := binmanager.New(cfg.Apps, cfg.Bundles, runtimemanager.New(cfg.Runtimes))
	return sourcefarm.BuildPlan(target.Root, target.FarmDir, cfg.Apps, b, sourcefarm.SystemLookPath(target.FarmDir)), nil
}

// sourceProjectRoot returns the git root whose config the farm is built from, or
// an actionable error.
//
// It is reached only when no --config was given, which is what makes every
// error here point at that flag: config discovery is git-root-only — datamitsu
// stats three filenames at the git root and nowhere else — so outside a
// repository, or inside one that declares no config, a load would silently
// succeed against the embedded default config and activate a handful of
// built-in apps. That looks like it worked and is not anybody's toolchain.
// Naming a config explicitly is the answer, and resolveSourceTarget takes it
// before this function runs.
func sourceProjectRoot(ctx context.Context) (string, error) {
	root, err := facts.GetGitRoot(ctx)
	if err != nil || root == "" {
		return "", fmt.Errorf("%s source needs a project: no git repository was found from this directory.\n"+
			"Run it inside a repository that has a %s config, or pass one explicitly:\n"+
			"  %s --config /path/to/%s.config.ts source bash",
			ldflags.PackageName, ldflags.PackageName, ldflags.PackageName, ldflags.PackageName)
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
func renderBash(a sourceActivation) string {
	farmVar := env.SourceFarmVarName()

	var b strings.Builder
	for _, v := range a.vars() {
		fmt.Fprintf(&b, "export %s=%s\n", v.name, shellquote.Bash(v.value))
	}
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
func renderFish(a sourceActivation) string {
	var b strings.Builder
	for _, v := range a.vars() {
		fmt.Fprintf(&b, "set -gx %s %s\n", v.name, shellquote.Fish(v.value))
	}
	fmt.Fprintf(&b, "fish_add_path --global --move --path %s\n", shellquote.Fish(a.FarmDir))
	return b.String()
}

// sourceActivation is what the shell renderers are given: the farm directory to
// prepend, and the identity to describe it by.
//
// It is not the Plan, because the Plan is what the farm *contains* and this is
// what the shell is *told*. The two differ for a farm with no git root, which
// has no Plan.Root to export and a config chain to export instead.
type sourceActivation struct {
	FarmDir     string
	Root        string
	ConfigPaths []string
}

// activationFor pairs a baked plan with the target that selected it. The farm
// directory comes from the plan — a fallback to the farm already on disk must
// activate that farm, not the one this invocation failed to write — while the
// identity comes from the target, which is where a rootless farm's chain lives.
func activationFor(target sourceTarget, plan sourcefarm.Plan) sourceActivation {
	return sourceActivation{
		FarmDir:     plan.FarmDir,
		Root:        target.Root,
		ConfigPaths: target.ConfigPaths,
	}
}

// shellVar is one exported variable in the emitted activation code.
type shellVar struct {
	name  string
	value string
}

// vars returns the variables the activation exports, in a fixed order.
//
// A git-root farm exports the root and the farm, exactly as before. An
// explicit-config farm exports the farm and the chain, and deliberately does not
// export the root variable at all: there is no git root behind it, and writing
// an empty one — or a fabricated one — would put a value in the place a user, a
// prompt and a bug report read as "the repository this shell is pinned to".
//
// Both are informational. Nothing resolves a tool through them; the shim answers
// from the farm directory an invocation arrived through.
func (a sourceActivation) vars() []shellVar {
	if a.Root == "" {
		return []shellVar{
			{env.SourceFarmVarName(), a.FarmDir},
			{env.SourceFarmConfigVarName(), strings.Join(a.ConfigPaths, string(os.PathListSeparator))},
		}
	}
	return []shellVar{
		{env.SourceRootVarName(), a.Root},
		{env.SourceFarmVarName(), a.FarmDir},
	}
}
