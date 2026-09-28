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
	"github.com/datamitsu/datamitsu/internal/report/render/azure"
	"github.com/datamitsu/datamitsu/internal/report/render/github"
	"github.com/datamitsu/datamitsu/internal/report/render/markdown"
	"github.com/datamitsu/datamitsu/internal/report/render/teamcity"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// Values of Options.Annotations: auto prints the annotations of the CI the run
// is in — GitHub workflow commands in GitHub Actions, logging commands in
// Azure Pipelines, service messages in TeamCity — when its stdout is free;
// github, azure and teamcity print theirs wherever the run is; off never
// prints any.
const (
	AnnotationsAuto     = "auto"
	AnnotationsGitHub   = "github"
	AnnotationsAzure    = "azure"
	AnnotationsTeamCity = "teamcity"
	AnnotationsOff      = "off"
)

// AnnotationModes lists the values --annotations takes.
func AnnotationModes() []string {
	return []string{AnnotationsAuto, AnnotationsGitHub, AnnotationsAzure, AnnotationsTeamCity, AnnotationsOff}
}

// nativeMode is the annotation mode of a CI vendor, "" for one that reads
// none.
func nativeMode(vendor string) string {
	switch vendor {
	case cienv.VendorGitHub:
		return AnnotationsGitHub
	case cienv.VendorAzure:
		return AnnotationsAzure
	case cienv.VendorTeamCity:
		return AnnotationsTeamCity
	}
	return ""
}

// annotationState is what the run decided about annotations and what it has
// printed so far.
type annotationState struct {
	// mode is github, azure, teamcity or off, resolved from the mode asked for.
	mode string
	// export names the annotations in the report's exports:
	// <mode>-annotations.
	export string
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

// resolveAnnotations decides whether a run prints annotations, and which. auto
// prints those of the CI the run is in when its stdout carries no document and
// sits beside no JSON-L stream; an explicit mode prints its own wherever the
// run is — the command layer refused it beside a document on stdout. The
// report records them in a CI that reads annotations, or when a mode was asked
// for.
func resolveAnnotations(requested, vendor string, quiet, stdoutDocument bool) annotationState {
	native := nativeMode(vendor)
	st := annotationState{mode: AnnotationsOff, recorded: native != ""}
	named := native
	switch requested {
	case AnnotationsGitHub, AnnotationsAzure, AnnotationsTeamCity:
		st.mode, st.recorded, named = requested, true, requested
	case AnnotationsAuto:
		switch {
		case native == "":
			st.why = "not a CI job that reads annotations"
		case stdoutDocument:
			st.why = "stdout carries a document"
		case quiet:
			st.why = "the run writes a JSON-L event stream"
		default:
			st.mode = native
		}
	case AnnotationsOff:
		st.why = "--annotations off"
	default:
		st.recorded = false
	}
	if named != "" {
		st.export = named + "-annotations"
	}
	return st
}

// toolText is what a line of tool text becomes before the runner prints it on
// stdout: unchanged, or — where the CI reads commands anywhere in a line —
// with every command broken.
var toolText = func(line string) string { return line }

// commandPrefixes are the openings of the commands a run in the CI of vendor
// that prints the annotations of mode must keep out of what it prints: a CI
// that reads commands anywhere in a line reads them in whatever the run prints,
// annotations or not.
func commandPrefixes(mode, vendor string) []string {
	var prefixes []string
	if mode == AnnotationsAzure || vendor == cienv.VendorAzure {
		prefixes = append(prefixes, azure.CommandPrefix)
	}
	if mode == AnnotationsTeamCity || vendor == cienv.VendorTeamCity {
		prefixes = append(prefixes, teamcity.MessagePrefix)
	}
	return prefixes
}

// neutralizerOf is toolText for commandPrefixes: each opening gets a space
// before its bracket.
func neutralizerOf(mode, vendor string) func(string) string {
	prefixes := commandPrefixes(mode, vendor)
	return func(line string) string {
		for _, prefix := range prefixes {
			line = strings.ReplaceAll(line, prefix, strings.TrimSuffix(prefix, "[")+" [")
		}
		return line
	}
}

// CommandGuard is the render.Target guard of a document written to stdout in
// the CI of vendor that prints the annotations of mode; nil where no CI reads
// commands in it.
func CommandGuard(mode, vendor string) func(format string, data []byte) []byte {
	prefixes := commandPrefixes(mode, vendor)
	if len(prefixes) == 0 {
		return nil
	}
	return func(format string, data []byte) []byte { return render.GuardCommands(format, data, prefixes) }
}

// commandToken ends the stop-commands region: unguessable, so no tool output
// inside the region can end it, and one per process.
var commandToken = sync.OnceValue(func() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
})

// openCommandRegion stops the CI from reading commands before the results
// block prints anything a tool wrote: a line of tool output that starts with
// "::" would otherwise be a GitHub workflow command, one that holds
// "##teamcity[" a service message, and a finding's message is tool output
// too. GitHub stops for a token no tool can guess; TeamCity stops for the
// build step, and toolText keeps a tool from turning it back on. Azure has no
// such region: toolText alone breaks its commands. The annotations are
// printed after the region is closed.
func (sc *sharedContext) openCommandRegion() {
	sc.annotations.executed = true
	if ui.Quiet() || sc.annotations.regionOpen {
		return
	}
	switch sc.annotations.mode {
	case AnnotationsGitHub:
		fmt.Println("::stop-commands::" + commandToken())
	case AnnotationsTeamCity:
		fmt.Println(teamcity.DisableServiceMessages)
	default:
		return
	}
	sc.annotations.regionOpen = true
}

func (sc *sharedContext) closeCommandRegion() {
	if !sc.annotations.regionOpen {
		return
	}
	switch sc.annotations.mode {
	case AnnotationsGitHub:
		fmt.Println("::" + commandToken() + "::")
	case AnnotationsTeamCity:
		fmt.Println(teamcity.EnableServiceMessages)
	}
	sc.annotations.regionOpen = false
}

// annotationExport is the annotations as the report's exports record them:
// written when the run prints them, omitted with the reason otherwise.
func (sc *sharedContext) annotationExport() (report.Export, bool) {
	st := sc.annotations
	if !st.recorded {
		return report.Export{}, false
	}
	e := report.Export{Format: st.export, Path: render.Stdout, Status: report.ExportWritten}
	switch {
	case st.mode == AnnotationsOff:
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
	if sc.annotations.mode == AnnotationsOff || !sc.annotations.executed || run == nil {
		return
	}
	var out bytes.Buffer
	switch sc.annotations.mode {
	case AnnotationsGitHub:
		candidates := github.Candidates(run)
		var touched map[string]bool
		if github.Overflows(candidates) {
			touched = touchedOrWhy(TouchedFiles(ctx, sc.rootPath, sc.ci, sc.ciRuntime))
		}
		_ = github.Print(&out, github.Select(candidates, touched), rest)
	case AnnotationsAzure:
		candidates := azure.Candidates(run)
		var touched map[string]bool
		if azure.Overflows(candidates) {
			touched = touchedOrWhy(AzureTouchedFiles(ctx, sc.rootPath, sc.ci))
		}
		_ = azure.Print(&out, azure.Select(candidates, touched), rest)
	case AnnotationsTeamCity:
		_ = teamcity.Print(&out, teamcity.Build(teamcity.Candidates(run)))
	}
	fmt.Print(foreignCommandsBroken(sc.annotations.mode, sc.ci.Vendor, out.String()))
}

// foreignCommandsBroken breaks, in annotations of mode printed in the CI of
// vendor, the commands of every other CI that reads them anywhere in a line:
// GitHub's escaping keeps "##teamcity[" in a message, where TeamCity would
// read it. The mode's own commands stay.
func foreignCommandsBroken(mode, vendor, text string) string {
	own := map[string]string{AnnotationsAzure: azure.CommandPrefix, AnnotationsTeamCity: teamcity.MessagePrefix}[mode]
	for _, prefix := range commandPrefixes(mode, vendor) {
		if prefix != own {
			text = strings.ReplaceAll(text, prefix, strings.TrimSuffix(prefix, "[")+" [")
		}
	}
	return text
}

// touchedOrWhy says, when the touched files are unknown, why their priority
// is off.
func touchedOrWhy(touched map[string]bool, why string) map[string]bool {
	if why != "" {
		info("touched-file priority off: " + why)
	}
	return touched
}

// AzureTouchedFiles lists the files, relative to the repository root, that the
// pull request an Azure Pipelines build builds touched: those that differ
// between the merge base with the target branch's remote-tracking ref and
// HEAD. why says, when it cannot tell, what would let it.
func AzureTouchedFiles(ctx context.Context, root string, ci cienv.Info) (touched map[string]bool, why string) {
	branch := strings.TrimPrefix(ci.BaseRef, "refs/heads/")
	if branch == "" {
		return nil, "not a pull request build"
	}
	ref := "origin/" + branch
	out, err := git(ctx, root, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	base := strings.TrimSpace(out)
	if err != nil || !isObjectName(base) {
		return nil, ref + " not fetched"
	}
	out, err = git(ctx, root, "diff", "--name-only", "-z", base+"...HEAD", "--")
	if err != nil {
		return nil, "no merge base of HEAD and " + ref
	}
	touched = map[string]bool{}
	for name := range strings.SplitSeq(out, "\x00") {
		if name != "" {
			touched[name] = true
		}
	}
	return touched, ""
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
	if budget <= 0 {
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
