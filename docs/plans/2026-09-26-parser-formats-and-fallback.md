# Plan 9: Parser formats and fallback — format parsers by name, an embedded sniffer, ABI v2

**Status:** ready for implementation. Plan 9 of `2026-09-26-unified-results.md`; implements D8
(a–f), the fallback and compatibility side of R12, and module release 2. No decisions open.
**Date:** 2026-09-26.
**Depends on:** plan 3 (`ParseFailed`, extraction outcomes, C1 for every provenance, task
identity, the path normalization function), plan 5 (the descriptor fields, the additive-ABI rule,
the released-module test bed). The build infrastructure (§2.5) has no dependency and can start
first.
**Unblocks:** nothing by data; it widens what plans 6–8 report (tools without a tool parser).
**Related:** `parsers/datamitsu-parsers` (`Cargo.toml`, `src/lib.rs`, `src/capabilities.rs`,
`src/tools/mod.rs`, `src/tools/json_diag.rs`, `src/tools/checkstyle.rs`, `src/tools/gccdiag.rs`,
`src/location.rs`), `internal/parsermanager` (`parsermanager.go`, `runtime.go`,
`capabilities.go`), `internal/tooling/executor.go` (`parseFileDiagnostics`, `runCommandIO`),
`internal/cache/cache.go` (`calculateInvalidationKey`), `internal/tooling/verdict.go`,
`internal/diagnostic`, `internal/runtimeconfig`, `internal/env`, `internal/hashutil`,
`cmd/devtools_parsers.go`, `Taskfile.yaml`, `.github/workflows/pr-checks.yml`,
`.github/workflows/release.yml`, `website/docs/guides/architecture/parsers.md`,
`config/src/prompts/datamitsu-config-author-guide.md`.

> **Why this exists.** More than twenty of the wrapper's active lint operations have no parser,
> so their findings reach no report. Many of those tools can print a standard format on request
> (`--format sarif`, `-f checkstyle`, `--output-format github`), and a tool that switched its
> format because of an environment variable prints one of the same handful of shapes. One
> implementation of each shape, callable by name from the public module and embedded in the
> binary as a sniffing fallback, covers both cases without a second parser implementation in Go.
> The embedded module has to be reproducible, committed, and guarded, or it is a blob nobody can
> audit.

---

## 0. The idea in one paragraph

The Rust crate gains a `format` module (SARIF, CodeClimate, ESLint JSON, none-ls JSON, GitHub
workflow commands, Azure `##vso`, gcc, MSVC, Checkstyle XML, JUnit XML) and a `fallback` sniffer
that tries them in order and returns the first recognized result with the format's name. The
public module exports them as parser keys (`sarif`, `checkstyle-xml`, `gcc`, …) so a config can
declare `outputParser: { module: "core", parser: "sarif" }` — no schema change. The response ABI
becomes v2: an object with `recognized` and `format`; the core learns to read v1 and v2 **before**
any module changes. A format-only build of the same crate is committed and embedded with
`go:embed`; the core calls it when a tool has no parser, the declared parser is unknown, failed,
or answered `recognized: false`, and records what it could and could not extract. The embedded
module is built in a `linux/amd64` container from a digest-pinned image, checked byte for byte
in CI, and stale-checked by a Go test against a committed source hash.

---

## 1. Grounding — verified facts

- `tools::dispatch` matches ~95 tool names (`src/tools/mod.rs`); an unknown name returns `[]`
  (`lib.rs:144-155`). `gccdiag` is a dispatch key already (`mod.rs:143`). `checkstyle.rs` parses
  SARIF (`checkstyle -f sarif`), `gccdiag.rs` the gcc shape, `json_diag.rs` extracts a JSON
  document from noise (`extract_lenient`), `location.rs` splits `file:row:col`. The only
  dependency is `tinyjson` (`Cargo.toml`); no XML parser.
- The ABI is `alloc`/`dealloc`/`parse`/`describe`/`reset`; `parse` returns a JSON array of
  nullable diagnostics; the Go decoder is `json.Unmarshal` into `[]RawDiagnostic`
  (`runtime.go:227`); every runtime test consumes `testdata/echo.wasm`.
- `runCommandIO` (`executor.go:1176-1192`) keeps the streams apart in parse mode and never
  parses a stdout-mode formatter's stdout: that stream is file content (`formatMode`).
- The release profile is `opt-level = "s"`, `lto`, `strip`, `panic = "abort"`,
  `codegen-units = 1` (`parsers/Cargo.toml`). `task build:parsers` builds into Cargo's target
  directory and regenerates the catalogue page; it does not copy any fixture (`Taskfile.yaml:28`).
- CI builds the module with the runner image's preinstalled `rustup` and adds only the
  `wasm32-unknown-unknown` target (`pr-checks.yml:16-19`); `DATAMITSU_PARSERS_VERSION` is baked
  per PR (`:32-36`). There is no `rust-toolchain.toml` and no `.gitattributes`.
- **Reproducibility, measured on 2026-09-26** (rustc 1.98.0, Linux x86_64): two builds from
  different directories are byte-identical without flags; `DATAMITSU_PARSERS_VERSION` changes
  the bytes; without remapping the module carries six machine-specific strings
  (`~/.cargo/registry/…/tinyjson-2.5.1/src/parser.rs` and five `~/.rustup/toolchains/<tc>/lib/
rustlib/src/rust/library/…` paths that appear only when `rust-src` is installed); with
  `--remap-path-prefix` for cargo home, the sysroot source tree (mapped to `/rustc/<commit>`)
  and the workspace, two directories give the same bytes. `tinyjson` has no build script and no
  proc-macro. The empty-module floor is 91 848 bytes; the full module is 403 292 bytes.
- `parsermanager.Manager` compiles a module once per content key, pools instances of modules that
  export `reset`, and keys the store by the module's SHA-256. The per-file invalidation key
  hashes `ldflags.Version` (`cache.go:608`), which two development builds share; the verdict
  identity (`verdict.go:42`) carries the declared module's hash from plan 3 and nothing about an
  embedded module.
- Plan 3's path normalization resolves a reported path against the invocation directory and
  keeps a cleaned absolute path outside the root (index §2.4).

---

## 2. Design

### 2.1 Format parsers in the crate (`src/format/`)

| Key                  | Recognizes / parses                                                                | Notes                                                                                              |
| -------------------- | ---------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `sarif`              | a JSON object with `version` and `runs[]`                                          | rules, `helpUri` → `url`, `level`, fingerprints ignored (ours are computed), paths as given        |
| `codeclimate`        | a JSON array whose objects carry `check_name` and `location.path`                  | severity words → 1–4; `fingerprint` ignored                                                        |
| `eslint-json`        | a JSON array whose objects carry `filePath` and `messages[]`                       | shared with `tools/eslint.rs`                                                                      |
| `json`               | a JSON array whose objects carry `message` and `line` (none-ls defaults)           | `json_diag` defaults                                                                               |
| `github-annotations` | lines `^\s*::(error\|warning\|notice)( [^:]*)?::`                                  | `file`, `line`, `col`, `endLine`, `endColumn`, `title` → `code`                                    |
| `azure-logissue`     | lines `##vso[task.logissue …]`                                                     | `type`, `sourcepath`, `linenumber`, `columnnumber`, `code`                                         |
| `gcc`                | `^(path):(\d+):(\d+): (error\|warning\|note\|info)?:? (.*)$`, and `path:line: msg` | the implementation behind `gccdiag`; both keys dispatch to it; trailing `[code]`/`(code)` → `code` |
| `msvc`               | `^(path)\((\d+),(\d+)\): (error\|warning) (\w+): (.*)$`                            | tsc/MSVC shape                                                                                     |
| `checkstyle-xml`     | a document whose root is `<checkstyle>`                                            | hand-written tokenizer (§2.2)                                                                      |
| `junit-xml`          | a document whose root is `<testsuites>` or `<testsuite>`                           | one finding per `failure`/`error`: `code` = testcase name, `source` = classname, kind `issue`      |

Every format parser is a `fn parse(stdout, stderr, exit_code) -> Response` like a tool parser,
listed in `capabilities::TOOLS` with `kind: "format"` and a description that names the tool flag
that produces the shape (`ruff --output-format sarif`, `shellcheck -f checkstyle`, `typos --format
brief`, …). The existing `checkstyle` key (the Java tool) stays; `checkstyle-xml` is the format.
JUnit input yields ordinary findings: the model's kinds stay `issue|security|synthetic` (a test
class is not introduced). ANSI sequences are stripped by the core before any module call (plan
4); the line parsers strip again defensively.

### 2.2 XML without a dependency (D8d)

`src/format/xml.rs`: a tokenizer for the two flat shapes — start tags with attributes, end tags,
self-closing tags, text, CDATA, and the five predefined entities plus numeric references; no
namespaces, no DTD, no processing beyond skipping. About 150–200 lines with tests. Because it is
hand-written, the crate's `tinyjson`-only dependency rule needs no exception. Fixtures:
`shellcheck -f checkstyle`, `hadolint -f checkstyle`, `tflint -f checkstyle`, `oxlint --format
checkstyle`, `phpcs --report=checkstyle`, `golangci-lint --out-format junit-xml`.

### 2.3 The sniffer (`fallback`)

`src/fallback.rs` tries, in order: `sarif`, `codeclimate`, `eslint-json`, `json`, then
`checkstyle-xml`, `junit-xml`, then the line formats `github-annotations`, `azure-logissue`,
`msvc`, `gcc`. A **structured** format is recognized by its envelope, whatever its finding count:
a SARIF document with empty `results`, an ESLint array whose files all have empty `messages`, a
`<checkstyle/>` with no files are recognized and clean. A bare `[]` or `{}` is not recognized
(no envelope). A line format is recognized when at least one line matches. The first recognized
format wins; the response names it. A parser key `fallback` dispatches to it; the public module
exports it too, so `devtools parsers run fallback` works with any build.

### 2.4 ABI v2 (`recognized`, `format`)

`parse` returns either the v1 array or a v2 object:

```json
{ "recognized": true, "format": "sarif", "diagnostics": [ … ] }
```

- `recognized: false` means the parser found nothing it understands (no envelope, no matching
  line) — distinct from "understood the format and found nothing" (`recognized: true`,
  `diagnostics: []`). This supersedes the LSP plan's Stage 3 "report found, empty" item.
- `format` is the format key for format parsers and the sniffer, the tool name for tool parsers.
- `describe` schema 3 adds `abi: 2`. The Go decoder accepts an array (v1) or an object (v2);
  unknown fields are ignored; a `schemaVersion` above the newest known is read as the newest
  known (forward compatibility, R12).
- Every tool parser is migrated to v2 with `recognized` computed by its own criterion (a JSON
  parser: an envelope was found; a line parser: at least one line matched, or the output was
  empty on exit 0).

### 2.5 Two build targets and the reproducible embedded build (D8a, D8c, D8f)

- Cargo features `tools` (every tool parser) and `format` (format parsers, the sniffer, the
  `fallback` key). The public module is `tools,format`; the embedded module is `format` only
  (estimated ~125 KiB from the measured floor). `describe` reports the feature set.
- **One source of the toolchain version:** `parsers/rust-toolchain.toml` pins the exact `rustc`
  and the `wasm32-unknown-unknown` target; `pr-checks.yml` and `release.yml` install through
  `rustup` from it. `parsers/embedded.Dockerfile` takes `ARG RUST_VERSION` and is `FROM
rust:${RUST_VERSION}-slim@sha256:<digest>`; `parsers/embedded.lock` holds the version and the
  digest on two lines and is the only other place the version appears. The gate (below) reads
  the toolchain file, passes the version as the build arg, and asserts `rustc -vV` **inside** the
  container names that version, so a digest of a different rustc cannot pass.
- `task build:parsers:embedded`:
  1. `docker build --platform linux/amd64 --build-arg RUST_VERSION=<from toolchain file> -t
datamitsu-parsers-embedded -f parsers/embedded.Dockerfile parsers`;
  2. `docker run --rm --platform linux/amd64 --user "$(id -u):$(id -g)" -e CARGO_HOME=/cargo -e
CARGO_TARGET_DIR=/src/target-embedded -e CARGO_INCREMENTAL=0 -v "$PWD/parsers:/src" -v
datamitsu-cargo-home:/cargo -w /src datamitsu-parsers-embedded cargo build --release
--locked --target wasm32-unknown-unknown --no-default-features --features format` with the
     three `--remap-path-prefix` flags of §1 as hygiene and no `DATAMITSU_PARSERS_VERSION` —
     a separate target directory so native and container artifacts never share one, the
     caller's uid so nothing root-owned lands in the checkout, a named volume as a writable cargo
     home;
  3. copy the result to `internal/parsermanager/embedded/fallback.wasm`;
  4. `go run ./internal/parsermanager/embedded/cmd/sourcehash` writes
     `internal/parsermanager/embedded/fallback.wasm.sources` (below).
     `task build:parsers` keeps building the public module and gains an explicit `task
build:parsers:fixture` that copies it to `internal/parsermanager/testdata/echo.wasm`.
- **CI gate** (`pr-checks.yml`, the `llmsdocs/embed` pattern, a required status check on
  `main`): the same container build, the `rustc -vV` assertion, `cmp` with the committed file;
  on mismatch upload the built module as the workflow artifact `embedded-fallback-wasm` and fail
  with `::error::the embedded fallback module is stale; download the artifact of this run and
commit it as internal/parsermanager/embedded/fallback.wasm, or run task
build:parsers:embedded`. A contributor never needs Docker or Rust to make the gate green.
- **Source hash** (the second freshness check of D8f, without a build script): the files listed
  in `parsers/datamitsu-parsers/embedded-sources.txt` (the `format` feature's sources,
  `Cargo.toml`, `Cargo.lock`, `rust-toolchain.toml`, `embedded.lock`, `embedded.Dockerfile`) are
  hashed by Go — sorted repository-relative paths, each contributing `path NUL length NUL bytes
NUL` with CRLF folded to LF — with XXH3-128 through `internal/hashutil` (a local staleness
  fingerprint, never compared with an external value: Hashing Policy). The build task commits the
  hex in `fallback.wasm.sources`; a Go test in `internal/parsermanager/embedded` recomputes it
  over the working tree and fails with "embedded module is stale: run task
  build:parsers:embedded" — no Rust needed to notice a stale blob, and a tool-parser edit does
  not demand a rebuild. `.gitattributes` gives the listed sources `eol=lf`.
- `.gitattributes`: `*.wasm binary linguist-generated=true` (from plan 1 if not already there).
- The embedded module is versioned with the binary: its `describe` version is the crate version;
  `ldflags.Version` is what a user sees.

### 2.6 The embedded module in the core (D8b)

- `internal/parsermanager/embedded`: `//go:embed fallback.wasm`; `Manager` gains a registered
  module under the reserved name `embedded` (a load error when a config declares a module of
  that name) with a content key from the XXH3 of the embedded bytes computed once at init; it is
  compiled once, prewarmed with the declared modules, pooled and reset like any other. It is not
  a `parsers` entry, not listed by `devtools parsers list`, not accepted in `outputParser.module`.
  `devtools parsers run <key> --embedded` runs it; `devtools parsers list --embedded` describes
  it; a new leaf command `devtools parsers sniff [<file>|-]` prints which format the sniffer picks
  for a captured output and the findings, for authors checking a tool's flag.
- **Identity.** That XXH3 is folded into `calculateInvalidationKey` (per-file passes) and into
  `verdictIdentity` (unit passes): `ldflags.Version` alone is `dev` for every development build,
  and a changed fallback must not replay a pass recorded by another build.
- **Caps** are runtime configuration, not local constants (Introspectable by Design):
  `runtimeconfig.MaxParseInputBytes = 8 MiB` per stream per process and
  `runtimeconfig.MaxFindingsPerProcess = 10 000`, with `DATAMITSU_MAX_PARSE_INPUT_BYTES` and
  `DATAMITSU_MAX_FINDINGS_PER_PROCESS` through `internal/env`, typed `Effective` fields, and the
  usual tests; exceeding either records the `truncated` outcome. They change what datamitsu
  produces, so they are ordinary fingerprint inputs, not `environExcluded`.
- **Orchestration** in `parseFileDiagnostics`. A stdout-mode formatter's stdout is file content
  and never reaches any parser; only its stderr is offered to the fallback. For everything else,
  in order:
  1. a declared `outputParser` → `parse(<parser>)` on the declared module; an unknown key per
     `describe` is `parser-unavailable`, blocks the pass, warns once per run, and still falls
     through to step 3 so the findings are not lost — the outcome stays `parser-unavailable`
     whatever the fallback returns;
  2. v2 `recognized: true` (or v1 non-empty, or v1 empty with exit 0) → done, provenance
     `parser` (or `format` when the key is a format parser), outcome `parsed-*`;
  3. otherwise (no declaration, parse error, `recognized: false`, v1 empty with exit ≠ 0) →
     `parse("fallback")` on the **embedded** module:
     - recognized → provenance `fallback:<format>`, outcome `parsed-*`; when a declared parser
       had failed, one `warn` line per run names the tool ("declared parser X did not recognize
       the output; parsed as sarif by the fallback");
     - not recognized and a parser **was** declared → `parse-failed` (blocks the pass, tool
       incomplete, plan 6's `parse-failed` reason);
     - not recognized and none was declared → `none` (the exit-status rule; `no-extraction` for
       completeness); with a failed process the synthetic finding of plan 6 stands in.
       The declared module's own `fallback` key is never used: the fallback's version is the
       binary's, whichever module a config pins.
- **Paths.** Every diagnostic of every provenance goes through plan 3's one normalization
  function (against the invocation directory; a cleaned absolute path outside the root is kept).
  For `fallback:*` results of the **line** formats there is one extra confidence rule: a matched
  line whose path does not exist on disk (relative to the invocation directory, or absolute)
  does not count as a match, so prose that looks like `path:1:2:` is not a finding. Structured
  formats are not subject to it.
- **Cache (D7, plan 3):** eligibility follows the extraction outcome as decided there; the rules
  above only say which outcome each branch records.

### 2.7 Compatibility (R12)

- The core reads ABI v1 and v2 and descriptor schemas 1–3; a declared module always parses its
  own tools; the fallback runs on the triggers of §2.6 only (the index's R12 list gains "v1
  empty with exit ≠ 0"), never because a module is old.
- Three contract scenarios in `internal/parsermanager`: the released v1 module of plan 1 (parses
  its tools, `describe` schema 1, no `fallback` key → the core uses the embedded one), the current
  module (`echo.wasm`, v2), and a synthetic module with `schemaVersion: 99` and an extra response
  field (read as the newest known; the field ignored).
- An old module (schema < 3) is reported once in `datamitsu config show`, `devtools parsers list`
  and with `-v` — never on a plain run (the user cannot fix it; the wrapper pins the hash).
- Lifetime of v1: supported while the latest wrapper release still pins it, and at least two
  minor releases after the wrapper moves, mirroring the author guide's rule.

### 2.8 Documentation

Each PR ships the documentation of what it adds (index §4.3):

- `guides/architecture/parsers.md`: the three layers (declared parser, format parsers by name,
  embedded fallback); the note "a tool's own JSON is usually richer than a standard format and
  is parsed without an intermediate conversion; prefer a tool parser when one exists, a format
  parser when the tool emits a standard shape, and know that line formats carry only file, line,
  column, level and text"; the reproducible-build procedure; the sentence "core embeds no
  per-version WASM hash" rewritten: the public module updates independently; the embedded
  fallback is part of the binary and not a distribution channel.
- `reference/parser-catalog.md` gains the format-parsers table (from `describe`).
- `configuration-api.md`: `outputParser.parser` accepts format keys, with the flag examples;
  `embedded` is a reserved module name.
- `cli-commands.md`: `devtools parsers sniff`, the `--embedded` flags, the two caps and their
  variables.
- The config-author guide, "Recent changes": format keys as a new authoring capability, the
  reserved name, the second module release. `CLAUDE.md`: the parsers policy paragraph (embedded
  module: part of the binary, not a distribution channel; committed only with the CI byte gate;
  the `tinyjson`-only rule holds because the XML tokenizer is hand-written) and a "Product
  Stage" entry for the reserved module name.

---

## 3. Work breakdown

**PR 1 — build infrastructure.** §2.5 without the embedded artifact: `rust-toolchain.toml`,
Dockerfile with `ARG`, `embedded.lock`, the task, the CI toolchain installation, `task
build:parsers:fixture`, `.gitattributes` if missing. No behaviour change.

**PR 2 — the core reads v1 and v2 (Go only).** The array-or-object decoder in `runtime.go`,
`Response{Recognized, Format, Diagnostics}`, descriptor schema 3 (`abi`) with the forward rule,
the caps as runtime configuration (§2.6) with their tests and documentation. The released module
and the current `echo.wasm` (still v1) both pass; a hand-written v2 fixture decodes.

**PR 3 — formats, ABI v2, features (Rust).** §2.1–§2.4 with the crate tests (fixtures from the
owner's survey of tool outputs; negative cases: a `::` inside a JSON string, a path with a colon,
a Windows path, ANSI, a truncated document, `{}` and `[]` in noise; clean, malformed and truncated
fixtures for every structured format), the feature split, the rebuilt `echo.wasm` (v2), the
catalogue page, the author-guide entry for format keys.

**PR 4 — the embedded artifact and its gates.** The first committed `fallback.wasm`, the CI byte
gate (a required check), `embedded-sources.txt`, the `sourcehash` generator and its Go test,
`internal/parsermanager/embedded` registration, the identity in both cache keys, `devtools parsers
… --embedded`, `devtools parsers sniff` with its blackbox test.

**PR 5 — orchestration.** §2.6 in `parseFileDiagnostics`, provenance, outcomes, the formatter
exclusion, the path confidence rule; blackbox: a shell tool printing SARIF with no parser
declared yields findings with `fallback:sarif`; a declared parser answering `recognized: false`
falls back with the warning; a declared parser and no recognizable output → `parse-failed` and
the tool incomplete in `--report json=`; no parser and no recognizable output with exit 1 → the
synthetic finding; a stdout-mode formatter whose formatted output contains `x.go:1:2: error: y`
creates no finding; `--no-parse` shows raw; the documentation of the three layers.

**PR 6 — compatibility and the rest of the documentation.** §2.7 scenarios, the old-module
report, `CLAUDE.md`, `task gen:llms-docs`.

Module release 2 (public module with formats and v2) ships with the core release after PR 6;
the wrapper bumps the hash at checkpoint R3 (or earlier, with R2).

---

## 4. Verification

- Crate: every format parser has positive and negative fixtures; the sniffer picks the expected
  format for each fixture, recognizes a clean SARIF/ESLint/Checkstyle document as recognized and
  empty, and answers `recognized: false` for prose, `[]` and `{}`; both feature builds pass the
  same tests; the XML tokenizer handles entities, CDATA and attributes with quotes.
- Reproducibility: the container build run twice on one machine and once in CI gives one
  SHA-256; the CI gate fails on a deliberately stale blob and uploads the artifact; the gate
  fails when the Dockerfile digest names another rustc than the toolchain file.
- Source hash: editing a `format` source without rebuilding fails the Go test; editing a tool
  parser does not; a CRLF checkout of a listed source gives the same hash.
- Core: the three contract scenarios; the orchestration cases of PR 5; the embedded module is
  absent from `devtools parsers list` without `--embedded`, rejected as `outputParser.module`,
  and a config declaring a module named `embedded` fails to load; two binaries with the same
  `ldflags.Version` and different embedded bytes do not share per-file or unit passes;
  `datamitsu config runtime | jq .maxParseInputBytes`.
- End to end: ruff (`--output-format sarif` declared as `parser: "sarif"`), shellcheck (`-f
checkstyle` as `checkstyle-xml`), typos (`--format brief` as `gcc`) parse in a gated e2e run.

---

## 5. Risks

- **Container build on Apple Silicon** runs under emulation; slow but rare. Contributors without
  Docker use the CI artifact.
- **Rust upgrades** touch the toolchain file, the lock and the blob in one PR; the gate keeps
  them aligned.
- **Sniffer false positives** on prose that looks like `path:1:2:`: the on-disk confidence rule
  and the `fallback:<format>` provenance keep them rare and attributable; D7 keeps them from
  hiding under a cache pass.
- **Two module releases** (plan 5 and this plan) cost two wrapper bumps; accepted (index R14).

---

## 6. Non-goals

- No new tool parsers (LSP plan Stage 3), no column-unit measurements beyond plan 5's.
- No Go implementation of any format parser.
- No `xmlparser`/`roxmltree` or other crates; no build script in the crate.
- No use of a declared module's `fallback` key by the core.
- No Git LFS.
