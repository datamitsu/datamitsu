# Plan 1: Execution characterization — freeze `check|fix|lint` behaviour before changing it

**Status:** ready for implementation. Plan 1 of `2026-09-26-unified-results.md`; no decisions
open.
**Date:** 2026-09-26.
**Depends on:** nothing. **Unblocks:** every other plan in the index.
**Related:** `test/cli` (`check_fix_lint_test.go`, `lsp_test.go`, `devtools_parsers_test.go`),
`internal/clitest` (`run.go`, `project.go`, `golden.go`), `internal/parsermanager/testdata`,
`internal/runner/runner.go`, `internal/tooling/executor.go`.

> **Why this exists.** The blackbox suite freezes the CLI contract so the core can change
> underneath it, and `TestContractCompletenessGate` guarantees every leaf command has a test. For
> `check`, `fix` and `lint` that test covers `--help` and `--explain` only
> (`test/cli/check_fix_lint_test.go`). Nothing freezes what happens when a tool actually runs: the
> five levels at which fail-fast stops a run, the per-file loop that breaks on the first failing
> file, the frames a failure prints, the JSON-L stream, the cache footer, the exit codes. Plans 2,
> 3 and 5 rewrite exactly those places. Without a recorded baseline, their golden diffs cannot tell
> an intended change from an accident.

---

## 0. The idea in one paragraph

Add one blackbox file that runs real (shell-script) tools through `check|fix|lint` in the
isolated harness and records what the binary does today — including the behaviours the later plans
will change — as goldens and as causal assertions on the event stream. Add the small harness
pieces those tests need: a duration normalizer, a marker-file helper, a JSON-L reader with chain
assertions, a shell-tool config builder, a parser-module seeder. Put the parser module the wrapper
currently pins into `internal/parsermanager/testdata` with its provenance, and test the core's
contract with it. Nothing in the binary changes.

---

## 1. Grounding — verified facts

- **Coverage today.** `test/cli/check_fix_lint_test.go` asserts `--help` and `--explain`
  (`summary`, `detailed`, `json`) against a synthetic config whose apps are never installed
  (`explainToolsConfigJS`). No test runs a tool. `test/cli/lsp_test.go` is the only place a tool
  process runs: shell apps of the form
  `{ shell: { name: "sh", args: ["-c", "printf a >> \"$1\"", "append-a"] } }`.
- **Harness.** `clitest.Run(tb, RunOptions, args...)` runs the instrumented binary with
  `BaseEnv` (isolated cache dir, `DATAMITSU_OFFLINE=1`, `DATAMITSU_NO_OCI=1`, `NO_COLOR=1`, no
  inherited `DATAMITSU_*`, `CI` or `TERM`) and returns stdout, stderr and the exit code.
  `clitest.NewProject` gives a temp git repository with `WriteFile`; `WriteOverlayConfig` writes
  an auto-discovered `datamitsu.config.js` on top of a before-config. `Normalizer` masks absolute
  paths (`MaskPath`), ANSI sequences, timestamps, the build version, Go-formatted durations
  (`durationRE`, `golden.go:33`, applied at `:112`) and box-drawing rule fills, and can sort
  lines. It normalizes text only: numeric JSON fields such as `duration_ms` and `ts` in a JSON-L
  line are not covered.
- **Planner shapes that matter for the scenarios.** A `per-file` scope yields one task per file
  (`planner.go:469-478`), so the executor's per-file loop over several files runs for a
  `per-project` or `repository` task whose args carry `{file}` (`RunsPerFile`,
  `internal/config/arity.go:54-59`). Repository-scoped tasks always overlap
  (`HasOverlap`, `planner.go:1174`), so two of them at one priority run sequentially whatever
  their globs; tasks in different project directories never overlap and run in parallel. Parallel
  tasks are shuffled before scheduling (`executor.go:356`).
- **Parser module fixture.** `internal/parsermanager/testdata/echo.wasm` (385 520 bytes) is a
  full build of the crate with every real parser; `runtime_test.go` reads it directly, and
  `devtools_parsers_test.go` passes it with `--wasm`. No blackbox test seeds a parser directory
  today; the only seeding is `internal/runner/parser_test.go:43-59`, through an `httptest`
  server. `storedModuleIsValid` (`parsermanager.go:553`) accepts a module placed at its
  content-addressed path when its bytes match the declared SHA-256, which is what an offline seed
  relies on. The file has no recorded provenance (version, source).
- **The wrapper's module.** The pinned wrapper version comes from the pnpm catalog in
  `pnpm-workspace.yaml` (`0.0.0-unstable.20260912.f201136`); its `parsers.core` entry declares an
  OCI source on `ghcr.io/datamitsu/datamitsu-parsers` by digest and the SHA-256
  `b5425355969f69f9a80a9838269b350b33ab47ec290358d7aa761483a2921986`
  (`datamitsu.config.js:10347-10353` in the installed package). The module is therefore
  identified by its hash, not by a release file name.
- **Platform skips** exist only for binary apps: `BinaryAvailable` returns true when
  `app.Binary == nil` (`binmanager.go:1006-1020`).
- **Fail-fast levels** (the behaviour to freeze, all hard-wired by
  `tooling.NewExecutor(root, false, true, …)` at `internal/runner/runner.go:269`):
  1. between priority groups — `executor.go:163-166`;
  2. between the sub-groups of one group, which run one after another when their tasks overlap —
     `:261-264`, `:283-286`;
  3. between parallel siblings — `:419-423`, and running siblings are killed through
     `exec.CommandContext` (`buildCommand`, `:575-580`);
  4. between files of a per-file operation — `break` at `:1006-1013` and `:908`;
  5. between the operations of `check` — `runner.go:967-971`.
- **What a failure prints.** `printFailedExecution` (`runner.go:1389-1442`): a red frame with
  dir, cwd, command, exit code, duration, then parsed diagnostics when `usableDiagnostics`
  (`:1372`) or the raw output. Cancelled tasks are hidden as noise (`:571-575`, `:1129`).
- **Events.** `uievent.Event` is flat; the runner emits `phase`, `tool_run` (start/done/fail),
  `chunk`, `error`, `done` per operation (`runner.go:420-684`). `toolOpID` is `run + tool + dir`
  and is not unique per task (`:1078-1092`). Every event carries `ts` and most carry
  `duration_ms`, and parallel events arrive in no fixed order.
- **Footer.** `printOperationFooter` (`:1455-1489`) prints `N tools · M runs · done in <dur>`, with
  `· K failed`, `· S skipped`, `· cache P%`; `<dur>` is the sum of group wall-clock times.
- **Exit codes today.** 0; 1 for any failure; 4 for `--require-coverage`
  (`internal/runner/exitcode.go`); `--fail-on-skip` gives 1 (`runner.go:989`); cobra flag errors
  give 1 (`cmd/root.go:263-281`); 2 and 3 exist only in `llms`.

---

## 2. What is frozen

Every scenario is a subtest with its own project, config, goldens (stdout and stderr, normalized)
and exit-code assertion. Where the behaviour is a known defect, the test records it as observed
and says so in a comment naming the plan that changes it; a later plan flips the assertion in the
same PR that changes the behaviour.

Tools are `sh` scripts declared as shell apps. A tool that "fails" exits 1 after printing a fixed
line; a tool that "runs" appends its name and its first argument to a marker file in the project
(`<root>/.markers/<tool>`), so the test can assert whether a process ran without reading logs.
Every scenario skips when `sh` is not on `PATH` (Windows), naming what is left unverified.

| #   | Scenario                                                                                                                                                                                                                                                                                               | Observed today (recorded)                                                                                                                                                                                                                     | Changed by            |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------- |
| S1  | `lint`: two repository-scoped tools, both exit 0                                                                                                                                                                                                                                                       | two `✓` lines, footer `2 tools · 2 runs`, exit 0; events `phase start`, two `tool_run start/done` pairs, `done` with `tools: 2, runs: 2`                                                                                                      | —                     |
| S2  | `lint`: repository-scoped tool A at priority 10 exits 1, repository-scoped tool B at priority 20                                                                                                                                                                                                       | A's red frame, B never runs (no marker, no events), footer `1 failed`, exit 1                                                                                                                                                                 | Plan 2                |
| S3  | `lint`: repository-scoped A and B at the same priority (they always overlap → sequential sub-groups), A exits 1                                                                                                                                                                                        | B never runs                                                                                                                                                                                                                                  | Plan 2                |
| S4  | `lint`: per-project A in `pkg/a` and per-project B in `pkg/b` at the same priority (disjoint project paths → parallel), `DATAMITSU_MAX_PARALLEL_WORKERS=2`; B writes a `started` marker at once, then sleeps 3 s and writes `done`; A waits (polling, ≤ 2 s) for B's `started` marker and then exits 1 | B's `done` marker absent (killed); B absent from the results block; no `tool_run` terminal event for B (orphaned start); exit 1. The handshake guarantees B had started when A failed                                                         | Plan 2, Plan 3 (IDs)  |
| S4b | as S4 with `DATAMITSU_MAX_PARALLEL_WORKERS=1` and A first in plan order: B never acquires a worker                                                                                                                                                                                                     | B cancelled before starting: no `started` marker, no events at all for B                                                                                                                                                                      | Plan 2                |
| S5  | `lint`: one per-project task with `args: ["{file}"]` over `bad1.txt`, `bad2.txt`, `ok.txt` (the executor's per-file loop), failing on files named `bad*`                                                                                                                                               | one failed file, loop breaks: `bad2.txt` and `ok.txt` have no marker; frame shows the first failure; exit 1                                                                                                                                   | Plan 2                |
| S6  | `check`: fix tool exits 1, lint tool exits 0                                                                                                                                                                                                                                                           | lint phase never starts (no `phase` event for lint), exit 1; no closing wall-clock line                                                                                                                                                       | Plan 2                |
| S7  | `lint` twice on a per-file tool over three clean files                                                                                                                                                                                                                                                 | second run: footer `cache 100%`, tool marker not written again; per-file events for the cached task as emitted today                                                                                                                          | Plan 3                |
| S8  | `lint`: a tool with an `outputParser` (`hadolint` from the seeded module) printing hadolint-shaped JSON and exiting 1; and the same with `--no-parse`                                                                                                                                                  | parsed lines in the frame (`file:row:col severity message [code]`); raw JSON with `--no-parse`                                                                                                                                                | Plan 3                |
| S9  | `lint`: the same parsed tool printing one finding and exiting 0, run twice                                                                                                                                                                                                                             | nothing printed for the passing run; second run is a cache hit (`cache 100%`)                                                                                                                                                                 | Plan 3 (C1)           |
| S10 | `lint --fail-on-skip` with a binary app declared for one platform that is not the host's (a well-formed SHA-256, never downloaded)                                                                                                                                                                     | `⊘ skipped` line, exit **1**                                                                                                                                                                                                                  | Plan 2                |
| S11 | `lint --require-coverage=repo` from a subdirectory                                                                                                                                                                                                                                                     | exit 4 with the coverage message                                                                                                                                                                                                              | —                     |
| S12 | `lint --log-format jsonl` on S2, S4 and S5                                                                                                                                                                                                                                                             | every stderr line is JSON with `type` and `op_id`; asserted through `AssertChains` and field checks, plus a golden of the stream normalized by `NormalizeJSONL` with sorted lines; chains are as observed, including the orphaned start of S4 | Plan 2, 3             |
| S13 | `lint --bogus-flag`, `lint --widen-to=Repo`, `lint --require-coverage=unit --tools alpha`                                                                                                                                                                                                              | exit **1** with `error:` on stderr                                                                                                                                                                                                            | Plan 2 (usage code 2) |
| S14 | `fix`: a per-file tool that appends a line to its file, run twice                                                                                                                                                                                                                                      | the file changes on the first run and is cached on the second (`AfterFix` records the post-fix hash); the frame is green; `check` runs lint after it                                                                                          | Plan 3 (C2)           |

Assertions on the event stream are causal, not positional: for every `tool_run` with
`status: start` there is a terminal `done`/`fail` with the same `op_id` unless the task was
cancelled; `done.runs` equals the number of terminal `tool_run` events; the `phase` of an operation
precedes every `tool_run` of that operation. Parallel scenarios never assert line order.

---

## 3. Harness additions (`internal/clitest`)

- **`clitest.NormalizeJSONL(stderr string) string`** — for JSON-L goldens: parses each line,
  replaces `ts` with `0` and `duration_ms` with `1` when present (presence is part of the
  contract: an `omitempty` zero must stay absent), re-encodes with sorted keys. Text
  normalization stays as it is (`durationRE` already masks human durations).
- **`clitest.ShellTool(name, script string, op ToolOpSpec) string`** — returns the JS for a tool
  whose app is `sh -c <script>`; `ToolOpSpec` carries scope, globs, priority and the placeholders
  (`{file}`, `{files}`), so a scenario is a few lines rather than a page of JS.
- **`clitest.MarkerDir(p *Project)`** and **`p.Marker(tool) (bool, string)`** — the `.markers`
  convention above.
- **`clitest.ParseJSONL(stderr string) ([]Event, error)`** and **`AssertChains(tb, events)`** —
  the causal checks of §2, reusable by plans 2, 3 and 6.
- **`clitest.SeedParserModule(tb, cacheDir, modulePath string) (declaration string)`** — copies
  the given module (`internal/parsermanager/testdata/echo.wasm`, or a released fixture of §4)
  into the run's isolated parser directory at the content-addressed path `moduleDir` computes
  (`{parsers dir}/{name}/{xxh3("parser-v2", hash)}/module.wasm`) and returns the `parsers`
  declaration (a placeholder `https://` URL plus the real SHA-256) to splice into a config;
  `storedModuleIsValid` then accepts it offline. Written from scratch (no blackbox test seeds
  parsers today); `internal/runner/parser_test.go` keeps its `httptest` variant, which exercises
  the download path.
- **`BaseEnv` strips more.** Today it removes `DATAMITSU_*`, `CI`, `TERM`, `NO_COLOR` and
  `GOCOVERDIR` (`internal/clitest/run.go:97-134`); a golden recorded inside GitHub Actions or
  inside an agent session therefore inherits `GITHUB_ACTIONS=true` or `CLAUDECODE=1`, and plan
  7's `--annotations auto` would print annotations in CI goldens only. The strip set gains
  `GITHUB_ACTIONS`, `TF_BUILD`, `TEAMCITY_VERSION`, the agent marker names and prefixes of plan 4
  (`AI_AGENT`, `AGENT`, `CLAUDECODE`, `CLAUDE_CODE`, `CLAUDE_CODE_CHILD_SESSION`, `GEMINI_CLI`,
  `CURSOR_AGENT`, `OPENCODE`, `AUGMENT_AGENT`, `CODEX_*`, `COPILOT_*`, `JUNIE_*`), `FORCE_COLOR`
  and `CLICOLOR_FORCE`. A scenario that needs one of them sets it explicitly. Plan 7 replaces
  the literal CI names with `cienv.Variables()` and pins the two lists with a test.

---

## 4. The released module fixture

`internal/parsermanager/testdata/echo.wasm` stays what it is: a build of the current crate.
Today nothing regenerates it (`task build:parsers` builds into Cargo's target directory and
regenerates the catalogue page only, `Taskfile.yaml:28-32`); plan 9 adds `task
build:parsers:fixture`, and until then the copy is a documented manual step in
`testdata/README.md`. Next to it:

- `internal/parsermanager/testdata/released/v1/b5425355.wasm` — the module the pinned wrapper
  declares (§1: hash `b5425355…`, OCI digest on `ghcr.io/datamitsu/datamitsu-parsers`), fetched
  by that digest (or from the release whose `checksums.txt` lists the hash) and verified against
  the hash; the file is named by the hash prefix because the pin carries no release name.
  **This file is immutable:** it is the last released module of response ABI v1 and descriptor
  schema 1, the contract plans 5 and 9 must keep reading. It is not replaced when the wrapper's
  pin moves; a later ABI gets its own directory (`released/v2/…`) when a v2 module has been
  released.
- `internal/parsermanager/testdata/released/README.md` — for each fixture: full SHA-256, the
  registry reference and digest (or release URL) it came from, the module's own `describe`
  version, the wrapper version that pinned it, the ABI and descriptor schema it carries, and the
  rule above.
- `internal/parsermanager/released_test.go` — the core ↔ released module contract:
  `describe` decodes with `schemaVersion: 1` and lists a fixed set of tool names recorded in the
  test (not "everything the current catalogue documents", which grows), exports `reset`;
  `parse` of a hadolint fixture yields the expected nullable diagnostics; an unknown tool name
  yields `[]`; the instance pools and resets. This is the previous-release scenario that plans 5
  and 9 extend into the "three contract scenarios" of index R12.

`.gitattributes` gains `*.wasm binary linguist-generated=true` here, because this PR is the first
to add a second module blob (the index's D8e applies to every `.wasm` in the repository).

---

## 5. Work breakdown

**PR 1 — harness and scenarios.** `internal/clitest` additions of §3; `test/cli/execution_test.go`
with S1–S14 and their goldens under `test/cli/testdata/golden/execution_*`. No change under
`cmd/` or `internal/` outside `clitest`.

**PR 2 — released module fixture.** The files of §4, the `.gitattributes` line, the contract
test. No change to the core.

Both PRs: `go test ./test/cli/ -count=2` byte-stable; the added wall time of the suite under 60 s
(S4 sleeps 3 s once). The blackbox binary is built with `-cover`, not `-race`
(`internal/clitest/binary.go:118`), so race safety of the executor is asserted by
`go test -race ./internal/tooling/ ./internal/runner/`, not by this suite.

---

## 6. Verification

- `go test ./test/cli/ -count=2` passes and every new golden is byte-stable across the two runs.
- `go test -race ./internal/tooling/ ./internal/runner/` passes (the blackbox binary is not
  race-instrumented).
- Deleting any one fail-fast `break`/`cancel` in the executor makes at least one of S2–S6 fail:
  the suite detects the behaviour it freezes (checked once by hand, not automated).
- `TestContractCompletenessGate` still passes; the new file registers `check`, `fix`, `lint`
  scenarios under the existing registration mechanism.
- The released module test passes offline (no network in the harness).

---

## 7. Risks

- **Timing in S4.** The handshake (A waits for B's `started` marker) removes the scheduling race
  the shuffled worker pool would otherwise introduce; the 3-second sleep is the window in which
  the kill must land. If a loaded CI runner still proves flaky, the window grows, the assertion
  does not weaken.
- **Windows.** The scenarios need `sh`; they skip on Windows. The contract they freeze is
  platform-independent, so the skip loses coverage, not correctness.
- **Golden churn.** Plans 2, 3 and 5 will regenerate most of these goldens. That is the point:
  each regeneration is reviewed as a diff against this baseline.

---

## 8. Non-goals

- No behaviour change of any kind; a scenario that reveals a bug records it.
- No frame-layout goldens beyond what the scenarios need: the frames change in plan 5.
- No Windows shell tools; no real third-party tools (offline suite).
- No fixture for a _future_ module: the released one is the previous-version contract, the
  crate build is the current one.
