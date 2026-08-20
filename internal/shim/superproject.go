package shim

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/datamitsu/datamitsu/internal/facts"
	"github.com/datamitsu/datamitsu/internal/gitenv"
)

// maxSuperprojectClimb bounds how many times loadManifest may climb out of a
// submodule into its superproject. A `.git` file pointing back at a descendant
// would otherwise spin forever; nesting deeper than this is not a layout to
// serve a farm for.
const maxSuperprojectClimb = 32

// maxGitFileSize bounds how much of a `.git` file is read. A real one is a
// single `gitdir:` line; anything larger is not something to parse on a path
// with a ~10 ms budget.
const maxGitFileSize = 4096

// gitLink classifies what a working tree's `.git` entry says about the
// repository above it.
type gitLink int

const (
	// gitLinkAmbiguous means the entry cannot be classified from the filesystem
	// alone — an ordinary `.git` directory (which git before 1.7.8 wrote for
	// submodules too), or a `.git` file in a shape this does not parse. Only git
	// can answer, so only this value forks it.
	gitLinkAmbiguous gitLink = iota
	// gitLinkSubmodule is a `.git` file pointing into <super>/.git/modules/…,
	// where <super> is an ancestor of this working tree. It is the shape of a
	// submodule, not proof of one — see superprojectOf.
	gitLinkSubmodule
	// gitLinkStandalone is proof that this working tree has no superproject —
	// a linked worktree, or a `.git` file pointing into a repository that is not
	// an ancestor.
	gitLinkStandalone
)

// superprojectOf reports the working tree that contains root as a submodule,
// which is the one level `git rev-parse --show-superproject-working-tree`
// climbs. It is the gate on loadManifest's climb: without it, an unrelated
// repository checked out inside another one — `outer/vendor/inner` — would be
// served `outer`'s farm, and a farm baked for a different repository is never
// used implicitly.
//
// The filesystem answers first because the submodule case is a hot path: a tool
// run from inside a submodule finds no manifest at the submodule root on every
// invocation, so the climb happens every time. Modern git writes a `.git` file
// there, and where it points rules out most layouts with two system calls.
//
// Where it points is not on its own proof that the climb is the one git makes,
// though: git reports a superproject only while the superproject's index still
// records that path as a gitlink, and it leaves the submodule's working tree —
// `.git` file and all — behind both when you check out a branch without the
// submodule and when you `git rm --cached` it. In those states facts.GetGitRoot
// resolves the submodule as its own root, so climbing would serve a farm baked
// for a root the authoritative resolution disagrees with. A modules-shaped link
// is therefore handed to facts.SuperprojectOf, which proves the registration
// from `.gitmodules` and the index without a subprocess — the same proof the
// root resolution itself climbs on.
//
// Only a shape neither can settle — most of all a plain `.git` directory, which
// is both an unrelated nested repository and the way git before 1.7.8 wrote
// submodules — pays for a git subprocess. That path already ends in exit 127
// unless git says otherwise, so a slow correct answer is the right trade there.
func superprojectOf(root string) (string, bool) {
	switch kind := classifyGitLink(root); kind {
	case gitLinkSubmodule:
		if super, ok := facts.SuperprojectOf(root); ok {
			return super, true
		}
		// Shaped like a submodule but not proven to be a registered one: git
		// answers, and answers empty for a leftover checkout.
	case gitLinkStandalone:
		return "", false
	case gitLinkAmbiguous:
		// Only a shape the filesystem cannot classify forks git.
	}
	return superprojectViaGit(root)
}

// classifyGitLink reads root's `.git` entry and reports what the path alone says
// about the repository above it.
func classifyGitLink(root string) gitLink {
	path := filepath.Join(root, ".git")

	info, err := os.Lstat(path)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() || info.Size() > maxGitFileSize {
		return gitLinkAmbiguous
	}

	raw, err := os.ReadFile(path) // #nosec G304 -- path is <worktree>/.git, size-checked above
	if err != nil {
		return gitLinkAmbiguous
	}
	gitdir, ok := parseGitFile(string(raw))
	if !ok {
		return gitLinkAmbiguous
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}

	return classifyGitDir(filepath.Clean(gitdir), root)
}

// parseGitFile extracts the target of a `.git` file. The whole file must be the
// single `gitdir:` line git writes; anything else is not parsed.
func parseGitFile(content string) (string, bool) {
	line, rest, _ := strings.Cut(content, "\n")
	if strings.TrimSpace(rest) != "" {
		return "", false
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return "", false
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	return target, true
}

// classifyGitDir decides what the git directory a `.git` file points at says
// about root.
//
// A submodule's git directory lives at <super>/.git/modules/<name> and a linked
// worktree's at <main>/.git/worktrees/<name>. A worktree link is proof of no
// superproject, and so is a modules link hosted by a repository that is not an
// ancestor of root, because a `.git` file pointing into an unrelated
// repository's `modules/` directory climbs nowhere git would follow. A modules
// link from an ancestor is the *shape* of a submodule and no more: whether the
// superproject still registers it is what facts.SuperprojectOf proves. A path
// carrying both markers is a linked worktree of a submodule, which is not
// decidable from the path — git decides it.
func classifyGitDir(gitdir, root string) gitLink {
	parts := strings.Split(filepath.ToSlash(gitdir), "/")
	marker := slices.Index(parts, ".git")
	if marker < 0 || marker == len(parts)-1 {
		return gitLinkAmbiguous
	}
	tail := parts[marker+1:]

	switch tail[0] {
	case "worktrees":
		if slices.Contains(tail[1:], "modules") {
			return gitLinkAmbiguous
		}
		return gitLinkStandalone
	case "modules":
		if slices.Contains(tail[1:], "worktrees") {
			return gitLinkAmbiguous
		}
	default:
		return gitLinkAmbiguous
	}

	owner := filepath.FromSlash(strings.Join(parts[:marker], "/"))
	if owner == "" || !isAncestor(owner, root) {
		return gitLinkStandalone
	}
	return gitLinkSubmodule
}

// isAncestor reports whether dir strictly contains path.
func isAncestor(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// superprojectViaGit asks git the same question, for the layouts the filesystem
// cannot answer. An error, an empty answer, or a git that is not installed all
// mean "no superproject", which keeps the dispatch on the loud-failure side.
func superprojectViaGit(root string) (string, bool) {
	// #nosec G204 -- root is a working-tree directory this process discovered by
	// walking up from its own cwd, and it is passed as an argument to a fixed
	// command, never through a shell.
	cmd := exec.CommandContext(context.Background(), "git", "-C", root, "rev-parse", "--show-superproject-working-tree")
	cmd.Env = gitenv.Environ()

	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	super := strings.TrimSpace(string(out))
	if super == "" {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(super)
	if err != nil {
		return "", false
	}
	return resolved, true
}
