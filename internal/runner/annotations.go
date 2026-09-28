package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/datamitsu/datamitsu/internal/cienv"
	"github.com/datamitsu/datamitsu/internal/gitenv"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render"
	"github.com/datamitsu/datamitsu/internal/report/render/github"
	"github.com/datamitsu/datamitsu/internal/report/render/markdown"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// Values of Options.Annotations: auto prints GitHub annotations in a GitHub
// Actions job whose stdout is free, github prints them wherever the run is,
// off never does.
const (
	AnnotationsAuto   = "auto"
	AnnotationsGitHub = "github"
	AnnotationsOff    = "off"
)

// AnnotationModes lists the values --annotations takes.
func AnnotationModes() []string {
	return []string{AnnotationsAuto, AnnotationsGitHub, AnnotationsOff}
}

// annotationsExport is how a report's exports name the annotations.
const annotationsExport = "github-annotations"

// annotationState is what the run decided about annotations and what it has
// printed so far.
type annotationState struct {
	// mode is github or off, resolved from the mode asked for.
	mode string
	// why says why the mode is off, for the report's exports.
	why string
	// recorded marks a run whose exports list the annotations: one in a
	// GitHub Actions job, or one asked for them explicitly.
	recorded bool
	// regionOpen marks the stop-commands region as printed and not closed.
	regionOpen bool
	// executed marks a run in which some task produced a result; one in which
	// none did prints nothing.
	executed bool
}

// resolveAnnotations decides whether a run prints annotations. auto prints
// them in a GitHub Actions job whose stdout carries neither a document nor
// sits beside a JSON-L stream; github prints them wherever the run is — the
// command layer refused it beside a document on stdout.
func resolveAnnotations(requested, vendor string, quiet, stdoutDocument bool) annotationState {
	st := annotationState{mode: AnnotationsOff, recorded: vendor == cienv.VendorGitHub || requested == AnnotationsGitHub}
	switch requested {
	case AnnotationsGitHub:
		st.mode = AnnotationsGitHub
	case AnnotationsAuto:
		switch {
		case vendor != cienv.VendorGitHub:
			st.why = "not a GitHub Actions job"
		case stdoutDocument:
			st.why = "stdout carries a document"
		case quiet:
			st.why = "the run writes a JSON-L event stream"
		default:
			st.mode = AnnotationsGitHub
		}
	case AnnotationsOff:
		st.why = "--annotations off"
	default:
		st.recorded = false
	}
	return st
}

// commandToken ends the stop-commands region: unguessable, so no tool output
// inside the region can end it, and one per process.
var commandToken = sync.OnceValue(func() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
})

// openCommandRegion stops the runner from reading workflow commands before
// the results block prints anything a tool wrote: a line of tool output that
// starts with "::" would otherwise be a command, and a finding's message is
// tool output too. The annotations are printed after it is closed.
func (sc *sharedContext) openCommandRegion() {
	sc.annotations.executed = true
	if sc.annotations.mode != AnnotationsGitHub || ui.Quiet() || sc.annotations.regionOpen {
		return
	}
	fmt.Println("::stop-commands::" + commandToken())
	sc.annotations.regionOpen = true
}

func (sc *sharedContext) closeCommandRegion() {
	if !sc.annotations.regionOpen {
		return
	}
	fmt.Println("::" + commandToken() + "::")
	sc.annotations.regionOpen = false
}

// annotationExport is the annotations as the report's exports record them:
// written when the run prints them, omitted with the reason otherwise.
func (sc *sharedContext) annotationExport() (report.Export, bool) {
	st := sc.annotations
	if !st.recorded {
		return report.Export{}, false
	}
	e := report.Export{Format: annotationsExport, Path: render.Stdout, Status: report.ExportWritten}
	switch {
	case st.mode != AnnotationsGitHub:
		e.Status, e.Detail = report.ExportOmitted, st.why
	case !st.executed:
		e.Status, e.Detail = report.ExportOmitted, "no task executed"
	}
	return e, true
}

// printAnnotations closes the stop-commands region and prints the run's
// annotations, once, after its last operation. rest names where the findings
// they leave out can be read.
func (sc *sharedContext) printAnnotations(ctx context.Context, run *report.Run, rest []string) {
	sc.closeCommandRegion()
	if e, ok := sc.annotationExport(); ok {
		emitReport(render.Spec{Format: e.Format, Path: e.Path}, e.Status, e.Detail, sc.secretValues())
	}
	if sc.annotations.mode != AnnotationsGitHub || !sc.annotations.executed || run == nil {
		return
	}
	candidates := github.Candidates(run)
	var touched map[string]bool
	if github.Overflows(candidates) {
		var why string
		touched, why = TouchedFiles(ctx, sc.rootPath, sc.ci, sc.ciRuntime)
		if why != "" {
			info("touched-file priority off: " + why)
		}
	}
	_ = github.Print(os.Stdout, github.Select(candidates, touched), rest)
}

// summaryLimit is how much Markdown GitHub takes from one step's summary,
// whoever wrote it.
const summaryLimit = 1 << 20

// wantsStepSummary reports a run whose Markdown goes to the step summary: one
// in a GitHub Actions job whose annotations are not turned off. The summary is
// a file, so a document on stdout does not stand in its way.
func (sc *sharedContext) wantsStepSummary() bool {
	requested := sc.opts.Annotations
	return sc.ci.Vendor == cienv.VendorGitHub && requested != "" && requested != AnnotationsOff
}

// writeStepSummary appends the run's Markdown to the step summary, within what
// the page has left — other commands of the step may have written to it —
// and reports whether it did. It is best-effort: a summary that cannot be
// written is one warning, never a failure.
func (sc *sharedContext) writeStepSummary(run *report.Run) bool {
	if run == nil || !sc.wantsStepSummary() || !sc.annotations.executed {
		return false
	}
	path := sc.ciRuntime.StepSummaryPath
	if path == "" {
		logger.Logger.Warn("the step summary was not written: GITHUB_STEP_SUMMARY is not set")
		return false
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		logger.Logger.Warn("the step summary was not written: " + err.Error())
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		logger.Logger.Warn("the step summary was not written: " + err.Error())
		return false
	}
	budget := summaryLimit - int(info.Size())
	if budget < markdown.MinBudget {
		logger.Logger.Warn(fmt.Sprintf("the step summary was not written: it holds %d bytes of GitHub's %d", info.Size(), summaryLimit))
		return false
	}
	if _, err := markdown.Write(f, run, budget); err != nil {
		logger.Logger.Warn("the step summary was not written: " + err.Error())
		return false
	}
	return true
}

// annotationsRest names where the findings the annotations leave out can be
// read: the step summary when it was written, and every report written to a
// file that lists them.
func annotationsRest(run *report.Run, summary bool) []string {
	if run == nil {
		return nil
	}
	var rest []string
	if summary {
		rest = append(rest, "the step summary")
	}
	for _, e := range run.Exports {
		listing := len(render.Listing([]render.Spec{{Format: e.Format}})) > 0
		if listing && e.Status == report.ExportWritten && e.Path != render.Stdout {
			rest = append(rest, e.Path)
		}
	}
	return rest
}

// info says something worth knowing that changes nothing: a log event on a
// JSON-L stream, a line on stderr otherwise.
func info(msg string) {
	if ui.Quiet() {
		ui.Emit(uievent.Event{Type: uievent.TypeLog, OpID: uievent.NextOpID("log"), Level: uievent.LevelInfo, Msg: msg})
		return
	}
	fmt.Fprintln(os.Stderr, "info: "+msg)
}

// TouchedFiles lists the files, relative to the repository root, that the
// change a GitHub job builds touched, so their findings are annotated first;
// why says, when it cannot tell, what would let it. On a pull_request the
// checkout is the merge commit, whose first parent is the base branch's tip,
// so HEAD^1..HEAD is the pull request's change — when HEAD is that merge
// commit and its parent was fetched. On a push the event names the commit the
// branch was at before. It runs git at most twice and never fails the run.
func TouchedFiles(ctx context.Context, root string, ci cienv.Info, rt cienv.Runtime) (touched map[string]bool, why string) {
	switch rt.EventName {
	case "pull_request":
		out, err := git(ctx, root, "log", "-1", "--format=%H %P", "HEAD")
		fields := strings.Fields(out)
		switch {
		case err != nil || len(fields) == 0:
			return nil, "HEAD cannot be read"
		case fields[0] != ci.SHA:
			return nil, "HEAD is not the merge commit"
		case len(fields) < 2:
			return nil, "HEAD^1 not fetched (set fetch-depth: 2)"
		}
		return changedSince(ctx, root, fields[1], "HEAD^1 not fetched (set fetch-depth: 2)")
	case "push":
		before := pushBefore(rt.EventPath)
		if strings.Trim(before, "0") == "" {
			return nil, "the push names no commit it started from"
		}
		return changedSince(ctx, root, before, "before commit not fetched (set fetch-depth: 0)")
	case "":
		return nil, "not a GitHub push or pull_request"
	}
	return nil, "a " + rt.EventName + " event names no change"
}

func changedSince(ctx context.Context, root, base, missing string) (map[string]bool, string) {
	// base comes from an event file: an object name, never an option.
	if !isObjectName(base) {
		return nil, "the event names no commit"
	}
	out, err := git(ctx, root, "diff", "--name-only", "-z", base, "HEAD", "--")
	if err != nil {
		return nil, missing
	}
	touched := map[string]bool{}
	for name := range strings.SplitSeq(out, "\x00") {
		if name != "" {
			touched[name] = true
		}
	}
	return touched, ""
}

// isObjectName reports a full SHA-1 or SHA-256 object name.
func isObjectName(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// pushBefore reads the commit a push started from out of its event file; ""
// when it cannot.
func pushBefore(eventPath string) string {
	if eventPath == "" {
		return ""
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return ""
	}
	var event struct {
		Before string `json:"before"`
	}
	if json.Unmarshal(data, &event) != nil {
		return ""
	}
	return event.Before
}

func git(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed subcommands; the revisions are checked hex object names
	cmd.Dir = root
	cmd.Env = gitenv.Environ()
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}
