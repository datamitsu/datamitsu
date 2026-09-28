package gitutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/gittest"
)

func TestMain(m *testing.M) {
	gittest.Isolate()
	os.Exit(m.Run())
}

type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &repo{t: t, dir: dir}
	r.git("init", "-q")
	r.git("config", "user.name", "gitutil-test")
	r.git("config", "user.email", "gitutil@datamitsu.invalid")
	return r
}

func (r *repo) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (r *repo) write(rel, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) remove(rel string) {
	r.t.Helper()
	if err := os.Remove(filepath.Join(r.dir, rel)); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) snapshot(env []string) Snapshot {
	r.t.Helper()
	if env == nil {
		env = Environ()
	}
	s, err := Take(context.Background(), r.dir, env)
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

// committed is a repository with clean.txt, dirty.txt and gone.txt committed,
// dirty.txt then modified and stale.txt untracked.
func committed(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t)
	r.write("clean.txt", "clean\n")
	r.write("dirty.txt", "committed\n")
	r.write("gone.txt", "gone\n")
	r.write(".gitignore", "ignored/\n")
	r.git("add", ".")
	r.git("commit", "-q", "-m", "init")
	r.write("dirty.txt", "edited\n")
	r.write("stale.txt", "untracked\n")
	return r
}

func TestDiff(t *testing.T) {
	r := committed(t)
	before := r.snapshot(nil)
	if before.Len() != 2 {
		t.Fatalf("before holds %d dirty files, want dirty.txt and stale.txt", before.Len())
	}

	r.write("clean.txt", "formatted\n")      // clean → modified
	r.write("dirty.txt", "committed\n")      // dirty → back to the index: reverted
	r.write("new dir/a.txt", "a\n")          // an untracked directory with two files
	r.write("new dir/b.txt", "b\n")          // is two created entries
	r.remove("gone.txt")                     // tracked → deleted
	r.remove("stale.txt")                    // untracked → deleted
	r.write("ignored/out.txt", "never seen") // ignored: outside the scope

	got := before.Diff(r.snapshot(nil))
	want := []Change{
		{"clean.txt", Modified},
		{"dirty.txt", Reverted},
		{"gone.txt", Deleted},
		{"new dir/a.txt", Created},
		{"new dir/b.txt", Created},
		{"stale.txt", Deleted},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Diff =\n%v\nwant\n%v", got, want)
	}
}

func TestDiffOfDirtyFiles(t *testing.T) {
	r := committed(t)
	before := r.snapshot(nil)
	r.write("dirty.txt", "edited again\n")
	r.write("stale.txt", "untracked, edited\n")
	got := before.Diff(r.snapshot(nil))
	want := []Change{{"dirty.txt", Modified}, {"stale.txt", Modified}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Diff = %v, want %v", got, want)
	}
	// A dirty file rewritten with the same bytes did not change.
	same := r.snapshot(nil)
	r.write("dirty.txt", "edited again\n")
	if got := same.Diff(r.snapshot(nil)); len(got) != 0 {
		t.Errorf("Diff = %v, want none", got)
	}
}

// TestTakeUnderAHook: a hook's `git commit` holds index.lock and may point
// GIT_INDEX_FILE at the index it commits; the snapshot takes no lock and reads
// that index.
func TestTakeUnderAHook(t *testing.T) {
	r := committed(t)
	lock := filepath.Join(r.dir, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if s := r.snapshot(nil); s.Len() != 2 {
		t.Errorf("with index.lock present the snapshot holds %d files, want 2", s.Len())
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("index.lock was touched: %v", err)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}

	// The index a commit writes stands in a file of its own: stale.txt is
	// staged there, so the snapshot reads it as tracked, where the
	// repository's index still has it untracked.
	index := filepath.Join(r.dir, ".git", "next-index")
	data, err := os.ReadFile(filepath.Join(r.dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, data, 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "GIT_INDEX_FILE="+index)
	add := exec.Command("git", "add", "stale.txt")
	add.Dir, add.Env = r.dir, env
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("staging into the other index: %v\n%s", err, out)
	}
	if s := r.snapshot(env); !s.entries["stale.txt"].tracked {
		t.Error("under GIT_INDEX_FILE stale.txt should be tracked")
	}
	if s := r.snapshot(nil); s.entries["stale.txt"].tracked {
		t.Error("with the repository's index stale.txt should be untracked")
	}
}

func TestTakeOutsideARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Take(context.Background(), dir, append(Environ(), "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir)))
	if err == nil || !strings.Contains(err.Error(), "git status") {
		t.Errorf("Take outside a repository = %v, want a git status error", err)
	}
}

func TestParse(t *testing.T) {
	out := "1 .M N... 100644 100644 100644 h1 h2 a file.txt\x00" +
		"2 R. N... 100644 100644 100644 h3 h4 R100 new.txt\x00old.txt\x00" +
		"u UU N... 100644 100644 100644 100644 a b c conflict.txt\x00" +
		"? untracked.txt\x00" +
		"? nested/\x00" +
		"! ignored.txt\x00"
	got, err := parse([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"a file.txt": true, "new.txt": true, "conflict.txt": true, "untracked.txt": false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parse = %v, want %v", got, want)
	}
	if _, err := parse([]byte("x what\x00")); err == nil {
		t.Error("an unknown record parsed")
	}
}
