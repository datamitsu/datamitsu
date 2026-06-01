# Runtime Review Fixes, goVersion Pull Bug, and RuntimeKind Refactor

## Overview

Closes the findings from the branch review of `feat/migrate-node-fnm-to-archive`
plus a reported bug, in three layers:

1. **Bug fix (urgent):** `pull:runtimes` silently deletes `goVersion` on every
   run. Root cause: `RuntimeJSON` in `cmd/devtools_pull_runtimes.go` has no `Go`
   field, so `readRuntimesJSON` drops the `go: { goVersion }` block on unmarshal
   and `writeRuntimesJSON` writes the entry back without it. The managed binaries
   survive (because `Managed` exists), only the Go-specific config is lost.
2. **Correctness + quality fixes** from the review (validator divergences,
   swallowed errors, scheme checks, duplication, simplifications).
3. **Architecture:** a `RuntimeKind` registry/abstraction replacing the hand-written
   `if/switch` dispatch duplicated across 6+ sites, then **full Go support in
   pull-runtimes** built on that abstraction (closes review finding #3 and makes
   the goVersion fix structural, not a band-aid).

Benefits: stops data loss in the wrapper-maintenance workflow, removes validator
inconsistencies that can make a generated config unloadable, and turns "add a
runtime kind" from a whole-repo edit fan-out into a single registry entry.

## Context (from discovery)

Files/components involved:

- `cmd/devtools_pull_runtimes.go` — pull dispatch, `RuntimeJSON`, `parseSHASUMS`,
  `isSHA256Hex`, `pullNodeRuntime`, `buildNodeBinaries`, `validRuntimeNames`
- `internal/runtimemanager/node.go`, `pnpm.go` — node-archive install + pnpm flow
- `internal/runtimemanager/runtimemanager.go` — `validateRelativePath`,
  `systemCommandForKind`, `resolveLibcKey`, dispatch chains (`GetCommandInfo`,
  `ComputeAppPath`, `CollectRequiredRuntimes`)
- `internal/runtimemanager/hash.go` — `calculateRuntimeHash` / `calculateSystemRuntimeHash`
- `internal/config/validate.go` — `isValidSHA256Hex`, `validateSafeRelativePath`,
  `ValidateRuntimes`
- `internal/config/config.go` — `RuntimeConfigGo` (`goVersion`), `RuntimeKind` enum
- `internal/nodekeys/nodekeys.go` — `VerifyClearsigned`
- `cmd/devtools_verify.go` — per-kind `if/switch` chains
- `internal/binmanager/extract.go` — hardened `extractTarToDir` / `ExtractDirForVerify`
- `internal/registry/` — version fetchers (no Go fetcher exists yet; `go.dev/dl/?mode=json`
  exposes versions + per-file SHA-256 over HTTPS, no GPG — same trust model as musl)
- `config/config.d.ts`, `config/src/runtimes.json`

Related patterns found:

- `config/src/runtimes.json` already ships a `go` runtime: `{ go: { goVersion: "1.26.3" }, kind: "go", managed: { binaries: {...} } }`
- Existing pull paths (uv/jvm/node) preserve unmanaged entries by copying `existing`
  into a new map — but the round-trip through the typed struct is what loses `go`.
- Pull side already enforces `https://` on the pnpm tarball URL
  (`fetchPNPMTarballHash`); the runtime side does not.

Dependencies identified: `go-crypto/openpgp`, `goccy/go-yaml`, internal `hashutil`,
`env`, `binmanager`, `registry`.

## Development Approach

- **Testing approach**: TDD (tests first) — for each task, write the failing
  test that pins the bug/contract, then implement until green.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** (success + error/edge).
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `go test ./...` after each change. Build requires `pnpm build` (compiles the
  embedded TS config) **before** `go build` — `go install` does NOT work for this
  project (see CLAUDE.md).
- Hash-policy guardrails (CLAUDE.md): SHA-256 for external verification, XXH3-128
  via `internal/hashutil` for internal keys; never relax mandatory-hash checks.
- Maintain backward compatibility for `runtimes.json` shape where feasible (alpha:
  breaking changes acceptable only when they improve correctness/safety).

## Testing Strategy

- **Unit tests**: required for every task (table-driven Go tests next to the code).
- **Round-trip / golden tests**: for the pull-runtimes JSON, assert that an
  existing `go` entry survives a pull cycle (regression guard for the reported bug).
- **No UI e2e**: this is a Go CLI; the closest to e2e is the mock-server pull tests
  (`cmd/devtools_pull_*_test.go`) — extend those with mock `go.dev`/SHASUMS hosts.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, in-repo docs.
- **Post-Completion** (no checkboxes): manual `pull:runtimes` dry-run verification,
  any follow-up in consuming configs.

## Implementation Steps

### Task 1: Stop `goVersion` data loss — add `Go` field to `RuntimeJSON` round-trip

- [x] write a failing test in `cmd/devtools_pull_runtimes_test.go`: seed a `runtimes.json`
      fixture containing a `go` entry with `go.goVersion`, run the read→merge→write
      cycle (filter to `node` so go is only carried over), assert the written file
      still contains `go.goVersion`
- [x] add a `GoConfigJSON` struct (goVersion field) and a `Go *GoConfigJSON`
      (`go,omitempty`) field to `RuntimeJSON` (`cmd/devtools_pull_runtimes.go:246`)
- [x] extend `runtimeVersion()` (`cmd/devtools_pull_runtimes.go:190`) to report `go=<ver>`
      so summary/`oldVersion` diffing works for go
- [x] write a test asserting an unmanaged kind is preserved verbatim through the cycle
      (general round-trip guard, not just go)
- [x] run `go test ./cmd/...` — must pass before next task

### Task 2: Normalize SHA-256 hex case so pull output stays config-loadable (review #1)

- [x] write a failing test: a SHASUMS manifest with UPPERCASE hex flows through
      `buildNodeBinaries` and the resulting hash is accepted by
      `config.ValidateRuntimes` (currently rejected — `isValidSHA256Hex` requires lowercase)
- [x] lowercase the hash (`strings.ToLower`) when recording it in `buildNodeBinaries`
      (`cmd/devtools_pull_runtimes.go:768`), keeping the `isSHA256Hex` length/hex guard
- [x] add a test asserting the stored hash equals the lowercase form
- [x] add a test that an invalid (non-hex / wrong-length) hash still hard-errors
- [x] run `go test ./cmd/... ./internal/config/...` — must pass before next task

### Task 3: Align `validateRelativePath` with `validateSafeRelativePath` (review #2)

- [x] write failing tests in `internal/runtimemanager/runtimemanager_test.go`:
      `..config/bin/tool` is ACCEPTED, `../escape` and `..` are REJECTED, and the
      result matches `config.validateSafeRelativePath` for the same inputs
- [x] replace `strings.HasPrefix(cleaned, "..")` (`runtimemanager.go:744`) with
      `cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator))`
- [x] add error-case tests (absolute path, embedded `../`)
- [x] run `go test ./internal/runtimemanager/... ./internal/config/...` — must pass

### Task 4: Fail loudly when Node LTS lookup errors during pull (review #4)

- [x] write a failing test: `pullNodeRuntime` (or its injectable seam) returns an
      error when the LTS lookup fails, instead of warning + pinning the fallback
- [x] change `pullNodeRuntime` (`cmd/devtools_pull_runtimes.go:842`) to return the
      error from `GetLatestNodeLTSVersion` rather than `Warning: ... (using fallback)`
- [x] ensure `runPullRuntimes` surfaces it as a non-zero exit (already aggregates `r.err`)
- [x] add a success-path test (lookup ok → version used)
- [x] run `go test ./cmd/...` — must pass

### Task 5: Enforce HTTPS on the pnpm tarball URL in the runtime download (review #9)

- [x] write a failing test in `internal/runtimemanager/pnpm_test.go`: a mock registry
      whose `dist.tarball` is `http://...` makes `downloadPNPMFromRegistryURL` error
- [x] add `if !strings.HasPrefix(meta.Dist.Tarball, "https://") { return err }` in
      `downloadPNPMFromRegistryURL` (`pnpm.go:101`), mirroring `fetchPNPMTarballHash`
- [x] add a success-path test (https tarball still works end-to-end via mock)
- [x] run `go test ./internal/runtimemanager/...` — must pass

### Task 6: Propagate `os.RemoveAll` failure on node app reinstall (review #10)

- [x] write a failing test exercising the "bin shim exists but module missing"
      reinstall branch and asserting a removal failure aborts with an error
      (inject via a read-only/locked path or a small seam)
- [x] change `_ = os.RemoveAll(appEnvPath)` (`node.go:119`) to check the error and
      return a wrapped failure instead of proceeding over a dirty tree
- [x] add a success-path test (removal succeeds → reinstall proceeds)
- [x] run `go test ./internal/runtimemanager/...` — must pass

### Task 7: Lock in SHASUMS fail-closed behavior + document provenance trust model (review #11, #12)

- [x] write a test asserting `buildNodeBinaries` hard-errors when an archive's hash
      is absent from the SHASUMS map (fail-closed contract for `parseSHASUMS` drops)
- [x] write a test for `VerifyClearsigned` confirming content appended AFTER the
      clearsign block is NOT included in the returned plaintext (refutes the
      "appended unsigned lines verify" concern)
- [x] add/clarify a comment on `VerifyClearsigned` (`nodekeys.go:88`) documenting the
      trust model: signature-validity only, no revocation/expiry enforcement, git-pinned
      hash is the runtime anchor (no behavior change)
- [x] run `go test ./cmd/... ./internal/nodekeys/...` — must pass

### Task 8: Minor simplifications in the node/pnpm flow (review #13)

- [x] remove the redundant `os.MkdirAll(appEnvPath, 0755)` at `node.go:135`
      (`writeAppWorkspaceFile` already creates it) — confirm via test that install still works
- [x] replace `defaultPNPMWorkspaceConfig()` indirection with a direct
      `pnpmdefaults.Defaults()` call at its single use site (`pnpm.go:384`)
- [x] extract a `hasSHA512Prefix(integrity string) bool` helper and use it in both the
      early check (`pnpm.go:104`) and `verifyPNPMIntegrity` (`pnpm.go:256`) to remove
      the duplicated literal
- [x] inline `installNode` or document why it differs from jvm/uv (which call
      `GetRuntimePath` directly) — pick one and apply consistently
- [x] run `go test ./internal/runtimemanager/...` — must pass

### Task 9: Compute merged `pnpm-workspace.yaml` once per exec (review #8)

- [x] write a test asserting the workspace YAML merge/marshal runs once (not twice)
      per `GetCommandInfo` call on a cache hit (inject a counter or assert via a seam)
- [x] refactor so `appEnvPath` + merged YAML are computed once and threaded into both
      `InstallNodeApp` and `GetNodeCommandInfo` (touch `GetCommandInfo` dispatch in
      `runtimemanager.go:401` and `resolveNodeAppEnvPath`)
- [x] add a test confirming the cache key (appEnvPath) is identical to before (no
      behavior change, only fewer recomputations)
- [x] run `go test ./internal/runtimemanager/...` — must pass

### Task 10: Shared hardened HTTP client helper (review #7)

- [x] write a test for the shared client's redirect policy (HTTPS→HTTP rejected, >10 redirects rejected) in the helper's package
      (`internal/httpx/httpx_test.go`: unit tests on `hardenedRedirect` + an end-to-end
      downgrade-rejection test through a real client/server, plus transport-defaults asserts)
- [x] add `newHardenedHTTPClient(timeout time.Duration) *http.Client` (proxy, dialer,
      TLS/response-header timeouts, redirect guard) in a shared spot (e.g.
      `internal/binmanager` exported or a small `internal/httpx`)
      — implemented as exported `httpx.NewHardenedClient` in the new `internal/httpx`
      package (exported so cross-package call sites can use it)
- [x] replace the duplicated clients in `pnpm.go:28`, `cmd/devtools_pull_runtimes.go:359`,
      and the jvm/remotecfg copies with the helper (preserving each call site's timeout)
      — note: `cmd/devtools_pull_runtimes.go` gains the hardened transport it previously
      lacked; remotecfg's dialer/response-header sub-timeouts standardize to the shared
      defaults (bounded by its unchanged 30s overall budget). binmanager's own client is
      left as-is (not in the listed call sites; its redirect test still references it).
- [x] add a test confirming each replaced client keeps its original timeout
      (`internal/runtimemanager/http_clients_test.go` pins pnpm/jvm at 5m,
      `internal/remotecfg/http_client_test.go` pins 30s,
      `cmd/devtools_pull_runtimes_http_client_test.go` pins 2m)
- [x] run `go test ./...` — must pass

### Task 11: Reuse binmanager's hardened tar extractor for pnpm (review #6)

- [x] write tests for the chosen binmanager entry point covering path-traversal
      (`../escape`), absolute symlink, and escaping symlink are skipped, and a normal
      `package/bin/pnpm.cjs` extracts correctly
      (`TestExtractArchiveToDir` in `internal/binmanager/extract_test.go`)
- [x] expose/reuse a binmanager dir-extractor for `.tgz` so pnpm extracts via
      the single hardened path; delete `extractFullTgz` (`pnpm.go`)
      — added exported `binmanager.ExtractArchiveToDir(archivePath, format, destDir)`
      wrapping the existing hardened `extractArchiveToPath` (writes directly into
      destDir, unlike `ExtractDirForVerify`/`extractTarGzToDir` which return a temp
      subdir — the path semantics pnpm requires). `pnpm.go` now calls it with
      `BinContentTypeTarGz`; the redundant pre-extract `os.MkdirAll(destDir)` was
      dropped (the extractor creates the dir).
- [x] verify the pnpm size limits are preserved or consciously unified with binmanager's
      — **consciously unified** onto binmanager's limits: per-file cap moves from the
      pnpm-local `maxPNPMDownloadSize` (100 MiB) to `MaxBinarySize` (500 MiB) and the
      total cap from `maxTotalExtractedSize` (500 MiB) to binmanager's 2 GiB. The pnpm
      _download_ cap stays 100 MiB (`maxPNPMDownloadSize` still guards the tarball
      fetch); only the extraction limits unify. The now-unused
      `maxTotalExtractedSize` const was removed from `pnpm.go`.
- [x] update `pnpm_test.go` extraction tests to target the shared extractor
      (`TestExtractFullTgz` → `TestPNPMExtractionViaSharedExtractor`, now calling
      `binmanager.ExtractArchiveToDir`)
- [x] run `go test ./internal/runtimemanager/... ./internal/binmanager/...` — must pass

### Task 12: RuntimeKind registry — table-driven facts (review #5, part 1)

- [x] write tests for a `runtimekind` registry: lookup by kind returns system-command
      name, the hash-fold version fields, and the validation rule; unknown kind errors
      (`internal/config/runtimekind_test.go`: `TestLookupRuntimeKind_KnownKinds`,
      `TestLookupRuntimeKind_UnknownKind`, `TestRuntimeKindHashFields`,
      `TestRuntimeKindValidate`, `TestAllRuntimeKinds`)
- [x] introduce a registry (map keyed by `config.RuntimeKind`) describing per-kind:
      system binary name, cache-affecting version field(s), validation requirements,
      and kind⇄name string mapping
      — added `internal/config/runtimekind.go` with `RuntimeKindInfo`
      (`Name`/`SystemCommand`/`HashFields`/`Validate`), the `runtimeKinds` map, and
      `LookupRuntimeKind`/`AllRuntimeKinds`. Lives in `config` (not a new package) to
      avoid a `config`↔registry import cycle, since `Validate`/`HashFields` reference
      `config.RuntimeConfig` and `ValidateRuntimes` consumes the registry.
- [x] replace `systemCommandForKind` (`runtimemanager.go:49`) and the duplicated
      hash-fold blocks in `calculateRuntimeHash`/`calculateSystemRuntimeHash`
      (`hash.go`) with registry lookups
      — `systemCommandForKind` now delegates to `config.LookupRuntimeKind`; both hash functions
      route their version-field fold through a single `appendKindVersionFields` helper
      backed by `RuntimeKindInfo.HashFields` (byte-identical field order preserved, so
      existing cache keys are unchanged).
- [x] replace the per-kind blocks in `ValidateRuntimes` (`validate.go:541`) with
      registry-driven validation (kind-agnostic mode/managed/system checks stay inline;
      unknown/legacy kinds with no registry entry are skipped as before)
- [x] write tests asserting managed-mode and system-mode hashes fold the SAME fields
      for every kind (guards the lock-step drift risk)
      — `TestRuntimeHashFoldsSameFieldsForEveryKind` in
      `internal/runtimemanager/hash_test.go`: per kind/field it mutates the field and
      asserts BOTH `calculateRuntimeHash` and `calculateSystemRuntimeHash` change, and
      cross-checks the enumerated field count against the registry's `HashFields`.
- [x] run `go test ./internal/runtimemanager/... ./internal/config/...` — must pass

### Task 13: RuntimeKind registry — consolidate dispatch (review #5, part 2)

- [x] write tests asserting `GetCommandInfo`/`ComputeAppPath`/`CollectRequiredRuntimes`
      route every kind correctly, including an unknown/empty kind error path
      (`internal/runtimemanager/runtimekind_dispatch_test.go`: `TestRuntimeAppRef`,
      `TestComputeAppPath_RoutesEveryKind`, `TestGetCommandInfo_RoutesEveryKind`,
      `TestCollectRequiredRuntimes_EveryKind` — each covers uv/node/jvm/go plus the
      empty/binary/shell non-runtime error path)
- [x] consolidate the three dispatch chains in `runtimemanager.go` into one helper that
      selects the set `App.*` sub-config and calls the matching install/command-info path
      — added `runtimeAppRef(app) (kind, runtimeRef, ok)` as the single point encoding the
      `App.*` precedence (uv→node→jvm→go); `CollectRequiredRuntimes` now routes through it
      (its 4× duplicated per-kind block collapses to one loop). `ComputeAppPath` and
      `GetCommandInfo` are converted to mirror-image `switch` statements that keep typed
      dispatch (each kind's install/path method takes a differently-typed `AppConfig*`,
      per the plan's Technical Details), so the precedence is now defined once and the two
      typed dispatchers mirror it.
- [x] consolidate the per-kind `if/switch` chains in `cmd/devtools_verify.go`
      (`runtimeAppKeyAndFP`, `resolveDefaultRuntimeName`, version extraction, `getAppVersion`)
      to use the registry's kind⇄name and sub-config accessors
      — added `runtimeAppInfo(app) (kind, version, runtimeRef, subConfig, ok)` (+ thin
      `runtimeAppVersion`) as the shared verify-phase dispatch point; `runtimeAppKeyAndFP`,
      the phase-3 entry loop, both version-extraction switches, and `getAppVersion` now use
      it. `resolveDefaultRuntimeName` is now registry-driven via `config.LookupRuntimeKind`
      (unknown/empty kind → "") instead of a string→`RuntimeKind` switch.
- [x] write tests covering devtools_verify fingerprint/version extraction per kind
      (`cmd/devtools_verify_dispatch_test.go`: `TestRuntimeAppInfo`,
      `TestRuntimeAppInfo_SubConfigMarshalsLikeTypedField`,
      `TestRuntimeAppKeyAndFP_EveryKind`, `TestRuntimeAppKeyAndFP_DefaultRuntimeFold`,
      `TestResolveDefaultRuntimeName_AllKinds`)
- [x] run `go test ./...` — must pass

### Task 14: Full Go support in pull-runtimes (review #3 + reported-bug capstone)

- [x] write tests for a new `internal/registry` Go release fetcher against a mock
      `go.dev/dl/?mode=json` body: returns latest stable version + per-file SHA-256;
      error/empty-body cases handled
      (`internal/registry/godev_test.go`: `TestGetLatestGoRelease` covers
      highest-stable selection, order-independence, unstable-skip, no-stable error,
      no-hashed-files error, empty body, server error, invalid JSON, connection error;
      `TestGoVersionLess` pins the version ordering)
- [x] add the Go release fetcher in `internal/registry` (HTTPS + published SHA-256,
      no GPG — document the trust model, parallel to musl)
      — added `internal/registry/godev.go` with `GetLatestGoRelease`/`GoRelease`
      (version + filename→SHA-256 map), `highestStableGoRelease`, and `goVersionLess`
      (semver-based, falls back to string compare). The fetcher's doc comment records
      the no-GPG / published-SHA-256 trust model (same as the musl node path).
- [x] add `buildGoRuntimeJSON` + `pullGoRuntime` building the os/arch archive map from
      go.dev with the published SHA-256 (lowercased; mandatory-hash enforced)
      — `goArchiveSpecs`/`goBinaryPath`/`goPullConfig`/`buildGoBinaries`/`pullGoRuntime`/
      `buildGoRuntimeJSON` in `cmd/devtools_pull_runtimes.go`; missing hash and non-hex
      hash are hard errors, hashes are lowercased before recording (mirrors node).
- [x] add `"go"` to `validRuntimeNames` (`cmd/devtools_pull_runtimes.go:34`) and a `go`
      case in the `runPullRuntimes` switch, leaning on the Task 12 registry so the
      addition is localized (validation/cache for go flow through the registry entry).
      Help text + `--runtime` flag description updated to list `go`.
- [x] write a `cmd/devtools_pull_node_test.go`-style mock-host test for `pull --runtime go`
      that asserts a generated entry with `go.goVersion` + verified hashes, and that a
      subsequent pull does NOT drop `goVersion`
      (`cmd/devtools_pull_go_test.go`: `TestRunPullRuntimes_Go_GeneratesEntryWithGoVersion`
      injects the `getLatestGoRelease` seam, runs the full pull twice, and after each
      run decodes the written file into `config.MapOfRuntimes` + `ValidateRuntimes`;
      plus spec/path, all-tuples, missing/invalid/uppercase-hash, builder, and
      `pullGoRuntime` success/lookup-error tests)
- [x] run `go test ./cmd/... ./internal/registry/...` — must pass

### Task 15: Verify acceptance criteria

- [x] verify the reported bug is gone: a `pull:runtimes` (dry-run or mock) preserves
      `go.goVersion` — `TestRunPullRuntimes_Go_GeneratesEntryWithGoVersion` (full pull run
      twice, `go.goVersion` preserved both times) plus `TestReadWriteRuntimesJSON_PreservesGoEntry`
      and `..._RoundtripPreservesCarriedOverKind` all pass
- [x] verify all review findings 1,2,4,6,9,10 are fixed and 3,5,7,8,11–13 addressed —
      Tasks 1–14 each landed the corresponding fix + regression test (see commits
      e609a32..b5a856a); finding #3 (full Go pull support) is exercised by the
      `internal/registry` Go fetcher tests and the `pull --runtime go` mock-host test
- [x] run full `go test ./...` — all packages pass (no FAIL)
- [x] `pnpm build` then `go build` succeed (embedded TS rebuilt) — the embedded-config
      compile (tsdown "Build complete", `dist/` artifacts regenerated) and `go build`
      (valid ELF binary via `go build -o`) both succeed. NOTE: the full `pnpm build`
      pipeline exits non-zero only at its final `pack:prepare` step, which expects
      GoReleaser cross-platform binaries (`dist/binaries/datamitsu-darwin_amd64`) absent
      in a dev checkout — a pre-existing packaging-step limitation unrelated to this work
- [x] run the project linter (`golangci-lint run` / `go vet ./...`) — fix all issues —
      `go vet ./...` clean; `golangci-lint run ./...` (via `datamitsu exec`) exits 0
- [x] verify test coverage of changed packages meets the project standard — runtimemanager
      60.3%, httpx 95.7%, nodekeys 86.4%, binmanager 70.9%, remotecfg 60.3%, registry 50.0%,
      cmd 38.4%, config 30.7% (in line with the levels recorded across iterations)

### Task 16: Update documentation

- [ ] document `pull-runtimes --runtime go` in `website/docs/how-to/maintain-wrapper.md`
- [ ] note the RuntimeKind registry in the architecture knowledge doc
      (`website/docs/guides/architecture/` and/or `docs/architecture.md`)
- [ ] update `config/config.d.ts` / config reference if the Go pull surface changed
- [ ] add any new identifiers (e.g. fetcher names) to `cspell.config.js` if flagged

## Technical Details

**goVersion round-trip (root cause):**
`readRuntimesJSON` → `json.Unmarshal` into `RuntimesJSON` (`map[string]*RuntimeJSON`).
`RuntimeJSON` lacks a `Go` field ⇒ the `go` sub-object is discarded. The map copy
(`runtimes[k] = v`) then carries an entry whose `go` block is already gone, and
`writeRuntimesJSON` re-marshals it without `goVersion`. Adding `Go *GoConfigJSON`
makes the round-trip lossless (Task 1); Task 14 makes go a first-class pull target.

**RuntimeKind registry shape (Tasks 12–13):**

```
type kindInfo struct {
    name          string                        // "uv" | "jvm" | "node" | "go"
    systemCommand string                        // "uv" | "java" | "node" | "go"
    hashFields    func(config.RuntimeConfig) []string
    validate      func(config.RuntimeConfig) []string // returns error strings
}
var runtimeKinds = map[config.RuntimeKind]kindInfo{ ... }
```

App-level dispatch (install/command-info) keeps a single switch because each kind's
method takes a differently-typed `AppConfig*`; the registry removes the _duplicated_
switches (system command, hash fold, validation, kind⇄name, verify sub-config) — that
is where the lock-step drift lives.

**Go release source (Task 14):** `https://go.dev/dl/?mode=json&include=all` (filter to
stable, highest version). Each file entry has `filename`, `os`, `arch`, `kind` (archive),
and `sha256`. Map datamitsu tuples → `go<ver>.<os>-<arch>.tar.gz` (and `.zip` on
windows), `binaryPath: "go/bin/go"`, `extractDir: true`. No GPG — HTTPS + published
SHA-256, same documented trust posture as the musl node path.

**Processing flow unchanged for verification:** all download paths keep the pinned
SHA-256 as the security anchor; this plan never removes or weakens a hash check.

## Post-Completion

_Informational — manual/external follow-ups._

**Manual verification:**

- Run a real `datamitsu devtools pull-runtimes --runtime go` (network) and confirm the
  regenerated `config/src/runtimes.json` keeps `go.goVersion` and verified hashes.
- Run a real `pull-runtimes` (all) and confirm uv/jvm/node/go all survive and update.
- Spot-check that a generated config loads (`datamitsu init` / validate) with no
  "must be 64 lowercase hex" errors.

**Supply-chain note (not in scope, by design):** musl node hashes and Go hashes are
HTTPS-sourced without GPG; the git-pinned SHA-256 is the trust anchor (documented,
matches node:alpine). An out-of-band cross-check could be a future hardening.
