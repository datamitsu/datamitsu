# CLI Blackbox Test Suite & Coverage Uplift

## Overview

Build a comprehensive **blackbox/CLI test suite** that freezes datamitsu's current
command-line contract (commands, flags, stdout/stderr, exit codes, JSON shapes)
**before a large core rewrite** that will add JSON-L logging and a watch / stdin–stdout
"programmable" mode (for VS Code / Obsidian / arbitrary wrappers). The CLI surface must
not change "ни на йоту" during that rewrite — these tests are the safety net that proves it.

Then, as a second wave, raise overall coverage from the current ~63–65% toward 80%+.

**Key benefits**

- Lock the exact externally-observable behavior of every Cobra command so the internals
  can be gutted and rebuilt with confidence.
- Subprocess-based golden tests are decoupled from internal structure → they survive the
  rewrite unchanged (no coupling to `rootCmd` globals, `ui.Display`, `os.Exit`, etc.).
- Coverage uplift de-risks the rewrite and the existing pipeline.

**How it integrates**

- A subprocess harness builds a `-cover`-instrumented binary once per run and executes it
  in isolated temp git repos with isolated cache/store + offline mode. Coverage from those
  runs is collected via `GOCOVERDIR` (Go 1.26) and merged with unit-test coverage.
- A separate, **gated** OCI-seeded tier exercises the real install/exec/lint pipelines using
  the user's released, digest-pinned config (`datamitsu.config.oci-ghcr.js`), which is far
  more stable than ad-hoc internet downloads and benefits from global-cache dedup.

**Out of scope (YAGNI):** the JSON-L / watch-mode / programmable-API feature itself is NOT
implemented here. This plan delivers only the test safety net that must exist first. No
production CLI behavior is changed by Phases 0–2 (purely additive test code).

## Context (from discovery)

**CLI surface** (12 top-level commands, ~35 leaf commands):

- Root + persistent flags: [cmd/root.go](../../cmd/root.go) — `--verbose/-v`, `--binary-command`,
  `--before-config`, `--no-auto-config`, `--config`, `--no-oci`; `SilenceUsage/SilenceErrors`;
  `cobra.OnInitialize` → `runtimeconfig.Init()` (+ `os.Exit(1)` on failure); `Execute()` sets up
  the global `ui.Display` and `os.Exit(1)` on error.
- `version` ([cmd/version.go](../../cmd/version.go)); `init` ([cmd/init.go](../../cmd/init.go));
  `exec` ([cmd/exec.go](../../cmd/exec.go), calls `os.Exit(1)` directly);
  `config show|types|runtime|chain-hash` ([cmd/config.go](../../cmd/config.go));
  `install` ([cmd/install.go](../../cmd/install.go)); `check`/`fix`/`lint`
  ([cmd/check.go](../../cmd/check.go), [cmd/fix.go](../../cmd/fix.go), [cmd/lint.go](../../cmd/lint.go));
  `setup` ([cmd/setup.go](../../cmd/setup.go));
  `cache clear|path|path project` ([cmd/cache.go](../../cmd/cache.go));
  `store path|clear|seed|status|import` ([cmd/store.go](../../cmd/store.go));
  `devtools …` (10 subcommands: pull-github/pull-node/pull-uv/pull-runtimes, apps, bundles,
  dockerfile, split-config, verify-all, pack-inline-archive — [cmd/devtools\*.go](../../cmd/)).
- JSON-emitting: `config show|runtime|chain-hash`, `store status --json`,
  `devtools verify-all --json`, `check|fix|lint --explain=json`.

**Existing tests:** 148 `_test.go`, **stdlib only** (no testify), table-driven, heavy
`t.TempDir`/`t.Setenv`/`httptest`. **No end-to-end test runs the built binary or
`rootCmd.Execute()` today** — this is the primary gap. `cmd/` has 34 unit-level test files
(mostly `devtools`), but no CLI-contract coverage.

**Technical enablers confirmed:**

- **Go 1.26.4** → `go build -cover` + `GOCOVERDIR` collects coverage from subprocess runs
  (so blackbox tests count toward the coverage number). Go's coverage runtime registers an
  exit hook, so even `os.Exit(1)` paths flush counters.
- Hermeticity: `--no-auto-config` + `--config <path>`; `DATAMITSU_CACHE_DIR=<tmp>` (base for
  cache **and** store: `{base}/cache`, `{base}/store` — [internal/env/env.go](../../internal/env/env.go));
  `DATAMITSU_OFFLINE=1` (`httpx.GuardOffline`); `--no-oci`/`DATAMITSU_NO_OCI`; `git init` in temp dir.
- Embedded [internal/config/config.js](../../internal/config/config.js) is checked in → `go build`
  works without a prior `pnpm build`.
- Minimal config shape (see [datamitsu.config.ts](../../datamitsu.config.ts)): three globals
  `getBeforeConfigs()`, `getConfig(config)`, `getMinVersion()`.
- Tool disabling: `.datamitsuignore` (`**/*: a, b` / `**/*: *` / `!` inversion —
  [internal/datamitsuignore/matcher.go](../../internal/datamitsuignore/matcher.go)),
  `disabled?: boolean` in tool config, or the `--tools a,b` flag.
- OCI config single-source-of-truth: vendor the released
  `https://github.com/shibanet0/datamitsu-config/releases/download/v0.1.6/datamitsu.config.oci-ghcr.js`
  into testdata (carries `oci.ref`+`oci.digest`, pinning the whole bundle).

**Decisions (from planning Q&A):**

1. Harness = **subprocess + `go build -cover`** (true blackbox, coverage via `GOCOVERDIR`).
2. Real-pipeline tier = **OCI-seeded**, base config inherited via `--before-config`, per-group
   temp git repo, most tools disabled/overridden, global-cache dedup. Base config kept in one place.
3. Scope ordering = **golden contract for the whole CLI first**, push the % later.

## Development Approach

- **Testing approach: characterization / golden (behavior-first).** We lock _existing_
  behavior, so this is not TDD — capture current output as golden, assert it stays stable.
- **Conventions:** stdlib `testing` only (no testify — match repo style), table-driven where it
  fits, `t.TempDir`/clean env. New reusable harness lives in `internal/clitest`.
- **Zero production changes in Phases 0–2** (additive test code only). Phase 3 prefers unit tests
  on `internal/*` and more subprocess error-path tests; it does **not** refactor `cmd/`.
- Complete each task fully before the next. Small, focused changes. Tests must pass before moving on.
- **CRITICAL: every task delivers passing tests** (success + error/edge cases) and all tests are
  green before starting the next task.
- **CRITICAL: keep this plan file in sync** — check items off, add ➕ discovered tasks, flag ⚠️ blockers.

## Testing Strategy

- **Blackbox (subprocess):** primary. Build instrumented binary once (`TestMain`), run it with
  args+env in isolated temp git repos; assert stdout/stderr/exit-code; golden files with
  normalization (strip ANSI, mask temp paths/version/durations). Offline by default.
- **Unit:** for Phase 3 coverage push on under-covered `internal/*` packages (and harness itself).
- **Gated OCI e2e:** behind build tag `//go:build e2e_oci` + env guard; never in default CI;
  exercises real seed/install/exec/init/check/fix/lint via the digest-pinned config.
- **Determinism:** `NO_COLOR=1`, non-TTY (pipes), cleaned env (strip inherited `DATAMITSU_*`),
  golden `-update` flag. Golden suite must be byte-stable across two consecutive runs.

## Progress Tracking

- Mark completed items `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix; document blockers with ⚠️ prefix.
- Update the plan if scope changes during implementation.

## What Goes Where

- **Implementation Steps** (`[ ]`): harness, tests, golden files, CI wiring, docs — all inside this repo.
- **Post-Completion** (no checkboxes): bumping the vendored OCI config on new releases, running the
  gated OCI tier locally/nightly, optional CI runner for the e2e tier.

## Implementation Steps

### Task 0.0: Baseline & guard rails

- [x] confirm `go build` works in the clean tree (embedded `internal/config/config.js` present) — `go build ./...` exits 0 on go1.26.4
- [x] record current coverage baseline: `go test ./cmd/... ./internal/... -coverprofile=baseline.out` then `go tool cover -func=baseline.out | tail -1` — write the number into this plan — **baseline total: 67.8% of statements** (2026-06-23)
- [x] list the 10 lowest-covered packages (`go tool cover -func=baseline.out | sort -k3 -n | head`) for Phase 3 targeting:
  1. `internal/cache` — 47.2%
  2. `cmd` — 51.7%
  3. `internal/detector` — 55.6%
  4. `internal/utils` — 57.4%
  5. `internal/runtimemanager` — 60.0%
  6. `internal/ui` — 60.8%
  7. `internal/term` — 66.7%
  8. `internal/binmanager` — 67.3%
  9. `internal/env` — 68.2%
  10. `internal/tooling` — 69.0%
      (`internal/ldflags` has no statements; excluded.)
- [x] write a short test (`internal/clitest/doc_test.go` placeholder) asserting the toolchain is Go ≥ 1.20 (so `GOCOVERDIR` is available)
- [x] run tests — must pass before next task — `go test ./internal/clitest/...` green

### Task 0.1: Subprocess harness — build-once instrumented binary

- [x] create `internal/clitest/binary.go`: `BuildOnce(t)` resolves module root (walk up to `go.mod`), runs `go build -cover -covermode=atomic -o <tmp>/datamitsu .` exactly once (guarded by `sync.Once`), returns the path — also `EnsureBuilt()` (TestMain-friendly, no `*testing.T`)
- [x] create a shared `GOCOVERDIR` (per `TestMain`) that all runs write into; expose `CoverDir()` — lazily-created temp dir; merge target pinnable via `DATAMITSU_TEST_COVER_OUT` (read in TestMain)
- [x] add `test/cli/main_test.go` with `TestMain` that calls `EnsureBuilt`, runs tests, then converts covdata → text (`WriteCoverProfile` → `go tool covdata textfmt`) into a known path (`DATAMITSU_TEST_COVER_OUT`) for CI merge
- [x] write tests for the harness: build succeeds (`binary_test.go`); the binary prints something for `version` (`test/cli/smoke_test.go`) — verified coverage profile written end-to-end
- [x] run tests — must pass before next task — `go test ./internal/clitest/... ./test/cli/...` green

### Task 0.2: Command runner + environment isolation

- [x] create `internal/clitest/run.go`: `Run(t, opts, args...) Result{Stdout,Stderr,ExitCode,Err}` via `os/exec`, capturing streams separately, with `opts` for env/workdir/stdin/timeout (+ `CacheDir`; default isolated `t.TempDir`; `DefaultTimeout`=60s, context-bounded with a fail-on-timeout)
- [x] build a clean base env (`BaseEnv(cacheDir)`): inherits `PATH`/`HOME` etc. from `os.Environ()`, sets `GOCOVERDIR`, `NO_COLOR=1`, `DATAMITSU_CACHE_DIR=<tmp>`, `DATAMITSU_OFFLINE=1`, `DATAMITSU_NO_OCI=1`; strips all inherited `DATAMITSU_*` + `CI`/`TERM` (and the keys it sets) so mode detection is deterministic and there are no duplicate-key ambiguities
- [x] helper `ExitCodeOf(err)` to extract exit codes portably (0/nil, real code via `*exec.ExitError`, -1 on start failure)
- [x] write tests: `version` → exit 0; unknown command → non-zero + message on stderr; stdout/stderr are separated; plus `BaseEnv` strip/set + no-duplicate-key + `ExitCodeOf` table — `go test ./internal/clitest/...` green, managed golangci-lint clean
- [x] run tests — must pass before next task — `go test ./internal/clitest/... ./test/cli/...` green

### Task 0.3: Temp project + config fixtures

- [x] create `internal/clitest/project.go`: `NewProject(t) *Project` → `t.TempDir()` (eval symlinks) + `git init` + minimal `git config` user; helpers `WriteFile`, `Chdir` (via `testing.TB.Chdir`)
- [x] `WriteMinimalConfig(p)` writes the no-op config (`getConfig` returns empty `apps/runtimes/setup/tools`, `getMinVersion`="0.0.0") to a non-auto-discovered name (`minimal.config.js`) for `--no-auto-config --config <path>`
- [x] `WriteOverlayConfig(p, beforeConfigPath, mutateJS)` writes auto-discoverable `datamitsu.config.js` whose `getBeforeConfigs()` returns `[{path: beforeConfigPath}]` and whose `getConfig` applies `mutateJS`; `WriteDatamitsuIgnore(p, lines)`
- [x] write tests: project resolves as git root; `config show` succeeds offline against the minimal config (exit 0, valid JSON, empty collections); plus `WriteFile`/overlay/ignore/`jsString`-escaping coverage
- [x] run tests — must pass before next task — `go test ./internal/clitest/... ./test/cli/...` green, managed golangci-lint clean

### Task 0.4: Output normalization + golden plumbing

- [x] create `internal/clitest/golden.go`: normalizers (strip ANSI; mask `t.TempDir` paths → `<TMP>`; mask `$HOME`/cache/store paths; mask the build's version → `<VERSION>`; mask durations/timings; optional line-sort for unordered output) — `Normalizer` with `MaskPath`/`SortLines`/`Apply`; built-in ANSI/timestamp/duration/version rules; longest-path-first masking
- [x] `AssertGolden(t, name, got)` comparing against `testdata/golden/<name>.txt`, with a package-level `-update` flag to (re)generate goldens — `GoldenDir` configurable; testable `compareGolden` core; line-diff on mismatch
- [x] write tests: normalizers are idempotent; `-update` writes the file; a mismatch fails with a readable diff — `golden_test.go` (idempotency, ANSI/path/version/timestamp/duration masking, sort, update→match, mismatch diff, missing-file hint)
- [x] run tests — must pass before next task — `go test ./internal/clitest/...` green, managed golangci-lint clean

### Task 1.1: Golden — root & global flags

- [x] `test/cli/root_test.go`: golden for `--help`, no-args help, `help <cmd>`; assert top-level command list is exactly the expected set (drift guard) — `root_help.txt`/`help_version.txt` goldens; no-args proven byte-identical to `--help`; `parseAvailableCommands` set-compares against `expectedTopLevelCommands`
- [x] flag parsing: `--verbose`, repeated `--config`, `--no-auto-config`, `--no-oci`, `--before-config`, `--binary-command` accepted; unknown global flag → non-zero + message — `TestRootGlobalFlagsAccepted` (9 combos incl. all-combined) + `TestRootBadFlags` unknown-global-flag case
- [x] confirm `SilenceUsage`/`SilenceErrors` behavior: a runtime error does NOT print `Usage:` (subprocess-level assertion) — `TestRootSilenceUsage` via `exec <unknown>` (offline runtime error, no usage block)
- [x] write error-path cases (bad flag value types) — table-driven — `TestRootBadFlags`: missing arg, bad bool value, unknown command; all non-zero + stderr message + no usage
- [x] run tests — must pass before next task — `go test ./test/cli/` green (byte-stable across runs), managed golangci-lint clean

### Task 1.2: Golden — `version`

- [x] golden the normalized `version` output (`<PackageName> version <VERSION>`); exit 0 — `version.txt` golden (`datamitsu version <VERSION>`), exit 0, empty stderr asserted (`TestVersionGolden`)
- [x] assert it ignores extra args / works with `--verbose` — `TestVersionIgnoresExtraArgs`: trailing args (`extra`, `foo bar`) + `--verbose`/`-v` all produce byte-identical normalized output and exit 0
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/...` green (byte-stable across two runs), managed golangci-lint clean

### Task 1.3: Golden — `config` group

- [x] `config --help`; `config show` → valid JSON golden + key-shape assertions (against minimal config) — `config_help.txt` golden + `expectedConfigSubcommands` drift guard; `config_show.txt` (`{}`) + `json.Unmarshal` empty-object assertion
- [x] `config types` → non-empty `.d.ts`, stable leading lines — `TestConfigTypes`: exit 0, empty stderr, ≥256 bytes, `declare global {` prefix (no full-file golden — the type surface churns)
- [x] `config runtime` → JSON golden + env-override behavior: `DATAMITSU_INSTALL_TIMEOUT=1200` ⇒ `installTimeoutSeconds==1200`; `DATAMITSU_MIN_RELEASE_AGE` ⇒ `minimumReleaseAgeMinutes` — key-shape assertions on stable keys (not a full golden: `maxParallelWorkers`/`libc` are machine/platform-dependent — assert presence only, per runtimeconfig "required keys, not field count" policy) + `TestConfigRuntimeEnvOverride` table (1200 / 99)
- [x] `config chain-hash`: bare single-file output vs multi-file table; unknown file → non-zero error — `config_chain_hash_table.txt` golden (deterministic XXH3 over fixed on-disk content); single-file bare hash asserted == its table row; unknown file → exit 1 naming the file
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/...` green, byte-stable across two runs, managed golangci-lint clean

### Task 1.4: Golden — `cache` group

- [x] `cache path` → printed path under `DATAMITSU_CACHE_DIR` (normalized); `cache --help` — `cache_path.txt` golden (`<CACHE>/cache`, base masked) + exact-equality guard; `cache_help.txt` static golden + `expectedCacheSubcommands` drift guard (`clear`, `path`)
- [x] `cache path project` inside a git project → project cache path; outside git → **characterized: still succeeds** — inside-git asserts the exact `{base}/cache/projects/<xxh3(gitRoot)>/cache` path; outside-git locks the CWD-fallback (no error — `resolveProjectRoot` only errors on a present-but-unusable `.git`, covered below)
- [x] `cache clear --dry-run` and `cache clear --all --dry-run` → golden messages, nothing deleted — `cache_clear_dry_run.txt`/`cache_clear_all_dry_run.txt` (cache base + project hash masked); planted sentinel under the project cache survives both runs
- [x] write error/edge cases (project resolution failure) — `TestCacheProjectResolutionFailure`: a bogus `.git` file → exit 1 + "failed to determine git root" on stderr, no usage block (SilenceUsage), for both `cache path project` and `cache clear --dry-run`
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/...` green, byte-stable across two runs, managed golangci-lint clean

### Task 1.5: Golden — `store` group (offline contract)

- [x] `store path` → `{base}/store` (normalized); `store --help` — `store_path.txt` golden (base masked `<CACHE>`) + exact-equality guard; `store_help.txt` static golden + `expectedStoreSubcommands` drift guard (`clear`, `import`, `path`, `seed`, `status`)
- [x] `store status` (human) and `store status --json` with no `oci` configured → **characterized as error**: BundleStatus refuses to invent a bundle, both forms exit 1 naming "no oci bundle declared", no usage block (`TestStoreStatusNoOCI`); a populated bundle needs the network → gated OCI e2e tier
- [x] `store clear` safety: `TestStoreClear` freezes the masked "Cleared store" line on the isolated temp store + proves a planted sentinel is removed; `TestStoreClearRefusesDangerousPath` sets `HOME` = store path → exit 1 "refusing to clear dangerous path", store dir untouched
- [x] `store seed` arg validation (bare tag without `--resolve-tag`; unpinned ref; no-arg-no-oci → all exit 1, no usage — `TestStoreSeedArgValidation`) and `store import` (`ExactArgs(1)` no-arg; missing-digest-no-oci; missing layout dir → all exit 1 — `TestStoreImportArgValidation`), all offline
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/...` green, byte-stable across two runs, managed golangci-lint clean (0 issues)

### Task 1.6: Golden — `init` (offline, minimal config)

- [x] `init --help`; `init --dry-run` with minimal config → golden plan output (banner/sections normalized) — `init_help.txt` static golden + `init_dry_run.txt` (banner/init-frame/footer; version+duration masked). ➕ Added a built-in normalizer rule (`ruleRE` in `golden.go`) collapsing box-drawing rule fills → `<RULE>`: their length tracks the masked-away duration/version width, so without it the footer was flaky run-to-run.
- [x] `init` with empty config + `--no-oci` + offline → no-op success (exit 0); assert no unexpected writes outside the temp project/cache — `TestInitNoopSuccess`: `init_noop.txt` golden, exit 0, asserts the project `.datamitsu/` is created (its one expected write) and a temp `HOME` stays empty (nothing leaks outside project/cache). `--no-oci`/offline come from `BaseEnv` (`DATAMITSU_NO_OCI=1`, `DATAMITSU_OFFLINE=1`).
- [x] flag combos: `--skip-download`, `--all`, `--fail-on-download-error` parse and behave offline — `TestInitFlagCombosOffline`: each yields byte-identical normalized output to a baseline `init` (all no-op "ready" against the empty config), exit 0, each in its own fresh project.
- [x] error paths: missing config + `--no-auto-config`; not a git root — `TestInitErrorPaths`: a `--config` pointing at a missing file → exit 1 "failed to load config", no usage; outside any git repo → exit 1 "failed to get git root", no usage. **Characterized:** omitting `--config` under `--no-auto-config` is NOT an error (init falls back to the embedded default config), so the genuine config-error path is a missing `--config` file instead.
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/...` green, byte-stable across two runs, managed golangci-lint clean (0 issues)

### Task 1.7: Golden — `exec` (offline contract)

- [x] `exec` with no app name + minimal config → tool listing golden (empty/grouped) — `exec_list_empty.txt` ("Available tools:" header, no groups) + `exec_list_grouped.txt` (two shell apps under `[shell]`, sorted, with the command+description detail column) via a two-shell-app config (shell apps download nothing → fully offline)
- [x] `exec <unknown>` → non-zero + clear error (offline) — `TestExecUnknownApp`: `exec nonesuch` → exit 1, stderr "not found in registry" naming the app, no usage block (SilenceUsage)
- [x] argument passthrough parsing (`exec foo -- --flag`) reaches the resolver without mangling — `TestExecArgPassthrough`: `exec hello-shell -- --flag value` runs echo with exactly `--flag value` (stdout byte-equal); ➕ `TestExecFlagsBeforeSeparatorRejected` characterizes the converse (no `--` → Cobra rejects the unknown flag)
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/...` green, byte-stable across two runs, managed golangci-lint clean (0 issues)

### Task 1.8: Golden — `install` (offline contract)

- [x] `install` with no targets → non-zero error; `install --help` — `TestInstallNoTargets` (exit 1, no usage block) **characterized:** the real message is `specify at least one app name or --runtime <name>`, not the plan-era "nothing to install"; `install_help.txt` static golden (normalized == raw — no version/path tokens) via `TestInstallHelpGolden`
- [x] arg + flag validation: `install app`, `--runtime node`, `--no-verify` parse; offline download attempt → graceful offline error — `TestInstallOfflineDownloadError` declares a binary app for every os/arch/libc (always-found candidate) → exit 1, stderr names `offline mode` + `DATAMITSU_OFFLINE`, no usage; `TestInstallNoVerifyFlagParses` proves `--no-verify` routes the same path; `TestInstallRuntimeFlagParses` characterizes `--runtime node` against an undeclared runtime → no-op exit 0; `TestInstallUnknownApp` → exit 1 "not found in registry"
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/...` green, byte-stable across two runs, managed golangci-lint clean (0 issues)

### Task 1.9: Golden — `check` / `fix` / `lint` (offline via `--explain`)

- [x] for each of the three: `--help`, and `--explain=summary|detailed|json` against a small synthetic tool config → golden plan (json shape asserted; no execution, so fully offline+deterministic) — `test/cli/check_fix_lint_test.go`: `{check,fix,lint}_help` static goldens (`TestCheckFixLintHelpGolden`, normalized==raw + `--explain` doc present) and `{check,fix,lint}_explain_{summary,detailed,json}` goldens (`TestExplainPlanGolden`). Synthetic two-tool config (alpha=fix+lint, beta=lint-only; both repository-scope, no globs → deterministic, no per-file paths) via undeclared apps (BinaryAvailable treats unknown as available → no skips/downloads). fix/lint JSON parsed into `planShape` (operation/rootPath==`<TMP>`/groups/skipped-not-null/task completeness); check emits fix-plan+lint-plan (two concatenated objects) → frozen as a golden + substring-asserted, not parsed as one doc.
- [x] flag matrix: `--file-scoped`, `--tools a,b`, `--fail-on-skip`, positional file args, and combinations — `TestExplainToolsSelection` (parses JSON, `--tools alpha` plans only alpha, drops beta) + `TestExplainFlagMatrix` (`--file-scoped` empty staged set, `--fail-on-skip` no-op in explain, positional `README.md`, and a `check --tools alpha --file-scoped --fail-on-skip --explain=detailed` combo → all exit 0, stdout-only)
- [x] error paths: unknown `--tools` value; `--explain` with bad mode — `TestExplainErrorPaths` (table over all three commands): unknown `--tools nonesuch` → exit 1 "tools not found" naming it; `--explain=bogus` → exit 1 "invalid --explain value"; both no usage block (SilenceUsage)
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/...` green, byte-stable across two runs (`-count=2`), managed golangci-lint clean (0 issues)

### Task 1.10: Golden — `setup` (offline, `--dry-run`)

- [x] `setup --help`; `setup --dry-run` with minimal config → golden planned actions (created/replaced/linked/removed normalized) — `test/cli/setup_test.go`: `setup_help.txt` static golden (normalized==raw, no version/path tokens) + `setup_dry_run.txt` (banner/setup-frame "no project types detected"/dry-run notice/footer; version+duration+rule-fills masked)
- [x] flag combos: `--opt-in-tools` (generates `.datamitsuignore` plan), `--skip-fix`, `--tools`, `--no-verify-hash` — `setup_opt_in_tools.txt` golden ("+ .datamitsuignore (would disable 2 tools)" plan line; dry-run asserts the file is NOT written) + `setup_tools_scoped.txt` golden (`--tools alpha` against tools owning no setup config → "no config generated for alpha" notice) + `TestSetupFlagCombosOffline` (`--skip-fix`/`--no-verify-hash` byte-identical to baseline dry-run) + `TestSetupUnknownTools` (unknown `--tools` → exit 1 "tools not found" naming it, no usage)
- [x] error paths: chain-hash drift reported (when `--no-verify-hash` omitted) without writing — `TestSetupChainHashDrift`: before-config generates `drift.txt`, root config pins a wrong `expectChainHash`; a real (non-dry-run) `setup` aborts at the gate → exit 1, stderr "chain-hash verification failed" naming the file, no usage block, and `drift.txt` is never created; `--no-verify-hash` bypasses the gate (exit 0, no drift report). Uses auto-discovery so `getBeforeConfigs()` is honored.
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/...` green, byte-stable across two runs (`-count=2`), managed golangci-lint clean (0 issues)

### Task 1.11: Golden — `devtools` group (contract/smoke only)

- [x] `devtools --help` and `--help` for each subcommand (apps, bundles, dockerfile, split-config, pull-github, pull-node, pull-uv, pull-runtimes, verify-all, pack-inline-archive) — `test/cli/devtools_test.go`: `TestDevtoolsHelpGolden` table over all 11 help surfaces → `devtools_help.txt` + `devtools_<sub>_help.txt` goldens; help blocks are static (no temp paths/durations/real version tokens) so the goldens are **raw** stdout, deliberately un-normalized. ➕ Normalizing would be wrong here: `ldflags.Version` defaults to `"dev"` and pull-runtimes' help mentions `go.dev`, whose `dev` the version rule would mask → build-dependent golden. `TestDevtoolsCommandSetDrift` adds set-compare drift guards for the devtools/apps/bundles subcommand sets (`expectedDevtoolsSubcommands` etc.).
- [x] arg/flag validation without network: `ExactArgs`/`NoArgs` violations; `pull-runtimes` requires `--update`; `dockerfile`/`split-config` require `--output`; `pack-inline-archive` requires a dir — `TestDevtoolsArgValidation` table: `dockerfile`/`split-config` no `--output` → `required flag(s) "output" not set`; `pull-runtimes`/`pull-github`/`pull-node`/`pull-uv`/`pack-inline-archive` no arg → `accepts 1 arg(s), received 0`; `pull-runtimes <file>` without `--update` → `--update flag is required`; `pack-inline-archive <file>` → `is not a directory`. All exit 1, message on stderr, **no usage block** (SilenceUsage asserted on every case).
- [x] `devtools apps list` / `bundles list` against minimal config → golden (empty/grouped) — `TestDevtoolsAppsList`/`TestDevtoolsBundlesList`: empty against the minimal config (exit 0, empty stdout) + populated goldens (`devtools_apps_list.txt` from a two-shell-app config → sorted `name (type) [- desc] - not installed`; `devtools_bundles_list.txt` from a two-file-bundle config → sorted `name [(version)] - not installed`). Shell apps / file bundles download nothing → fully offline.
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/...` green, byte-stable across two runs (`-count=2`), managed golangci-lint clean (0 issues)

### Task 1.12: Contract completeness gate

- [x] add a test enumerating every leaf command and asserting each has ≥1 blackbox test (registry-driven guard against future commands slipping through untested) — `test/cli/completeness_test.go`: `TestContractCompletenessGate` walks the binary's `--help` tree (`discoverLeafCommands`) to enumerate the live leaf set and asserts it equals exactly `testedLeafCommands` ∪ `builtinLeafCommands` (untested-new / unregistered-builtin / stale-entry all fail). ➕ Discovery surfaced 5 leaves the golden suite had not yet exercised — added blackbox tests for each before wiring the gate: `config lockfile` (`TestConfigLockfile`: empty-list notice + unknown-app error), `devtools apps inspect|path` and `devtools bundles inspect|path` (`TestDevtoolsAppsInspectPath`/`TestDevtoolsBundlesInspectPath`: unknown→"not found in config", known-but-not-installed→"is not installed", all offline, no usage block via shared `assertOfflineError`). `cache path` is a runnable group (has the `project` child) so it is covered as a group, not required as a leaf.
- [x] run the whole golden suite twice → assert byte-stable (determinism) — `TestGoldenSuiteDeterministic` runs each high-risk dynamic-output invocation (version, cache path, config show/runtime, init/setup --dry-run, check/fix/lint --explain=json, exec list, devtools apps list) twice in independent isolated setups and asserts the normalized outputs are byte-identical; the exhaustive form (`go test ./test/cli/ -count=2`) is also green.
- [x] run tests — must pass before next task — `go test ./test/cli/ ./internal/clitest/... -count=2` green, managed golangci-lint clean (0 issues)

### Task 2.1: OCI e2e tier — vendored config + scaffolding (gated)

- [x] vendor `test/e2e/testdata/datamitsu.config.oci-ghcr.js` from the v0.1.6 release URL; add `test/e2e/source.go` with the single `OCIConfigSource` const + a comment documenting the "re-download to update" procedure (single source of truth) — vendored 1,079,517 bytes (sha256 `5d9bba7d…df486`, carries `oci.digest` `sha256:1f53…a326`); `source.go` is intentionally **untagged** so the package always has a compilable file and the default build reports "no test files" instead of failing
- [x] add build tag `//go:build e2e_oci` to the tier; `RequireOCIE2E(t)` skips unless enabled (tag + `DATAMITSU_TEST_OCI=1`) — `helpers_test.go`/`smoke_test.go` carry the tag (gate 1: absent → no test files in default build); `RequireOCIE2E` is gate 2 (tag present but `DATAMITSU_TEST_OCI!=1` → `t.Skip`)
- [x] persistent, configurable cache for dedup: `DATAMITSU_TEST_CACHE` (default stable temp path; may point at the real cache) instead of a wiped `t.TempDir` — `testCacheDir()` honors `DATAMITSU_TEST_CACHE`, defaults to `{os.TempDir}/datamitsu-e2e-cache` (created + symlink-resolved, never wiped)
- [x] overlay generator: temp git repo + `datamitsu.config.js` inheriting the vendored config via `getBeforeConfigs`, disabling all but a minimal tool set (via `getConfig` and/or `.datamitsuignore`) — `newOverlayProject(t, mutateJS)` reuses `clitest.NewProject` + `clitest.WriteOverlayConfig`, inheriting the vendored config via `getBeforeConfigs` and trimming tools in `getConfig`. ➕ Added `onlineEnv()` (strips `DATAMITSU_OFFLINE`/`DATAMITSU_NO_OCI` from `clitest.BaseEnv` — `Offline()` treats any non-empty value incl. `"0"` as on, so they must be removed, not set to `0`) + `runOnline()` (online sibling of `clitest.Run`, 5-min timeout, same shared `GOCOVERDIR`).
- [x] write a gated smoke test that is correctly skipped when the tag/env is absent — `TestOCISmoke`: builds an overlay, runs `config show`, asserts valid JSON with an `apps` key (proves vendored config parses + overlay merges; no network). Verified: tag-absent → "no test files"; tag-present env-absent → SKIP; tag+env → PASS.
- [x] run tests (default build: tier skipped) — must pass before next task — `go test ./test/e2e/...` → "no test files" (default build clean); `-tags e2e_oci` compiles + skips without env; `DATAMITSU_TEST_OCI=1 -tags e2e_oci` PASS; managed golangci-lint clean (0 issues) both default and `--build-tags e2e_oci`

### Task 2.2: OCI e2e — `store seed` + `store status`

- [x] `store seed` (no arg → config `oci`) pulls the digest-pinned bundle into the test cache — `test/e2e/store_seed_status_test.go` (`TestOCIStoreSeedAndStatus`, gated `e2e_oci`+`DATAMITSU_TEST_OCI=1`): reads the inherited `oci` ref/digest from `config show` (`declaredOCI`), then `store seed` with no arg → exit 0 + `Seeded store from <ref>@<digest>` line. Overlay keeps the config intact (`return config;`) so coverage reflects the real bundle; full-bundle airgap seed into the persistent `testCacheDir`.
- [x] `store status --json` asserts bundle ref/digest/seeded + covered apps — decodes `ocibundle.Status` JSON (`ociStatus`): asserts `ref`/`digest` equal the declared pin, `fullySeeded==true`, a non-empty `selected` platform, ≥1 layer with every layer `present`, and (when the bundle declares apps) ≥1 app `covered`.
- [x] re-seed is idempotent / fast (dedup from cache) — second `store seed` → exit 0 + same `Seeded store from …` line; post-reseed `store status --json` is unchanged (same digest, still `fullySeeded`). Dedup comes from the warm `testCacheDir` (never wiped).
- [x] run with `-tags e2e_oci DATAMITSU_TEST_OCI=1` — must pass; default build still green — default build: `go test ./test/e2e/...` → "no test files"; `-tags e2e_oci` compiles (`go vet`) + SKIPs without env; managed golangci-lint clean (0 issues) both default and `--build-tags e2e_oci`. (Live network seed runs locally/nightly per the gated tier — see Post-Completion.)

### Task 2.3: OCI e2e — `install` + `exec`

- [x] `install <minimal-app>` materializes into the store (assert install path exists) — `test/cli`/`test/e2e/install_exec_test.go` (`TestOCIInstallAndExec`, gated `e2e_oci`+`DATAMITSU_TEST_OCI=1`): `discoverInstallTarget` picks a minimal host-supported binary app from `config show` (prefers small single-binary tools — shellcheck/hadolint/shfmt/… — and requires the bundle to `cover` it for this host, so it seeds from the warm cache, never the network; skips if none); `install <app>` (default post-install verify ON) → exit 0, then asserts the isolated `store path` dir is non-empty AND `store status --json` reports that app `present`.
- [x] `exec <app> -- --version` runs the real tool → exit 0 + version substring present — `exec <app> -- <versionArgs>` where `versionArgs` is the app's declared `versionCheck.args` (falling back to `--version`, mirroring the install verify default) → exit 0 + non-empty combined stdout/stderr (version output lands on either stream depending on the tool).
- [x] `exec` with no args lists the now-installed tool — bare `exec` → exit 0, stdout contains the "Available tools:" header and the installed app's name.
- [x] run with the e2e tag — must pass; default build still green — default build `go test ./test/e2e/...` → "no test files"; `-tags e2e_oci` compiles + `go vet` clean + SKIPs without env; managed golangci-lint clean (0 issues) both default and `--build-tags e2e_oci`. (Live network install/exec runs locally/nightly per the gated tier — see Post-Completion.)

### Task 2.4: OCI e2e — `init` (minimal)

- [x] `init` with the overlay (most tools disabled) installs the minimal set + creates `.datamitsu` links and runs `initCommands`; assert filesystem effects — `test/e2e/init_test.go` (`TestOCIInitMinimal`, gated `e2e_oci`+`DATAMITSU_TEST_OCI=1`): overlay inherits the digest-pinned bundle (`return config;`), a real `init` exits 0 and materializes the project-local `.datamitsu/` (asserts the always-written `datamitsu.config.d.ts` + `.gitignore` type-def pair, which co-exist with any link symlinks); a second `init` proves idempotency (still exit 0, `.datamitsu/` intact). Bundle seeds from the warm `testCacheDir`, not the network.
- [x] `init --dry-run` parity: plan matches what the real run does — dry-run runs first on the clean tree: exit 0, output carries the `dry-run` marker, and **zero** filesystem effects (asserts `.datamitsu/` is NOT created — `CreateDatamitsuTypeDefinitions`/`CreateDatamitsuLinks` both no-op under `dryRun`). `extractInitPlan` then compares the two runs: the detected project-types descriptor (the body line under the `init` phase-open rule) must be identical, and every hook the dry-run **planned** must actually run in the real init (subset — the real run may surface more once earlier hooks create their `when` targets, but must never skip a promised one).
- [x] run with the e2e tag — must pass; default build still green — default `go test ./test/e2e/...` → "no test files"; `go vet -tags e2e_oci ./test/e2e/...` clean; `-tags e2e_oci` (env absent) → SKIP; managed golangci-lint clean (0 issues) both default and `--build-tags e2e_oci`. (Live network init runs locally/nightly per the gated tier — see Post-Completion.)

### Task 2.5: OCI e2e — `check` / `fix` / `lint` (one fast tool)

- [ ] enable a single fast tool (e.g. one formatter) on a fixture file with a known issue
- [ ] `lint` reports the issue (non-zero); `fix` repairs the file (content changes); `check` runs fix-then-lint
- [ ] assert behavior (exit codes + file diff), not byte-exact output (tool versions vary)
- [ ] run with the e2e tag — must pass; default build still green

### Task 3.1: Combined coverage tooling + refreshed baseline

- [ ] wire combined coverage: unit tests emit covdata (`go test ./... -cover -args -test.gocoverdir=$DIR1`) + blackbox `GOCOVERDIR=$DIR2`; merge via `go tool covdata merge -i=$DIR1,$DIR2 -o=$MERGED`; `textfmt` → single profile
- [ ] add a `package.json`/Taskfile script (e.g. `test:coverage:all`) producing the merged profile
- [ ] record the new combined coverage number; re-list the lowest-covered packages to finalize the Phase 3 target list (data-driven)
- [ ] run tests — must pass before next task

### Task 3.2: Coverage push — under-covered `internal/*` (wave 1)

- [ ] pick the 3–4 lowest-covered packages with real logic from Task 3.1 (candidates: `internal/ui`, `internal/term`, `internal/sponsor`, `internal/remotecfg`, `internal/ocidigest`, `internal/cache`, `internal/httpx`, `internal/dockerfile`)
- [ ] add table-driven unit tests for their public functions (success + error/edge)
- [ ] run tests + measure delta — must pass before next task

### Task 3.3: Coverage push — `cmd/` error branches via subprocess (wave 2)

- [ ] add subprocess tests for cmd error paths not hit by golden tests: conflicting flags, invalid flag values, missing/unreadable config, read-only target dirs, `runtimeconfig.Init` failure via a bad env value
- [ ] cover the `os.Exit` paths in `exec.go`/`root.go` (verify exit codes; coverage flushed via exit hook)
- [ ] run tests + measure delta — must pass before next task

### Task 3.4: Coverage push — close the gap to target (wave 3)

- [ ] iterate on remaining gaps from `go tool cover -func` until the agreed target (~80%+) is reached
- [ ] if a branch is only reachable in-process and not worth a subprocess test, add a focused unit test on the underlying `internal/*` function instead (no `cmd/` refactor)
- [ ] run tests + measure delta — must pass before next task

### Task 3.5: CI wiring

- [ ] update [.github/workflows/pr-checks.yml](../../.github/workflows/pr-checks.yml) to run the blackbox tier and upload the merged coverage profile (Codecov/Coveralls)
- [ ] ensure CI runs `pnpm build` before `go test` (so embedded `config.js` is fresh) and that `test/cli` builds the instrumented binary
- [ ] keep the OCI e2e tier OUT of default CI (gated); optionally add a manual/nightly job
- [ ] run CI locally (act or a dry equivalent) / push branch — checks must pass

### Task N-1: Verify acceptance criteria

- [ ] every leaf command has ≥1 blackbox test + flag-combo coverage (Task 1.12 gate passes)
- [ ] golden suite is byte-stable across two runs; offline tier needs no network
- [ ] OCI e2e tier passes locally with `-tags e2e_oci DATAMITSU_TEST_OCI=1`
- [ ] combined coverage meets the agreed target (~80%+); record the final number
- [ ] run the managed golangci-lint (`--max-issues-per-linter=0 --max-same-issues=0`) — all issues fixed
- [ ] confirm zero production CLI behavior changed (Phases 0–2 additive)

### Task N: Documentation

- [ ] add `test/cli/README.md` (or website docs per the docs policy): how to run the blackbox suite, update goldens (`-update`), run/update the OCI tier, and bump the vendored config (single source of truth)
- [ ] note the coverage-merge workflow and the `e2e_oci` gate in CONTRIBUTING / architecture docs
- [ ] update [CLAUDE.md](../../CLAUDE.md) testing section if new conventions were introduced

## Technical Details

**Directory layout**

- `internal/clitest/` — reusable harness: `binary.go` (build-once `-cover`), `run.go`
  (runner + clean env), `project.go` (temp git + config writers), `golden.go` (normalize + golden).
- `test/cli/` — offline golden contract tests (`package cli_test`) + `main_test.go` (`TestMain`)
  - `testdata/golden/*.txt`.
- `test/e2e/` — gated OCI tier (`//go:build e2e_oci`) + `testdata/datamitsu.config.oci-ghcr.js`
  (vendored) + `source.go` (release URL/version).

**Coverage collection (subprocess, Go 1.26)**

- Build: `go build -cover -covermode=atomic -o <bin> .` at module root.
- Run each invocation with `GOCOVERDIR=<dir>` in the env → instrumented binary writes counters
  on exit (including `os.Exit`, via the runtime exit hook).
- Convert/merge: `go tool covdata merge -i=<unitDir>,<blackboxDir> -o=<merged>` then
  `go tool covdata textfmt -i=<merged> -o=coverage.out` for Codecov/Coveralls.

**Hermetic offline invocation (default tier)** — clean env with:
`DATAMITSU_CACHE_DIR=<tmp>`, `DATAMITSU_OFFLINE=1`, `DATAMITSU_NO_OCI=1`, `NO_COLOR=1`,
no inherited `DATAMITSU_*`/`CI`/`TERM`; args include `--no-auto-config --config <generated>`;
CWD is a `git init`-ed temp dir. Output captured via pipes ⇒ non-TTY ⇒ plain mode (no progress bars).

**Golden normalization** — replace, in order: ANSI escapes → ""; temp project/cache/store/home
paths → `<TMP>`/`<CACHE>`/`<STORE>`/`<HOME>`; build version → `<VERSION>`; durations/timestamps →
`<DUR>`/`<TS>`. Keep normalizers in one place so all goldens share rules.

**OCI tier config inheritance** — generated `datamitsu.config.js`:

```js
globalThis.getBeforeConfigs = () => [{ path: "<abs path to vendored oci-ghcr.js>" }];
globalThis.getConfig = (config) => {
  /* keep a minimal tool/app set; disable the rest */ return config;
};
globalThis.getMinVersion = () => "0.0.0";
```

Tools are trimmed via `getConfig` and/or a generated `.datamitsuignore` (`**/*: *` then `!` re-enable
the chosen few) and/or the `--tools` flag for individual runs.

## Post-Completion

_Items requiring manual or external action — informational only._

**Maintenance**

- Bumping the vendored OCI config: re-download `OCIConfigSource` into `test/e2e/testdata/` whenever a
  new `datamitsu-config` release is cut (single edit point).
- Running the OCI e2e tier locally: `go test -tags e2e_oci ./test/e2e/...` with `DATAMITSU_TEST_OCI=1`
  (and optionally `DATAMITSU_TEST_CACHE` pointed at a warm cache for dedup speed).

**External / CI**

- Optional nightly/manual CI job for the OCI tier (needs registry pull access; not on every PR).
- Coverage thresholds in Codecov/Coveralls may need adjustment once the merged number stabilizes.
