package facts

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// dirIdentity carries the two properties of a directory that decide whether
// repository discovery may continue through it.
type dirIdentity struct {
	uid    uint64
	device uint64
}

// identityOf is the seam the walk reads directory identity through. It is a
// package variable so tests can present ownership and mount layouts that cannot
// be built in a temp directory without root.
var identityOf = statIdentity

// ownedByCurrentUser reports whether path belongs to the user running this
// process. It mirrors, conservatively, the ownership check git performs before
// it will use a repository (`safe.directory`, "detected dubious ownership"): a
// path this returns false for is one git may refuse outright, so the walk must
// not answer for it. Anything unreadable, or on a platform with no ownership
// data, counts as not owned — declining costs a subprocess, answering costs
// correctness.
func ownedByCurrentUser(path string) bool {
	id, ok := identityOf(path)
	return ok && id.uid == uint64(os.Getuid()) //nolint:gosec // uid is non-negative on every platform reaching this
}

// deviceOf reports the filesystem a directory lives on.
func deviceOf(path string) (uint64, bool) {
	id, ok := identityOf(path)
	return id.device, ok
}

// maxGitFileSize bounds how much of a `.git` file is read. A real one is a
// single `gitdir:` line; anything larger is not something to parse.
const maxGitFileSize = 4096

// maxSuperprojectLevels bounds the submodule climb so a `.git` file cycle
// (a gitdir pointing back at an ancestor) cannot spin forever. Nesting this
// deep is not a layout to guess about — the walk gives up and git answers.
const maxSuperprojectLevels = 32

// maxGitConfigSize bounds how much of a repository config is read when checking
// for a relocated working tree. Real configs are a few hundred bytes; anything
// past this is not something to scan on the startup path.
const maxGitConfigSize = 1 << 20

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
// malformed `.git` file, a working tree owned by another user or reached only by
// crossing a mount boundary (see findWorkTreeRoot), no repository at all —
// returns false. Guessing wrong here silently produces a wrong project root, and
// therefore wrong cache keys, so the walk answers only for layouts it can prove.
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
			// core.worktree moves the working tree away from the directory
			// holding `.git`, and core.bare says there is no working tree at
			// all, so in either case the walk's answer would not be the one
			// `--show-toplevel` gives. Resolving where core.worktree points
			// (and what a per-worktree config layers on top) means implementing
			// git's config lookup; git already has one.
			if declaresNoWorkTreeHere(gitdir, kind) {
				return "", false
			}
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
			super, ok := superprojectOf(gitdir, top)
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
// It refuses (false) in six cases: the climb passes through a git directory
// itself (standing inside `.git/`, or in a bare repository — git reports no
// working tree for either), a `.git` directory turns out not to be a valid
// repository (git would keep climbing past it; deciding how far is git's job),
// a `.git` entry exists but cannot be stat'd (a permission or IO error says
// nothing about what is there, and climbing past it would answer with an
// ancestor root), no `.git` entry exists at all, the climb would leave the
// filesystem it started on, or the working tree it lands on is not owned by the
// current user.
//
// The last two are git behaviours a plain walk up the tree does not have. Git
// stops discovery at a mount boundary unless GIT_DISCOVERY_ACROSS_FILESYSTEM is
// set, so a repository above a mount point is not the answer for a directory
// inside it; and git refuses a repository owned by another user unless
// `safe.directory` says otherwise. Without either check the walk would answer —
// with a root, and therefore a project config to evaluate — where git reports no
// repository at all.
func findWorkTreeRoot(dir string) (string, bool) {
	device, ok := deviceOf(dir)
	if !ok {
		return "", false
	}

	for {
		if looksLikeGitDir(dir) {
			return "", false
		}

		switch info, err := os.Lstat(filepath.Join(dir, ".git")); {
		case errors.Is(err, fs.ErrNotExist):
			// No marker here; keep climbing.
		case err != nil:
			// Something is there but cannot be read — a permission or IO error.
			// Climbing past it would skip the nearest repository marker and
			// answer with an ancestor root, so the walk declines instead.
			return "", false
		case info.IsDir():
			if !looksLikeGitDir(filepath.Join(dir, ".git")) {
				return "", false
			}
			return acceptWorkTreeRoot(dir)
		default:
			return acceptWorkTreeRoot(dir)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}

		parentDevice, ok := deviceOf(parent)
		if !ok || parentDevice != device {
			return "", false
		}
		dir = parent
	}
}

// acceptWorkTreeRoot returns dir as the working tree root only if the current
// user owns both it and its `.git` entry. Git checks the repository it is about
// to use, not the directory the search began in, so the check belongs here
// rather than at the start of the climb.
func acceptWorkTreeRoot(dir string) (string, bool) {
	if !ownedByCurrentUser(dir) || !ownedByCurrentUser(filepath.Join(dir, ".git")) {
		return "", false
	}
	return dir, true
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

// declaresNoWorkTreeHere reports whether the repository at gitdir has no
// working tree the walk may report, for either of two reasons.
//
// The first is relocation — `core.worktree` in the repository config, or the
// `extensions.worktreeConfig` that lets a per-worktree config set it. Without
// this check a repository with `core.worktree` set resolves to the directory
// holding `.git`, while git reports the configured working tree instead.
//
// The second is `core.bare`. A directory can hold a perfectly ordinary `.git`
// directory and still be marked bare, and for a bare repository git has no
// working tree at all: `--show-toplevel` fails with "this operation must be run
// in a work tree". The walk would otherwise answer with the directory holding
// `.git`, which is exactly the confident-wrong-root failure it exists to avoid.
// This one check is value-aware — `bare = false` is written into every
// `git init` config, so matching the key alone would disable the fast path
// everywhere.
//
// It is otherwise deliberately blunt: it matches on the key alone, without
// resolving sections, so `[remote "worktree"]` or any other stray occurrence of
// the key costs the fast path and nothing else. An unreadable, implausibly
// large, or entirely absent config counts as "no usable working tree" for the
// same reason — declining is free, guessing is not.
//
// The candidates are the repository config, the per-worktree config git writes
// under the worktree's own git directory, and — for a linked worktree, whose
// git directory is <main>/.git/worktrees/<name> and holds no config of its own
// — the shared config two levels up.
func declaresNoWorkTreeHere(gitdir string, kind gitLinkKind) bool {
	candidates := []string{
		filepath.Join(gitdir, "config"),
		filepath.Join(gitdir, "config.worktree"),
	}
	if kind == gitLinkWorktree {
		candidates = append(candidates, filepath.Join(filepath.Dir(filepath.Dir(gitdir)), "config"))
	}

	found := false
	for _, path := range candidates {
		info, err := os.Stat(path)
		switch {
		case os.IsNotExist(err):
			continue
		case err != nil || info.Size() > maxGitConfigSize:
			return true
		}
		found = true

		raw, err := os.ReadFile(path) // #nosec G304 -- path is a git config under gitdir, size-checked above
		if err != nil {
			return true
		}
		if configWithoutWorkTree(string(raw)) {
			return true
		}
	}

	// A repository with no config at all is not a shape to reason about.
	return !found
}

// configWithoutWorkTree reports whether a git config says the repository has no
// working tree at the directory holding `.git` — it names the `worktree` or
// `worktreeConfig` key, or it sets `bare` to true. Only the left-hand side of an
// assignment is examined, so a remote URL or a branch name containing the word
// does not match.
func configWithoutWorkTree(content string) bool {
	for line := range strings.SplitSeq(content, "\n") {
		key, value, ok := configAssignment(line)
		if !ok {
			// A variable with no `=` is git's shorthand for "true", so a lone
			// `bare` line marks the repository bare just as `bare = true` does.
			key, value = configFlagName(line), "true"
		}
		if strings.EqualFold(key, "worktree") || strings.EqualFold(key, "worktreeConfig") {
			return true
		}
		if strings.EqualFold(key, "bare") && !isGitConfigFalse(value) {
			return true
		}
	}
	return false
}

// configFlagName returns the variable named by a git config line carrying no
// `=`, with any leading section header dropped the way configAssignment drops
// it. A line that is empty, a bare section header, or a comment names nothing.
func configFlagName(line string) string {
	if i := strings.LastIndex(line, "]"); i >= 0 {
		line = line[i+1:]
	}
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
		return ""
	}
	return line
}

// isGitConfigFalse reports whether value is one of the spellings git reads as a
// false boolean. Everything else — including a value git would reject outright —
// counts as true, so the walk declines rather than guesses.
func isGitConfigFalse(value string) bool {
	switch strings.ToLower(value) {
	case "", "false", "no", "off", "0":
		return true
	default:
		return false
	}
}

// configAssignment splits one git config line into its key and value. Git
// accepts a variable on the same line as its section header
// (`[core] worktree = /elsewhere`), so any leading section header is dropped
// before the key is returned — comparing against the raw left-hand side would
// miss that form. Dropping everything up to the last `]` also strips the
// subsection quoting of `[remote "worktree"] url = ...`, leaving `url`.
func configAssignment(line string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	if i := strings.LastIndex(key, "]"); i >= 0 {
		key = key[i+1:]
	}
	return strings.TrimSpace(key), strings.TrimSpace(value), true
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

// SuperprojectOf reports the working tree that records root as a submodule —
// the one level `git rev-parse --show-superproject-working-tree` climbs — when
// the filesystem proves it, and false when it does not.
//
// It is the proof gitRootPure climbs on, exported so a caller outside this
// package (the source-mode shim, which must select a farm for exactly the root
// GetGitRoot resolves) gates its own climb on the same condition rather than on
// a weaker path-shaped guess. False means "not proven", which is not the same as
// "no superproject": a caller that must be sure asks git.
func SuperprojectOf(root string) (string, bool) {
	top, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}

	gitdir, kind := classifyGitLink(top)
	if kind != gitLinkSubmodule {
		return "", false
	}
	return superprojectOf(gitdir, top)
}

// superprojectOf maps the submodule working tree at top, whose git directory is
// gitdir (<owner>/.git/modules/...), to the superproject working tree one level
// up. gitRootPure calls it repeatedly, so a nested submodule reaches the top of
// the chain one level at a time.
//
// A `.git` file pointing into `modules/` is not on its own proof that the climb
// is the one git makes: git reports a superproject only while the superproject's
// index still records that path as a gitlink, and it leaves the submodule's
// working tree (with its `.git` file) behind both when you check out a branch
// that does not have the submodule and when you `git rm --cached` it. In those
// states `--show-superproject-working-tree` is empty and `--show-toplevel` is the
// submodule itself, so climbing would answer with a root git disagrees with.
// Three things are therefore proved before the climb is accepted: the repository
// owning gitdir really does contain that superproject (a `.git` file pointing
// into an unrelated repository's `modules/` directory climbs nowhere), the
// superproject declares a submodule at this path in `.gitmodules`, and — the
// condition git itself tests — its index holds a gitlink there.
func superprojectOf(gitdir, top string) (string, bool) {
	owner, ok := gitDirOwner(gitdir)
	if !ok {
		return "", false
	}
	owner, err := filepath.EvalSymlinks(owner)
	if err != nil {
		return "", false
	}

	super, ok := nearestGitAncestor(top)
	if !ok {
		return "", false
	}
	if !containsPath(owner, super) {
		return "", false
	}

	rel, ok := submodulePath(super, top)
	if !ok {
		return "", false
	}
	// `.gitmodules` is checked first because it is the cheaper read and rules
	// out the common shapes; the index is what actually decides.
	if !declaresSubmoduleAt(super, rel) || !recordsGitlinkAt(super, rel) {
		return "", false
	}

	return super, true
}

// gitDirOwner returns the working tree whose `.git` holds gitdir — everything
// before the outermost `.git` component of the path.
func gitDirOwner(gitdir string) (string, bool) {
	parts := strings.Split(gitdir, string(filepath.Separator))

	for i, part := range parts {
		if part != ".git" || i == 0 {
			continue
		}
		owner := strings.Join(parts[:i], string(filepath.Separator))
		if owner == "" {
			return "", false
		}
		return owner, true
	}

	return "", false
}

// nearestGitAncestor returns the closest strict ancestor of dir holding a `.git`
// entry — where a submodule's own superproject working tree root sits. dir is
// physical (see gitRootPure), so the result is too.
func nearestGitAncestor(dir string) (string, bool) {
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent

		if hasGitEntry(dir) {
			return dir, true
		}
	}
}

// containsPath reports whether ancestor is path or one of its ancestors. A
// relative path of exactly ".." means path is ancestor's parent, which is the
// opposite of containment and so must be rejected alongside "../...".
func containsPath(ancestor, path string) bool {
	rel, err := filepath.Rel(ancestor, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}

// submodulePath returns the location of the working tree at sub relative to
// super, slash-separated — the path `.gitmodules` and the index both spell.
// Both arguments are physical (see gitRootPure), so the relative path between
// them is the real one. Anything that is not a strict descendant is not a
// submodule path.
func submodulePath(super, sub string) (string, bool) {
	rel, err := filepath.Rel(super, sub)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// declaresSubmoduleAt reports whether super's `.gitmodules` records a submodule
// whose path is rel.
//
// A missing, unreadable or implausibly large `.gitmodules`, or one that does not
// name this path, means the climb is not provably git's — the caller declines and
// git answers. `.gitmodules` alone is not sufficient proof (git consults the
// index, not this file), which is why superprojectOf pairs it with
// recordsGitlinkAt.
func declaresSubmoduleAt(super, rel string) bool {
	path := filepath.Join(super, ".gitmodules")
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxGitConfigSize {
		return false
	}

	raw, err := os.ReadFile(path) // #nosec G304 -- path is <super>/.gitmodules, size-checked above
	if err != nil {
		return false
	}

	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, ok := configAssignment(line)
		if !ok || !strings.EqualFold(key, "path") {
			continue
		}
		if filepath.ToSlash(filepath.Clean(value)) == rel {
			return true
		}
	}
	return false
}
