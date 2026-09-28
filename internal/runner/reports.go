package runner

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// now is the time a report is stamped with: SOURCE_DATE_EPOCH when it is set,
// so that one input gives one document, and the clock otherwise.
func now() time.Time {
	if t, ok := env.SourceDateEpoch(); ok {
		return t
	}
	return time.Now().UTC()
}

// startReport begins recording the run when it has a report to write.
func (sc *sharedContext) startReport() {
	if len(sc.opts.Reports) == 0 {
		return
	}
	opts := report.Options{
		Root:   sc.rootPath,
		Tools:  sc.cfg.Tools,
		Apps:   sc.cfg.Apps,
		FailOn: config.Severity(sc.opts.FailOn),
	}
	if sc.parserMgr != nil {
		opts.Parsers = sc.parserMgr.DescribedParser
	}
	sc.report = report.NewAccumulator(opts)
}

// beginReportOperation records that operation started with plan; nil when the
// run writes no report.
func (sc *sharedContext) beginReportOperation(operation config.OperationType, plan *tooling.ExecutionPlan) *report.OperationRecord {
	if sc.report == nil {
		return nil
	}
	return sc.report.BeginOperation(string(operation), plan, sc.executor.TaskDir)
}

// reportSelection is the run's selection as the report states it.
func (sc *sharedContext) reportSelection() report.Selection {
	sel := report.Selection{
		Mode:       sc.selection.String(),
		FileScoped: sc.fileScoped,
		Tools:      append([]string(nil), sc.selectedTools...),
	}
	sort.Strings(sel.Tools)
	switch sc.selection.Mode {
	case tooling.SelectionSubtree:
		sel.Dir = report.RelPath(sc.rootPath, sc.selection.Dir)
	case tooling.SelectionPaths:
		for _, p := range sc.selection.Paths {
			sel.Paths = append(sel.Paths, report.RelPath(sc.rootPath, p))
		}
		sort.Strings(sel.Paths)
	case tooling.SelectionAll, tooling.SelectionEmpty:
	}
	return sel
}

// exportTarget is one report on its way to disk: the temporary file it is
// written into beside its path, or stdout.
type exportTarget struct {
	spec     render.Spec
	renderer render.Renderer
	tmp      *os.File
	err      error
}

// finishReports writes every report of the run after its last operation,
// whether or not its tools failed: the run that fails is the one a pipeline
// needs to read. err is what the run returns so far. A report that could not be
// written is always said — as a report event in JSON-L mode — and fails the
// run with exitcode.Export only when nothing else did: a tool failure (1) and
// an incomplete run (4) outrank it.
func (sc *sharedContext) finishReports(operations []config.OperationType, err error) error {
	if sc.report == nil {
		return err
	}
	for _, op := range operations {
		sc.report.NotRun(string(op))
	}
	targets := openExports(sc.opts.Reports)
	exports := make([]report.Export, len(targets))
	for i, t := range targets {
		exports[i] = report.Export{Format: t.spec.Format, Path: t.spec.Path, Status: report.ExportWritten}
		if t.err != nil {
			exports[i].Status, exports[i].Detail = report.ExportFailed, t.err.Error()
		}
	}
	run := sc.report.Build(report.BuildInfo{
		Version:       ldflags.Version,
		Configuration: sc.cfg.DisplayName(),
		StartedAt:     sc.startedAt,
		EndedAt:       now(),
		Selection:     sc.reportSelection(),
		FailFast:      sc.failFast,
		Exports:       exports,
	})

	var failures []error
	for i := range targets {
		t := &targets[i]
		if t.err == nil {
			t.err = t.write(run)
		}
		status, msg := report.ExportWritten, ""
		if t.err != nil {
			status, msg = report.ExportFailed, t.err.Error()
			failures = append(failures, fmt.Errorf("report %s: %s: %w", t.spec.Format, t.spec.Path, t.err))
		}
		emitReport(t.spec, status, msg)
	}
	if len(failures) == 0 {
		return err
	}
	last := len(failures)
	if err == nil {
		// The last failure becomes the run's error, which the exit site prints.
		last--
	}
	if !ui.Quiet() {
		for _, f := range failures[:last] {
			fmt.Fprintf(os.Stderr, "%s %s\n", clr.Red("error:"), f)
		}
	}
	if err != nil {
		return err
	}
	return exitcode.ExportError{Err: failures[last]}
}

// omitReports says, for a run that could not start, that none of its reports
// was written.
func omitReports(specs []render.Spec, cause error) {
	for _, s := range specs {
		emitReport(s, report.ExportOmitted, cause.Error())
	}
}

func emitReport(spec render.Spec, status, msg string) {
	ui.Emit(uievent.Event{
		Type:   uievent.TypeReport,
		OpID:   uievent.NextOpID("report"),
		Status: status,
		Format: spec.Format,
		Path:   spec.Path,
		Msg:    msg,
	})
}

// openExports prepares every target before anything is rendered, so the
// document can record which of its exports could not be written.
func openExports(specs []render.Spec) []exportTarget {
	targets := make([]exportTarget, 0, len(specs))
	for _, spec := range specs {
		t := exportTarget{spec: spec}
		t.renderer, _ = render.Lookup(spec.Format)
		if !spec.Stdout() {
			t.tmp, t.err = createBeside(spec.Path)
		}
		targets = append(targets, t)
	}
	return targets
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

func (t *exportTarget) write(run *report.Run) error {
	if t.tmp == nil {
		w := bufio.NewWriter(os.Stdout)
		if err := t.renderer.Render(w, run, t.spec.Options); err != nil {
			return err
		}
		if err := w.Flush(); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		return nil
	}
	tmp := t.tmp.Name()
	w := bufio.NewWriter(t.tmp)
	err := t.renderer.Render(w, run, t.spec.Options)
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
		err = os.Rename(tmp, t.spec.Path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return unwrapPathError(err)
	}
	return nil
}
