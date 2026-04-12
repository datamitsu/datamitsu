package facts

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/target"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sync/errgroup"
)

// Facts contains information about the project environment.
// Path-related fields (CWD, GitRoot, ProjectRoot, ProjectCachePath) have been
// removed. Use template placeholders ({cwd}, {root}, {toolCache}) in tool
// configs instead.
type Facts struct {
	// PackageName is the name of this package from ldflags
	PackageName string `json:"packageName"`
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

// Collect gathers all facts about the current environment.
// Returns Facts, the git root path (empty if not in a git repo), and any error.
func Collect(ctx context.Context, binaryCommandOverride string) (*Facts, string, error) {
	libc := target.LibcUnknown
	if runtime.GOOS == "linux" {
		libc = target.DetectLibc()
	}

	facts := &Facts{
		PackageName: ldflags.PackageName,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Libc:        string(libc),
	}

	// Get binary path
	ex, err := os.Executable()
	if err != nil {
		return nil, "", err
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
		return nil, "", err
	}

	// Try to get git root (non-fatal if not in git repo)
	gitRoot, err := GetGitRoot(ctx)
	if err == nil {
		facts.IsInGitRepo = true

		// Check if we're in a monorepo (CWD is different from git root)
		relPath, err := filepath.Rel(gitRoot, cwd)
		if err == nil && relPath != "." && relPath != "" {
			facts.IsMonorepo = true
		}
	} else {
		// If a .git directory exists but git command failed (broken install,
		// permissions), surface the error rather than silently falling back
		// to CWD with a wrong cache key.
		if traverser.HasGitDir(cwd) {
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

// GetGitRoot returns the root of the topmost repository in the submodules hierarchy
func GetGitRoot(ctx context.Context) (string, error) {
	current := ""

	ex, err := os.Getwd()
	if err != nil {
		return "", err
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
			cmd.Env = os.Environ()
			cmd.Dir = ex

			out, err := cmd.Output()
			if err != nil {
				return err
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
			cmd.Env = os.Environ()
			cmd.Dir = ex

			out, err := cmd.Output()
			if err == nil {
				parent = strings.TrimSpace(string(out))
			}
			return nil
		})

		if err := g.Wait(); err != nil {
			return "", err
		}

		// If no parent - we're at the top level
		if parent == "" {
			return root, nil
		}

		// Continue climbing up
		current = parent
	}
}
