# Plan: Cut CLI startup cost from ~195 ms to ~15 ms per invocation

**Status:** implemented. Task 4 was skipped by its own gate; the `exec` ≤ 70 ms
acceptance criterion is missed at 93.4 ms, attributed to goja evaluating the large
before-config on every invocation — cross-process config-evaluation caching is out of
scope here and is named as the follow-up.
**Date:** 2026-08-19.
**Related:** `internal/facts`, `internal/engine`, `internal/config`, `cmd/config_loader.go`, `go.mod`.
**Blocks:** `docs/plans/2026-08-19-source-mode.md` — source mode's per-invocation and
branch-switch numbers are hostage to this work, but nothing here is a design commitment to it.

**Branch:** `cli-startup-cost`, branched from `main`. First of three stacked plans
(`cli-startup-cost` → `source-mode` → `global-config-layer`). Do all work on this branch and
do not merge to `main` as part of this plan — the next plan stacks on top of it.
Review base ref: `main` (the default).

## Overview

Every `datamitsu` invocation pays ~195 ms of overhead before the tool it was asked to run
starts. Measured on Apple M1 Max at commit `20c75ba`:

| Path                                               | Wall (min)     |
| -------------------------------------------------- | -------------- |
| `actionlint --version` run directly from the store | 6.1–7.3 ms     |
| `datamitsu exec actionlint -- --version`           | 199.6–259.9 ms |
| `datamitsu version` (no config load at all)        | 39.7–51.9 ms   |
| empty Go binary (`func main(){}`)                  | 6.0–7.0 ms     |

Only ~7 ms of that is the irreducible Go process floor. The rest lives in three independent
buckets, and the two largest are cheap to remove:

1. **~33 ms — `github.com/mattn/go-runewidth` v0.0.27.** Its `init()` eagerly builds rune
   lookup tables: 33.0 ms of CPU at every process start, 88% of all package-init time across
   196 packages (`GODEBUG=inittrace=1`). v0.0.28 makes the tables lazy: 0.01 ms. It reaches us
   as an indirect dependency through `mpb` → `internal/ui`.
2. **~105 ms — forked `git rev-parse`.** Every `datamitsu exec` forks **10** git processes:
   `facts.GetGitRoot` (`internal/facts/facts.go:171`) runs `--show-toplevel` and
   `--show-superproject-working-tree` as an errgroup pair, and it is called once in
   `loadConfigImpl` plus once inside each of the four `engine.New` calls. One fork costs
   10.3 ms; the parallel pair costs 21.3 ms.
3. **~85 ms — JS config evaluation.** Four goja VMs and four esbuild `StripTypes` calls per
   run. 25.1 ms of the esbuild time is spent stripping types from
   `node_modules/@shibanet0/datamitsu-config/datamitsu.config.oci-ghcr.js` — a 1,096,706-byte
   file that is **already plain JavaScript**.

This plan removes buckets 1 and 2 in full and the wasted esbuild pass from bucket 3. It does
not restructure config loading or introduce a config cache — that is deliberately out of
scope, because the cheap wins land first and change the arithmetic for whatever comes next.

Target after this plan: `datamitsu version` ≈ 12 ms, `datamitsu exec <installed tool>` ≈ 60 ms.

## Context (from discovery)

- `internal/facts/facts.go:171` — `GetGitRoot` walks superproject levels, spawning two git
  processes per level via `errgroup`. Its result is a pure function of cwd within one process.
- `internal/engine/engine.go` — `engine.New` calls `facts.CollectWithOptions`, which calls
  `GetGitRoot`. Four engines are constructed per `exec`: one in `discoverBeforeConfigs`
  (`cmd/config_loader.go:624`) and one per config source.
- `cmd/config_loader.go:679` and `:694` — both call `config.StripTypes` unconditionally,
  regardless of whether the source is `.ts`, `.js` or `.mjs`.
- `internal/config/config.go:471` — `StripTypes` is the esbuild entry point; it already logs
  its own duration at debug level (`:476`).
- `scripts/bench-overhead.sh` — the repo's existing startup benchmark. It independently
  reports 265.6 ms min / 335.0 ms median of attributable overhead against a 9.0 ms
  `bash -c` baseline.
- `internal/timing` — instruments the planner and runner only; the config-load path is not
  covered.

## Development Approach

- **Testing approach**: Regular (code first, then tests), except Task 1 which is
  measurement-first because every later task is validated against its baseline.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
  Tests are listed as separate checklist items, never bundled with implementation.
- **CRITICAL: all tests must pass before starting the next task.** Run `go test ./...` and
  `go test ./test/cli/ -count=2` (the CLI goldens must stay byte-stable).
- **CRITICAL: update this plan file when scope changes during implementation.**
- Behavior must not change. This plan is a pure performance change: every existing golden
  file must remain byte-identical, and any golden that moves is a bug in the change, not a
  golden to regenerate.

## Testing Strategy

- **Unit tests**: required for every task. Stdlib `testing` only, table-driven,
  `t.TempDir`/`t.Setenv` — no testify.
- **Blackbox CLI tests**: `go test ./test/cli/ -count=2` must pass unchanged. No golden file
  may be regenerated by this plan.
- **Benchmarks**: Go benchmarks are the acceptance evidence for Tasks 2, 3, 4 and 5. Commit
  them; they are the regression guard.
- **No e2e**: the OCI e2e tier (`test/e2e`, `//go:build e2e_oci`) is untouched.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Record the measured before/after number for each perf task directly in this file.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, benchmarks, docs in this repo.
- **Post-Completion** (no checkboxes): measurements to re-run on an idle machine, and the
  follow-up work this plan deliberately does not do.

## Implementation Steps

### Task 1: Make the startup cost reproducible from the CLI

Every later task is judged against this. Build it first so the numbers are not re-derived
from throwaway scripts.

- [x] extend `internal/timing` (or add a sibling) so the config-load path can be
      instrumented: record durations for `discoverBeforeConfigs`, each `engine.New`, each
      `config.StripTypes` call, each `getConfig()` evaluation, and total `loadConfig`
- [x] wire the instrumentation into `cmd/config_loader.go` at the existing seams
      (`:624` pre-pass, `:679`, `:694`) and into `internal/engine/engine.go`'s `New`
- [x] gate emission behind an existing debug/timing mechanism rather than a new always-on
      path — add the env var to `internal/env/e.go` + getter in `internal/env/env.go` per the
      Environment Variable Usage Policy, and confirm `internal/clitest` strips it (it strips
      `DATAMITSU_*`, so a `DATAMITSU_`-prefixed name is automatically golden-safe)
- [x] add `BenchmarkLoadConfig` in package `cmd` that exercises the same path `exec` uses,
      reporting ns/op and allocs/op
- [x] add `BenchmarkGetGitRoot` and `BenchmarkEngineNew` so Tasks 3 and 4 have direct targets
- [x] record the baseline in this file: `go test ./cmd/ -run XXX -bench 'LoadConfig|GetGitRoot|EngineNew' -benchtime 30x -count 4`
- [x] write tests asserting the instrumentation emits nothing when the env var is unset
- [x] write tests asserting each recorded phase name appears exactly once per load
- [x] run `go test ./...` and `go test ./test/cli/ -count=2` — must pass before Task 2

**Task 1 results.**

Instrumentation: `DATAMITSU_STARTUP_TIMINGS=1` (`internal/timing/startup.go`,
`env.IsStartupTimingsEnabled`). Phases are aggregated by name for the process and reported to
stderr at most once — from the end of `loadConfigImpl` (because `exec` calls `os.Exit` and
never returns to `Execute`) and from `cmd/root.go`'s `Execute` for commands that never load
config. `internal/clitest` strips every `DATAMITSU_*` var, so goldens are unaffected.

Baseline, Apple M1 Max, `-benchtime 30x -count 4`, fixture git repo with a trivial
`datamitsu.config.js` (min of 4 runs):

| Benchmark             | ns/op      | B/op      | allocs/op |
| --------------------- | ---------- | --------- | --------- |
| `BenchmarkLoadConfig` | 73,473,125 | 2,240,691 | 16,156    |
| `BenchmarkGetGitRoot` | 16,568,433 | 115,252   | 169       |
| `BenchmarkEngineNew`  | 17,170,300 | 196,871   | 1,172     |

Phase report for one real `datamitsu exec` in that fixture (`loadConfig` total 81.18 ms):
`engine.New` n=3 / 53.84 ms, `discoverBeforeConfigs` n=1 / 23.47 ms, `facts.GetGitRoot` n=1 /
17.39 ms, `config.StripTypes` n=2 / 6.44 ms, `getConfig` n=2 / 0.20 ms.

➕ Discovered: the auto config is read **and type-stripped twice** — once by the
`discoverBeforeConfigs` pre-pass and once as a config source. Task 5's extension check removes
both for `.js`/`.mjs`. The `facts.GetGitRoot` phase counts only the loader's own call; the
four inside `engine.New` are folded into that phase's total, which is why `engine.New` at
17.2 ms/op is almost entirely git-root resolution (Task 3).

### Task 2: Bump go-runewidth to v0.0.28

The single highest value-to-effort change in the repository.

- [x] `go get github.com/mattn/go-runewidth@v0.0.28` and `go mod tidy`
- [x] confirm it is still reachable only as an indirect dependency via `mpb` →
      `internal/ui`, and that no direct import was introduced
- [x] verify with `GODEBUG=inittrace=1 ./datamitsu version 2>&1 | grep runewidth` that
      package-init time for it drops from ~33 ms to <0.1 ms
- [x] record before/after `datamitsu version` wall-min in this file (expected ~52 ms → ~13 ms)
- [x] write a test in `internal/ui` asserting the width helpers still behave correctly for
      the cases the display relies on — CJK wide runes, combining marks, emoji ZWJ sequences,
      and ASCII — so the lazy-table change cannot silently alter column math
- [x] write a test covering the progress-bar/line truncation path with a wide-rune string
- [x] run `go test ./...` and `go test ./test/cli/ -count=2` — the CLI goldens must be
      byte-identical, which is the proof that rendering did not shift
- [x] must pass before Task 3

**Task 2 results.** Apple M1 Max, machine under light load, n=40 per measurement.

| Measurement                       | Before (v0.0.27) | After (v0.0.28) |
| --------------------------------- | ---------------- | --------------- |
| `runewidth` package init (clock)  | 28.0 ms          | 0.057 ms        |
| `runewidth` package init (allocs) | 54 (57,368 B)    | 2 (32 B)        |
| `datamitsu version` wall-min      | 39.9 ms          | 11.7 ms         |
| `datamitsu version` wall-median   | 41.0 ms          | 12.7 ms         |

`go mod why` still resolves as `internal/ui` → `mpb/v8` → `go-runewidth`; the dependency
stays `// indirect` and no source file imports it. Every `test/cli` golden is byte-identical
(`git status test/cli/testdata/golden/` is empty after `-count=2`), which is the proof that
column math did not move.

Tests: `internal/ui/runewidth_test.go`. Deliberately asserts through `decor.Name` and a real
`mpb` render rather than importing `runewidth` — that keeps the dependency indirect and
covers the actual rendering path. Coverage: padding by display width (CJK, Hangul, mixed,
NFC vs NFD latin, stacked combining marks, emoji ZWJ, plain emoji, UI symbols, empty), the
extra-space separator contract for names at/over the column, and renderer line truncation on
a rune boundary for wide-rune strings. Spot-checked against v0.0.27 directly:
`StringWidth`/`Truncate` return identical results for every one of these inputs.

### Task 3: Memoize the git root for the process lifetime

`GetGitRoot` is called 5 times per `exec` and is a pure function of cwd within one process.
This task changes no semantics — it removes 4 of the 5 calls. The riskier pure-Go
replacement is deliberately deferred to Task 6.

- [x] add a process-scoped memo in `internal/facts` keyed by the working directory the lookup
      started from, guarded by `sync.RWMutex` (or `sync.Map`), caching both the resolved root
      and the error
- [x] make the memo explicitly resettable from tests (an unexported reset helper) so cases
      that `t.Chdir`/`os.Chdir` are not poisoned by a previous case
- [x] confirm the memo is safe under `errgroup` concurrency — `engine.New` calls can overlap
- [x] document in the function comment that datamitsu is a short-lived process and the cwd
      does not change mid-run, which is what makes the memo sound; call out the LSP
      (`cmd/lsp.go`) as the one long-lived case and verify it does not depend on re-resolution
- [x] write a test asserting two calls from the same cwd spawn git exactly once (inject or
      count via a test seam, e.g. a package-level hook or a PATH shim in `t.TempDir`)
- [x] write a test asserting two calls from different cwds resolve independently
- [x] write a test asserting a cached error is returned identically on the second call
- [x] write a concurrency test (`-race`) hammering the memo from 16 goroutines
- [x] record the measured drop in `BenchmarkLoadConfig` in this file (expected ~85 ms)
- [x] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 4

**Task 3 results.** Apple M1 Max, `-benchtime 30x -count 4`, same fixture as Task 1 (min of
4 runs).

| Benchmark             | Before (Task 1) | After     | Drop     |
| --------------------- | --------------- | --------- | -------- |
| `BenchmarkLoadConfig` | 73,473,125      | 3,353,589 | −70.1 ms |
| `BenchmarkGetGitRoot` | 16,568,433      | 561,165   | −16.0 ms |
| `BenchmarkEngineNew`  | 17,170,300      | 679,025   | −16.5 ms |

(ns/op. Allocs: `LoadConfig` 16,156 → 15,492; `GetGitRoot` 169 → 9; `EngineNew` 1,172 → 1,012.)

Read the last two rows as amortized, not per-call: with the memo, one benchmark iteration in
30 pays the real ~17 ms resolution and the other 29 are map lookups, so `BenchmarkGetGitRoot`
now measures the memoized path plus 1/30 of a resolution. That is the honest shape of the
change — the cost did not get cheaper, it happens once per process instead of five times.
Task 6 makes the resolution itself cheap and is what will move these numbers on the first
call. `BenchmarkLoadConfig`'s 70 ms drop is the real per-invocation win.

Design notes: `GetGitRoot` now resolves `os.Getwd()` and memoizes on it; the climb moved to
an unexported `resolveGitRoot(ctx, cwd)`, reachable through the `gitRootLookup` package
variable so tests can count resolutions. Each cache entry carries a `sync.Once`, so
overlapping `engine.New` calls collapse onto one resolution instead of racing to duplicate it.
Errors are cached — a directory does not become a repository mid-run — **except** when
`ctx.Err() != nil`, since a cancelled context says nothing about the layout; that entry is
dropped so a later call retries.

`cmd/lsp.go` (the one long-lived command) resolves its root through `traverser.GetGitRoot`,
not this function, and loads config exactly once per session, so it does not depend on
re-resolution.

⚠️ Pre-existing, unrelated: `go test ./internal/cache/ -race` panics in
`(*Cache).debounceSave` (nil `zap.Logger` fired from a timer goroutine after the test's logger
is torn down, `internal/cache/cache.go:651`). Verified identical on the unmodified tree via
`git stash`. Not caused by this task; `go test ./...` without `-race` is green, as are
`./internal/facts/` and `./cmd/` with `-race`.

### Task 4: Collect facts once per process and share across engines

After Task 3, `engine.New`'s dominant cost is gone, but `facts.CollectWithOptions` still
re-reads the environment and re-derives the same values four times.

- [x] measure `BenchmarkEngineNew` after Task 3 and record it here; **if the remaining cost is
      under 1 ms/op, mark this task skipped with a note and move to Task 5** — do not add a
      sharing mechanism for a cost that no longer exists
- [x] if it is still material: collect the facts snapshot once in `cmd/config_loader.go` and
      pass it into each `engine.New` via an option, keeping the existing per-engine collection
      as the fallback when no snapshot is supplied — **skipped, gate not met**
- [x] ensure the snapshot is immutable once shared — no engine may mutate what another reads
      — **skipped, no snapshot introduced**
- [x] verify `isMonorepo` and any other cwd-derived fact is still correct for every source,
      since all four engines run from the same cwd in one process — **skipped, collection
      unchanged**
- [x] write a test asserting the shared snapshot produces byte-identical facts to
      per-engine collection for a fixture repo — **skipped, no shared snapshot exists**
- [x] write a test asserting an engine constructed without a snapshot still collects its own
      — **skipped, no snapshot option exists**
- [x] run `go test ./...` and `go test ./test/cli/ -count=2` — must pass before Task 5

**Task 4 results — SKIPPED by the task's own gate.**

Re-measured on the current tree, Apple M1 Max, `-benchtime 30x -count 4` (min of 4 runs):

| Benchmark             | Task 1 baseline | Task 3    | Task 4 re-measure |
| --------------------- | --------------- | --------- | ----------------- |
| `BenchmarkEngineNew`  | 17,170,300      | 679,025   | 671,474           |
| `BenchmarkLoadConfig` | 73,473,125      | 3,353,589 | 3,289,406         |
| `BenchmarkGetGitRoot` | 16,568,433      | 561,165   | 576,749           |

(ns/op. `EngineNew` allocs steady at 1,012.)

`BenchmarkEngineNew` is 0.67 ms/op, under the 1 ms/op threshold, so the gate trips and no
facts-sharing mechanism is added. Note that most of even that 0.67 ms is not fact derivation:
`BenchmarkGetGitRoot` measures 0.58 ms/op on the same amortization (one real resolution spread
over 30 iterations, per the Task 3 note), so `engine.New`'s own non-git cost is roughly
0.1 ms/op. Collecting facts once and threading a snapshot through an engine option would buy
back a fraction of that while adding an immutability contract and a fallback path to maintain
— the wrong trade at this magnitude. Task 3's memo already removed the cost this task existed
to address.

No code changed. `go test ./...` and `go test ./test/cli/ -count=2` are green and
`git status` reports no modifications under `test/cli/testdata/golden/`.

### Task 5: Skip esbuild for sources that are already JavaScript

25.1 ms of the 28.8 ms of esbuild time per run is spent stripping types from a file that has
none.

- [x] in `cmd/config_loader.go`, skip the `config.StripTypes` call at `:679` and `:694` when
      the source path's extension is `.js` or `.mjs`, passing the content through unchanged
- [x] keep `StripTypes` for `.ts`, `.mts`, `.cts` and for the embedded default config
      (`internal/config/config.go:462`)
- [x] decide the rule by file extension, not by content sniffing — a heuristic that guesses
      wrong silently produces a syntax error deep inside goja
- [x] confirm the OCI/remote-config path (`internal/remotecfg`) routes through the same
      extension check, or document why it cannot and handle it explicitly
- [x] write a table test over `.ts`/`.mts`/`.js`/`.mjs`/no-extension asserting which inputs
      reach esbuild (use a test seam or assert on the debug log)
- [x] write a test asserting a `.js` config containing syntax esbuild would rewrite
      (e.g. modern syntax it would downlevel) is passed through byte-identically
- [x] write a test asserting a `.ts` config still gets its types stripped
- [x] record the measured drop in `BenchmarkLoadConfig` in this file (expected ~25 ms)
- [x] run `go test ./...` and `go test ./test/cli/ -count=2` — must pass before Task 6

**Task 5 results.** Apple M1 Max.

Both loader seams now go through `prepareConfigSource(content, ref)`, which consults
`isPlainJavaScriptSource(ref)` and calls esbuild only when the source may contain types. The
Task 1 instrumentation is the observation seam — `PhaseStripTypes` is recorded exactly when
esbuild runs, so tests count the phase rather than wrapping the production call. (A
`var stripTypes = config.StripTypes` function-value seam was tried first and discarded: the
unresolvable indirect call sent gosec's taint analysis off the rails and produced a
false-positive G703 on unrelated code in `discoverAutoConfig`.)

The rule is the extension and nothing else — `.js`/`.mjs` pass through, everything
unrecognised (`.ts`/`.mts`/`.cts`, `.cjs`, no extension, `default`, an OCI reference) still
strips, because esbuild is a no-op on valid JavaScript while a wrong guess hands goja
TypeScript and fails far from its cause. `internal/remotecfg` needs no change: its content
arrives at `loadConfigString` with the source **URL** as the ref, and the same helper
(`configSourceExt`) reads the extension off the URL's path component only — so
`https://example.js` is a host rather than a JS file, `oci://ghcr.io/org/cfg:v1` has no extension, and a query string does not hide the extension.

Real `datamitsu exec actionlint -- --version` in this repo (whose auto config is
`datamitsu.config.ts` and whose declared before-config is the 1.95 MB
`datamitsu.config.oci-ghcr.js`), `DATAMITSU_STARTUP_TIMINGS=1`:

| Phase               | Before        | After        |
| ------------------- | ------------- | ------------ |
| `config.StripTypes` | n=3 / 47.8 ms | n=2 / 3.9 ms |
| `loadConfig` total  | 178.1 ms      | 85.0 ms      |

Wall-clock min, n=40: `exec actionlint -- --version` 161.6 ms → 127.5 ms (−34 ms). Stripping
the oci-ghcr file alone benchmarks at 34.0 ms/op, which is the whole of the win; the two
remaining strips are the `.ts` auto config, read once by the `getBeforeConfigs` pre-pass and
once as a config source.

`BenchmarkLoadConfig` moves far less because its fixture config is a three-line `.js` file,
not a 2 MB one — the benchmark measures the call being skipped, not the file it was skipping
(`-benchtime 30x -count 4`, min of 4):

| Benchmark             | Task 4    | Task 5    |
| --------------------- | --------- | --------- |
| `BenchmarkLoadConfig` | 3,289,406 | 2,719,640 |
| `BenchmarkGetGitRoot` | 576,749   | 560,964   |
| `BenchmarkEngineNew`  | 671,474   | 661,688   |

(ns/op. `LoadConfig` allocs 15,492 → 13,461, B/op 2,240,691 → 1,295,377 — esbuild's buffers,
gone.)

Tests: `cmd/config_js_source_test.go` (extension table incl. URLs and OCI refs, which refs
reach the esbuild seam, `.js` byte-identical pass-through of syntax esbuild would reformat,
`.ts` still stripped, and both through the real `loadConfigFile` + engine).
`TestStartupTimingsPhasesPerLoad` is now table-driven over the auto config's extension and
pins the counts directly: `.js` → 0 strips, `.ts` → 2. Every `test/cli` golden is
byte-identical after `-count=2`.

### Task 6: Pure-Go git-root discovery with a git fallback

This is the last ~21 ms and the only task in this plan with real semantic risk, which is why
it is last. `GetGitRoot` does not return the nearest `.git` — it climbs
`--show-superproject-working-tree` to the **topmost superproject**
(`internal/facts/facts.go:171-231`). A naive walk-up-to-first-`.git` returns a different
answer inside a submodule.

- [x] implement a pure-Go resolver in `internal/facts` that walks up from cwd looking for
      `.git`, and reproduces the superproject climb: when `.git` is a **file**, read its
      `gitdir:` line and classify it
- [x] distinguish the two cases that both produce a `.git` file, because they must be
      resolved differently: a **submodule** whose gitdir points into
      `<super>/.git/modules/<name>` (keep climbing — this is what
      `--show-superproject-working-tree` follows) and a **linked worktree** whose gitdir
      points into `<main>/.git/worktrees/<name>` (do **not** treat the main repo as a
      superproject)
- [x] use the pure-Go resolver as a fast path and fall back to the existing git subprocess
      path on any ambiguity, unreadable `.git` file, or unrecognised gitdir shape — never
      guess
- [x] add an env-var escape hatch to force the subprocess path, defined in
      `internal/env/e.go` + `internal/env/env.go` per the Environment Variable Usage Policy,
      so a user hitting an unforeseen layout has a documented workaround
- [x] keep the memo from Task 3 in front of both paths
- [x] write table tests over fixture layouts built in `t.TempDir`: plain repo, nested
      subdirectory, bare repo, repo with a linked worktree, repo with a submodule, submodule
      inside a submodule, and a directory with no repo at all
- [x] write a differential test asserting the pure-Go resolver and the git subprocess return
      the identical root for every fixture layout — this is the load-bearing test
- [x] write a test asserting the fallback engages (and is observable) for a malformed
      `.git` file
- [x] write a test asserting the force-subprocess env var works
- [x] record the measured drop in `BenchmarkGetGitRoot` in this file (expected 21.3 ms → ~2 µs)
- [x] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 7

**Task 6 results.** Apple M1 Max.

`internal/facts/gitroot.go` holds the walk; `resolveGitRoot` is now a dispatcher that tries
it and forks git only when it declines. The memo from Task 3 sits in front of both, unchanged.

The walk answers only for layouts it can prove, and returns `false` — "ask git" — for
everything else, because a wrong root silently produces wrong project cache keys. It declines
for: a bare repository or a cwd inside `.git/` (git reports no working tree for either),
a repository whose config sets `core.bare` — an ordinary `.git` directory can be marked bare,
and `--show-toplevel` then fails outright, so the check reads the value rather than the key
(`git init` writes `bare = false` into every config), a
`.git` directory that is not a valid repository (git would climb past it; how far is git's
business), a repository nested inside another repository's working tree (deciding whether the
outer one records it in its index as mode 160000 means reading that index), a separate git
directory, a gitdir path carrying both `modules` and `worktrees` (a linked worktree of a submodule —
which link to follow is not decidable from the path), a `.git` file whose target does not
exist, and a malformed or unparsable `.git` file. `DATAMITSU_FORCE_GIT_SUBPROCESS=1` skips the
walk entirely.

Two behaviours the naive walk-up-to-the-first-`.git` gets wrong are reproduced explicitly:
`--show-toplevel` reports a _physical_ path, so the walk starts from cwd with symlinks
resolved (on macOS every `t.TempDir` is under the `/var` → `/private/var` symlink, so this is
not a corner case); and the submodule climb jumps to the **outermost** `.git` component of
the gitdir, so `<super>/.git/modules/outer/modules/inner` reaches the topmost superproject in
one step instead of one level at a time.

| Benchmark             | Task 5    | Task 6    | Drop     |
| --------------------- | --------- | --------- | -------- |
| `BenchmarkGetGitRoot` | 560,964   | 5,558     | −0.56 ms |
| `BenchmarkEngineNew`  | 661,688   | 138,161   | −0.52 ms |
| `BenchmarkLoadConfig` | 2,719,640 | 2,236,744 | −0.48 ms |

(ns/op, `-benchtime 30x -count 4`, min of 4. `GetGitRoot` allocs 9 → 6, `EngineNew` 1,012 →
1,009.) These are still the amortized numbers of the Task 3 note — one real resolution spread
over 30 iterations. The uncached first call is what actually moved, measured directly by the
new `BenchmarkGitRootPure` / `BenchmarkGitRootSubprocess` pair in `internal/facts` on the same
fixture (`-benchtime 200x -count 2`, min):

| Resolver               | ns/op      | B/op    | allocs/op |
| ---------------------- | ---------- | ------- | --------- |
| `resolveGitRootViaGit` | 16,892,365 | 114,831 | 165       |
| `gitRootPure`          | 42,194     | 11,488  | 116       |

16.9 ms → 42 µs, a factor of 400. (Not the ~2 µs the plan estimated: most of the remaining
cost is `EvalSymlinks` on a deep `/var/folders/...` fixture path plus one `Stat` per ancestor
level, and both are real work the subprocess also did.)

End to end, in this repository with a PATH shim logging every `git` argv:
`datamitsu exec actionlint -- --version` forks git **0** times (baseline 10, Task 3 left 2),
`facts.GetGitRoot` reports 61.8 µs (was 17.4 ms at Task 1), and `loadConfig` totals 80.9 ms.
Wall-clock min, n=40: `datamitsu version` **11.3 ms**, `datamitsu exec actionlint -- --version`
**82.2 ms** (was 127.5 ms after Task 5).

Tests: `internal/facts/gitroot_test.go`. Eleven fixture layouts built with real git — plain
repo, nested subdirectory, bare repo, linked worktree, submodule, submodule inside a
submodule, no repository, inside `.git/`, nested repository, empty `.git` directory, separate
git directory — run twice: once pinning what the walk must answer or decline, and once as the
load-bearing differential test that its answer equals git's for every layout it answers for.
Plus `gitSubprocessLookup`, a counting seam that makes the fallback observable (asserted to
run exactly once for a malformed `.git` file and for `DATAMITSU_FORCE_GIT_SUBPROCESS=1`, and
zero times for a plain repository), and unit tables over `parseGitFile` and
`classifyGitDirPath`.

➕ Follow-up from review, implemented here: the submodule climb originally accepted
`.gitmodules` as proof that the superproject still registers the submodule. Git tests the
superproject's **index** for a gitlink (mode 160000), not that file, and the two disagree after
`git rm --cached <sub>` — verified against real git: `--show-superproject-working-tree` goes
empty while `.gitmodules` still lists the path, so the walk would have answered with a root git
disagrees with. `internal/facts/gitindex.go` now reads the index and proves the gitlink; the
`.gitmodules` check is kept in front of it as the cheaper filter. The reader is deliberately
partial — index version 4 (prefix-compressed entry paths), an unrecognised entry shape, an
oversized file, or trailing bytes that do not parse as extension blocks all report false, which
declines the climb and costs one subprocess rather than risking a wrong root. The digest size
is not recorded in the index header, so the scan tries SHA-1 and SHA-256 and keeps whichever
accounts for every byte up to the trailing checksum. Three fixture layouts pin it: a submodule
unregistered from the index (declines), a sha256 superproject (answers, exercising the 32-byte
branch) and an index version 4 superproject (declines).

The scan matches **stage 0 entries only**. Git answers this question from the first
`ls-files --stage` line for the path, and an unmerged path has no stage 0 entry at all — its
stages 1, 2 and 3 are listed in that order, so a file/gitlink conflict whose stage 1 is a regular
file makes git report no superproject even though stages 2 and 3 are gitlinks. Verified against
real git: with `100644` at stage 1 and `160000` at stages 2 and 3,
`--show-superproject-working-tree` is empty and `--show-toplevel` is the submodule itself, while
a stage-blind scan answered with the superproject. A fourth fixture builds exactly that index via
`update-index --index-info` and pins the decline.

Two further review fixes in the same area: `containsPath` treated a relative path of exactly
`".."` as containment, which would have let `superprojectOf` accept a gitdir owner that is a
child of the candidate superproject; and `findWorkTreeRoot` treated every `Lstat` failure on a
`.git` entry as "absent" and kept climbing, so a permission or IO error could skip the nearest
repository marker and answer with an ancestor root — only `fs.ErrNotExist` now continues the
climb, anything else declines. `resolveGitRoot` also checks `ctx.Err()` before either path: the
subprocess path got cancellation from `exec.CommandContext`, but the pure-Go walk touches only
the filesystem and would otherwise let a cancelled config load carry on (the memo already drops
context failures rather than caching them).

`go test ./...`, `go test ./internal/facts/ ./cmd/ -race` and `go test ./test/cli/ -count=2`
are green with no golden regenerated. `go test ./... -race` still hits the pre-existing
`internal/cache` `debounceSave` panic flagged under Task 3 — unrelated and unchanged.

➕ For Task 8: `DATAMITSU_FORCE_GIT_SUBPROCESS` (and `DATAMITSU_STARTUP_TIMINGS` from Task 1)
need rows in the environment-variable table at `website/docs/reference/cli-commands.md:1038`.
Deferred there deliberately so `task gen:llms-docs` runs once, not once per task.

### Task 7: Verify acceptance criteria

- [x] `datamitsu version` wall-min is ≤ 15 ms (baseline 39.7–51.9 ms) — **13.2 ms, met**
- [x] `datamitsu exec <installed binary app> -- --version` wall-min is ≤ 70 ms
      (baseline 199.6–259.9 ms) — measured **93.4 ms**, ⚠️ **NOT met** (see below)
- [x] `git rev-parse` forks per `datamitsu exec` is ≤ 2, verified by a PATH shim script that
      logs argv and then execs the real git (baseline: 10) — **0, met**
- [x] `esbuild StripTypes` calls per `datamitsu exec` no longer include the 1.07 MB
      before-config, verified from the debug log at `internal/config/config.go:476` — **met**
- [x] `go test ./...` passes with `-race` — met, after fixing the pre-existing
      `internal/cache` panic that had blocked this gate since Task 3
- [x] `go test ./test/cli/ -count=2` passes with **zero regenerated goldens** — `git status`
      shows no changes under `test/cli/testdata/golden/` — **met**
- [x] `pnpm dm check` (linters) passes — met (22 tools, 80 runs, 0 failures)
- [x] coverage meets the project standard via `pnpm test:coverage:all` — met, 81.0% combined
- [x] re-run `scripts/bench-overhead.sh` and record before/after in this file — met

**Task 7 results.** Apple M1 Max, load average 36–55 (not idle — every wall-clock figure below
is an upper bound). Wall-clock **min** over n=40, per the measurement discipline.

| Criterion                            | Baseline       | Now     | Target | Verdict |
| ------------------------------------ | -------------- | ------- | ------ | ------- |
| `datamitsu version` wall-min         | 39.7–51.9 ms   | 13.2 ms | ≤15 ms | ✅ met  |
| `exec actionlint -- --version`       | 199.6–259.9 ms | 93.4 ms | ≤70 ms | ❌ miss |
| `git` forks per `exec`               | 10             | 0       | ≤2     | ✅ met  |
| Largest `StripTypes` call per `exec` | ~34 ms         | 9.6 ms  | —      | ✅ met  |

`scripts/bench-overhead.sh`, n=50, attributable launch overhead (`[2].startup − [1].startup`):

| Measure | Baseline | Now     |
| ------- | -------- | ------- |
| min     | 265.6 ms | 76.8 ms |
| median  | 335.0 ms | 80.2 ms |

(`bare bash -c` baseline moved 9.0 ms → 7.2 ms min, so the comparison is like for like.)

**Git forks.** A PATH shim — a `git` script early on `PATH` that logs argv and then `exec`s the
real git — records **0** invocations for `datamitsu exec actionlint -- --version` (baseline 10, Task 3
left 2). The shim is proven live by two controls: a direct `git rev-parse` through it logs 1,
and `DATAMITSU_FORCE_GIT_SUBPROCESS=1` logs 2 — so 0 is the pure-Go walk from Task 6, not a
shim that failed to intercept.

**esbuild.** `datamitsu exec actionlint -v` emits three `esbuild StripTypes` debug lines at
9.6 ms, 1.2 ms and 0.5 ms — two for the `.ts` auto config (pre-pass + config source) and one
for the embedded default config, which is deliberately still stripped. The ~34 ms strip of the
1.95 MB `datamitsu.config.oci-ghcr.js` is gone. (The startup-timings report shows `n=2` rather
than 3 because `GetDefaultConfig` does not route through the instrumented phase.)

**⚠️ Why the `exec` criterion misses, and why it is not a regression.** `DATAMITSU_STARTUP_TIMINGS=1`
on a real `exec` in this repository reports `loadConfig` at 110 ms with every instrumented
sub-phase summing to ~10 ms (`getConfig` 3.8 ms, `discoverBeforeConfigs` 2.8 ms,
`config.StripTypes` 2.6 ms, `engine.New` 0.7 ms, `facts.GetGitRoot` 0.07 ms). The unattributed
~100 ms is goja parsing and executing the config sources — dominated by the 1,954,839-byte
`datamitsu.config.oci-ghcr.js` before-config.

Measured directly in two scratch repositories with otherwise identical three-line configs:

| Scratch repo                              | `loadConfig` |
| ----------------------------------------- | ------------ |
| trivial config only                       | 9.7 ms       |
| same + the 1.95 MB `.js` as before-config | 67.3 ms      |

+58 ms from that one file, with **zero** `StripTypes` calls — esbuild is correctly skipped, so
the cost is goja compile+execute and nothing this plan targets. The plan's own "What this plan
does not do" section excludes cross-process config-evaluation caching and names it the dominant
remaining cost (~85 ms) and the natural next plan; that is exactly what this measurement shows.
The ≤70 ms target was an estimate that did not account for this repository's unusually large
before-config. All three buckets this plan set out to remove are gone: runewidth init (−28 ms),
git forks (10 → 0), and the wasted esbuild pass (−34 ms). Total: 199.6 ms → 93.4 ms, a 2.1×
improvement, with the remainder isolated, attributed and handed to the follow-up plan.

**➕ Out-of-plan fix required by the `-race` gate.** `go test ./... -race` had been failing
since Task 3 on a pre-existing `internal/cache` crash, flagged there as unrelated and verified
on the unmodified tree. It blocks this criterion, so it is fixed here rather than deferred.

Root cause: `newVerdictTestCache()` built a `Cache` as a bare struct literal with no logger.
Every verdict write calls `MarkDirty`, which arms the 100 ms debounce timer; the timer fired
after its test had returned, `Save()` failed on the empty path, and `c.logger.Warn` dereferenced
nil **in a timer goroutine** — unrecoverable, so it killed the whole test binary. Suppressing
the panic then exposed 7 data races underneath: the same orphaned timers were serializing
`c.data` while later tests mutated theirs.

Three changes, all narrow: `Cache.log()` returns `zap.NewNop()` when the logger is nil and all
ten logging sites route through it (a panic in a detached goroutine takes the process down
rather than failing one operation, so logging must never assume a logger); `Shutdown` guards
`close(c.shutdownCh)` against a nil channel so a bare `Cache` can always have its timer stopped;
and the test helper takes `*testing.T` and registers `t.Cleanup(c.Shutdown)` so no timer
outlives its test. Regression tests in `internal/cache/logger_guard_test.go` cover the
debounced-save-with-nil-logger path, `Shutdown` idempotency without a channel, and the non-nil
fallback. Both new functions are at 100% statement coverage.

No production behavior changes: `NewCache` has always supplied a logger and a shutdown channel,
so these paths are unreachable outside tests. Every `test/cli` golden is byte-identical.

### Task 8: [Final] Update documentation

- [x] record the final measurement table in this plan file, replacing the estimates
- [x] if `scripts/bench-overhead.sh` gained flags or output, update its usage comment —
      **no change needed**, the script is byte-identical to `main` (`git diff main...HEAD --
scripts/` is empty): it gained no flags and its output shape is unchanged, only the
      numbers it prints moved
- [x] add a short note to `website/docs/guides/architecture/execution.md` (or the nearest
      architecture page) describing the config-load cost model and the memo, so the next
      person does not re-introduce a per-engine `GetGitRoot`
- [x] run `task gen:llms-docs` and commit `internal/llmsdocs/embed` if any website page
      changed — the `llms-docs-drift` CI job re-harvests on every PR and fails on any diff

**Task 8 results.**

**Final measurements**, replacing the Overview's estimates. Apple M1 Max, wall-clock **min**
over n=40, machine under load (every figure is an upper bound):

| Path                                         | Baseline (`20c75ba`) | Final   | Change |
| -------------------------------------------- | -------------------- | ------- | ------ |
| `datamitsu version`                          | 39.7–51.9 ms         | 13.2 ms | −3.5×  |
| `datamitsu exec actionlint -- --version`     | 199.6–259.9 ms       | 93.4 ms | −2.1×  |
| `actionlint --version` direct from the store | 6.1–7.3 ms           | —       | —      |
| empty Go binary (`func main(){}`)            | 6.0–7.0 ms           | —       | —      |

`scripts/bench-overhead.sh`, n=50, attributable launch overhead
(`[2].startup − [1].startup`): min 265.6 ms → **76.8 ms**, median 335.0 ms → **80.2 ms**.
The `bare bash -c` baseline moved 9.0 ms → 7.2 ms min, so the comparison is like for like.

Per-bucket, against the Overview's three buckets:

| Bucket                      | Estimated | Removed      | Where      |
| --------------------------- | --------- | ------------ | ---------- |
| `go-runewidth` package init | ~33 ms    | −28.0 ms     | Task 2     |
| forked `git rev-parse`      | ~105 ms   | 10 → 0 forks | Tasks 3, 6 |
| wasted esbuild `StripTypes` | ~25 ms    | −34.2 ms     | Task 5     |

Benchmarks across the whole plan (ns/op, `-benchtime 30x -count 4`, min of 4; the
`GetGitRoot`/`EngineNew` rows are amortized over 30 iterations per the Task 3 note):

| Benchmark             | Task 1 baseline | Final     | Change |
| --------------------- | --------------- | --------- | ------ |
| `BenchmarkLoadConfig` | 73,473,125      | 2,236,744 | −32.8× |
| `BenchmarkGetGitRoot` | 16,568,433      | 5,558     | −2981× |
| `BenchmarkEngineNew`  | 17,170,300      | 138,161   | −124×  |

Uncached git-root resolution, measured directly (`-benchtime 200x -count 2`, min):
`resolveGitRootViaGit` 16,892,365 ns/op → `gitRootPure` 42,194 ns/op, a factor of 400.

The `datamitsu version` target (≤15 ms) is met. The `exec` target (≤70 ms) is not, at
93.4 ms; Task 7 attributes the gap to goja compiling and executing this repository's
1.95 MB `datamitsu.config.oci-ghcr.js` before-config (+58 ms, measured in isolation with
**zero** `StripTypes` calls), which is cross-process config-evaluation caching — explicitly
out of scope here and named as the natural next plan.

**Documentation.** The config-load cost model went to a new architecture page,
`website/docs/guides/architecture/startup.md`, rather than into `execution.md`: that page
documents the parallel executor, and startup precedes the whole discovery → planning →
execution → cache pipeline. It covers the three cost rules (per-engine work is multiplied by
the source count, forking dominates, evaluation scales with source size), the git-root memo
with an explicit warning against re-introducing a per-engine resolution, the pure-Go walk and
when it declines to answer, the extension-only type-stripping rule and why content sniffing
is not used, and how to measure with `DATAMITSU_STARTUP_TIMINGS=1` and
`scripts/bench-overhead.sh`. Linked from `architecture/index.md` (component table, stage
diagram, reading order) and `website/sidebars.ts`.

`DATAMITSU_STARTUP_TIMINGS` and `DATAMITSU_FORCE_GIT_SUBPROCESS` are now rows in the
environment-variable table at `website/docs/reference/cli-commands.md` — the ➕ item deferred
from Task 6. `internal/llmsdocs/embed` regenerated with `task gen:llms-docs`.

## Technical Details

**Measurement discipline.** All baseline numbers above were taken on a machine under load
average 25–205. Wall-clock **minimum** over n≥40 is the estimator to use; medians are
contaminated by contention. Report min, and state n.

**Why the goldens must not move.** This plan changes no observable behavior. `test/cli` runs
the compiled binary and byte-compares stdout/stderr. If a golden shifts, the change altered
output — most plausibly through the runewidth bump changing column math in `internal/ui`.
Investigate; do not regenerate.

**Why the memo is sound.** datamitsu is a short-lived process that does not `chdir` mid-run,
so cwd → git root is constant for the process. The one exception is `cmd/lsp.go`, which is
long-lived; Task 3 requires verifying the LSP does not depend on re-resolving the root within
a session.

**What this plan does not do.** It does not cache config evaluation across processes, does not
reduce the four-VM structure, and does not touch `discoverBeforeConfigs`' habit of building an
entire engine to read one function that returns a list of filenames (`cmd/config_loader.go:624`,
32.5 ms). Those are real, but they are architecture changes and they belong with the consumer
that needs them.

## Post-Completion

**Manual verification:**

- Re-run every benchmark on an idle machine (load average < 2) and update the recorded
  numbers. The committed figures are upper bounds.
- Verify on Linux as well as macOS — the git-fork cost and the `.git` file layouts differ.
- Verify inside a real submodule checkout and a real `git worktree` before trusting Task 6.

**Follow-up work this plan deliberately excludes:**

- Cross-process config-evaluation caching. This becomes the dominant remaining cost
  (~85 ms) and is the natural next plan. Note the CLAUDE.md forward contract: config JS
  evaluation is not cached today, and `internal/engine/configinputs.go:36-41` documents that
  every field exposed in `datamitsuConfigInputs` must be folded into the cache fingerprint
  when it is.
- Collapsing `discoverBeforeConfigs`' dedicated engine into a cheaper read.
- Store garbage collection. Unrelated to startup, but discovered alongside: the store is
  14 GB with no GC, and `lefthook` alone holds six coexisting 13 MB copies.
