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

- [ ] write a test asserting `buildNodeBinaries` hard-errors when an archive's hash
      is absent from the SHASUMS map (fail-closed contract for `parseSHASUMS` drops)
- [ ] write a test for `VerifyClearsigned` confirming content appended AFTER the
      clearsign block is NOT included in the returned plaintext (refutes the
      "appended unsigned lines verify" concern)
- [ ] add/clarify a comment on `VerifyClearsigned` (`nodekeys.go:88`) documenting the
      trust model: signature-validity only, no revocation/expiry enforcement, git-pinned
      hash is the runtime anchor (no behavior change)
- [ ] run `go test ./cmd/... ./internal/nodekeys/...` — must pass

### Task 8: Minor simplifications in the node/pnpm flow (review #13)

- [ ] remove the redundant `os.MkdirAll(appEnvPath, 0755)` at `node.go:135`
      (`writeAppWorkspaceFile` already creates it) — confirm via test that install still works
- [ ] replace `defaultPNPMWorkspaceConfig()` indirection with a direct
      `pnpmdefaults.Defaults()` call at its single use site (`pnpm.go:384`)
- [ ] extract a `hasSHA512Prefix(integrity string) bool` helper and use it in both the
      early check (`pnpm.go:104`) and `verifyPNPMIntegrity` (`pnpm.go:256`) to remove
      the duplicated literal
- [ ] inline `installNode` or document why it differs from jvm/uv (which call
      `GetRuntimePath` directly) — pick one and apply consistently
- [ ] run `go test ./internal/runtimemanager/...` — must pass

### Task 9: Compute merged `pnpm-workspace.yaml` once per exec (review #8)

- [ ] write a test asserting the workspace YAML merge/marshal runs once (not twice)
      per `GetCommandInfo` call on a cache hit (inject a counter or assert via a seam)
- [ ] refactor so `appEnvPath` + merged YAML are computed once and threaded into both
      `InstallNodeApp` and `GetNodeCommandInfo` (touch `GetCommandInfo` dispatch in
      `runtimemanager.go:401` and `resolveNodeAppEnvPath`)
- [ ] add a test confirming the cache key (appEnvPath) is identical to before (no
      behavior change, only fewer recomputations)
- [ ] run `go test ./internal/runtimemanager/...` — must pass

### Task 10: Shared hardened HTTP client helper (review #7)

- [ ] write a test for the shared client's redirect policy (HTTPS→HTTP rejected, >10 redirects rejected) in the helper's package
- [ ] add `newHardenedHTTPClient(timeout time.Duration) *http.Client` (proxy, dialer,
      TLS/response-header timeouts, redirect guard) in a shared spot (e.g.
      `internal/binmanager` exported or a small `internal/httpx`)
- [ ] replace the duplicated clients in `pnpm.go:28`, `cmd/devtools_pull_runtimes.go:359`,
      and the jvm/remotecfg copies with the helper (preserving each call site's timeout)
- [ ] add a test confirming each replaced client keeps its original timeout
- [ ] run `go test ./...` — must pass

### Task 11: Reuse binmanager's hardened tar extractor for pnpm (review #6)

- [ ] write tests for the chosen binmanager entry point covering path-traversal
      (`../escape`), absolute symlink, and escaping symlink are skipped, and a normal
      `package/bin/pnpm.cjs` extracts correctly
- [ ] expose/reuse a binmanager dir-extractor for `.tgz` (extend
      `ExtractDirForVerify`/`extractTarGzToDir`, `extract.go:564`) so pnpm extracts via
      the single hardened path; delete `extractFullTgz` (`pnpm.go:161`)
- [ ] verify the pnpm size limits are preserved or consciously unified with binmanager's
      (note the chosen limits in this plan if changed)
- [ ] update `pnpm_test.go` extraction tests to target the shared extractor
- [ ] run `go test ./internal/runtimemanager/... ./internal/binmanager/...` — must pass

### Task 12: RuntimeKind registry — table-driven facts (review #5, part 1)

- [ ] write tests for a `runtimekind` registry: lookup by kind returns system-command
      name, the hash-fold version fields, and the validation rule; unknown kind errors
- [ ] introduce a registry (map keyed by `config.RuntimeKind`) describing per-kind:
      system binary name, cache-affecting version field(s), validation requirements,
      and kind⇄name string mapping
- [ ] replace `systemCommandForKind` (`runtimemanager.go:49`) and the duplicated
      hash-fold blocks in `calculateRuntimeHash`/`calculateSystemRuntimeHash`
      (`hash.go`) with registry lookups
- [ ] replace the per-kind blocks in `ValidateRuntimes` (`validate.go:541`) with
      registry-driven validation
- [ ] write tests asserting managed-mode and system-mode hashes fold the SAME fields
      for every kind (guards the lock-step drift risk)
- [ ] run `go test ./internal/runtimemanager/... ./internal/config/...` — must pass

### Task 13: RuntimeKind registry — consolidate dispatch (review #5, part 2)

- [ ] write tests asserting `GetCommandInfo`/`ComputeAppPath`/`CollectRequiredRuntimes`
      route every kind correctly, including an unknown/empty kind error path
- [ ] consolidate the three dispatch chains in `runtimemanager.go` into one helper that
      selects the set `App.*` sub-config and calls the matching install/command-info path
- [ ] consolidate the per-kind `if/switch` chains in `cmd/devtools_verify.go`
      (`runtimeAppKeyAndFP`, `resolveDefaultRuntimeName`, version extraction, `getAppVersion`)
      to use the registry's kind⇄name and sub-config accessors
- [ ] write tests covering devtools_verify fingerprint/version extraction per kind
- [ ] run `go test ./...` — must pass

### Task 14: Full Go support in pull-runtimes (review #3 + reported-bug capstone)

- [ ] write tests for a new `internal/registry` Go release fetcher against a mock
      `go.dev/dl/?mode=json` body: returns latest stable version + per-file SHA-256;
      error/empty-body cases handled
- [ ] add the Go release fetcher in `internal/registry` (HTTPS + published SHA-256,
      no GPG — document the trust model, parallel to musl)
- [ ] add `buildGoRuntimeJSON` + `pullGoRuntime` building the os/arch archive map from
      go.dev with the published SHA-256 (lowercased; mandatory-hash enforced)
- [ ] add `"go"` to `validRuntimeNames` (`cmd/devtools_pull_runtimes.go:33`) and a `go`
      case in the `runPullRuntimes` switch, leaning on the Task 12 registry so the
      addition is localized
- [ ] write a `cmd/devtools_pull_node_test.go`-style mock-host test for `pull --runtime go`
      that asserts a generated entry with `go.goVersion` + verified hashes, and that a
      subsequent pull does NOT drop `goVersion`
- [ ] run `go test ./cmd/... ./internal/registry/...` — must pass

### Task 15: Verify acceptance criteria

- [ ] verify the reported bug is gone: a `pull:runtimes` (dry-run or mock) preserves
      `go.goVersion`
- [ ] verify all review findings 1,2,4,6,9,10 are fixed and 3,5,7,8,11–13 addressed
- [ ] run full `go test ./...`
- [ ] `pnpm build` then `go build` succeed (embedded TS rebuilt)
- [ ] run the project linter (`golangci-lint run` / `go vet ./...`) — fix all issues
- [ ] verify test coverage of changed packages meets the project standard

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
