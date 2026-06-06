package traverser

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"golang.org/x/sync/errgroup"
)

// Walker recursively collects non-ignored file paths under a directory,
// applying .gitignore rules relative to the repository root.
type Walker struct {
	rootPath string
	path     string
	git      *GitIgnore
}

// Walk returns all non-ignored file paths discovered under the walker's path.
func (w *Walker) Walk(ctx context.Context) ([]string, error) {
	results := make([]string, 0, 10000)
	mu := &sync.Mutex{}

	err := w.walk(ctx, &results, mu)
	return results, err
}

func (w *Walker) walk(ctx context.Context, results *[]string, mu *sync.Mutex) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("walk %q: %w", w.path, err)
	}

	git := w.git

	gitignorePath := filepath.Join(w.path, ".gitignore")
	if _, err := os.Stat(gitignorePath); err == nil {
		if content, err := os.ReadFile(gitignorePath); err == nil {
			git = w.git.Clone()
			git.AddGitIgnoreFile(gitignorePath, content)
			_ = git.Compile()
		}
	}

	entries, err := os.ReadDir(w.path)
	if err != nil {
		return fmt.Errorf("read dir %q: %w", w.path, err)
	}

	var localFiles []string
	var dirs []string

	localFiles = make([]string, 0, len(entries))
	dirs = make([]string, 0, len(entries)/4)

	for _, d := range entries {
		select {
		case <-ctx.Done():
			return fmt.Errorf("walk %q: %w", w.path, ctx.Err())
		default:
		}

		name := d.Name()

		if d.IsDir() && name == ".git" {
			continue
		}

		currentPath := filepath.Join(w.path, name)

		if git.IsIgnored(currentPath, d.IsDir()) {
			continue
		}

		if d.Type()&fs.ModeSymlink != 0 {
			continue
		}

		if d.IsDir() {
			dirs = append(dirs, currentPath)
		} else {
			localFiles = append(localFiles, currentPath)
		}
	}

	if len(localFiles) > 0 {
		mu.Lock()
		*results = append(*results, localFiles...)
		mu.Unlock()
	}

	if len(dirs) == 0 {
		return nil
	}

	gg, gCtx := errgroup.WithContext(ctx)
	gg.SetLimit(8)

	for _, dir := range dirs {
		gg.Go(func() error {
			childWalker := Walker{
				rootPath: w.rootPath,
				path:     dir,
				git:      git,
			}
			return childWalker.walk(gCtx, results, mu)
		})
	}

	if err := gg.Wait(); err != nil {
		return fmt.Errorf("walk subdirectories of %q: %w", w.path, err)
	}
	return nil
}

// FindFiles finds all files in the repository starting from rootPath, respecting .gitignore
func FindFiles(ctx context.Context, rootPath string) ([]string, error) {
	return FindFilesFromPath(ctx, rootPath, rootPath)
}

// FindFilesFromPath finds all files starting from scanPath, respecting .gitignore from rootPath
func FindFilesFromPath(ctx context.Context, rootPath string, scanPath string) ([]string, error) {
	git := NewGitIgnore(rootPath)
	_ = git.CollectRules(ctx, rootPath)
	_ = git.Compile()

	w := Walker{
		rootPath: rootPath,
		path:     scanPath,
		git:      git,
	}

	return w.Walk(ctx)
}

// SortAscending returns a sorted copy of arr without mutating the input.
func SortAscending(arr []string) []string {
	result := make([]string, len(arr))
	copy(result, arr)
	sort.Strings(result)
	return result
}

// Diff returns the elements of slice1 that are not present in slice2.
func Diff(slice1, slice2 []string) []string {
	map2 := make(map[string]bool)
	for _, item := range slice2 {
		map2[item] = true
	}

	result := []string{}
	for _, item := range slice1 {
		if !map2[item] {
			result = append(result, item)
		}
	}
	return result
}
