// Package facts collects information about the current project environment
// (OS, arch, libc, git repository layout, environment variables) for use by
// tool configuration and cache keys.
package facts

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/gitenv"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/target"
	"github.com/datamitsu/datamitsu/internal/traverser"

	"golang.org/x/sync/errgroup"
)

// Facts contains information about the project environment.
// Path-related fields (CWD, GitRoot, ProjectRoot, ProjectCachePath) have been
// removed. Use template placeholders ({cwd}, {root}, {toolCache}) in tool
// configs instead.
type Facts struct {
	// PackageName is the name of this package from ldflags
	PackageName string `json:"packageName"`
	// Version is the running build's version string, verbatim from ldflags.
	// Diagnostics only: a config that needs a minimum core declares it through
	// getMinVersion(), which is enforced for stable releases.
	Version string `json:"version"`
	// BinaryCommand is the command to run this binary (can be overridden via env or flag)
	BinaryCommand string `json:"binaryCommand"`
	// BinaryPath is the absolute path to the currently running binary
	BinaryPath string `json:"binaryPath"`
	// OS is the operating system (darwin, linux, windows, etc.)
	OS string `json:"os"`
	// Arch is the CPU architecture (amd64, arm64, etc.)
	Arch string `json:"arch"`
	// Libc is the libc implementation on the host system ("glibc", "musl", or "unknown").
	// On non-Linux systems, this is always "unknown".
	Libc string `json:"libc"`
	// IsInGitRepo indicates whether we're inside a git repository
	IsInGitRepo bool `json:"isInGitRepo"`
	// IsMonorepo indicates whether we're in a subdirectory of git root (potential monorepo)
	IsMonorepo bool `json:"isMonorepo"`
	// Env contains all environment variables
	Env map[string]string `json:"env"`
}

// CollectOptions tweaks fact collection for special-purpose callers.
type CollectOptions struct {
	// TolerateGitFailure degrades a broken git context (a .git directory
	// exists but git cannot run) to "not in a git repository" instead of
	// failing. Store-level commands set it — they operate on the global
	// store, so a wrong project root cannot poison a project cache key.
	// Project commands keep the hard error.
	TolerateGitFailure bool
}

// Collect gathers all facts about the current environment.
// Returns Facts, the git root path (empty if not in a git repo), and any error.
func Collect(ctx context.Context, binaryCommandOverride string) (*Facts, string, error) {
	return CollectWithOptions(ctx, binaryCommandOverride, CollectOptions{})
}

// CollectWithOptions is Collect with explicit CollectOptions.
func CollectWithOptions(ctx context.Context, binaryCommandOverride string, opts CollectOptions) (*Facts, string, error) {
	// Through HostTarget, not DetectLibc: the memo runs the `ldd` probe once per
	// process rather than once per engine (four engines are built per config
	// load), and it is the only path that honours the DATAMITSU_LIBC override.
	// Calling DetectLibc here made facts().libc disagree with the libc used for
	// store paths and OCI bundle selection whenever the override was set.
	host := target.HostTarget()

	facts := &Facts{
		PackageName: ldflags.PackageName,
		Version:     ldflags.Version,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Libc:        string(host.Libc),
	}

	// Get binary path
	ex, err := os.Executable()
	if err != nil {
		return nil, "", fmt.Errorf("determine executable path: %w", err)
	}
	facts.BinaryPath = ex

	// Set binary command (override or default)
	if binaryCommandOverride != "" {
		facts.BinaryCommand = binaryCommandOverride
	} else if envOverride := env.GetBinaryCommandOverride(); envOverride != "" {
		facts.BinaryCommand = envOverride
	} else {
		facts.BinaryCommand = ex
	}

	// Get current working directory (needed for monorepo detection)
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("determine working directory: %w", err)
	}

	// Try to get git root (non-fatal if not in git repo)
	gitRoot, err := GetGitRoot(ctx)
	if err == nil {
		facts.IsInGitRepo = true

		// Check if we're in a monorepo (CWD is different from git root).
		// Both sides are resolved first: git reports the real path, while Getwd
		// reports the path as entered, so under a symlinked directory (on macOS
		// /tmp and /var are symlinks into /private) the two spellings of the same
		// directory differ and every repository there would look like a monorepo.
		relPath, err := filepath.Rel(resolveSymlinks(gitRoot), resolveSymlinks(cwd))
		if err == nil && relPath != "." && relPath != "" {
			facts.IsMonorepo = true
		}
	} else {
		// If a .git directory exists but git command failed (broken install,
		// permissions), surface the error rather than silently falling back
		// to CWD with a wrong cache key — unless the caller opted into the
		// degraded mode (the cmd layer warns about the skipped project config).
		if !opts.TolerateGitFailure && traverser.HasGitDir(cwd) {
			return nil, "", fmt.Errorf("failed to determine git root (a .git directory exists but git command failed): %w", err)
		}
		facts.IsInGitRepo = false
		gitRoot = ""
	}

	// Collect all environment variables
	facts.Env = collectAllEnv()

	return facts, gitRoot, nil
}

// collectAllEnv collects all environment variables
func collectAllEnv() map[string]string {
	envMap := make(map[string]string)

	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value := parts[1]

		envMap[key] = value
	}

	return envMap
}

// resolveSymlinks returns path with every symlink resolved, or path unchanged
// when it cannot be resolved (it may not exist, or be unreadable). Used to
// compare two paths that were obtained differently — a failure to resolve just
// falls back to the textual comparison that was there before.
func resolveSymlinks(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// gitRootEntry is one memoized lookup. once collapses concurrent callers for
// the same working directory onto a single resolution.
type gitRootEntry struct {
	once sync.Once
	root string
	err  error
}

var (
	gitRootMu    sync.RWMutex
	gitRootCache = map[string]*gitRootEntry{}
)

// gitRootLookup is the uncached resolver. It is a package variable so tests can
// count how often a memoized call actually reaches git.
var gitRootLookup = resolveGitRoot

// resetGitRootCache drops every memoized lookup. Test-only: production code
// never needs it, but tests that os.Chdir between cases would otherwise read a
// previous case's answer for a reused path.
func resetGitRootCache() {
	gitRootMu.Lock()
	gitRootCache = map[string]*gitRootEntry{}
	gitRootMu.Unlock()
}

// GetGitRoot returns the root of the topmost repository in the submodules
// hierarchy, memoized for the lifetime of the process and keyed by the working
// directory the lookup starts from.
//
// The memo is sound because datamitsu is a short-lived process that does not
// chdir mid-run, so cwd -> git root is constant for one invocation. A single
// `datamitsu exec` asks for the root five times (once in the config loader,
// once per engine.New), so the memo removes four of the five — including, for
// the layouts gitRootPure declines to answer for, four pairs of forked git
// processes.
//
// The one long-lived command is `datamitsu lsp` (cmd/lsp.go): it resolves the
// root once at startup via traverser.GetGitRoot — not this function — and loads
// config once for the whole session, so it does not depend on re-resolution
// within a session either.
//
// Errors are memoized alongside successes: a directory that is not a repository
// does not become one mid-run. The exception is a failure caused by a cancelled
// or expired context, which says nothing about the repository layout and is
// therefore not retained.
func GetGitRoot(ctx context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}

	gitRootMu.RLock()
	entry := gitRootCache[cwd]
	gitRootMu.RUnlock()

	if entry == nil {
		gitRootMu.Lock()
		if entry = gitRootCache[cwd]; entry == nil {
			entry = &gitRootEntry{}
			gitRootCache[cwd] = entry
		}
		gitRootMu.Unlock()
	}

	entry.once.Do(func() {
		entry.root, entry.err = gitRootLookup(ctx, cwd)
		if entry.err != nil && ctx.Err() != nil {
			gitRootMu.Lock()
			if gitRootCache[cwd] == entry {
				delete(gitRootCache, cwd)
			}
			gitRootMu.Unlock()
		}
	})

	return entry.root, entry.err
}

// gitSubprocessLookup is the forking resolver. It is a package variable for the
// same reason gitRootLookup is: tests need to observe when the pure-Go walk
// hands over to git.
var gitSubprocessLookup = resolveGitRootViaGit

// resolveGitRoot answers from the filesystem when it can and from git when it
// cannot. The pure-Go walk returns false for every layout it is not certain
// about (see gitRootPure), and DATAMITSU_FORCE_GIT_SUBPROCESS=1 skips it
// entirely — a wrong root poisons project cache keys, so both paths exist to
// keep the fast one from ever having to guess.
//
// A cancelled context is honoured before either path runs. The subprocess path
// gets that from exec.CommandContext, but the walk touches only the filesystem
// and would otherwise let a cancelled config load carry on; GetGitRoot drops the
// resulting entry rather than memoizing it.
func resolveGitRoot(ctx context.Context, cwd string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("resolve git root: %w", err)
	}
	if !env.IsForceGitSubprocessEnabled() {
		if root, ok := gitRootPure(cwd); ok {
			return root, nil
		}
	}
	return gitSubprocessLookup(ctx, cwd)
}

// resolveGitRootViaGit climbs from ex to the topmost superproject working tree,
// forking two git processes per level.
func resolveGitRootViaGit(ctx context.Context, ex string) (string, error) {
	current := ""

	for {
		var root, parent string

		g, gctx := errgroup.WithContext(ctx)

		// Get root
		g.Go(func() error {
			args := []string{"rev-parse", "--show-toplevel"}
			if current != "" {
				args = append([]string{"-C", current}, args...)
			}
			cmd := exec.CommandContext(gctx, "git", args...)
			cmd.Env = gitenv.Environ()
			cmd.Dir = ex

			out, err := cmd.Output()
			if err != nil {
				return fmt.Errorf("run git rev-parse --show-toplevel: %w", err)
			}
			root = strings.TrimSpace(string(out))
			return nil
		})

		// Get parent
		g.Go(func() error {
			args := []string{"rev-parse", "--show-superproject-working-tree"}
			if current != "" {
				args = append([]string{"-C", current}, args...)
			}
			cmd := exec.CommandContext(gctx, "git", args...)
			cmd.Env = gitenv.Environ()
			cmd.Dir = ex

			out, err := cmd.Output()
			if err == nil {
				parent = strings.TrimSpace(string(out))
			}
			return nil
		})

		if err := g.Wait(); err != nil {
			return "", fmt.Errorf("resolve git root: %w", err)
		}

		// If no parent - we're at the top level
		if parent == "" {
			return root, nil
		}

		// Continue climbing up
		current = parent
	}
}
