# Plan: Cut the per-run CPU that decides not to run anything

**Status:** complete. All seven tasks are done; Task 4's gate closed and that task is deliberately
skipped. Final numbers are in Overview § Final measurements. What remains is the owner-machine
manual verification listed under Post-Completion.
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

### Final measurements

The table above is the **pre-change baseline** and is kept for the shape it records. What follows
replaces the plan's estimates with what was actually measured, on this repository (1,087 tracked
files, i9-14900K, `DATAMITSU_TRACE=1`), A/B against a binary built from `f062b0d` — the commit the
branch was cut from. Full method and per-task detail live in the Task 1–6 sections below.

| Metric                             |   Baseline |      Final |                                             Change | Task |
| ---------------------------------- | ---------: | ---------: | -------------------------------------------------: | ---- |
| `verdictKeys` total, warm `lint`   |    71.7 ms |    27.4 ms |                                         **−61.8%** | 2, 3 |
| `verdictKeys` max single task      |    11.9 ms |   10.66 ms |                                               −10% | 3    |
| `cache.verdict_bytes_hashed`, warm |    77.5 MB |    11.3 MB |                                         **−85.4%** | 3    |
| `cache.verdict_bytes_hashed`, cold |   131.5 MB |    77.6 MB |                                             −41.0% | 2    |
| `cache.verdict_hash_memo_misses`   |          — |      1,097 |                          ≈ 1 read per tracked file | 3    |
| `cache.verdict_hash_memo_hits`     |          — |      5,645 |                                                  — | 3    |
| `getCommandInfo` total             |   15.61 ms |    9.72 ms |                                             −37.7% | 5    |
| `exec.command_info_resolved`       |         30 |         21 |                 one per distinct app, not per task | 5    |
| `parser.module_instantiations`     |   = parses |          1 | one per live parse, for a module exporting `reset` | 5    |
| `walk.repository_walks`, `check`   |          3 |          2 |                                                 −1 | 5    |
| `lint` wall-min (n=9)              |    1.919 s |    1.879 s |                                              −2.1% | —    |
| `check` wall-min (n=9)             |    5.226 s |    5.093 s |                                              −2.5% | —    |
| `BenchmarkVerdictInputs`           | 5.98 ms/op | 6.95 ms/op |                                     unchanged path | 2    |
| `BenchmarkVerdictProbe`            |          — | 1.18 ms/op |             5.9× cheaper than the pass it replaces | 2    |

**The set of tools that ran is byte-identical** to the baseline on a cold cache, a warm cache, and a
run where one file changed — the criterion the plan says to fail on. Task 4's gate closed (27.4 ms
against a 300 ms threshold), so no policy knob was added.

Wall-clock moves by ~2% because the ~50 ms this plan removes is a small fraction of a 1.9 s run on a
1,087-file tree. The saving scales with tracked bytes and with tasks per app, which is what the
14,711-file measurement in the table above was dominated by; re-running it there is Post-Completion
manual verification on the owner's machine.

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

- [x] for `OpLint` (read-only), replace the full re-hash with a cheap staleness probe: re-stat every
      member and guard and re-hash only the paths whose size or modification time changed since the
      pre-run pass, which requires recording those stats during the first pass
- [x] a path that cannot be stat'ed, or whose stat is unchanged but whose mtime granularity makes
      the comparison unsafe, must fall back to re-hashing — never to assuming unchanged
- [x] leave `OpFix` on the full re-hash: a fixer rewrites files by design, mtime granularity is
      exactly the case that bites, and the existing branch already treats a fix specially
- [x] write a differential test: for a fixture unit, the pre/post comparison must reach the same
      verdict as a full re-hash — unchanged, one file touched (mtime only, same bytes), one file
      rewritten with different bytes, one file deleted, one guard appearing
- [x] write a test asserting a file rewritten within the same mtime tick is still detected (write,
      stat, rewrite with different length — length alone must catch it)
- [x] write a test asserting `OpFix` still performs the full re-hash
- [x] record the measured drop in `verdictKeys` total and in `cache.file_hashes`
- [x] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 3

#### Task 2 implementation and measurements

`verdictKeysMeasured` now returns a `verdictSnapshot` (`internal/tooling/verdict.go`) instead of a
bare input hash: the pre-run pass records, per path, the entry it hashed plus the size and
modification time of the handle it read it from. `recordVerdict` asks that snapshot to `refresh()`
for a read-only operation, which re-stats every path and re-reads only the ones a stat cannot clear.
`OpFix` keeps calling `verdictInputs` for the full second pass.

`settled` is the whole of the trade, and it answers "unchanged" only when: the path stats, is not a
directory, was read the first time, has the same size and the same mtime, **and** its mtime is at
least `mtimeGranularity` (2 s, FAT's tick) older than the moment the snapshot began. Anything else —
a stat error, a file that was missing and now exists, a file modified inside the current tick — falls
through to a re-hash.

Measured on this repository (1,087 tracked files, i9-14900K), against the Task 1 baseline:

| Metric                                    |   Baseline |                      After | Change                                            |
| ----------------------------------------- | ---------: | -------------------------: | ------------------------------------------------- |
| `cache.verdict_bytes_hashed`, cold `lint` |   131.5 MB |                    77.6 MB | **−41.0%**                                        |
| `cache.verdict_bytes_hashed`, warm `lint` |    77.5 MB |                    77.6 MB | unchanged (a hit never records)                   |
| `verdictKeys` total, cold                 |    78.3 ms |                    83.8 ms | unchanged — the span covers only the pre-run pass |
| `cache.file_hashes`                       |        657 |                        657 | unchanged — a different cache                     |
| `lint` wall-min (n=9, warm)               |     1.89 s |                     1.93 s | noise; the win is on a miss                       |
| `BenchmarkVerdictInputs` (2,000 × 2 KB)   | 5.98 ms/op |                 6.95 ms/op | full re-hash, unchanged path                      |
| `BenchmarkVerdictProbe` (same unit)       |          — | **1.18 ms/op**, 3,463 MB/s | 5.9× cheaper than the pass it replaces            |

The cold-run byte drop is exactly the 41% Task 1 predicted for the second pass, and it is the whole
of the win: warm runs never reached `recordVerdict` to begin with, so `verdictKeys` — which times
only the pre-run pass — does not move. ⚠️ `cache.file_hashes` counts the per-file content cache in
`internal/cache`, not the verdict pass, so it was never going to move here; `cache.verdict_bytes_hashed`
is the counter this task actually targets. Task 3's memo is what moves `cache.file_hashes`.

### Task 3: Hash each file once per process, not once per tool

A file in a package is hashed by every per-project tool planning a task there. The content of a file
does not depend on which tool is asking.

- [x] add a process-scoped content-hash memo keyed by path, **validated** by size and modification
      time — a cache of a pure function with a cheap validity check, not an assumption
- [x] the memo must be shared across tasks and safe under the executor's worker pool (`-race`)
- [x] `recordVerdict`'s post-run probe must bypass the memo, or the check it performs becomes
      tautological — this is the single most important line in the task
- [x] write a test asserting two tools over the same unit read each file once
- [x] write a test asserting a file rewritten between two tasks is re-hashed (change the length)
- [x] write a concurrency test (`-race`) hammering the memo from 16 goroutines
- [x] write a test asserting the post-run probe does not consult the memo
- [x] record the measured drop in `cache.file_hashes` and in `lint` wall-min
- [x] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 4

#### Task 3 implementation and measurements

`internal/tooling/hash_memo.go` holds `contentMemo`, a process-scoped `path -> (hash, size, mtime)`
map behind a `sync.RWMutex`. `hashedState` takes a memo and a mode: in `memoShared` it stats the path
first and returns the memoized hash when the stat still matches the entry — with `nil`, or in
`memoRewrite`, it reads the bytes. `verdictSnapshotOf` (the pre-run pass) passes `contentMemo` in
`memoShared`; `verdictSnapshot.refresh` (the read-only post-run probe) passes `nil`, because it
exists precisely to notice a write the pre-run pass could not and a memo filled by that pass would
answer it with its own input.

`verdictInputs` (the `OpFix` second pass) passes `contentMemo` in `memoRewrite`: it never consults an
entry, for the same reason as the probe, but it **replaces** the ones it read. The pre-fix hashes it
has just disproved are exactly the entries a later task in the same process must not be handed —
`check` runs `fix` and then `lint` in one process — and a fixer that preserves size and mtime, the
write this full re-hash exists to catch, would otherwise leave them live and stat-clean for the lint
pass to hit an unrelated old verdict on. Overwriting also repopulates the memo with the post-fix
bytes, so the sibling `lint` task re-reads nothing the fixer did not touch.

`lookup` declines an entry whose mtime is newer than `mtimeGranularity` before the moment the entry's
**bytes were read** — the same guard `settled` uses against `snap.taken`, for the same reason: a file
written inside the tick the read landed in can be rewritten again at the same length and show an
identical stat. The anchor is the read and not the lookup: waiting cannot settle such an entry,
because the ambiguous write it fails to rule out already happened.

Measured on this repository (1,087 tracked files, i9-14900K), same-machine A/B between a binary built
from `HEAD` and this change, warm `lint`:

| Metric                                    | Baseline |   After | Change     |
| ----------------------------------------- | -------: | ------: | ---------- |
| `verdictKeys` total, warm `lint`          |  60.6 ms | 21.7 ms | **−64.2%** |
| `verdictKeys` max single task             |  11.9 ms |  8.4 ms | −29.4%     |
| `cache.verdict_bytes_hashed`, warm `lint` |  65.3 MB | 11.2 MB | **−82.8%** |
| `cache.verdict_hash_memo_hits`            |        — |   4,415 | —          |
| `cache.verdict_hash_memo_misses`          |        — |   1,092 | —          |
| `cache.file_hashes`                       |        8 |       8 | unchanged  |
| `lint` wall-min (n=9, warm)               |   1.65 s |  1.65 s | unchanged  |

4,415 hits against 1,092 misses is the shape the task predicted: each file is read once and answered
five times, once per per-project tool planning a task over its unit.

⚠️ Correction to Task 2's closing note: `cache.file_hashes` counts the per-file content cache in
`internal/cache`, which the verdict pass does not use, so the memo was never going to move it either
— the counter this task moves is `cache.verdict_bytes_hashed`. Task 6's second acceptance criterion
should be read against that counter, not `cache.file_hashes`.

`lint` wall-min does not move on this repository because 60 ms of the 1.65 s is inside the noise;
the win is CPU that scales with tracked bytes, which is what the Overview's 14,711-file measurement
was dominated by.

### Task 4: Decide the unit-granularity default from Task 1's table

- [x] re-read Task 1's table after Tasks 2 and 3. **If `verdictKeys` is now under 300 ms total,
      mark this task skipped with a note and move to Task 5** — do not add a policy knob for a cost
      that no longer exists → **gate closed: task skipped**
- [x] if it is still material: propose (in this file, for the owner to approve — do not implement
      unilaterally) either a size threshold above which a unit-granularity verdict is not attempted,
      or making unit granularity opt-in the way repo granularity already is → not material; no
      proposal made, no policy knob added
- [x] whatever is chosen must keep `--explain` honest: a tool whose verdict was not attempted is
      not the same as a tool that was skipped, and the two must not print the same → vacuous, since
      nothing was chosen; `--explain` is unchanged by this task

#### Task 4 decision: skipped, gate not met

Re-measured on this repository (1,087 tracked files, i9-14900K, `DATAMITSU_TRACE=1`, binary built
from Task 3's `HEAD`), two consecutive `datamitsu lint` runs:

| Run                            | `verdictKeys` total | max single task | `verdict_bytes_hashed` | processes spawned |
| ------------------------------ | ------------------: | --------------: | ---------------------: | ----------------: |
| cold verdict cache (80 spawns) |            24.81 ms |         7.87 ms |                11.9 MB |                80 |
| warm (7 spawns, 25 cache hits) |            28.22 ms |        11.13 ms |                11.2 MB |                 7 |

Both are an order of magnitude below the 300 ms threshold the task set, so the gate closes and no
policy knob is added. 80 `verdictKeys` spans, of which 28 actually apply the cache; the rest report
`applies=false` and cost sub-microsecond, so unit granularity is not paying for tools it cannot help.

⚠️ This is the 1,087-file repository, not the 14,711-file monorepo the Overview measured. The gate
was written to be read against Task 1's table, which was recorded on this same tree, so the
comparison is like-for-like — but the Overview's 3,055 ms figure is not re-verifiable here. Task 6's
first acceptance criterion (≤ 600 ms on a large repository) remains the check that would reopen this
question, and it is a manual measurement on the owner's machine.

### Task 5: The cheap, unambiguous duplication

Three independent items, none of which changes a decision.

- [x] memoize `GetCommandInfo` per app inside the executor for the lifetime of one `Execute`, handing
      out copies rather than the shared pointer; first verify by grep that no caller mutates
      `CommandInfo.Env` or its slices
- [x] have the runner call `bundled.FindIgnoreFiles` once and pass the result to both `RunFix` and
      `RunLint`, instead of each walking the tree — `check` drops from three walks to two
- [x] pool wazero parser instances instead of instantiating one per parse, keeping one instance per
      module per worker; an instance must be reset or discarded between parses so no state leaks
      between two tools' output
- [x] write a test asserting `GetCommandInfo` is called once per distinct app across a multi-task plan
- [x] write a test asserting a returned `CommandInfo` cannot be mutated by one caller into another's
      view
- [x] write a test asserting `check` performs exactly two repository walks (assert on the
      `walk.repository_walks` counter)
- [x] write a test asserting two parses through a pooled instance produce the same diagnostics as two
      parses through fresh instances — including a parse that errors between them
- [x] record the measured drop in `getCommandInfo` total, walk count, and `parser.instantiate` total
- [x] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 6

#### Task 5 implementation and measurements

**Command info.** `internal/tooling/command_info_memo.go` holds `commandInfoMemo`, a
`app -> (*CommandInfo, error)` map whose entries resolve under a `sync.Once`, so the five tasks the
worker pool starts for one app at the same moment wait on one resolve instead of starting five.
`Execute` stores a fresh memo and clears it on return (`atomic.Pointer`), so an install between two
runs is always seen and `FormatContent` — the LSP path, which runs outside `Execute` — resolves
directly. Callers get `cloneCommandInfo`, never the shared pointer: `Env`, `Args` and `RequiredPaths`
are all reference types, and one task editing them would rewrite another task's command line. A
grep confirmed no caller mutates them today (`buildCommand` only reads); the copy is what keeps that
true. A resolve failure is memoized too — within one `Execute` an unresolvable tool cannot become
resolvable, and retrying per task turns one error into N installs.

**Ignore-file discovery.** `bundled.RunFix`/`RunLint` now take the file list instead of discovering
it; the runner calls `FindIgnoreFiles` once and hands the same list to both.

**Parser instances.** `Manager` keeps idle `ParserRuntime`s per content key (max 8), and
`ParseOutput` draws from that pool. Reuse is gated on the module exporting `reset` (a fifth,
optional ABI export, added to `datamitsu-parsers`): the host calls it before pooling an instance, and
a module that omits it is instantiated per parse as before — the ABI does not make a module
stateless, so without that boundary parse N+1 could observe parse N. Two further rules keep pooling
invisible: an instance whose parse
returned an error is closed rather than pooled (a trap mid-ABI can leave the module's allocator in a
state the next parse would inherit), and a failure on a _reused_ instance is retried once on a fresh
one (the failure may belong to the instance — it can be closed underneath the pool — and pooling must
never turn a parse that works into one that fails). A fresh instance is never retried: its failure is
the module's answer to that input.

The pool numbers below were measured against a module built from this tree (with `reset`). A
_released_ module predating the export parses at the pre-pooling cost — `parser.module_instantiations`
equal to the parse count and `parser.unresettable_discards` equal to it too — until the next parser
release ships the export.

Measured on this repository (1,087 tracked files, i9-14900K), same-machine A/B between a binary built
from `HEAD` and this change, warm `lint`, `DATAMITSU_TRACE=1`:

| Metric                          | Baseline |   After | Change                                      |
| ------------------------------- | -------: | ------: | ------------------------------------------- |
| `getCommandInfo` total (n=30)   | 15.61 ms | 9.72 ms | **−37.7%**                                  |
| `getCommandInfo` max single     |  2.10 ms | 0.65 ms | −69.2%                                      |
| `exec.command_info_resolved`    |       30 |       7 | one per distinct app, not one per task      |
| `exec.command_info_memo_hits`   |        — |      23 | —                                           |
| `parser.module_instantiations`  |        2 |       1 | one per module, not one per parse           |
| `parser.instance_pool_hits`     |        — |       1 | —                                           |
| `walk.repository_walks`, `lint` |        2 |       2 | unchanged — the duplicate walk is `check`'s |
| `exec.processes_spawned`        |        5 |       5 | the set of tools that ran is identical      |
| `exec.verdict_cache_hits`       |       23 |      23 | same skips                                  |

The 23 memo hits still show as `getCommandInfo` spans in the trace, because a task that arrives while
the first resolve is in flight blocks on the `Once` — the span covers the wait. That is why the total
drops by 38% rather than by 23/30: the win is the resolves that no longer happen, not the waits.

`walk.repository_walks` on `check` is measured by `TestCheckAndLintShareOneIgnoreDiscoveryWalk`
(`internal/runner/walks_test.go`) rather than from the CLI, because a real `check` on this repository
rewrites files. Reintroducing the second discovery makes that test report 3; with the shared list a
`check` walks exactly as many times as a `lint` does, which is 2.

`parser.module_instantiations` is 1 against 2 parses here because this repository parses two tool
invocations through one module on a warm run. The counter is the shape that matters: it is now the
number of concurrently-live parses, not the number of parses.

### Task 6: Verify acceptance criteria

- [x] `verdictKeys` total on a warm `lint` of a large repository is ≤ 600 ms (baseline 3,055 ms) →
      **27.4 ms** on this repository; the large-repository run is not reproducible here (see the note
      below) and stays in Post-Completion
- [x] `cache.file_hashes` on that run is at most the number of distinct files in the planned units
      (each file hashed once), plus the post-run probes → read against `cache.verdict_hash_memo_misses`
      per Task 3's correction: **1,097 misses against 1,094 tracked files**
- [x] `lint` wall-min improves and `check` wall-min improves; record both with n≥9
- [x] **the set of tools that ran is identical to the baseline** on a cold cache, a warm cache, and a
      run where one file changed — compare `--explain=json` and the run summary against the
      pre-change binary. This is the criterion that matters; the timings are secondary →
      **byte-identical on all three**
- [x] `go test ./...` passes with `-race` (under `umask 022`)
- [x] `go test ./test/cli/ -count=2` passes with **zero** regenerated goldens
- [x] `pnpm dm check` passes
- [x] coverage meets the project standard via `pnpm test:coverage:all` → **82.5%** combined

#### Task 6 verification

Verified against a baseline binary built from `f062b0d` — the commit this branch was cut from — so
the A/B is this plan's five tasks against nothing else. The baseline tree was built by
copying the working tree (which carries the generated `internal/config/config.js` that `go build`
embeds) and checking `f062b0d` over it, then deleting the files this branch added; both binaries were
built with the same toolchain minutes apart.

**The criterion that matters — the set of tools that ran.** For each binary in turn: the project's
`toolstate.msgpack` was deleted (cold), `lint` was run, run again (warm), then a comment was appended
to `internal/tooling/verdict.go` and `lint` run a third time (one file changed). The per-tool summary
lines with durations stripped, and `--explain=json`, were compared:

| Cache state      | tool set (35 lines) | `--explain=json` | run summary                          |
| ---------------- | ------------------- | ---------------- | ------------------------------------ |
| cold             | identical           | identical        | 80 runs, 13 skipped, cache 0% both   |
| warm             | identical           | identical        | 80 runs, 13 skipped, cache 100% both |
| one file changed | identical           | identical        | 80 runs, 13 skipped, cache 99% both  |

The changed-file run is the one that proves the probe is not lying: it drops to 99% and spends
5.45 s (baseline) / 3.56 s (new) against 1.9 s warm, so the touched unit really was re-run by both
binaries, and by the same tools.

**Counters, warm `lint`, `DATAMITSU_TRACE=1`:**

| Counter / span                   |    Value | Reading                                                              |
| -------------------------------- | -------: | -------------------------------------------------------------------- |
| `verdictKeys` total (n=80)       |  27.4 ms | ≪ the 600 ms criterion                                               |
| `verdictKeys` max single task    | 10.66 ms | —                                                                    |
| `cache.verdict_hash_memo_misses` |    1,097 | ≈ 1,094 tracked files — each file hashed once                        |
| `cache.verdict_hash_memo_hits`   |    5,645 | the five per-project tools sharing those reads                       |
| `cache.verdict_bytes_hashed`     |  11.3 MB | against 77.5 MB at the Task 1 baseline                               |
| `exec.command_info_resolved`     |       21 | against 80 tasks                                                     |
| `exec.command_info_memo_hits`    |       59 | —                                                                    |
| `parser.module_instantiations`   |        1 | one per live parse; pooling is gated on the `reset` export           |
| `walk.repository_walks`          |        2 | `check` is 2 as well (`TestCheckAndLintShareOneIgnoreDiscoveryWalk`) |
| `exec.processes_spawned`         |        7 | —                                                                    |
| `exec.verdict_cache_hits`        |       25 | —                                                                    |

**Wall-clock minimum, n=9, interleaved baseline/new so drift hits both equally:**

| Operation | Baseline min | New min | Change |
| --------- | -----------: | ------: | ------ |
| `lint`    |      1.919 s | 1.879 s | −2.1%  |
| `check`   |      5.226 s | 5.093 s | −2.5%  |

Both improve, and both improvements are small because the CPU this plan removed — ~50 ms of hashing
and resolving — is a small fraction of a 1.9 s run on a 1,087-file tree. The saving scales with
tracked bytes and with tasks per app, which is why the Overview's 14,711-file measurement was
dominated by it and this one is not.

**Suites:** `go test ./... -race` under `umask 022` — 0 failures. `go test ./test/cli/ -count=2` —
pass, `git status test/cli` clean, so zero goldens were regenerated. `pnpm dm check` — exit 0, working
tree unchanged. `pnpm test:coverage:all` — 82.5% combined; every function added by this plan in
`verdict.go` is at 100% except `hashedState` (88.5%), whose uncovered branches are `io` error paths.

⚠️ The first criterion's threshold was written against a 14,711-file monorepo that is not available
to the implementation, exactly as Task 4 recorded. What is verified here is that the criterion holds
by a factor of 22 on the tree that _is_ available, and that the shape the Overview measured — hashing
that scales with bytes and repeats per tool — is gone rather than reduced: `verdict_hash_memo_misses`
is now the file count, which is the invariant the 600 ms number was a proxy for. Re-running it on the
large repository stays in Post-Completion as owner-machine manual verification.

### Task 7: [Final] Update documentation

- [x] record the final measurement table in this plan file, replacing the estimates →
      **Overview § Final measurements**; the 14,711-file table above it is kept, relabelled as the
      pre-change baseline it always was
- [x] document the content-hash memo and its validity check in
      `website/docs/guides/architecture/caching.md`, including the rule that the post-run probe
      bypasses it and why → new **Unit Verdicts and the Content-Hash Memo** section
- [x] if Task 4 produced a proposal, record the owner's decision here rather than leaving it open →
      no proposal: the gate closed at 27.4 ms against a 300 ms threshold, so there is nothing to
      decide (Task 4 § decision)
- [x] run `task gen:llms-docs` and commit `internal/llmsdocs/embed` if any website page changed —
      the `llms-docs-drift` CI job re-harvests on every PR and fails on any diff → re-harvested
      (53 pages, `pageSetHash c3b8fe33e2591d19df0d0f59c77fac37`) **after** `pnpm dm check` reformatted
      the new tables, so the embedded bytes match the formatted page

#### Task 7 notes

The caching page gains one section rather than a new page: the memo is not a cache users configure,
it is an implementation detail of a cache that is already documented there, and splitting it would
separate the memo from the verdict it serves. Three things are stated explicitly because they are the
parts that can be got wrong later — the two-second mtime guard and what it rules out, that the
post-run probe bypasses the memo (and that a tautological check is worse than none), and that `fix`
keeps the full re-hash rather than the stat probe.

**Suite state at close:** `go test ./...` — 0 failures. `go test ./test/cli/ -count=2` — pass with
`git status test/cli` clean, so zero goldens were regenerated. `pnpm dm check` — 80 runs, 0 failures.

## Technical Details

**Measurement discipline.** Wall-clock **minimum** over n≥9 is the estimator; medians are
contaminated by contention. Report the min and state n. `verdictKeys` is concurrent, so its _total_
is CPU across tasks, not wall time — quote both, and never present the total as a wall-clock saving.

**Why mtime+size is acceptable here and not everywhere.** The memo in Task 3 caches a pure function
(bytes → hash) behind a validity check, and a stale entry can only be produced by a write that
preserves both size and mtime within the filesystem's granularity. The post-run probe in Task 2 is
the one place where exactly that write is plausible — a fixer rewriting a file it just read — which
is why that path keeps the full hash for `OpFix`, never reads through the memo, and overwrites the
entries it disproved so no later task in the process inherits them.

**Why the comparison is not mtime+size alone.** A writer that _restores_ the mtime it found —
`rsync -a`, `cp -p`, an archive extraction, anything calling `Chtimes` — can rewrite the same number
of bytes and leave both halves of that pair untouched, and no anchoring of the tick guard can see it:
the mtime it puts back is hours old, which is exactly the state the guard calls settled. Both stat
comparisons therefore also carry `fileIdent` (`internal/tooling/statident*.go`), the inode and the
inode-change time — the part a writer cannot put back, because the write moves it and the `utimes`
call that restores the mtime moves it again. It is the same field git's index stores for the same
reason. On Windows no change time is reachable from a path-only stat, so `fileIdent` carries a
`known` flag that is false there — and an unknown identity is a miss, never a match. That platform
therefore keeps the pre-plan behaviour exactly: the memo never hands an entry out and the post-run
probe re-hashes every path, so it loses the saving rather than the guarantee. Degrading to mtime+size
would have been the one thing the zero value must not do, since two zero values compare equal. The
tests that assert a _saved read_ skip there and name the missing property; the tests that assert a
_correct answer_ run everywhere.

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
