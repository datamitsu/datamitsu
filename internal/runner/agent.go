package runner

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/ui"
)

// Values of Options.Output: human is the terminal's frames, colour and
// progress; agent is one line per finding and one summary line per
// operation, for a program that reads the run.
const (
	OutputHuman = "human"
	OutputAgent = "agent"
)

// OutputModes lists the values --output takes.
func OutputModes() []string {
	return []string{OutputHuman, OutputAgent}
}

// agentTailLines is how much of a failed tool's output an agent gets.
const agentTailLines = 20

// agentOutput reports a run that prints an agent's records instead of the
// human rendering; a JSON-L stream keeps stdout clean even then.
func (sc *sharedContext) agentOutput() bool {
	return sc.opts.Output == OutputAgent && !ui.Quiet()
}

// agentOperation is one operation as agent output shows it: its record and
// the numbers of its footer.
type agentOperation struct {
	op      config.OperationType
	record  *report.OperationRecord
	cause   stopCause
	summary opSummary
	note    string
}

// printAgentOperation prints an operation as an agent reads it, from the
// report's record of it, masked as a report is: every finding the terminal
// would show (report.Visible, the rule of the frames) as
// "path:row:col: <severity> <source>(<code>): <message>", a failure that left
// no finding in the words of its synthetic finding with the end of what the
// tool printed, the files, tasks and tools the run did not reach, and a
// summary line. Every record is one line.
func (sc *sharedContext) printAgentOperation(a agentOperation) {
	op := a.record.Operation()
	report.MaskAll(&op, sc.secretValues())
	var b strings.Builder
	var shown, hidden levelCounts
	baselined := 0
	stopped := map[string]bool{}
	for _, c := range op.Cancelled {
		stopped[c.TaskID] = true
	}
	for _, tr := range op.Tools {
		unrun := map[string][]string{}
		var order []string
		for _, inv := range tr.Invocations {
			agentInvocation(&b, tr, inv, &shown, &hidden, &baselined)
			notRun := inv.State == string(tooling.ProcessNotStarted) || inv.State == string(tooling.ProcessCancelled)
			if !notRun || stopped[inv.TaskID] {
				continue
			}
			label := agentLabel(tr.Name, inv.Dir)
			if _, seen := unrun[label]; !seen {
				order = append(order, label)
			}
			for _, f := range inv.Files {
				unrun[label] = append(unrun[label], f.Path)
			}
		}
		for _, label := range order {
			if files := unrun[label]; len(files) > 0 {
				record(&b, fmt.Sprintf("%s: %d %s not run (%s): %s", label, len(files), plural(len(files), "file", "files"),
					a.cause, strings.Join(files, ", ")))
			}
		}
	}
	for _, c := range op.Cancelled {
		state := "not started"
		if c.Started {
			state = "cancelled"
		}
		record(&b, fmt.Sprintf("%s: %s (%s)", agentLabel(c.Tool, c.Dir), state, c.Cause))
	}
	for _, s := range op.Skipped {
		record(&b, fmt.Sprintf("%s: skipped (%s)", s.Tool, skipText(s)))
	}
	if line := agentChanges(op); line != "" {
		record(&b, line)
	}
	summary := fmt.Sprintf("%s: %d tools · %d runs · %d failed · %d errors %d warnings", a.op, a.summary.tools, a.summary.runs,
		a.summary.failed, shown[0], shown[1])
	if shown[2]+shown[3] > 0 {
		summary += fmt.Sprintf(" %d info %d hints", shown[2], shown[3])
	}
	summary += fmt.Sprintf(" · %d hidden", hidden.total())
	if baselined > 0 {
		summary += fmt.Sprintf(" · %d baselined", baselined)
	}
	if a.note != "" {
		summary += " · " + a.note
	}
	record(&b, summary)
	fmt.Print(b.String())
}

// agentChanges is the record of the files a fix changed, which an agent has to
// read again, or of why they were not observed; "" for an operation that
// observes none — lint, or a fix that planned nothing.
func agentChanges(op report.Operation) string {
	if op.Name != string(config.OpFix) || !op.Ran || op.ChangesReason == report.ChangesNoFixTask {
		return ""
	}
	changes := op.AllChanges()
	paths := make([]string, len(changes))
	for i, c := range changes {
		paths[i] = c.Path
	}
	listed := ""
	if len(paths) > 0 {
		listed = ": " + strings.Join(paths, ", ")
	}
	if op.ChangesObserved {
		return fmt.Sprintf("fix changed %d %s%s", len(paths), plural(len(paths), "file", "files"), listed)
	}
	why := op.ChangesReason
	if op.ChangesDetail != "" {
		why += " (" + op.ChangesDetail + ")"
	}
	line := "fix changes not observed: " + why
	if len(paths) > 0 {
		line += fmt.Sprintf("; changed at least %d %s%s", len(paths), plural(len(paths), "file", "files"), listed)
	}
	return line
}

// skipText is why the planner left a tool out, in the words of the human
// block.
func skipText(s report.Skip) string {
	reasons := map[string]tooling.SkipReason{}
	for _, r := range []tooling.SkipReason{tooling.SkipReasonConfig, tooling.SkipReasonUnsupportedPlatform, tooling.SkipReasonNotNarrowable} {
		reasons[r.String()] = r
	}
	return tooling.SkippedTool{ToolName: s.Tool, Reason: reasons[s.Reason], Detail: s.Detail}.ReasonText()
}

func agentInvocation(b *strings.Builder, tr report.ToolRun, inv report.Invocation, shown, hidden *levelCounts, baselined *int) {
	visible, below := report.Visible(inv)
	for _, f := range visible {
		shown.add(severityOf(f.Severity))
		record(b, agentFinding(f))
	}
	for _, f := range below {
		if f.Baselined {
			*baselined++
		} else {
			hidden.add(severityOf(f.Severity))
		}
	}
	label := agentLabel(tr.Name, inv.Dir)
	var failure string
	synthetic, isSynthetic := report.Synthetic(inv)
	switch {
	case isSynthetic:
		// The synthetic finding names the tool first; the record names its
		// directory too.
		failure = label + strings.TrimPrefix(synthetic.Message, tr.Name)
	case inv.FailureKind == "setup":
		failure = label + " failed before it ran"
	case inv.FailureKind == "exit" && inv.ExitCode != nil && *inv.ExitCode == 0 && len(visible)+len(below) == 0:
		// An exit-0 failure is a formatter that wrote nothing: it never had findings,
		// where one of another exit code without any lost them to a duplicate.
		failure = label + " failed without parsable findings"
	default:
		return
	}
	if len(inv.Files) == 1 {
		failure = inv.Files[0].Path + ": " + failure
	}
	record(b, failure)
	for _, line := range tailLines(inv.OutputTail, agentTailLines) {
		b.WriteString(frameIndent + line + "\n")
	}
}

// record writes one record on one line: a line break in anything it names —
// a message, a path, a directory — is written as the two characters \n.
func record(b *strings.Builder, text string) {
	b.WriteString(lineBreaks.Replace(text) + "\n")
}

var lineBreaks = strings.NewReplacer("\r\n", `\n`, "\r", `\n`, "\n", `\n`)

// frameIndent is the border a frame puts before every line of tool output;
// the tail keeps it, so no line of it reaches the left margin, where a CI
// could read it as a command or a problem matcher's match.
const frameIndent = "  │  "

// agentFinding is one finding on one line: the path relative to the
// repository root, the row and column as the tool reported them when known,
// the level, the rule and the message, whose line breaks become the two
// characters \n.
func agentFinding(f report.Finding) string {
	var loc string
	if l := f.Location; l.Path != "" {
		loc = l.Path
		if l.Row > 0 {
			loc += ":" + strconv.Itoa(l.Row)
			if l.Col > 0 {
				loc += ":" + strconv.Itoa(l.Col)
			}
		}
		loc += ": "
	}
	source := f.Source
	if source == "" {
		source = f.Tool
	}
	if f.Code != "" {
		source += "(" + f.Code + ")"
	}
	return fmt.Sprintf("%s%s %s: %s", loc, f.Severity, source, f.Message)
}

func severityOf(level string) diagnostic.Severity {
	return diagnostic.Severity(config.Severity(level).Level())
}

func agentLabel(tool, dir string) string {
	if dir == "" {
		return tool
	}
	return tool + " [" + dir + "]"
}

// tailLines is the last n lines of text, blank ones dropped.
func tailLines(text string, n int) []string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines[max(0, len(lines)-n):]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// describedParser is what the run's parser modules said about a parser key,
// nil when no parser is configured.
func (sc *sharedContext) describedParser() report.ParserFacts {
	if sc.parserMgr == nil {
		return nil
	}
	return sc.parserMgr.DescribedParser
}

// printAgentLine prints a line of agent output, masked.
func (sc *sharedContext) printAgentLine(line string) {
	report.MaskAll(&line, sc.secretValues())
	fmt.Println(line)
}
