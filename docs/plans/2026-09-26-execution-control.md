# Plan 2: Execution control — keep-going, `check` wall clock, and the exit-code contract

**Status:** ready for implementation. Plan 2 of `2026-09-26-unified-results.md`; implements D17,
D18, D22 and the execution-side half of R7. No decisions open.
**Date:** 2026-09-26.
**Depends on:** plan 1 (the goldens it changes). **Unblocks:** plan 3 (they edit the same
per-file loop), plan 6 (keep-going, exit 2, cancelled tasks in the result).
**Related:** `internal/runner/runner.go` (`initSharedContext`, `runSequential`, `runOperation`,
`printOperationFooter`, `skipFailure`), `internal/tooling/executor.go` (fail-fast sites),
`internal/uievent`, `internal/env`, `internal/runtimeconfig`, `internal/timing`, `cmd/root.go`,
`cmd/check.go`, `cmd/fix.go`, `cmd/lint.go`, `internal/runner/exitcode.go`, `cmd/exitcode.go`.

> **Why this exists.** A run that stops at the first failure is the right default for a person at a
> terminal, and the wrong shape for every other consumer: a report that lists the first failing
> tool and the first failing file of a per-file linter is a report about one error. Today the stop
> is hard-wired at five levels, `check` has no total time, `--fail-on-skip` and a usage error share
> exit code 1 with a real failure, and cancelled tasks vanish from every output. This plan adds one
> switch and makes the run's outcome legible.

---

## 0. The idea in one paragraph

`--fail-fast=false` (or `DATAMITSU_FAIL_FAST=false`) runs every group, sub-group, sibling, file
and both operations of `check` to the end; the exit code is still 1 if anything failed. Tasks a
fail-fast run cancelled or never started are shown, counted and reported instead of hidden.
`check` prints one closing line with the wall clock of the whole command and emits a run-level
`done` event. Usage errors exit 2 and `--fail-on-skip` exits 4, so a pipeline can tell "you called
it wrong", "the code is bad" and "we did not look at everything" apart.

---

## 1. Grounding — verified facts

- `failFast` is the third argument of `tooling.NewExecutor` and is `true` in the runner
  (`internal/runner/runner.go:269`); the language server passes `false`
  (`internal/lsp/session.go:101`). No flag, no variable.
- The five stop levels and their sites: priority groups (`executor.go:163-166`), parallel
  sub-groups (`:261-264`, `:283-286`), parallel siblings (`:419-423`, running ones killed through
  `exec.CommandContext` from `buildCommand`, `:575-580`), per-file loop (`:1006-1013`, and `:908`
  on a stdin error), operations of `check` (`runSequential`, `runner.go:967-971`, documented as
  "If fix fails, lint is skipped" in `cli-commands.md:129`).
- **Not every cancelled task has a result.** Siblings cancelled while waiting for a worker get a
  result with `Cancelled: true` and `FailureReason: FailureReasonCancelled`; tasks in later
  priority groups and later sequential sub-groups are abandoned without any result
  (`executor.go:163`, `:261`). The runner drops the results it does get from `tool_run`/`error`
  events (`runner.go:571-575`) and from the results block (`groupResultsByTool`, `:1129`). A task
  cancelled after its `start` event leaves an orphaned start (`:1087`).
- `printOperationFooter` (`:1454-1486`) prints per-operation `done in <dur>` where `<dur>` is the
  sum of `GroupExecutionResult.WallClockDuration`; nothing prints after the operation loop, and
  the loop returns on the first operation error. `runSequential` also serves `--explain`: the two
  operations of `check --explain=json` go through the same loop (`runner.go:335`) and print two
  JSON documents (D9 keeps that).
- `sc.timings` is created in `initSharedContext` (`:148`) before the config load; its start time
  is unexported (`internal/timing/timing.go:15`) and the package exposes no elapsed-time accessor.
- The only `done` event is per operation (`:674`). The run-level event the granularity plan
  promised in §11.4 does not exist.
- Exit sites: `cmd/root.go:263-281` — a `CodedError` returns its code, anything else 1; cobra flag
  errors and `runner.Options.validate()` errors are plain errors, so `lint --bogus` and
  `--widen-to=Repo` exit 1. `internal/runner/exitcode.go` defines `ExitCoverage = 4` and
  `coverageError`; `cmd/exitcode.go` defines the `CodedError` interface. `cmd` imports
  `internal/runner`, not the reverse. `skipFailure` (`runner.go:989`) and `coverageFailure`
  return immediately after the operation loop (`:975-985`), so a tool failure already reported by
  the loop never reaches them today.
- `runtimeconfig` keeps compile-time defaults as constants and tests `Compute()` per field
  (`internal/runtimeconfig/runtimeconfig_test.go`), as the Runtime Config Policy requires.

---

## 2. Design

### 2.1 `--fail-fast`

```text
datamitsu fix|lint|check --fail-fast[=true|false]   default true
DATAMITSU_FAIL_FAST=false                            env twin; the flag wins
```

- `internal/runtimeconfig`: constant `FailFast = true`; `Effective.FailFast bool`
  (`json:"failFast"`) and `Effective.FailFastSource string` (`"default"` or `"env"`) wired in
  `Compute()`; visible in `datamitsu config runtime`. `internal/env`: `envVar failFast` with
  default `"true"`, getter `FailFast() (value, set bool)` — `true`/`1` and `false`/`0`
  (case-insensitive, trimmed) are the two values, anything else is invalid and the command layer
  exits 2 for it (the getter itself falls back to the default, by policy). Plan 6 needs the
  `set` half: an explicit `true` from the flag or the environment together with a listing report
  is a usage error, an implied `true` is not. Precedence: flag > env > (plan 6) report-implied >
  default. Tests in `env_test.go` (unset, `true`, `false`, `0`, `FALSE`, `yes`) and
  `runtimeconfig_test.go` (default, override, source, presence of both keys, stable JSON).
  `DATAMITSU_FAIL_FAST` is added to `environExcluded` (index §1.2, registered details: it
  changes what a run prints, not what the farm contains) and is not observation-only; `CLAUDE.md`'s
  environment policy gains the "execution-only" category in this PR.
- `runner.Options` gains `FailFast *bool` (nil = use the environment). The three commands bind
  `--fail-fast` with `NoOptDefVal = "true"` so both `--fail-fast` and `--fail-fast=false` parse.
- The errors of `bundled.RunFix` and `bundled.RunLint` (the bundled ignore-file checks, before
  the operations) are setup errors, not tool failures: they still stop the run in both modes.
- `initSharedContext` passes the resolved value to `NewExecutor`. The executor needs no new logic:
  its `failFast == false` paths already run everything; what changes is that the runner now
  reaches them.

Effect per level with `--fail-fast=false`:

| Level                 | Today                                       | Keep-going                                                                                                                                                                 |
| --------------------- | ------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| priority groups       | stop after the first failing group          | every group runs in order                                                                                                                                                  |
| parallel sub-groups   | stop                                        | all run                                                                                                                                                                    |
| parallel siblings     | waiting ones cancelled, running ones killed | all finish; no process is killed by fail-fast (Ctrl-C and SIGTERM unchanged)                                                                                               |
| per-file files        | `break` on the first failure                | every file runs; the task is `Success=false`, `ExitCode` is the last **failing** file's (as today, `executor.go:995`, `:1030`), and the diagnostics of every file are kept |
| operations of `check` | fix failed → lint skipped                   | lint runs (D17); the footer prints `lint ran after a failed fix`                                                                                                           |
| exit code             | 1 on any failure                            | unchanged: 1 if any invocation failed                                                                                                                                      |

**Outcome selection in `runSequential`.** The loop keeps the first operation error (`opErr`) and
continues when keep-going is on. After the loop the skip and coverage checks run as today, and
the returned error is chosen in this order: `opErr` (exit 1) before the `--fail-on-skip` error
(exit 4, §2.5) before the coverage error (exit 4). Plan 6 appends the export error (exit 5) at the
end of the same list. This keeps index §2.2's precedence 1 > 4 > 5 and does not change the
single exit site in `cmd/root.go`.

### 2.2 Cancelled and unstarted tasks are visible

With fail-fast on, a task that did not run is a fact about the run every later consumer needs (a
JUnit `<skipped>`, a SARIF run omitted for incompleteness, an agent that must not read "no
output" as "clean"). Today it is hidden or absent. From this plan:

- **Two kinds, both derived by the runner.** A task with a `Cancelled` result was started or was
  waiting for a worker; a task that has no result at all was never reached (a later group or
  sub-group). The runner computes the second kind as _planned tasks minus tasks with a result_,
  keyed by (tool, dir, plan index) — a stable identity that exists before plan 3 introduces the
  executor's `TaskID`; plan 3 replaces the key with `TaskID` without changing the output.
- **Results block.** After the tool lines, one line per such task: `⊘ <tool> [dir]  cancelled
(fail-fast)` for a started-then-cancelled task and `⊘ <tool> [dir]  not started (fail-fast)`
  for an unreached one, in the style of planner skips; the footer adds `· N cancelled`.
- **Events.** Both kinds end with `status: "skip"` (the `StatusSkip` value granularity plan
  §11.4 reserved), never `"fail"`: a cancelled task is not a failure. An unreached task emits one
  `tool_run` with `msg: "not started: fail-fast"`; a started task that was killed emits its
  terminal `tool_run` with `msg: "cancelled: fail-fast"`, which closes the orphaned start of
  today. A task cancelled by Ctrl-C or SIGTERM carries `msg: "cancelled: interrupted"` — the
  executor distinguishes the two through the context's cause (`context.Cause`), which the
  fail-fast `cancel` sets. Exact start/terminal pairing for per-file tasks sharing one
  directory still waits for plan 3's unique ids; until then the chain assertion of plan 1
  tolerates that one known ambiguity and nothing else. `done` gains `cancelled *int
json:"cancelled,omitempty"`, counted separately from `skipped` (planner skips and
  `skip: true`); a pointer so zero can be written when a consumer asks for it.
- **`ExecutionResult`** is unchanged; the runner reads `Cancelled` and `FailureReason`.
- The human summary's `(N failed)` does not count cancelled runs: they are not failures.

### 2.3 `check` wall clock (D22)

After both operations of `check`, one closing rule in the footer style:

```text
┗━ check · done in 12.3s · fix 4.1s · lint 7.9s · setup 0.3s
```

- `done in` is the wall clock of the whole command from the start of `initSharedContext` to the
  end of the last operation. `internal/timing` gains `func (t *Timings) Elapsed() time.Duration`
  (the started time is unexported today) and the runner reads it; no dependency on
  `DATAMITSU_TIMINGS`. `fix`/`lint` repeat the operation footers' execution sums. `setup` is the
  difference: config load, walk, bundled checks, planning, `EnsureTools`, parser prewarm.
- A skipped operation is named: `lint not run` (fix failed under fail-fast). The line prints on
  failure too, so it is emitted after the operation loop, before the outcome selection of §2.1.
- Printed only in **execution mode** when more than one operation was planned (`check`);
  `--explain` in every mode prints nothing extra (the two-document `--explain=json` contract is
  untouched); `fix` and `lint` alone keep their footer as the last line; `RunContinuation`
  (reconcile's post-fix) never prints it.
- **Run-level event.** `{type: "done", op_id: "cmd-N", op: "check"|"fix"|"lint", status,
success, duration_ms: <wall clock>, tools, runs, failed, skipped, cancelled, complete}` — sums
  over the operations, emitted for every execution of the three commands (D22: only the human
  line is `check`-only; the event is what granularity plan §11.4 asked for). `complete` is a
  `*bool` meaning execution completeness: every planned operation ran and no task was cancelled
  or unstarted (plan 6 adds extraction and scope to its own `Run.Complete`; this field does not
  change meaning then). Per-operation `done` events are unchanged; a consumer distinguishes by
  `op_id` prefix (`cmd-` versus `run-`). Emitted in execution mode only.

### 2.4 Usage errors exit 2

- A new leaf package `internal/exitcode` holds the codes (`Usage = 2`, `Coverage = 4`; plan 6
  adds `Export = 5`) and the error types (`UsageError`, `CoverageError`, each implementing
  `ExitCode() int`). `internal/runner/exitcode.go`'s `coverageError` moves there; `cmd/exitcode.go`
  keeps the `CodedError` interface and its single exit site. Both `cmd` and `internal/runner`
  import `internal/exitcode`; nothing cycles.
- `rootCmd.SetFlagErrorFunc` wraps every cobra flag-parse error in `exitcode.UsageError`.
- `runner.Options.validate()` returns `exitcode.UsageError` for invalid `--widen-to` and
  `--require-coverage` values; the `--require-coverage` with `--tools` refusal, which lives in
  `initSharedContext` (`errRequireCoverageWithTools`, `runner.go:236-238`, `:789`), returns the
  same type.
- Positional-argument validators are wrapped once by `cmd.usageArgs(cobra.PositionalArgs)` on the
  commands that use them.
- Messages are unchanged; only the code moves from 1 to 2. `llms` keeps its own 2 and 3. Later
  plans route every "you called it wrong" error (invalid `DATAMITSU_*` values checked in the
  command layer, `--report` conflicts, policy refusals known at plan time) through the same type.

### 2.5 `--fail-on-skip` exits 4

`skipFailure` returns `exitcode.CoverageError`: a tool with no binary for the host is "we did not
look at everything", the same thing `--require-coverage` reports. Intentional `skip: true` and
narrowing skips are still never counted (`recordSkips` is unchanged). When both `--fail-on-skip`
and `--require-coverage` fail, both messages are printed in that order and the process exits 4
once (granularity plan §11.4).

---

## 3. Interfaces

```go
// internal/env: envVar failFast (DATAMITSU_FAIL_FAST, default "true"); func FailFast() (value, set bool); environExcluded += failFast
// internal/runtimeconfig: const FailFast = true; Effective.FailFast bool `json:"failFast"`; Effective.FailFastSource string `json:"failFastSource"`
// internal/timing: func (t *Timings) Elapsed() time.Duration
// internal/exitcode: const Usage = 2, Coverage = 4; type UsageError, CoverageError
// internal/runner: Options.FailFast *bool
// internal/uievent: StatusSkip = "skip"; Event.Cancelled *int `json:"cancelled,omitempty"`;
//                   Event.Complete *bool `json:"complete,omitempty"` (run-level done)
// cmd: rootCmd.SetFlagErrorFunc(...) → exitcode.UsageError; func usageArgs(cobra.PositionalArgs) cobra.PositionalArgs
```

Documentation surfaces: `website/docs/reference/cli-commands.md` (the `--fail-fast` flag on
`check`, `fix`, `lint`; `DATAMITSU_FAIL_FAST` in the variables table; the `check` example output
with the closing line; the run-level `done`, `status: "skip"` and `cancelled` in the events
contract; the exit-code table with 2 and 4; the "If fix fails, lint is skipped" sentence at `:129`
reworded); `website/docs/guides/architecture/execution.md` (fail-fast is the default and how to
turn it off; the level table above; the cancelled-task lines); `task gen:llms-docs`; the agent
guide gains two lines: in CI run `datamitsu lint --fail-fast=false` (CI runs `lint`; `fix` and
`check` change the working tree), and for local work run `datamitsu check --fail-fast=false` to
see every failure at once; `CLAUDE.md` "Product Stage" gains two breaking entries (usage errors
exit 2; `--fail-on-skip` exits 4).

---

## 4. Work breakdown

**PR 1 — keep-going and visible cancellations.** §2.1 and §2.2: env, runtimeconfig (constant,
field, tests), flag, `Options.FailFast`, the outcome selection, cancelled and unstarted lines,
events, docs. Goldens of plan 1's S2–S6 gain keep-going twins (`execution_*_keep_going`); the
fail-fast goldens change where cancelled tasks now print. Breaking note: none (default
unchanged; the new lines are additive).

**PR 2 — `check` wall clock and run-level `done`.** §2.3 with `timing.Elapsed()`. Goldens for
S6 and a new `check_total_time` scenario; negative goldens for `check --explain` in all three
modes (nothing extra printed). The existing duration normalizer masks the times.

**PR 3 — `internal/exitcode` and usage code 2.** §2.4. Plan 1's S13 flips from 1 to 2. Breaking
note.

**PR 4 — `--fail-on-skip` exits 4.** §2.5. Plan 1's S10 flips from 1 to 4. Breaking note; one
line in `cli-commands.md` "Skipped tools".

PRs 3 and 4 are independent of 1 and 2 and of each other; they are separate so each exit-code
change can be reverted alone (index §4.3).

---

## 5. Verification

- `--fail-fast=false` on plan 1's fixtures: S2 and S3 run B (marker present); S4's B finishes (its
  `done` marker present, terminal `done` event for B); S4b's B runs; S5 runs all three files and
  reports both failures with the last failing exit code; S6 runs lint after the failed fix, prints
  `lint ran after a failed fix`, exit 1.
- `DATAMITSU_FAIL_FAST=false` without the flag behaves the same; `--fail-fast` with
  `DATAMITSU_FAIL_FAST=false` restores the default; `datamitsu config runtime | jq .failFast`
  reports the effective value; the env and runtimeconfig unit tests of §2.1.
- Fail-fast default: S2's B and S4b's B print `not started (fail-fast)` with a `status: "skip"`
  event; S4's B prints `cancelled (fail-fast)` with a terminal `skip` event whose `msg` says
  `cancelled: fail-fast`; a Ctrl-C during S4 gives `cancelled: interrupted`; `done.cancelled`
  counts both kinds; a tool failure plus `--fail-on-skip` exits 1 (1 > 4); `--fail-on-skip` alone
  with a platform-skipped tool exits 4; `--fail-on-skip` with `--require-coverage=repo` from a
  subdirectory prints both messages and exits 4.
- The run-level `done` is emitted for `fix` and `lint` too, with `op` set accordingly.
- `check`: the closing line's `done in` ≥ `fix` + `lint`; `lint not run` on a failed fix under
  fail-fast; the run-level `done` carries `op: "check"` and sums; none of this prints for `fix` or
  `lint` alone, for `config reconcile`'s post-fix, or for any `--explain` mode.
- `lint --bogus`, `lint --widen-to=Repo`, `lint --require-coverage=unit --tools x` exit 2 with the
  same messages as before.
- `go test ./test/cli/ -count=2` byte-stable; `go test -race ./internal/runner/ ./internal/tooling/`.

---

## 6. Risks

- **Noise under keep-going.** A failed formatter at priority 10 hands unformatted input to
  linters at 20; their findings are real but transient. Documented as the cost of the mode; the
  report of plan 6 marks the fix operation failed so a reader can discount them.
- **Long runs.** With every group running, a repository with several broken tools takes longer to
  fail. That is what the user asked for; the default is unchanged.
- **Scripts that test `== 1`** for `--fail-on-skip` or for usage errors break. Alpha; breaking
  notes.

---

## 7. Non-goals

- No change to which tasks the planner produces, to `--widen-to`, `--require-coverage` or to the
  cache.
- No new stop levels and no partial keep-going (per-level switches).
- No report-driven automatic keep-going: that belongs to plan 6, which owns `--report`.
- The language server is untouched: it already runs without fail-fast.
