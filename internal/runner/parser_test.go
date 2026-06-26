package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
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
	parser := newDiagnosticParser(config.MapOfParsers{
		"core": {URL: srv.URL, Hash: hex.EncodeToString(sum[:])},
	})

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
