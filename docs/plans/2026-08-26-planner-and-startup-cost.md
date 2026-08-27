# Plan: Cut per-invocation overhead on large repositories

**Status:** phase 1 implemented and measured (uncommitted working tree); phases 2–4 designed, not
started.
**Date:** 2026-08-26.
**Related:** `internal/tooling/planner.go`, `internal/tooling/verdict.go`, `internal/globmatch`,
`internal/datamitsuignore`, `internal/project`, `internal/traverser`, `internal/trace`,
`cmd/config_loader.go`, `internal/facts`, `internal/runner`.
**Follows:** `docs/plans/completed/2026-08-19-cli-startup-cost.md`, which cut process startup and
named cross-process config-evaluation caching as the next step. This plan finds that the config
load was no longer the dominant cost on a large repository — planning was, by an order of
magnitude — removes that, and leaves the config cache as phase 2.

## Measurement subject

All numbers below are from a large private TypeScript monorepo used as the benchmark: **14,711
tracked files, ~60 workspace packages, 44 configured tools of which 29 are applicable and 15 are
disabled tree-wide by `.datamitsuignore`**. Host: Linux, i9-14900K, warm page cache, warm store.
Both binaries built from the same commit, differing only by this plan's changes, invoked
identically through `--before-config <2 MB shared config>`. Wall-clock **min over n=9**, which is
the estimator to use on a machine that is not idle.

The repository itself is not named anywhere in this document and no path, package or branch from it
appears here; only its shape, which is what the numbers scale with.

## Results, phase 1

| Scenario                                          | Before        | After        | Change          |
| ------------------------------------------------- | ------------- | ------------ | --------------- |
| `version`                                         | 6.8 ms        | 6.5 ms       | —               |
| `lint --explain=summary` (plans, runs zero tools) | **1992.7 ms** | **166.0 ms** | **−92%, 12.0×** |
| `exec <installed tool> -- --version`              | 97.1 ms       | 80.8 ms      | −17%            |
| `lint`, warm caches                               | 4699.1 ms     | 2743.8 ms    | −42%            |
| `check`, warm caches                              | 8792.7 ms     | 5852.7 ms    | −33%            |
| `lint`, execution cache cleared                   | 15.5 s        | 11.9 s       | −23%            |

`--explain` is the honest measure of datamitsu's own overhead: it performs the entire config load
and the entire plan and then runs nothing. The other rows are diluted by the tools' own work — on
this repository one `tsc` invocation alone takes ~2 s and 232 s of CPU across 59 projects.

Inside that 166 ms: `loadConfig` 39 ms, `collectTasks` 58 ms, two repository walks 44 ms, process
floor ~7 ms. For `exec`, where there is no planning at all, `loadConfig` is half the command:
39.3 ms of 78.1 ms.

**The plan is unchanged.** `--explain=json` from both binaries yields the identical set of 343 lint
tasks and 205 fix tasks — same tool, working directory, scope, granularity, arity, coverage and file
set for every one. Every `test/cli` golden is byte-identical after `-count=2`.

## Phase 1 — implemented

Ordered by measured win.

### 1.1 `groupFilesByProject` called `filepath.Rel` per (file × project)

`internal/tooling/planner.go`. The innermost loop of planning tested containment with
`filepath.Rel`, which splits both paths, walks their components and allocates a string — for every
matched file against every detected project, for every per-project tool. Roughly 1.6 M calls per
`lint`. Both sides are absolute and cleaned, so containment is a prefix test.

**−333 ms → −8 ms.** The single largest item in the whole plan.

### 1.2 Tools disabled tree-wide still paid a full glob sweep

`internal/datamitsuignore/matcher.go`, `internal/tooling/planner.go`. Under the opt-in model most
configured tools are disabled by a root `**/*: <tool>` rule. They produced no tasks — after
sweeping every file in the repository. `Matcher.DisabledEverywhere` proves the "disabled and
nothing can re-enable it" case conservatively (a root-scoped whole-tree disable, and no inversion
anywhere naming the tool or `*`), and the planner drops such tools before matching.

15 of 44 tools dropped; **−174 ms**.

### 1.3 Glob matching re-parsed the pattern on every path

New `internal/globmatch`. `doublestar.Match` takes a pattern _string_ and parses it per call; the
planner made 1.77 M such calls per `lint`. Patterns are now classified once:

- `**/*<literal>` → `HasSuffix`
- `**/<literal-segment>` → final-segment equality
- a pattern with no metacharacters → string equality
- anything else → doublestar, with a necessary-condition extension pre-filter

The equivalences are pinned by `TestFastPathsMatchDoublestar`, which checks every recognised shape
against the real matcher over a corpus of awkward paths (dotfiles, multi-dot names, a directory
whose name carries the extension, non-ASCII), and by `TestPrepareKinds`, which fails when a
refactor quietly stops taking a fast path.

1.77 M matches → 206 K. **−~150 ms**, and it made 1.5 and 1.6 cheap.

### 1.4 Root-relative paths were derived per file per tool

`internal/tooling/planner.go`, `internal/project/detector.go`. 426 K `filepath.Rel` calls in glob
matching plus 88 K in the `.datamitsuignore` probe, for a value that is constant within a run. The
walk's paths are absolute, clean and under the root, so the relative form is a slice of the existing
string; it is derived once beside `cachedFiles`.

### 1.5 Project detection walked the tree once per project type

`internal/project/detector.go`. `DetectAll` called `matchesType` per type and each of those ran a
full gitignore-aware traversal — dozens of identical walks on the `init` path.
`DetectAllWithLocationsFromFiles` also did `filepath.Rel` + `doublestar.Match` per file per marker.
Both fixed; detection on the lint path went **107 ms → 4 ms**.

### 1.6 Two concurrent walks produced the same file list

`internal/tooling/planner.go`. `initializeCache` ran discovery and detection in an errgroup, each
walking the whole tree. Concurrency hid the duplication rather than removing it. Now one walk feeds
both. Walks per `lint`: 3 → 2, and later to 1 in §4.1.

### 1.7 `unitMembers` and `unitGuards` recomputed per task

`internal/tooling/verdict.go`. `unitMembers` scanned every tracked file per task; `unitGuards`
stat'ed every guard name at every ancestor level per task (16,325 stats, 14,079 of them ENOENT).
Both depend only on the directory. Memoized: guard stats 16,325 → 6,441, `attachUnit` 43 ms → 20 ms.

### 1.8 `.datamitsuignore` matching allocated per query

`internal/datamitsuignore/matcher.go`. `IsDisabled` built a slice of applicable entries, sorted it,
and allocated a decision map — on every (file, tool) pair. Entries are now kept sorted at insertion,
each rule's glob is scoped and compiled once, a rule that names neither the tool nor `*` is rejected
before its glob is matched, and the single tool's decision replaces the map.

### 1.9 `facts` forked `ldd` once per engine and ignored `DATAMITSU_LIBC`

`internal/facts/facts.go` called `target.DetectLibc` directly instead of the memoized
`target.HostTarget()`. Four wasted `ldd` forks per invocation — **and a correctness bug**: that path
does not honour the `DATAMITSU_LIBC` override, so `facts().libc` could disagree with the libc used
for store paths and OCI bundle selection.

### 1.10 The runner forked `git` for a root it already had

`internal/runner/runner.go` used `traverser.GetGitRoot`, which always forks two processes per
hierarchy level, while `facts.GetGitRoot` — memoized and resolved from the filesystem since the
previous plan — had answered the identical question microseconds earlier. The previous plan's
"git forks 10 → 0" was true of `exec`; `lint`/`fix`/`check` still forked. Now they do not.

### 1.11 Setup content was evaluated on every command and discarded

`cmd/config_loader.go`. Every config load rendered every setup entry's `content()` — 57 file reads
and 100 VM calls for the shared config — and only `setup`, `init` and `config chain-hash` consume
the result. It is now opt-in via `loadConfigOptions.evaluateSetupContent`, and
`loadConfigForChainHash` exists so the one non-setup consumer asks for it explicitly.

### 1.12 Parser prewarm was a serial barrier

`internal/runner/runner.go`. `wazero.CompileModule` (~80 ms) ran to completion before the first tool
started, although nothing can parse output before a tool has produced any. It now runs in the
background; `compiledFor` collapses concurrent callers onto one compilation, so a parse arriving early joins the in-flight
compilation.

### 1.13 ➕ Discovered: the plan was non-deterministic

Not a performance issue, found while diffing two runs to prove the plan had not changed. The same
binary produced a **differently ordered plan on every invocation**: `createPerProjectTasksWithFiles`
ranged over a map, and the concurrent walk appended files in whatever order its goroutines won.

Consequences: `--explain=json` could not be diffed between two runs of the same binary on an
unchanged repository; the argv handed to each tool varied run to run; and — the one with teeth —
`internal/datamitsuignore` resolves precedence between two ignore files at equal depth by
**discovery order**, so which of two same-depth rules won was random.

Fixed by sorting the walk result and iterating project paths in order. `--explain=json` is now
byte-stable across runs; `TestPlanOrderIsDeterministic` and
`TestFindFilesFromPathReturnsSortedPaths` pin both halves. Cost: one sort of the file list per walk.

### 1.14 ➕ Discovered: a failed run reported "done in 0ms"

`internal/tooling/executor.go`. `executeGroup` assigned `result.WallClockDuration` at the bottom of
the function, and its two fail-fast branches `return result` before reaching it. A group that failed
fast therefore reported zero, and the run footer showed `done in 0ms` for a run that had just spent
seconds spawning processes — or, with several groups, the sum of only the groups that happened not
to fail (an observed run whose tools took 27 s reported `done in 1.13s`).

Fixed by recording the duration in a `defer`, so every return path measures.
`TestExecuteGroupRecordsDurationOnFailFast` covers both fail-fast branches and the passing path; it
was verified to fail against the unfixed function, which is the only way to know a test of this
shape is load-bearing.

### 1.15 Telemetry: `internal/trace`

Spans with wall offsets, monotonic durations and pre-registered counters, written per run as a
Chrome Trace Event file (openable in Perfetto) plus an aggregated text report, under
`{cache}/traces` or `DATAMITSU_TRACE_DIR`.

**Gating, and why.** Off unless `DATAMITSU_TRACE=1`. Measured, interleaved, n=25 on
`lint --explain`: a normal build with tracing off is **164.6 ms** against **163.5 ms** for a build
with the facility compiled out — indistinguishable. Tracing on costs **+5.9 ms**, which includes
recording ~1,400 spans and writing two files.

An `-ldflags -X` value cannot compile telemetry out: the linker injects a _variable_, and the
compiler must assume it may be true, so every call site, string literal and package `init` stays
linked. A build tag with a paired `const` does eliminate it, and `-tags datamitsu_notrace` is
provided — verified: 27 fewer symbols, the report's string literals absent, 60 KB smaller, and
`DATAMITSU_TRACE=1` writes nothing. But the default is compiled-in, because the point of this
facility is to measure a **released** binary in a real repository without rebuilding it, and the
measurement above says that costs nothing.

`DATAMITSU_TRACE` and `DATAMITSU_TRACE_DIR` are in `environExcluded`
(`internal/env/environ.go`): `Environ()` feeds the source-mode farm staleness key, so a variable
that only turns observation on would otherwise make every farm stale the moment someone tried to
measure one — and the first traced invocation would measure a rebake instead of the command under
study. `TestTraceVarsExcludedFromEnviron` pins it, with a control so it cannot pass vacuously.

## Phase 2 — cross-process config-evaluation cache

**Recommendation: measured, feasible, and deliberately not next.** The numbers are below; the
reason is the last subsection.

The largest remaining per-invocation cost, and the one the previous plan named. After phase 1.11
removed the wasted setup evaluation, `loadConfig` is **39.3 ms** on this repository, paid by every
command that loads config: goja re-parses the ~2 MB shared config (`compileConfig` 14.7 ms),
re-runs its top level (1.7 ms), calls `getConfig` per layer (2.2 ms) and marshals the merged result
out of the VM (`exportConfigResult` 13.7 ms), then validates it (2.4 ms).

### 2.0 Feasibility, measured

`cmd/config_cache_feasibility_test.go` (gated on `CONFIG_CACHE_FEASIBILITY`, which points at a real
before-config; a synthetic fixture would measure nothing). `-benchtime 30x -count 3`, min of 3,
same host as the rest of this plan.

| Measurement                                       | Cost        |
| ------------------------------------------------- | ----------- |
| **Evaluate the chain in goja** (what a hit skips) | **31.6 ms** |
| Read the 1.95 MB config + XXH3 it (the cache key) | 0.275 ms    |
| Read the 1.80 MB artifact + msgpack-decode it     | 1.157 ms    |
| **Total hit path**                                | **~1.4 ms** |
| msgpack marshal (miss path, write)                | 0.59 ms     |

(The 31.6 ms benchmark loads two layers in a warm process; the real three-layer chain in a cold
process measures 39.3 ms in a trace. The benchmark is the conservative figure.)

Supporting numbers, each of which settles a design question:

| Question                                | Measurement                                                                     | Answer                                                                                             |
| --------------------------------------- | ------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| Hash content, or `stat` mtime+size?     | `stat` 0.6 µs vs read+hash 275 µs — but the artifact read is 1.16 ms either way | **Content.** Robustness costs 1% of the win.                                                       |
| XXH3 or SHA-256 for the key?            | XXH3 30 µs (64 GB/s) vs SHA-256 704 µs (2.8 GB/s) over 1.95 MB                  | **XXH3**, 24× cheaper — and what the hashing policy requires for an internal key over local files. |
| msgpack or JSON for the artifact?       | decode 0.77 ms vs 6.7 ms; 1.80 MB vs 1.86 MB                                    | **msgpack**, 8.7× faster to decode.                                                                |
| Does the config survive the round trip? | `TestConfigCacheRoundTripsThroughMsgpack` compares the whole graph, not counts  | **Yes**, byte-identical.                                                                           |

So the mechanism works: **31.6 ms → 1.4 ms, a factor of 22.**

### 2.1 Why it is not next, despite that

Where the ~38 ms actually lands:

| Command                       | Now     | With the cache | Change        |
| ----------------------------- | ------- | -------------- | ------------- |
| `exec <tool> -- --version`    | 78.1 ms | ~40 ms         | −49%          |
| `lint`, warm, this repository | 2744 ms | ~2706 ms       | −1.4%         |
| `version`                     | 6.5 ms  | 6.5 ms         | —             |
| a source-mode tool invocation | —       | —              | — (see below) |

Two facts decide it. First, `lint`/`fix`/`check` on a repository this size are dominated by the
tools, so a 38 ms saving is inside the run-to-run noise. Second, **source mode already bypasses the
config load entirely**: `internal/shim/shim.go` imports neither `cmd` nor `internal/engine` — it
resolves argv[0] against the baked farm and execs, never building the cobra tree. The invocation
shape that runs most often in an activated shell therefore gains nothing from this cache.

That leaves `exec` and the short commands, where the win is real and large in relative terms — and
is worth more on slower hardware: the previous plan measured the same load at 110 ms on an Apple M1
Max, so a CI runner would see two to three times this saving.

Against that, two other items have better ratios on the commands actually being run:

- **Phase 3** (`verdictKeys`) is 3.0 s of CPU on a warm `lint` whose tools ran for 2.0 s — roughly
  eighty times the config win, on the command in daily use.
- The remaining `collectTasks` (58 ms) and the two repository walks (44 ms) together match the
  config win, with no new correctness surface at all.

The config cache's risk is not in the mechanism, it is in the key, and one of its landmines has no
external symptom (§2.3.1). Do phase 3 and the phase 4 walk work first; take this when `exec`
latency, or CI, becomes the thing that hurts.

### 2.2 What it stores

**goja cannot snapshot.** `goja.Compile` returns a `*Program` with no exported serialization and
there is no VM-heap snapshot API. Two consequences: a compiled program can be reused across VMs
_within_ one process (which is what would fix the double compile of the auto config), and nothing
but the _evaluated result_ can cross a process boundary. The cache must store data, not code.

Store the merged `config.Config` as msgpack under
`{cache}/config-eval/{namespace}/{key}.msgpack`, content-addressed, written by
`CreateTemp`+`rename`. The key must be a strict superset of `sourcefarm.ComputeStalenessKey`,
because that hashes only `DATAMITSU_*` while config JS sees the whole environment through
`facts().env`. It must contain: the content hash of every chain file in chain order; the resolved
chain shape including `--no-auto-config` and the existence of unchosen auto-config candidates; the
URL and declared SHA-256 of every `getRemoteConfigs()` entry; the entire sorted environment; every
field of `datamitsuConfigInputs`; the JS-visible `Facts`; cwd and git root separately; `.git/HEAD`;
and `ldflags.Version` plus a format version.

### 2.3 Landmines, in order of how quietly they bite

1. **Non-determinism in config JS.** goja exposes `Date` and `Math.random` and nothing calls
   `SetRandSource`/`SetTimeSource`. A config reading either is not cacheable and the cache would
   serve wrong results with no external symptom. Refuse to store when the VM observed either.
2. **`ConfigSetup.Content` is a live goja function** (`json:"-"`). It cannot be serialized and must
   not be faked. A hit that returns `Setup` entries with `Content == nil` is correct for `lint` and
   wrong for `setup` — gate on the caller, which phase 1.11 already made explicit.
3. **The `datamitsuConfigInputs` forward contract** at `internal/engine/configinputs.go` says every
   exposed field must fold into this key. Landing the cache means updating that comment and adding a
   test that fails when the injected key set changes.
4. A `DATAMITSU_CONFIG_CACHE`-style toggle must go into `environExcluded`, for the reason in 1.14.
5. Caching the post-validation config is sound only because `ldflags.Version` is in the key.

Expected: 52 ms → ~1 ms on a hit, on every invocation.

## Phase 3 — verdict-cache input hashing

The largest remaining **CPU** cost on a warm run: `verdictKeys` measured **3.0 s across 120 tasks**
on a `lint` whose tools ran for 2.0 s. 117 of those 120 tasks were cache hits — three seconds of
CPU spent deciding not to run anything.

`verdictInputs` reads and XXH3-hashes every file of a unit, and `recordVerdict` does it again after
the run. A file in a package is hashed once per per-project tool — four to six times per run.

Directions, cheapest first:

1. **Reuse the pre-run hash in `recordVerdict`** where the operation is read-only. Halves it.
2. **A per-process content-hash memo, keyed by path and validated by mtime+size.** Sound as a cache
   of a pure function; the validation is what makes it safe when a `fix` rewrites files mid-run.
   `recordVerdict`'s "did the inputs move under the tool" check must bypass the memo or it becomes
   tautological.
3. **Measure whether the verdict cache is net-positive per tool.** For a unit rooted at the
   repository root, `unitMembers` is the whole working tree — hashed twice — to decide whether to
   skip a tool that might take 200 ms. Instrument `bytes_hashed` per task against that task's own
   `spawn` duration; the answer probably differs per tool.

## Phase 4 — smaller, independent

- **`getCommandInfo` per task** — 118 ms across 120 tasks, ~10 distinct apps. `EnsureTools` has
  already installed everything before the executor runs, so the result is constant. Memoize in the
  executor and hand out copies; check first that no caller mutates `CommandInfo.Env`.
- ~~**The second and third repository walks**~~ — **done**, see 4.1 below.
- **Parser instance pooling** — a fresh wazero instance per parse (~230 µs) where the count equals
  the parse count.
- **wazero compilation cache** — `NewCompilationCacheWithDir` would remove the ~80 ms compile
  entirely. **Needs an explicit security decision first**: the store re-verifies a module's SHA-256
  on every load, whereas wazero's cache hands back compiled machine code keyed by its own hash and
  never re-checked by us. That is a new trust assumption about `{cache}`, not an extension of an
  existing one, and this repository's hash policy is deliberately absolute. Not done for that
  reason.
- **Execution cache load/save** — a full msgpack decode at startup, a second decode plus an encode
  plus a whole-file rewrite on save, and per-file hashing under the exclusive lock. Count the actual
  `Save` calls per run first: the 100 ms debounce may already collapse them to one.
- **Store garbage collection** — 7.1 GB on this machine with no GC; every config-hash change orphans
  a directory permanently.

### 4.1 One repository walk per run

`internal/runner/runner.go`, `internal/bundled/datamitsuignore.go`, `internal/tooling/planner.go`.

Three consumers wanted the same gitignore-aware list of the same tree: bundled fix, bundled lint,
and the planner. Merging the two bundled passes was the easy half and landed with phase 3 (§1.6 took
`lint` from 3 walks to 2). The remaining pair looked riskier — the note above worried that a `fix`
between the walks could create or delete an ignore file — and that turned out not to be possible.
`RunFix` only rewrites files it was handed, each through a temp file in the same directory followed
by a rename. Contents change; the file **set** cannot. Nothing else runs in between.

So the runner walks once and hands the list to both: `bundled.IgnoreFilesIn` filters it, and
`Planner.SeedFiles` gives the planner the same slice to use in place of its own walk. The matcher is
still built from disk after bundled fix has run, so the rules that take effect are the fixed ones —
seeding shares the file set, not the file contents.

This is also a correctness change, not only a saving. `buildIgnoreMatcher` already documented that
its file set and bundled's must be identical, because it validates the rules the planner then
applies — a divergence would mean linting one set of rules and enforcing another. That agreement was
a property two call sites had to keep; now there is one call site.

Walks per `lint` and per `check`: 2 → 1. On this repository (1,099 files) `scanFiles` goes from
2.48 ms to 157 ns and the run drops ~2.7 ms; the saving is one full traversal, so it scales with the
tree rather than being a fixed win. `--explain=json` for `lint` and `fix` is byte-identical to the
previous binary (≈500 KB and ≈168 KB of output).

`TestSeedFilesMatchesWalk` is the load-bearing test: a seeded planner and one that walked for itself
must agree on the file list, its root-relative form, the detected projects, and the ignore matcher
built from them. `TestRunPerformsOneRepositoryWalk` pins the count, and asserts the fix actually
rewrote its fixture — without that, handing every consumer an empty list would also read as one
walk. `TestInvalidateDropsAnUnconsumedSeed` keeps a stale snapshot from surviving the call whose
whole purpose is to discard stale snapshots.

## Verification standard used

Every phase-1 change was checked three ways: `go test ./...` green (with `umask 022` — a 0002 umask
makes the source-mode tests fail on the test binary's own file mode, unrelated to any change),
`go test ./test/cli/ -count=2` green with zero regenerated goldens, and `--explain=json` compared
against the baseline binary as a set of tasks. The last one is what catches a planning optimisation
that quietly changes what runs.
