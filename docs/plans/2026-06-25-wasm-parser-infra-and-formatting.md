# Plan: WASM parser infrastructure + formatting (LSP mode, Phase 1)

**Status:** ready for implementation. Date: 2026-06-25.
**Source:** `revise.txt` (owner's Phase-1 design) + `analysis.md` (none-ls/efm extraction).
**Scope owner decisions (2026-06-25):** unit tests = _regular_ (code, then tests);
lsp `order` ties = _reuse the actual existing mechanism_ (alphabetical by name);
filename as above.

> **Phase boundary (hard).** This phase builds the _parser plumbing_ (declare → build →
> sign → deliver → load) plus _formatting_ as the first consumer. The architectural
> invariant that governs everything: **a WASM parser extracts only what the tool actually
> emitted (nullable fields, `None` = "not provided"); the Go core fills defaults.** The
> "raw-with-holes / defaults-in-core" boundary is held throughout.
>
> **NOT in this phase (Phase 2+, separate plans):** finalizing the `Diagnostic` struct,
> real diagnostic parsers (hadolint/yamllint/dotenv*linter/cue_fmt), pretty diagnostic
> output, a debug flag, and any `lsp` \_behavior*. Formatting does not need the Diagnostic
> contract (`analysis.md` §4: formatter is stdin→stdout→diff, no fields). Do **not** design
> the Diagnostic struct here — only lay out that parser-output fields are nullable.

## Overview

Add the ability to wrap third-party linters/formatters behind datamitsu and parse their
text output into structured results via signed Rust→WASM modules, loaded into the Go core
through a sandboxed runtime. Phase 1 proves the whole pipe end-to-end with a trivial
`echo` parser, and delivers _formatting_ (which needs no parser at all) as the first real,
shippable consumer.

End-to-end Phase-1 slice (the completion criterion):

> echo WASM module declared in config as a `parsers` entity → built in CI → signed by the
> existing goreleaser/cosign flow → SHA-256 in `checksums.txt` → delivered via GitHub
> Releases + npm wrapper → downloaded by the core with hash (+ signature-covered) check →
> loaded into wazero → dispatcher invoked → fixed echo result returned.
> **And independently:** formatting via the existing fix path + a new line-based diff in the
> core works in the CLI.

## Context (from discovery)

Grounded against the actual code on 2026-06-25. **Four assumptions in `revise.txt` did not
match reality and are corrected below.**

### Config schema (Step 1)

- TS types: `config/config.d.ts` — `Config` (`:289-376`), `Bundle` (`:1025-1048`),
  `ArchiveSpec` (url+hash artifact, `:971-993`), `App` (`:795-870`), `Tool` (`:637-670`),
  `ToolOperation` (`:675-779`). The embedded copy `internal/config/config.d.ts` is **generated**
  by `task build:lib` (`cp ./config/config.d.ts ./internal/config/config.d.ts`) — edit only
  the source, never the copy.
- Go structs: `internal/config/config.go` — `Config` (`:260-275`), `Tool` (`:75-86`),
  `ToolOperation` (`:61-72`), `OperationType` `OpFix`/`OpLint` (`:51-58`). Artifact structs
  live in `internal/binmanager`: `App` (`binmanager.go:97-128`), `ArchiveSpec`
  (`binmanager.go:132-137`), `Bundle` (`bundle.go:16-21`). `parsers` mirrors the **url+hash
  ArchiveSpec/Bundle shape**, _not_ App (no runtime/lockfile — it is data, not a process).
- Validation chain: `internal/config/validate.go` (e.g. `ValidateBundles`, `ValidateTools`)
  wired in `cmd/config_loader.go` (`:171-204`).
- ⚠ **Correction 1 — `ToolOperation.app` is NOT validated at config-load today** (only at
  runtime planning via `platformChecker.BinaryAvailable`, `planner.go:212-222`). So
  `outputParser → parsers` will be a **new load-time validator** in `validate.go` (mirror the
  way runtime/bundle refs are validated), not a mirror of an existing app-ref check.
- ⚠ **Correction 2 — there is NO existing "empty operations" validation** (tools missing the
  requested op are silently skipped, `planner.go:184-195`). Step 1.4's "tool must have ≥1
  facet" check is **brand new**, not a mirror.
- ⚠ **Correction 3 — equal-priority tie-break is ALPHABETICAL by name** (`planner.go:178-182`
  `sort.Strings(toolNames)`, then `sort.Ints` on priority `:437-468`), _not_ definition order
  as `revise.txt` assumed. Per owner decision we **reuse this actual mechanism** for lsp
  `order` ties.

### Artifact download / store (Step 3)

- Reuse `internal/binmanager`: download+retry (`download.go:178`), SHA-256 verify
  (`hash.go:47-105`, sha256 at `:61-66`, mismatch error `"hash mismatch: expected %s, got %s"`
  `:100-102`), SHA-256-only policy (`hashtype.go:19-21` `IsAllowedDownloadHashType`),
  content-addressed cache key via XXH3 (`hash.go:24-43` `calculateConfigHash` →
  `hashutil.XXH3Multi`), atomic `moveFile` (`download.go:228-263`), download coalescing via
  `singleflight.Group` (`binmanager.go:166`, `:381-393`). Store root: `env.GetStorePath()`
  (`env/env.go:42`) → `{base}/store`; `.bin`/`.bundles` subtrees today.
- ⚠ **Correction (Step 2/5) — NOT turborepo.** The monorepo is **pnpm workspaces +
  `Taskfile.yaml`**; `pnpm-workspace.yaml` packages = `packaging/action`, `programmable-api/js`,
  `website`. `turbo` is not a dependency. `wazero` is **not** in `go.mod` yet. No Rust code or
  CI toolchain exists.

### Execution / formatting (Step 4)

- Runner: `internal/tooling/executor.go`. Input is passed via **`{file}`/`{files}`/`{root}`/
  `{cwd}`/`{toolCache}` placeholder substitution** (`:1028-1072`) — there is **no stdin
  support and no temp-file/`${INPUT}` mode**. Output capture combines stdout+stderr into one
  buffer (`runCommandWithOutput` `:1231-1244`); fix tools **mutate files in place** (the core
  does not read stdout-as-content).
- ⚠ **Correction 4 — there is NO diff anywhere in the Go core** (grep for diff/myers/
  ComputeEdits/TextEdit = none). So Step 4 is genuinely net-new: add a _stdin / separated-
  stdout-capture_ input mode + a _line-based Myers diff written from scratch_ + apply. The
  design (diff-in-core, `analysis.md` §4.2) stands; it is just new code, not reuse.

### CI / release (Step 5)

- `.goreleaser.yml`: builds (`:31-54`), `checksum` → `checksums.txt`, sha256 (`:78-80`),
  `signs` → **cosign `sign-blob` on `artifacts: checksum` only** (`:159-167`, keyless/OIDC
  sigstore bundle). ✅ This _aligns with_ `revise.txt` §5.2: a WASM whose SHA-256 lands in
  `checksums.txt` is automatically covered by the existing signature — the core verifies the
  signature/trust-root, not an embedded per-version hash.
- Release pipeline: `.github/workflows/release.yml` (goreleaser-action, `normalize-binaries.sh`,
  `pack.ts`, `actions/attest` on checksums). npm wrapper pattern: `packaging/npm/datamitsu`
  (`get-exe.js`, `optionalDependencies` platform packages), orchestrated by `packaging/pack.ts`
  (`preparePlatformPackages`, `publishNpm`). New Rust crate location: top-level `parsers/`
  (parallel to `programmable-api/`).

### Dependencies identified

- New Go dep: `github.com/tetratelabs/wazero` (pure Go, no CGo — consistent with the core).
- New toolchain: Rust + `wasm32-unknown-unknown` target (freestanding cdylib, manual
  memory ABI — smallest artifact, ~15KB target per `revise.txt` §2.1).

## Development Approach

- **Testing approach: Regular** (implement, then write unit tests before the next task) —
  matches the existing stdlib table-driven / characterization-golden suite (`t.TempDir`,
  `t.Setenv`, no testify).
- Per-step **dog-fooding** on this very repo is a required gate (the `revise.txt` principle):
  a step is not closed until it has run against real datamitsu code, with intermediate logging.
- **Build requires the JS step** — `go install` does NOT work. After editing
  `config/config.d.ts` / the config TS, run `task build:lib` (regenerates
  `internal/config/config.js` + copies `config.d.ts`), then `go build` / `task build`. Never
  hand-edit the generated `internal/config/config.d.ts`.
- **Hashing policy:** downloaded WASM verified with **SHA-256** (mandatory hash on every
  `parsers` entry — empty/missing hash is a config _error_, not a warning); internal cache
  keys use **XXH3** via `internal/hashutil`.
- **Env access** goes through `internal/env` (add `GetParsersPath()` getter + `e.go`
  definition), never raw `os.Getenv` for datamitsu vars.
- **CRITICAL: every task includes new/updated tests; all tests pass before the next task.**
- **Update this plan when scope changes** (➕ new tasks, ⚠ blockers).

### Dog-fooding safety constraints (for the autonomous executor)

- Dog-fooding uses **read-only / config-parse commands and throwaway temp configs**. Do
  **NOT** run `dm setup` (it mass-rewrites managed configs) and do **NOT** destructively edit
  `datamitsu.config.ts`. If `init` is ever needed, use `pnpm exec datamitsu init` (a bare
  `./datamitsu init` breaks the `.datamitsu/` symlinks).
- Do **NOT** commit `revise.txt` or `analysis.md` (local scratch). This plan file _is_
  committable.
- Do **NOT** autonomously edit the separate `datamitsu-config` repo (declaring real parser
  entries there is Post-Completion / owner-gated).

## Testing Strategy

- **Unit tests** (required every task): Go via stdlib `testing` (table-driven, `t.TempDir`);
  Rust via `cargo test`; TS/packaging via the existing test setup.
- **CLI golden suite**: `go test ./test/cli/ -count=2` must remain byte-stable; any new
  surface (config entity, formatting path) that touches CLI output needs a golden update via
  `-update` and a blackbox test (`TestContractCompletenessGate` requires ≥1 test per leaf cmd).
- **No e2e UI tests** in this repo; the OCI e2e tier (`test/e2e`, `//go:build e2e_oci`) is not
  required by Phase 1.
- **Coverage**: keep the merged target (≈80%+) via `pnpm test:coverage:all`.

## Progress Tracking

- Mark items `[x]` immediately when done. ➕ new tasks, ⚠ blockers. Keep in sync with reality.

## What Goes Where

- **Implementation Steps** (`[ ]`): in-repo code, tests, config, CI YAML, docs.
- **Post-Completion** (no checkboxes): cutting a signed release tag and verifying live
  signatures; declaring real parser entries in the `datamitsu-config` repo; any owner-gated
  external action.

## Implementation Steps

### Task 1: Declare the `parsers` config entity (url+hash artifact)

- [x] Add a `Parser` interface `{ url: string; hash: string; version?: string }` and
      `parsers?: Record<string, Parser>` on `Config` in `config/config.d.ts`, modeled on
      `ArchiveSpec`/`Bundle` (url+hash data), explicitly NOT on `App`. JSDoc: SHA-256 hash
      mandatory per security policy.
- [x] Add Go `Parser` struct + `MapOfParsers` and a `Parsers MapOfParsers` field on `Config`
      in `internal/config/config.go` (json tags `url`/`hash`/`version`).
- [x] Add `ValidateParsers` in `internal/config/validate.go`: each parser must have a non-empty
      `url` and a non-empty, well-formed SHA-256 `hash` (64 lowercase hex) — empty hash → hard
      error (mirror the bundle/archive hash-mandatory rule). Wire it into the validator chain in
      `cmd/config_loader.go` (`:171-204`).
- [x] Run `task build:lib` so the generated `internal/config/config.js` + `config.d.ts` reflect
      the new type (do not hand-edit the generated copy).
- [x] Write tests (`internal/config/..._test.go`): a config with a valid `parsers` map loads;
      missing/empty hash → error; malformed hash → error.
- [x] Run `go test ./internal/config/...` + `task build` — must pass before Task 2.
      (`go build ./...` clean; `task build`'s `pack:prepare` step needs cross-platform
      goreleaser binaries unavailable locally — unrelated to this change.)

### Task 2: Add `tool.outputParser` + reference validation

- [x] Add `outputParser?: string` to the `Tool` interface in `config/config.d.ts` (a by-name
      reference into `parsers`). Add the matching field to the Go `Tool` struct
      (`internal/config/config.go:75-86`).
- [x] In `internal/config/validate.go`, extend tool validation: if `outputParser` is set, it
      must name an existing `parsers` entry; dangling reference → clear error naming the tool
      and the missing parser (new load-time check — there is no existing app-ref check to
      mirror; follow the runtime-ref validation style). (Extended `ValidateTools` to take the
      `parsers` map; wired `currentConfig.Parsers` through in `cmd/config_loader.go`.)
- [x] Run `task build:lib`.
- [x] Write tests: valid `outputParser` passes; dangling `outputParser` → error with the
      expected message; entity-level + reference-level errors both covered.
- [x] Run `go test ./internal/config/...` — must pass before Task 3.

### Task 3: Declare the reserved `lsp` config entity (types only, no behavior)

- [ ] Add a discriminated union in `config/config.d.ts`: `LspProxy { type: "proxy"; app: string;
projectTypes: string[]; order?: number }` (no globs) and `LspDerived { type: "derived";
tool: string; order?: number }` (inherits the referenced tool's projectTypes/globs and its
      `outputParser`); `lsp?: Record<string, LspProxy | LspDerived>` on `Config`. JSDoc: reserved
      for Phase 3+, declaration only.
- [ ] Add Go structs (`LspEntry` with a `Type` discriminator + optional fields, since Go has no
      unions) and `Config.Lsp`. Minimal structural validation only: `type ∈ {proxy, derived}`;
      `proxy` requires `app` + non-empty `projectTypes`; `derived` requires a `tool` that exists
      in `tools`. NO runtime behavior.
- [ ] Run `task build:lib`.
- [ ] Write tests: valid proxy/derived entries parse; invalid `type` → error; `derived.tool`
      dangling → error.
- [ ] Run `go test ./internal/config/...` — must pass before Task 4.

### Task 4: Post-merge tool-facet validation + alphabetical lsp-order helper

- [ ] After all config overlays are merged (load-time), validate each tool has ≥1 facet: a
      `fix`/`lint` operation OR (future) an `lsp` binding. **Phase 1: require fix/lint present;
      `lsp` is not yet counted.** Runtime check (not a type), placed alongside the other
      post-merge validations. This is new (no existing empty-operations check).
- [ ] Add a small pure comparator `lessLspByOrderThenName(a, b)` that sorts by `order` then
      **alphabetically by entry name** on ties — reusing the `sort.Strings`-by-name convention
      from `planner.go:178-182`. Not wired into behavior (Phase 3 consumes it); unit-tested now
      so the convention is pinned. (Records that `revise.txt` §1.5's "definition order"
      parenthetical was incorrect; actual behavior is alphabetical.)
- [ ] Write tests: tool with no fix/lint → error; tool with a fix op → ok; comparator: equal
      `order` → alphabetical by name.
- [ ] **Dog-food 1:** `task build`; confirm this repo's config loads clean; with a _throwaway
      temp config_ declare a probe `parsers` entry + a `outputParser` on a probe tool →
      validation passes; flip the ref to a non-existent name → validation fails with the clear
      error. Log the outcomes. (Temp config only — do not mutate `datamitsu.config.ts`.)
- [ ] Run `go test ./internal/config/...` + CLI golden suite `go test ./test/cli/ -count=2` —
      must pass before Task 5.

### Task 5: Scaffold the Rust→WASM parser crate (echo skeleton)

- [ ] Create top-level `parsers/` Rust workspace (parallel to `programmable-api/`): workspace
      `Cargo.toml` + member crate (e.g. `parsers/datamitsu-parsers`) with `crate-type =
["cdylib"]` and `[profile.release] opt-level = "s"`, `lto = true`, `strip = true`.
- [ ] Implement the **call contract** (hold the boundary): an exported dispatcher
      `parse(tool_name, stdout: &[u8], stderr: &[u8], exit_code: i32)` that receives **raw bytes
      whole, NOT line-split by the host** (`analysis.md` §2.3: line-splitting in the host loses
      multiline cases like cue_fmt — the parser decides whether to split). Implement the WASM
      memory ABI (exported `alloc`/`dealloc`; pass ptr/len; return ptr/len of an output buffer).
- [ ] Lay out the **output form** (do NOT finalize): a tentative `RawDiagnostic { message:
String, row: Option<u32>, col: Option<u32>, end_row: Option<u32>, end_col: Option<u32>,
severity: Option<u8>, source: Option<String>, code: Option<String> }` serialized as
      **JSON**; every field except `message` is `Option<T>` (`None` = "tool didn't provide",
      not an error — `analysis.md` §1: only `message` is mandatory). JSON, not MessagePack
      (debuggable, premature otherwise). Mark in code: shape placeholder, finalized in Phase 2.
- [ ] Dispatcher body: a single `match` arm `"echo"` returning a fixed serialized result for
      pipe-testing; all other names → empty/`unknown` result.
- [ ] Register the crate in `pnpm-workspace.yaml` `packages` and add a `Taskfile.yaml` target
      (e.g. `build:parsers`) that runs `cargo build --release --target wasm32-unknown-unknown`
      and reports the `.wasm` size.
- [ ] Write a crate `README.md`: how to add a parser (one `match` arm + a module fn) so Phase 2
      adds hadolint/yamllint/dotenv_linter/cue_fmt mechanically.
- [ ] Write Rust tests (`cargo test`, native): the `echo` branch round-trips input→fixed output;
      ABI alloc/dealloc behaves.
- [ ] **Dog-food 2:** build to `.wasm` locally; measure + log artifact size (expect ~15-30KB);
      exercise the echo branch via the test. Must pass before Task 6.

### Task 6: Parser-artifact manager (reuse download/verify/store)

- [ ] `go get github.com/tetratelabs/wazero`; ensure `go mod tidy` is clean (CI gate).
- [ ] Add `env.GetParsersPath()` → `filepath.Join(GetStorePath(), ".parsers")` with an `e.go`
      definition (per the env policy).
- [ ] New `internal/parsermanager` package that **reuses** the binmanager machinery: download
      (`download.go` path) → SHA-256 verify (`verifyFileHash` + `IsAllowedDownloadHashType`) →
      content-addressed store under `.parsers/{name}/{hash}` (XXH3 cache key via `hashutil`) →
      `singleflight` dedup so a parser declared in N tools downloads once. Expose
      `LoadWASMBytes(name) ([]byte, error)`. (If a binmanager internal needs exporting to reuse
      cleanly, export it minimally rather than copy-pasting.)
- [ ] Write tests: serve a fixture via `httptest`/`file://`; correct hash → stored & loadable;
      wrong hash → "hash mismatch" error; same url+hash referenced twice → one download
      (assert via server hit count or log).
- [ ] Run `go test ./internal/parsermanager/...` — must pass before Task 7.

### Task 7: wazero runtime — instantiate + invoke the dispatcher

- [ ] Add a runtime wrapper (in `internal/parsermanager` or `internal/parserwasm`): instantiate
      a wazero module from stored bytes, drive the memory ABI (alloc, write the raw
      `stdout`/`stderr`/`exit_code` input, call exported `parse`, read the output buffer),
      deserialize the JSON into the tentative nullable Go structs (NOT a finalized Diagnostic).
- [ ] Commit a small prebuilt `echo.wasm` test fixture under `testdata/` (regenerated by the
      `build:parsers` task), so the Go test does not require cargo at `go test` time.
- [ ] Write tests: load `echo.wasm`, invoke with sample input, assert the fixed echo output;
      malformed module → error.
- [ ] **Dog-food 3:** on this repo, the core downloads the echo parser into the store, loads it
      into wazero, and gets the fixed answer; corrupt the stored hash → load fails; a parser
      referenced by N tools is downloaded once (check the download log). Must pass before Task 8.

### Task 8: Line-based Myers diff in the Go core

- [ ] New `internal/textdiff` (or `internal/diff`) package: a **line-based Myers diff** producing
      a minimal edit set (port the semantics of efm's `ComputeEdits`, `langserver/diff.go` —
      more precise than none-ls's single-edit; preserves positions for undo/LSP reuse). Unchanged
      input → `nil` (both reference projects return nil on no-op). This is **Go core, not WASM**
      (diff is shared policy, lives with the defaults). Produce edits shaped so they serve both
      CLI apply now and LSP `TextEdit` ranges in Phase 3 (use `[]rune` for any column math —
      `analysis.md` multibyte note).
- [ ] Write tests (table-driven): identical → nil; pure insert / pure delete / replace; trailing
      newline handling; multibyte lines; large-file minimal-edit assertion (not whole-file).
- [ ] Run `go test ./internal/textdiff/...` — must pass before Task 9.

### Task 9: Executor input modes — stdin + separated stdout capture

- [ ] Extend `internal/tooling/executor.go` to support, in addition to the existing `{file}`
      placeholder: (a) **stdin feeding** (`to_stdin`: pipe file content to the tool's stdin) and
      (b) **separated stdout capture** (stdout = candidate formatted content, kept apart from
      stderr — today they are combined in `runCommandWithOutput:1231-1244`). Gate via a config
      flag on the operation (e.g. `input: "stdin" | "file"`, `output: "stdout" | "inplace"`);
      **default behavior unchanged** for existing tools.
- [ ] Add the corresponding TS/Go config fields (and `task build:lib`).
- [ ] Write tests: a fake formatter script in stdin mode and in stdout mode; assert input
      delivery and that stdout is captured cleanly without stderr contamination; existing
      placeholder behavior regression-tested.
- [ ] Run `go test ./internal/tooling/...` + `go test ./test/cli/ -count=2` — must pass before
      Task 10.

### Task 10: Formatting pipeline (capture → diff → apply) in the CLI

- [ ] Wire the formatter contract (`analysis.md` §4.1): capture the original file content, run
      the tool through the existing fix vehicle in the new capture mode (stdout-content OR
      in-place + re-read), treat the result as the full new file text, compute the minimal diff
      (Task 8), and apply the minimal edits to the file. No change → no edits (`nil`).
      **Formatting uses NO WASM parser** (text→text). Works in the CLI context (file/project/
      repo) via the existing fix mechanism; the LSP wrapper (`textDocument/formatting`) is
      Phase 3 but reuses this diff-in-core contract.
- [ ] Write tests: end-to-end formatter with a transform script — diff is minimal (not the whole
      file), unchanged input yields zero edits, output equals a direct tool run.
- [ ] **Dog-food 4:** run formatting through the new path on this repo using an already-configured
      formatter (e.g. gofmt/prettier); compare the result to running the tool directly (must
      match); verify the diff is minimal and "no change → no edits". Log outcomes.
- [ ] Run the full unit suite + CLI golden suite — must pass before Task 11.

### Task 11: CI — build WASM, feed goreleaser, checksum + sign

- [ ] In `.github/workflows/release.yml` (build job, before goreleaser): add Rust toolchain
      setup (rustup + `wasm32-unknown-unknown`) and build the `.wasm` (via the `build:parsers`
      task). Cache the cargo/registry + target dir (`actions/cache`, keyed on `Cargo.lock`).
- [ ] Make goreleaser include the `.wasm` as an extra release artifact so its **SHA-256 lands in
      `checksums.txt`** (`.goreleaser.yml` `extra_files`/release files). Since the existing
      `signs` block signs `artifacts: checksum` (`:159-167`), the WASM hash is then covered by
      the existing cosign signature — no per-version hash embedded in the core (matches §5.2).
- [ ] "Tests" for CI config: run `goreleaser release --snapshot --clean` locally/in-job and
      assert `dist/checksums.txt` lists the `.wasm` entry; add a small assertion script.
- [ ] Run the relevant checks — must pass before Task 12.

### Task 12: npm wrapper for the WASM module + parser manifest generator

- [ ] Add a platform-independent npm package that distributes the built `.wasm` (mirror
      `packaging/npm` but single-package — no per-platform `optionalDependencies`; bundle the
      `.wasm`, expose its path). Wire it into `packaging/pack.ts` (`prepare`/`publish`).
- [ ] Add a **parser manifest** generator: a release-time JSON manifest of `{ name → { url,
hash, version } }` for every shipped parser. The manifest is versioned **with the WASM
      monorepo** (not the core) and its integrity is covered by the signed `checksums.txt`
      (sign the manifest directly too if cheap). This manifest is what config maintainers import
      to obtain a parser's url+hash.
- [ ] Write tests (`pack.ts` / packaging): the wasm npm package builds and contains the `.wasm`;
      the manifest JSON is well-formed and its hashes equal the `checksums.txt` entries.
- [ ] **Dog-food 5:** run the release flow in dry-run / on a test tag; verify the `.wasm` is in
      the artifacts, its hash is in `checksums.txt`, the signature verifies, and the npm wrapper
      installs and yields the `.wasm`. Log outcomes.
- [ ] Run packaging tests — must pass before Task 13.

### Task 13: Verify acceptance criteria (Phase-1 end-to-end slice)

- [ ] Verify the full slice: echo `parsers` entity → built in CI → signed → in `checksums.txt`
      → delivered → downloaded by the core with hash/signature check → loaded in wazero →
      dispatcher invoked → fixed result. AND: formatting via fix + diff-in-core works in the CLI.
- [ ] Confirm the scope boundaries were NOT crossed: Diagnostic not finalized (only nullable
      layout), no real diagnostic parsers, no diagnostic output, no debug flag, no `lsp`
      behavior (type declared only).
- [ ] Run the full unit suite (`go test ./...` + `cargo test`), CLI golden suite
      (`go test ./test/cli/ -count=2`, byte-stable), and the managed `golangci-lint`
      (`--max-issues-per-linter=0 --max-same-issues=0`) — all clean.
- [ ] Verify merged coverage meets the ≈80% target (`pnpm test:coverage:all`).

### Task 14: Documentation

- [ ] Document the `parsers` config entity, `tool.outputParser`, and the reserved `lsp` type in
      the website docs (`website/docs/...`), and the formatting (stdin→stdout→diff) path. Use the
      repo's BAD/GOOD config conventions and architecture-doc style (Mermaid, no Go snippets).
- [ ] Update the architecture docs to include the WASM parser pipeline + diff-in-core.
- [ ] Confirm the crate `README.md` (Task 5) and `config/config.d.ts` ambient declarations give
      IDE autocomplete for the new config surfaces.

_Note: ralphex automatically moves the completed plan to `docs/plans/completed/`._

## Technical Details

- **Nullable contract (the spine):** parser returns `Option<T>` for every field but `message`;
  `None` = "tool didn't emit this", filled by the core's defaults later (Phase 2). The core
  owns defaults (col=1, severity fallback, range completion) and diff — the WASM module owns
  only extraction. Do not finalize the Diagnostic struct in Phase 1.
- **WASM ABI:** `wasm32-unknown-unknown` cdylib; host passes raw byte buffers via exported
  `alloc`/`dealloc` + ptr/len; dispatcher signature carries the tool name first. Raw bytes are
  delivered whole (never host-line-split) to preserve multiline parsing.
- **Hashing split:** SHA-256 (`crypto/sha256`) for verifying the downloaded `.wasm`; XXH3
  (`internal/hashutil`) for the content-addressed store key. Hash on a `parsers` entry is
  mandatory; empty hash is a config error.
- **Trust model:** the core verifies the cosign signature on `checksums.txt` (which lists the
  WASM SHA-256); it does NOT embed a per-version WASM hash, so parsers update independently of
  the core. Concrete hashes live in the manifest/config, not the core binary.
- **Tie-break:** lsp `order` ties resolve alphabetically by entry name (reusing the
  `sort.Strings` convention from `planner.go`); `revise.txt`'s "definition order" note was
  inaccurate.
- **Build invariant:** `go install` does not work; the config TS must be compiled first
  (`task build:lib`) and is embedded via `go:embed`.

## Post-Completion

_Manual / external — no checkboxes._

**Manual verification**

- Cut a real signed release tag and verify the WASM signature/checksum end-to-end live (the
  in-repo dog-food uses dry-run/snapshot).
- Manually confirm the npm wrapper resolves and loads the `.wasm` from a real published version.

**External system updates**

- Declare real `parsers` entries (and any `outputParser` wiring) in the separate
  `datamitsu-config` repo — owner-gated; do not edit that repo autonomously.
- Phase 2 (separate plan): finalize the Diagnostic struct, write the
  hadolint/yamllint/dotenv_linter/cue_fmt parsers on this proven pipe, add diagnostic output +
  debug flag. Phase 3+: implement `lsp` proxy/derived behavior and `textDocument/formatting`.
