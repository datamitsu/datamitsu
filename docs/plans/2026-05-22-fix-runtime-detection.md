# Fix Runtime Detection (FNM + JVM)

## Overview

- Fix two bugs in `pull-runtimes` devtools command that produce incorrect runtime configurations
- **FNM:** darwin/arm64 is lost because arch-unspecified macOS assets only match amd64 via implicit rule
- **JVM:** wrong assets selected (debugimage, static-libs-glibc) instead of actual JDK binaries
- Both bugs result in broken `runtimes.json` output that fails at install time

## Context (from discovery)

- Files/components involved:
  - `internal/detector/scoring.go` — implicit arch matching logic
  - `internal/detector/scoring_test.go` — tests for scoring
  - `cmd/devtools_pull_runtimes.go` — `detectJVMBinaries()` function
  - `cmd/devtools_pull_runtimes_test.go` — tests for pull-runtimes
  - `internal/detector/detect.go` — `DetectBinary()` entry point
- Related patterns: scoring-based asset selection, platform tuple iteration, deduplication logic
- Dependencies: `internal/syslist`, `internal/github`, `internal/binmanager`

## Development Approach

- **Testing approach**: TDD — write failing tests first, then implement fixes
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy

- **Unit tests**: required for every task
- Test real-world asset names from FNM and Temurin releases
- Verify both positive cases (correct selection) and negative cases (wrong assets rejected)

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Write failing test for FNM darwin/arm64 (TDD red)

- [x] add test case in `internal/detector/scoring_test.go`: `fnm-macos.zip` with `darwin/arm64` should match (OSMatch=true, ArchMatch=true, Total>0)
- [x] add test case: `fnm-macos.zip` with `darwin/amd64` still matches (regression guard)
- [x] add test case: asset with explicit arch like `tool-darwin-arm64.tar.gz` still preferred over arch-unspecified for arm64
- [x] run tests — new darwin/arm64 test must FAIL (proves the bug exists)

### Task 2: Fix implicit arm64 matching for macOS in scorer

- [x] in `internal/detector/scoring.go` `ScoreAsset()`: add rule after implicit amd64 — when OS matches darwin, no arch indicator present, and requesting arm64, set OSMatch=true and ArchMatch=true
- [x] run tests — all tests must pass including new darwin/arm64 test

➕ Discovered: explicit OS+arch matches and implicit matches can tie on Total score, allowing implicit asset (e.g. `fnm-macos.zip`) to alphabetically beat an explicit asset (e.g. `tool-darwin-arm64.tar.gz`). Added `IsExplicit` field to `AssetScore` and use it as a tiebreaker before alphabetical sort so explicit matches always win on ties.

### Task 3: Write failing test for JVM asset pre-filtering (TDD red)

- [ ] add test in `cmd/devtools_pull_runtimes_test.go` (or appropriate location): given a list of Temurin assets (jdk, debugimage, static-libs-glibc, jre, sbom), `detectJVMBinaries()` must select the `-jdk_` asset for each platform
- [ ] verify test FAILS with current code (proves wrong asset selected)

### Task 4: Pre-filter JVM assets to only JDK type

- [ ] in `cmd/devtools_pull_runtimes.go` `detectJVMBinaries()`: before the platform loop, filter `release.Assets` to only include assets containing `-jdk_` in their name (case-insensitive)
- [ ] add helper function `filterJDKAssets(assets []github.Asset) []github.Asset` for clarity
- [ ] write unit test for `filterJDKAssets` with representative Temurin asset names
- [ ] run tests — all tests must pass

### Task 5: Verify acceptance criteria

- [ ] verify FNM: `fnm-macos.zip` matches both darwin/amd64 AND darwin/arm64
- [ ] verify JVM: only `-jdk_` assets are selected for all platforms
- [ ] verify no regressions for UV runtime detection (uses same `detectRuntimeBinaries`)
- [ ] run full test suite (`go test ./...`)
- [ ] run linter if configured

### Task 6: [Final] Update documentation

- [ ] no documentation changes expected unless implementation diverges

## Technical Details

### FNM scorer fix

In `ScoreAsset()`, the implicit matching chain becomes:

```
1. osMatch && !hasArchIndicator && archType == amd64  → match (existing)
2. osMatch && !hasArchIndicator && osType == darwin && archType == arm64  → match (NEW)
3. archMatch && !hasOSIndicator && osType == linux  → match (existing)
4. else → standard explicit matching
```

### JVM pre-filter

Temurin naming convention: `OpenJDK{ver}U-{type}_{arch}_{os}_hotspot_{version}.{ext}`
Where `{type}` is one of: `jdk`, `jre`, `debugimage`, `static-libs`, `static-libs-glibc`, `jmods`, `sbom`, `sources`

Filter rule: keep only assets where name contains `-jdk_` (the hyphen-jdk-underscore pattern uniquely identifies the JDK product type).

## Post-Completion

**Manual verification:**

- Run `datamitsu devtools pull-runtimes --update --dry-run config/src/runtimes.json` and verify output contains correct darwin/arm64 for FNM and `-jdk_` URLs for JVM
