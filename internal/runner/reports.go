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
	reads := len(sc.opts.Reports) > 0 || ui.Quiet() || sc.annotations.mode == AnnotationsGitHub ||
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
	ui.SetEventMask(func(e *uievent.Event) { report.MaskAll(e, sc.secrets.Values()) })
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
// marks those the baseline holds, and then applies the failOn threshold, so
// that a baselined finding fails nothing and cancels nothing under fail-fast.
// A fingerprint is matched as the process computes it: its ordinal counts the
// findings of that process, which is what the report settles too unless two
// processes of one tool reported one line.
func (sc *sharedContext) gate() tooling.Gate {
	threshold := tooling.ThresholdGate(config.Severity(sc.opts.FailOn), sc.severityContract, sc.ignoredFailOn.add)
	if sc.annotator == nil {
		return threshold
	}
	baseline := sc.opts.Baseline
	return func(task tooling.Task, proc *tooling.ProcessResult) tooling.GateDecision {
		sc.annotator.Annotate(task, proc)
		for i := range proc.Diagnostics {
			d := &proc.Diagnostics[i]
			d.Baselined = d.Anchor != nil && baseline[d.Anchor.Fingerprint]
		}
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

// buildReport builds the record of the run once its last operation has ended,
// masked, with every report it was asked for opened on its path so that the
// record says which of them could not be; nil when the run records nothing.
func (sc *sharedContext) buildReport(operations []config.OperationType) (*report.Run, []*render.Target) {
	if sc.report == nil {
		return nil, nil
	}
	for _, op := range operations {
		sc.report.NotRun(string(op))
	}
	targets := make([]*render.Target, len(sc.opts.Reports))
	exports := make([]report.Export, 0, len(targets)+1)
	for i, spec := range sc.opts.Reports {
		targets[i] = render.Open(spec, os.Stdout)
		e := report.Export{Format: spec.Format, Path: spec.Path, Status: report.ExportWritten}
		if err := targets[i].Err; err != nil {
			e.Status, e.Detail = report.ExportFailed, err.Error()
		}
		exports = append(exports, e)
	}
	if e, ok := sc.annotationExport(); ok {
		exports = append(exports, e)
	}
	run := sc.report.Build(report.BuildInfo{
		Version:       ldflags.Version,
		Configuration: sc.cfg.DisplayName(),
		StartedAt:     sc.startedAt,
		EndedAt:       now(),
		Selection:     sc.reportSelection(),
		FailFast:      sc.failFast,
		Exports:       exports,
		CI: report.CIEnvironment{
			Vendor: sc.ci.Vendor, SHA: sc.ci.SHA, Ref: sc.ci.Ref, BaseRef: sc.ci.BaseRef, PRNumber: sc.ci.PRNumber,
		},
	})
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
func (sc *sharedContext) writeReports(run *report.Run, targets []*render.Target, err error) error {
	if run == nil {
		return err
	}
	for _, note := range render.Notes(run, sc.opts.Reports) {
		logger.Logger.Warn(note)
	}
	var failures []error
	for _, t := range targets {
		status, msg := report.ExportWritten, ""
		writeErr := t.Write(run)
		if declined, ok := errors.AsType[render.DeclinedError](writeErr); ok {
			status, msg = report.ExportOmitted, declined.Reason
		} else if writeErr != nil {
			status, msg = report.ExportFailed, writeErr.Error()
			failures = append(failures, fmt.Errorf("report %s: %s: %w", t.Spec.Format, t.Spec.Path, writeErr))
		}
		emitReport(t.Spec, status, msg, sc.secretValues())
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
	tools := map[string]bool{}
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			tools[task.ToolName] = true
		}
	}
	for _, crowded := range capped {
		err := render.CheckCapacity(crowded, len(tools))
		if err == nil {
			continue
		}
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
