package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
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
// report to write, a JSON-L stream to carry diagnostic events, annotations, a
// step summary or an agent's records to print, or a baseline to match
// fingerprints against.
func (sc *sharedContext) startReport() {
	reads := len(sc.opts.Reports) > 0 || ui.Quiet() || sc.annotations.mode != AnnotationsOff ||
		sc.wantsStepSummary() || sc.agentOutput() || sc.opts.Baseline != nil
	if !reads {
		return
	}
	sc.secrets = sc.collectSecrets()
	sc.executor.SetEnvObserver(func(environ []string) { sc.secrets.Add(environ) })
	opts := report.Options{
		Root:    sc.rootPath,
		Tools:   sc.cfg.Tools,
		Apps:    sc.cfg.Apps,
		FailOn:  config.Severity(sc.opts.FailOn),
		Secrets: sc.secrets,
		Parsers: sc.describedParser(),
	}
	// The stream is masked as a report is, every event alike: a task's op_id
	// names its directory, and the events of one task must still correlate.
	// A CI that reads commands anywhere in a line reads them on stderr too.
	ui.SetEventMask(func(e *uievent.Event) {
		report.MaskAll(e, sc.secrets.Values())
		report.RewriteAll(e, toolText)
	})
	sc.report = report.NewAccumulator(opts)
	sc.annotator = report.NewAnnotator(sc.rootPath, opts.Parsers)
}

// secretValues are the values a report and the event stream mask: those of
// the host's variables, of every app's and operation's env, and of the
// environment every tool process got, whose names say they hold a secret.
func (sc *sharedContext) secretValues() []string {
	return sc.secrets.Values()
}

// collectSecrets starts the values to mask with what is known before any tool
// runs; the environment each process is built with adds its own, placeholders
// expanded.
func (sc *sharedContext) collectSecrets() *report.Secrets {
	var envs []map[string]string
	for _, app := range sc.cfg.Apps {
		envs = append(envs, app.Env, app.RuntimeEnv)
	}
	for _, tool := range sc.cfg.Tools {
		for _, op := range tool.Operations {
			envs = append(envs, op.Env)
		}
	}
	secrets := &report.Secrets{}
	secrets.Add(env.EnvironAll(), envs...)
	return secrets
}

// gate is the hook the executor runs over every parsed process: it anchors the
// process's findings — fingerprints and columns, while the files are on disk —
// marks those the baseline holds (report.BaselineMatcher), and then applies
// the failOn threshold, so that a baselined finding fails nothing and cancels
// nothing under fail-fast.
func (sc *sharedContext) gate() tooling.Gate {
	threshold := tooling.ThresholdGate(config.Severity(sc.opts.FailOn), sc.severityContract, sc.ignoredFailOn.add)
	if sc.annotator == nil {
		return threshold
	}
	if sc.opts.Baseline != nil && sc.baselineMatcher == nil {
		sc.baselineMatcher = report.NewBaselineMatcher(sc.opts.Baseline, sc.rootPath)
	}
	return func(task tooling.Task, proc *tooling.ProcessResult) tooling.GateDecision {
		sc.annotator.Annotate(task, proc)
		sc.baselineMatcher.Mark(task, proc)
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
	listing := render.Listing(opts.Reports)
	if len(listing) == 0 {
		return nil
	}
	for _, spec := range opts.Reports {
		status := report.ExportOmitted
		if slices.Contains(listing, spec.Format) {
			status = report.ExportRefused
		}
		emitReport(spec, status, "the run is narrowed: "+strings.Join(why, ", "), report.SecretValues(env.EnvironAll()))
	}
	return exitcode.UsageErrorf("the run is narrowed (%s), so report %s would not list every finding: "+
		"run it over the whole repository, or pass --allow-partial to write it with the reasons it is incomplete",
		strings.Join(why, ", "), strings.Join(listing, ", "))
}

// emitDiagnostics writes a diagnostic event for each finding of a tool that
// finished: those at or above the operation's failOn, or every one under
// --events diagnostics=all. Its op_id is the task's; the stream's masker masks
// it as a report would be.
func (sc *sharedContext) emitDiagnostics(runOpID string, found []report.ToolFinding) {
	if len(found) == 0 || !ui.Quiet() {
		return
	}
	for _, tf := range found {
		f := tf.Finding
		if !f.Reported && !sc.opts.AllDiagnostics {
			continue
		}
		e := uievent.Event{
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
			Msg:         f.Message,
			Fingerprint: f.Fingerprint,
			Provenance:  f.Provenance,
			Reported:    new(f.Reported),
			Gates:       new(f.Gates),
			Baselined:   f.Baselined,
		}
		ui.Emit(e)
	}
}

type reportTarget struct {
	*render.Target

	exportIndex int
}

// buildReport records the completed operations and prepares the exports. A
// capacity refusal affects only the target, not the operation it describes.
func (sc *sharedContext) buildReport(operations []config.OperationType) (*report.Run, []reportTarget) {
	if sc.report == nil {
		return nil, nil
	}
	for _, op := range operations {
		sc.report.NotRun(string(op))
	}
	targets := make([]reportTarget, 0, len(sc.opts.Reports))
	run := sc.report.Build(report.BuildInfo{
		Version:       ldflags.Version,
		Configuration: sc.cfg.DisplayName(),
		StartedAt:     sc.startedAt,
		EndedAt:       now(),
		Selection:     sc.reportSelection(),
		FailFast:      sc.failFast,
		CI: report.CIEnvironment{
			Vendor: sc.ci.Vendor, SHA: sc.ci.SHA, Ref: sc.ci.Ref, BaseRef: sc.ci.BaseRef, PRNumber: sc.ci.PRNumber,
		},
	})
	for _, spec := range sc.opts.Reports {
		e := report.Export{Format: spec.Format, Path: spec.Path, Status: report.ExportWritten}
		if err := render.CheckRunCapacity(spec, run); err != nil {
			e.Status, e.Detail = report.ExportRefused, err.Error()
			if cleanupErr := render.RemoveStaleFile(spec); cleanupErr != nil {
				e.Detail += "\n" + cleanupErr.Error()
			}
		} else {
			target := render.Open(spec, os.Stdout)
			target.Guard = CommandGuard(sc.annotations.mode, sc.ci.Vendor)
			if target.Err != nil {
				e.Status, e.Detail = report.ExportFailed, target.Err.Error()
			}
			targets = append(targets, reportTarget{target, len(run.Exports)})
		}
		run.Exports = append(run.Exports, e)
	}
	if e, ok := sc.annotationExport(); ok {
		run.Exports = append(run.Exports, e)
	}
	render.Describe(run, sc.opts.Reports)
	report.Mask(run, sc.secretValues())
	return run, targets
}

// writeReports writes every report of the run after its last operation,
// whether or not its tools failed: the run that fails is the one a pipeline
// needs to read. err is what the run returns so far. A report that could not be
// written is always said — as a report event in JSON-L mode — and fails the
// run with exitcode.Export only when nothing else did: a tool failure (1) and
// an incomplete run (4) outrank it.
func (sc *sharedContext) writeReports(run *report.Run, targets []reportTarget, err error) error {
	if run == nil {
		return err
	}
	specs := make([]render.Spec, 0, len(targets))
	for _, target := range targets {
		if target.Err == nil {
			specs = append(specs, target.Spec)
		}
	}
	for _, note := range render.Notes(run, specs) {
		logger.Logger.Warn(note)
	}
	var failures []error
	for i, spec := range sc.opts.Reports {
		export := run.Exports[i]
		if export.Status != report.ExportRefused {
			continue
		}
		emitReport(spec, export.Status, export.Detail, sc.secretValues())
		if err != nil {
			err = fmt.Errorf("%w\n%s", err, export.Detail)
		} else {
			err = exitcode.ExportError{Err: errors.New(export.Detail)}
		}
	}
	for _, target := range targets {
		if target.Err == nil && run.Exports[target.exportIndex].Status == report.ExportWritten {
			run.Exports[target.exportIndex].Status = ""
		}
	}
	write := func(t reportTarget) {
		export := &run.Exports[t.exportIndex]
		if export.Status == "" {
			export.Status = report.ExportWritten
		}
		status, msg := report.ExportWritten, ""
		writeErr := t.Write(run)
		if declined, ok := errors.AsType[render.DeclinedError](writeErr); ok {
			status, msg = report.ExportOmitted, declined.Reason
		} else if writeErr != nil {
			status, msg = report.ExportFailed, writeErr.Error()
			failure := fmt.Sprintf("report %s: %s: %s", t.Spec.Format, t.Spec.Path, writeErr)
			report.MaskAll(&failure, sc.secretValues())
			failures = append(failures, errors.New(failure))
		}
		detail := msg
		report.MaskAll(&detail, sc.secretValues())
		run.Exports[t.exportIndex].Status, run.Exports[t.exportIndex].Detail = status, detail
		emitReport(t.Spec, status, msg, sc.secretValues())
	}
	for _, target := range targets {
		if target.Spec.Format != "json" {
			write(target)
		}
	}
	for _, target := range targets {
		if target.Spec.Format == "json" {
			write(target)
		}
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
			fmt.Fprintf(os.Stderr, "%s %s\n", clr.Red("error:"), toolText(f.Error()))
		}
	}
	if err != nil {
		return err
	}
	return exitcode.ExportError{Err: failures[last]}
}

// refusedReports is a report refused once the plan is known but before any
// tool ran: the run returns it as it is and writes nothing.
type refusedReportsError struct{ exitcode.UsageError }

// refuseCrowdedReports refuses, before anything runs, a report asked for as
// one file — or stdout — whose format holds fewer tools in one file than the
// run plans for the operation it writes: SARIF, which GitHub reads at most
// twenty runs of per file. A directory target splits such a run instead.
func (sc *sharedContext) refuseCrowdedReports(ctx context.Context, operations []config.OperationType) error {
	var capped []render.Spec
	for _, s := range sc.opts.Reports {
		if _, ok := render.OneFileCap(s); ok {
			capped = append(capped, s)
		}
	}
	if len(capped) == 0 {
		return nil
	}
	names := make([]string, len(operations))
	for i, op := range operations {
		names[i] = string(op)
	}
	plan, err := sc.planner.Plan(ctx, config.OperationType(common.ListedOperationName(names)), sc.selection, sc.selectedTools)
	if err != nil || plan == nil {
		// The operation meets the same error when it plans, and says so.
		return nil //nolint:nilerr // not this check's error to report
	}
	crowded, err := crowdedReport(capped, plan)
	if err != nil {
		for _, spec := range sc.opts.Reports {
			status := report.ExportOmitted
			if spec.Format == crowded.Format {
				status = report.ExportRefused
			}
			emitReport(spec, status, err.Error(), sc.secretValues())
		}
		return refusedReportsError{exitcode.UsageError{Err: err}}
	}
	return nil
}

func crowdedReport(specs []render.Spec, plan *tooling.ExecutionPlan) (render.Spec, error) {
	tools := map[string]bool{}
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			tools[task.ToolName] = true
		}
	}
	count := len(tools)
	for _, spec := range specs {
		if err := render.CheckCapacity(spec, count); err != nil {
			return spec, err
		}
	}
	return render.Spec{}, nil
}

// omitReports says, for a run that could not start, that none of its reports
// was written.
func omitReports(specs []render.Spec, cause error) {
	for _, s := range specs {
		emitReport(s, report.ExportOmitted, cause.Error(), report.SecretValues(env.EnvironAll()))
	}
}

// emitReport writes a report event, masked as the report would be: its path
// and its message come from the user and the file system.
func emitReport(spec render.Spec, status, msg string, secrets []string) {
	e := uievent.Event{
		Type:   uievent.TypeReport,
		OpID:   uievent.NextOpID("report"),
		Status: status,
		Format: spec.Format,
		Path:   spec.Path,
		Msg:    msg,
	}
	report.MaskAll(&e, secrets)
	ui.Emit(e)
}
