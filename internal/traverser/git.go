package traverser

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"
)

// GetGitRoot returns the root of the topmost repository in submodules hierarchy
func GetGitRoot(ctx context.Context, cwd string) (string, error) {
	current := ""

	for {

		var root, parent string

		g, gctx := errgroup.WithContext(ctx)

		g.Go(func() error {
			args := []string{"rev-parse", "--show-toplevel"}
			if current != "" {
				args = append([]string{"-C", current}, args...)
			}
			cmd := exec.CommandContext(gctx, "git", args...)
			cmd.Env = os.Environ()

			cmd.Dir = cwd

			out, err := cmd.Output()
			if err != nil {
				return err
			}
			root = strings.TrimSpace(string(out))
			return nil
		})

		g.Go(func() error {
			args := []string{"rev-parse", "--show-superproject-working-tree"}
			if current != "" {
				args = append([]string{"-C", current}, args...)
			}
			cmd := exec.CommandContext(gctx, "git", args...)
			cmd.Env = os.Environ()

			cmd.Dir = cwd

			out, err := cmd.Output()
			if err == nil {
				parent = strings.TrimSpace(string(out))
			}
			return nil
		})

		if err := g.Wait(); err != nil {
			return "", err
		}

		if parent == "" {
			return root, nil
		}

		current = parent
	}
}

// HasGitDir checks if a .git directory exists in startPath or any ancestor.
func HasGitDir(startPath string) bool {
	dir := filepath.Clean(startPath)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// collectGitignorePaths collects paths to .gitignore from root to target
func collectGitignorePaths(root, target string) []string {
	var paths []string

	current := root
	for {
		gitignorePath := filepath.Join(current, ".gitignore")

		if _, err := os.Stat(gitignorePath); err == nil {
			paths = append(paths, gitignorePath)
		}

		if current == target {
			break
		}

		rel, err := filepath.Rel(current, target)
		if err != nil {
			break
		}

		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) == 0 {
			break
		}

		current = filepath.Join(current, parts[0])
	}

	return paths
}
