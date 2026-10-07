package parsermanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

// The three modules the core must keep reading (R12): the released one of
// ABI 1 and descriptor schema 1, the one this crate builds today, and one from
// a later release whose descriptor schema and answers carry what this core
// does not know.

// servedManager serves each module over httptest as the parsers entry of its
// name.
func servedManager(t *testing.T, modules map[string][]byte) *Manager {
	t.Helper()
	t.Setenv("DATAMITSU_PARSERS_DIR", t.TempDir())
	parsers := config.MapOfParsers{}
	for name, wasm := range modules {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(wasm)
		}))
		t.Cleanup(srv.Close)
		sum := sha256.Sum256(wasm)
		parsers[name] = config.Parser{URL: srv.URL, Hash: hex.EncodeToString(sum[:])}
	}
	m := New(parsers)
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	return m
}

func TestTheReleasedModuleParsesItsToolsAndTheEmbeddedOneFallsBack(t *testing.T) {
	ctx := context.Background()
	m := servedManager(t, map[string][]byte{"released": releasedV1(t)})

	caps, err := m.DescribeParser(ctx, "released")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if caps.SchemaVersion != 1 || caps.ABI != 1 {
		t.Errorf("released: schema %d abi %d, want 1 and 1", caps.SchemaVersion, caps.ABI)
	}
	if known, err := m.HasParser(ctx, "released", FallbackParser); err != nil || known {
		t.Errorf("HasParser(released, fallback) = %v, %v; the released module has no sniffer", known, err)
	}
	finding := []byte(`[{"line":1,"code":"DL3006","message":"Always tag","column":1,"file":"Dockerfile","level":"warning"}]`)
	resp, err := m.ParseOutput(ctx, "released", "hadolint", finding, nil, 1)
	if err != nil || resp.ABI != 1 || !resp.Recognized || len(resp.Diagnostics) != 1 {
		t.Fatalf("released hadolint = %+v, %v; want its finding in an ABI 1 answer", resp, err)
	}
	sarif := []byte(`{"version":"2.1.0","runs":[{"results":[{"message":{"text":"m"}}]}]}`)
	resp, err = m.ParseOutput(ctx, "released", "hadolint", sarif, nil, 1)
	if err != nil || resp.Recognized {
		t.Fatalf("released hadolint on SARIF = %+v, %v; want an empty ABI 1 answer read as not recognized", resp, err)
	}
	fallback, err := m.Fallback(ctx, sarif, nil, 1)
	if err != nil || !fallback.Recognized || fallback.Format != "sarif" || len(fallback.Diagnostics) != 1 {
		t.Errorf("the embedded fallback = %+v, %v; want the SARIF finding", fallback, err)
	}
}

func TestTheCurrentModuleAnswersWithItsOwnKeys(t *testing.T) {
	ctx := context.Background()
	m := servedManager(t, map[string][]byte{"core": echoWASM(t)})
	for _, key := range []string{"hadolint", "sarif", FallbackParser} {
		if known, err := m.HasParser(ctx, "core", key); err != nil || !known {
			t.Errorf("HasParser(core, %s) = %v, %v", key, known, err)
		}
	}
	resp, err := m.ParseOutput(ctx, "core", "hadolint", []byte("no JSON here"), nil, 1)
	if err != nil || resp.ABI != 2 || resp.Recognized || resp.Format != "hadolint" {
		t.Errorf("core hadolint on prose = %+v, %v; want an ABI 2 answer that recognized nothing", resp, err)
	}
	// A JSON tool parser given another format's findings leaves them to the
	// fallback, even though the output is JSON.
	sarif := []byte(`{"version":"2.1.0","runs":[{"results":[{"level":"error","message":{"text":"m"}}]}]}`)
	for _, exit := range []int32{0, 1} {
		resp, err = m.ParseOutput(ctx, "core", "hadolint", sarif, nil, exit)
		if err != nil || resp.Recognized {
			t.Errorf("core hadolint on SARIF, exit %d = %+v, %v; want not recognized", exit, resp, err)
		}
	}
	resp, err = m.ParseOutput(ctx, "core", "yamllint", []byte("Success: nothing to report\n"), nil, 0)
	if err != nil || !resp.Recognized {
		t.Errorf("core yamllint on a clean summary = %+v, %v; want recognized", resp, err)
	}
}

func TestAModuleFromALaterReleaseIsReadAsTheNewestKnown(t *testing.T) {
	ctx := context.Background()
	describe := `{"schemaVersion":99,"module":"datamitsu-parsers","version":"9.0.0","abi":2,"features":["format"],` +
		`"hologram":{"x":1},"tools":[{"name":"future","description":"d","url":"","operations":{},` +
		`"severities":["error"],"kind":"format","confidence":"high"}]}`
	parse := `{"recognized":true,"format":"future","confidence":0.97,"diagnostics":[{"message":"m","row":2,"weight":3}]}`
	m := servedManager(t, map[string][]byte{"future": synthModule(t, describe, parse)})

	caps, err := m.DescribeParser(ctx, "future")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if caps.SchemaVersion != SchemaNewest || caps.ABI != 2 || len(caps.Tools) != 1 || caps.Tools[0].Kind != "format" {
		t.Errorf("describe = %+v, want schema %d with its tool", caps, SchemaNewest)
	}
	if contract, err := m.SeverityContract(ctx, "future"); err != nil || !contract {
		t.Errorf("SeverityContract(future) = %v, %v; a later schema keeps the contract", contract, err)
	}
	resp, err := m.ParseOutput(ctx, "future", "future", []byte("x"), nil, 1)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !resp.Recognized || resp.Format != "future" || len(resp.Diagnostics) != 1 ||
		resp.Diagnostics[0].Row == nil || *resp.Diagnostics[0].Row != 2 {
		t.Errorf("parse = %+v, want the one finding with its known fields", resp)
	}
}

// TestOutdatedModulesAreNamed: a catalog lists each distinct module once, and
// DescribeStored describes only what is already in the store.
func TestOutdatedModulesAreNamed(t *testing.T) {
	ctx := context.Background()
	m := servedManager(t, map[string][]byte{"released": releasedV1(t), "core": echoWASM(t)})
	if got := DescribeStored(ctx, m.parsers); len(got) != 0 {
		t.Fatalf("DescribeStored() before any fetch = %+v, want nothing", got)
	}
	cat, err := m.ListCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var outdated []string
	for _, mod := range cat.Modules {
		if mod.Outdated() {
			outdated = append(outdated, mod.Parser)
		}
	}
	if len(cat.Modules) != 2 || len(outdated) != 1 || outdated[0] != "released" {
		t.Errorf("modules = %+v, want core and an outdated released", cat.Modules)
	}
	stored := DescribeStored(ctx, m.parsers)
	if len(stored) != 2 || stored[0].Parser != "core" || stored[1].SchemaVersion != 1 {
		t.Errorf("DescribeStored() = %+v", stored)
	}
	if note := stored[1].OutdatedNote(); !strings.Contains(note, `"released" (datamitsu-parsers v0.2.1) is descriptor schema 1`) {
		t.Errorf("note = %q", note)
	}
}

// synthModule assembles a WebAssembly module of the parser ABI whose describe
// and parse return fixed JSON: a stand-in for a module this core cannot build.
func synthModule(t *testing.T, describe, parse string) []byte {
	t.Helper()
	const base = 16
	describeAt, parseAt := base, base+len(describe)
	packed := func(ptr, n int) int64 { return int64(ptr)<<32 | int64(n) }

	var w wasmWriter
	w.raw([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	i32, i64 := byte(0x7f), byte(0x7e)
	w.section(1, vec( // types
		funcType([]byte{i32}, []byte{i32}),
		funcType([]byte{i32, i32}, nil),
		funcType([]byte{i32, i32, i32, i32, i32, i32, i32}, []byte{i64}),
		funcType(nil, []byte{i64}),
		funcType(nil, nil),
	))
	w.section(3, vec([]byte{0}, []byte{1}, []byte{2}, []byte{3}, []byte{4}))
	w.section(5, vec([]byte{0x00, 0x01}))
	w.section(6, vec(append([]byte{i32, 0x01, 0x41}, append(signedLEB(4096), 0x0b)...)))
	w.section(7, vec(
		export("memory", 0x02, 0),
		export("alloc", 0x00, 0),
		export("dealloc", 0x00, 1),
		export("parse", 0x00, 2),
		export("describe", 0x00, 3),
		export("reset", 0x00, 4),
	))
	alloc := []byte{0x23, 0x00, 0x23, 0x00, 0x20, 0x00, 0x6a, 0x24, 0x00}
	w.section(10, vec(
		body(alloc),
		body(nil),
		body(append([]byte{0x42}, signedLEB(packed(parseAt, len(parse)))...)),
		body(append([]byte{0x42}, signedLEB(packed(describeAt, len(describe)))...)),
		body(nil),
	))
	w.section(11, vec(dataSegment(describeAt, describe), dataSegment(parseAt, parse)))
	return w.buf.Bytes()
}

type wasmWriter struct{ buf bytes.Buffer }

func (w *wasmWriter) raw(b []byte) { w.buf.Write(b) }

func (w *wasmWriter) section(id byte, content []byte) {
	w.buf.WriteByte(id)
	w.buf.Write(unsignedLEB(uint64(len(content))))
	w.buf.Write(content)
}

func vec(items ...[]byte) []byte {
	out := unsignedLEB(uint64(len(items)))
	for _, item := range items {
		out = append(out, item...)
	}
	return out
}

func funcType(params, results []byte) []byte {
	out := append([]byte{0x60}, unsignedLEB(uint64(len(params)))...)
	out = append(out, params...)
	out = append(out, unsignedLEB(uint64(len(results)))...)
	return append(out, results...)
}

func export(name string, kind byte, index int) []byte {
	out := append(unsignedLEB(uint64(len(name))), name...)
	return append(append(out, kind), unsignedLEB(uint64(index))...)
}

func body(code []byte) []byte {
	inner := append([]byte{0x00}, code...) // no locals
	inner = append(inner, 0x0b)
	return append(unsignedLEB(uint64(len(inner))), inner...)
}

func dataSegment(offset int, content string) []byte {
	out := append([]byte{0x00, 0x41}, signedLEB(int64(offset))...)
	out = append(out, 0x0b)
	out = append(out, unsignedLEB(uint64(len(content)))...)
	return append(out, content...)
}

func unsignedLEB(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

func signedLEB(v int64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0) {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}
