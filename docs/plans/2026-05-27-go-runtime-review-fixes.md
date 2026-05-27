# Go Runtime Code Review Fixes

## Overview

Address code-review findings on the `feat/go-runtime` branch (Go runtime kind for binmanager/runtimemanager) before merging to `main`. The review surfaced 14 issues; this plan implements the subset the user agreed to fix.

**Out of scope (explicit user decisions):**

- **#1 GOPROXY hardcoding** — lockfile generation is a one-time dev-machine operation; `go build -mod=readonly` against the pinned `go.sum` already provides the supply-chain guarantee. Hardcoding `proxy.golang.org` would break corporate proxies.
- **#2 musl variant for Go SDK** — Go upstream tarballs are statically-linked (Go is self-hosted); they work on Alpine out of the box. The `"libc": "glibc"` label is misleading but functionally correct.
- **#10 extractDir zip bomb** — not Go-specific, requires deeper refactor of `cmd/devtools_verify.go`; defer to a separate branch.
- **#12 GOPROXY test** — moot after #1 retract.
- **#13 ordering of app type if/else chains** — pre-existing, keep current order (by usage frequency).
- **#14 cspell GONOSUMCHECK** — cosmetic.

**In scope:**

- #3 `GenerateGoLockFiles` pollutes the cache dir with 100+MiB and never cleans up
- #4 `app.Go.Version` not validated (goes verbatim into `go get pkg@version`)
- #5 `app.Go.PackageName` not validated (`packageName: ".."` can short-circuit "already installed" check)
- #6 `installOnce` retry pattern is race-prone — replace with `singleflight.Group` across all 4 sibling runtimes for consistency
- #7 `goVersion` is required even in system mode — mismatched with UV's pattern
- #8 `Files`/`Links`/`Archives` allowed on Go apps despite being meaningless (Go build doesn't consume them); also ban for JVM apps (one-command URL download, no workdir needed)
- #9 `_ = os.Remove` swallows non-NotExist errors during workdir prep
- #11 Build log says "Building" while sibling runtimes say "Installing"

## Context (from discovery)

**Files/components involved:**

- `internal/runtimemanager/go.go` — Go runtime install + lockfile generation
- `internal/runtimemanager/runtimemanager.go` — shared `installOnce` struct + `runtimeInstall` sync.Map
- `internal/runtimemanager/fnm.go` — 3 install sites (pnpmInstall, nodeInstall, appInstall)
- `internal/runtimemanager/uv.go` — 1 install site
- `internal/runtimemanager/jvm.go` — 1 install site
- `internal/config/validate.go` — app + runtime validation rules
- `cmd/config_lockfile.go` — lockfile generation entry point (Go branch uses `freshInstallPath` as workdir)
- Test files: `go_test.go`, `fnm_test.go`, `uv_test.go`, `jvm_test.go`, `runtimemanager_test.go`, `validate_test.go`, `config_lockfile_test.go`

**Related patterns found:**

- `installOnce { sync.Once + err }` + `sync.Map` + `LoadOrStore` → `once.Do` → `if err { CompareAndDelete }` — used in 7 places across 4 runtime files; identical race pattern (a deletion can orphan an in-flight reader).
- `golang.org/x/sync v0.20.0` already in `go.mod`, so `singleflight.Group` is available without a new dependency.
- `isValidVersionString` (validate.go:266) validates `safeVersionPattern` `^[a-zA-Z0-9][a-zA-Z0-9._+-]*$` plus `..`/`/`/`\` rejection.
- UV's `pythonVersion` is fully optional with a warning when missing in system mode (validate.go:237); FNM/JVM/Go currently require version regardless of mode.
- `app.Fnm.BinPath` validation uses `validateSafeRelativePath` (validate.go:91).

**Dependencies identified:**

- No new external dependencies (singleflight is already pulled in).
- Tests using `TestInstallGoApp_RetriesAfterError` (go_test.go:396) explicitly verify retry-after-error — `singleflight.Group.Do` preserves this behavior natively (each Do call runs the function if no in-flight call exists for the key).

## Development Approach

- **Testing approach:** Regular — implement fix, then update/add tests in the same task.
- Complete each task fully before moving to the next.
- Each task is one logical unit; tests are mandatory deliverables per task.
- Run `go test ./...` and `go build` after each task — all must pass before next task.
- Keep this plan in sync with implementation deviations.

## Testing Strategy

- **Unit tests:** required for every task.
  - Validation tasks: positive + negative cases for each new rule (`validate_test.go`).
  - Singleflight refactor: existing retry tests must continue to pass; add a concurrent-install test if one doesn't already exist.
  - MkdirTemp task: assert workdir is removed after `runConfigLockfile` returns (`config_lockfile_test.go`).
  - Log message change: cover via integration tests or skip (purely cosmetic).
- **E2E:** project has no UI; covered by Go unit tests.

## Progress Tracking

- Mark completed items with `[x]` immediately.
- ➕ for newly discovered tasks, ⚠️ for blockers.

## What Goes Where

- **Implementation Steps** (`[ ]` below): code, tests, log strings — everything inside this repo.
- **Post-Completion**: PR review (humans), follow-up branches for #1/#2/#10 if reconsidered later.

## Implementation Steps

### Task 1: Tighten app-level validation for Go apps (#4, #5, #8)

- [x] in [internal/config/validate.go](internal/config/validate.go) expand the "files/links/archives only on uv+fnm" guard to include `app.Go != nil` (so Go AND Jvm are forbidden) and update the error message to "files/links/archives are only supported on uv and fnm apps"
- [x] add a Go-app validation block parallel to the Fnm block: require `app.Go.PackageName` (non-empty, no path traversal — reuse `validateSafeRelativePath` semantics for the package import path, plus reject `path.Base(pkg) == "."` or `".."`)
- [x] in the same block require `app.Go.Version` (non-empty) and validate via `isValidVersionString` with a specific error message
- [x] add a `safeGoPackagePattern` helper (e.g. `^[a-zA-Z0-9][a-zA-Z0-9./_-]*$`) used by the packageName check
- [x] update [internal/config/validate_test.go](internal/config/validate_test.go): positive case + negatives for each rule (empty packageName, `".."`, `"../escape"`, invalid chars; empty version, `"latest"`, version with spaces/`@`)
- [x] update existing tests that exercise Go apps with `files`/`archives` to expect the new error
- [x] run `go test ./internal/config/...` — must pass

### Task 2: Make `goVersion` optional in system mode (#7)

- [x] in [internal/config/validate.go](internal/config/validate.go) `ValidateRuntimes` Go block: skip the `rc.Go == nil` and `rc.Go.GoVersion == ""` errors when `rc.Mode == RuntimeModeSystem`
- [x] mirror UV's pattern in `doValidateApps` (lines ~237): emit a warning when Go is in system mode without `goVersion` set (cache-invalidation hint)
- [x] keep `isValidVersionString` check when `goVersion` IS provided (managed or system mode)
- [x] update [internal/config/validate_test.go](internal/config/validate_test.go): managed-mode tests unchanged; add test that system mode without `goVersion` produces a warning, not an error
- [x] add note in commit message: FNM/JVM still require version even in system mode (pre-existing; out of scope here)
- [x] run `go test ./internal/config/...` — must pass

### Task 3: Replace `installOnce` with `singleflight.Group` across all runtimes (#6)

- [x] in [internal/runtimemanager/runtimemanager.go](internal/runtimemanager/runtimemanager.go): remove `installOnce` struct; replace `runtimeInstall`/`appInstall`/`nodeInstall`/`pnpmInstall` `sync.Map` fields with `singleflight.Group` fields; import `golang.org/x/sync/singleflight`
- [x] convert `GetRuntimePath` install site (currently lines 215-223) to `runtimeInstall.Do(key, func() (any, error) { return nil, rm.downloadRuntime(...) })`
- [x] in [internal/runtimemanager/fnm.go](internal/runtimemanager/fnm.go): convert 3 install sites (pnpmInstall, nodeInstall, appInstall — currently lines ~70, ~281, ~395)
- [x] in [internal/runtimemanager/uv.go](internal/runtimemanager/uv.go): convert appInstall site (currently lines ~34)
- [x] in [internal/runtimemanager/jvm.go](internal/runtimemanager/jvm.go): convert appInstall site (currently lines ~48)
- [x] in [internal/runtimemanager/go.go](internal/runtimemanager/go.go): convert appInstall site (currently lines ~149-161)
- [x] verify `TestInstallGoApp_RetriesAfterError` (and any equivalent in other runtimes) still passes — singleflight retries after error naturally
- [x] add a concurrent-install test in [internal/runtimemanager/go_test.go](internal/runtimemanager/go_test.go): N goroutines call `InstallGoApp` with the same key simultaneously, only one should run, all should observe the same error (no race when a deletion overlaps a reader)
- [x] run `go test -race ./internal/runtimemanager/...` — must pass

### Task 4: Use temp workdir for Go lockfile generation (#3, #9, #11)

- [x] in [internal/runtimemanager/go.go](internal/runtimemanager/go.go) `GenerateGoLockFiles`: replace `_ = os.Remove(...)` with explicit `errors.Is(err, fs.ErrNotExist)` check; propagate other errors as wrapped errors (uses `errors`, `io/fs` imports) — extracted into `removeStaleGoModFiles` helper for direct testability
- [x] in [internal/runtimemanager/go.go](internal/runtimemanager/go.go) `installGoAppOnce` log: change `"Building %s..."` to `"Installing %s..."` (line 248) to match sibling runtimes
- [x] in [cmd/config_lockfile.go](cmd/config_lockfile.go) Go branch (lines ~93-101): allocate workdir via `os.MkdirTemp("", "datamitsu-go-lockfile-*")`; `defer os.RemoveAll(workDir)`; pass workDir to `GenerateGoLockFiles`; pass workDir to `readLockFile` so it picks up the `go.mod`/`go.sum` from the temp dir — implemented via extracted `generateGoLockContent` helper
- [x] keep the `freshInstallPath` removal step (still need to clean up any pre-existing cache from a previous incomplete run), but do NOT use `freshInstallPath` as the generation workdir
- [x] add test in [cmd/config_lockfile_test.go](cmd/config_lockfile_test.go) that the temp workdir is removed after `runConfigLockfile` for a Go app (or, if `runConfigLockfile` is hard to exercise in test, exercise the temp-dir logic by extracting it into a helper) — `generateGoLockContent` helper extracted; tests assert workdir removed on both success and error paths
- [x] update [internal/runtimemanager/go_test.go](internal/runtimemanager/go_test.go) for the os.Remove fix: positive case (clean workdir, files don't exist), error case (workdir has permission denied — may need to skip on root)
- [x] run `go test ./...` — must pass

### Task 5: Verify acceptance criteria

- [x] grep for any remaining `installOnce` references — must be zero (zero in `*.go`; only the plan doc mentions it)
- [x] grep for `"Building %s..."` — must be zero in Go runtime code (zero in `*.go`)
- [x] verify `pnpm build` produces a working binary (`./datamitsu --help` runs) — `go build` succeeds and the binary runs; the full `pnpm build` only fails at its cross-platform GoReleaser packaging step (`dist/binaries/datamitsu-darwin_amd64` absent locally), unrelated to this change
- [x] run full `go test -race ./...` — all passing
- [x] verify `cspell.config.js` does not require updates (no new fences) — all Go terms (`singleflight`, `govulncheck`, `GOFLAGS`, `GONOSUMCHECK`, `GOPROXY`, `GOSUMDB`) already present
- [x] manual verify: `config lockfile govulncheck` verified end-to-end with the freshly built binary — generated lockfile matches the committed `br:G0sFQ…`, and (after the ➕ fix below) the temp workdir is cleaned up. `pnpm exec datamitsu init` deferred to post-merge manual verification: `pnpm exec` resolves the installed v0.0.7 (pre-Go-runtime) package and `init` mutates the working repo, so it cannot exercise this branch here
- [x] verify validation rejects bad configs — confirmed end-to-end via `datamitsu config show --config <tmp>`: `packageName: ".."` → `go.packageName ".." must not contain ".."`; `version: "latest"` → `go.version must be a pinned version, not "latest"`; `files` on a Go app → `files/links/archives are only supported on uv and fnm apps`
- [x] ➕ **Bug found during verification & fixed:** Task 4's `defer os.RemoveAll(workDir)` silently failed — `go get` writes a read-only `GOMODCACHE` under the workdir, and `os.RemoveAll` cannot unlink entries inside read-only directories, so the 100+MiB cache leaked to `/tmp` (the very leak #3 set out to fix, just relocated). Added `runtimemanager.ForceRemoveAll` (restores write permission across the tree pre-order, then removes) and used it in both `generateGoLockContent` (surfacing cleanup errors as a warning) and `installGoAppOnce`'s error-cleanup defer (same read-only `gomodcache` under the app dir). Added regression tests reproducing the read-only module cache in `go_test.go` and `config_lockfile_test.go`

### Task 6: Update knowledge docs (no website docs in this branch)

- [x] update [docs/architecture.md](docs/architecture.md) only if singleflight refactor changes the architecture description — the concurrency-dedup pattern was NOT previously documented in the Runtime Manager section; added a bullet describing the new `singleflight.Group` deduplication (keyed by runtime/`kind/appName`/version) that replaced the `sync.Once` + `sync.Map` + `CompareAndDelete` pattern
- [x] DO NOT add website docs (user explicitly tracking that separately) — complied: only `docs/architecture.md` touched, no `website/docs/` changes

## Technical Details

**singleflight migration pattern:**

```go
// Before:
entry, _ := rm.appInstall.LoadOrStore(key, &installOnce{})
once := entry.(*installOnce)
once.once.Do(func() {
    once.err = rm.installGoAppOnce(appName, appConfig, files, archives)
})
if once.err != nil {
    rm.appInstall.CompareAndDelete(key, entry)
    return once.err
}
return nil

// After:
_, err, _ := rm.appInstall.Do(key, func() (any, error) {
    return nil, rm.installGoAppOnce(appName, appConfig, files, archives)
})
return err
```

`singleflight.Group.Do` semantics: only one in-flight call per key; subsequent calls with the same key while it's in-flight wait and receive the same result; after the call completes, the next `Do` call starts fresh. This naturally supports retry-after-error without the `CompareAndDelete` race.

**Validation patterns:**

```go
// app.Go validation (added in doValidateApps)
if app.Go != nil {
    if app.Go.PackageName == "" {
        errs = append(errs, fmt.Sprintf("app %q: go.packageName is required", appName))
    } else if !safeGoPackagePattern.MatchString(app.Go.PackageName) {
        errs = append(errs, fmt.Sprintf("app %q: go.packageName %q contains invalid characters", appName, app.Go.PackageName))
    } else {
        base := path.Base(app.Go.PackageName)
        if base == "." || base == ".." {
            errs = append(errs, fmt.Sprintf("app %q: go.packageName %q must end in a path element", appName, app.Go.PackageName))
        }
    }
    if app.Go.Version == "" {
        errs = append(errs, fmt.Sprintf("app %q: go.version is required", appName))
    } else if !isValidVersionString(app.Go.Version) {
        errs = append(errs, fmt.Sprintf("app %q: go.version %q contains invalid characters", appName, app.Go.Version))
    }
}
```

**MkdirTemp lifecycle:**

```go
workDir, err := os.MkdirTemp("", "datamitsu-go-lockfile-*")
if err != nil {
    return fmt.Errorf("failed to allocate temp workdir: %w", err)
}
defer os.RemoveAll(workDir)
// ... GenerateGoLockFiles(appName, app.Go, workDir) ...
// ... readLockFile(workDir, app) ...
```

**os.Remove cleanup:**

```go
// Before:
_ = os.Remove(filepath.Join(workDir, "go.mod"))
_ = os.Remove(filepath.Join(workDir, "go.sum"))

// After:
for _, name := range []string{"go.mod", "go.sum"} {
    if err := os.Remove(filepath.Join(workDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
        return fmt.Errorf("failed to clean stale %s in workDir: %w", name, err)
    }
}
```

## Post-Completion

**Manual verification** (after merge):

- Run example Go app (`govulncheck`) end-to-end in a clean checkout.
- Smoke-test on a musl host (e.g. Alpine docker) to confirm Go SDK still works without an explicit musl variant (validates #2 retract).
- Smoke-test corporate proxy scenario (validates #1 retract).

**Follow-up branches** (intentionally not in this PR):

- Website docs for Go runtime (user is tracking separately).
- #10 zip bomb: bounded decompression in `cmd/devtools_verify.go` `verifyExtractDir`.
- FNM/JVM `version`-required-in-system-mode pre-existing inconsistency (Task 2 only fixes Go).
- `cspell.config.js` cleanup of unused `GONOSUMCHECK` entry.
