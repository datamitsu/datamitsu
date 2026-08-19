package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/engine"
	"github.com/datamitsu/datamitsu/internal/timing"

	"github.com/dop251/goja"
)

// stripTypesCalls reports how many esbuild passes were recorded since the
// startup recorder was last reset. Instrumentation is the observation seam:
// prepareConfigSource records PhaseStripTypes exactly when it calls esbuild, so
// counting the phase counts the calls without adding an indirection to the production path.
func stripTypesCalls(t *testing.T) int {
	t.Helper()
	for _, p := range timing.StartupPhases() {
		if p.Name == timing.PhaseStripTypes {
			return p.Count
		}
	}
	return 0
}

func TestIsPlainJavaScriptSource(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{"js file", "/repo/datamitsu.config.js", true},
		{"mjs file", "/repo/datamitsu.config.mjs", true},
		{"uppercase js", "/repo/DATAMITSU.CONFIG.JS", true},
		{"ts file", "/repo/datamitsu.config.ts", false},
		{"mts file", "/repo/datamitsu.config.mts", false},
		{"cts file", "/repo/datamitsu.config.cts", false},
		{"cjs file", "/repo/datamitsu.config.cjs", false},
		{"no extension", "/repo/config-without-extension", false},
		{"named source", "default", false},
		{"empty", "", false},
		{"https js url", "https://example.com/shared/datamitsu.config.js", true},
		{"https mjs url", "https://example.com/shared/datamitsu.config.mjs", true},
		{"https js url with query", "https://example.com/c.js?v=2", true},
		{"https ts url", "https://example.com/shared/datamitsu.config.ts", false},
		{"https no extension", "https://example.com/shared/config", false},
		{"oci reference", "oci://ghcr.io/datamitsu/config:v1", false},
		{"dotted host only", "https://example.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPlainJavaScriptSource(tt.ref); got != tt.want {
				t.Errorf("isPlainJavaScriptSource(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

// TestPrepareConfigSourceReachesEsbuild pins which sources pay the esbuild pass.
func TestPrepareConfigSourceReachesEsbuild(t *testing.T) {
	tests := []struct {
		name      string
		ref       string
		wantStrip bool
	}{
		{"ts strips", "config.ts", true},
		{"mts strips", "config.mts", true},
		{"cts strips", "config.cts", true},
		{"no extension strips", "config", true},
		{"remote url without extension strips", "https://example.com/config", true},
		{"js skips", "config.js", false},
		{"mjs skips", "config.mjs", false},
		{"remote js url skips", "https://example.com/config.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enableStartupTimings(t)

			const content = "function getConfig(input) { return {}; }\n"
			if _, err := prepareConfigSource(content, tt.ref); err != nil {
				t.Fatalf("prepareConfigSource error: %v", err)
			}

			want := 0
			if tt.wantStrip {
				want = 1
			}
			if got := stripTypesCalls(t); got != want {
				t.Errorf("esbuild calls = %d, want %d for %q", got, want, tt.ref)
			}
		})
	}
}

// TestPrepareConfigSourceJSPassthrough asserts a .js config esbuild would
// otherwise rewrite reaches goja byte-identically.
func TestPrepareConfigSourceJSPassthrough(t *testing.T) {
	// Formatting esbuild normalizes (double quotes, indentation, comment
	// stripping, no trailing semicolons) plus modern syntax it could downlevel.
	const content = `// keep me
const  x   =   { a : 1 ,   ...{ b : 2 } }
class C { #p = 1 ; get p ( ) { return this . #p } }
const y = x ?. a ?? 0
function getConfig ( input ) { return { ...input , tools : { } } }
`

	got, err := prepareConfigSource(content, "/repo/datamitsu.config.js")
	if err != nil {
		t.Fatalf("prepareConfigSource error: %v", err)
	}
	if got != content {
		t.Errorf("js config was rewritten\n got: %q\nwant: %q", got, content)
	}
}

// TestPrepareConfigSourceTSStillStripped asserts .ts sources keep going
// through esbuild and come out as executable JavaScript.
func TestPrepareConfigSourceTSStillStripped(t *testing.T) {
	const content = `interface Cfg { tools: Record<string, unknown> }
const name: string = "demo";
function getConfig(input: Cfg): Cfg { return input as Cfg; }
`

	got, err := prepareConfigSource(content, "/repo/datamitsu.config.ts")
	if err != nil {
		t.Fatalf("prepareConfigSource error: %v", err)
	}
	if got == content {
		t.Fatal("ts config was passed through unchanged; types were not stripped")
	}
	for _, leftover := range []string{"interface Cfg", ": string", "as Cfg"} {
		if strings.Contains(got, leftover) {
			t.Errorf("stripped output still contains TypeScript %q:\n%s", leftover, got)
		}
	}
	if !strings.Contains(got, "function getConfig") {
		t.Errorf("stripped output lost getConfig:\n%s", got)
	}
}

// TestLoadConfigFileSkipsEsbuildForJS exercises the real file-loading path:
// a .js config must execute without reaching esbuild, a .ts one must reach it.
func TestLoadConfigFileSkipsEsbuildForJS(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		content   string
		wantStrip bool
	}{
		{
			name:      "js file",
			file:      "datamitsu.config.js",
			content:   `function getConfig(input) { return { ignoreRules: ["from-js: eslint"] }; }`,
			wantStrip: false,
		},
		{
			name:      "ts file",
			file:      "datamitsu.config.ts",
			content:   `function getConfig(input: unknown) { return { ignoreRules: ["from-ts: eslint"] }; }`,
			wantStrip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enableStartupTimings(t)

			p := filepath.Join(t.TempDir(), tt.file)
			if err := os.WriteFile(p, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			e, err := engine.New(context.Background(), "")
			if err != nil {
				t.Fatalf("engine.New error: %v", err)
			}
			if err := loadConfigFile(e, p); err != nil {
				t.Fatalf("loadConfigFile error: %v", err)
			}

			if _, ok := goja.AssertFunction(e.VM().Get("getConfig")); !ok {
				t.Fatal("getConfig is not a function after load")
			}

			want := 0
			if tt.wantStrip {
				want = 1
			}
			if got := stripTypesCalls(t); got != want {
				t.Errorf("esbuild calls = %d, want %d", got, want)
			}
		})
	}
}
