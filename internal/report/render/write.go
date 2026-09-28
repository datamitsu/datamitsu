package render

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
)

// Target is one report on its way to its path: a temporary file beside it,
// renamed over it once written, so that a reader never sees half a report; a
// directory whose files are each written that way; or stdout. A format with a
// completeness companion gets one beside its file, written the same way.
type Target struct {
	Spec     Spec
	renderer Renderer
	tmp      *os.File
	// companion is the temporary file of the completeness companion; nil for
	// a format without one, and on stdout.
	companion *os.File
	// appendTo is the file an appending format adds its entry to.
	appendTo *os.File
	stdout   io.Writer
	// Err is why the target could not be opened; nil when it was.
	Err error
}

// Open prepares spec's target before anything is rendered, so that a document
// can record which of its exports could not be written. Missing directories
// are created. stdout is where "-" writes.
func Open(spec Spec, stdout io.Writer) *Target {
	t := &Target{Spec: spec, stdout: stdout}
	r, ok := Lookup(spec.Format)
	if !ok {
		t.Err = fmt.Errorf("unknown report format %q", spec.Format)
		return t
	}
	t.renderer = r
	switch {
	case spec.Stdout():
	case spec.Dir():
		t.Err = makeDir(spec.Path)
	case appends(r):
		t.appendTo, t.Err = openAppend(spec.Path)
	default:
		t.tmp, t.Err = createBeside(spec.Path)
		if _, companioned := r.(Companioned); companioned && t.Err == nil {
			if t.companion, t.Err = createBeside(common.CompanionPath(spec.Path)); t.Err != nil {
				_ = t.tmp.Close()
				_ = os.Remove(t.tmp.Name())
			}
		}
	}
	return t
}

// Write renders run into the target and moves it into place. On failure
// nothing is left at the path that was not there before; a directory keeps
// the files written before the one that failed. A format that declines the
// run writes nothing and returns a DeclinedError.
func (t *Target) Write(run *report.Run) error {
	if t.Err != nil {
		return t.Err
	}
	if why := declines(t.Spec, run); why != "" {
		t.clear()
		return DeclinedError{Reason: why}
	}
	switch {
	case t.Spec.Dir() && !t.Spec.Stdout():
		return t.writeDir(run)
	case t.appendTo != nil:
		return t.writeAppend(run)
	case t.tmp == nil:
		w := bufio.NewWriter(t.stdout)
		if err := t.renderer.Render(w, run, t.Spec.Options); err != nil {
			return err
		}
		if err := w.Flush(); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		return nil
	}
	err := fill(t.tmp, func(w io.Writer) error { return t.renderer.Render(w, run, t.Spec.Options) })
	if c, ok := t.renderer.(Companioned); ok && t.companion != nil {
		companion := c.Companion(run, t.Spec.Options)
		if companionErr := fill(t.companion, companion.Write); err == nil {
			err = companionErr
		}
	}
	// A companion must never stand beside a report it does not describe,
	// which a job would read as that report's, not even for a moment or after
	// a crash: the earlier one goes before the report is replaced, and the new
	// one comes after. In between there is none, which no job publishes on.
	if err == nil && t.companion != nil {
		if removeErr := os.Remove(common.CompanionPath(t.Spec.Path)); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = removeErr
		}
	}
	if err == nil {
		err = os.Rename(t.tmp.Name(), t.Spec.Path)
	}
	if err == nil && t.companion != nil {
		err = os.Rename(t.companion.Name(), common.CompanionPath(t.Spec.Path))
	}
	if err != nil {
		_ = os.Remove(t.tmp.Name())
		if t.companion != nil {
			_ = os.Remove(t.companion.Name())
		}
		return unwrapPathError(err)
	}
	return nil
}

// clear leaves nothing at a declined target that an earlier run wrote: a
// file there, or a split format's files in its directory, would be read as
// this run's.
func (t *Target) clear() {
	if t.appendTo != nil {
		_ = t.appendTo.Close()
	}
	if t.tmp != nil {
		_ = t.tmp.Close()
		_ = os.Remove(t.tmp.Name())
		if info, err := os.Lstat(t.Spec.Path); err == nil && info.Mode().IsRegular() {
			_ = os.Remove(t.Spec.Path)
		}
	}
	if t.companion != nil {
		_ = t.companion.Close()
		_ = os.Remove(t.companion.Name())
		_ = os.Remove(common.CompanionPath(t.Spec.Path))
	}
	if d, ok := t.renderer.(DirRenderer); ok && t.Spec.Dir() {
		_ = t.removeOwned(d, nil)
	}
}

// writeAppend adds the run's entry to the end of its file in one write, which
// a local file system keeps whole beside another process appending to the
// same file; a network file system may not.
func (t *Target) writeAppend(run *report.Run) error {
	var buf bytes.Buffer
	err := t.renderer.Render(&buf, run, t.Spec.Options)
	if err == nil {
		_, err = t.appendTo.Write(buf.Bytes())
	}
	if closeErr := t.appendTo.Close(); err == nil {
		err = closeErr
	}
	return unwrapPathError(err)
}

func appends(r Renderer) bool {
	_, ok := r.(Appending)
	return ok
}

// openAppend opens the file an appending format adds to, creating it and its
// directories when missing, with the mode the umask gives a new file.
func openAppend(path string) (*os.File, error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, errors.New("is a directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, unwrapPathError(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666)
	if err != nil {
		return nil, unwrapPathError(err)
	}
	return f, nil
}

func fill(f *os.File, render func(io.Writer) error) error {
	w := bufio.NewWriter(f)
	err := render(w)
	if err == nil {
		err = w.Flush()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// writeDir writes the files of a format split over a directory. Every file
// is written under a temporary name before any is moved into place, so a
// failure while writing leaves the directory as it was; then the files of the
// format an earlier run left there that this one did not write are removed.
// Once the first file has replaced an earlier one, a failure removes every
// file of the format instead: half of this run's files beside the rest of an
// earlier run's would pass for one run to whatever uploads the directory.
func (t *Target) writeDir(run *report.Run) error {
	dirRenderer, ok := t.renderer.(DirRenderer)
	if !ok {
		return fmt.Errorf("report %s is one file, and %s names a directory", t.Spec.Format, t.Spec.Path)
	}
	files, err := dirRenderer.RenderFiles(run, t.Spec.Options)
	if err != nil {
		return err
	}
	temps := make([]string, 0, len(files))
	discard := func() {
		for _, tmp := range temps {
			_ = os.Remove(tmp)
		}
	}
	for _, file := range files {
		tmp, err := createBeside(filepath.Join(t.Spec.Path, file.Name))
		if err != nil {
			discard()
			return fmt.Errorf("%s: %w", file.Name, err)
		}
		temps = append(temps, tmp.Name())
		err = fill(tmp, func(w io.Writer) error {
			if _, err := w.Write(file.Data); err != nil {
				return fmt.Errorf("write report: %w", err)
			}
			return nil
		})
		if err != nil {
			discard()
			return fmt.Errorf("%s: %w", file.Name, unwrapPathError(err))
		}
	}
	written := map[string]bool{}
	for i, file := range files {
		if err := os.Rename(temps[i], filepath.Join(t.Spec.Path, file.Name)); err != nil {
			discard()
			_ = t.removeOwned(dirRenderer, nil)
			return fmt.Errorf("%s: %w", file.Name, unwrapPathError(err))
		}
		written[file.Name] = true
	}
	if err := t.removeOwned(dirRenderer, written); err != nil {
		_ = t.removeOwned(dirRenderer, nil)
		return err
	}
	return nil
}

// removeOwned removes the files of the format in the directory, except those
// in keep; it returns the first failure and goes on past it.
func (t *Target) removeOwned(r DirRenderer, keep map[string]bool) error {
	entries, err := os.ReadDir(t.Spec.Path)
	if err != nil {
		return unwrapPathError(err)
	}
	var first error
	for _, e := range entries {
		if !e.Type().IsRegular() || !r.Owns(e.Name()) || keep[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(t.Spec.Path, e.Name())); err != nil && first == nil {
			first = fmt.Errorf("remove %s, left by an earlier run: %w", e.Name(), unwrapPathError(err))
		}
	}
	return first
}

func makeDir(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return unwrapPathError(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return unwrapPathError(err)
	}
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	return nil
}

// createBeside creates the temporary file a report is written into, in the
// directory of its path so that the final rename is atomic. It gets the mode a
// report created in place would: the process's umask decides, and a report the
// rename replaces keeps its own permissions.
func createBeside(path string) (*os.File, error) {
	existing, statErr := os.Stat(path)
	if statErr == nil && existing.IsDir() {
		return nil, errors.New("is a directory")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, unwrapPathError(err)
	}
	for range 100 {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, fmt.Errorf("name a temporary file: %w", err)
		}
		name := filepath.Join(dir, "."+filepath.Base(path)+"."+hex.EncodeToString(suffix[:])+".tmp")
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, unwrapPathError(err)
		}
		if statErr == nil && existing.Mode().IsRegular() {
			if err := f.Chmod(existing.Mode().Perm()); err != nil {
				_ = f.Close()
				_ = os.Remove(name)
				return nil, unwrapPathError(err)
			}
		}
		return f, nil
	}
	return nil, errors.New("no free temporary name")
}

// unwrapPathError drops the path an *os.PathError repeats: the message it is
// part of already names the report's path.
func unwrapPathError(err error) error {
	if pe, ok := errors.AsType[*os.PathError](err); ok {
		return pe.Err
	}
	return err
}
