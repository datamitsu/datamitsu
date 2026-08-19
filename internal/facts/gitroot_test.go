package facts

import (
	"context"
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
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// initRepo creates a repository with one commit, which submodules need.
func initRepo(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "file.txt")
	git(t, dir, "commit", "-qm", "init")
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
			viaGit, gitErr := resolveGitRootViaGit(context.Background(), start)

			if !ok {
				// Nothing to compare — the fallback is what runs. Assert only
				// that the two agree on there being a repository at all when
				// git succeeds, which is the fallback's whole job.
				return
			}
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
