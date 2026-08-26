# Plan: Cache config evaluation across processes

**Status:** ready for implementation. Every design decision below is closed and backed by a
measurement; nothing is left to be chosen during the work.
**Date:** 2026-08-26.
**Related:** `cmd/config_loader.go`, `internal/config`, `internal/engine`, `internal/facts`,
`internal/env`, `internal/hashutil`, `internal/sourcefarm`, `internal/cache`.
**Follows:** `docs/plans/2026-08-26-planner-and-startup-cost.md` (phase 1, landed) and
`docs/plans/completed/2026-08-19-cli-startup-cost.md`, which first named this work.

**Branch:** `perf/config-eval-cache`, branched from `perf/large-repo-overhead` (or from `main` once
that has merged). Review base ref: `main`.

## Overview

Every command that loads config re-evaluates the whole chain in goja: it parses the ~2 MB shared
config, runs its top level, calls `getConfig` per layer, marshals the merged result out of the VM
and validates it. Nothing about that result is kept, so the next invocation does it again.

Measured on a 14,711-file monorepo, i9-14900K, warm caches, after phase 1:

| Measurement                                                     | Cost               |
| --------------------------------------------------------------- | ------------------ |
| `loadConfig` in a real `exec` (trace, cold process)             | **39.3 ms**        |
| — `compileConfig` (goja parses 1.95 MB)                         | 14.7 ms            |
| — `exportConfigResult` (`vm.ExportTo` of the graph)             | 13.7 ms            |
| — `validateConfig`                                              | 2.4 ms             |
| — `callGetConfig` / `runConfigTopLevel` / `readConfigFile`      | 2.2 / 1.7 / 1.2 ms |
| Evaluate the chain, isolated benchmark (2 layers, warm process) | 31.6 ms            |

And the cache's own cost, measured by `cmd/config_cache_feasibility_test.go`:

| Measurement                                   | Cost        |
| --------------------------------------------- | ----------- |
| Read the 1.95 MB config + XXH3 it (the key)   | 0.275 ms    |
| Read the 1.80 MB artifact + msgpack-decode it | 1.157 ms    |
| **Total hit path**                            | **~1.4 ms** |
| msgpack marshal (miss path)                   | 0.59 ms     |

**31.6 ms → 1.4 ms, a factor of 22.** Expected end-to-end: `exec <tool> -- --version` 78 ms → ~40 ms.
`lint` on a large repository moves ~1.4% and is not the reason to do this.

Four design questions are already settled by measurement, and must not be re-opened:

| Question                              | Evidence                                                           | Decision                                                                                       |
| ------------------------------------- | ------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| Hash content or `stat` mtime+size?    | `stat` 0.6 µs vs read+hash 275 µs, against a 1.16 ms artifact read | **Content.** Robustness costs 1% of the win.                                                   |
| XXH3 or SHA-256 for the key?          | XXH3 30 µs (64 GB/s) vs SHA-256 704 µs (2.8 GB/s) over 1.95 MB     | **XXH3-128**, and it is what the Hashing Policy requires for an internal key over local files. |
| msgpack or JSON for the artifact?     | decode 0.77 ms vs 6.7 ms; 1.80 MB vs 1.86 MB                       | **msgpack** (`github.com/shamaton/msgpack/v2`, already a direct dependency).                   |
| Does the config survive a round trip? | `TestConfigCacheRoundTripsThroughMsgpack` compares the whole graph | **Yes**, byte-identical.                                                                       |

## Context (from discovery)

- `cmd/config_loader.go:159` `loadConfigImpl` is the single entry point. It returns
  `(*config.Config, *config.SetupLayerMap, *goja.Runtime, error)`.
- **goja cannot snapshot.** `goja.Compile` returns a `*Program` with no exported serialization, and
  there is no VM-heap snapshot API. Only the _evaluated result_ can cross a process boundary. A
  compiled `*Program` can be reused across VMs within one process, but that is a different
  optimisation.
- `internal/config/config.go` — `ConfigSetup.Content` is `any` tagged `json:"-"`: it holds a live
  `goja.Value`. It cannot be serialized and must never be faked.
- `cmd/config_loader.go` already has `loadConfigOptions.evaluateSetupContent` (phase 1), off by
  default. Only `loadConfigForSetup` and `loadConfigForChainHash` turn it on.
- **Exactly one caller uses the returned VM**: `cmd/setup.go:80`, through `loadConfigForSetup`.
  Every other call site spells it `cfg, _, _, err`. Verified by grep over `cmd/`.
- `internal/engine/configinputs.go:36-41` carries a forward contract in a comment: config
  evaluation is not cached today, and when it is, every field of `datamitsuConfigInputs` must fold
  into the cache key. Today that is exactly `minimumReleaseAgeMinutes`.
- `internal/sourcefarm/manifest.go:377` `ComputeStalenessKey` hashes `env.Environ()`, which is
  `DATAMITSU_*` minus `environExcluded`. **That is narrower than what config JS can see**, which is
  the whole environment through `facts().env` — the shared config branches on `CI`.
- `internal/env/environ.go:20` `environExcluded` — a variable that only turns behaviour observation
  on must be listed here, or every command in a `datamitsu source` shell recomputes the farm key.
- `internal/facts/facts.go:70` `CollectWithOptions` is the full list of what JS can read about the
  host.
- `internal/hashutil` is the XXH3 wrapper; never import the third-party xxh3 package directly.
- `internal/cache/cache.go:479` `calculateInvalidationKey` already marshals the whole config with
  `json.Marshal` for the execution cache's own key. Do not let the two serializations drift.

## Development Approach

- **Testing approach**: Regular (code first, then tests), except Task 1 which is measurement-first.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** for the code it changes. Tests are listed
  as separate checklist items, never bundled into an implementation item.
- **CRITICAL: all tests must pass before starting the next task.** Run `go test ./...` and
  `go test ./test/cli/ -count=2`.
- **CRITICAL: run the test suite with `umask 022`.** With the common `umask 002` the source-mode
  tests fail on the _test binary's own file mode_ (`source mode needs it owner-executable and not
group- or world-writable`). That failure is environmental and unrelated to any change; do not
  "fix" it in the source.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Behavior must not change on a miss, and must not change _observably_ on a hit. Every `test/cli`
  golden must stay byte-identical; a golden that moves is a bug in the change, not a golden to
  regenerate.

## Testing Strategy

- **Unit tests**: required for every task. Stdlib `testing` only, table-driven, `t.TempDir`/
  `t.Setenv` — no testify.
- **Differential tests**: the load-bearing shape here. For any input, the config a hit returns must
  equal the config a miss returns. Assert on the whole graph, not on field counts.
- **Blackbox CLI tests**: `go test ./test/cli/ -count=2` with zero regenerated goldens.
- **Benchmarks**: `cmd/config_cache_feasibility_test.go` already exists and is gated on
  `CONFIG_CACHE_FEASIBILITY` pointing at a real before-config. Extend it rather than starting over.
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

### Task 1: Pin the baseline

- [x] run the existing feasibility benchmarks and record the numbers in this file:
      `CONFIG_CACHE_FEASIBILITY=<path to a real before-config> go test ./cmd/ -run XXX -bench 'ConfigCache' -benchtime 30x -count 3`
- [x] record `DATAMITSU_TRACE=1 datamitsu exec <installed tool> -- --version` in a large repository:
      the `loadConfig` span total and the command's wall total
- [x] record the same two numbers for `datamitsu lint --explain=summary`
- [x] add `BenchmarkConfigCacheHit` as a placeholder that fails until Task 5 lands, so the
      acceptance number has a home

#### Recorded baseline (2026-08-26)

Machine: i9-14900K, Linux, warm caches, machine **not** idle (a build ran alongside some
iterations) — re-run on an idle machine per Post-Completion before quoting these as final.

**Feasibility benchmarks** — `CONFIG_CACHE_FEASIBILITY=node_modules/@shibanet0/datamitsu-config/datamitsu.config.base.js`
(1,954,723 B), `go test ./cmd/ -run XXX -bench 'ConfigCache' -benchtime 30x -count 3`, best of 3:

| Benchmark                                      | ns/op          | Note                                 |
| ---------------------------------------------- | -------------- | ------------------------------------ |
| `ConfigCacheEvaluate`                          | **34,015,395** | the cost the cache removes           |
| `ConfigCacheKeyXXH3/read+hash`                 | 328,029        | the key, read from a cold page cache |
| `ConfigCacheKeyXXH3/hash-only`                 | 29,209         | 66.9 GB/s                            |
| `ConfigCacheKeyXXH3/sha256-only`               | 702,063        | 2.78 GB/s — 24× slower than XXH3     |
| `ConfigCacheKeyXXH3/stat-only`                 | 1,885          | rejected: mtime is not robust        |
| `ConfigCacheSerialize/msgpack-marshal`         | 729,195        | miss path                            |
| `ConfigCacheSerialize/msgpack-unmarshal`       | 813,532        |                                      |
| `ConfigCacheSerialize/json-unmarshal`          | 7,081,066      | 8.7× msgpack — confirms msgpack      |
| `ConfigCacheSerialize/read-artifact-from-disk` | **1,293,316**  | the whole hit path minus the key     |

Hit path ≈ 0.33 ms (key) + 1.29 ms (read + decode) = **~1.6 ms against 34.0 ms of evaluation, 21×.**

**Command-level baseline** — 14,370-tracked-file TypeScript monorepo, `--before-config` pointed at a
1.95 MB base config so the chain matches the table above; wall is the **min over n=20** measured
without tracing, spans are from separate `DATAMITSU_TRACE=1` runs (tracing itself costs ~2 ms):

| Command                  | wall min (n=20) | `loadConfig` span (3 runs) |
| ------------------------ | --------------- | -------------------------- |
| `exec task -- --version` | **60.7 ms**     | 38.2 / 40.5 / 42.3 ms      |
| `lint --explain=summary` | **155.9 ms**    | 38.0 / 38.4 / 40.3 ms      |

Inside a traced `exec`: `compileConfig` 14.7–16.0 ms, `exportConfigResult` 12.6–13.0 ms,
`validateConfig` 1.9–2.5 ms — the same shape the Overview predicted.

For scale-sensitivity, the same repository with **its own** 1.08 MB before-config: `exec` 45.4 ms
wall / 23.8 ms `loadConfig`, `lint --explain=summary` 146.0 ms wall / 24.1 ms `loadConfig`. Config
size, not repository size, drives the cost this plan removes.

⚠️ Task 6's acceptance number ("≤ 45 ms, baseline 78 ms") was written against a different machine
state; against the 60.7 ms measured here the equivalent target is **≤ 28 ms**, i.e. the baseline
minus the ~38 ms `loadConfig` plus the ~1.6 ms hit path. Judge Task 6 against the 60.7 ms figure
recorded here, re-measured on the same machine.

### Task 2: The cache key

The whole risk of this plan is here. A key that is too narrow serves a wrong config silently; a key
that is too wide only costs a miss.

- [x] create `internal/configcache` with `Inputs` (a struct, not a map) and
      `Key(Inputs) string` returning an XXH3-128 hex digest via `internal/hashutil`
- [x] `Inputs` must carry, and `Key` must hash, all of:
  - [x] the content hash of every file in the chain, **in chain order** — hash the bytes, not
        mtime+size, so the cache survives `git checkout`, `git stash` and rebuilds that reset modification times
  - [x] the resolved chain shape: absolute paths in order, the `--no-auto-config` flag, and the
        existence of every auto-config candidate that was _not_ chosen (mirror
        `internal/sourcefarm/manifest.go:292-305`)
  - [x] the URL and declared SHA-256 of every `getRemoteConfigs()` entry (content-addressed by
        construction, so the declared hash is sufficient)
  - [x] **the entire environment**, sorted — not `env.Environ()`, which is deliberately narrower.
        The shared config branches on `CI`, `DATAMITSU_DEV_MODE`, `DATAMITSU_OCI_MINIMAL` and
        `DATAMITSU_PACKAGE_NAME`; a `DATAMITSU_*`-only key is defeated by `CI` alone
  - [x] every field of `datamitsuConfigInputs` (`internal/engine/configinputs.go`) — today exactly
        `minimumReleaseAgeMinutes`
  - [x] the JS-visible `Facts` (`internal/facts/facts.go:70`): `binaryCommand`, `binaryPath`,
        `packageName`, `version`, `os`, `arch`, `libc`, `isInGitRepo`, `isMonorepo`
  - [x] cwd and git root **separately** — `isMonorepo` is derived from their relationship, and setup
        content receives paths computed relative to cwd
  - [x] `.git/HEAD` (the resolved ref), because a branch switch can add, delete or change chain files
  - [x] `ldflags.Version`, plus a `formatVersion` integer for the artifact schema
- [x] write a test asserting the key changes when each input changes, one subtest per input — this
      is the table that proves nothing was forgotten
- [x] write a test asserting the key is stable across two calls with identical inputs
- [x] write a test that fails when `configinputs.go`'s injected key set changes without `Inputs`
      changing, so adding a config input cannot silently skip the key
- [x] update the forward-contract comment at `internal/engine/configinputs.go:36-41`: it currently
      says caching does not exist
- [x] run `go test ./...` and `go test ./test/cli/ -count=2` — must pass before Task 3

### Task 3: Refuse to cache a non-deterministic config

This is the one failure mode with no external symptom, so it is built before the cache can store
anything. goja exposes `Date` and `Math.random` and nothing in this repository calls
`SetRandSource`/`SetTimeSource` (grep: zero hits outside tests). A config reading either is not
cacheable, and a cache that stored it would serve a wrong answer forever without erroring.

- [x] in `internal/engine`, install instrumented shims over `Date.now`, the `Date` constructor and
      `Math.random` that record an observation flag on the Engine
- [x] expose the flag (e.g. `Engine.ObservedNonDeterminism() bool`)
- [x] the shims must be transparent: same values, same types, same behaviour — they only record
- [x] `loadConfigImpl` must refuse to _write_ an artifact when any engine in the chain observed
      non-determinism, and log the refusal at debug level naming the source
- [x] write a test with a config calling `Date.now()` asserting the flag is set and nothing is
      stored
- [x] write a test with a config calling `Math.random()` asserting the same
- [x] write a test with a config doing neither asserting the flag is clear and an artifact is stored
      — the flag half is asserted (`TestConfigEvalCacheableRefusesNonDeterministicConfig/deterministic`);
      the "an artifact is stored" half moves to Task 5, which is where a write first exists
- [x] write a test asserting the shims return usable values (a config that computes with `Date.now()`
      still evaluates correctly)
- [x] run `go test ./...` and `go test ./test/cli/ -count=2` — must pass before Task 4

#### How the observation is made (2026-08-26)

➕ The shims are goja's `SetTimeSource` / `SetRandSource` hooks
(`internal/engine/determinism.go`), not JS wrappers around the globals. goja
reads `r.now()` for `new Date()`, `Date()` and `Date.now()` and `r.rand()` for
`Math.random()` — `builtin_date.go:16,72,98` and `builtin_math.go:216` are the
complete set of clock and entropy reads a config can make — so the hooks cover
exactly what the task asked for while staying transparent by construction.

⚠️ A JS-level shim was written first and rejected: goja's `instanceof` refuses a
Proxy on the right-hand side, so proxying `Date` makes `x instanceof Date` throw
(`TypeError: Expecting a function in instanceof check`). A plain wrapper function
breaks `Date.prototype` and `class X extends Date`
instead. The hooks have neither problem,
and a `new Date(2020, 0, 1)` never reaches the time source, so a config that
builds dates from explicit arguments keeps its cache entry.

The chain-level verdict is `cmd.configEvalCacheable()`, the OR over every
engine in the chain (including the setup-content pass). It is parked at "not
cacheable" for the duration of a load, so a chain that fails half-way cannot
leave an earlier chain's verdict standing for Task 5's write to read.

### Task 4: The artifact store

- [x] store at `{env.GetCachePath()}/config-eval/{namespace}/{key}.msgpack`. `GetCachePath()` is
      the cache tree, not the store: it must be safe to delete
- [x] `{namespace}` mirrors the source-farm layout — `projects/{XXH3-128(gitRoot)}` for a repository
      chain, and the explicit-config namespace (`internal/sourcefarm/plan.go:413`) for a
      machine-level `--config` chain. **Do not fall back to cwd**, or two directories share an entry
- [x] the artifact holds the merged `config.Config` with every `ConfigSetup.Content` nil, encoded
      with msgpack
- [x] write with `os.CreateTemp` in the same directory + `os.Rename`, never in place. Entries are
      immutable per key, so reads need no lock and a concurrent double-write of one key is harmless
- [x] a decode failure, a truncated file or an unknown `formatVersion` is a **miss**, never an
      error: a corrupt cache must degrade to evaluating, and the bad entry should be removed
- [x] prune entries unread for N days at write time — do not repeat the store's "no GC, 7 GB" mistake
- [x] `datamitsu cache clear` must clear this tree; check `internal/cache/cache.go:547` `ClearAll`
- [x] write a round-trip test over a real merged config (extend the existing one)
- [x] write a test asserting a corrupt artifact is a miss and is removed
- [x] write a test asserting an unknown `formatVersion` is a miss
- [x] write a test asserting the rename is atomic under concurrent writers (`-race`, N goroutines)
- [x] write a test asserting `cache clear` removes it
- [x] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 5

#### How the store is built (2026-08-26)

`internal/configcache/store.go`. `NewStore(namespace)` takes a namespace from
`ProjectNamespace(gitRoot)` or `ChainNamespace(configPaths)` — both delegate to
the farm identities in `internal/env` (`HashProjectPath`, `ConfigFarmIdentity`),
so the artifacts sit beside the farm rather than in a second naming scheme, and
there is no cwd fallback. Namespace and key are both validated (kind must be
`projects`/`configs`, identity and key must be lowercase hex), which makes a
path escape structurally impossible rather than merely unlikely.

➕ `Save` drops setup content through `withoutSetupContent`, which **copies**
rather than clearing in place: the caller's config is the live one the command
is about to run with. `TestStoreRoundTrip` asserts both halves.

➕ "Unread for N days" is implemented as mtime plus a refresh on the hit path:
`Load` touches an entry only once its mtime is older than `refreshInterval`
(24 h), so a config that is read daily never expires while the hit path stays a
read in the common case. `MaxAge` is 14 days, pruned by `Prune` from `Save` —
the miss path — so garbage collection never costs the fast path anything. The
walk collects and the removals happen after it, so the tree is never mutated
mid-walk.

➕ `cache clear` reaches the tree through `configcache.ClearAll` /
`ClearProject`, called from `internal/cache`'s functions of the same name. The
config-eval tree is a **sibling** of `projects/`, so clearing only `projects/`
would have left evaluated configs behind — `TestClearRemovesEvaluatedConfigs`
pins that.

### Task 5: Wire it into the loader

- [x] add `loadConfigOptions.requireVM bool` and set it in `loadConfigForSetup`, which is the only
      path whose caller uses the returned `*goja.Runtime` (`cmd/setup.go:80`)
- [x] serve from cache only when `!opts.requireVM && !opts.evaluateSetupContent`; otherwise evaluate
- [x] on a hit return `(cfg, &emptyLayerMap, nil, nil)` — an **empty** layer map, not a partial one
- [x] on a miss, evaluate as today, then write the artifact
- [x] **snapshot the key inputs before reading the config files**, and re-verify nothing in the watch
      set moved between evaluation and write; a file edited mid-evaluation must read as stale rather
      than be stamped fresh. `cmd/source.go:525-533` already demonstrates the required ordering
- [x] validation runs on the _evaluated_ config and the post-validation result is what is stored;
      this is sound only because `ldflags.Version` is in the key (Task 2)
- [x] add `DATAMITSU_CONFIG_CACHE` (default on) per the Environment Variable Usage Policy: `envVar`
      in `internal/env/e.go`, getter in `internal/env/env.go`, test in `internal/env/env_test.go`,
      typed field on `runtimeconfig.Effective` wired in `Compute()`, consumed via
      `runtimeconfig.Get()`
- [x] **add `DATAMITSU_CONFIG_CACHE` to `environExcluded`** in `internal/env/environ.go`, with a
      comment: it decides whether a cache is consulted, never what datamitsu produces, so folding it
      into the source-mode staleness key would rebake every farm when someone toggled it
- [x] add `trace` spans: `configcache.key`, `configcache.read`, `configcache.write`, and a
      hit/miss counter
- [x] write the load-bearing differential test: for a fixture chain, the config from a cold load and
      the config from a hit must be **equal as a whole graph** (compare serialized forms, not counts)
- [x] write a test asserting `loadConfigForSetup` never serves from cache and always returns a VM
- [x] write a test asserting `config chain-hash` still produces the same hashes (it evaluates setup
      content, so it must miss)
- [x] write a test asserting a changed config file produces a miss
- [x] write a test asserting a changed environment variable produces a miss (use `CI`)
- [x] write a test asserting `DATAMITSU_CONFIG_CACHE=0` always evaluates
- [x] fill in `BenchmarkConfigCacheHit` from Task 1 and record hit vs miss here
- [x] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 6

#### How the wiring works, and what changed in scope (2026-08-27)

`cmd/config_cache.go` holds the per-load handle; `loadConfigImpl` consults it after the chain is
resolved and writes to it after validation.

**Measured, same machine as the Task 1 baseline** (i9-14900K, 1.95 MB before-config,
`-benchtime 30x -count 3`, best of 3):

| Benchmark             | ns/op          | Note                                   |
| --------------------- | -------------- | -------------------------------------- |
| `ConfigCacheEvaluate` | **31,588,546** | cache off (`DATAMITSU_CONFIG_CACHE=0`) |
| `ConfigCacheHit`      | **2,042,650**  | same chain, served from disk           |

**15.5× on the evaluation, 29.5 ms removed from every load that hits.** The hit is ~0.4 ms above the
Task 1 estimate because a hit also collects facts and hashes the chain — both are what make the key
trustworthy, and both were counted as "the key" at 0.33 ms rather than measured end to end.

➕ **The key is computed after the chain is resolved, not before.** The declared before-configs are
part of the chain and are only known once the auto config has been read for them. The pre-read
snapshot the task asks for therefore covers exactly the auto config — the only file read that early
— and the post-evaluation re-hash covers everything else. Hashing the flag-given paths twice was
tried and reverted: it re-read 1.95 MB to learn nothing and cost 0.5 ms of the hit path.

➕ **`Inputs.SkipRemoteConfig`** was added to the Task 2 key. `--skip-remote-config` drops every
remote layer from the merged result and is not derivable from the chain bytes, so a key without it
would serve a full config to a run that asked for none. The declared `getRemoteConfigs()` entries
stay in `Inputs` but are left empty by the loader: they cannot be known before evaluation, and they
are a pure function of inputs that are already in the key.

➕ **`skipLockfileValidation` loads neither read nor write.** That path validates less than every
other one, so an artifact it wrote would let a later strict load skip the error it exists to raise —
the only direction in which this cache could turn a refusal into a silent success.

➕ **The artifact carries the validation warnings and the resolved remote URLs**
(`configcache.Entry`), and a hit replays both. Without that, a command's stderr would depend on
whether a cache happened to be warm — and `devtools verify-all` would stop listing the remote
configs the chain resolved. This is why `Store.Save`/`Load` take an `Entry` rather than a bare
`*config.Config`.

➕ **`env.EnvironAll()`** is the environment fingerprint: the whole environment, sorted, minus
`environExcluded`. The exclusions carry over deliberately — a key that folded in `DATAMITSU_TRACE`
would make the first traced run miss because it was traced, so the instrument would change the
measurement it was reaching for.

➕ `sourcefarm.gitHeadPath` is now exported as `GitHeadPath`: the loader needs the same
worktree-aware HEAD the farm watches, and duplicating that logic is how the two drift.

⚠️ `BenchmarkConfigCacheEvaluate` now sets `DATAMITSU_CONFIG_CACHE=0` explicitly. Left as it was, it
would have measured a cache hit and reported it as the cost of evaluation.

### Task 6: Verify acceptance criteria

- [x] `datamitsu exec <installed binary app> -- --version` wall-min is ≤ 45 ms in a repository whose
      before-config is ~2 MB (baseline 78 ms) — measure with n≥20, report the min — **23.8 ms**
- [x] a cache hit's `loadConfig` span is ≤ 3 ms — **2.44 ms min, 2.96 ms max over n=12**
- [x] `go test ./...` passes with `-race` (under `umask 022`)
- [x] `go test ./test/cli/ -count=2` passes with **zero** regenerated goldens
- [x] `pnpm dm check` passes
- [x] coverage meets the project standard via `pnpm test:coverage:all` — **82.3%** combined
- [x] a second identical invocation writes no new artifact (assert the file's mtime is unchanged) —
      `TestConfigCacheSecondInvocationWritesNothing`
- [x] `datamitsu config show` output is byte-identical between a miss and a hit —
      `TestConfigShowIdenticalAcrossCacheMissAndHit` (stdout **and** stderr)
- [x] the cache tree's size after 20 invocations across 3 branches is bounded (prune works) —
      `TestConfigCacheTreeBoundedAcrossBranches`: 21 loads, 3 artifacts

#### Acceptance measurements (2026-08-27)

Same machine and same repository as the Task 1 baseline (i9-14900K, 14,370-file TypeScript monorepo,
`--before-config` pointed at the 1.95 MB base config), machine **not** idle:

| Measurement                              | Baseline (Task 1) | Now                           |
| ---------------------------------------- | ----------------- | ----------------------------- |
| `exec task -- --version` wall min (n=20) | 60.7 ms           | **23.8 ms**                   |
| same, `DATAMITSU_CONFIG_CACHE=0`         | —                 | 68.2 ms                       |
| `loadConfig` span on a hit (n=12)        | 38–42 ms          | **2.44 ms min / 2.96 ms max** |
| — `configcache.read`                     | —                 | 2.1–2.5 ms                    |
| — `configcache.key`                      | —                 | 0.36–0.65 ms                  |

The wall min beats both the written target (≤ 45 ms) and the revised one the Task 1 ⚠️ note derived
from this machine (≤ 28 ms).

➕ **`HashChainFile` now streams.** It first measured 1.4–2.2 ms — the key alone was two thirds of
the ≤ 3 ms budget — because `os.ReadFile` faults in a fresh ~2 MB buffer per chain file. Hashing
through `hashutil.XXH3Reader` over an open file (io.Copy's 32 KB window) cut the whole
`configcache.key` span to 0.36–0.65 ms, a 3–4× improvement for a five-line change. The artifact read
is left as `os.ReadFile`: msgpack decoding needs the whole buffer anyway.

➕ **Four blackbox tests, `test/cli/config_cache_test.go`.** Three are the acceptance items above;
the fourth (`TestConfigCacheDisabledStoresNothing`) pins that `DATAMITSU_CONFIG_CACHE=0` never
creates the tree, so a user who turns the cache off has no cache to go stale.

⚠️ The branch-bounding test auto-discovers its config rather than passing `--config`. An explicit
chain is machine-level, has no git root and therefore no HEAD in its key, so all three branches
would legitimately share one entry — the property under test only exists for a repository chain.

### Task 7: [Final] Update documentation

- [ ] record the final measurement table in this plan file, replacing the estimates
- [ ] add `DATAMITSU_CONFIG_CACHE` to the environment-variable table in
      `website/docs/reference/cli-commands.md`
- [ ] extend `website/docs/guides/architecture/startup.md` with the cache: what is in the key, why
      the environment is hashed whole rather than the `DATAMITSU_*` subset, and the non-determinism
      refusal
- [ ] run `task gen:llms-docs` and commit `internal/llmsdocs/embed` if any website page changed —
      the `llms-docs-drift` CI job re-harvests on every PR and fails on any diff

## Technical Details

**Measurement discipline.** Wall-clock **minimum** over n≥20 is the estimator; medians are
contaminated by contention on a machine that is not idle. Report the min and state n.

**Why an empty layer map rather than a partial one.** A hit cannot reconstruct `ConfigSetup.Content`
— it is a live goja function. Returning `Setup` entries with `Content == nil` is correct for `lint`
and `exec` and wrong for `setup`. Gating on the caller (Task 5) rather than on the artifact means
the wrong shape is never produced, instead of being produced and hopefully not used.

**Why the whole environment.** `env.Environ()` exists for the source-mode farm key and is
deliberately narrow. Config JS sees everything through `facts().env`. Reusing the narrow one here
would be a silent correctness bug on the first repository whose config branches on `CI`.

**Why validation is cached too.** The eleven validators at `cmd/config_loader.go:270-326` are a pure
function of the merged config, so caching the post-validation result is sound — provided a binary
that validates differently produces a different key. That is exactly what `ldflags.Version` in the
key buys, and it is why that input is not optional.

**Interaction with the execution cache.** `internal/cache/cache.go:479` builds its invalidation key
by `json.Marshal`ing the whole config. Do not switch it to msgpack as a convenience: that changes
every user's key once and resets their per-file cache. If it happens anyway, say so in the changelog.

## Post-Completion

**Manual verification:**

- Re-run every benchmark on an idle machine (load average < 2) and update the recorded numbers.
- Verify on a machine-level `--config` chain (no git root), which uses the other namespace.
- Verify a branch switch that changes the config produces a miss and then a hit.
- Verify inside a `datamitsu source` shell that the farm is not reported stale by the new env var.

**Follow-up work this plan deliberately excludes:**

- Caching the evaluated setup layer map. It needs the content hashes of all 57 setup target files
  and the project-detection result in its key, and only `setup`/`init`/`chain-hash` would use it.
- In-process `*goja.Program` reuse, which would remove the double compile of the auto config on a
  miss. `cmd/config_loader.go` already separates compile from run, which is the seam it needs.
- The remaining items in `docs/plans/2026-08-26-planner-and-startup-cost.md` phases 3 and 4 —
  notably the verdict-cache input hashing, which is 3.0 s of CPU on a warm `lint` and a larger win
  than this plan on the commands run daily.
