// Package junit renders a report.Run as JUnit XML, which GitLab, Jenkins,
// Azure Pipelines, CircleCI, Buildkite and Bitbucket all read as test results.
//
// A test report fails a build on one failed case, so what fails here is what
// failed the run and nothing else (R6): one suite per operation and tool, one
// case per file the tool answered for, a <failure> only for a file with a
// finding that gated — at or above the operation's failOn under an active
// gate — and one extra case for an invocation that failed without such a
// finding, so that a failed whole-unit tool does not fail every clean file of
// its unit. Findings below the threshold are the case's <system-out>; a tool
// that failed without any finding is an <error> carrying its masked output
// tail, never a security tool's; cancelled and skipped tasks are <skipped>.
//
// JUnit XML has no normative schema. The shape written is the one consumers
// agree on; the XSD Jenkins' xUnit plugin ships for it
// (https://github.com/jenkinsci/xunit-plugin/blob/master/src/main/resources/org/jenkinsci/plugins/xunit/types/model/xsd/junit-10.xsd)
// is a reference, not a contract.
package junit

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// Renderer writes JUnit XML.
type Renderer struct{}

// Name is the format's name in --report.
func (Renderer) Name() string { return "junit" }

// Options is empty: the document has no variant to choose.
func (Renderer) Options() []string { return nil }

// OmitsIncompleteTools is false: every tool is a suite, whether or not it is
// complete, so a narrowed run refuses the report.
func (Renderer) OmitsIncompleteTools() bool { return false }

// Companion describes every operation of the run, which the document holds.
func (Renderer) Companion(run *report.Run, _ map[string]string) common.Companion {
	ops := make([]*report.Operation, len(run.Operations))
	for i := range run.Operations {
		ops[i] = &run.Operations[i]
	}
	return common.NewCompanion(run, "junit", ops, nil)
}

// Render writes a suite for every tool of every operation, and one for an
// operation that did not run.
func (Renderer) Render(w io.Writer, run *report.Run, _ map[string]string) error {
	suites := make([]suite, 0, len(run.Operations))
	for i := range run.Operations {
		suites = append(suites, operationSuites(run, &run.Operations[i])...)
	}
	var b strings.Builder
	var total counts
	for _, s := range suites {
		total.add(s.counts())
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<testsuites name="datamitsu" tests="%d" failures="%d" errors="%d" skipped="%d" time="%s">`+"\n",
		total.tests, total.failures, total.errors, total.skipped, seconds(total.time))
	timestamp := run.StartedAt.UTC().Format("2006-01-02T15:04:05")
	for _, s := range suites {
		s.write(&b, timestamp)
	}
	b.WriteString("</testsuites>\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write junit: %w", err)
	}
	return nil
}

// suite is one <testsuite>: an operation's tool, or an operation that did not
// run.
type suite struct {
	name       string
	properties [][2]string
	cases      []*testCase
	// time is what the tool's invocations took, however it is split among
	// the cases.
	time report.Millis
}

type counts struct {
	tests, failures, errors, skipped int
	time                             report.Millis
}

func (c *counts) add(o counts) {
	c.tests += o.tests
	c.failures += o.failures
	c.errors += o.errors
	c.skipped += o.skipped
	c.time += o.time
}

func (s suite) counts() counts {
	c := counts{tests: len(s.cases), time: s.time}
	for _, tc := range s.cases {
		switch tc.outcome() {
		case outcomeFailure:
			c.failures++
		case outcomeError:
			c.errors++
		case outcomeSkipped:
			c.skipped++
		case outcomePass:
		}
	}
	return c
}

func (s suite) write(b *strings.Builder, timestamp string) {
	c := s.counts()
	fmt.Fprintf(b, `  <testsuite name="%s" tests="%d" failures="%d" errors="%d" skipped="%d" time="%s" timestamp="%s">`+"\n",
		common.XMLAttr(s.name), c.tests, c.failures, c.errors, c.skipped, seconds(c.time), timestamp)
	if len(s.properties) > 0 {
		b.WriteString("    <properties>\n")
		for _, p := range s.properties {
			fmt.Fprintf(b, `      <property name="%s" value="%s"/>`+"\n", common.XMLAttr(p[0]), common.XMLAttr(p[1]))
		}
		b.WriteString("    </properties>\n")
	}
	for _, tc := range s.cases {
		tc.write(b)
	}
	b.WriteString("  </testsuite>\n")
}

// operationSuites are the suites of one operation: one per tool it ran or the
// planner skipped, by tool name, or one for an operation that did not run.
func operationSuites(run *report.Run, op *report.Operation) []suite {
	runProps := [][2]string{{"run.complete", strconv.FormatBool(run.Complete)}, {"run.incomplete", reasons(run.Incomplete)}}
	if !op.Ran {
		return []suite{{
			name:       op.Name,
			properties: runProps,
			cases:      []*testCase{{className: op.Name, name: op.Name, skipped: "did not run"}},
		}}
	}
	byName := map[string]*suite{}
	var names []string
	at := func(tool string) *suite {
		if s, ok := byName[tool]; ok {
			return s
		}
		byName[tool] = &suite{name: op.Name + "/" + tool}
		names = append(names, tool)
		return byName[tool]
	}
	for i := range op.Tools {
		tr := &op.Tools[i]
		s := at(tr.Name)
		s.properties = append([][2]string{
			{"complete", strconv.FormatBool(tr.Complete)},
			{"incomplete", reasons(tr.Incomplete)},
			{"cached", strconv.Itoa(cachedFiles(tr))},
		}, runProps...)
		s.cases = toolCases(tr, op.Cancelled)
		for _, inv := range tr.Invocations {
			s.time += inv.Duration
		}
	}
	for _, skip := range op.Skipped {
		s := at(skip.Tool)
		if s.properties == nil {
			s.properties = runProps
		}
		s.cases = append(s.cases, &testCase{className: skip.Tool, name: skip.Tool, skipped: skipMessage(skip)})
	}
	sort.Strings(names)
	out := make([]suite, 0, len(names))
	for _, name := range names {
		out = append(out, *byName[name])
	}
	return out
}

func skipMessage(s report.Skip) string {
	var msg string
	switch s.Reason {
	case "config":
		msg = "skip: true"
	case "unsupported-platform":
		msg = "platform-skip"
	case "not-narrowable":
		msg = "narrowed"
	default:
		msg = s.Reason
	}
	if s.Detail != "" {
		msg += ": " + s.Detail
	}
	return msg
}

func reasons(rs []report.Reason) string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return strings.Join(out, ",")
}

func cachedFiles(tr *report.ToolRun) int {
	n := 0
	for _, inv := range tr.Invocations {
		if inv.State == string(tooling.FileCached) || inv.State == string(tooling.FileVerdictHit) {
			n += len(inv.Files)
		}
	}
	return n
}

// answered reports a state in which an invocation, or a file, has its result.
func answered(state string) bool {
	switch state {
	case string(tooling.ProcessRan), string(tooling.FileCached), string(tooling.FileVerdictHit):
		return true
	}
	return false
}

// invocationCase is the case of one invocation: one that failed on its own
// without a gating finding, did not get set up, or reported a finding without
// a file.
type invocationCase struct {
	dir, id string
	tc      *testCase
}

// settledFailure is an invocation that exited non-zero on findings the
// report lists with another invocation of the tool.
type settledFailure struct {
	inv   report.Invocation
	code  int
	count int
}

// toolCases builds the cases of one tool run: a case per file its
// invocations answered for, a case per invocation that failed on its own
// without a gating finding or reported a finding without a file, and a
// skipped case per task the run stopped.
func toolCases(tr *report.ToolRun, cancelled []report.Cancel) []*testCase {
	files := map[string]*testCase{}
	fileCase := func(path string) *testCase {
		if files[path] == nil {
			files[path] = &testCase{className: tr.Name, name: path}
		}
		return files[path]
	}
	var invocations []invocationCase
	var settled []settledFailure
	withhold := common.Security(tr)

	for _, inv := range tr.Invocations {
		var own *testCase
		ownCase := func() *testCase {
			if own == nil {
				own = &testCase{className: tr.Name}
				invocations = append(invocations, invocationCase{dir: dirName(inv.Dir), id: inv.ID, tc: own})
			}
			return own
		}
		if inv.State == string(tooling.ProcessSetupFailed) {
			tc := ownCase()
			tc.time += inv.Duration
			tc.failure = &caseFailure{kind: "setup", message: "setup failed", tail: tail(inv, withhold)}
			continue
		}
		// A task cancelled or never started is its skipped case.
		if !answered(inv.State) {
			continue
		}
		count := 0
		for _, fr := range inv.Files {
			if answered(fr.State) {
				count++
			}
		}
		for _, fr := range inv.Files {
			if !answered(fr.State) {
				continue
			}
			tc := fileCase(fr.Path)
			if count == 1 && inv.State == string(tooling.ProcessRan) {
				tc.time += inv.Duration
			}
		}

		gating := false
		var synthetic *report.Finding
		for _, f := range inv.Findings {
			switch {
			case common.Synthetic(f):
				synthetic = &f
			case f.Location.Path == "":
				ownCase().add(f, tr.FailOn)
				gating = gating || f.Gates
			default:
				fileCase(f.Location.Path).add(f, tr.FailOn)
				gating = gating || f.Gates
			}
		}
		// A threshold failure is the gating finding's, wherever the report
		// lists it after duplicates across invocations collapsed; only a
		// process that failed on its own, without one, needs a case of its
		// own — not a failure of every file it answered for.
		if inv.State != string(tooling.ProcessRan) || inv.FailureKind != report.FailureExit || gating {
			continue
		}
		code := 0
		if inv.ExitCode != nil {
			code = *inv.ExitCode
		}
		if synthetic == nil && len(inv.Findings) == 0 && code != 0 {
			// It exited on findings another invocation lists: decided once
			// every file's case is known.
			settled = append(settled, settledFailure{inv: inv, code: code, count: count})
			continue
		}
		tc := ownCase()
		if count != 1 {
			tc.time += inv.Duration
		}
		fail := &caseFailure{kind: "exit", message: fmt.Sprintf("exit %d", code)}
		for _, f := range inv.Findings {
			if !common.Synthetic(f) {
				fail.findings = append(fail.findings, line(f))
			}
		}
		switch {
		case synthetic != nil:
			fail.text, fail.tail = synthetic.Message, tail(inv, withhold)
		case len(fail.findings) > 0:
			fail.onFindings = true
		default:
			// Rejected after it exited 0, without a finding.
			fail.tail = tail(inv, withhold)
		}
		tc.failure = fail
	}
	// A process whose findings another invocation lists failed on them: when
	// one gates, a file it answered for fails already; otherwise it is a
	// failure of its own, with its findings listed there.
	for _, s := range settled {
		gated := false
		for _, fr := range s.inv.Files {
			if tc := files[fr.Path]; answered(fr.State) && tc != nil && len(tc.gating) > 0 {
				gated = true
			}
		}
		if gated {
			continue
		}
		tc := &testCase{className: tr.Name, failure: &caseFailure{kind: "exit", message: fmt.Sprintf("exit %d", s.code), onFindings: true}}
		if s.count != 1 {
			tc.time = s.inv.Duration
		}
		invocations = append(invocations, invocationCase{dir: dirName(s.inv.Dir), id: s.inv.ID, tc: tc})
	}

	// An invocation's case is named after its directory, told apart by the
	// invocation when several of the tool's share one.
	perDir := map[string]int{}
	for _, c := range invocations {
		perDir[c.dir]++
	}
	own := make([]*testCase, 0, len(invocations))
	for _, c := range invocations {
		c.tc.name = c.dir
		if perDir[c.dir] > 1 {
			c.tc.name = c.dir + " (" + c.id + ")"
		}
		own = append(own, c.tc)
	}
	sort.SliceStable(own, func(i, j int) bool { return own[i].name < own[j].name })

	out := make([]*testCase, 0, len(files)+len(own))
	out = append(out, sortedCases(files)...)
	out = append(out, own...)
	var stopped []*testCase
	listed := map[string]bool{}
	for _, c := range cancelled {
		if c.Tool != tr.Name {
			continue
		}
		state := "not started"
		if c.Started {
			state = "cancelled"
		}
		listed[c.TaskID] = true
		stopped = append(stopped, &testCase{className: tr.Name, name: c.TaskID, skipped: state + ": " + c.Cause})
	}
	// A task the run never reached for a reason of its own — the tools could
	// not be installed — has no stop record; it did not run all the same.
	for _, inv := range tr.Invocations {
		state := "not started"
		switch {
		case listed[inv.TaskID]:
			continue
		case inv.State == string(tooling.ProcessCancelled):
			state = "cancelled"
		case inv.State != string(tooling.ProcessNotStarted):
			continue
		}
		listed[inv.TaskID] = true
		stopped = append(stopped, &testCase{className: tr.Name, name: inv.TaskID, skipped: state})
	}
	sort.SliceStable(stopped, func(i, j int) bool { return stopped[i].name < stopped[j].name })
	return append(out, stopped...)
}

func dirName(dir string) string {
	if dir = strings.ReplaceAll(dir, `\`, "/"); dir == "" {
		return "."
	}
	return dir
}

func tail(inv report.Invocation, withhold bool) string {
	if withhold {
		return ""
	}
	return inv.OutputTail
}

func sortedCases(cases map[string]*testCase) []*testCase {
	names := make([]string, 0, len(cases))
	for name := range cases {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*testCase, len(names))
	for i, name := range names {
		out[i] = cases[name]
	}
	return out
}

type testCase struct {
	className, name string
	time            report.Millis
	// gating are the findings at or above the threshold that failed their
	// invocation; below are the rest, the case's output.
	gating, below []string
	failOn        string
	// failure is set on the case of an invocation that failed on its own.
	failure *caseFailure
	skipped string
}

// caseFailure is how an invocation failed on its own: exit, a <failure> when
// it printed findings and an <error> carrying its output tail when it printed
// none, or setup, an <error>.
type caseFailure struct {
	kind, message string
	onFindings    bool
	findings      []string
	text, tail    string
}

func (tc *testCase) add(f report.Finding, failOn string) {
	if f.Gates {
		tc.gating = append(tc.gating, line(f))
		tc.failOn = failOn
		return
	}
	tc.below = append(tc.below, line(f))
}

type outcome int

const (
	outcomePass outcome = iota
	outcomeFailure
	outcomeError
	outcomeSkipped
)

func (tc *testCase) outcome() outcome {
	switch {
	case tc.skipped != "":
		return outcomeSkipped
	case len(tc.gating) > 0 || (tc.failure != nil && tc.failure.onFindings):
		return outcomeFailure
	case tc.failure != nil:
		return outcomeError
	}
	return outcomePass
}

func (tc *testCase) write(b *strings.Builder) {
	fmt.Fprintf(b, `    <testcase classname="%s" name="%s" time="%s"`, common.XMLAttr(tc.className), common.XMLAttr(tc.name), seconds(tc.time))
	var body strings.Builder
	switch tc.outcome() {
	case outcomeSkipped:
		fmt.Fprintf(&body, `      <skipped message="%s"/>`+"\n", common.XMLAttr(tc.skipped))
	case outcomeFailure:
		kind, message, lines := "threshold", findingsAtOrAbove(len(tc.gating), tc.failOn), tc.gating
		if len(tc.gating) == 0 {
			kind, message, lines = tc.failure.kind, tc.failure.message, tc.failure.findings
		}
		fmt.Fprintf(&body, `      <failure type="%s" message="%s">%s</failure>`+"\n",
			kind, common.XMLAttr(message), common.XMLText(strings.Join(lines, "\n")))
	case outcomeError:
		var text []string
		if tc.failure.text != "" {
			text = append(text, tc.failure.text)
		}
		if tc.failure.tail != "" {
			text = append(text, strings.TrimRight(tc.failure.tail, "\n"))
		}
		fmt.Fprintf(&body, `      <error type="%s" message="%s">%s</error>`+"\n",
			tc.failure.kind, common.XMLAttr(tc.failure.message), common.XMLText(strings.Join(text, "\n")))
	case outcomePass:
	}
	if len(tc.below) > 0 {
		fmt.Fprintf(&body, "      <system-out>%s</system-out>\n", common.XMLText(strings.Join(tc.below, "\n")))
	}
	if body.Len() == 0 {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	b.WriteString(body.String())
	b.WriteString("    </testcase>\n")
}

func findingsAtOrAbove(n int, failOn string) string {
	noun := "findings"
	if n == 1 {
		noun = "finding"
	}
	return fmt.Sprintf("%d %s at or above failOn=%s", n, noun, failOn)
}

// line is one finding as a case lists it: "path:row:col: severity
// source(code): message", the column in code points and only where the report
// could convert it, the location left out for a finding without a file.
func line(f report.Finding) string {
	rule := common.SourceOf(f)
	if f.Code != "" {
		rule += "(" + f.Code + ")"
	}
	text := fmt.Sprintf("%s %s: %s", f.Severity, rule, f.Message)
	loc := f.Location
	if loc.Path == "" {
		return text
	}
	where := loc.Path
	if r := common.RegionOf(loc, common.Chars); r.Line > 0 {
		where += ":" + strconv.Itoa(r.Line)
		if r.Col > 0 {
			where += ":" + strconv.Itoa(r.Col)
		}
	}
	return where + ": " + text
}

func seconds(ms report.Millis) string {
	return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64)
}
