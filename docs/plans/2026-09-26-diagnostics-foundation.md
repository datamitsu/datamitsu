# Plan 3: Diagnostics foundation — a result the cache, the editor and every report can trust

**Status:** ready for implementation. Plan 3 of `2026-09-26-unified-results.md`; owns Stage 0 of
`2026-09-24-lsp-diagnostics.md` (moved here, see the index §7) and implements R5, D7 and the
verdict-identity half of R12. No decisions open.
**Date:** 2026-09-26.
**Depends on:** plan 1 (goldens), plan 2 (the per-file loop and the cancelled-task representation
it changes). **Unblocks:** plans 5, 6, 9 and the LSP diagnostics plan's Stage 1.
**Related:** `internal/diagnostic/diagnostic.go`, `internal/tooling/executor.go`
(`parseFileDiagnostics`, `executePerFile`, `executeBatch`, callbacks), `internal/tooling/types.go`
(`ExecutionResult`), `internal/tooling/verdict.go` (`verdictIdentity`, `verdictSnapshot`,
`recordVerdict`), `internal/cache/cache.go` (`ShouldRun`, `markPassed`, `AfterFix`,
`calculateInvalidationKey`), `internal/runner/runner.go` (`toolOpID`, parse warnings,
`usableDiagnostics`), `parsers/datamitsu-parsers` (fixtures, tests only).

> **Why this exists.** A diagnostic is a claim about a file, and every consumer after the terminal
> keeps it: an editor until the next publish, GitHub code scanning until the next upload, GitLab
> until the next pipeline. Today the pipeline that produces it has gaps that only the terminal
> can afford: a parse failure looks like a clean run; a cache pass is recorded from the exit code
> alone, so a tool that exits 0 on findings hides them on every later run; a pass can be recorded
> against bytes the tool never saw; positions have no stated contract; paths come back in three
> spellings; a task folds N processes into one output, one command and the edits of its last
> file; and a task's events cannot be told apart from its siblings. This plan closes those gaps
> in the core, once, for the CLI, the editor and the reports alike.

---

## 0. The idea in one paragraph

Make the executor's result complete and honest: every position 1-based with an exclusive end,
every path absolute, every process an invocation spawned recorded with its own files, exit,
extraction outcome and edits, every parse failure marked, every task uniquely identified in the
event stream. Then change what a cache pass means: it is recorded only against the bytes the tool
actually read (C2) and only when extraction happened and found nothing at any level and of any
provenance (C1, D7), so a cache hit can be replayed as "nothing to report" by anyone. The upgrade
cools every cache once, and the module a tool's parser comes from is an explicit part of the
verdict identity, because under C1 the parser decides the pass.

---

## 1. Grounding — verified facts

Read at `main` `65326d6`; the LSP diagnostics plan §1 measured the same code and is not repeated
here beyond what this plan changes.

- **`Resolve`** (`internal/diagnostic/diagnostic.go:72-99`) replaces only `nil` positions with 1;
  its doc comment promises 0 → 1 and the code does not do it. Several parsers emit 0 on purpose
  (`trivy`, `reek`, `npm_groovy_lint`; `spectral`, `vacuum`, `pylint` are 0-based throughout, so
  a positive column from them is off by one, `spectral.rs:84`). No end-column convention is
  stated; several parsers use the `end_col = col + 1` idiom.
- **Paths.** A per-file run stamps its file on diagnostics without one (`executor.go:809-813`); a
  batch run passes `""` (`:1208`) and gives the tool paths relative to the working directory
  (`makeRelativePaths`, `:1081`; `getWorkingDir`, `:1460-1468`). Tools report a mix of relative
  (tsc, golangci-lint, yamllint, cspell) and absolute (eslint) paths; nothing normalizes them;
  the CLI shortens absolute ones against the working directory only (`relativeToBase`,
  `runner.go:1336-1345`).
- **Parse failures** are `log.Warn` per invocation (`executor.go:800-807`) and leave
  `Diagnostics` empty: indistinguishable from a clean run. An unknown parser name returns `[]`
  from the module (`lib.rs:149`); validation checks only the module reference
  (`validate.go:812-820`).
- **Cache.** `updateCacheAfterSuccess` (`:724-763`) records per-file passes and `recordVerdict`
  (`:551-553`, `verdict.go:628`) unit verdicts from `result.Success` alone. `markPassed`
  (`cache.go:741-782`) appends a tool to an existing entry without comparing its `ContentHash`;
  the LSP plan reproduces the defect (A passes on X; B passes on Y; revert to X; B is skipped on
  content it never saw). `ShouldRun` hashes under the read lock (`:425-489`, at `:462`).
  `calculateInvalidationKey` (`:600-625`) hashes `ldflags.Version` (always `dev` locally), the
  whole config JSON and the selected tools; a key mismatch clears both per-file entries and
  verdicts (`cache.go:227`, `:610`).
- **Verdict identity** (`verdict.go:41-58`): tool, operation, unit dir, granularity, arity, raw
  args, sorted `op.Env`. The parser module is not named in it; today that is covered indirectly,
  because a change of `config.Parser.Hash` changes the config JSON and therefore the enclosing
  invalidation key. `verdictSnapshot` (`verdict.go:202`) already records per-file identity, size,
  modification and change times and a snapshot time, and re-hashes only when a stat is
  inconclusive (`recordVerdict`, `:634-658`).
- **`ExecutionResult`** (`types.go:156-188`): `Output` is the joined output of every process,
  `ExitCode` is the last **failing** process's code (`executor.go:995`, `:1030`), `Command` is the
  last process's command line, `FormatEdits` holds the edits of the last formatted file
  (`:976`); batch chunks are executed separately and merged away (`:1314`). There is no list of
  files, no `Cached`, no per-process outcome.
- **Events.** `toolOpID` is `run + tool + dir` (`runner.go:1078-1092`): per-file tasks in one
  directory share an id, a tool in two sibling directories mixes its chunk events, and a task
  cancelled after its start leaves the start orphaned. The executor callbacks carry no task
  identity (`executor.go:84-89`).
- **Display.** Diagnostics are printed only under a failed run (`printFailedExecution`,
  `runner.go:1389`); a batch result whose diagnostics carry no file prints raw output
  (`usableDiagnostics`, `:1372-1385`).
- **`--no-parse` / `DATAMITSU_NO_PARSE`** turn parsing off entirely (`runner.go:270-276`,
  `parser.go:12-21`), so with them a parsed lint operation would record no pass under C1.

---

## 2. Design

### 2.1 Positions and paths (the core contract)

Set in `internal/diagnostic` and documented on `Diagnostic`:

- `Row`, `Col` are 1-based; `nil` **and 0** become 1.
- `EndRow`, `EndCol` are 1-based and `EndCol` is exclusive; a missing end, or an end before the
  start, becomes a point span at the start.
- `File` is absolute and cleaned. `parseFileDiagnostics` gains the invocation's working directory
  and resolves a relative reported path against it; an absolute one is cleaned. A per-file
  process stamps its file on diagnostics without one, as today; a batch of exactly one file
  stamps that file; a larger batch leaves a file-less diagnostic file-less.
- What the core cannot do: tell a 0-based positive column from a 1-based one, or an inclusive
  end from an exclusive one. Those are corrected in the parsers — plan 5's module release audits
  every parser for base and end convention (spectral, vacuum, pylint; the `col + 1` idiom) — and a
  configuration pinned to an older module keeps the older positions. The column unit stays a
  property of the parser (`columnUnit`, plan 5); conversion into other units is plan 6's
  `internal/textpos`.

Visible CLI changes, named in the PR and reflected in plan 1's goldens: `file:R:0` prints as
`file:R:1`; a one-file batch prints parsed lines instead of raw output; a path that climbs out of
the working directory prints absolute; `./x` prints as `x`.

### 2.2 Extraction outcome, `ParseFailed`, and `--no-parse` as a display flag

Every process gets an **extraction outcome**, the vocabulary the index §2.5 fixes:

| Outcome              | When                                                                                                 | May record a pass |
| -------------------- | ---------------------------------------------------------------------------------------------------- | ----------------- |
| `parsed-clean`       | a parser ran without error and returned no diagnostic (v1), or `recognized: true` with none (plan 9) | yes               |
| `parsed-findings`    | a parser returned at least one diagnostic                                                            | no                |
| `parser-unavailable` | the module failed to load, or the declared parser key is not in the module's `describe`              | no                |
| `parse-failed`       | the module returned an error                                                                         | no                |
| `truncated`          | the output exceeded the parse cap or the findings cap                                                | no                |
| `none`               | no parser is declared (until plan 9's fallback runs) — nothing was attempted                         | exit-status rule  |

- `ExecutionResult.ParseFailed bool` (any process of the task is `parse-failed` or
  `parser-unavailable`). The executor logs the failure at `debug`; the runner prints one warning
  per tool per run (`output parser failed for <tool>: <first error>`), one per run when a module
  fails to load (naming how many tools will run uncached and suggesting `datamitsu devtools
parsers prefetch`), and one per run for an unknown parser key. An unknown key is an extraction
  failure that blocks the pass; it is not a config load error, because `describe` needs the
  module and loading must stay offline-safe.
- `--no-parse` and `DATAMITSU_NO_PARSE` no longer disable parsing. The executor parses whenever a
  tool declares a parser; the flag switches the runner's display to raw output. A run with the
  flag still fetches and compiles parser modules. (LSP plan D12, accepted.)

### 2.3 Rule C2 — record against the bytes the tool saw

- The executor hashes each file **before** the run through the process-wide content memo
  (`memoShared`), for new entries too, and hands the cache a `Seen` value built the way
  `verdictSnapshot` builds its entries (`verdict.go:202`): hash, size, modification time, change
  time, file identity, snapshot time. The cache compares under its lock and never hashes there.
- After the run the file is re-checked with the same probe `recordVerdict` uses
  (`verdictSnapshot.refresh`): unchanged by identity and times → the entry is recorded (replacing
  an entry whose hash differs, adding the tool to one whose hash matches); a stat that moved, or
  one whose resolution cannot rule out a same-length rewrite, re-hashes (`memoRewrite`, never
  served from the memo); a file that changed during the run records nothing; a failed hash
  records nothing.
- API (`internal/cache`): `type Seen struct{ Hash string; Size int64; ModTime, ChangeTime
time.Time; Identity FileIdentity; At time.Time }`, `Check(file, tool string, op Operation, seen
Seen, enabled bool) (run bool)`, `AfterLint(file, tool string, seen Seen, unchanged bool,
enabled bool) error`. `AfterFix` keeps its comparison. `markPassed`'s append-without-compare
  path goes away.
- **Upgrade.** `calculateInvalidationKey` gains a constant cache-semantics component; this PR
  sets it to `c2v1`, so every entry recorded by the old writer misses once and cannot hit the
  repaired reader with stale evidence.

### 2.4 Rule C1 — a pass means "nothing to report"

For a **lint** operation, per process, by extraction outcome:

- A per-file pass is recorded for a file only when the process's outcome is `parsed-clean` after
  attribution: every diagnostic of the process matched a file of that process (after §2.1's
  normalization), and none matched this file. A diagnostic that matches no file of the process
  blocks every pass of the process. `parsed-findings`, `parser-unavailable`, `parse-failed` and
  `truncated` record nothing; `none` keeps today's exit-status rule until plan 9 replaces `none`
  with a fallback outcome.
- A unit verdict is recorded only when every process of the task is `parsed-clean` (or `none`
  under the exit-status rule) and the task produced no diagnostic at all.
- **D7:** every finding of every provenance and every level counts, including plan 9's fallback
  findings and findings below plan 5's `failOn`. There is no "informational" finding that a pass
  may hide. Eligibility follows the outcome, never the presence of an `outputParser` declaration.
- Fix operations record a pass when the task succeeded — which, from plan 5, includes the
  threshold gate — and not from extraction: the parser also runs over fix output, where text
  such as eslint's fix report is not what it was written for.
- Consequence, by design: a file with findings of a tool that exits 0 on them (hadolint
  `info`/`style`) re-runs on every `check`. Warm and cold `check` wall time on this repository and
  on the startup-cost plan's large-repository benchmark are measured and recorded in the PR; a
  regression that matters opens the separate decision of §6 (finding-fingerprint replay), it does
  not block the merge (R5). A second measurement follows checkpoint R1, after the wrapper drops
  eslint's `--quiet` (D21), when the number of files with findings grows.

**Upgrade.** The semantics component becomes `c1v1` in this PR: every pass recorded under the
exit-status rule, including by PR 3's writer, misses once.

### 2.5 The parser module is part of the verdict identity, explicitly

`verdictIdentity` gains two components: the SHA-256 (`config.Parser.Hash`) of the module the
tool's `outputParser` names and the parser key (`outputParser.parser`), or empty components when
the tool has none — the key matters because `parser: "x"` and `parser: "y"` of one module answer
differently. Today a change of either is caught only through the whole-config hash in the
enclosing invalidation key; naming them in the identity makes the dependency explicit (under C1
the parser decides the pass, and plan 5 changes what a module reports) and keeps it when a later
plan narrows what the invalidation key hashes. The identity prefix moves from `dmv1` to `dmv2`,
so every stored verdict misses once with this change. Plan 9 adds the embedded fallback module's
content hash to `calculateInvalidationKey`, because `ldflags.Version` is `dev` for every local
build and a changed fallback must not replay passes it did not decide.

### 2.6 A result that lists its processes and files

```go
// internal/tooling/types.go
type ProcessResult struct {
    ID          string           // TaskID + "#" + index (§2.7)
    Files       []string         // absolute, cleaned; the files this process was given
    State       ProcessState     // ran | cancelled | not-started | setup-failed
    ExitCode    *int             // nil unless State == ran
    Success     bool
    Extraction  Extraction       // §2.2 vocabulary
    ParseError  string           // the module's error text for parse-failed / parser-unavailable
    OutputTail  []byte           // last 4 KiB of the joined output; plan 6 masks before export
    Diagnostics []diagnostic.Diagnostic
    DurationMs  int64
}

type FileResult struct {
    File       string
    State      FileState        // ran | cached | verdict-hit | cancelled | not-started | setup-failed
    ProcessID  string           // "" unless State == ran
    Success    bool             // ran: the process outcome; cached/verdict-hit: true
    ExitCode   *int             // nil unless State == ran
    Edits      []textdiff.Edit  // diff-in-core edits applied to this file, nil when unchanged
}

// ExecutionResult gains:
TaskID      string
Processes   []ProcessResult // one per spawned process; per-file mode: one per file; batch: one per chunk
Files       []string        // the task's files as planned, absolute; run or cached alike
FileResults []FileResult    // one per Files entry, same order; a verdict hit lists the unit members as verdict-hit
Cached      bool            // no process ran: a verdict hit, or every file cached
WholeUnit   bool            // argv carried no path: the result speaks for UnitDir
UnitDir     string
ParseFailed bool
```

Aggregate fields keep their meaning for the frame and are documented separately: `Output` is
the joined output of every process, `ExitCode` the last failing process's code, `Command` the
last process's command line, `Diagnostics` the concatenation over processes. `FormatEdits` is
removed: nothing reads it (the language server's `FormatFile` recomputes its edits with
`textdiff.ComputeEdits`, `internal/lsp/server.go:272`), and `FileResult.Edits` is its per-file
replacement. For a `WholeUnit` task (`arity=none`), `Files` and `FileResults` list the unit's
members as planned (`UnitMembers`), so a unit tool has a file result per member. Plan 6's
`Invocation` is a `ProcessResult` with its parent task's identity, and plan 6's `OutputTail` is
`ProcessResult.OutputTail` after masking.

### 2.7 Unique task and process identity

The executor gives every task an identity before it runs: `TaskID = <tool>:<dir>:<seq>`, where
`seq` numbers the tasks of one `Execute` in plan order; each process is `TaskID#<n>`.
`TaskStartCallback`, `FileProgressCallback` and `ResultCallback` carry the task identity (a new
parameter, or the result's `TaskID`). The runner's `toolOpID` becomes `<run op id>:<TaskID>`;
chunk events use their own task's id rather than "any active dir"; plan 2's cancelled-task lines
switch from the (tool, dir, plan index) key to `TaskID`. With this the three cases of
`runner.go:1078-1092` are gone and the comment goes with them. JSON-L goldens change once
(`:<seq>` suffix) and are reviewed as such.

### 2.8 Parser fixtures: clean and finding-bearing

`parsers/datamitsu-parsers` gains, for every wired parser (actionlint, checkmake, dclint,
dotenv-linter, eslint, hadolint, protolint, yamllint, cspell, tsc, golangci-lint, harper-cli,
vale), a **pair** of fixtures recorded from the real tool: clean output (exit 0, no findings)
that must yield no diagnostic and no error, and finding-bearing output whose expected diagnostics
are asserted field by field. A parser that returns `[]` for everything fails the second; a
format change that turns findings into nothing is caught when the fixture is re-recorded with the
tool bump, which the wrapper's bump procedure requires. Tests only; the module bytes do not
change and no release is needed. Plan 9's `recognized` closes the remaining gap between bumps.

---

## 3. Interfaces

```go
// internal/diagnostic
func Resolve(raw parsermanager.RawDiagnostic, source string) Diagnostic // 0→1, exclusive end; plan 5 adds the exit-aware level
// internal/tooling
type Extraction string // parsed-clean | parsed-findings | parser-unavailable | parse-failed | truncated | none
type ProcessState string; type FileState string
func (e *Executor) parseFileDiagnostics(ctx, proc *ProcessResult, task Task, workingDir string, stdout, stderr []byte, exitCode int)
type TaskStartCallback func(taskID, toolName, relativeDir string)
type FileProgressCallback func(taskID, toolName string, fileIndex, totalFiles int, success bool)
// internal/cache
type Seen struct{ Hash string; Size int64; ModTime, ChangeTime time.Time; Identity FileIdentity; At time.Time }
func (c *Cache) Check(file, tool string, op Operation, seen Seen, enabled bool) bool
func (c *Cache) AfterLint(file, tool string, seen Seen, unchanged bool, enabled bool) error
// internal/cache: const cacheSemantics = "c1v1" (after PR 4; "c2v1" after PR 3)
```

Documentation: `website/docs/guides/architecture/caching.md` (C1, C2, the semantics component,
the one-time cold cache); `guides/architecture/parsers.md` (the position contract, the extraction
outcomes, `ParseFailed`, `--no-parse` as display-only, the removed "arrive in a later phase"
note); `reference/cli-commands.md` (`--no-parse`, the `:<seq>` suffix in the events contract);
`task gen:llms-docs`. `CLAUDE.md` "Product Stage": one entry, "every cache is cold once after
the upgrade; files with findings of an exit-0 tool are no longer cached".

---

## 4. Work breakdown

**PR 1 — positions and paths.** §2.1: `Resolve` fixes, absolute `File`, single-file stamping;
plan 1's S8 golden changes; `runner_diagnostic_test.go` updated.

**PR 2 — extraction outcome, `ParseFailed`, display-only `--no-parse`, fixtures.** §2.2 and
§2.8 (the Rust fixtures ride along; no module release).

**PR 3 — C2.** §2.3 with the `markPassed` reproduction as a regression test and the `c2v1`
semantics component.

**PR 4 — C1, D7, `c1v1`, the module hash in the verdict identity.** §2.4 and §2.5, with the
performance measurement in the PR description. Separate from PR 3 so each cache change cools the
cache and is measured alone (index §4.3).

**PR 5 — `Processes` and `FileResults`.** §2.6, including the removal of `FormatEdits`.

**PR 6 — task and process identity.** §2.7; JSON-L goldens regenerated once.

Order within the stack is the order above; PRs 1–2 and 5–6 do not touch the cache and can be
reviewed while 3–4 are measured.

---

## 5. Verification

- `Resolve` table: 0 row, 0 col, end before start, missing end, `nil` positions.
- A relative `File` from a tsc-shaped batch resolves against the working directory; eslint's
  absolute `File` is left alone; a file-less diagnostic in a one-file batch is stamped, in a
  two-file batch it is not.
- Extraction: a parse error → `parse-failed`, `ParseFailed`, no pass, one warning per tool per
  run; an unknown parser key → `parser-unavailable`, no pass, one warning; a module that fails
  to load → one warning naming the count; `--no-parse` prints raw output and still records by the
  parsed rule.
- The `markPassed` reproduction: A on X, B on Y, revert to X, B runs. An entry recorded before
  `c2v1` misses.
- A tool that exits 0 with a parsed diagnostic records neither a per-file pass nor a verdict; a
  diagnostic naming a path outside the process blocks every pass of that process; one spelled
  `./x` for the process's `x` matches; a fix operation of a parsed tool still records its pass
  from `Success`; an entry recorded before `c1v1` misses; a verdict recorded with one `parsers`
  hash misses after the hash changes.
- The post-run probe re-hashes only when identity or times are inconclusive; a file rewritten
  during the run records nothing; a failed hash records nothing.
- `Processes` has one entry per spawned process with its own files, exit and outcome; a per-file
  task with one clean and one failing file yields two processes, one `parsed-clean` and one
  `parsed-findings`; a batch task split into two chunks yields two; `FileResults` has one entry
  per planned file with `State` `ran`/`cached`/`not-started`/`cancelled`/`setup-failed` in a
  mixed run under fail-fast, and `verdict-hit` for every unit member on a verdict hit; a
  `WholeUnit` task lists its unit members; per-file edits are per file.
- Two tasks of one tool in one directory have distinct op ids; chunk events carry their own task's
  id; plan 1's `AssertChains` passes without exceptions.
- Warm and cold `check` timings before and after PR 4, on this repository and on the large
  benchmark; `go test ./test/cli/ -count=2`; `go test -race ./internal/tooling/ ./internal/cache/`.

---

## 6. Risks

- **Uncached findings cost time.** If PR 4's measurement shows a regression that matters, the
  fallback is to keep C1 and add the finding-fingerprint replay to the per-file entry as a
  separate decision; the "pass only for gating findings" variant is rejected (it reopens the
  silent gap).
- **Parser drift between tool bumps.** The finding-bearing fixtures catch a parser that stops
  finding anything when they are re-recorded; between bumps a changed format still reads as
  `parsed-clean` for a v1 module. Plan 9's ABI v2 `recognized` closes that for report-style
  parsers.
- **Golden churn.** Op ids and positions change once each; both are reviewed against plan 1's
  baseline.

---

## 7. Non-goals

- No column-unit conversion (plan 5 declares the unit; plan 6's `internal/textpos` converts).
- No severity policy changes (plan 5).
- No fallback parsing (plan 9), no report model (plan 6).
- No editor lane, no `publishDiagnostics`, no diagnostics in the cache.
- No change to `--explain=json`.
