# Unstable Version Bypass

## Overview

- Unstable builds (`0.0.0-unstable.DATE.SHA`) cannot load any user config because semver treats prerelease versions as lower than stable (`v0.0.0-unstable < v0.0.0`)
- Fix: when the current binary version contains `-unstable`, log a warning and skip the version check instead of returning a hard error
- This matches the principle that unstable users accept risk — the version gate is advisory, not blocking

## Context (from discovery)

- Files/components involved:
  - `internal/version/check.go` — `CompareVersions()` + `normalizeVersion()`
  - `internal/version/check_test.go` — stable version tests
  - `internal/version/check_unstable_test.go` — existing tests documenting the bug
  - `cmd/config_loader.go:248` — sole caller of `CompareVersions()`
  - `internal/ldflags/ldflags.go` — `Version` variable (`"dev"` default, overridden via `-ldflags`)
  - `.goreleaser.yml:47` — ldflags injection
  - `.github/workflows/release.yml:204` — unstable version format: `0.0.0-unstable.${DATE}.${SHORT_SHA}`
- Related patterns found:
  - `"dev"` version is already special-cased in `normalizeVersion()` → `"v0.0.0"`
  - `cmd/config_loader.go` already uses `logger.Logger.Warn()` for non-fatal config issues
- Dependencies: `golang.org/x/mod/semver`, `go.uber.org/zap` (via `internal/logger`)

## Development Approach

- **Testing approach**: TDD — update tests first, then implement
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**

## Testing Strategy

- **Unit tests**: `internal/version/check_test.go` and `check_unstable_test.go`
- **Integration tests**: `cmd/config_loader_test.go` — test that unstable version + config with minVersion logs warning and proceeds

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Change CompareVersions to return warning info for unstable versions (TDD)

- [x] Update tests in `internal/version/check_unstable_test.go`: unstable current + stable required should return `nil` error (bypass), not an error
- [x] Add test: `CompareVersions("0.0.0-unstable.20260523.abc", "0.0.0")` returns `nil` (passes)
- [x] Add test: `CompareVersions("0.0.0-unstable.20260523.abc", "0.1.0")` returns `nil` (passes — unstable users accept risk)
- [x] Add test: stable versions still behave as before (no regression)
- [x] Add exported function or return type that signals "bypassed due to unstable" so caller can log a warning
- [x] Run tests — must pass before next task

### Task 2: Implement unstable bypass in CompareVersions

- [ ] Modify `CompareVersions` in `internal/version/check.go`: if `current` contains `-unstable`, return a signal (not error) indicating the check was bypassed
- [ ] Design decision: either (a) add a second return value `(error, bool)` where `bool` = skipped, or (b) return a sentinel type `VersionSkipped` that the caller can type-assert
- [ ] Ensure `normalizeVersion` is unchanged — the bypass happens in `CompareVersions` logic, not normalization
- [ ] Run tests — must pass before next task

### Task 3: Update config_loader.go to log warning on unstable bypass

- [ ] Update call site at `cmd/config_loader.go:248` to handle the new bypass signal
- [ ] Log warning via `logger.Logger.Warn()` with message like: "version check skipped: current version is unstable (0.0.0-unstable.xxx), config requires 0.0.1 — proceeding at your own risk"
- [ ] Write test in `cmd/config_loader_test.go`: unstable ldflags.Version + config with getMinVersion > "0.0.0" should succeed (no error returned)
- [ ] Write test: stable ldflags.Version + config with getMinVersion > current should still fail
- [ ] Run tests — must pass before next task

### Task 4: Clean up check_unstable_test.go

- [ ] Remove or update diagnostic tests that were documenting the bug (they now test the fix)
- [ ] Ensure test names clearly describe expected behavior, not the bug
- [ ] Run full test suite (`go test ./...`)
- [ ] Run linter if configured

### Task 5: Update documentation

- [ ] Update `docs/architecture.md` version requirement section (~line 357) to mention unstable version bypass behavior
- [ ] Update error message in `CompareVersions` if it still references upgrade instructions — unstable users shouldn't see "run go install @latest"

## Technical Details

**Current flow:**

```
ldflags.Version = "0.0.0-unstable.20260523.abc"
  → normalizeVersion → "v0.0.0-unstable.20260523.abc"
  → semver.Compare("v0.0.0-unstable.20260523.abc", "v0.0.0") → -1
  → ERROR: "requires datamitsu v0.0.0 or higher"
```

**Fixed flow:**

```
ldflags.Version = "0.0.0-unstable.20260523.abc"
  → detect "-unstable" in current version
  → WARNING: "version check skipped: unstable build, config requires v0.0.0"
  → continue loading config (no error)
```

**Return type options for CompareVersions:**

Option A — second return value:

```go
func CompareVersions(current, required string) (skipped bool, err error)
```

Option B — sentinel error type (preferred, no API breakage for callers that only check `err != nil`):

```go
type VersionSkipped struct { Current, Required string }
func (v *VersionSkipped) Error() string { ... }
// Caller: if errors.As(err, &skipped) { warn } else if err != nil { fail }
```

## Post-Completion

**Manual verification:**

- Build with unstable version ldflags and verify config loads with warning
- Test with `go run -ldflags "-X github.com/datamitsu/datamitsu/internal/ldflags.Version=0.0.0-unstable.test" .`
