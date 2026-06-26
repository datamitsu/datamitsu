package tooling

import (
	"context"
	"errors"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
)

// fakeParser records its inputs and returns a canned result/error.
type fakeParser struct {
	gotModule, gotParser, gotTool string
	gotStdout                     []byte
	gotExit                       int32
	diags                         []diagnostic.Diagnostic
	err                           error
}

func (f *fakeParser) Parse(_ context.Context, module, parser, toolName string, stdout, _ []byte, exitCode int32) ([]diagnostic.Diagnostic, error) {
	f.gotModule, f.gotParser, f.gotTool, f.gotStdout, f.gotExit = module, parser, toolName, stdout, exitCode
	return f.diags, f.err
}

func parseTask(module, parser string) Task {
	return Task{
		ToolName: "eslint",
		Tool:     config.Tool{Name: "eslint", OutputParser: &config.OutputParser{Module: module, Parser: parser}},
	}
}

func TestParseFileDiagnostics_StampsFileAndAppends(t *testing.T) {
	fp := &fakeParser{diags: []diagnostic.Diagnostic{
		{Message: "a", Row: 1},
		{Message: "b", Row: 2, File: "already.js"}, // pre-set file is kept
	}}
	e := &Executor{parser: fp}
	var result ExecutionResult

	e.parseFileDiagnostics(context.Background(), &result, parseTask("core", "eslint"), "broken.js", []byte("OUT"), []byte("ERR"), 1)

	if fp.gotModule != "core" || fp.gotParser != "eslint" || fp.gotTool != "eslint" ||
		string(fp.gotStdout) != "OUT" || fp.gotExit != 1 {
		t.Fatalf("parser called with unexpected args: %+v", fp)
	}
	if len(result.Diagnostics) != 2 {
		t.Fatalf("got %d diagnostics, want 2", len(result.Diagnostics))
	}
	if result.Diagnostics[0].File != "broken.js" {
		t.Errorf("first diag file = %q, want stamped broken.js", result.Diagnostics[0].File)
	}
	if result.Diagnostics[1].File != "already.js" {
		t.Errorf("parser-provided file overwritten: %q", result.Diagnostics[1].File)
	}
}

func TestParseFileDiagnostics_ParseErrorIsNonFatal(t *testing.T) {
	e := &Executor{parser: &fakeParser{err: errors.New("boom")}}
	var result ExecutionResult
	// Must not panic and must leave Diagnostics empty.
	e.parseFileDiagnostics(context.Background(), &result, parseTask("core", "eslint"), "f.js", nil, nil, 0)
	if len(result.Diagnostics) != 0 {
		t.Errorf("a parse error must yield no diagnostics, got %+v", result.Diagnostics)
	}
}

func TestJoinStreams(t *testing.T) {
	cases := []struct {
		out, err, want string
	}{
		{"out", "", "out"},
		{"", "err", "err"},
		{"out", "err", "out\nerr"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := string(joinStreams([]byte(c.out), []byte(c.err))); got != c.want {
			t.Errorf("joinStreams(%q,%q) = %q, want %q", c.out, c.err, got, c.want)
		}
	}
}
