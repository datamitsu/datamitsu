# Fix: .datamitsuignore ignored for per-project tools (fallback resurrection)

## Overview

`dm check --explain` runs tools that a catch-all `.datamitsuignore` rule
(`**/*: <tool>`, the opt-in-tools generated file) is supposed to disable. The
ignore matcher itself is correct — it reports every listed tool as disabled for
every path, including the repo root. The bug is in task planning: when a
per-project tool is disabled for **all** of its projects, the per-project filter
loop drops every task, and a fallback then **resurrects** the tool with the full
file list, bypassing `.datamitsuignore` entirely.

Net effect: `.datamitsuignore` works only while at least one project for a tool
stays enabled. A _full_ disable (exactly what the opt-in model relies on) is
silently undone. This makes the opt-in-tools feature non-functional for every
per-project tool (eslint, oxlint, prettier, tsc, tsgo, cspell, …).

**Confirmed empirically** against the installed `v0.0.17` binary: every tool in
the ignore file triggered the fallback (`tool=eslint ALL projects filtered out …
restoring task with 4047 files`, and likewise for oxlint/prettier/tsc/tsgo/cspell).
A one-line guard on the fallback made `dm check --explain` correctly plan **zero**
tasks.

## Context (from discovery)

- **Buggy file**: `internal/tooling/planner.go`, function `createPerProjectTasksWithFiles`.
- **Two twin fallback sites** (identical anti-pattern, both restore `baseTask`
  with all files without consulting `.datamitsuignore`):
  - **Site A** — `if len(filteredLocations) == 0` (no projects of the tool's
    type remain after type/cwd filtering). Around line 624.
  - **Site B** — `if len(tasks) == 0` after the per-project loop (projects
    existed but were all filtered out — by `.datamitsuignore` in this bug).
    Around line 684. **This is the site the reported bug hits.**
- **Correct sibling code (do NOT change)**:
  - Repository scope (`collectTasks`, `case config.ToolScopeRepository`) already
    calls `p.isToolDisabledForProject(toolName, p.rootPath)` and `continue`s —
    no fallback. This is the guarantee per-project must mirror.
  - Per-file scope already calls `p.isToolDisabledForFile` and `continue`s.
  - The no-files per-project branch (`if len(files) == 0 { … }`, ~line 633-650)
    returns the filtered task list directly with **no** fallback — already correct.
- **Existing matcher API used by the fix**: `Planner.isToolDisabledForProject(toolName, absProjectDir) bool`
  (planner.go) → `datamitsuignore.Matcher.IsProjectDisabled`.
- **Test file**: `internal/tooling/tooling_test.go` (package `tooling`).
- **Test to mirror**: `TestCollectTasksRepositoryScopeRespectsDatamitsuignore`
  (line ~1120). It builds a `Planner` **struct literal** injecting `ignoreMatcher`
  directly (no disk scan), then calls `p.collectTasks(...)` and asserts task count.
  Reuse this exact shape.
- **Dependencies**: `internal/datamitsuignore` (matcher), `internal/project`
  (`ProjectLocation`), `internal/config` (tool/op config). All already imported
  in the test file.

## Development Approach

- **Testing approach**: **TDD** — within each fix task, write the regression
  test first, run it to confirm it FAILS against current code (proving the bug),
  then apply the guard and confirm it PASSES.
- Complete each task fully before moving to the next.
- Make small, focused changes — the production change is two one-line guards.
- **CRITICAL: every task with code changes ends with passing tests.** Tests are a
  required checklist item, not optional.
- **CRITICAL: all tests must pass before starting the next task** — no exceptions.
- **CRITICAL: update this plan file if scope changes during implementation.**
- Run `go test ./internal/tooling/` after each change; full `go test ./...` and
  `golangci-lint run` before finishing.
- Maintain backward compatibility — the guard only suppresses tasks that
  `.datamitsuignore` already declares disabled; nothing else changes.

## Testing Strategy

- **Unit tests** (required, this project has no e2e suite): add
  `TestCollectTasksPerProjectScopeRespectsDatamitsuignore` covering both fallback
  sites, each with a positive case (catch-all rule → 0 tasks) and a negative case
  (non-matching rule → tool still planned). Mirror the table/subtest style of the
  existing repository-scope test.
- **No UI/e2e tests** exist in datamitsu (Go CLI). The end-to-end check is a
  manual binary run, listed under Post-Completion.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]`): test + code changes inside the datamitsu repo.
- **Post-Completion** (no checkboxes): manual binary verification, release, and
  propagation to consumer repos.

## Implementation Steps

### Task 0: Create fix branch

- [x] in `/home/shibanet0/ghq/github.com/datamitsu/datamitsu`, create branch
      `fix/datamitsuignore-perproject-fallback` off the current branch
- [x] confirm working tree is clean except the in-progress plan move already staged
- [x] no commit yet (await explicit approval per repo policy)

### Task 1: Regression test + fix — per-project fallback (Site B, `len(tasks) == 0`)

- [ ] in `internal/tooling/tooling_test.go`, add
      `TestCollectTasksPerProjectScopeRespectsDatamitsuignore` with a `newPlanner(rule)`
      helper modeled on `TestCollectTasksRepositoryScopeRespectsDatamitsuignore`:
      a per-project tool (e.g. `prettier`, `Scope: config.ToolScopePerProject`,
      `Globs: []string{"**/*.js"}`), `cachedFiles: []string{"/repo/src/index.js"}`,
      `cachedProjects: []project.ProjectLocation{{Path: "/repo", Type: "node"}}`,
      `cacheInitialized: true`, `ignoreMatcher: m`
- [ ] subtest "catch-all disables per-project tool": rule `**/*: prettier` →
      `collectTasks(config.OpLint, nil)` must return **0** tasks
- [ ] run `go test ./internal/tooling/ -run TestCollectTasksPerProjectScopeRespectsDatamitsuignore -v`
      and confirm the catch-all subtest **FAILS** against current code (documents the bug)
- [ ] apply the guard at Site B in `createPerProjectTasksWithFiles`
      (planner.go ~684), immediately before `baseTask.Files = files`:
      `if p.isToolDisabledForProject(baseTask.ToolName, p.rootPath) { return nil }`
- [ ] add subtest "non-matching rule keeps per-project tool" (rule `**/*: other-tool`
      → exactly 1 task, `ToolName == "prettier"`) to lock in no-regression
- [ ] run the same test — both subtests must PASS before Task 2

### Task 2: Regression test + fix — no-locations fallback (Site A, `len(filteredLocations) == 0`)

- [ ] extend the test (or add a sibling) for the empty-locations path: same
      per-project tool with **no** `ProjectTypes`, `cachedProjects: []` (empty),
      `cachedFiles: []string{"/repo/src/index.js"}` so `findFilesByGlobs` yields a
      match but `filteredLocations` is empty → Site A fallback
- [ ] subtest "catch-all disables tool when no projects detected": rule
      `**/*: prettier` → `collectTasks` returns **0** tasks
- [ ] run the test, confirm this new subtest **FAILS** against current code
- [ ] apply the same guard at Site A (planner.go ~624), before
      `baseTask.Files = files`:
      `if p.isToolDisabledForProject(baseTask.ToolName, p.rootPath) { return nil }`
- [ ] add negative subtest (rule `**/*: other-tool` → 1 task) for Site A
- [ ] run the test — all subtests PASS before Task 3

### Task 3: Verify acceptance criteria

- [ ] re-read both fallback sites; confirm both now guard with
      `isToolDisabledForProject(baseTask.ToolName, p.rootPath)` and the
      `cwdPath != rootPath` early-return is preserved (guard goes AFTER it)
- [ ] confirm repository-scope, per-file, and no-files per-project branches are
      unchanged (no accidental edits)
- [ ] run full suite: `go test ./...` — all green
- [ ] run `golangci-lint run` — no new findings on touched files
- [ ] confirm existing ignore tests still pass:
      `go test ./internal/tooling/ -run Datamitsuignore -v`

### Task 4: [Final] Update changelog / docs

- [ ] add a CHANGELOG / release-notes entry if the repo keeps one
      (search for `CHANGELOG*`); describe the fix as
      "respect catch-all `.datamitsuignore` for per-project and no-location tools"
- [ ] if opt-in-tools docs assert full-disable works, add a regression note/link

_Note: ralphex automatically moves completed plans to `docs/plans/completed/`._

## Technical Details

### The fix (both sites, identical guard)

```go
// Site A — internal/tooling/planner.go, ~line 624
if len(filteredLocations) == 0 {
    if p.cwdPath != p.rootPath {
        return nil
    }
    // Don't resurrect a tool that .datamitsuignore disabled for the root.
    if p.isToolDisabledForProject(baseTask.ToolName, p.rootPath) {
        return nil
    }
    baseTask.Files = files
    return []Task{baseTask}
}

// Site B — internal/tooling/planner.go, ~line 684
if len(tasks) == 0 {
    if p.cwdPath != p.rootPath {
        return nil
    }
    // Don't resurrect a tool that .datamitsuignore disabled for the root.
    if p.isToolDisabledForProject(baseTask.ToolName, p.rootPath) {
        return nil
    }
    baseTask.Files = files
    return []Task{baseTask}
}
```

### Why guard on `p.rootPath`

Both fallbacks restore a single task scoped to the repo root (`baseTask.ProjectPath`
is left at root, all `files` attached). `isToolDisabledForProject(tool, rootPath)`
asks the matcher the exact question that fallback target implies — "is this tool
disabled for the root?" — via the synthetic-path check
(`IsProjectDisabled` → `IsDisabled(tool, "x")`). A `**/*: tool` rule answers yes,
so the fallback returns `nil` instead of resurrecting. Non-catch-all rules
(e.g. `**/*.md: tool`) do **not** trigger a project-level disable, so unrelated
tools are unaffected.

### Processing flow (per-project tool, catch-all ignore)

1. `collectTasks` → `ToolScopePerProject` → `findFilesByGlobs` yields matches.
2. `createPerProjectTasksWithFiles` filters projects by type/cwd → loop calls
   `isToolDisabledForProject` per project → every project `continue`s.
3. `len(tasks) == 0` → **before fix**: restore all files (bug). **After fix**:
   root is ignore-disabled → `return nil`. Tool is correctly omitted.

### Test scaffold shape (mirror of existing repo-scope test)

```go
newPlanner := func(rule string) *Planner {
    m := datamitsuignore.NewMatcher()
    if err := m.AddFile("", rule); err != nil {
        t.Fatalf("AddFile(%q) error = %v", rule, err)
    }
    return &Planner{
        rootPath:         "/repo",
        cwdPath:          "/repo",
        tools:            tools, // per-project prettier with Globs ["**/*.js"]
        cachedFiles:      []string{"/repo/src/index.js"},
        cachedProjects:   []project.ProjectLocation{{Path: "/repo", Type: "node"}}, // Site B
        cacheInitialized: true,
        ignoreMatcher:    m,
    }
}
```

## Post-Completion

_Manual / external — no checkboxes, informational only._

**Manual end-to-end verification** (already done once during diagnosis; repeat on
final binary):

- `go build -ldflags "-X github.com/datamitsu/datamitsu/internal/ldflags.Version=<ver>" -o /tmp/dm-fixed .`
- run against a consumer repo whose `.datamitsuignore` disables every tool, the
  way the real wrapper does:
  `/tmp/dm-fixed --before-config <…>/@shibanet0/datamitsu-config/datamitsu.config.js check --explain`
- expect the Execution Plan to list **no** tools (every tool is in that repo's
  `.datamitsuignore`).

**Release & propagation**:

- Tag a new datamitsu release (current installed = `v0.0.17`, which has the bug;
  HEAD/`unstable` also has it — `git diff v0.0.17 HEAD -- internal/tooling/planner.go`
  is empty).
- After release, bump the datamitsu binary used by `@shibanet0/datamitsu-config`
  and by consumer repos so the opt-in `.datamitsuignore` actually disables tools
  there.

**Note on consumer repos**: a consumer's `.datamitsuignore` may currently be
**untracked** (`git status` shows it not added). Once the datamitsu fix ships,
also `git add` the file so it is committed alongside the opt-in setup.
