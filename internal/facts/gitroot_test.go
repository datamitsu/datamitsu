package facts

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// layout builds one fixture repository shape under a fresh temp directory and
// returns the directory to resolve from. Every layout is built with real git so
// the differential test compares against the same bytes git itself wrote.
type layout struct {
	name string
	// build returns the directory a lookup starts from.
	build func(t *testing.T, base string) string
	// wantPure is the root gitRootPure must return, relative to the layout's
	// own notion of a root, or "" when the walk is expected to decline.
	wantPure func(base string) string
	// declines marks layouts the pure walk must refuse to answer for.
	declines bool
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// initRepo creates a repository with one commit, which submodules need.
func initRepo(t *testing.T, dir string) {
	t.Helper()

	initRepoWith(t, dir)
}

// initRepoWith is initRepo with extra arguments for `git init`, so a case can
// pick an object format or any other init-time property.
func initRepoWith(t *testing.T, dir string, initArgs ...string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, append([]string{"init", "-q", "-b", "main"}, initArgs...)...)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "file.txt")
	git(t, dir, "commit", "-qm", "init")
}

// appendGitConfig appends raw text to a repository's config, for the config
// spellings `git config` will not write itself (a variable on its section
// header's line, a variable with no value at all).
func appendGitConfig(t *testing.T, repo, text string) {
	t.Helper()

	path := filepath.Join(repo, ".git", "config")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte(text)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// gitOut runs git and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return strings.TrimSpace(string(out))
}

// gitIn runs git with stdin, for the plumbing commands that read their input
// that way (`update-index --index-info`).
func gitIn(t *testing.T, dir, stdin string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	cmd.Stdin = strings.NewReader(stdin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

func gitTestEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
}

// addSubmodule adds child (a path on disk) into parent under name.
func addSubmodule(t *testing.T, parent, child, name string) {
	t.Helper()

	git(t, parent, "-c", "protocol.file.allow=always",
		"submodule", "add", "-q", child, name)
	git(t, parent, "commit", "-qm", "add "+name)
}

func physical(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

func gitRootLayouts() []layout {
	return []layout{
		{
			name: "plain repo",
			build: func(t *testing.T, base string) string {
				t.Helper()
				repo := filepath.Join(base, "repo")
				initRepo(t, repo)
				return repo
			},
			wantPure: func(base string) string { return filepath.Join(base, "repo") },
		},
		{
			name: "nested subdirectory",
			build: func(t *testing.T, base string) string {
				t.Helper()
				repo := filepath.Join(base, "repo")
				initRepo(t, repo)
				sub := filepath.Join(repo, "a", "b", "c")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				return sub
			},
			wantPure: func(base string) string { return filepath.Join(base, "repo") },
		},
		{
			name: "bare repo",
			build: func(t *testing.T, base string) string {
				t.Helper()
				bare := filepath.Join(base, "bare.git")
				if err := os.MkdirAll(bare, 0o755); err != nil {
					t.Fatal(err)
				}
				git(t, bare, "init", "-q", "--bare", "-b", "main")
				return bare
			},
			declines: true,
		},
		{
			name: "linked worktree",
			build: func(t *testing.T, base string) string {
				t.Helper()
				repo := filepath.Join(base, "repo")
				initRepo(t, repo)
				wt := filepath.Join(base, "wt")
				git(t, repo, "worktree", "add", "-q", wt)
				return wt
			},
			wantPure: func(base string) string { return filepath.Join(base, "wt") },
		},
		{
			name: "submodule",
			build: func(t *testing.T, base string) string {
				t.Helper()
				super := filepath.Join(base, "super")
				child := filepath.Join(base, "child")
				initRepo(t, super)
				initRepo(t, child)
				addSubmodule(t, super, child, "sub")
				return filepath.Join(super, "sub")
			},
			wantPure: func(base string) string { return filepath.Join(base, "super") },
		},
		{
			name: "submodule inside a submodule",
			build: func(t *testing.T, base string) string {
				t.Helper()
				super := filepath.Join(base, "super")
				middle := filepath.Join(base, "middle")
				leaf := filepath.Join(base, "leaf")
				initRepo(t, super)
				initRepo(t, middle)
				initRepo(t, leaf)
				addSubmodule(t, middle, leaf, "inner")
				addSubmodule(t, super, middle, "outer")
				git(t, super, "-c", "protocol.file.allow=always",
					"submodule", "update", "-q", "--init", "--recursive")
				return filepath.Join(super, "outer", "inner")
			},
			wantPure: func(base string) string { return filepath.Join(base, "super") },
		},
		{
			name: "no repository at all",
			build: func(t *testing.T, base string) string {
				t.Helper()
				plain := filepath.Join(base, "plain")
				if err := os.MkdirAll(plain, 0o755); err != nil {
					t.Fatal(err)
				}
				return plain
			},
			declines: true,
		},
		{
			name: "inside the git directory",
			build: func(t *testing.T, base string) string {
				t.Helper()
				repo := filepath.Join(base, "repo")
				initRepo(t, repo)
				return filepath.Join(repo, ".git", "refs")
			},
			declines: true,
		},
		{
			name: "repository nested in another repository",
			build: func(t *testing.T, base string) string {
				t.Helper()
				outer := filepath.Join(base, "outer")
				initRepo(t, outer)
				inner := filepath.Join(outer, "vendored")
				initRepo(t, inner)
				return inner
			},
			declines: true,
		},
		{
			name: "empty .git directory",
			build: func(t *testing.T, base string) string {
				t.Helper()
				dir := filepath.Join(base, "no-repo")
				if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			declines: true,
		},
		{
			name: "core.worktree relocates the working tree",
			build: func(t *testing.T, base string) string {
				t.Helper()
				repo := filepath.Join(base, "repo")
				initRepo(t, repo)
				wt := filepath.Join(base, "elsewhere")
				if err := os.MkdirAll(wt, 0o755); err != nil {
					t.Fatal(err)
				}
				git(t, repo, "config", "core.worktree", wt)
				return repo
			},
			declines: true,
		},
		{
			name: "core.worktree on the section header line",
			build: func(t *testing.T, base string) string {
				t.Helper()
				repo := filepath.Join(base, "repo")
				initRepo(t, repo)
				wt := filepath.Join(base, "elsewhere")
				if err := os.MkdirAll(wt, 0o755); err != nil {
					t.Fatal(err)
				}
				// Git accepts a variable on its section header's line, and
				// honours it during discovery just like the multi-line form
				// `git config` writes.
				appendGitConfig(t, repo, "[core] worktree = "+wt+"\n")
				return repo
			},
			declines: true,
		},
		{
			// An ordinary `.git` directory whose config says core.bare=true.
			// Git reports no working tree for it at all ("this operation must
			// be run in a work tree"), so answering with the directory holding
			// `.git` would be a confidently wrong root.
			name: "core.bare set on a repository with a work tree layout",
			build: func(t *testing.T, base string) string {
				t.Helper()
				repo := filepath.Join(base, "repo")
				initRepo(t, repo)
				git(t, repo, "config", "core.bare", "true")
				return repo
			},
			declines: true,
		},
		{
			// Git reads a variable written with no `=` as true, so a lone
			// `bare` line marks the repository bare just as `bare = true` does.
			name: "core.bare written as a valueless variable",
			build: func(t *testing.T, base string) string {
				t.Helper()
				repo := filepath.Join(base, "repo")
				initRepo(t, repo)
				appendGitConfig(t, repo, "[core]\n\tbare\n")
				return repo
			},
			declines: true,
		},
		{
			name: "submodule left behind by a branch that lacks it",
			build: func(t *testing.T, base string) string {
				t.Helper()
				super := filepath.Join(base, "super")
				child := filepath.Join(base, "child")
				initRepo(t, super)
				initRepo(t, child)
				git(t, super, "checkout", "-q", "-b", "with-sub")
				addSubmodule(t, super, child, "sub")
				// Git leaves the submodule's working tree, and its `.git`
				// file, behind on a branch that does not record it.
				git(t, super, "checkout", "-q", "main")
				return filepath.Join(super, "sub")
			},
			declines: true,
		},
		{
			// `git rm --cached sub` drops the gitlink from the index while
			// leaving both `.gitmodules` and the submodule's working tree on
			// disk. Git stops calling it a submodule the moment the gitlink is
			// gone, so `.gitmodules` alone cannot be the proof.
			name: "submodule unregistered from the index",
			build: func(t *testing.T, base string) string {
				t.Helper()
				super := filepath.Join(base, "super")
				child := filepath.Join(base, "child")
				initRepo(t, super)
				initRepo(t, child)
				addSubmodule(t, super, child, "sub")
				git(t, super, "rm", "--cached", "-q", "sub")
				return filepath.Join(super, "sub")
			},
			declines: true,
		},
		{
			// The index does not record its digest size, so the scan settles it
			// by which one accounts for every byte. A sha256 superproject is
			// the case that proves the 32-byte branch is reachable.
			name: "submodule of a sha256 superproject",
			build: func(t *testing.T, base string) string {
				t.Helper()
				super := filepath.Join(base, "super")
				child := filepath.Join(base, "child")
				initRepoWith(t, super, "--object-format=sha256")
				initRepoWith(t, child, "--object-format=sha256")
				addSubmodule(t, super, child, "sub")
				return filepath.Join(super, "sub")
			},
			wantPure: func(base string) string { return filepath.Join(base, "super") },
		},
		{
			// An unmerged path has no stage 0 entry, and git answers from the
			// first `ls-files --stage` line for it — here a regular file at
			// stage 1, so git reports no superproject even though stages 2 and 3
			// are gitlinks. The scan matches stage 0 only, so it declines rather
			// than reproduce that ordering rule.
			name: "submodule path in a file/gitlink conflict",
			build: func(t *testing.T, base string) string {
				t.Helper()
				super := filepath.Join(base, "super")
				child := filepath.Join(base, "child")
				initRepo(t, super)
				initRepo(t, child)
				addSubmodule(t, super, child, "sub")

				commit := gitOut(t, super, "rev-parse", "HEAD:sub")
				blob := gitOut(t, super, "hash-object", "-w", "file.txt")
				git(t, super, "rm", "--cached", "-q", "sub")
				gitIn(t, super,
					"100644 "+blob+" 1\tsub\n"+
						"160000 "+commit+" 2\tsub\n"+
						"160000 "+commit+" 3\tsub\n",
					"update-index", "--index-info")
				return filepath.Join(super, "sub")
			},
			declines: true,
		},
		{
			// Index version 4 prefix-compresses entry paths, which the scan
			// does not reconstruct — so the gitlink cannot be proved and git
			// answers instead.
			name: "submodule of a superproject with an index version 4",
			build: func(t *testing.T, base string) string {
				t.Helper()
				super := filepath.Join(base, "super")
				child := filepath.Join(base, "child")
				initRepo(t, super)
				initRepo(t, child)
				addSubmodule(t, super, child, "sub")
				git(t, super, "update-index", "--index-version", "4")
				return filepath.Join(super, "sub")
			},
			declines: true,
		},
		{
			name: "per-worktree config extension enabled",
			build: func(t *testing.T, base string) string {
				t.Helper()
				repo := filepath.Join(base, "repo")
				initRepo(t, repo)
				git(t, repo, "config", "extensions.worktreeConfig", "true")
				return repo
			},
			declines: true,
		},
		{
			name: "separate git directory",
			build: func(t *testing.T, base string) string {
				t.Helper()
				work := filepath.Join(base, "work")
				if err := os.MkdirAll(work, 0o755); err != nil {
					t.Fatal(err)
				}
				git(t, work, "init", "-q", "-b", "main",
					"--separate-git-dir", filepath.Join(base, "elsewhere.git"))
				return work
			},
			declines: true,
		},
	}
}

func TestGitRootPureLayouts(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	for _, l := range gitRootLayouts() {
		t.Run(l.name, func(t *testing.T) {
			base := physical(t, t.TempDir())
			start := l.build(t, base)

			got, ok := gitRootPure(start)

			if l.declines {
				if ok {
					t.Fatalf("gitRootPure(%q) = %q, true; want a decline", start, got)
				}
				return
			}
			if !ok {
				t.Fatalf("gitRootPure(%q) declined; want %q", start, l.wantPure(base))
			}
			if want := l.wantPure(base); got != want {
				t.Errorf("gitRootPure(%q) = %q, want %q", start, got, want)
			}
		})
	}
}

// The load-bearing test: for every layout the walk answers for, its answer must
// be the one git gives. A layout it declines is answered by git anyway, so the
// only way to be wrong is to answer confidently and differently.
func TestGitRootPureMatchesGitSubprocess(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	for _, l := range gitRootLayouts() {
		t.Run(l.name, func(t *testing.T) {
			base := physical(t, t.TempDir())
			start := l.build(t, base)

			pure, ok := gitRootPure(start)
			if !ok {
				// Nothing to compare: git is what answers for this layout, so
				// its answer is right by construction.
				return
			}

			viaGit, gitErr := resolveGitRootViaGit(context.Background(), start)
			if gitErr != nil {
				t.Fatalf("gitRootPure answered %q but git failed: %v", pure, gitErr)
			}
			if want := physical(t, viaGit); pure != want {
				t.Errorf("gitRootPure(%q) = %q, git says %q", start, pure, want)
			}
		})
	}
}

// countingSubprocessLookup swaps in a counted wrapper around the forking
// resolver so a test can see the fallback engage.
func countingSubprocessLookup(t *testing.T) *atomic.Int64 {
	t.Helper()

	var calls atomic.Int64
	prev := gitSubprocessLookup
	gitSubprocessLookup = func(ctx context.Context, cwd string) (string, error) {
		calls.Add(1)
		return prev(ctx, cwd)
	}
	t.Cleanup(func() { gitSubprocessLookup = prev })

	return &calls
}

func TestResolveGitRootFallsBackForMalformedGitFile(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	base := physical(t, t.TempDir())
	repo := filepath.Join(base, "repo")
	initRepo(t, repo)

	// Replace the .git directory with a file git cannot follow.
	gitDir := filepath.Join(repo, ".git")
	if err := os.Rename(gitDir, filepath.Join(base, "moved.git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gitDir, []byte("this is not a gitdir pointer\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := gitRootPure(repo); ok {
		t.Fatal("gitRootPure() answered for a malformed .git file; want a decline")
	}

	calls := countingSubprocessLookup(t)
	if _, err := resolveGitRoot(context.Background(), repo); err == nil {
		t.Error("resolveGitRoot() should surface git's error for a malformed .git file")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("subprocess resolver ran %d times, want 1 (the fallback must engage)", got)
	}
}

func TestResolveGitRootSkipsSubprocessForPlainRepo(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	base := physical(t, t.TempDir())
	repo := filepath.Join(base, "repo")
	initRepo(t, repo)

	calls := countingSubprocessLookup(t)

	got, err := resolveGitRoot(context.Background(), repo)
	if err != nil {
		t.Fatalf("resolveGitRoot() error = %v", err)
	}
	if got != repo {
		t.Errorf("resolveGitRoot() = %q, want %q", got, repo)
	}
	if calls.Load() != 0 {
		t.Errorf("subprocess resolver ran %d times, want 0", calls.Load())
	}
}

func TestResolveGitRootForceSubprocessEnvVar(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	base := physical(t, t.TempDir())
	repo := filepath.Join(base, "repo")
	initRepo(t, repo)

	calls := countingSubprocessLookup(t)
	t.Setenv("DATAMITSU_FORCE_GIT_SUBPROCESS", "1")

	got, err := resolveGitRoot(context.Background(), repo)
	if err != nil {
		t.Fatalf("resolveGitRoot() error = %v", err)
	}
	if want := physical(t, got); got != want || got != repo {
		t.Errorf("resolveGitRoot() = %q, want %q", got, repo)
	}
	if calls.Load() != 1 {
		t.Errorf("subprocess resolver ran %d times, want 1 (the env var forces it)", calls.Load())
	}
}

func TestParseGitFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantOK  bool
	}{
		{"absolute pointer", "gitdir: /repo/.git/modules/sub\n", "/repo/.git/modules/sub", true},
		{"relative pointer", "gitdir: ../.git/modules/sub", "../.git/modules/sub", true},
		{"no trailing newline", "gitdir: /repo/.git", "/repo/.git", true},
		{"path with spaces", "gitdir: /re po/.git/worktrees/w\n", "/re po/.git/worktrees/w", true},
		{"empty", "", "", false},
		{"wrong prefix", "GITDIR: /repo/.git\n", "", false},
		{"missing target", "gitdir:\n", "", false},
		{"extra lines", "gitdir: /repo/.git\nextra\n", "", false},
		{"plain text", "this is not a gitdir pointer\n", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseGitFile(tt.content)
			if ok != tt.wantOK {
				t.Fatalf("parseGitFile(%q) ok = %v, want %v", tt.content, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("parseGitFile(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestClassifyGitDirPath(t *testing.T) {
	tests := []struct {
		name   string
		gitdir string
		want   gitLinkKind
	}{
		{"submodule", "/repo/.git/modules/sub", gitLinkSubmodule},
		{"nested submodule", "/repo/.git/modules/outer/modules/inner", gitLinkSubmodule},
		{"linked worktree", "/repo/.git/worktrees/wt", gitLinkWorktree},
		{"worktree of a submodule is ambiguous", "/repo/.git/modules/sub/worktrees/wt", gitLinkUnknown},
		{"separate git dir", "/elsewhere/repo.git", gitLinkUnknown},
		{"modules without a .git parent", "/repo/modules/sub", gitLinkUnknown},
		{"git dir itself", "/repo/.git", gitLinkUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyGitDirPath(tt.gitdir); got != tt.want {
				t.Errorf("classifyGitDirPath(%q) = %v, want %v", tt.gitdir, got, tt.want)
			}
		})
	}
}

func TestContainsPath(t *testing.T) {
	tests := []struct {
		name     string
		ancestor string
		path     string
		want     bool
	}{
		{"same path", "/a/b", "/a/b", true},
		{"direct child", "/a/b", "/a/b/c", true},
		{"deep descendant", "/a/b", "/a/b/c/d", true},
		{"parent is not contained", "/a/b", "/a", false},
		{"grandparent is not contained", "/a/b/c", "/a", false},
		{"sibling", "/a/b", "/a/c", false},
		{"prefix but not a component", "/a/b", "/a/bc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsPath(tt.ancestor, tt.path); got != tt.want {
				t.Errorf("containsPath(%q, %q) = %v, want %v", tt.ancestor, tt.path, got, tt.want)
			}
		})
	}
}

// A `.git` entry that exists but cannot be stat'd says nothing about what it
// is. Climbing past it would skip the nearest repository marker and answer with
// an ancestor root, so the walk must decline and let git decide.
func TestGitRootPureDeclinesForUnreadableGitEntry(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}

	base := physical(t, t.TempDir())
	outer := filepath.Join(base, "outer")
	initRepo(t, outer)

	// A repository below outer whose `.git` cannot be reached, because its own
	// directory is not searchable.
	inner := filepath.Join(outer, "inner")
	initRepo(t, inner)
	start := filepath.Join(inner, "work")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}

	if got, ok := gitRootPure(start); !ok || got != inner {
		// The nested-repository rule declines here anyway; assert the fixture
		// is the shape the test needs before making it unreadable.
		if ok {
			t.Fatalf("gitRootPure(%q) = %q; fixture is not the expected shape", start, got)
		}
	}

	if err := os.Chmod(inner, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(inner, 0o755) })

	if got, ok := gitRootPure(start); ok {
		t.Errorf("gitRootPure(%q) = %q, true; want a decline for an unreadable .git entry", start, got)
	}
}

// The subprocess path honours cancellation through exec.CommandContext; the
// pure-Go walk touches only the filesystem, so the dispatcher has to check.
func TestResolveGitRootHonoursCancelledContext(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	base := physical(t, t.TempDir())
	repo := filepath.Join(base, "repo")
	initRepo(t, repo)

	calls := countingSubprocessLookup(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := resolveGitRoot(ctx, repo)
	if err == nil {
		t.Fatalf("resolveGitRoot() = %q, nil; want the cancellation error", got)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("resolveGitRoot() error = %v, want it to wrap context.Canceled", err)
	}
	if calls.Load() != 0 {
		t.Errorf("subprocess resolver ran %d times, want 0", calls.Load())
	}
}

// stubIdentity replaces the ownership/device source with one driven by a table,
// so a test can present layouts that otherwise need root or a spare mount point.
// Paths absent from the table keep the real answer.
func stubIdentity(t *testing.T, overrides map[string]dirIdentity) {
	t.Helper()

	prev := identityOf
	identityOf = func(path string) (dirIdentity, bool) {
		if id, ok := overrides[path]; ok {
			return id, true
		}
		return prev(path)
	}
	t.Cleanup(func() { identityOf = prev })
}

// Git refuses a repository owned by another user ("detected dubious
// ownership") unless safe.directory allows it. The walk must not answer where
// git would refuse, or a foreign-owned datamitsu.config.js at that root becomes
// the project config.
func TestGitRootPureDeclinesForeignOwnedWorkTree(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	base := physical(t, t.TempDir())
	repo := filepath.Join(base, "repo")
	initRepo(t, repo)

	if _, ok := gitRootPure(repo); !ok {
		t.Fatal("gitRootPure declined before the ownership stub; fixture is wrong")
	}

	foreign := dirIdentity{uid: uint64(os.Getuid()) + 1, device: 1}
	stubIdentity(t, map[string]dirIdentity{repo: foreign})

	if got, ok := gitRootPure(repo); ok {
		t.Errorf("gitRootPure(%q) = %q, true; want a decline for a foreign-owned work tree", repo, got)
	}

	// The `.git` entry alone being foreign is equally disqualifying.
	stubIdentity(t, map[string]dirIdentity{filepath.Join(repo, ".git"): foreign})

	if got, ok := gitRootPure(repo); ok {
		t.Errorf("gitRootPure(%q) = %q, true; want a decline for a foreign-owned git dir", repo, got)
	}
}

// Git stops discovery at a mount boundary unless GIT_DISCOVERY_ACROSS_FILESYSTEM
// is set, so a repository above a mount point is not the root for a directory
// inside it.
func TestGitRootPureDeclinesAcrossFilesystemBoundary(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git is not available")
	}

	base := physical(t, t.TempDir())
	repo := filepath.Join(base, "repo")
	initRepo(t, repo)

	mounted := filepath.Join(repo, "mnt", "project")
	if err := os.MkdirAll(mounted, 0o755); err != nil {
		t.Fatal(err)
	}

	if got, ok := gitRootPure(mounted); !ok || got != repo {
		t.Fatalf("gitRootPure(%q) = %q, %v; want %q before the device stub", mounted, got, ok, repo)
	}

	// Present everything at or below the mount point as a different filesystem.
	self := uint64(os.Getuid())
	stubIdentity(t, map[string]dirIdentity{
		mounted:                     {uid: self, device: 2},
		filepath.Join(repo, "mnt"):  {uid: self, device: 2},
		repo:                        {uid: self, device: 1},
		filepath.Join(repo, ".git"): {uid: self, device: 1},
	})

	if got, ok := gitRootPure(mounted); ok {
		t.Errorf("gitRootPure(%q) = %q, true; want a decline across a mount boundary", mounted, got)
	}
}

// TestConfigWithoutWorkTree pins the boolean spellings of `bare`. The false
// side matters most: `git init` writes `bare = false` into every repository
// config, so a key-only match would decline the fast path everywhere.
func TestConfigWithoutWorkTree(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"git init default", "[core]\n\tfilemode = true\n\tbare = false\n", false},
		{"bare false uppercase", "[core]\n\tbare = FALSE\n", false},
		{"bare no", "[core]\n\tbare = no\n", false},
		{"bare off", "[core]\n\tbare = off\n", false},
		{"bare zero", "[core]\n\tbare = 0\n", false},
		{"bare with an empty value", "[core]\n\tbare =\n", false},
		{"bare true", "[core]\n\tbare = true\n", true},
		{"bare true uppercase", "[core]\n\tbare = True\n", true},
		{"bare yes", "[core]\n\tbare = yes\n", true},
		{"bare one", "[core]\n\tbare = 1\n", true},
		{"bare with no value at all", "[core]\n\tbare\n", true},
		{"bare on the section header line", "[core] bare = true\n", true},
		{"a commented-out assignment", "[core]\n\t; bare = true\n", false},
		{"a commented-out valueless variable", "[core]\n\t# bare\n", false},
		{"a branch named bare", "[branch \"bare\"]\n\tremote = origin\n", false},
		{"core.worktree", "[core]\n\tworktree = /elsewhere\n", true},
		{"extensions.worktreeConfig", "[extensions]\n\tworktreeConfig = true\n", true},
		{"a remote named worktree", "[remote \"worktree\"]\n\turl = git@example.com:x.git\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := configWithoutWorkTree(tt.content); got != tt.want {
				t.Errorf("configWithoutWorkTree(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}
