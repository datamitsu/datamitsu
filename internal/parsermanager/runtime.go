package parsermanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// RawDiagnostic is the tentative Go mirror of the WASM parser's output form
// (parsers/datamitsu-parsers/src/diagnostic.rs).
//
// ⚠ Shape placeholder — finalized in Phase 2. This is NOT the core's final
// Diagnostic struct (the Go core owns that, designed later). The only commitment
// held here is the nullable contract: only Message is mandatory; every other
// field is a pointer so a JSON-absent field stays nil, meaning "the tool did not
// emit this" rather than a zero value the core would mistake for real data.
type RawDiagnostic struct {
	Message  string  `json:"message"`
	Row      *uint32 `json:"row,omitempty"`
	Col      *uint32 `json:"col,omitempty"`
	EndRow   *uint32 `json:"end_row,omitempty"`
	EndCol   *uint32 `json:"end_col,omitempty"`
	Severity *uint8  `json:"severity,omitempty"`
	Source   *string `json:"source,omitempty"`
	Code     *string `json:"code,omitempty"`
}

// ParserRuntime is an instantiated wazero module ready to parse tool output. It
// drives the module's memory ABI (alloc/dealloc + ptr/len buffers). Get one from
// Manager.Acquire (shared, compile-once runtime) or NewRuntime (one-shot, owns
// its runtime) and Close it when done; it is not safe for concurrent Parse calls
// (a single module instance owns its linear memory).
type ParserRuntime struct {
	// ownRuntime is set only for one-shot instances (NewRuntime) that own their
	// runtime; nil for instances a Manager hands out from its shared, compile-once
	// runtime. Close releases the whole runtime when set, otherwise just this
	// instance — so a Manager can serve many isolated instances from one runtime.
	ownRuntime wazero.Runtime
	mod        api.Module
	alloc      api.Function
	dealloc    api.Function
	parse      api.Function
	describe   api.Function
}

// NewRuntime instantiates a parser module from its verified WASM bytes in a
// fresh, sandboxed wazero runtime (no host imports, no WASI — the parser is pure
// computation over byte buffers). The instance owns this runtime; Close releases
// it. For repeated parsing of the same module, prefer Manager.Acquire, which
// compiles once and instantiates per call.
func NewRuntime(ctx context.Context, wasm []byte) (*ParserRuntime, error) {
	r := wazero.NewRuntime(ctx)
	mod, err := r.Instantiate(ctx, wasm)
	if err != nil {
		_ = r.Close(ctx)
		return nil, fmt.Errorf("instantiate parser module: %w", err)
	}
	pr, err := newInstance(ctx, mod, r)
	if err != nil {
		_ = r.Close(ctx)
		return nil, err
	}
	return pr, nil
}

// newInstance wraps an already-instantiated module, resolving its ABI exports.
// ownRuntime is the runtime to close together with the instance (one-shot use),
// or nil when a Manager owns the shared runtime and Close releases only the
// instance's linear memory.
func newInstance(ctx context.Context, mod api.Module, ownRuntime wazero.Runtime) (*ParserRuntime, error) {
	pr := &ParserRuntime{
		ownRuntime: ownRuntime,
		mod:        mod,
		alloc:      mod.ExportedFunction("alloc"),
		dealloc:    mod.ExportedFunction("dealloc"),
		parse:      mod.ExportedFunction("parse"),
		describe:   mod.ExportedFunction("describe"),
	}
	if pr.alloc == nil || pr.dealloc == nil || pr.parse == nil || pr.describe == nil {
		_ = mod.Close(ctx)
		return nil, errors.New("parser module missing required exports (alloc/dealloc/parse/describe)")
	}
	return pr, nil
}

// Describe invokes the module's `describe` export and decodes its capability
// manifest: the tools it can parse, how to invoke each, and its build-injected
// version. It takes no tool input — it is pure static introspection, the
// counterpart to Parse.
func (p *ParserRuntime) Describe(ctx context.Context) (Capabilities, error) {
	res, err := p.describe.Call(ctx)
	if err != nil {
		return Capabilities{}, fmt.Errorf("call describe: %w", err)
	}

	// describe returns (ptr << 32) | len of a freshly allocated output buffer.
	packed := res[0]
	resPtr := uint32(packed >> 32)
	resLen := uint32(packed & 0xffffffff)
	defer p.free(ctx, resPtr, resLen)

	buf, ok := p.mod.Memory().Read(resPtr, resLen)
	if !ok {
		return Capabilities{}, fmt.Errorf("describe output out of range (ptr=%d len=%d)", resPtr, resLen)
	}
	// Copy before decoding so a later alloc cannot move the bytes out from under
	// json.Unmarshal (Memory().Read may return a view into linear memory).
	out := make([]byte, len(buf))
	copy(out, buf)

	var caps Capabilities
	if err := json.Unmarshal(out, &caps); err != nil {
		return Capabilities{}, fmt.Errorf("decode describe output: %w", err)
	}
	return caps, nil
}

// Close releases this instance. For a one-shot runtime (NewRuntime) it closes the
// whole runtime; for a Manager-owned instance it closes only this module's linear
// memory, leaving the shared runtime and its compiled modules intact.
func (p *ParserRuntime) Close(ctx context.Context) error {
	if p.ownRuntime != nil {
		if err := p.ownRuntime.Close(ctx); err != nil {
			return fmt.Errorf("close parser runtime: %w", err)
		}
		return nil
	}
	if err := p.mod.Close(ctx); err != nil {
		return fmt.Errorf("close parser instance: %w", err)
	}
	return nil
}

// Parse invokes the module's dispatcher for toolName over the tool's raw
// stdout/stderr bytes and exit code, returning the decoded diagnostics. Raw
// bytes are passed whole (never line-split here) so multiline parsers keep their
// input intact. The result fields are nullable per the RawDiagnostic contract.
func (p *ParserRuntime) Parse(
	ctx context.Context,
	toolName string,
	stdout, stderr []byte,
	exitCode int32,
) ([]RawDiagnostic, error) {
	toolPtr, toolLen, err := p.writeBuf(ctx, []byte(toolName))
	if err != nil {
		return nil, fmt.Errorf("write tool name: %w", err)
	}
	defer p.free(ctx, toolPtr, toolLen)

	outPtr, outLen, err := p.writeBuf(ctx, stdout)
	if err != nil {
		return nil, fmt.Errorf("write stdout: %w", err)
	}
	defer p.free(ctx, outPtr, outLen)

	errPtr, errLen, err := p.writeBuf(ctx, stderr)
	if err != nil {
		return nil, fmt.Errorf("write stderr: %w", err)
	}
	defer p.free(ctx, errPtr, errLen)

	res, err := p.parse.Call(ctx,
		uint64(toolPtr), uint64(toolLen),
		uint64(outPtr), uint64(outLen),
		uint64(errPtr), uint64(errLen),
		//nolint:gosec // G115: the module's `parse` takes exit_code as a wasm i32;
		// this is an intentional bit-reinterpretation of the host int32, not a
		// lossy widening — the module reads it back as i32.
		uint64(uint32(exitCode)),
	)
	if err != nil {
		return nil, fmt.Errorf("call parse: %w", err)
	}

	// parse returns (ptr << 32) | len of a freshly allocated output buffer.
	packed := res[0]
	resPtr := uint32(packed >> 32)
	resLen := uint32(packed & 0xffffffff)
	defer p.free(ctx, resPtr, resLen)

	buf, ok := p.mod.Memory().Read(resPtr, resLen)
	if !ok {
		return nil, fmt.Errorf("parse output out of range (ptr=%d len=%d)", resPtr, resLen)
	}
	// Memory().Read may return a view into linear memory; copy before decoding
	// so a later alloc cannot move the bytes out from under json.Unmarshal.
	out := make([]byte, len(buf))
	copy(out, buf)

	var diags []RawDiagnostic
	if err := json.Unmarshal(out, &diags); err != nil {
		return nil, fmt.Errorf("decode parser output: %w", err)
	}
	return diags, nil
}

// writeBuf allocates len(data) bytes in the module and writes data into them,
// returning the ptr/len. An empty buffer needs no allocation: the module's ABI
// treats a null ptr / zero len as the empty slice.
func (p *ParserRuntime) writeBuf(ctx context.Context, data []byte) (uint32, uint32, error) {
	if len(data) == 0 {
		return 0, 0, nil
	}
	if len(data) > math.MaxUint32 {
		return 0, 0, fmt.Errorf("buffer too large for wasm32 (%d bytes)", len(data))
	}
	n := uint32(len(data)) //nolint:gosec // G115: guarded by the MaxUint32 check above.
	res, err := p.alloc.Call(ctx, uint64(n))
	if err != nil {
		return 0, 0, fmt.Errorf("alloc(%d): %w", n, err)
	}
	//nolint:gosec // G115: wasm32 alloc returns a u32 linear-memory offset.
	ptr := uint32(res[0])
	if !p.mod.Memory().Write(ptr, data) {
		return 0, 0, fmt.Errorf("write out of range (ptr=%d len=%d)", ptr, n)
	}
	return ptr, n, nil
}

// free returns a buffer to the module. A zero ptr/len pair is the empty-slice
// sentinel writeBuf produces and is never allocated, so skip it.
func (p *ParserRuntime) free(ctx context.Context, ptr, length uint32) {
	if ptr == 0 || length == 0 {
		return
	}
	_, _ = p.dealloc.Call(ctx, uint64(ptr), uint64(length))
}
