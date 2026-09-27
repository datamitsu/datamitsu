package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/tooling"

	"go.uber.org/zap"
)

func TestParsingDisabled(t *testing.T) {
	t.Cleanup(func() { SetParsingDisabledByFlag(false) })

	SetParsingDisabledByFlag(false)
	if parsingDisabled() {
		t.Error("parsingDisabled() = true with flag off and env unset")
	}

	SetParsingDisabledByFlag(true)
	if !parsingDisabled() {
		t.Error("parsingDisabled() = false, want true when --no-parse flag set")
	}

	SetParsingDisabledByFlag(false)
	t.Setenv("DATAMITSU_NO_PARSE", "1")
	if !parsingDisabled() {
		t.Error("parsingDisabled() = false, want true when DATAMITSU_NO_PARSE set")
	}
}

// TestDiagnosticParser_EndToEnd proves the full consume path on the real WASM
// module: serve the committed parser fixture, resolve a parsers config entry,
// run eslint's actual --format json output through it, and assert finalized
// diagnostics (defaults filled, eslint's numeric severity normalized).
// coreModule serves the committed crate build as the parsers entry "core" and
// returns a Manager over it.
func coreModule(t *testing.T) *parsermanager.Manager {
	t.Helper()
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())

	wasm, err := os.ReadFile(filepath.Join("..", "parsermanager", "testdata", "echo.wasm"))
	if err != nil {
		t.Fatalf("read wasm fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(wasm)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(wasm)

	// The parsers entry is named "core", NOT after the dispatch key — proving
	// module and parser are independent (so versions can be aliased freely).
	mgr := parsermanager.New(config.MapOfParsers{
		"core": {URL: srv.URL, Hash: hex.EncodeToString(sum[:])},
	})
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })
	return mgr
}

func TestDiagnosticParser_EndToEnd(t *testing.T) {
	parser := newDiagnosticParser(coreModule(t), newParseProblems())

	eslintJSON := []byte(`[{"filePath":"a.js","messages":[` +
		`{"ruleId":"no-undef","severity":2,"message":"'z' is not defined.","line":2,"column":25,"endLine":2,"endColumn":26},` +
		`{"ruleId":"semi","severity":1,"message":"Missing semicolon.","line":1,"column":10}]}]`)

	// module "core" (the parsers entry), parser "eslint" (dispatch key), tool "eslint" (source).
	diags, err := parser.Parse(context.Background(), "core", "eslint", "eslint", eslintJSON, nil, 1)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want 2: %+v", len(diags), diags)
	}

	// eslint severity 2 → Error, fields preserved.
	if diags[0].Severity != diagnostic.SeverityError || diags[0].Code != "no-undef" ||
		diags[0].Row != 2 || diags[0].Col != 25 {
		t.Errorf("unexpected first diagnostic: %+v", diags[0])
	}
	// eslint severity 1 → Warning; missing end_* defaulted to start by Resolve.
	if diags[1].Severity != diagnostic.SeverityWarning {
		t.Errorf("second severity = %v, want Warning", diags[1].Severity)
	}
	if diags[1].EndRow != diags[1].Row || diags[1].EndCol != diags[1].Col {
		t.Errorf("missing end should default to start, got %+v", diags[1])
	}
	if diags[1].Source != "eslint" {
		t.Errorf("source = %q, want eslint", diags[1].Source)
	}
}

// TestDiagnosticParser_Unavailable: a key the module does not list, and a
// module that cannot load, are reported as unavailable — never as an empty,
// clean parse — and recorded for the run's warnings.
func TestDiagnosticParser_Unavailable(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	wasm, err := os.ReadFile(filepath.Join("..", "parsermanager", "testdata", "echo.wasm"))
	if err != nil {
		t.Fatalf("read wasm fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(wasm)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(wasm)
	mgr := parsermanager.New(config.MapOfParsers{
		"core":   {URL: srv.URL, Hash: hex.EncodeToString(sum[:])},
		"broken": {URL: srv.URL, Hash: strings.Repeat("0", 64)},
	})
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })
	problems := newParseProblems()
	parser := newDiagnosticParser(mgr, problems)

	for _, c := range []struct{ module, key, tool string }{
		{"core", "no-such-parser", "alpha"},
		{"core", "no-such-parser", "beta"},
		{"broken", "hadolint", "gamma"},
		{"broken", "hadolint", "gamma"},
		{"broken", "yamllint", "delta"},
	} {
		diags, err := parser.Parse(context.Background(), c.module, c.key, c.tool, []byte("x"), nil, 1)
		if _, ok := errors.AsType[*tooling.ParserUnavailableError](err); !ok {
			t.Errorf("%s/%s: err = %v, want a ParserUnavailableError", c.module, c.key, err)
		}
		if len(diags) != 0 {
			t.Errorf("%s/%s: diagnostics = %+v, want none", c.module, c.key, diags)
		}
	}

	got := problems.pending()
	if len(got) != 2 {
		t.Fatalf("pending = %q, want one warning for the module and one for the key", got)
	}
	if !strings.HasPrefix(got[0], `parser module "broken" could not be loaded, so 2 tool(s) that use it ran without parsing `+
		`and their lint passes are not cached; `+
		`"datamitsu devtools parsers prefetch" fetches it ahead of a run: `) {
		t.Errorf("module warning = %q", got[0])
	}
	if want := `parser module "core" has no parser "no-such-parser", so the output of alpha, beta is not parsed ` +
		`and its lint passes are not cached`; got[1] != want {
		t.Errorf("key warning = %q, want %q", got[1], want)
	}
	if again := problems.pending(); len(again) != 0 {
		t.Errorf("a problem is reported once per run, got %q again", again)
	}
}

func TestParseProblems_FailedParseOncePerTool(t *testing.T) {
	problems := newParseProblems()
	problems.parseFailed("hadolint", errors.New("first"))
	problems.parseFailed("hadolint", errors.New("second"))
	problems.parseFailed("eslint", errors.New("boom"))
	want := []string{"output parser failed for eslint: boom", "output parser failed for hadolint: first"}
	if got := problems.pending(); !slices.Equal(got, want) {
		t.Errorf("pending = %q, want %q", got, want)
	}
	problems.parseFailed("hadolint", errors.New("third"))
	if got := problems.pending(); len(got) != 0 {
		t.Errorf("a tool already reported this run is not reported again, got %q", got)
	}
}

// failingModules loads every module and knows every key, and fails every parse
// the way a module does whose output the core cannot decode.
type failingModules struct{}

func (failingModules) HasParser(context.Context, string, string) (bool, error) { return true, nil }

func (failingModules) ParseOutput(context.Context, string, string, []byte, []byte, int32) ([]parsermanager.RawDiagnostic, error) {
	return nil, errors.New("decode parser output: unexpected end of JSON input")
}

type shellApps map[string]*binmanager.CommandInfo

func (a shellApps) GetBinaryPath(context.Context, string) (string, error) { return "", os.ErrNotExist }

func (a shellApps) GetCommandInfo(_ context.Context, app string) (*binmanager.CommandInfo, error) {
	return a[app], nil
}

// TestParseFailureThroughTheExecutor follows a parse that fails in a loaded
// module through the whole path: the process is parse-failed, the task reports
// ParseFailed, no pass is cached, and the run warns once for the tool however
// many of its invocations failed.
func TestParseFailureThroughTheExecutor(t *testing.T) {
	root := t.TempDir()
	files := make([]string, 0, 2)
	for _, name := range []string{"a.txt", "b.txt"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	c, err := cache.NewCache(t.TempDir(), root, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	executor := tooling.NewExecutor(root, false, false, shellApps{
		"hadolint": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", "exit 0"}},
	}, c)
	problems := newParseProblems()
	executor.SetParser(newDiagnosticParser(failingModules{}, problems))

	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{{
		ToolName:    "hadolint",
		Tool:        config.Tool{Name: "hadolint", OutputParser: &config.OutputParser{Module: "core", Parser: "hadolint"}},
		Operation:   config.OpLint,
		OpConfig:    config.ToolOperation{App: "hadolint", Scope: config.ToolScopePerFile, Args: []string{"{file}"}},
		Files:       files,
		ProjectPath: root,
	}}}}}
	results, err := executor.Execute(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	result := results[0].Results[0]
	if !result.Success || !result.ParseFailed {
		t.Errorf("Success = %v, ParseFailed = %v, want a passing task whose output was not parsed", result.Success, result.ParseFailed)
	}
	for _, proc := range result.Processes {
		if proc.Extraction != tooling.ExtractionParseFailed || !strings.Contains(proc.ParseError, "unexpected end of JSON input") {
			t.Errorf("process %s: Extraction = %s, ParseError = %q", proc.ID, proc.Extraction, proc.ParseError)
		}
	}
	for _, file := range files {
		if !c.Check(file, "hadolint", cache.OperationLint, observeFile(t, file), true) {
			t.Errorf("%s: a pass was cached for output that was not parsed", file)
		}
	}
	want := []string{"output parser failed for hadolint: decode parser output: unexpected end of JSON input"}
	if got := problems.pending(); !slices.Equal(got, want) {
		t.Errorf("warnings = %q, want %q", got, want)
	}
}

// TestLevelsAndRuleURLThroughTheExecutor follows findings from the real module
// to the task result: the rule URL tfsec prints survives decoding, resolution
// and aggregation; a level the tool printed stays what it said whatever the
// exit code; and a finding without one (checkmake prints none) is an error when
// the tool failed and a warning when it passed.
func TestLevelsAndRuleURLThroughTheExecutor(t *testing.T) {
	const tfsecJSON = `{"results":[{"rule_id":"aws-s3-enable-bucket-logging",` +
		`"description":"Bucket has logging disabled","severity":"LOW",` +
		`"links":["https://example.test/checks/aws-s3-enable-bucket-logging"],` +
		`"location":{"filename":"main.tf","start_line":4,"end_line":6}}]}`
	const checkmakeLine = `1:minphony:Required target "all" is missing from the Makefile.`
	mgr := coreModule(t)

	cases := []struct {
		name, parser, output string
		exit                 int
		want                 diagnostic.Severity
		wantURL              string
	}{
		{"printed level, passed", "tfsec", tfsecJSON, 0, diagnostic.SeverityInfo, "https://example.test/checks/aws-s3-enable-bucket-logging"},
		{"printed level, failed", "tfsec", tfsecJSON, 1, diagnostic.SeverityInfo, "https://example.test/checks/aws-s3-enable-bucket-logging"},
		{"no level, passed", "checkmake", checkmakeLine, 0, diagnostic.SeverityWarning, ""},
		{"no level, failed", "checkmake", checkmakeLine, 1, diagnostic.SeverityError, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "input")
			if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			script := fmt.Sprintf("printf '%%s\\n' '%s'; exit %d", tc.output, tc.exit)
			executor := tooling.NewExecutor(root, false, false, shellApps{
				"tool": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", script}},
			}, nil)
			executor.SetParser(newDiagnosticParser(mgr, newParseProblems()))
			plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{{
				ToolName:    tc.parser,
				Tool:        config.Tool{Name: tc.parser, OutputParser: &config.OutputParser{Module: "core", Parser: tc.parser}},
				Operation:   config.OpLint,
				OpConfig:    config.ToolOperation{App: "tool", Scope: config.ToolScopePerFile, Args: []string{"{file}"}},
				Files:       []string{file},
				ProjectPath: root,
			}}}}}
			results, err := executor.Execute(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			diags := results[0].Results[0].Diagnostics
			if len(diags) != 1 {
				t.Fatalf("diagnostics = %+v, want one", diags)
			}
			if diags[0].Severity != tc.want || diags[0].URL != tc.wantURL {
				t.Errorf("severity %v, url %q; want %v, %q", diags[0].Severity, diags[0].URL, tc.want, tc.wantURL)
			}
		})
	}
}

// TestThresholdFailsTheNamedFileOfABatch runs the real module over one batch
// of each list-taking tool: a finding at or above --fail-on=warning at exit 0
// fails the run, and only the file it names.
func TestThresholdFailsTheNamedFileOfABatch(t *testing.T) {
	mgr := coreModule(t)
	reports := map[string]string{
		"semgrep": `{"results":[{"check_id":"r.eval","path":"a.py","start":{"line":1,"col":1},` +
			`"end":{"line":1,"col":6},"extra":{"message":"eval is dangerous","severity":"ERROR"}}]}`,
		"tfsec": `{"results":[{"rule_id":"r","description":"d","severity":"HIGH","status":0,` +
			`"location":{"filename":"a.py","start_line":1,"end_line":1}}]}`,
		"buildifier": `{"files":[{"filename":"a.py","warnings":[{"start":{"line":1,"column":1},` +
			`"end":{"line":1,"column":2},"category":"load","message":"m"}]}]}`,
		"phpstan": `{"files":{"a.py":{"errors":1,"messages":[{"message":"m","line":1}]}}}`,
		"phpcs": `{"files":{"a.py":{"errors":1,"warnings":0,"messages":[{"message":"m","source":"S.R",` +
			`"severity":5,"type":"ERROR","line":1,"column":1}]}}}`,
		// The finding sits in the second file of the report, behind a clean one.
		"rubocop": `{"files":[{"path":"b.py","offenses":[]},{"path":"a.py","offenses":[{"severity":"error",` +
			`"message":"m","cop_name":"X/Y","corrected":false,"location":{"start_line":1,"start_column":1,` +
			`"last_line":1,"last_column":2}}]}]}`,
	}
	for parser, report := range reports {
		t.Run(parser, func(t *testing.T) {
			root := t.TempDir()
			files := make([]string, 0, 2)
			for _, name := range []string{"a.py", "b.py"} {
				path := filepath.Join(root, name)
				if err := os.WriteFile(path, []byte("x = 1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				files = append(files, path)
			}
			executor := tooling.NewExecutor(root, false, false, shellApps{
				"tool": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", "printf '%s' '" + report + "'"}},
			}, nil)
			executor.SetParser(newDiagnosticParser(mgr, newParseProblems()))
			executor.SetGate(tooling.ThresholdGate(config.SeverityWarning, func(module string) bool {
				ok, err := mgr.SeverityContract(context.Background(), module)
				return err == nil && ok
			}, nil))
			plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{{
				ToolName:    parser,
				Tool:        config.Tool{Name: parser, OutputParser: &config.OutputParser{Module: "core", Parser: parser}},
				Operation:   config.OpLint,
				OpConfig:    config.ToolOperation{App: "tool", Scope: config.ToolScopeRepository, Args: []string{"{files}"}},
				Files:       files,
				ProjectPath: root,
			}}}}}
			results, err := executor.Execute(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			result := results[0].Results[0]
			if result.Success || result.FailureReason != tooling.FailureReasonThreshold {
				t.Fatalf("Success = %v, FailureReason = %v; want a threshold failure", result.Success, result.FailureReason)
			}
			got := make([]bool, 0, len(result.FileResults))
			for _, fr := range result.FileResults {
				got = append(got, fr.Success)
			}
			if !slices.Equal(got, []bool{false, true}) {
				t.Errorf("file success = %v, want only a.py failed", got)
			}
		})
	}
}

func observeFile(t *testing.T, path string) cache.Seen {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	hash, err := hashutil.XXH3Reader(f)
	if err != nil {
		t.Fatal(err)
	}
	return cache.Seen{Hash: hash}
}
