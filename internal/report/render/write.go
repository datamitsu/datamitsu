package render

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/report"
)

// Target is one report on its way to its path: a temporary file beside it,
// renamed over it once written, so that a reader never sees half a report; or
// stdout.
type Target struct {
	Spec     Spec
	renderer Renderer
	tmp      *os.File
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
	if !spec.Stdout() {
		t.tmp, t.Err = createBeside(spec.Path)
	}
	return t
}

// Write renders run into the target and moves it into place. On failure
// nothing is left at the path that was not there before.
func (t *Target) Write(run *report.Run) error {
	if t.Err != nil {
		return t.Err
	}
	if t.tmp == nil {
		w := bufio.NewWriter(t.stdout)
		if err := t.renderer.Render(w, run, t.Spec.Options); err != nil {
			return err
		}
		if err := w.Flush(); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		return nil
	}
	tmp := t.tmp.Name()
	w := bufio.NewWriter(t.tmp)
	err := t.renderer.Render(w, run, t.Spec.Options)
	if err == nil {
		err = w.Flush()
	}
	if closeErr := t.tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		// CreateTemp creates the file 0600; a report is for the pipeline to read.
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, t.Spec.Path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return unwrapPathError(err)
	}
	return nil
}

// createBeside creates the temporary file a report is written into, in the
// directory of its path so that the final rename is atomic.
func createBeside(path string) (*os.File, error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, errors.New("is a directory")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, unwrapPathError(err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return nil, unwrapPathError(err)
	}
	return f, nil
}

// unwrapPathError drops the path an *os.PathError repeats: the message it is
// part of already names the report's path.
func unwrapPathError(err error) error {
	if pe, ok := errors.AsType[*os.PathError](err); ok {
		return pe.Err
	}
	return err
}
