package traverser

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/logger"

	"go.uber.org/zap"
)

type MonorepoTraverser struct {
	cwdPath  string
	rootPath string
	log      *zap.Logger

	git *GitIgnore
}

func New(ctx context.Context, cwd string) (MonorepoTraverser, error) {
	log := logger.Logger.With(zap.Namespace("traverser"), zap.String("cwd", cwd))

	rootPath, err := GetGitRoot(ctx, cwd)
	if err != nil {
		return MonorepoTraverser{}, err
	}

	log = log.With(zap.String("root", rootPath))

	log.Debug("found git root")

	git := NewGitIgnore(rootPath)
	_ = git.CollectRules(ctx, cwd)
	_ = git.Compile()

	return MonorepoTraverser{
		cwdPath:  cwd,
		rootPath: rootPath,
		log:      log,
		git:      git,
	}, nil
}

func (t *MonorepoTraverser) Walk(ctx context.Context) error {

	// err := fastwalk.Walk(&conf, t.cwdPath, func(path string, d os.DirEntry, err error) error {
	// 	log := t.log.With(zap.Namespace("walk"))

	// 	if err != nil {
	// 		return err
	// 	}

	// 	isDir := d.IsDir()

	// 	// relativeCwdPath, err := filepath.Rel(t.cwdPath, path)
	// 	// if err != nil {
	// 	// 	return err
	// 	// }

	// 	if isIgnored(t.patterns, t.rootPath, path, isDir) {

	// 		log.With(zap.Bool("isDir", isDir)).Debug("IGNORE:", zap.String("path", path))

	// 		if isDir {
	// 			return filepath.SkipDir
	// 		}
	// 	} else {
	// 		log.With(zap.Bool("isDir", isDir)).Debug(path)
	// 		paths = append(paths, path)
	// 	}

	// 	if isDir {
	// 		base := d.Name()
	// 		if base == ".git" {
	// 			return filepath.SkipDir
	// 		}

	// 		// gitignorePath := filepath.Join(path, ".gitignore")

	// 		if base == "node_modules" {
	// 			return filepath.SkipDir
	// 		}
	// 	}

	// 	// Skip directories in skipDirs list
	// 	// if d.IsDir() {
	// 	// 	base := d.Name()
	// 	// 	if skipMap[base] {
	// 	// 		return filepath.SkipDir
	// 	// 	}

	// 	// 	// Early pruning: skip directories that cannot contain matching files
	// 	// 	if includeMatcher != nil && !includeMatcher.CanMatchInDirectory(path) {
	// 	// 		return filepath.SkipDir
	// 	// 	}

	// 	// 	return nil
	// 	// }

	// 	// // Filter files by skip patterns if matcher is provided
	// 	// if fileMatcher != nil && fileMatcher.Match(path) {
	// 	// 	return nil // Skip this file
	// 	// }

	// 	// // Add regular files (thread-safe)
	// 	// mu.Lock()
	// 	// files = append(files, path)
	// 	// mu.Unlock()

	// 	return nil
	// })

	w := Walker{
		rootPath: t.rootPath,
		path:     t.cwdPath,
		git:      t.git,
	}
	paths, err := w.Walk(ctx)

	t.log.Debug("walk complete", zap.Int("count", len(paths)))

	return err
}
