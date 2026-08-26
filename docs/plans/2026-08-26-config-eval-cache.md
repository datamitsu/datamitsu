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

- [ ] in `internal/engine`, install instrumented shims over `Date.now`, the `Date` constructor and
      `Math.random` that record an observation flag on the Engine
- [ ] expose the flag (e.g. `Engine.ObservedNonDeterminism() bool`)
- [ ] the shims must be transparent: same values, same types, same behaviour — they only record
- [ ] `loadConfigImpl` must refuse to _write_ an artifact when any engine in the chain observed
      non-determinism, and log the refusal at debug level naming the source
- [ ] write a test with a config calling `Date.now()` asserting the flag is set and nothing is
      stored
- [ ] write a test with a config calling `Math.random()` asserting the same
- [ ] write a test with a config doing neither asserting the flag is clear and an artifact is stored
- [ ] write a test asserting the shims return usable values (a config that computes with `Date.now()`
      still evaluates correctly)
- [ ] run `go test ./...` and `go test ./test/cli/ -count=2` — must pass before Task 4

### Task 4: The artifact store

- [ ] store at `{env.GetCachePath()}/config-eval/{namespace}/{key}.msgpack`. `GetCachePath()` is
      the cache tree, not the store: it must be safe to delete
- [ ] `{namespace}` mirrors the source-farm layout — `projects/{XXH3-128(gitRoot)}` for a repository
      chain, and the explicit-config namespace (`internal/sourcefarm/plan.go:413`) for a
      machine-level `--config` chain. **Do not fall back to cwd**, or two directories share an entry
- [ ] the artifact holds the merged `config.Config` with every `ConfigSetup.Content` nil, encoded
      with msgpack
- [ ] write with `os.CreateTemp` in the same directory + `os.Rename`, never in place. Entries are
      immutable per key, so reads need no lock and a concurrent double-write of one key is harmless
- [ ] a decode failure, a truncated file or an unknown `formatVersion` is a **miss**, never an
      error: a corrupt cache must degrade to evaluating, and the bad entry should be removed
- [ ] prune entries unread for N days at write time — do not repeat the store's "no GC, 7 GB" mistake
- [ ] `datamitsu cache clear` must clear this tree; check `internal/cache/cache.go:547` `ClearAll`
- [ ] write a round-trip test over a real merged config (extend the existing one)
- [ ] write a test asserting a corrupt artifact is a miss and is removed
- [ ] write a test asserting an unknown `formatVersion` is a miss
- [ ] write a test asserting the rename is atomic under concurrent writers (`-race`, N goroutines)
- [ ] write a test asserting `cache clear` removes it
- [ ] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 5

### Task 5: Wire it into the loader

- [ ] add `loadConfigOptions.requireVM bool` and set it in `loadConfigForSetup`, which is the only
      path whose caller uses the returned `*goja.Runtime` (`cmd/setup.go:80`)
- [ ] serve from cache only when `!opts.requireVM && !opts.evaluateSetupContent`; otherwise evaluate
- [ ] on a hit return `(cfg, &emptyLayerMap, nil, nil)` — an **empty** layer map, not a partial one
- [ ] on a miss, evaluate as today, then write the artifact
- [ ] **snapshot the key inputs before reading the config files**, and re-verify nothing in the watch
      set moved between evaluation and write; a file edited mid-evaluation must read as stale rather
      than be stamped fresh. `cmd/source.go:525-533` already demonstrates the required ordering
- [ ] validation runs on the _evaluated_ config and the post-validation result is what is stored;
      this is sound only because `ldflags.Version` is in the key (Task 2)
- [ ] add `DATAMITSU_CONFIG_CACHE` (default on) per the Environment Variable Usage Policy: `envVar`
      in `internal/env/e.go`, getter in `internal/env/env.go`, test in `internal/env/env_test.go`,
      typed field on `runtimeconfig.Effective` wired in `Compute()`, consumed via
      `runtimeconfig.Get()`
- [ ] **add `DATAMITSU_CONFIG_CACHE` to `environExcluded`** in `internal/env/environ.go`, with a
      comment: it decides whether a cache is consulted, never what datamitsu produces, so folding it
      into the source-mode staleness key would rebake every farm when someone toggled it
- [ ] add `trace` spans: `configcache.key`, `configcache.read`, `configcache.write`, and a
      hit/miss counter
- [ ] write the load-bearing differential test: for a fixture chain, the config from a cold load and
      the config from a hit must be **equal as a whole graph** (compare serialized forms, not counts)
- [ ] write a test asserting `loadConfigForSetup` never serves from cache and always returns a VM
- [ ] write a test asserting `config chain-hash` still produces the same hashes (it evaluates setup
      content, so it must miss)
- [ ] write a test asserting a changed config file produces a miss
- [ ] write a test asserting a changed environment variable produces a miss (use `CI`)
- [ ] write a test asserting `DATAMITSU_CONFIG_CACHE=0` always evaluates
- [ ] fill in `BenchmarkConfigCacheHit` from Task 1 and record hit vs miss here
- [ ] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 6

### Task 6: Verify acceptance criteria

- [ ] `datamitsu exec <installed binary app> -- --version` wall-min is ≤ 45 ms in a repository whose
      before-config is ~2 MB (baseline 78 ms) — measure with n≥20, report the min
- [ ] a cache hit's `loadConfig` span is ≤ 3 ms
- [ ] `go test ./...` passes with `-race` (under `umask 022`)
- [ ] `go test ./test/cli/ -count=2` passes with **zero** regenerated goldens
- [ ] `pnpm dm check` passes
- [ ] coverage meets the project standard via `pnpm test:coverage:all`
- [ ] a second identical invocation writes no new artifact (assert the file's mtime is unchanged)
- [ ] `datamitsu config show` output is byte-identical between a miss and a hit
- [ ] the cache tree's size after 20 invocations across 3 branches is bounded (prune works)

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
