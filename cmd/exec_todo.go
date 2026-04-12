package cmd

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v6/plumbing/format/gitignore"
	"golang.org/x/sync/errgroup"
)

type Facts struct {
	Value int `json:"value"`
}

// GetGitRoot returns the root of the topmost repository in submodules hierarchy
func GetGitRoot(ctx context.Context) (string, error) {
	current := ""

	ex, err := os.Getwd()
	if err != nil {
		return "", err
	}

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

			cmd.Dir = ex

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

		if parent == "" {
			return root, nil
		}

		current = parent
	}
}

// GitignoreMatcher contains compiled gitignore rules
type GitignoreMatcher struct {
	patterns []gitignore.Pattern
	root     string
}

// CollectGitignoreRules collects all .gitignore rules from root to target
func CollectGitignoreRules(ctx context.Context, root, target string) (*GitignoreMatcher, error) {
	root = filepath.Clean(root)
	target = filepath.Clean(target)

	if !strings.HasPrefix(target, root) {
		return &GitignoreMatcher{root: root}, nil
	}

	paths := collectGitignorePaths(root, target)

	if len(paths) == 0 {
		return &GitignoreMatcher{root: root}, nil
	}

	type result struct {
		index    int
		patterns []gitignore.Pattern
	}

	resultCh := make(chan result, len(paths))
	g, gctx := errgroup.WithContext(ctx)

	for i, path := range paths {
		i, path := i, path

		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			relPath, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return nil
			}

			domain := []string{}
			if relPath != "." {
				domain = strings.Split(relPath, string(filepath.Separator))
			}

			var patterns []gitignore.Pattern
			scanner := bufio.NewScanner(bytes.NewReader(content))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}

				pattern := gitignore.ParsePattern(line, domain)
				patterns = append(patterns, pattern)
			}

			resultCh <- result{index: i, patterns: patterns}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	close(resultCh)

	fileContents := make([][]gitignore.Pattern, len(paths))
	for res := range resultCh {
		fileContents[res.index] = res.patterns
	}

	var allPatterns []gitignore.Pattern
	for _, patterns := range fileContents {
		allPatterns = append(allPatterns, patterns...)
	}

	return &GitignoreMatcher{
		patterns: allPatterns,
		root:     root,
	}, nil
}

// IsIgnored checks if the path is ignored
// path can be absolute or relative to root
func (gm *GitignoreMatcher) IsIgnored(path string, isDir bool) bool {
	if len(gm.patterns) == 0 {
		return false
	}

	path = filepath.Clean(path)

	if filepath.IsAbs(path) {
		rel, err := filepath.Rel(gm.root, path)
		if err != nil {
			return false
		}
		path = rel
	}

	path = filepath.ToSlash(path)

	parts := strings.Split(path, "/")

	matcher := gitignore.NewMatcher(gm.patterns)

	return matcher.Match(parts, isDir)
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

func GetGitignoreMatcher(ctx context.Context) (*GitignoreMatcher, error) {
	root, err := GetGitRoot(ctx)
	if err != nil {
		return nil, err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	return CollectGitignoreRules(ctx, root, cwd)
}
