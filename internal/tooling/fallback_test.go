package tooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
)

func sniffedAs(format string, diags ...diagnostic.Diagnostic) *ParseAnswer {
	return &ParseAnswer{Diagnostics: diags, Recognized: true, Format: format, FormatParser: true}
}

// TestTheParsersReadAnOutputInTurn pins the orchestration of §2.6: which
// parser's answer a process keeps, the outcome it records, what read the
// findings and what the run is told.
func TestTheParsersReadAnOutputInTurn(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "real.c")
	if err := os.WriteFile(existing, []byte("int x;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unavailable := &ParserUnavailableError{Err: errors.New("module did not load")}
	finding := diagnostic.Diagnostic{Message: "m", File: "real.c"}
	cases := []struct {
		name         string
		declared     bool
		parser       *fakeParser
		stdout       string
		want         Extraction
		provenance   string
		module       string
		findings     int
		fellBack     []string
		readNothing  []string
		fallbackRuns int
	}{
		{
			name: "a declared tool parser that recognized the output decides", declared: true,
			parser: &fakeParser{diags: []diagnostic.Diagnostic{finding}}, stdout: "x",
			want: ExtractionParsedFindings, provenance: ProvenanceParser, module: "core", findings: 1,
		},
		{
			name: "a declared format parser", declared: true,
			parser: &fakeParser{format: true}, stdout: "x",
			want: ExtractionParsedClean, provenance: ProvenanceFormat, module: "core",
		},
		{
			name: "the fallback reads what the declared parser did not", declared: true,
			parser: &fakeParser{unrecognized: true, fallback: sniffedAs("sarif", finding)}, stdout: "{}",
			want: ExtractionParsedFindings, provenance: "fallback:sarif", module: EmbeddedParserModule, findings: 1,
			fellBack: []string{"eslint/core/eslint->sarif"}, fallbackRuns: 1,
		},
		{
			name: "neither recognizes the output of a declared parser", declared: true,
			parser: &fakeParser{unrecognized: true}, stdout: "prose",
			want: ExtractionParseFailed, readNothing: []string{"eslint/core/eslint"}, fallbackRuns: 1,
		},
		{
			name: "a parse error falls back and is reported by the parser alone", declared: true,
			parser: &fakeParser{err: errors.New("boom"), fallback: sniffedAs("checkstyle-xml")}, stdout: "<checkstyle/>",
			want: ExtractionParsedClean, provenance: "fallback:checkstyle-xml", module: EmbeddedParserModule,
			fellBack: []string{"eslint/core/eslint->checkstyle-xml"}, fallbackRuns: 1,
		},
		{
			name: "an unavailable parser stays unavailable with the fallback's findings", declared: true,
			parser: &fakeParser{err: unavailable, fallback: sniffedAs("sarif", finding)}, stdout: "{}",
			want: ExtractionParserUnavailable, provenance: "fallback:sarif", module: EmbeddedParserModule, findings: 1,
			fellBack: []string{"eslint/core/eslint->sarif"}, fallbackRuns: 1,
		},
		{
			name:   "a tool without a parser, read by the fallback",
			parser: &fakeParser{fallback: sniffedAs("gcc", finding)}, stdout: "real.c:1:1: error: m",
			want: ExtractionParsedFindings, provenance: "fallback:gcc", module: EmbeddedParserModule, findings: 1,
			fallbackRuns: 1,
		},
		{
			name:   "a tool without a parser whose output nothing recognizes",
			parser: &fakeParser{}, stdout: "All good", want: ExtractionNone, fallbackRuns: 1,
		},
		{
			name:   "empty output is not sniffed",
			parser: &fakeParser{fallback: sniffedAs("gcc", finding)}, stdout: " \n", want: ExtractionNone,
		},
		{
			name: "a line format naming no file that exists is prose",
			parser: &fakeParser{fallback: sniffedAs("gcc",
				diagnostic.Diagnostic{Message: "m", File: "2024-01-02 10"})}, stdout: "2024-01-02 10:20:30: started",
			want: ExtractionNone, fallbackRuns: 1,
		},
		{
			name: "a line format keeps the findings on files that exist",
			parser: &fakeParser{fallback: sniffedAs("gcc", finding,
				diagnostic.Diagnostic{Message: "gone", File: "gone.c"})}, stdout: "real.c:1:1: e",
			want: ExtractionParsedFindings, provenance: "fallback:gcc", module: EmbeddedParserModule, findings: 1,
			fallbackRuns: 1,
		},
		{
			name: "a declared parser's partial answer is truncated", declared: true,
			parser: &fakeParser{diags: []diagnostic.Diagnostic{finding}, partial: true}, stdout: "[{",
			want: ExtractionTruncated, provenance: ProvenanceParser, module: "core", findings: 1,
		},
		{
			name:   "the fallback's findings in a partial output are truncated",
			parser: &fakeParser{fallback: &ParseAnswer{Diagnostics: []diagnostic.Diagnostic{finding}, Recognized: true, Format: "sarif", Partial: true}},
			stdout: "{}", want: ExtractionTruncated, provenance: "fallback:sarif", module: EmbeddedParserModule, findings: 1,
			fallbackRuns: 1,
		},
		{
			name:   "a document cut off that nothing read leaves a tool without a parser truncated",
			parser: &fakeParser{fallback: &ParseAnswer{Format: "fallback", Partial: true}}, stdout: `{"runs":[`,
			want: ExtractionTruncated, fallbackRuns: 1,
		},
		{
			name:   "a fallback that fails leaves a tool without a parser parse-failed",
			parser: &fakeParser{fallbackFails: errors.New("trap")}, stdout: "x",
			want: ExtractionParseFailed, fallbackRuns: 1,
		},
		{
			name: "a line format that named no file leaves the later ones their turn",
			parser: &fakeParser{
				fallback: sniffedAs("github-annotations", diagnostic.Diagnostic{Message: "noise", File: "/no/such/source.xyz"}),
				formats: map[string]ParseAnswer{
					"gcc": {Diagnostics: []diagnostic.Diagnostic{finding}, Recognized: true, Format: "gcc"},
				},
			},
			stdout: "::warning file=/no/such/source.xyz,line=1::noise\nreal.c:1:1: error: m",
			want:   ExtractionParsedFindings, provenance: "fallback:gcc", module: EmbeddedParserModule, findings: 1,
			fallbackRuns: 1,
		},
		{
			name: "a structured format keeps a finding on a path that is not there",
			parser: &fakeParser{fallback: sniffedAs("sarif",
				diagnostic.Diagnostic{Message: "m", File: "gone.py"})}, stdout: "{}",
			want: ExtractionParsedFindings, provenance: "fallback:sarif", module: EmbeddedParserModule, findings: 1,
			fallbackRuns: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &Executor{parser: c.parser}
			task := parseTask("core", "eslint")
			var proc ProcessResult
			e.parseFileDiagnostics(context.Background(), &proc, task, dir, []byte(c.stdout), nil, 1, c.declared)
			if proc.Extraction != c.want || proc.Provenance != c.provenance || proc.ParserModule != c.module {
				t.Errorf("got %q, %q, %q; want %q, %q, %q",
					proc.Extraction, proc.Provenance, proc.ParserModule, c.want, c.provenance, c.module)
			}
			if len(proc.Diagnostics) != c.findings {
				t.Errorf("findings = %+v, want %d", proc.Diagnostics, c.findings)
			}
			if !slices.Equal(c.parser.fellBack, c.fellBack) || !slices.Equal(c.parser.readNothing, c.readNothing) {
				t.Errorf("told fell back %q, read nothing %q; want %q, %q",
					c.parser.fellBack, c.parser.readNothing, c.fellBack, c.readNothing)
			}
			if c.parser.fallbackCalls != c.fallbackRuns {
				t.Errorf("the fallback ran %d times, want %d", c.parser.fallbackCalls, c.fallbackRuns)
			}
		})
	}
}

// TestOutputCutShortIsTruncatedWhateverReadIt: output over a parse limit is
// truncated even when nothing recognized the part a parser read, so a pass is
// never recorded over what the cut left unread.
func TestOutputCutShortIsTruncatedWhateverReadIt(t *testing.T) {
	for _, declared := range []bool{false, true} {
		fp := &fakeParser{unrecognized: true}
		e := &Executor{parser: fp}
		e.SetParseLimits(ParseLimits{InputBytes: 8, Findings: 10})
		var proc ProcessResult
		e.parseFileDiagnostics(context.Background(), &proc, parseTask("core", "eslint"), t.TempDir(),
			[]byte("progress 10%\nreal.c:1:1: error: m\n"), nil, 0, declared)
		if proc.Extraction != ExtractionTruncated {
			t.Errorf("declared %v: extraction %q, want truncated", declared, proc.Extraction)
		}
	}
}

// TestAFormattersStdoutReachesNoParser: the formatted file content is never
// sniffed, whatever it holds; the formatter's stderr is.
func TestAFormattersStdoutReachesNoParser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tool is an sh script")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "x.go")
	if err := os.WriteFile(file, []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fp := &fakeParser{fallback: sniffedAs("gcc", diagnostic.Diagnostic{Message: "y", File: file})}
	e := NewExecutor(dir, false, false, &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"fmt": shellApp(`printf 'package x\n// x.go:1:2: error: y\n'; echo 'note' >&2`),
	}}, nil)
	e.SetParser(fp)
	e.executeTask(context.Background(), Task{
		ToolName:  "fmt",
		Operation: config.OpFix,
		Tool:      config.Tool{Name: "fmt", OutputParser: &config.OutputParser{Module: "core", Parser: "gcc"}},
		OpConfig: config.ToolOperation{
			App: "fmt", Scope: config.ToolScopePerFile, Args: []string{"{file}"}, Output: config.ToolOutputStdout,
		},
		Files:       []string{file},
		ProjectPath: dir,
	})
	if fp.gotModule != "" {
		t.Errorf("the declared parser read a formatter's output (%q)", fp.gotStdout)
	}
	if len(fp.fallbackStdout) != 0 || string(fp.fallbackErr) != "note\n" {
		t.Errorf("the fallback read stdout %q and stderr %q, want stderr alone", fp.fallbackStdout, fp.fallbackErr)
	}
}

// TestNoPassOverOutputTheFallbackCouldNotRead: a tool without a parser whose
// output the fallback failed on, or found a document cut off in, records no
// pass though it exited 0.
func TestNoPassOverOutputTheFallbackCouldNotRead(t *testing.T) {
	for name, fp := range map[string]*fakeParser{
		"failed":  {fallbackFails: errors.New("trap")},
		"partial": {fallback: &ParseAnswer{Format: "fallback", Partial: true}},
	} {
		e := &Executor{parser: fp}
		var proc ProcessResult
		e.parseFileDiagnostics(context.Background(), &proc, parseTask("core", "eslint"), t.TempDir(), []byte("x"), nil, 0, false)
		if got := passesOf(proc, []string{"a.go"}); len(got) != 0 {
			t.Errorf("%s: %s records passes %v", name, proc.Extraction, got)
		}
	}
}

// TestTheLaterLineFormatsRunInTheSniffersOrder: when the sniffer's line format
// named no file, the core asks the ones after it, in order, until one does.
func TestTheLaterLineFormatsRunInTheSniffersOrder(t *testing.T) {
	fp := &fakeParser{fallback: sniffedAs("azure-logissue", diagnostic.Diagnostic{Message: "m", File: "/nowhere"})}
	e := &Executor{parser: fp}
	var proc ProcessResult
	e.parseFileDiagnostics(context.Background(), &proc, parseTask("core", "eslint"), t.TempDir(), []byte("x"), nil, 0, false)
	if want := []string{"msvc", "gcc"}; !slices.Equal(fp.formatsAsked, want) {
		t.Errorf("asked %q, want %q", fp.formatsAsked, want)
	}
	if proc.Extraction != ExtractionNone {
		t.Errorf("extraction %q, want none: nothing named a file", proc.Extraction)
	}
}
