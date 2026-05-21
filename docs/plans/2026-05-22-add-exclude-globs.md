# Add excludeGlobs to ToolOperation

## Overview

- Add `excludeGlobs` optional field to `ToolOperation` for explicit file exclusion from tool execution
- Fix misleading JSDoc that claims "gitignore-style patterns" — the `doublestar` library doesn't support `!` negation
- Make `globs` optional — Go code already handles empty globs correctly (treats as "all files")
- Provides a clean, explicit exclusion mechanism instead of overloading `globs` with negation syntax

## Context (from discovery)

- Files/components involved:
  - `internal/config/config.go:50-60` — Go `ToolOperation` struct
  - `config/config.d.ts:524-615` — TypeScript type definition with JSDoc
  - `internal/tooling/planner.go:288-358` — `collectTasks` with 4 scope branches using globs
  - `internal/tooling/planner.go:702-723` — `filterFilesByGlobs` function
  - `internal/tooling/planner.go:725-773` — `HasOverlap` / `globsOverlap` functions
  - `internal/tooling/plan_formatter.go:42-52` — `TaskJSON` struct
  - `internal/tooling/tooling_test.go` — existing glob tests at line 624 and 1972
  - `website/docs/guides/tooling-system.md` — tooling documentation
- Related patterns: `.datamitsuignore` has negation support via custom parser (different layer — disables tools per-file, not file filtering)
- Glob library: `github.com/bmatcuk/doublestar/v4` — supports `*`, `**`, `?`, `[...]`, `{alt1,alt2}` but NOT `!` negation

## Development Approach

- **Testing approach**: TDD — write failing tests first, then implement
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy

- **Unit tests**: `TestExcludeFilesByGlobs` for the new function, integration tests for `collectTasks` with excludeGlobs
- **Existing tests**: `TestFilterFilesByGlobs`, `TestGlobsOverlap` must continue passing unchanged

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Write tests for excludeFilesByGlobs (TDD - red phase)

- [x] add `TestExcludeFilesByGlobs` in `internal/tooling/tooling_test.go` near `TestFilterFilesByGlobs` (line 624)
- [x] test cases: nil excludes (no-op), empty excludes (no-op), single pattern, multiple patterns, exclude all files
- [x] run tests — new tests should fail (function doesn't exist yet)

### Task 2: Implement excludeFilesByGlobs (TDD - green phase)

- [x] add `ExcludeGlobs []string` field to `ToolOperation` struct in `internal/config/config.go:55` (after Globs)
- [x] add `omitempty` to existing `Globs` field json tag
- [x] add `excludeFilesByGlobs(files, excludeGlobs)` function in `internal/tooling/planner.go` after `filterFilesByGlobs` (line 723)
- [x] run tests — `TestExcludeFilesByGlobs` should pass now

### Task 3: Write integration tests for collectTasks with excludeGlobs (TDD - red phase)

- [x] add test: `globs + excludeGlobs` together produces correct filtered file set in collectTasks
- [x] add test: empty `Globs` (nil) with `ExcludeGlobs` still works (backward compat)
- [x] run tests — new integration tests should fail
      <!-- cspell:disable-next-line -->
      или ну типа то можно ли это но наверное это можно сделать одним и тем же но как будто бы кажется вот что-то мне кажется что нужно делать типы вайки у ямал формате формат яму дату вайки у формат джессон уайки у формат h7 ну типа что-то в таком духе кажется нужно делать чтобы иметь возможность тюнить это под каждый конкретный формат соответствии с тем как там нужны и что нужно подтянуть

### Task 4: Integrate excludeFilesByGlobs into collectTasks (TDD - green phase)

- [x] call `excludeFilesByGlobs` in repository scope branch (after line 293)
- [x] call `excludeFilesByGlobs` in per-project scope branch (after line 310, after `filterFilesToCwd`)
- [x] call `excludeFilesByGlobs` in per-file scope branch (after line 326, after `filterFilesToCwd`)
- [x] call `excludeFilesByGlobs` in default scope branch (after line 347, after `filterFilesToCwd`)
- [x] add comment to `HasOverlap` explaining excludeGlobs are intentionally not considered (conservative)
- [x] run tests — all integration tests should pass

### Task 5: Update JSON formatter

- [x] add `ExcludeGlobs []string` to `TaskJSON` struct in `internal/tooling/plan_formatter.go:49`
- [x] populate `ExcludeGlobs` in Format method at line 323
- [x] run tests — must pass

### Task 6: Update TypeScript definition

- [x] fix JSDoc for `globs`: replace "gitignore-style patterns" with "doublestar glob syntax", note `!` not supported
- [x] make `globs` optional: `globs?: string[]`
- [x] add `excludeGlobs?: string[]` field with JSDoc (after `globs`, before `invalidateOn`)
- [x] update scope examples in JSDoc to show `excludeGlobs` usage

### Task 7: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] verify edge cases: nil globs, nil excludeGlobs, overlapping patterns
- [ ] run full test suite (`go test ./...`)
- [ ] run linter if configured
- [ ] verify existing tests unchanged and passing

### Task 8: [Final] Update documentation

- [ ] add "Exclude Patterns" section in `website/docs/guides/tooling-system.md` with example
- [ ] update glob description to say "doublestar syntax" instead of "gitignore-style"

## Technical Details

**Filtering order:** `globs` (include) → `excludeGlobs` (exclude) → `filterFilesToCwd` (scope restriction) → `.datamitsuignore` (tool disable)

**excludeGlobs semantics:**

- Files matching ANY `excludeGlobs` pattern are removed from the matched set
- Empty/nil `excludeGlobs` = no exclusion (backward compatible)
- Uses same `doublestar.Match()` as `globs`

**Overlap detection:** `excludeGlobs` intentionally NOT considered — two tools with same `globs` but different `excludeGlobs` are treated as overlapping (conservative, correct for safety)

## Post-Completion

**Manual verification:**

- Test with a real config that uses `excludeGlobs` via `datamitsu explain`
- Verify JSON output includes `excludeGlobs` when set
