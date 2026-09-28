// Package gitutil reads what git says about a working tree, for a run that
// has to know which files its tools changed.
package gitutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/gitenv"
	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// Snapshot is the dirty files of a working tree at one moment — those git
// reports as differing from the index, or untracked — each with a hash of its
// content. A file absent from it was clean: its content was the index's.
// Nothing replaces it once taken.
type Snapshot struct {
	entries map[string]entry
}

type entry struct {
	tracked bool
	// hash is the XXH3 of the content, or of a symbolic link's target; ""
	// for a file the working tree does not hold.
	hash string
	// executable is the file's executable bit, which git tracks too.
	executable bool
}

// Len is how many dirty files the snapshot holds.
func (s Snapshot) Len() int { return len(s.entries) }

// Environ is the environment a snapshot runs git with: the process's, without
// the variables that bind git to a hook's repository, but with the index a hook
// set, so that a snapshot inside `git commit` reads the index being committed.
func Environ() []string {
	env := gitenv.Environ()
	if index, ok := os.LookupEnv("GIT_INDEX_FILE"); ok && index != "" {
		env = append(env, "GIT_INDEX_FILE="+index)
	}
	return env
}

// Take snapshots the working tree of the repository at root. It runs
// `git status --porcelain=v2 -z --untracked-files=all` without optional locks
// — a hook's `git commit` holds index.lock, and a status that refreshed the
// index would fail on it — and hashes every dirty file. env is git's
// environment (Environ). An index git cannot read is a failed snapshot, never
// a reason to read another: snapshots of one run compare against one index.
func Take(ctx context.Context, root string, env []string) (Snapshot, error) {
	out, err := status(ctx, root, env)
	if err != nil {
		return Snapshot{}, err
	}
	paths, err := parse(out)
	if err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{entries: make(map[string]entry, len(paths))}
	for path, tracked := range paths {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, fmt.Errorf("hash dirty files: %w", err)
		}
		e, err := entryOf(ctx, filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return Snapshot{}, fmt.Errorf("hash %s: %w", path, err)
		}
		e.tracked = tracked
		s.entries[path] = e
	}
	return s, nil
}

func status(ctx context.Context, root string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "status", "--porcelain=v2", "-z",
		"--untracked-files=all", "--ignore-submodules=all", "--no-renames")
	cmd.Dir = root
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("git status: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("git status: %w", err)
	}
	return stdout.Bytes(), nil
}

// parse reads porcelain v2 records, NUL-separated, into the dirty paths and
// whether each is tracked. A directory entry — a nested repository git does
// not descend into — is outside the scope.
func parse(out []byte) (map[string]bool, error) {
	paths := map[string]bool{}
	records := strings.Split(string(out), "\x00")
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if rec == "" {
			continue
		}
		var path string
		tracked := true
		switch rec[0] {
		case '1':
			path = field(rec, 8)
		case '2':
			path = field(rec, 9)
			i++ // the original path of a rename or copy
		case 'u':
			path = field(rec, 10)
		case '?':
			path, tracked = strings.TrimPrefix(rec, "? "), false
		case '!', '#':
			continue
		default:
			return nil, fmt.Errorf("git status: unexpected record %q", rec)
		}
		if path == "" {
			return nil, fmt.Errorf("git status: unexpected record %q", rec)
		}
		if strings.HasSuffix(path, "/") {
			continue
		}
		paths[path] = tracked
	}
	return paths, nil
}

// field returns what follows the first n space-separated fields of rec: the
// path, which may hold spaces itself.
func field(rec string, n int) string {
	rest := rec
	for range n {
		_, after, ok := strings.Cut(rest, " ")
		if !ok {
			return ""
		}
		rest = after
	}
	return rest
}

// entryOf is what a snapshot records of a file: the XXH3 of its content, or
// of a symbolic link's target, and its executable bit; an empty hash when the
// working tree does not hold it. Reading stops when ctx is done.
func entryOf(ctx context.Context, path string) (entry, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return entry{}, nil
	}
	if err != nil {
		return entry{}, fmt.Errorf("stat: %w", err)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return entry{}, fmt.Errorf("read link: %w", err)
		}
		return entry{hash: "link:" + hashutil.XXH3Hex([]byte(target))}, nil
	case info.IsDir():
		return entry{hash: "dir"}, nil
	case !info.Mode().IsRegular():
		// A named pipe, a socket or a device is never opened: opening a pipe
		// waits for a writer.
		return entry{hash: "special:" + info.Mode().Type().String()}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return entry{}, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	hash, err := hashutil.XXH3Reader(stoppableReader{stopped: ctx.Err, r: f})
	if err != nil {
		return entry{}, fmt.Errorf("read: %w", err)
	}
	return entry{hash: hash, executable: info.Mode()&0o111 != 0}, nil
}

// stoppableReader stops reading once stopped says why: a large dirty file
// must not hold a snapshot past its deadline.
type stoppableReader struct {
	stopped func() error
	r       io.Reader
}

func (s stoppableReader) Read(p []byte) (int, error) {
	if err := s.stopped(); err != nil {
		return 0, fmt.Errorf("stopped: %w", err)
	}
	return s.r.Read(p) //nolint:wrapcheck // a reader hands io.EOF on as it is
}

// Change kinds.
const (
	// Created: the file did not exist before and does after.
	Created = "created"
	// Modified: the file existed before and after with other content.
	Modified = "modified"
	// Deleted: the file existed before and does not after.
	Deleted = "deleted"
	// Reverted: a dirty file came back to its index content — a formatter
	// that restored what HEAD holds.
	Reverted = "reverted"
)

// Change is one file whose state differs between two snapshots; Path is
// relative to the repository root with "/".
type Change struct {
	Path string
	Kind string
}

// Diff lists what changed from s to after, over the union of the paths either
// holds, sorted by path.
func (s Snapshot) Diff(after Snapshot) []Change {
	var out []Change
	for path, a := range after.entries {
		b, dirtyBefore := s.entries[path]
		switch {
		case !dirtyBefore && !a.tracked:
			out = append(out, Change{path, Created})
		case !dirtyBefore && a.hash == "":
			out = append(out, Change{path, Deleted})
		case !dirtyBefore:
			out = append(out, Change{path, Modified})
		case b.hash == a.hash && b.executable == a.executable:
		case b.hash == "":
			out = append(out, Change{path, Created})
		case a.hash == "":
			out = append(out, Change{path, Deleted})
		default:
			out = append(out, Change{path, Modified})
		}
	}
	for path, b := range s.entries {
		if _, dirtyAfter := after.entries[path]; dirtyAfter {
			continue
		}
		if b.tracked {
			out = append(out, Change{path, Reverted})
		} else {
			out = append(out, Change{path, Deleted})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
