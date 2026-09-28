package render

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/checkstyle"
	"github.com/datamitsu/datamitsu/internal/report/render/codequality"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
	"github.com/datamitsu/datamitsu/internal/report/render/junit"
	"github.com/datamitsu/datamitsu/internal/report/render/rdjsonl"
	"github.com/datamitsu/datamitsu/internal/report/render/sarif"
)

var (
	_ DirRenderer   = sarif.Renderer{}
	_ Capped        = sarif.Renderer{}
	_ Omitter       = sarif.Renderer{}
	_ OptionChecker = sarif.Renderer{}
	_ Companioned   = junit.Renderer{}
	_ Companioned   = codequality.Renderer{}
	_ Companioned   = checkstyle.Renderer{}
	_ Companioned   = rdjsonl.Renderer{}
)

// companionRenderer stands in for a format without a place for completeness.
type companionRenderer struct{ fakeRenderer }

func (companionRenderer) Render(w io.Writer, _ *report.Run, _ map[string]string) error {
	_, err := io.WriteString(w, "findings\n")
	return err
}

func (companionRenderer) OmitsIncompleteTools() bool { return false }

func (companionRenderer) Companion(run *report.Run, _ map[string]string) common.Companion {
	return common.NewCompanion(run, "companioned", []*report.Operation{common.ListedOperation(run)}, nil)
}

func withCompanion(t *testing.T) {
	t.Helper()
	prev := renderers
	renderers = append(append([]Renderer{}, prev...), companionRenderer{fakeRenderer{name: "companioned"}})
	t.Cleanup(func() { renderers = prev })
}

func incompleteRun() *report.Run {
	return &report.Run{
		Selection: report.Selection{Mode: "all"},
		Exports:   []report.Export{{Format: "companioned", Path: "x", Status: "written"}},
		Operations: []report.Operation{{Name: "lint", Ran: true, Tools: []report.ToolRun{
			{Name: "eslint", Complete: true},
			{Name: "tsc", Incomplete: []report.Reason{"parse-failed"}},
		}}},
	}
}

// A format with a companion writes it beside its file, both atomically; on
// stdout it has none.
func TestTargetWritesCompanion(t *testing.T) {
	withCompanion(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "cq.json")
	target := Open(Spec{Format: "companioned", Path: path}, nil)
	if target.Err != nil {
		t.Fatal(target.Err)
	}
	if err := target.Write(incompleteRun()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "findings\n" {
		t.Errorf("report = %q", got)
	}
	raw, err := os.ReadFile(path + ".completeness.json")
	if err != nil {
		t.Fatal(err)
	}
	var c common.Companion
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Schema != "datamitsu.completeness/1" || c.Complete || len(c.Tools) != 2 || c.Tools[1].Incomplete[0] != "parse-failed" {
		t.Errorf("companion = %s", raw)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("the directory holds %d entries, want the report and its companion", len(entries))
	}

	var out bytes.Buffer
	if err := Open(Spec{Format: "companioned", Path: Stdout}, &out).Write(incompleteRun()); err != nil {
		t.Fatal(err)
	}
	if out.String() != "findings\n" {
		t.Errorf("stdout = %q, want the report alone", out.String())
	}
}

// A directory target writes a split format's files and removes the ones an
// earlier run left that this one did not write, and nothing else.
func TestTargetDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sarif") + "/"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"datamitsu-1.sarif", "datamitsu-3.sarif", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	target := Open(Spec{Format: "sarif", Path: dir}, nil)
	if target.Err != nil {
		t.Fatal(target.Err)
	}
	if err := target.Write(&report.Run{}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if !reflect.DeepEqual(got, []string{"datamitsu-1.sarif", "notes.txt"}) {
		t.Errorf("directory = %v, want the new file and the unrelated one", got)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "datamitsu-1.sarif")); !strings.Contains(string(data), `"runs": []`) {
		t.Errorf("datamitsu-1.sarif = %s", data)
	}

	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if target := Open(Spec{Format: "sarif", Path: blocker + "/"}, nil); target.Err == nil {
		t.Error("a file at a directory target opened")
	}
}

func TestCheckCapacity(t *testing.T) {
	tests := []struct {
		spec  Spec
		tools int
		want  string
	}{
		{spec: Spec{Format: "sarif", Path: "a.sarif"}, tools: 20},
		{spec: Spec{Format: "sarif", Path: "a.sarif"}, tools: 21, want: "report sarif would hold 21 tools in one file, more than the 20 it can: name a directory, sarif=<dir>/"},
		{spec: Spec{Format: "sarif", Path: "-"}, tools: 21, want: "in stdout"},
		{spec: Spec{Format: "sarif", Path: "out/"}, tools: 500},
		{spec: Spec{Format: "json", Path: "a.json"}, tools: 500},
	}
	for _, tt := range tests {
		err := CheckCapacity(tt.spec, tt.tools)
		if (err == nil) != (tt.want == "") || (err != nil && !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("CheckCapacity(%+v, %d) = %v, want %q", tt.spec, tt.tools, err, tt.want)
		}
	}
}

// Describe names the companion and the tools a format leaves out in the
// run's exports; Notes says the same once per tool.
func TestDescribeAndNotes(t *testing.T) {
	withCompanion(t)
	run := incompleteRun()
	run.Exports = []report.Export{
		{Format: "companioned", Path: "cq.json", Status: "written"},
		{Format: "sarif", Path: "out/", Status: "written"},
	}
	specs := []Spec{{Format: "companioned", Path: "cq.json"}, {Format: "sarif", Path: "out/"}}
	Describe(run, specs)
	if run.Exports[0].Companion != "cq.json.completeness.json" || run.Exports[0].Omitted != nil {
		t.Errorf("companioned export = %+v", run.Exports[0])
	}
	want := []report.OmittedTool{{Operation: "lint", Tool: "tsc", Reasons: []string{"parse-failed"}}}
	if run.Exports[1].Companion != "" || !reflect.DeepEqual(run.Exports[1].Omitted, want) {
		t.Errorf("sarif export = %+v", run.Exports[1])
	}

	notes := Notes(run, specs)
	wantNotes := []string{"report: lint tool tsc is incomplete (parse-failed): left out of sarif; flagged in the completeness companion of companioned"}
	if !reflect.DeepEqual(notes, wantNotes) {
		t.Errorf("Notes = %q, want %q", notes, wantNotes)
	}
	notes = Notes(run, []Spec{{Format: "companioned", Path: Stdout}})
	if len(notes) != 1 || !strings.Contains(notes[0], "listed as if complete by companioned on stdout, which has no companion") {
		t.Errorf("Notes on stdout = %q", notes)
	}
	if notes := Notes(run, []Spec{{Format: "json", Path: "run.json"}}); len(notes) != 0 {
		t.Errorf("the own JSON says it itself, yet Notes = %q", notes)
	}
	run.Operations[0].Tools = run.Operations[0].Tools[1:]
	notes = Notes(run, []Spec{{Format: "sarif", Path: "r.sarif"}})
	if len(notes) != 2 || notes[1] != "report: sarif holds no tool run, so an upload of it changes no alert" {
		t.Errorf("Notes of a SARIF file without a run = %q", notes)
	}
}
