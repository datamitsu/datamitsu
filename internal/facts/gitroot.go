package facts

import (
	"os"
	"path/filepath"
	"strings"
)

// maxGitFileSize bounds how much of a `.git` file is read. A real one is a
// single `gitdir:` line; anything larger is not something to parse.
const maxGitFileSize = 4096

// maxSuperprojectLevels bounds the submodule climb so a `.git` file cycle
// (a gitdir pointing back at an ancestor) cannot spin forever. Nesting this
// deep is not a layout to guess about — the walk gives up and git answers.
const maxSuperprojectLevels = 32

// gitLinkKind classifies what a working tree's `.git` entry says about the
// repository above it.
type gitLinkKind int

const (
	// gitLinkUnknown means the entry cannot be classified confidently. It is
	// the value every unhandled shape collapses to, and it always means
	// "ask git".
	gitLinkUnknown gitLinkKind = iota
	// gitLinkRepo is an ordinary `.git` directory.
	gitLinkRepo
	// gitLinkSubmodule is a `.git` file pointing into <super>/.git/modules/...
	// This is the link `git rev-parse --show-superproject-working-tree`
	// follows, so the climb continues at <super>.
	gitLinkSubmodule
	// gitLinkWorktree is a `.git` file pointing into <main>/.git/worktrees/...
	// A linked worktree's main repository is not a superproject, so the climb
	// stops here.
	gitLinkWorktree
)

// gitRootPure resolves the topmost superproject working tree for cwd by walking
// the filesystem, with no subprocess. The second return value reports whether
// the answer is trustworthy: false means the layout is one the walk declines to
// guess about and the caller must fall back to git.
//
// It reproduces two git behaviours that a naive walk-up-to-the-first-`.git`
// gets wrong:
//
//   - `--show-toplevel` reports a physical path, so the walk starts from cwd
//     with its symlinks resolved.
//   - `--show-superproject-working-tree` climbs out of a submodule into its
//     superproject, and repeating that reaches the topmost one. A submodule and
//     a linked worktree both have a `.git` *file*, and only the submodule
//     climbs, so the two are told apart by where the `gitdir:` line points.
//
// Everything else — a bare repository, a repository nested inside another
// repository's working tree, a separate git directory, an unreadable or
// malformed `.git` file, no repository at all — returns false. Guessing wrong
// here silently produces a wrong project root, and therefore wrong cache keys,
// so the walk answers only for layouts it can prove.
func gitRootPure(cwd string) (string, bool) {
	dir, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", false
	}

	for range maxSuperprojectLevels {
		top, ok := findWorkTreeRoot(dir)
		if !ok {
			return "", false
		}

		gitdir, kind := classifyGitLink(top)
		switch kind {
		case gitLinkRepo, gitLinkWorktree:
			// A repository inside another repository's working tree may be a
			// submodule the outer index records as mode 160000 (an embedded
			// .git directory, as git wrote them before 1.7.8), or may be
			// unrelated. Telling those apart means reading that index; git
			// already does.
			if hasAncestorGitEntry(top) {
				return "", false
			}
			return top, true
		case gitLinkSubmodule:
			super, ok := superprojectOf(gitdir)
			if !ok {
				return "", false
			}
			dir = super
		case gitLinkUnknown:
			return "", false
		}
	}

	return "", false
}

// findWorkTreeRoot climbs from dir to the nearest ancestor holding a usable
// `.git` entry — the working tree root `git rev-parse --show-toplevel` would
// report.
//
// It refuses (false) in three cases: the climb passes through a git directory
// itself (standing inside `.git/`, or in a bare repository — git reports no
// working tree for either), a `.git` directory turns out not to be a valid
// repository (git would keep climbing past it; deciding how far is git's job),
// or no `.git` entry exists at all.
func findWorkTreeRoot(dir string) (string, bool) {
	for {
		if looksLikeGitDir(dir) {
			return "", false
		}

		switch info, err := os.Lstat(filepath.Join(dir, ".git")); {
		case err != nil:
			// keep climbing
		case info.IsDir():
			if !looksLikeGitDir(filepath.Join(dir, ".git")) {
				return "", false
			}
			return dir, true
		default:
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// hasGitEntry reports whether dir holds a `.git` entry of any kind.
func hasGitEntry(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// hasAncestorGitEntry reports whether any strict ancestor of dir holds a `.git`
// entry — the marker of a repository nested inside another one.
func hasAncestorGitEntry(dir string) bool {
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent

		if hasGitEntry(dir) {
			return true
		}
	}
}

// looksLikeGitDir reports whether dir is itself a git directory: a bare
// repository, or the `.git` directory of a normal one. Both hold HEAD, objects
// and refs at the top level.
func looksLikeGitDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		return false
	}
	for _, name := range []string{"objects", "refs"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// classifyGitLink reads the `.git` entry of the working tree root top and
// reports the git directory it designates together with its kind. The returned
// path is meaningful only for gitLinkSubmodule.
func classifyGitLink(top string) (string, gitLinkKind) {
	path := filepath.Join(top, ".git")

	info, err := os.Lstat(path)
	if err != nil {
		return "", gitLinkUnknown
	}
	if info.IsDir() {
		return path, gitLinkRepo
	}
	if !info.Mode().IsRegular() || info.Size() > maxGitFileSize {
		return "", gitLinkUnknown
	}

	raw, err := os.ReadFile(path) // #nosec G304 -- path is <worktree>/.git, size-checked above
	if err != nil {
		return "", gitLinkUnknown
	}

	gitdir, ok := parseGitFile(string(raw))
	if !ok {
		return "", gitLinkUnknown
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(top, gitdir)
	}
	gitdir = filepath.Clean(gitdir)

	// A pointer to a git directory that is not there is a broken checkout, and
	// what git makes of it is git's to say.
	if info, err := os.Stat(gitdir); err != nil || !info.IsDir() {
		return "", gitLinkUnknown
	}

	return gitdir, classifyGitDirPath(gitdir)
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

// classifyGitDirPath decides what a `.git` file's target says about the
// repository above it, from the path alone.
//
// A submodule's git directory lives at <super>/.git/modules/<name> (and nests
// as .../modules/<outer>/modules/<inner>); a linked worktree's lives at
// <main>/.git/worktrees/<name>. A path carrying both markers — a linked
// worktree of a submodule, or a submodule literally named "worktrees" — is
// ambiguous and left to git.
func classifyGitDirPath(gitdir string) gitLinkKind {
	parts := strings.Split(filepath.ToSlash(gitdir), "/")

	tail := parts
	for i, part := range parts {
		if part == ".git" {
			tail = parts[i+1:]
			break
		}
	}
	if len(tail) == len(parts) || len(tail) == 0 {
		return gitLinkUnknown
	}

	var kind gitLinkKind
	switch tail[0] {
	case "modules":
		kind = gitLinkSubmodule
	case "worktrees":
		kind = gitLinkWorktree
	default:
		return gitLinkUnknown
	}

	// Both markers in one path is a linked worktree of a submodule (or a
	// submodule named "worktrees"). Which link to follow is not decidable from
	// the path, so it is not decided here.
	for _, part := range tail[1:] {
		if part == "modules" && kind == gitLinkWorktree {
			return gitLinkUnknown
		}
		if part == "worktrees" && kind == gitLinkSubmodule {
			return gitLinkUnknown
		}
	}

	return kind
}

// superprojectOf maps a submodule git directory (<super>/.git/modules/...) back
// to the superproject working tree. It takes the outermost `.git` component, so
// a nested submodule jumps straight to the top of the chain, and it verifies
// the result really is a working tree before returning it.
func superprojectOf(gitdir string) (string, bool) {
	parts := strings.Split(gitdir, string(filepath.Separator))

	for i, part := range parts {
		if part != ".git" || i == 0 {
			continue
		}

		super := strings.Join(parts[:i], string(filepath.Separator))
		if super == "" {
			return "", false
		}
		if !hasGitEntry(super) {
			return "", false
		}

		resolved, err := filepath.EvalSymlinks(super)
		if err != nil {
			return "", false
		}
		return resolved, true
	}

	return "", false
}
