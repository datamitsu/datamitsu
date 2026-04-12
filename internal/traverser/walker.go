package traverser

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"golang.org/x/sync/errgroup"
)

type Walker struct {
	rootPath string
	path     string
	git      *GitIgnore
}

func (w *Walker) walk(ctx context.Context, results *[]string, mu *sync.Mutex) error {
	if err := ctx.Err(); err != nil {
		return err
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
		return err
	}

	var localFiles []string
	var dirs []string

	localFiles = make([]string, 0, len(entries))
	dirs = make([]string, 0, len(entries)/4)

	for _, d := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
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

	return gg.Wait()
}

func (w *Walker) Walk(ctx context.Context) ([]string, error) {

	results := make([]string, 0, 10000)
	mu := &sync.Mutex{}

	err := w.walk(ctx, &results, mu)
	return results, err
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

func SortAscending(arr []string) []string {
	result := make([]string, len(arr))
	copy(result, arr)
	sort.Strings(result)
	return result
}

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
