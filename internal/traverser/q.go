package traverser

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v6/plumbing/format/gitignore"
	"golang.org/x/sync/errgroup"
)

type gitIgnoreFile struct {
	content []byte
	absPath string
}

// GitIgnore accumulates .gitignore files for a repository and compiles them
// into matchable patterns to decide whether a path is ignored.
type GitIgnore struct {
	root       string
	list       []gitIgnoreFile
	patterns   []gitignore.Pattern
	isCompiled bool
}

// NewGitIgnore returns a GitIgnore rooted at the given repository root path.
func NewGitIgnore(root string) *GitIgnore {
	return &GitIgnore{
		root: filepath.Clean(root),
	}
}

// Compile parses all collected .gitignore files into gitignore patterns.
func (g *GitIgnore) Compile() error {
	for _, res := range g.list {
		relPath, err := filepath.Rel(g.root, filepath.Dir(res.absPath))
		if err != nil {
			continue
		}

		domain := []string{}
		if relPath != "." {
			domain = strings.Split(relPath, string(filepath.Separator))
		}

		scanner := bufio.NewScanner(bytes.NewReader(res.content))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			pattern := gitignore.ParsePattern(line, domain)
			g.patterns = append(g.patterns, pattern)
		}
	}

	g.isCompiled = true

	return nil
}

// CountPatterns returns the number of compiled patterns, or an error if the
// GitIgnore has not been compiled yet.
func (g *GitIgnore) CountPatterns() (int, error) {
	if !g.isCompiled {
		return 0, errors.New("is not compiled")
	}

	return len(g.patterns), nil
}

// Clone returns a copy of the GitIgnore with its collected files but without
// compiled patterns, so additional files can be added before compiling.
func (g *GitIgnore) Clone() *GitIgnore {
	return &GitIgnore{
		root: g.root,
		list: append([]gitIgnoreFile{}, g.list...),
	}
}

// AddGitIgnoreFile registers a .gitignore file's content for later compilation.
// It panics if the GitIgnore has already been compiled.
func (g *GitIgnore) AddGitIgnoreFile(absPath string, content []byte) {
	if g.isCompiled {
		panic("already compiled")
	}

	g.list = append(g.list, gitIgnoreFile{
		absPath: absPath,
		content: content,
	})
}

// IsIgnored reports whether the given path is ignored by the compiled patterns.
// It panics if the GitIgnore has not been compiled.
func (g *GitIgnore) IsIgnored(path string, isDir bool) bool {
	if !g.isCompiled {
		panic("is not compiled")
	}

	if len(g.patterns) == 0 {
		return false
	}

	path = filepath.Clean(path)

	if filepath.IsAbs(path) {
		rel, err := filepath.Rel(g.root, path)
		if err != nil {
			return false
		}
		path = rel
	}

	path = filepath.ToSlash(path)

	parts := strings.Split(path, "/")

	matcher := gitignore.NewMatcher(g.patterns)

	return matcher.Match(parts, isDir)
}

// CollectRules reads all .gitignore files from the root down to target and
// adds them to the GitIgnore. Targets outside the root are ignored.
func (g *GitIgnore) CollectRules(ctx context.Context, target string) error {
	if g.isCompiled {
		panic("already compiled")
	}

	target = filepath.Clean(target)

	if target != g.root {
		rel, err := filepath.Rel(g.root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil //nolint:nilerr // target outside root: no rules to collect
		}
	}

	paths := collectGitignorePaths(g.root, target)

	type result struct {
		index int
		file  gitIgnoreFile
	}

	resultCh := make(chan result, len(paths))
	gr, gctx := errgroup.WithContext(ctx)

	for i, path := range paths {
		gr.Go(func() error {
			if err := gctx.Err(); err != nil {
				return fmt.Errorf("collect gitignore rules: %w", err)
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return nil //nolint:nilerr // skip unreadable gitignore, keep collecting the rest
			}

			// relPath, err := filepath.Rel(root, filepath.Dir(path))
			// if err != nil {
			// 	return nil
			// }

			// domain := []string{}
			// if relPath != "." {
			// 	domain = strings.Split(relPath, string(filepath.Separator))
			// }

			// var patterns []gitignore.Pattern
			// var stringPatterns []string
			// scanner := bufio.NewScanner(bytes.NewReader(content))
			// for scanner.Scan() {
			// 	line := strings.TrimSpace(scanner.Text())
			// 	if line == "" || strings.HasPrefix(line, "#") {
			// 		continue
			// 	}

			// 	stringPatterns = append(stringPatterns, line)

			// 	pattern := gitignore.ParsePattern(line, domain)
			// 	patterns = append(patterns, pattern)
			// }

			file := gitIgnoreFile{
				content: content,
				absPath: path,
			}

			resultCh <- result{index: i, file: file}
			return nil
		})
	}

	if err := gr.Wait(); err != nil {
		return fmt.Errorf("read gitignore files: %w", err)
	}
	close(resultCh)

	list := make([]gitIgnoreFile, len(paths))
	filled := make([]bool, len(paths))
	for res := range resultCh {
		list[res.index] = res.file
		filled[res.index] = true
	}

	for i, ok := range filled {
		if ok {
			g.list = append(g.list, list[i])
		}
	}

	return nil
}
