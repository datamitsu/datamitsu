package traverser

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// maxGitLinkSize bounds the `.git` file of a linked worktree or submodule,
// which holds a single `gitdir:` line.
const maxGitLinkSize = 4096

// repoExcludeFiles returns the exclude files git applies to the whole working
// tree at root, weakest first: core.excludesFile, then info/exclude of the
// repository's common directory — which a linked worktree shares with its main
// checkout. Every .gitignore outranks both. Outside a repository git applies
// neither, so neither does the walker.
func repoExcludeFiles(ctx context.Context, root string) []string {
	common := gitCommonDir(root)
	if common == "" {
		return nil
	}
	var files []string
	if global := globalExcludesFile(ctx, root); global != "" {
		files = append(files, global)
	}
	return append(files, filepath.Join(common, "info", "exclude"))
}

// globalExcludesFile resolves core.excludesFile the way git does. The value is
// read through git because it may come from any config scope, an include, or
// need `~` expansion. An empty value disables the file; only an unset key (or
// no usable git) falls back to git's own default.
func globalExcludesFile(ctx context.Context, root string) string {
	cmd := exec.CommandContext(ctx, "git", "config", "--path", "--get", "core.excludesFile")
	cmd.Dir = root
	cmd.Env = gitenv.Environ()
	if out, err := cmd.Output(); err == nil {
		path := strings.TrimSpace(string(out))
		if path != "" && !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		return path
	}

	configHome := env.ConfigHome()
	if configHome == "" {
		return ""
	}
	return filepath.Join(configHome, "git", "ignore")
}

// gitCommonDir returns the repository directory holding info/exclude for the
// working tree at root, or "" when root has no readable `.git` entry.
func gitCommonDir(root string) string {
	dotGit := filepath.Join(root, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return ""
	}

	gitDir := dotGit
	if !info.IsDir() {
		if !info.Mode().IsRegular() || info.Size() > maxGitLinkSize {
			return ""
		}
		data, err := os.ReadFile(dotGit) // #nosec G304 -- <root>/.git, size-checked above
		if err != nil {
			return ""
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
		target = strings.TrimSpace(target)
		if !ok || target == "" {
			return ""
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		gitDir = filepath.Clean(target)
	}

	// A linked worktree's git directory names the main repository in commondir.
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir")) // #nosec G304 -- fixed name under the git directory
	if err != nil {
		return gitDir
	}
	common := strings.TrimSpace(string(data))
	if common == "" {
		return gitDir
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	return filepath.Clean(common)
}
