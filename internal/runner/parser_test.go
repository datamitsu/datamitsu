package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/tooling"
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
func TestDiagnosticParser_EndToEnd(t *testing.T) {
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
	parser := newDiagnosticParser(mgr, newParseProblems())

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
