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
	"slices"
	"strings"

	"github.com/datamitsu/datamitsu/internal/configcontract"
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
	// Version is the running build's version string, verbatim from ldflags:
	// a release tag, "dev" for a plain `go build`, or "0.0.0-unstable.*" for a
	// prerelease. Published for diagnostics and reporting only — branch on
	// Capabilities instead, since "dev" and the unstable channel both sort below
	// every real release and make version comparison useless.
	Version string `json:"version"`
	// Capabilities lists the behaviours this build supports, sorted. A config
	// asks `facts().capabilities?.includes("arity")`; the optional chain matters,
	// because a core built before this field existed omits the key entirely,
	// which is the correct negative answer from a build that cannot be changed.
	Capabilities []string `json:"capabilities"`
	// ArgPlaceholders lists the {placeholder} tokens valid in a tool operation's
	// args, so a config can check what the running core will substitute rather
	// than discovering it as a validation error.
	ArgPlaceholders []string `json:"argPlaceholders"`
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
	libc := target.LibcUnknown
	if runtime.GOOS == "linux" {
		libc = target.DetectLibc(ctx)
	}

	facts := &Facts{
		PackageName:     ldflags.PackageName,
		Version:         ldflags.Version,
		Capabilities:    configcontract.Capabilities(),
		ArgPlaceholders: slices.Clone(configcontract.ArgPlaceholders),
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		Libc:            string(libc),
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

// GetGitRoot returns the root of the topmost repository in the submodules hierarchy
func GetGitRoot(ctx context.Context) (string, error) {
	current := ""

	ex, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}

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
