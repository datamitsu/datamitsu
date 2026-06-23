# Test Coverage Uplift to 80%+ (combined unit + blackbox)

## Overview

Raise datamitsu's **combined test coverage** (unit + blackbox) from the current **72.0%
unit-only** baseline to **80%+**, ROI-ordered so effort goes where the most uncovered
statements are.

The combined-coverage merge **already exists** (`scripts/coverage-all.sh`, added with #142): it
runs unit covdata + blackbox `GOCOVERDIR` and merges them via `go tool covdata`. A plain
`go test -coverprofile` cannot see subprocess execution, so it under-reports — the merge
reclaims the blackbox contribution (e.g. `cmd/`: 54.4% unit-only → 29.5% blackbox-only →
**68.1% merged**). **Measured combined baseline today: 76.3%** (vs 72.0% unit-only). Only
**~3.7 points (~500 statements)** remain to reach 80%+, cleared by a focused unit-test push on
the largest pure-unit gaps (runtimemanager, binmanager); CI just needs to adopt the existing
script.

**Key benefits**

- An honest coverage number that reflects what the blackbox suite already covers.
- Targeted tests on the riskiest, least-covered logic (runtime/bin managers, tooling, OCI).
- De-risks the upcoming core rewrite alongside the CLI-contract safety net from #142.

**Out of scope (YAGNI):** chasing 100%; testing pure ANSI rendering, `main.go`, build-injected
`ldflags`, generated/embedded config, or trivial constant data. The OCI e2e tier (`test/e2e`)
is a separate plan.

## Context (from discovery)

**Precondition / sequencing (decided):** this plan starts **after PR #142 (blackbox harness +
golden contract) is merged to `main`**, on a **new branch from `main`**. #142 is currently
being implemented by ralphex on `feat/cli-blackbox-tests`; do not start until it lands so the
harness (`internal/clitest`, `test/cli`) and `GOCOVERDIR` plumbing are present.

**Measured baseline (2026-06-23): 76.3% combined** (`scripts/coverage-all.sh`), vs **72.0%
unit-only** (`go test ./... -coverprofile`, which cannot see subprocess coverage). The merge
already reclaims the blackbox contribution — e.g. `cmd/`: 54.4% unit-only → 29.5% blackbox-only
→ **68.1% merged**. Gap to 80%: **~3.7 points (~500 statements)**.

**ROI ranking — uncovered statements per package (highest first):**

| Package                                                                                                        |  Uncovered |   Total |        Cov% | Notes                                                                                              |
| -------------------------------------------------------------------------------------------------------------- | ---------: | ------: | ----------: | -------------------------------------------------------------------------------------------------- |
| `cmd`                                                                                                          |       1647 |    3615 | 54%→**68%** | Merge already reclaims most (54.4%→68.1% combined); residual = error branches + devtools internals |
| `internal/runtimemanager`                                                                                      |        477 |    1263 |         62% | Biggest pure-unit gap — runtime install/version/platform/error paths                               |
| `internal/binmanager`                                                                                          |        400 |    1318 |         70% | download/extract/exec/error paths (httptest + t.TempDir)                                           |
| `internal/tooling`                                                                                             |        217 |    1125 |         81% | planner/runner tool logic                                                                          |
| `internal/ocibundle`                                                                                           |        161 |     662 |         76% | seed/status/layout error paths                                                                     |
| `internal/runner`                                                                                              |         97 |     422 |         77% | sequential/parallel orchestration branches                                                         |
| `internal/ui`                                                                                                  |         81 |     232 |         65% | **cover logic only**, not ANSI rendering                                                           |
| `internal/config`                                                                                              |         79 |     565 |         86% | loader edge cases                                                                                  |
| `internal/cache`                                                                                               |         58 |     248 |         77% | clear/path/project resolution                                                                      |
| `internal/engine` (+`/tools`)                                                                                  |      53+38 | 374+226 |     86%/83% | goja injection branches                                                                            |
| `internal/ocidigest`                                                                                           |         50 |     241 |         79% | resolver/token error paths                                                                         |
| `internal/registry`,`traverser`,`managedconfig`,`target`,`utils`,`install`,`bundled`,`remotecfg`,`verifycache` | 14–38 each |       — |           — | small mop-up                                                                                       |

Math (post-merge): combined baseline **76.3%**; total ≈ 13.4k statements. To reach 80% we must
cover **~500 more statements**. The merge is already in place (it lifted `cmd/` to 68.1%);
runtimemanager (477 uncov) + binmanager (~380 uncov) unit tests alone clear the gap — Tasks 5–9
are headroom above 80%.

**Excluded from effort (not from the honest denominator):** `main.go`, `internal/ldflags`
(0%, build-injected vars), `internal/constants` (no tests; trivial data), generated
`internal/config/config.js`, pure ANSI emission in `internal/term` (66.7%) and the render
layer of `internal/ui`.

**Conventions:** stdlib `testing` only (no testify), table-driven, `httptest`, `t.TempDir`,
`t.Setenv`. Go 1.26.4 (`go tool covdata` available). Use the managed golangci-lint
(`--max-issues-per-linter=0 --max-same-issues=0`).

**Decisions (from planning Q&A):**

1. Count **combined** coverage (unit + blackbox merge); target **80%+**.
2. Start **after #142 merges**, new branch from `main`.
3. Prioritize by ROI (uncovered-statement count); exclude generated/main/pure-render.

## Development Approach

- **Testing approach: regular** — add tests for existing behavior; no production changes
  (except tiny testability seams only if unavoidable, preferring tests on `internal/*`).
- Each task targets specific uncovered functions found via
  `go tool cover -func=<profile> | grep <pkg> | sort -t')' -k2 -n`.
- Complete each task fully; **all tests pass and coverage delta is measured before the next**.
- **CRITICAL: every task delivers passing tests** (success + error/edge cases).
- **CRITICAL: keep this plan in sync** — check items off, add ➕ discovered tasks, ⚠️ blockers.
- Re-measure after Task 1; later task targets are finalized from the post-merge numbers.

## Testing Strategy

- **Unit tests**: primary vehicle for Tasks 2+ (table-driven, mocked network/fs).
- **Combined coverage**: unit covdata + blackbox `GOCOVERDIR` merged via `go tool covdata`,
  emitted as one profile for Codecov/Coveralls and the total %.
- **Per-task delta**: record covered-% before/after each task in the checklist.
- **Determinism**: tests offline and hermetic; no reliance on the dev's real cache/store.

## Progress Tracking

- Mark completed items `[x]` immediately; add ➕ for discovered tasks, ⚠️ for blockers.
- Update the ROI table / targets if the post-merge numbers shift priorities.

## What Goes Where

- **Implementation Steps** (`[ ]`): coverage tooling, unit tests, CI wiring, docs — in-repo.
- **Post-Completion** (no checkboxes): Codecov threshold bump once the number stabilizes.

## Implementation Steps

### Task 1: Adopt the existing combined-coverage merge in CI

- [x] `scripts/coverage-all.sh` already merges unit + blackbox covdata (added with #142) — confirm it runs green and prints the combined total (**76.3% baseline** reconfirmed 2026-06-23 — `total: 76.3%`)
- [x] add a `test:coverage:all` wrapper in package.json/Taskfile that calls the script (already present: `"test:coverage:all": "bash scripts/coverage-all.sh"`)
- [x] update [.github/workflows/pr-checks.yml](../../.github/workflows/pr-checks.yml) to feed the **merged** profile to Codecov/Coveralls (replacing the unit-only `coverage.out`, which under-reports `cmd/`) — the `test` job already runs `pnpm test:coverage:all` and uploads the merged `coverage.out` to both Codecov and Coveralls
- [x] re-rank residual gaps from the merged profile (record lowest-covered packages — see table below)
- [x] verify CI uploads the merged number — workflow `test` job uploads merged `coverage.out`; local merge runs green and prints `76.3%`

**Re-ranked residual gaps (merged profile, 2026-06-23) — uncovered statements, highest first:**

| Package                   | Uncovered | Total |  Cov% |
| ------------------------- | --------: | ----: | ----: |
| `cmd`                     |      1153 |  3615 | 68.1% |
| `internal/runtimemanager` |       477 |  1263 | 62.2% |
| `internal/binmanager`     |       381 |  1318 | 71.1% |
| `internal/tooling`        |       207 |  1125 | 81.6% |
| `internal/ocibundle`      |       160 |   662 | 75.8% |
| `internal/runner`         |        72 |   422 | 82.9% |
| `internal/ui`             |        68 |   232 | 70.7% |
| `internal/config`         |        67 |   565 | 88.1% |
| `internal/cache`          |        59 |   248 | 76.2% |
| `internal/ocidigest`      |        50 |   241 | 79.3% |
| `internal/engine`         |        48 |   374 | 87.2% |
| `internal/registry`       |        38 |   314 | 87.9% |
| `internal/engine/tools`   |        38 |   226 | 83.2% |
| `internal/managedconfig`  |        33 |   167 | 80.2% |

Ordering matches the original ROI ranking — runtimemanager (Task 2) + binmanager (Task 3) remain the biggest pure-unit gaps and alone clear the ~500-statement gap to 80%.

### Task 2: internal/runtimemanager error & platform paths

- [x] list uncovered funcs: `go tool cover -func=coverage.out | grep runtimemanager | sort -t')' -k2 -n` (top pure-unit gaps: `ComputeRuntimeStorePath` 0%, `GetJVMCommandInfo` 0%, `getRuntimePath`/`InstallRuntimes` error branches, `moveRuntimeFiles`/`removeAll` error paths)
- [x] add table-driven tests for the lowest-covered runtime install / version-resolution / platform-selection functions (success cases) — new `internal/runtimemanager/coverage_paths_test.go`: `ComputeRuntimeStorePath` (managed/system/store-path), `GetJVMCommandInfo` (jar + main-class modes, system-config fallback), `getRuntimePath` system-mode resolution
- [x] add error/edge cases (download failure, bad version, unsupported platform, timeout) using `httptest` + `t.TempDir` — unknown runtime, system/managed missing config, unsupported arch/libc, unsafe `binaryPath`, `moveRuntimeFiles` empty-dir/missing-source/unsafe-path, `ComputeAppPath`/`GetCommandInfo` non-runtime & unresolvable-runtime branches, `removeAll` seam
- [x] run `go test ./internal/runtimemanager/...` + measure delta — **passes; coverage 62.2% → 66.9% (+4.7 pts)**, full `go test ./...` + managed golangci-lint clean

### Task 3: internal/binmanager download/extract/exec paths

- [x] list uncovered funcs in `binmanager` (top pure-unit gaps: `VerifyBinaryExtraction`/`DownloadFileForVerify` 0%, `ResolvedBinaryInfo`/`GetBundles`/`InstallBundleByName` 0%, `downloadFileSimple`/`writeFiles`/`downloadAndExtractExternalArchive`, `copyDir`/`copyFile`/`extractZipToDir`/`extractArchiveToPath` partial branches)
- [x] add tests for uncovered extract (archive formats), exec wiring, and download error handling (`httptest`, in-memory tar.gz/zip fixtures) — new `internal/binmanager/coverage_paths_test.go`: `VerifyBinaryExtraction` (binary + tar.gz member), `DownloadFileForVerify`, `downloadFileSimple`, `downloadAndExtractExternalArchive` (download→verify→extract), `writeFiles`, `copyDir`/`copyFile` (files/nested/symlinks), `extractZipToDir`, `extractArchiveToPath`/`ExtractArchiveToDir`, `ResolvedBinaryInfo`, `GetBundles`, `InstallBundleByName`, `InstallBundles` already-cached branch
- [x] add error/edge cases (corrupt archive, missing binary, hash mismatch, missing file, path-traversal/absolute-symlink skips, dest in a missing directory, unsupported format, escape-install) via `httptest` + `t.TempDir`
- [x] run `go test ./internal/binmanager/...` + measure delta — **passes; coverage 69.3% → 72.5% unit-only (+3.2 pts)**, full `go test ./...` + managed golangci-lint clean

### Task 4: internal/tooling planner/runner logic

- [x] list uncovered funcs in `tooling` (top pure-unit gaps: `executeBatchChunksParallel` 0%, `GetTimings` 0%, `executeBatch` 34.4%, `findFilesByGlobs` 33.3%, `parseGlobExtensions` 48%, `formatParallelizationInfo` 55.6%, `filterTasksBySelectedTools` 66.7%, `createPerProjectTasksWithFiles` 68.9%, `SkipReason.String` 75%)
- [x] add table-driven tests for uncovered planning/overlap/scoping branches (success cases) — new `internal/tooling/coverage_paths_test.go`: `GetTimings`, `parseGlobExtensions` (simple/brace/nil-reject branches), `filterFilesByGlobs`/`findFilesByGlobs` (cached + uncached traverser fallback), `createPerProjectTasksWithFiles` (projectTypes filter, file grouping, no-files-per-project, no-restriction), `formatParallelizationInfo` (single/all-parallel/worker-pool-exceeded/multi-group), `executeBatch` dry-run (whole-project, single chunk, multi-chunk parallel fan-out)
- [x] add error/edge cases (no tools, unknown tool, conflicting scopes) — `filterTasksBySelectedTools` `ToolNotFoundError` with sorted available list, `SkipReason.String` out-of-range default branch, `executeBatchChunksParallel` pre-cancelled context → cancelled classification
- [x] run `go test ./internal/tooling/...` + measure delta — **passes; coverage 80.7% → 89.5% unit-only (+8.8 pts)**, full `go test ./...` + managed golangci-lint clean

### Task 5: internal/ocibundle + ocidigest error paths

- [x] list uncovered funcs in `ocibundle` and `ocidigest` (top pure-unit gaps: ocidigest `NewResolver`/`Registry` 0%, `parseSHA256Digest`/`fetchToken`/`saveCachedDigest`/`PullManifest` partial; ocibundle `removeStaged` 0%, `placeSubtree`/`SeedFromLayout`/`writeMarker`/`runtimeAppKind`/`buildReVerifyIndex`/`expectedSubtrees`/`subtreeRel` + `layoutSource` blob/manifest/blobPath partial)
- [x] add tests for seed/status/layout and resolver/token branches (success cases) — new `internal/ocidigest/coverage_paths_test.go`: `NewResolver`/`Registry`, `fetchToken` access_token fallback + service/scope forwarding; new `internal/ocibundle/coverage_paths_test.go`: `layoutSource` manifest/blob/blobPath success, `placeSubtree` (race-loss drop + fresh rename), `removeStaged`, `runtimeAppKind` (uv/node/jvm/go), `subtreeRel`, `buildReVerifyIndex` binary app, `expectedSubtrees` binary-app path
- [x] add error/edge cases (bad digest, missing layer, registry/token failure) with mocked transport — `parseSHA256Digest` (missing prefix/wrong algo/short/non-hex/uppercase), `PullManifest` bad-digest/oversize/5xx, `fetchToken` malformed-challenge/invalid-realm/non-OK/undecodable/empty-token, `saveCachedDigest` mkdir failure; ocibundle `blobPath` bad digests, `manifest` missing/mismatch, `blob` missing/size-mismatch, `SeedFromLayout` missing-dir/not-a-directory, `writeMarker` mkdir failure, `subtreeRel`/`expectedSubtrees` outside-store + unknown/shell-app skips
- [x] run `go test ./internal/ocibundle/... ./internal/ocidigest/...` + measure delta — **passes; ocidigest 79.3% → 86.3% (+7.0 pts), ocibundle 75.7% → 78.1% (+2.4 pts)**, full `go test ./...` + managed golangci-lint clean

### Task 6: internal/runner, cache, target mop-up

- [x] list uncovered funcs in `runner`, `cache`, `target` (top gaps: runner `RunContinuation`/`getStagedFiles` 0%, `createCache` 46.7%; cache `Save` 48.4%/`debounceSave` 50%/`AfterFix`/`markPassed` nil-data branches; target `HostTarget` 0%, `detectViaELF`/`detectViaLoaderPaths`/`defaultLoaderGlob`/`runLddVersion` 0–66.7%)
- [x] add tests for uncovered orchestration / cache-clear / project-resolution / target-selection branches — new `internal/runner/coverage_paths_test.go` (`createCache` invalidateOn dedup + no-invalidateOn, `getStagedFiles` real-git staged/committed, `RunContinuation` wiring), `internal/cache/coverage_paths_test.go` (`AfterFix` change-reset + unchanged-add, disabled no-op, `debounceSave` timer flush), `internal/target/coverage_paths_test.go` (`HostTarget` memoization, `defaultLoaderGlob`, real-Linux `detectViaLdd`/`detectViaELF`/`detectViaLoaderPaths`/`runLddVersion`)
- [x] add error/edge cases (fail-fast, missing git root, empty selection) — `getStagedFiles` non-repo error branch, nil-data errors for `Save`/`AfterFix`/`AfterLint`(markPassed), `RunContinuation` config-load failure, `defaultLoaderGlob` malformed-pattern error
- [x] run tests for the three packages + measure delta — **passes; runner 77.0% → 82.5% (+5.5), cache 76.6% → 81.0% (+4.4), target 77.3% → 87.2% (+9.9)**, full `go test ./...` + managed golangci-lint clean

### Task 7: small internal/\* packages mop-up

- [ ] cover remaining gaps in `managedconfig`, `utils`, `remotecfg`, `verifycache`, `config` (loader edges), and `engine`/`engine/tools` injection branches
- [ ] each: success + error/edge cases; skip pure-render code paths
- [ ] (optional) add a trivial test for `internal/constants` if cheap 100% is wanted
- [ ] run `go test ./...` + measure delta — must pass before next task

### Task 8: internal/ui logic (non-render)

- [ ] cover `ui` logic branches (state/progress bookkeeping), explicitly NOT ANSI emission
- [ ] add tests asserting behavior via the data model, not escape-sequence bytes
- [ ] run `go test ./internal/ui/...` + measure delta — must pass before next task

### Task 9: cmd/ residual error branches (post-merge gaps)

- [ ] from the post-Task-1 merged profile, list `cmd/` statements still uncovered after blackbox
- [ ] add subprocess error-path tests (conflicting flags, invalid values, missing/unreadable config, `runtimeconfig.Init` failure via bad env) reusing `internal/clitest`
- [ ] where a branch is only reachable in-process, add a unit test on the underlying `internal/*` helper instead (no `cmd/` refactor)
- [ ] run combined coverage + measure delta — must pass before next task

### Task N-1: Verify acceptance criteria

- [ ] combined coverage ≥ 80% (record the final number)
- [ ] full suite green: `go test ./...` and the blackbox tier
- [ ] managed golangci-lint clean (`--max-issues-per-linter=0 --max-same-issues=0`)
- [ ] coverage run is deterministic and offline; CI uploads the merged profile

### Task N: Documentation

- [ ] document the combined-coverage workflow (`test:coverage:all`, covdata merge) in CONTRIBUTING / architecture docs
- [ ] note the measurement caveat (blackbox needs the merge to count) so future contributors don't misread `cmd/` numbers

## Technical Details

**Combined coverage commands (Go 1.26):**

```bash
DIR1=$(mktemp -d); DIR2=$(mktemp -d); M=$(mktemp -d)
go test ./... -cover -args -test.gocoverdir=$DIR1           # unit → covdata
GOCOVERDIR=$DIR2 go test ./test/cli/...                     # blackbox subprocess → covdata
go tool covdata merge -i=$DIR1,$DIR2 -o=$M
go tool covdata percent -i=$M                               # total %
go tool covdata textfmt -i=$M -o=coverage.out              # for Codecov/Coveralls
```

**Finding ROI targets inside a package:**

```bash
go tool cover -func=coverage.out | grep <pkg> | awk '$3+0 < 80' | sort -t')' -k2 -n
```

## Post-Completion

_Informational — no checkboxes._

- Bump the Codecov/Coveralls target threshold once the combined number stabilizes ≥ 80%.
- Revisit `internal/term`/`ui` render coverage and the `test/e2e` OCI tier in their own plans
  if a higher target is later desired.
