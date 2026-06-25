# datamitsu WASM parsers

Signed Rust→WASM modules that turn a third-party tool's raw text output into
structured, **nullable** diagnostics for the datamitsu Go core.

## Architectural invariant

A parser extracts **only what the tool actually emitted**. Every diagnostic field
but `message` is optional; `None` means "the tool did not provide this", not an
error. The Go core fills defaults (column, severity fallback, range completion)
and computes diffs — the WASM module owns **only extraction**. Do not invent data
in a parser, and do not finalize the diagnostic shape here: `RawDiagnostic`
(`datamitsu-parsers/src/diagnostic.rs`) is a Phase-1 placeholder, finalized in
Phase 2.

## Call contract (host ABI)

The host (Go core, via wazero) drives a small manual-memory ABI — there is no
`wasm-bindgen`, which keeps the artifact in the ~15-30KB range.

1. Host calls `alloc(len) -> ptr` for each input buffer (tool name, stdout,
   stderr) and writes the bytes.
2. Host calls
   `parse(tool_ptr, tool_len, stdout_ptr, stdout_len, stderr_ptr, stderr_len, exit_code) -> u64`.
   The result packs `(ptr << 32) | len` of a freshly allocated UTF-8 JSON buffer.
3. Host reads the JSON, then calls `dealloc(ptr, len)` on the output buffer (and
   on each input buffer) to free it.

Raw bytes are delivered **whole — never host-line-split**. Line-splitting in the
host loses multiline cases (e.g. `cue_fmt`); the parser decides whether to split.

## Output form

`parse` returns a JSON array of diagnostics. Each object always has `message`;
every other field (`row`, `col`, `end_row`, `end_col`, `severity`, `source`,
`code`) is present only if the tool emitted it. An unknown tool name returns `[]`.

```json
[{ "message": "missing newline", "row": 12, "col": 1, "code": "DL3000" }]
```

## Adding a parser (Phase 2 onward)

The dispatcher in `datamitsu-parsers/src/lib.rs` is a single `match` on the tool
name. To add a tool:

1. Add a module function `fn <tool>(stdout: &[u8], stderr: &[u8], exit_code: i32) -> String`
   that parses the raw bytes and returns `diagnostic::to_json_array(&diags)`.
2. Add one `match` arm: `"<tool>" => <tool>(stdout, stderr, exit_code),`.
3. Add `cargo test` cases for the new branch.

That is the whole mechanism — hadolint, yamllint, dotenv_linter, and cue_fmt all
plug in this way.

## Build & test

```bash
# Native unit tests (no wasm toolchain needed)
cargo test --manifest-path parsers/Cargo.toml

# Build the WASM artifact and report its size
task build:parsers
# -> parsers/target/wasm32-unknown-unknown/release/datamitsu_parsers.wasm
```

The release profile (workspace `parsers/Cargo.toml`) uses `opt-level = "s"`, LTO,
strip, `codegen-units = 1`, and `panic = "abort"` to minimize artifact size.
