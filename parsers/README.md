# datamitsu WASM parsers

Signed Rust→WASM modules that turn a third-party tool's raw text output into
structured, **nullable** diagnostics for the datamitsu Go core.

## Architectural invariant

A parser extracts **only what the tool actually emitted**. Every diagnostic field
but `message` is optional; `None` means "the tool did not provide this", not an
error. The Go core fills defaults (column, the level of a finding without one,
range completion) and computes diffs — the WASM module owns **only extraction**.
Do not invent data in a parser, and do not finalize the diagnostic shape here:
`RawDiagnostic` (`datamitsu-parsers/src/diagnostic.rs`) is a Phase-1 placeholder,
finalized in Phase 2.

Every parser also keeps the rules `src/contract.rs` checks for all of them: a
level only from a token the tool printed, the tool's name in `source` and the
rule in `code`, 1-based positions with an exclusive end, and a measured column
unit. The [output parser guide](../website/docs/guides/architecture/parsers.md)
explains each.

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
4. Host calls `reset()` before reusing the instance for another parse. A module
   that exports it declares it can be returned to its post-instantiation state;
   the host reuses **only** such instances, instantiating a fresh module per
   parse for any module that omits the export. Keep every parse's state
   reachable from `reset` — never in a global that survives it.

Raw bytes are delivered **whole — never host-line-split**. Line-splitting in the
host loses multiline cases (e.g. `cue_fmt`); the parser decides whether to split.

## Output form

`parse` returns the answer of response ABI 2: whether the parser recognized the
output, the format it read (a format key, or the tool name for a tool parser), and
the diagnostics. Each diagnostic always has `message`; every other field (`row`,
`col`, `end_row`, `end_col`, `severity`, `source`, `code`, `url`, `file`) is present
only if the tool emitted it. An unknown key recognizes nothing.

```json
{
  "recognized": true,
  "format": "hadolint",
  "diagnostics": [{ "message": "missing newline", "row": 12, "col": 1, "code": "DL3000" }]
}
```

`describe` (schema 3) says `"abi": 2` and names the build's `features`.

## Two builds

The crate has two features: `tools` (one parser per tool) and `format` (the
standard-format parsers under `src/format/` and the sniffer in `src/fallback.rs`).
The public module is the default, both; the fallback the core embeds is
`--no-default-features --features format`. `cargo test` runs either build.

## Adding a parser

Each tool is one module under `datamitsu-parsers/src/tools/`. To add one:

1. Add `src/tools/<tool>.rs` with its `DESCRIPTOR` — the level vocabulary in
   `severities`, `column_unit` (or an entry on `UNKNOWN_COLUMN_UNITS`),
   `category` and `kind` — and
   `pub fn parse(stdout: &[u8], stderr: &[u8], exit_code: i32) -> Vec<RawDiagnostic>`,
   with `cargo test` cases beside it and its `SAMPLES`.
2. Register it: `pub mod <tool>;`, a dispatch arm and a `samples` entry in
   `src/tools/mod.rs`, its descriptor in `TOOLS` in `src/capabilities.rs`, and its
   row in `POSITIONS` in `src/contract.rs`. The core checks a configuration's
   parser key against `describe` before it parses, so a parser missing from `TOOLS`
   is treated as unknown even though it dispatches.
3. When a configuration wires the parser, record a clean and a finding-bearing run
   of the real tool under `datamitsu-parsers/fixtures/<tool>/` and assert them in
   `src/tools/fixtures.rs` ([fixtures/README.md](datamitsu-parsers/fixtures/README.md)).

A format parser is one module under `src/format/`, with its `DESCRIPTOR` of kind
`format`, a `parse` returning a `Response`, its `SAMPLES`, an entry in
`format::PARSERS` (which also sets the sniffer's order), in `format::DESCRIPTORS`
and `format::samples`, and rows in `FORMAT_POSITIONS` and
`FORMAT_UNKNOWN_COLUMN_UNITS` in `src/contract.rs`.

## Build & test

```bash
# Native unit tests (no wasm toolchain needed)
cargo test --manifest-path parsers/Cargo.toml

# Build the WASM artifact and report its size
task build:parsers
# -> parsers/target/wasm32-unknown-unknown/release/datamitsu_parsers.wasm

# Build it into the fixture the core's tests run (after any change here)
task build:parsers:fixture
# -> internal/parsermanager/testdata/echo.wasm
```

`rust-toolchain.toml` pins the Rust release. rustup reads it from the directory
cargo runs in, so run cargo from `parsers/` (the tasks do) to build with it;
`embedded.lock` and `embedded.Dockerfile` name the same release for the
container build.

The release profile (workspace `parsers/Cargo.toml`) uses `opt-level = "s"`, LTO,
strip, `codegen-units = 1`, and `panic = "abort"` to minimize artifact size.
