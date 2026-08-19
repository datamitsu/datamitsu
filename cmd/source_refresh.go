package cmd

import (
	"fmt"
	"io"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"

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

	root, err := sourceProjectRoot(ctx)
	if err != nil {
		return err
	}

	if !sourceRefreshForce && sourceFarmIsFresh(root) {
		_, _ = fmt.Fprintf(stderr, "%s: source farm for %s is already up to date\n", ldflags.PackageName, root)
		return nil
	}

	plan, err := bakeSourceFarm(ctx, stderr)
	if err != nil {
		return err
	}

	summarizeRefresh(stderr, plan)
	return nil
}

// sourceFarmIsFresh reports whether the baked manifest still matches the tree.
//
// Every non-fresh state — missing, unreadable, aged out — answers false: they
// all mean the same repair, and refusing to re-bake a farm that cannot be read
// would leave the user with no way to fix it short of deleting the cache.
func sourceFarmIsFresh(root string) bool {
	manifestPath, err := env.GetProjectManifestPath(root)
	if err != nil {
		return false
	}
	return manifestStatus(manifestPath).Fresh
}

// summarizeRefresh writes the one line the user reads after a re-bake.
//
// It goes to stderr with the rest of source mode's human output: a refresh may
// be called from the same shell function that runs an activation through `eval`, and a single
// stray byte on stdout there is a syntax error in the user's shell.
func summarizeRefresh(stderr io.Writer, plan sourcefarm.Plan) {
	pending := 0
	for _, e := range plan.Entries {
		if !e.Installed {
			pending++
		}
	}
	_, _ = fmt.Fprintf(stderr, "%s: baked %d tool(s) for %s (%d not downloaded yet, %d excluded)\n",
		ldflags.PackageName, len(plan.Entries), plan.Root, pending, len(plan.Excluded))
}
