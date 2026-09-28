// Package sarif renders a report.Run as SARIF 2.1.0 for GitHub code scanning
// and the other services that keep alerts across uploads.
//
// Such a service closes an alert when the next upload of its tool no longer
// holds it, so the one thing a SARIF file must never do is claim more than the
// run covered. Every choice below follows from how GitHub matches and closes
// alerts, as measured on a throwaway repository:
//   - one run per tool, of the lint operation (fix when the run has no lint):
//     alerts are closed per (ref, category, tool.driver.name), and two runs of
//     one tool in one category are rejected;
//   - a tool whose completeness is not established is left out — GitHub leaves
//     the alerts of a tool absent from an upload untouched — and so is a tool
//     with more results than GitHub keeps from one run, which it would cut and
//     close the rest of;
//   - the category is written into automationDetails.id, "datamitsu/" or
//     "<category>/", because the upload action's category input fills in only
//     runs that have none;
//   - a finding's fingerprint is its partialFingerprints.primaryLocationLineHash,
//     the only key GitHub matches alerts on;
//   - at most twenty runs go in one file, GitHub's limit: a directory target
//     splits a run with more tools into files of twenty, tools sorted by name.
package sarif

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
)

// SARIF constants and the limits GitHub code scanning applies to an upload.
const (
	Version = "2.1.0"
	// SchemaURI is where the SARIF 2.1.0 schema is published.
	SchemaURI = "https://json.schemastore.org/sarif-2.1.0.json"
	// RunsPerFile is how many runs GitHub accepts in one file.
	RunsPerFile = 20
	// ResultsPerRun is how many results GitHub keeps from one run; it cuts a
	// run with more to its most severe ones and closes the alerts of the rest.
	ResultsPerRun = 25000
	// DefaultCategory is the category a file is written under unless
	// ?category= names another.
	DefaultCategory = "datamitsu"
	// SourceRoot is the base every relative artifact URI is resolved against:
	// the repository root.
	SourceRoot = "%SRCROOT%"
)

// Renderer writes SARIF.
type Renderer struct{}

// Name is the format's name in --report.
func (Renderer) Name() string { return "sarif" }

// Options lists category, the code-scanning category the file is written
// under.
func (Renderer) Options() []string { return []string{"category"} }

// OmitsIncompleteTools is true: a tool whose completeness is not established
// is left out, which keeps its alerts open, so the file is written for a
// narrowed run too.
func (Renderer) OmitsIncompleteTools() bool { return true }

// ToolsPerFile is how many tools one file holds.
func (Renderer) ToolsPerFile() int { return RunsPerFile }

// WrittenTools is how many runs the file holds: one per tool written.
func (Renderer) WrittenTools(run *report.Run, options map[string]string) int {
	return len(Runs(run, options))
}

// CheckOptions refuses an empty category.
func (Renderer) CheckOptions(options map[string]string) error {
	if raw, ok := options["category"]; ok && strings.TrimSuffix(strings.TrimSpace(raw), "/") == "" {
		return errors.New("category needs a name: ?category=<name>")
	}
	return nil
}

// Render writes the whole run as one file; a run with more tools than one
// file holds needs a directory target.
func (Renderer) Render(w io.Writer, run *report.Run, options map[string]string) error {
	runs := Runs(run, options)
	if len(runs) > RunsPerFile {
		return fmt.Errorf("%d tools do not fit one SARIF file, which GitHub reads at most %d runs of: write it to a directory", len(runs), RunsPerFile)
	}
	return encode(w, runs)
}

// RenderFiles splits the run into files of at most RunsPerFile runs, tools
// sorted by name, named datamitsu-1.sarif, datamitsu-2.sarif and so on; a run
// without a tool to write is one file with none.
func (Renderer) RenderFiles(run *report.Run, options map[string]string) ([]common.File, error) {
	runs := Runs(run, options)
	var files []common.File
	for start := 0; start == 0 || start < len(runs); start += RunsPerFile {
		var b bytes.Buffer
		if err := encode(&b, runs[start:min(start+RunsPerFile, len(runs))]); err != nil {
			return nil, err
		}
		files = append(files, common.File{Name: fileName(len(files) + 1), Data: b.Bytes()})
	}
	return files, nil
}

func fileName(n int) string { return "datamitsu-" + strconv.Itoa(n) + ".sarif" }

var ownName = regexp.MustCompile(`^datamitsu-[1-9][0-9]*\.sarif$`)

// Owns reports a file a directory target of this format writes.
func (Renderer) Owns(name string) bool { return ownName.MatchString(name) }

// Omitted lists the tools of the listed operation the file leaves out: those
// whose completeness is not established, with their reasons, and those with
// more results than one run holds.
func (Renderer) Omitted(run *report.Run, _ map[string]string) []report.OmittedTool {
	op := common.ListedOperation(run)
	if op == nil {
		return nil
	}
	var out []report.OmittedTool
	for i := range op.Tools {
		tr := &op.Tools[i]
		if reasons, omit := omission(tr); omit {
			out = append(out, report.OmittedTool{Operation: op.Name, Tool: tr.Name, Reasons: reasons})
		}
	}
	return out
}

// omission says whether a tool run is left out, and why.
func omission(tr *report.ToolRun) ([]string, bool) {
	if !tr.Complete {
		reasons := make([]string, len(tr.Incomplete))
		for i, r := range tr.Incomplete {
			reasons[i] = string(r)
		}
		return reasons, true
	}
	if countResults(tr) > ResultsPerRun {
		return []string{common.ReasonTooManyResults}, true
	}
	return nil, false
}

// Runs builds the SARIF runs of the listed operation: one per tool that is
// written, sorted by tool name.
func Runs(run *report.Run, options map[string]string) []Run {
	op := common.ListedOperation(run)
	if op == nil {
		return []Run{}
	}
	id := category(options) + "/"
	runs := []Run{}
	for i := range op.Tools {
		tr := &op.Tools[i]
		if _, omit := omission(tr); omit {
			continue
		}
		runs = append(runs, toolRun(tr, id))
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Tool.Driver.Name < runs[j].Tool.Driver.Name })
	return runs
}

func category(options map[string]string) string {
	if raw, ok := options["category"]; ok {
		if c := strings.TrimSuffix(strings.TrimSpace(raw), "/"); c != "" {
			return c
		}
	}
	return DefaultCategory
}

func countResults(tr *report.ToolRun) int {
	n := 0
	for _, inv := range tr.Invocations {
		for _, f := range inv.Findings {
			if !common.Synthetic(f) && f.Location.Path != "" {
				n++
			}
		}
	}
	return n
}

func toolRun(tr *report.ToolRun, automationID string) Run {
	r := Run{
		AutomationDetails: AutomationDetails{ID: automationID},
		ColumnKind:        "unicodeCodePoints",
		Tool: Tool{Driver: Driver{
			Name:           tr.Name,
			Version:        tr.App.Version,
			InformationURI: tr.App.OfficialURL,
			Rules:          []Rule{},
		}},
		Invocations: []Invocation{},
		Results:     []Result{},
	}
	var findings []report.Finding
	for _, inv := range tr.Invocations {
		r.Invocations = append(r.Invocations, invocation(inv))
		for _, f := range inv.Findings {
			if !common.Synthetic(f) && f.Location.Path != "" {
				findings = append(findings, f)
			}
		}
	}
	sort.SliceStable(findings, func(i, j int) bool { return common.Less(findings[i], findings[j]) })

	helpURI := map[string]string{}
	for _, f := range findings {
		id := ruleID(f)
		if _, seen := helpURI[id]; !seen || helpURI[id] == "" {
			helpURI[id] = f.RuleURL
		}
	}
	ids := make([]string, 0, len(helpURI))
	for id := range helpURI {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	index := make(map[string]int, len(ids))
	for i, id := range ids {
		index[id] = i
		r.Tool.Driver.Rules = append(r.Tool.Driver.Rules, Rule{ID: id, ShortDescription: Message{Text: id}, HelpURI: helpURI[id]})
	}
	for _, f := range findings {
		id := ruleID(f)
		r.Results = append(r.Results, Result{
			RuleID:    id,
			RuleIndex: index[id],
			Level:     Level(f.Severity),
			Message:   Message{Text: f.Message},
			Locations: []Location{location(f.Location)},
			PartialFingerprints: map[string]string{
				"primaryLocationLineHash": f.Fingerprint,
				"datamitsu/v1":            f.Fingerprint,
			},
			Properties: ResultProperties{Provenance: f.Provenance, Gates: f.Gates},
		})
	}
	return r
}

// ruleID is the finding's rule, or "<tool>/unknown" for a finding without one.
func ruleID(f report.Finding) string {
	if f.Code != "" {
		return f.Code
	}
	return f.Tool + "/unknown"
}

// invocation is one process — or the files a cache answered — of a tool. A
// finding that names no file cannot be a result, which code scanning shows
// only at a location: it is a notification of the invocation that reported
// it, as is the synthetic finding of a tool that failed without a parsable
// one.
func invocation(inv report.Invocation) Invocation {
	out := Invocation{
		ExecutionSuccessful: inv.Success,
		ExitCode:            inv.ExitCode,
		WorkingDirectory:    workingDirectory(inv.Dir),
		Properties:          InvocationProperties{Datamitsu: InvocationFacts{ID: inv.ID, State: inv.State, Extraction: inv.Extraction}},
	}
	for _, f := range inv.Findings {
		switch {
		case common.Synthetic(f):
			out.ToolExecutionNotifications = append(out.ToolExecutionNotifications,
				Notification{Level: LevelError, Message: Message{Text: f.Message}})
		case f.Location.Path == "":
			out.ToolExecutionNotifications = append(out.ToolExecutionNotifications,
				Notification{Level: Level(f.Severity), Message: Message{Text: f.Message}, Descriptor: &Descriptor{ID: ruleID(f)}})
		}
	}
	return out
}

func workingDirectory(dir string) *ArtifactLocation {
	dir = strings.ReplaceAll(dir, `\`, "/")
	switch {
	case dir == "" || dir == ".":
		return &ArtifactLocation{URI: "./", URIBaseID: SourceRoot}
	case common.Outside(dir):
		return &ArtifactLocation{URI: common.FileURI(strings.TrimSuffix(dir, "/")) + "/"}
	}
	return &ArtifactLocation{URI: common.RelativeURI(strings.TrimSuffix(dir, "/")) + "/", URIBaseID: SourceRoot}
}

func location(loc report.Location) Location {
	artifact := ArtifactLocation{URI: common.RelativeURI(loc.Path), URIBaseID: SourceRoot}
	if common.Outside(loc.Path) {
		artifact = ArtifactLocation{URI: common.FileURI(loc.Path)}
	}
	out := Location{PhysicalLocation: PhysicalLocation{ArtifactLocation: artifact}}
	if r := common.RegionOf(loc, common.Chars); r.Line > 0 {
		out.PhysicalLocation.Region = &Region{StartLine: r.Line, StartColumn: r.Col, EndLine: r.EndLine, EndColumn: r.EndCol}
	}
	return out
}

// SARIF levels.
const (
	LevelError   = "error"
	LevelWarning = "warning"
	LevelNote    = "note"
)

// Level is the SARIF level of a core severity: info and hint are both notes.
func Level(severity string) string {
	switch config.Severity(severity) {
	case config.SeverityError:
		return LevelError
	case config.SeverityWarning:
		return LevelWarning
	case config.SeverityInfo, config.SeverityHint:
		return LevelNote
	}
	return LevelNote
}

func encode(w io.Writer, runs []Run) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(Log{Version: Version, Schema: SchemaURI, Runs: runs}); err != nil {
		return fmt.Errorf("write sarif: %w", err)
	}
	return nil
}
