package cmd

import (
	"fmt"
	"io"

	"github.com/datamitsu/datamitsu/internal/ldflags"

	"github.com/spf13/cobra"
)

var sourceRefreshForce bool

var sourceRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Re-bake the project's farm",
	Long: `Re-resolves the project's declared apps and rewrites the farm the current
shell's PATH points at.

Nothing normally needs this. A tool invocation notices on its own that the config
changed and re-bakes before running, which is what makes branch switching
invisible. This command exists for the case that detection cannot see: the
staleness check watches the config chain's files and ` + "`.git/HEAD`" + `, so a config
that branches on an environment variable outside datamitsu's own namespace —
reachable through facts().env — changes what it produces without changing
anything the check compares.

  ` + ldflags.PackageName + ` source refresh            # re-bake only if the tree changed
  ` + ldflags.PackageName + ` source refresh --force    # re-bake unconditionally

Refresh resolves and materializes; it downloads nothing. A tool that has not been
fetched yet stays a shim entry and installs on its first real use.

The summary goes to stderr. stdout stays empty, so this is safe to call from the
same shell function that runs an activation through eval.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error { return runSourceRefresh(cmd) },
}

func init() {
	sourceRefreshCmd.Flags().BoolVar(&sourceRefreshForce, "force", false,
		"Re-bake even when the manifest is already fresh")
}

// runSourceRefresh re-bakes the farm for the current project.
//
// Without --force the on-disk manifest decides: a fresh one means the farm
// already describes this tree, and rewriting it would churn inodes under every
// live shell for no change. The freshness check runs before the config is
// loaded, so the no-op case costs a handful of lstat calls rather than a full
// config evaluation.
func runSourceRefresh(cmd *cobra.Command) error {
	ctx := commandContext(cmd)
	stderr := cmd.ErrOrStderr()

	target, err := resolveSourceTarget(ctx)
	if err != nil {
		return err
	}

	if !sourceRefreshForce && sourceManifestDecides(target) && sourceFarmIsFresh(target) {
		_, _ = fmt.Fprintf(stderr, "%s: source farm for %s is already up to date\n", ldflags.PackageName, target.label())
		return nil
	}

	res, err := bakeSourceFarm(ctx, stderr, target)
	if err != nil {
		return err
	}

	summarizeRefresh(stderr, res, target)
	if res.MaterializeErr != nil {
		// Refresh is the repair command: exiting 0 after failing to write the
		// farm tells a CI step that the toolchain was updated when it still
		// holds whatever the previous bake left. Activation may survive this;
		// an explicit repair may not.
		return fmt.Errorf("failed to re-bake the source farm for %s: %w", target.label(), res.MaterializeErr)
	}
	return nil
}

// sourceFarmIsFresh reports whether the baked manifest still matches the tree
// and the farm it describes is still on disk.
//
// Every other state — missing, unreadable, aged out, or a manifest whose farm
// directory has been deleted out from under it — answers false: they all mean
// the same repair, and refusing to re-bake a farm that cannot be read would
// leave the user with no way to fix it short of deleting the cache. The farm
// directory is part of the question because a manifest survives an `rm -rf` of
// the farm perfectly intact: answering "already up to date" there would refuse
// the repair for the one state that most needs it.
func sourceFarmIsFresh(target sourceTarget) bool {
	_, fresh := freshSourcePlanFor(target)
	return fresh
}

// summarizeRefresh writes the one line the user reads after a re-bake.
//
// It goes to stderr with the rest of source mode's human output: a refresh may
// be called from the same shell function that runs an activation through `eval`, and a single
// stray byte on stdout there is a syntax error in the user's shell.
//
// A bake that failed but left the previous farm standing reports the failure
// instead of a count. Printing "baked N tool(s)" for a farm that was not written
// is the one summary that would send the user away believing the repair worked.
func summarizeRefresh(stderr io.Writer, res bakeResult, target sourceTarget) {
	if res.MaterializeErr != nil {
		// The failure itself is reported twice already if this repeats it:
		// sourcefarm wrote its own line through Options.Warn, and runSourceRefresh
		// returns the error for the process to print. This says only what the
		// user cannot infer from either — which farm they are left with.
		_, _ = fmt.Fprintf(stderr, "%s: the farm for %s was not re-baked, the previous one is left in place\n",
			ldflags.PackageName, target.label())
		return
	}
	pending := 0
	for _, e := range res.Plan.Entries {
		if !e.Installed {
			pending++
		}
	}
	_, _ = fmt.Fprintf(stderr, "%s: baked %d tool(s) for %s (%d not downloaded yet, %d excluded)\n",
		ldflags.PackageName, len(res.Plan.Entries), target.label(), pending, len(res.Plan.Excluded))
}
