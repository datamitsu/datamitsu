package traverser

import (
	"bufio"
	"bytes"
	"context"
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

type GitIgnore struct {
	root       string
	list       []gitIgnoreFile
	patterns   []gitignore.Pattern
	isCompiled bool
}

func NewGitIgnore(root string) *GitIgnore {

	return &GitIgnore{
		root: filepath.Clean(root),
	}
}

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

func (g *GitIgnore) CountPatterns() (int, error) {
	if !g.isCompiled {
		return 0, fmt.Errorf("is not compiled")
	}

	return len(g.patterns), nil
}

func (g *GitIgnore) Clone() *GitIgnore {
	return &GitIgnore{
		root: g.root,
		list: append([]gitIgnoreFile{}, g.list...),
	}
}

func (g *GitIgnore) AddGitIgnoreFile(absPath string, content []byte) {
	if g.isCompiled {
		panic("already compiled")
	}

	g.list = append(g.list, gitIgnoreFile{
		absPath: absPath,
		content: content,
	})

}

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

func (g *GitIgnore) CollectRules(ctx context.Context, target string) error {
	if g.isCompiled {
		panic("already compiled")
	}

	target = filepath.Clean(target)

	if target != g.root {
		rel, err := filepath.Rel(g.root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
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
		i, path := i, path

		gr.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return nil
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
		return err
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
