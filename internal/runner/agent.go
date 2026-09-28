package runner

import (
	"fmt"
	"path/filepath"
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

// agentOperation is what one operation printed in agent mode, before its
// summary line: the operation, and its numbers.
type agentOperation struct {
	op      config.OperationType
	groups  []toolExecutionGroup
	results []tooling.GroupExecutionResult
	stopped []stoppedTask
	skipped []tooling.SkippedTool
	cause   stopCause
	summary opSummary
	note    string
}

// printAgentOperation prints an operation as an agent reads it: every finding
// the terminal would show (visibleMask, the rule the frames follow) as
// "path:row:col: <severity> <source>(<code>): <message>", a failure that left
// no finding with the end of what the tool printed, the files, tasks and
// tools the run did not reach, and a summary line.
func (sc *sharedContext) printAgentOperation(a agentOperation) {
	var b strings.Builder
	var shown, hidden levelCounts
	for _, g := range a.groups {
		for _, exec := range g.executions {
			sc.agentResult(&b, exec.result, &shown, &hidden)
		}
	}
	for _, group := range a.results {
		for _, result := range group.Results {
			if files := unrunFiles(result); len(files) > 0 {
				fmt.Fprintf(&b, "%s: %d %s not run (%s): %s\n", agentLabel(result.ToolName, result.RelativeDir),
					len(files), plural(len(files), "file", "files"), a.cause, strings.Join(relativeFiles(files, sc.rootPath), ", "))
			}
		}
	}
	for _, t := range a.stopped {
		fmt.Fprintf(&b, "%s: %s (%s)\n", agentLabel(t.tool, t.dir), t.state(), t.cause)
	}
	for _, s := range a.skipped {
		fmt.Fprintf(&b, "%s: skipped (%s)\n", s.ToolName, s.ReasonText())
	}
	fmt.Fprintf(&b, "%s: %d tools · %d runs · %d failed · %d errors %d warnings", a.op, a.summary.tools, a.summary.runs,
		a.summary.failed, shown[0], shown[1])
	if extra := shown[2] + shown[3]; extra > 0 {
		fmt.Fprintf(&b, " %d info %d hints", shown[2], shown[3])
	}
	fmt.Fprintf(&b, " · %d hidden", hidden.total())
	if a.note != "" {
		b.WriteString(" · " + a.note)
	}
	b.WriteString("\n")
	text := b.String()
	report.MaskAll(&text, sc.secretValues())
	fmt.Print(text)
}

// agentResult writes one task's records.
func (sc *sharedContext) agentResult(b *strings.Builder, result tooling.ExecutionResult, shown, hidden *levelCounts) {
	label := agentLabel(result.ToolName, result.RelativeDir)
	ran := false
	for _, proc := range result.Processes {
		for i, visible := range visibleMask(proc) {
			d := proc.Diagnostics[i]
			if !visible {
				hidden.add(d.Severity)
				continue
			}
			shown.add(d.Severity)
			b.WriteString(sc.agentFinding(result.ToolName, d) + "\n")
		}
		if proc.State != tooling.ProcessRan {
			continue
		}
		ran = true
		if proc.Success || len(proc.Diagnostics) > 0 {
			continue
		}
		withhold := report.WithholdsOutput(sc.cfg.Tools[result.ToolName], sc.describedParser())
		if len(proc.Files) == 1 {
			b.WriteString(report.RelPath(sc.rootPath, proc.Files[0]) + ": ")
		}
		b.WriteString(failureLine(label, proc, withhold, sc.securityTool(result.ToolName)) + "\n")
		for _, line := range tailLines(report.OutputTail(proc, withhold, sc.secretValues()), agentTailLines) {
			b.WriteString(frameIndent + line + "\n")
		}
	}
	if !ran && !result.Success && !result.IsCancelled() {
		msg := "failed before it ran"
		if result.Error != nil {
			msg += ": " + oneLine(result.Error.Error())
		}
		b.WriteString(label + " " + msg + "\n")
	}
}

// frameIndent is the border a frame puts before every line of tool output;
// the tail keeps it, so no line of it reaches the left margin, where a CI
// could read it as a command or a problem matcher's match.
const frameIndent = "  │  "

// failureLine is a failure that left no finding, in the words of the report's
// synthetic finding.
func failureLine(label string, proc tooling.ProcessResult, withhold, security bool) string {
	code := 0
	if proc.ExitCode != nil {
		code = *proc.ExitCode
	}
	switch {
	case security && withhold:
		return fmt.Sprintf("%s failed (exit %d); output withheld for a security tool", label, code)
	case code == 0:
		return label + " failed without parsable findings"
	}
	return fmt.Sprintf("%s exited %d without parsable findings", label, code)
}

// agentFinding is one finding on one line: the path relative to the
// repository root, the row and column when known, the level, the rule and the
// message, whose line breaks become the two characters \n.
func (sc *sharedContext) agentFinding(tool string, d diagnostic.Diagnostic) string {
	var loc string
	if d.File != "" {
		loc = report.RelPath(sc.rootPath, d.File)
		if d.Row > 0 {
			loc += ":" + strconv.Itoa(d.Row)
			if d.Col > 0 {
				loc += ":" + strconv.Itoa(d.Col)
			}
		}
		loc += ": "
	}
	source := d.Source
	if source == "" {
		source = tool
	}
	if d.Code != "" {
		source += "(" + d.Code + ")"
	}
	message := string(tooling.StripCSI([]byte(d.Message)))
	message = strings.NewReplacer("\r\n", `\n`, "\r", `\n`, "\n", `\n`).Replace(message)
	return fmt.Sprintf("%s%s %s: %s", loc, d.Severity, source, message)
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

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func relativeFiles(files []string, root string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if rel, err := filepath.Rel(root, f); err == nil && !strings.HasPrefix(rel, "..") {
			f = filepath.ToSlash(rel)
		}
		out = append(out, f)
	}
	return out
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

// securityTool reports a tool whose parser module puts it in the security
// category.
func (sc *sharedContext) securityTool(name string) bool {
	p := sc.cfg.Tools[name].OutputParser
	if p == nil || sc.parserMgr == nil {
		return false
	}
	facts, ok := sc.parserMgr.DescribedParser(p.Module, p.Parser)
	return ok && facts.Tool.Category == "security"
}

// printAgentLine prints a line of agent output, masked.
func (sc *sharedContext) printAgentLine(line string) {
	report.MaskAll(&line, sc.secretValues())
	fmt.Println(line)
}
