# Fix Detector: Non-Executable File Filtering

## Overview

The asset detector in `internal/detector/` incorrectly selects `.vsix` (VS Code
extension) files — and potentially other non-executable formats such as `.deb`,
`.rpm`, `.nupkg`, `.whl` — as binary candidates when processing GitHub release
assets via `devtools pull-github`.

**Root cause (tombi-toml/tombi example):**
The only Linux binary in the tombi release is a musl variant. On a glibc host
the scoring gives it a libc-mismatch penalty (+1 instead of +5), while the
`.vsix` file has no libc indicator (neutral +5) and ends up 2 points ahead:

| Asset                                          | OS    | Arch | Libc          | Archive | Total      |
| ---------------------------------------------- | ----- | ---- | ------------- | ------- | ---------- |
| `tombi-cli-…-x86_64-unknown-linux-musl.tar.gz` | +1000 | +100 | +1 (mismatch) | +2      | **1103**   |
| `tombi-vscode-…-linux-x64.vsix`                | +1000 | +100 | +5 (neutral)  | 0       | **1105** ✗ |

**Fix:** add an exclusion list of known non-executable extensions and filter them
out in `filterValidAssets`, the same way checksum files are already filtered.

## Context (from discovery)

- Files involved: `internal/detector/patterns.go`, `detect.go`,
  `patterns_test.go`, `scoring_test.go`
- Related patterns: `ChecksumExtensions` / `IsChecksumFile` in `patterns.go`;
  `filterValidAssets` in `detect.go`
- Dependency: none — detector package is self-contained

## Development Approach

- **Testing approach**: TDD — failing test first, then implementation
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- Run `go test ./internal/detector/...` after each change

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## What Goes Where

- **Implementation Steps**: code changes and tests in this repo
- **Post-Completion**: manual verification on real releases

## Implementation Steps

### Task 1: Add failing regression test for vsix filtering

- [x] add `TestDetectBinary_FiltersVsix` in `scoring_test.go` (or
      `patterns_test.go`) that asserts `DetectBinary` returns an error (no match)
      when the only assets are `.vsix` files matching the requested OS/arch
- [x] add `TestDetectBinary_PrefersArchiveOverVsix` that asserts the real
      `.tar.gz` binary is selected over a matching `.vsix` when both are present
- [x] run `go test ./internal/detector/...` — tests MUST fail at this point
      (red)

### Task 2: Implement NonExecutableExtensions and IsNonExecutableFile

- [x] add `NonExecutableExtensions` slice to `internal/detector/patterns.go`
      with: `.vsix`, `.deb`, `.rpm`, `.nupkg`, `.whl`
- [x] add `IsNonExecutableFile(filename string) bool` function alongside
      `IsChecksumFile`
- [x] add unit tests for `IsNonExecutableFile` in `patterns_test.go` (success +
      false-positive cases, case-insensitive)
- [x] run `go test ./internal/detector/...` — new unit tests must pass; failing
      regression tests from Task 1 still fail

### Task 3: Wire IsNonExecutableFile into filterValidAssets

- [x] update `filterValidAssets` in `internal/detector/detect.go` to also
      reject assets where `IsNonExecutableFile(asset.Name)` is true
- [x] run `go test ./internal/detector/...` — ALL tests must now pass (green)

### Task 4: Verify acceptance criteria

- [x] confirm `TestDetectBinary_FiltersVsix` passes
- [x] confirm `TestDetectBinary_PrefersArchiveOverVsix` passes
- [x] run full test suite `go test ./...` — no regressions
- [x] run linter `golangci-lint run ./internal/detector/...` (if available)

### Task 5: [Final] Update documentation

- [x] update CLAUDE.md / agents-docs-website.md if the detector behaviour is
      documented there (check for any mention of filterValidAssets or
      ChecksumExtensions)

## Technical Details

**New additions to `patterns.go`:**

```go
var NonExecutableExtensions = []string{
    ".vsix",  // VS Code extension
    ".deb",   // Debian package
    ".rpm",   // RPM package
    ".nupkg", // NuGet package
    ".whl",   // Python wheel
}

func IsNonExecutableFile(filename string) bool {
    lowerName := strings.ToLower(filename)
    for _, ext := range NonExecutableExtensions {
        if strings.HasSuffix(lowerName, ext) {
            return true
        }
    }
    return false
}
```

**Updated `filterValidAssets` in `detect.go`:**

```go
func filterValidAssets(assets []github.Asset) []github.Asset {
    var valid []github.Asset
    for _, asset := range assets {
        if !IsChecksumFile(asset.Name) && !IsNonExecutableFile(asset.Name) {
            valid = append(valid, asset)
        }
    }
    return valid
}
```

## Post-Completion

**Manual verification:**

- run `datamitsu devtools pull-github` against a config referencing
  `tombi-toml/tombi` and confirm the selected asset is `tombi-cli-…tar.gz`,
  not a `.vsix`
