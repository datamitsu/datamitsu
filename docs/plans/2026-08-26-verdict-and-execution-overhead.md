# Plan: Cut the per-run CPU that decides not to run anything

**Status:** ready for implementation. The measurements are recorded; two of the four tasks have a
gate that may close them, and that is deliberate.
**Date:** 2026-08-26.
**Related:** `internal/tooling/verdict.go`, `internal/tooling/executor.go`, `internal/bundled`,
`internal/parsermanager`, `internal/cache`, `internal/runner`.
**Follows:** `docs/plans/2026-08-26-planner-and-startup-cost.md` (phase 1, landed) and
`docs/plans/2026-08-26-config-eval-cache.md` (phase 2).

**Branch:** `perf/verdict-and-execution-overhead`, branched from `perf/config-eval-cache` (or from
`main` once the stack has merged). Review base ref: the branch below it in the stack while it is
unmerged, `main` after.

## Overview

Phase 1 removed the planner's cost and phase 2 removes the config load's. What is left on a warm
`lint` is the work the executor does to decide it does not need to run anything.

Measured on a 14,711-file monorepo, i9-14900K, warm caches, `datamitsu lint` after phase 1
(`DATAMITSU_TRACE=1`):

| Span / counter                 | n       | Total       | Note                                       |
| ------------------------------ | ------- | ----------- | ------------------------------------------ |
| `executeTask`                  | 120     | 5.036 s     | concurrent; wall for the phase is 2.66 s   |
| **`verdictKeys`**              | 120     | **3.055 s** | max 572 ms for a single task               |
| `spawn`                        | 3       | 1.978 s     | the only real tool work — 117 cache hits   |
| `getCommandInfo`               | 120     | 118.6 ms    | ~10 distinct apps                          |
| `walk.repository_walks`        | 2       | —           | 3 on `check`                               |
| `parser.module_instantiations` | =parses | —           | a fresh wazero instance per parse, ~230 µs |

Read the first two rows together: **three seconds of CPU went into deciding to skip tools whose own
work would have been two seconds.** `verdictInputs` reads and XXH3-hashes every file of a unit, and
`recordVerdict` hashes them all again afterwards. A file inside a package is hashed once per
per-project tool — four to six times per run on this repository.

This plan does not assume the verdict cache is wrong. It makes the trade visible per tool first
(Task 1), then removes the duplication that is unambiguously waste (Tasks 2–3), then decides.

## Context (from discovery)

- `internal/tooling/verdict.go:93` `verdictInputs(members, guards, root)` builds the cache value.
- `internal/tooling/verdict.go:111` `hashedPaths` uses `os.ReadFile` + `hashutil.XXH3Hex` per path —
  whole-file reads, not streaming, not mtime-gated.
- `internal/tooling/executor.go:462` `verdictKeys` is called before the task runs;
  `internal/tooling/verdict.go:336` `recordVerdict` calls `verdictInputs` **again** after it, to
  detect inputs that moved underneath the tool. For a read-only operation that second pass is the
  only thing standing between a pass and a lie, so it cannot simply be deleted.
- `internal/tooling/verdict.go:144` `unitMembers` for a unit rooted at the repository root is
  **every tracked file**. Repo granularity is opt-in for exactly this reason
  (`internal/tooling/executor.go`, `verdict.go:311`); unit granularity is not.
- `internal/binmanager/binmanager.go:484` `GetCommandInfo` resolves a binary path, may install, and
  merges app env into a fresh `CommandInfo` per call. `EnsureTools` has already installed everything
  in the plan before the executor runs, so within one `Execute` the answer is constant.
- `internal/bundled/datamitsuignore.go:26` `FindIgnoreFiles` walks the whole tree. `RunFix` and
  `RunLint` each call it (`internal/runner/runner.go:931`, `:935`), **before** the planner builds its
  own file list — so `check` pays three walks and `lint` two.
- `internal/parsermanager/parsermanager.go:115` `Acquire` instantiates a fresh wazero module per
  call. Compilation is already shared and prewarmed off the critical path (phase 1).
- `internal/cache/cache.go:145`/`:209` — `Load` decodes the whole msgpack file; `Save` calls
  `mergeFromDisk` (a second full decode), marshals and rewrites. `debounceSave`
  (`internal/cache/cache.go:656`) resets a 100 ms timer on every `MarkDirty`, so a busy run may save
  only once at `Shutdown` — or may not.
- `internal/trace` (phase 1) already carries `cache.file_hashes`, `exec.verdict_cache_hits` and
  `exec.processes_spawned`, so the before/after numbers for this plan need no new harness.

## Development Approach

- **Testing approach**: measurement-first for Task 1; regular (code first, then tests) after.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** for the code it changes. Tests are listed
  as separate checklist items, never bundled into an implementation item.
- **CRITICAL: all tests must pass before starting the next task.** Run `go test ./...` and
  `go test ./test/cli/ -count=2`.
- **CRITICAL: run the test suite with `umask 022`.** With `umask 002` the source-mode tests fail on
  the _test binary's own file mode_. That failure is environmental; do not "fix" it in the source.
- **CRITICAL: update this plan file when scope changes during implementation.**
- **CRITICAL: a skip must never become wrong.** Every change here touches the decision to not run a
  tool. A cache that is 10% faster and skips one tool it should have run is a regression, not an
  optimisation. Where a task trades certainty for speed, it says so and gates on a test that proves
  the trade.

## Testing Strategy

- **Unit tests**: required for every task. Stdlib `testing` only, table-driven, `t.TempDir`/
  `t.Setenv` — no testify.
- **Differential tests**: for every change to verdict inputs, the verdict computed the new way must
  equal the verdict computed the old way for a fixture tree, including after a file is touched,
  rewritten with identical bytes, and rewritten with different bytes.
- **Blackbox CLI tests**: `go test ./test/cli/ -count=2` with zero regenerated goldens.
- **Benchmarks**: commit them; they are the regression guard.
- **No e2e**: the OCI tier is untouched.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Record the measured before/after number for each performance task directly in this file.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, benchmarks and docs in this repository.
- **Post-Completion** (no checkboxes): measurements to re-run on an idle machine, and follow-up work
  this plan deliberately does not do.

## Implementation Steps

### Task 1: Make the verdict cache's trade visible per tool

Before making the hashing cheaper, establish whether it is worth doing at all for each tool. The
answer plausibly differs: for a tool that runs in 40 ms over a unit of 3,000 files, hashing those
files twice is pure loss.

- [x] add a `bytes_hashed` counter and a per-task attribute to the existing `verdictKeys` span
      recording the member count, the guard count and the total bytes read
- [x] add a span attribute recording whether the verdict was a hit or a miss
- [x] produce a table, per tool, of `verdictKeys` time against that tool's own `spawn` duration on a
      cold run and on a warm run, and record it in this file
- [x] record the baseline: `verdictKeys` total, `cache.file_hashes`, and `lint` wall-min (n≥9)
- [x] add `BenchmarkVerdictInputs` over a fixture unit of ~2,000 files so Tasks 2 and 4 have a target
- [x] write a test asserting the new counter and attributes are absent when tracing is off
- [x] run `go test ./...` and `go test ./test/cli/ -count=2` — must pass before Task 2

#### Task 1 measurements

Recorded on **this repository** (1,087 tracked files, i9-14900K, `DATAMITSU_TRACE=1`), not on the
14,711-file monorepo of the Overview — that machine is not available to the implementation. The
absolute numbers are therefore an order of magnitude smaller; the _shape_ is what carries over, and
the ratio per tool is the thing Task 4 has to read.

Per tool, `verdictKeys` span total (summed over its tasks, concurrent) against that tool's own
`spawn` total. Tools whose `verdictKeys` is 0.00 ms are ones the cache does not apply to (repo
granularity without `cache: true`, or file granularity); they are listed to show the cache is not
paying for them.

| Tool                 |   n | `verdictKeys` | bytes hashed | hits (warm) | `spawn` cold | `spawn` warm |
| -------------------- | --: | ------------: | -----------: | ----------: | -----------: | -----------: |
| cspell               |  11 |       25.0 ms |      20.5 MB |        9/11 |     6,244 ms |       879 ms |
| tsc                  |   5 |       16.6 ms |      15.2 MB |         5/5 |     2,040 ms |         0 ms |
| oxlint               |   6 |       15.8 ms |      16.0 MB |         5/6 |       318 ms |        55 ms |
| tsgo                 |   4 |        9.9 ms |      13.5 MB |         4/4 |       299 ms |         0 ms |
| golangci-lint        |   1 |       11.9 ms |      11.2 MB |         1/1 |     2,913 ms |         0 ms |
| rustfmt              |   1 |        1.2 ms |       1.1 MB |         1/1 |         0 ms |         0 ms |
| eslint               |   6 |       0.00 ms |            0 |           — |    12,005 ms |         0 ms |
| prettier             |   6 |       0.00 ms |            0 |           — |     3,625 ms |         0 ms |
| shellcheck           |  11 |       0.01 ms |            0 |           — |       206 ms |         0 ms |
| gitleaks             |   1 |       0.00 ms |            0 |           — |       904 ms |       888 ms |
| editorconfig-checker |   1 |       0.00 ms |            0 |           — |       238 ms |       226 ms |

**Baseline (warm `lint`, this repository):**

| Metric                       |                                     Value |
| ---------------------------- | ----------------------------------------: |
| `verdictKeys` total          |               71.7 ms warm / 78.3 ms cold |
| `cache.verdict_bytes_hashed` |              77.5 MB warm / 131.5 MB cold |
| `cache.file_hashes`          |                                       657 |
| `exec.verdict_cache_hits`    |                                 25 (warm) |
| `exec.processes_spawned`     |                          7 warm / 80 cold |
| `walk.repository_walks`      |                                         2 |
| `lint` wall-min (n=9)        |                                    1.89 s |
| `BenchmarkVerdictInputs`     | 5.98 ms/op, 685 MB/s (2,000 × 2 KB files) |

**Two readings that matter for the later tasks.**

1. _The trade is currently positive here._ Every tool the cache applies to costs 1–25 ms of hashing
   and saves hundreds to thousands of milliseconds of spawn. On this repository Task 4's gate is
   already met (71.7 ms ≪ 300 ms); the 3,055 ms of the Overview is a property of a 14× larger tree,
   where `verdictKeys` scales with bytes and `spawn` does not scale as fast.
2. _The post-run re-hash is half the volume._ Cold: 131.5 MB counted in total against 77.5 MB
   attributed to `verdictKeys` spans — the missing 54 MB is `recordVerdict` hashing the same files a
   second time. Warm the two are equal, because a hit never reaches `recordVerdict`. That is Task 2's
   target, stated as a number: **the second pass is ~41% of all verdict bytes on a cold run and 0% on
   a fully warm one.**

### Task 2: Stop hashing the same file twice per task

`recordVerdict` recomputes the entire input vector after the run. That second pass exists to catch
inputs that moved while the tool ran, and for a read-only operation it is load-bearing — but it is
only load-bearing for files that _could_ have moved.

- [ ] for `OpLint` (read-only), replace the full re-hash with a cheap staleness probe: re-stat every
      member and guard and re-hash only the paths whose size or modification time changed since the
      pre-run pass, which requires recording those stats during the first pass
- [ ] a path that cannot be stat'ed, or whose stat is unchanged but whose mtime granularity makes
      the comparison unsafe, must fall back to re-hashing — never to assuming unchanged
- [ ] leave `OpFix` on the full re-hash: a fixer rewrites files by design, mtime granularity is
      exactly the case that bites, and the existing branch already treats a fix specially
- [ ] write a differential test: for a fixture unit, the pre/post comparison must reach the same
      verdict as a full re-hash — unchanged, one file touched (mtime only, same bytes), one file
      rewritten with different bytes, one file deleted, one guard appearing
- [ ] write a test asserting a file rewritten within the same mtime tick is still detected (write,
      stat, rewrite with different length — length alone must catch it)
- [ ] write a test asserting `OpFix` still performs the full re-hash
- [ ] record the measured drop in `verdictKeys` total and in `cache.file_hashes`
- [ ] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 3

### Task 3: Hash each file once per process, not once per tool

A file in a package is hashed by every per-project tool planning a task there. The content of a file
does not depend on which tool is asking.

- [ ] add a process-scoped content-hash memo keyed by path, **validated** by size and modification
      time — a cache of a pure function with a cheap validity check, not an assumption
- [ ] the memo must be shared across tasks and safe under the executor's worker pool (`-race`)
- [ ] `recordVerdict`'s post-run probe must bypass the memo, or the check it performs becomes
      tautological — this is the single most important line in the task
- [ ] write a test asserting two tools over the same unit read each file once
- [ ] write a test asserting a file rewritten between two tasks is re-hashed (change the length)
- [ ] write a concurrency test (`-race`) hammering the memo from 16 goroutines
- [ ] write a test asserting the post-run probe does not consult the memo
- [ ] record the measured drop in `cache.file_hashes` and in `lint` wall-min
- [ ] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 4

### Task 4: Decide the unit-granularity default from Task 1's table

- [ ] re-read Task 1's table after Tasks 2 and 3. **If `verdictKeys` is now under 300 ms total,
      mark this task skipped with a note and move to Task 5** — do not add a policy knob for a cost
      that no longer exists
- [ ] if it is still material: propose (in this file, for the owner to approve — do not implement
      unilaterally) either a size threshold above which a unit-granularity verdict is not attempted,
      or making unit granularity opt-in the way repo granularity already is
- [ ] whatever is chosen must keep `--explain` honest: a tool whose verdict was not attempted is
      not the same as a tool that was skipped, and the two must not print the same

### Task 5: The cheap, unambiguous duplication

Three independent items, none of which changes a decision.

- [ ] memoize `GetCommandInfo` per app inside the executor for the lifetime of one `Execute`, handing
      out copies rather than the shared pointer; first verify by grep that no caller mutates
      `CommandInfo.Env` or its slices
- [ ] have the runner call `bundled.FindIgnoreFiles` once and pass the result to both `RunFix` and
      `RunLint`, instead of each walking the tree — `check` drops from three walks to two
- [ ] pool wazero parser instances instead of instantiating one per parse, keeping one instance per
      module per worker; an instance must be reset or discarded between parses so no state leaks
      between two tools' output
- [ ] write a test asserting `GetCommandInfo` is called once per distinct app across a multi-task plan
- [ ] write a test asserting a returned `CommandInfo` cannot be mutated by one caller into another's
      view
- [ ] write a test asserting `check` performs exactly two repository walks (assert on the
      `walk.repository_walks` counter)
- [ ] write a test asserting two parses through a pooled instance produce the same diagnostics as two
      parses through fresh instances — including a parse that errors between them
- [ ] record the measured drop in `getCommandInfo` total, walk count, and `parser.instantiate` total
- [ ] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 6

### Task 6: Verify acceptance criteria

- [ ] `verdictKeys` total on a warm `lint` of a large repository is ≤ 600 ms (baseline 3,055 ms)
- [ ] `cache.file_hashes` on that run is at most the number of distinct files in the planned units
      (each file hashed once), plus the post-run probes
- [ ] `lint` wall-min improves and `check` wall-min improves; record both with n≥9
- [ ] **the set of tools that ran is identical to the baseline** on a cold cache, a warm cache, and a
      run where one file changed — compare `--explain=json` and the run summary against the
      pre-change binary. This is the criterion that matters; the timings are secondary
- [ ] `go test ./...` passes with `-race` (under `umask 022`)
- [ ] `go test ./test/cli/ -count=2` passes with **zero** regenerated goldens
- [ ] `pnpm dm check` passes
- [ ] coverage meets the project standard via `pnpm test:coverage:all`

### Task 7: [Final] Update documentation

- [ ] record the final measurement table in this plan file, replacing the estimates
- [ ] document the content-hash memo and its validity check in
      `website/docs/guides/architecture/caching.md`, including the rule that the post-run probe
      bypasses it and why
- [ ] if Task 4 produced a proposal, record the owner's decision here rather than leaving it open
- [ ] run `task gen:llms-docs` and commit `internal/llmsdocs/embed` if any website page changed —
      the `llms-docs-drift` CI job re-harvests on every PR and fails on any diff

## Technical Details

**Measurement discipline.** Wall-clock **minimum** over n≥9 is the estimator; medians are
contaminated by contention. Report the min and state n. `verdictKeys` is concurrent, so its _total_
is CPU across tasks, not wall time — quote both, and never present the total as a wall-clock saving.

**Why mtime+size is acceptable here and not everywhere.** The memo in Task 3 caches a pure function
(bytes → hash) behind a validity check, and a stale entry can only be produced by a write that
preserves both size and mtime within the filesystem's granularity. The post-run probe in Task 2 is
the one place where exactly that write is plausible — a fixer rewriting a file it just read — which
is why that path keeps the full hash for `OpFix` and bypasses the memo entirely.

**What must not change.** The set of tools that run. Every task here is an optimisation of the
decision procedure, not of the decision. Task 6's third criterion is the one to fail the plan on.

## Post-Completion

**Manual verification:**

- Re-run every benchmark on an idle machine (load average < 2) and update the recorded numbers.
- Verify on a repository with a unit rooted at the repository root, which is the pathological case
  for `unitMembers`.
- Verify a `fix` run that rewrites files still records no verdict it did not earn.

**Follow-up work this plan deliberately excludes:**

- **The wazero compilation cache** (`NewCompilationCacheWithDir`), which would remove the ~80 ms
  module compile entirely. It hands back compiled machine code keyed by wazero's own hash and never
  re-verified by us, whereas the store re-verifies a module's SHA-256 on every load. That is a new
  trust assumption about `{cache}`, not an extension of an existing one, and this repository's hash
  policy is deliberately absolute. **It needs the owner's decision, not an agent's.**
- Execution-cache load/save restructuring (a full decode at startup, a second decode plus an encode
  plus a whole-file rewrite per save). Count the actual `Save` calls per run first — the 100 ms
  debounce may already collapse them to one, which caps the win at ~10 ms.
- Store garbage collection: 7.1 GB with no GC, since every config-hash change orphans a directory
  permanently. A separate feature, not an optimisation.
