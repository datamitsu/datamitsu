package parsermanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

// The core must keep reading every module a released configuration can pin.
// These tests hold it to the last released module of response ABI v1 and
// descriptor schema 1, committed under testdata/released (its provenance is in
// testdata/released/README.md). The file never changes, so what it reports is
// recorded here literally rather than derived from the current crate.

const (
	releasedV1Path    = "testdata/released/v1/b5425355.wasm"
	releasedV1SHA256  = "b5425355969f69f9a80a9838269b350b33ab47ec290358d7aa761483a2921986"
	releasedV1Version = "v0.2.1"
)

// releasedV1Tools is the whole catalogue the released module describes.
var releasedV1Tools = []string{
	"actionlint", "alex", "ansiblelint", "bean_check", "bslint", "buf", "buildifier", "cfn_lint",
	"checkmake", "checkstyle", "clazy", "clj_kondo", "cmake_lint", "codespell", "commitlint",
	"cppcheck", "credo", "cspell", "cue_fmt", "deadnix", "djlint", "dotenv_linter", "echo",
	"editorconfig_checker", "erb_lint", "eslint", "fish", "gccdiag", "gdlint", "gitleaks", "gitlint",
	"glslc", "golangci_lint", "hadolint", "haml_lint", "harper_cli", "ktlint", "kube_linter", "ltrs",
	"markdownlint", "markdownlint_cli2", "markuplint", "mdl", "mlint", "mypy", "npm_groovy_lint",
	"opacheck", "opentofu_validate", "perlimports", "phpcs", "phpmd", "phpstan", "pmd", "proselint",
	"protolint", "puppet_lint", "pydoclint", "pylint", "qmllint", "reek", "regal", "revive",
	"rpmspec", "rstcheck", "rubocop", "saltlint", "selene", "semgrep", "solhint", "spectral",
	"sqlfluff", "sqruff", "staticcheck", "statix", "stylint", "swiftlint", "teal",
	"terraform_validate", "terragrunt_validate", "textidote", "textlint", "tfsec", "tidy", "trivy",
	"tsc", "twigcs", "vacuum", "vale", "verilator", "vint", "write_good", "yamllint", "zsh",
}

// releasedV1 reads the released module and fails unless its bytes are the ones
// the provenance record names.
func releasedV1(t *testing.T) []byte {
	t.Helper()
	wasm, err := os.ReadFile(filepath.FromSlash(releasedV1Path))
	if err != nil {
		t.Fatalf("read the released module: %v", err)
	}
	if sum := sha256.Sum256(wasm); hex.EncodeToString(sum[:]) != releasedV1SHA256 {
		t.Fatalf("%s has SHA-256 %x, want %s: the released fixture is immutable",
			releasedV1Path, sum, releasedV1SHA256)
	}
	return wasm
}

func newReleasedV1Runtime(t *testing.T) *ParserRuntime {
	t.Helper()
	ctx := context.Background()
	rt, err := NewRuntime(ctx, releasedV1(t))
	if err != nil {
		t.Fatalf("NewRuntime(released v1): %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(ctx) })
	return rt
}

func TestReleasedV1Describe(t *testing.T) {
	rt := newReleasedV1Runtime(t)
	caps, err := rt.Describe(context.Background())
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if caps.SchemaVersion != 1 {
		t.Errorf("schemaVersion = %d, want 1", caps.SchemaVersion)
	}
	if caps.Module != "datamitsu-parsers" || caps.Version != releasedV1Version {
		t.Errorf("module %q version %q, want datamitsu-parsers %s", caps.Module, caps.Version, releasedV1Version)
	}
	names := make([]string, 0, len(caps.Tools))
	for _, tool := range caps.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, releasedV1Tools) {
		t.Errorf("described tools = %v\nwant %v", names, releasedV1Tools)
	}
	if rt.reset == nil {
		t.Error("the released module does not export reset")
	}
}

func TestReleasedV1ParsesHadolint(t *testing.T) {
	rt := newReleasedV1Runtime(t)
	out := []byte(`[
{"file":"-","line":3,"column":1,"level":"warning","code":"DL3008","message":"Pin versions in apt get install"},
{"file":"Dockerfile","line":7,"column":2,"level":"info","code":"DL3059","message":"Multiple consecutive RUN instructions"},
{"file":"-","line":9,"column":1,"level":"style","code":"DL3015","message":"Avoid additional packages"},
{"file":"-","line":2,"column":1,"level":"unheard-of","code":"DL9999","message":"Unknown level"}]`)
	diags, err := rt.Parse(context.Background(), "hadolint", out, nil, 1)
	if err != nil {
		t.Fatalf("Parse(hadolint): %v", err)
	}

	type want struct {
		message, code string
		row, col      uint32
		// severity 0 means the module left it unset.
		severity uint8
		file     string
	}
	wants := []want{
		{"Pin versions in apt get install", "DL3008", 3, 1, 2, ""},
		{"Multiple consecutive RUN instructions", "DL3059", 7, 2, 3, "Dockerfile"},
		{"Avoid additional packages", "DL3015", 9, 1, 4, ""},
		{"Unknown level", "DL9999", 2, 1, 0, ""},
	}
	if len(diags) != len(wants) {
		t.Fatalf("got %d diagnostics, want %d: %+v", len(diags), len(wants), diags)
	}
	for i, w := range wants {
		d := diags[i]
		if d.Message != w.message || d.Code == nil || *d.Code != w.code ||
			d.Row == nil || *d.Row != w.row || d.Col == nil || *d.Col != w.col {
			t.Errorf("diagnostic %d = %+v, want %+v", i, d, w)
		}
		switch {
		case w.severity == 0 && d.Severity != nil:
			t.Errorf("diagnostic %d: an unknown level must leave severity unset, got %d", i, *d.Severity)
		case w.severity != 0 && (d.Severity == nil || *d.Severity != w.severity):
			t.Errorf("diagnostic %d: severity = %v, want %d", i, d.Severity, w.severity)
		}
		switch {
		case w.file == "" && d.File != nil:
			t.Errorf("diagnostic %d: the stdin placeholder must leave file unset, got %q", i, *d.File)
		case w.file != "" && (d.File == nil || *d.File != w.file):
			t.Errorf("diagnostic %d: file = %v, want %q", i, d.File, w.file)
		}
		if d.EndRow != nil || d.EndCol != nil || d.Source != nil {
			t.Errorf("diagnostic %d: fields hadolint does not report must stay unset, got %+v", i, d)
		}
	}
}

func TestReleasedV1UnknownToolYieldsNothing(t *testing.T) {
	rt := newReleasedV1Runtime(t)
	diags, err := rt.Parse(context.Background(), "not-a-real-parser", []byte("x"), []byte("y"), 1)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("an unknown tool yields no diagnostics, got %+v", diags)
	}
}

// TestReleasedV1PoolsAndResets serves the released module from a store the
// test fills itself, offline, and checks that sequential parses share one reset
// instance and stay independent of each other.
func TestReleasedV1PoolsAndResets(t *testing.T) {
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "1")
	decl := config.Parser{URL: "https://parsers.example.invalid/released.wasm", Hash: releasedV1SHA256}
	dir := moduleDir("released", decl)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, wasmFileName), releasedV1(t), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m := New(config.MapOfParsers{"released": decl})
	t.Cleanup(func() { _ = m.Close(ctx) })

	if _, err := m.ParseOutput(ctx, "released", "echo", []byte("warm"), nil, 0); err != nil {
		t.Fatalf("warmup ParseOutput: %v", err)
	}
	tracingOn(t)
	for i, input := range []string{"first", "second", "third"} {
		diags, err := m.ParseOutput(ctx, "released", "echo", []byte(input), nil, int32(i))
		if err != nil {
			t.Fatalf("ParseOutput(%s): %v", input, err)
		}
		if len(diags) != 1 || diags[0].Message != input {
			t.Errorf("ParseOutput(%s) = %+v, want the echoed input alone", input, diags)
		}
	}
	if got := counter(t, "parser.module_instantiations"); got != 0 {
		t.Errorf("parser.module_instantiations = %d over 3 sequential parses, want 0", got)
	}
	if got := counter(t, "parser.instance_pool_hits"); got != 3 {
		t.Errorf("parser.instance_pool_hits = %d, want 3", got)
	}
	if got := counter(t, "parser.unresettable_discards"); got != 0 {
		t.Errorf("parser.unresettable_discards = %d, want 0", got)
	}
}
