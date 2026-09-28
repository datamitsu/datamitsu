package render

import (
	"bufio"
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
	stdout    io.Writer
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
// the files written before the one that failed.
func (t *Target) Write(run *report.Run) error {
	switch {
	case t.Err != nil:
		return t.Err
	case t.Spec.Dir() && !t.Spec.Stdout():
		return t.writeDir(run)
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

// fill writes into f through render and closes it.
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

// writeDir writes the files of a format split over a directory, each through
// a temporary file of its own, then removes the files of the format an
// earlier run left there that this one did not write: an upload of the
// directory would read them as part of this run.
func (t *Target) writeDir(run *report.Run) error {
	dirRenderer, ok := t.renderer.(DirRenderer)
	if !ok {
		return fmt.Errorf("report %s is one file, and %s names a directory", t.Spec.Format, t.Spec.Path)
	}
	files, err := dirRenderer.RenderFiles(run, t.Spec.Options)
	if err != nil {
		return err
	}
	written := map[string]bool{}
	for _, file := range files {
		path := filepath.Join(t.Spec.Path, file.Name)
		tmp, err := createBeside(path)
		if err != nil {
			return fmt.Errorf("%s: %w", file.Name, err)
		}
		err = fill(tmp, func(w io.Writer) error {
			if _, err := w.Write(file.Data); err != nil {
				return fmt.Errorf("write report: %w", err)
			}
			return nil
		})
		if err == nil {
			err = os.Rename(tmp.Name(), path)
		}
		if err != nil {
			_ = os.Remove(tmp.Name())
			return fmt.Errorf("%s: %w", file.Name, unwrapPathError(err))
		}
		written[file.Name] = true
	}
	entries, err := os.ReadDir(t.Spec.Path)
	if err != nil {
		return unwrapPathError(err)
	}
	for _, e := range entries {
		if e.Type().IsRegular() && dirRenderer.Owns(e.Name()) && !written[e.Name()] {
			if err := os.Remove(filepath.Join(t.Spec.Path, e.Name())); err != nil {
				return fmt.Errorf("remove %s, left by an earlier run: %w", e.Name(), unwrapPathError(err))
			}
		}
	}
	return nil
}

// makeDir creates the directory a split format writes into.
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
