package runner

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
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

// startReport begins recording the run when something reads what it found: a
// report to write, or a JSON-L stream to carry diagnostic events.
func (sc *sharedContext) startReport() {
	if len(sc.opts.Reports) == 0 && !ui.Quiet() {
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
	sc.annotator = report.NewAnnotator(sc.rootPath, opts.Parsers)
}

// secretValues are the values a report and the event stream mask: those of
// the host's variables and of every app's and operation's env whose names say
// they hold a secret. Computed once per run.
func (sc *sharedContext) secretValues() []string {
	sc.secretsOnce.Do(func() { sc.secrets = sc.collectSecrets() })
	return sc.secrets
}

func (sc *sharedContext) collectSecrets() []string {
	var envs []map[string]string
	for _, app := range sc.cfg.Apps {
		envs = append(envs, app.Env, app.RuntimeEnv)
	}
	for _, tool := range sc.cfg.Tools {
		for _, op := range tool.Operations {
			envs = append(envs, op.Env)
		}
	}
	return report.SecretValues(env.EnvironAll(), envs...)
}

// gate is the hook the executor runs over every parsed process: it anchors the
// process's findings — fingerprints and columns, while the files are on disk —
// and then applies the failOn threshold, so that a fingerprint exists before
// the threshold decides.
func (sc *sharedContext) gate() tooling.Gate {
	threshold := tooling.ThresholdGate(config.Severity(sc.opts.FailOn), sc.severityContract, sc.ignoredFailOn.add)
	if sc.annotator == nil {
		return threshold
	}
	return func(task tooling.Task, proc *tooling.ProcessResult) tooling.GateDecision {
		sc.annotator.Annotate(task, proc)
		return threshold(task, proc)
	}
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

// refuseNarrowedReports refuses, before anything runs, a run narrowed at plan
// time — named files, a subdirectory, --file-scoped, --tools — that is asked
// for a report listing findings: such a report would read as the findings of
// the repository. A format that leaves out a tool whose completeness is not
// established is written anyway. --allow-partial writes every report, with
// the reasons it is incomplete.
func refuseNarrowedReports(opts Options, sel tooling.Selection, fileScoped bool, tools []string) error {
	if len(opts.Reports) == 0 || opts.AllowPartial {
		return nil
	}
	var why []string
	switch {
	case fileScoped:
		why = append(why, "--file-scoped")
	case sel.Mode == tooling.SelectionPaths:
		why = append(why, "files named")
	case sel.Mode == tooling.SelectionSubtree:
		why = append(why, "run in a subdirectory")
	}
	if len(tools) > 0 {
		why = append(why, "--tools")
	}
	if len(why) == 0 {
		return nil
	}
	var listing []string
	for _, spec := range opts.Reports {
		if r, ok := render.Lookup(spec.Format); ok && !r.OmitsIncompleteTools() {
			listing = append(listing, spec.Format)
		}
	}
	if len(listing) == 0 {
		return nil
	}
	for _, spec := range opts.Reports {
		status := report.ExportOmitted
		if slices.Contains(listing, spec.Format) {
			status = report.ExportRefused
		}
		emitReport(spec, status, "the run is narrowed: "+strings.Join(why, ", "))
	}
	return exitcode.UsageErrorf("the run is narrowed (%s), so report %s would not list every finding: "+
		"run it over the whole repository, or pass --allow-partial to write it with the reasons it is incomplete",
		strings.Join(why, ", "), strings.Join(listing, ", "))
}

// emitDiagnostics writes a diagnostic event for each finding of a tool that
// finished: those at or above the operation's failOn, or every one under
// --events diagnostics=all. Its op_id is the task's. The message is masked as
// a report's would be.
func (sc *sharedContext) emitDiagnostics(runOpID string, found []report.ToolFinding) {
	if len(found) == 0 || !ui.Quiet() {
		return
	}
	var mask *strings.Replacer
	for _, tf := range found {
		f := tf.Finding
		if !f.Reported && !sc.opts.AllDiagnostics {
			continue
		}
		if mask == nil {
			mask = maskReplacer(sc.secretValues())
		}
		ui.Emit(uievent.Event{
			Type:        uievent.TypeDiagnostic,
			OpID:        toolOpID(runOpID, tf.TaskID),
			Tool:        f.Tool,
			Dir:         tf.Dir,
			File:        f.Location.Path,
			Row:         f.Location.Row,
			Col:         f.Location.Col,
			EndRow:      f.Location.EndRow,
			EndCol:      f.Location.EndCol,
			Severity:    f.Severity,
			Code:        f.Code,
			Source:      f.Source,
			Msg:         mask.Replace(f.Message),
			Fingerprint: f.Fingerprint,
			Provenance:  f.Provenance,
			Reported:    new(f.Reported),
			Gates:       new(f.Gates),
		})
	}
}

func maskReplacer(secrets []string) *strings.Replacer {
	pairs := make([]string, 0, 2*len(secrets))
	for _, s := range secrets {
		pairs = append(pairs, s, report.Masked)
	}
	return strings.NewReplacer(pairs...)
}

// streamOutcome fails a run whose JSON-L stream could not be written: its
// consumer read less than the run did. It exits 1 — a broken stderr is not an
// unwritten report — unless the run was interrupted, and says so on stdout, the
// one stream left.
func streamOutcome(err error) error {
	streamErr := ui.EventStreamFailed()
	if streamErr == nil {
		return err
	}
	if interrupted, ok := errors.AsType[interruptedError](err); ok {
		return interrupted
	}
	msg := "the JSON-L event stream could not be written: " + streamErr.Error()
	if err != nil {
		msg = err.Error() + "\n" + msg
	}
	return errors.New(msg)
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
	if sc.report == nil || len(sc.opts.Reports) == 0 {
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
	report.Mask(run, sc.secretValues())

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
