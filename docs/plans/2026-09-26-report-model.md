# Plan 6: Report model — one record of a run, and the rules every export inherits

**Status:** ready for implementation. Plan 6 of `2026-09-26-unified-results.md`; implements D1,
D4, D9, D10 (exit 5), D12/R9, D15, R7 (report side), R8, R11, R15. No decisions open.
**Date:** 2026-09-26.
**Depends on:** plan 2 (keep-going, exit 2, cancelled tasks), plan 3 (`Processes`, `FileResults`,
`Cached`, extraction outcomes, task identity, C1), plan 5 (the gate hook, `reported`/`gates`).
**Unblocks:** plans 7, 8, 10.
**Related:** `internal/runner/runner.go` (`runSequential`, `runOperation`, the result callback),
`internal/tooling` (`ExecutionResult`, `ProcessResult`, `ExecutionPlan`, `Selection`, the gate
hook), `internal/diagnostic`, `internal/uievent`, `internal/env`, `internal/runtimeconfig`,
`internal/exitcode`, `cmd/{check,fix,lint}.go`, `cmd/root.go`, `internal/hashutil`. Plans 3–5
rewrite the code this plan integrates with, so it names symbols, not lines.

> **Why this exists.** Every consumer of a run wants the same facts: which tools ran, on which
> files, what they found, what they changed, what they did not cover, and how sure the core is
> about each of those. Today those facts are spread over `ExecutionResult`, the planner's
> `skipped[]`, the cache statistics and the terminal, and two of them do not exist (per-tool
> completeness, a stable identity for a finding). Renderers written against that would each
> re-derive them differently. This plan builds the one model, the rules that make it safe to
> export (completeness, masking, fingerprints), the `--report` machinery, the own JSON document,
> and the JSON-L events — so that plans 7 and 8 are renderers and nothing else.

---

## 0. The idea in one paragraph

As each task completes, the runner folds its processes into an accumulator; at the end of the
run the accumulator becomes `report.Run`: operations → tool runs → invocations (one per process)
→ file results and findings, plus changes, skips and cancellations, plus for every tool a
completeness verdict built from three facts (scope, execution, extraction). Every finding gets a
SHA-256 fingerprint that survives line shifts, computed inside the gate hook so that a later
baseline can use it, and carries `reported` and `gates`. Text fields are masked for secret-like
values; argv and the environment are never in the model. `--report <format>=<path>` writes the
model through a renderer, atomically, even when tools failed, and exits 5 only when a requested
file could not be written; a listing report turns fail-fast off and refuses a run narrowed at plan
time unless `--allow-partial`. `--log-format jsonl` gains one flat `diagnostic` event per reported
finding. `check` is one document. Nothing here is cached.

---

## 1. Grounding — verified facts

- After `sc.executor.Execute` in `runOperation` the runner has `[]GroupExecutionResult`, the plan
  (`Skipped`, tasks with `Coverage`), and cache statistics; it prints the results block and emits
  the per-operation `done`. The executor reports each task's result through `SetResultCallback`
  as it completes. `runSequential` loops over the operations and, from plan 2, keeps the first
  error.
- `Selection` (granularity plan §3.1) has modes `All`, `Subtree`, `Paths`, `Empty`; `--tools`
  narrows the tool set (`selectedToolsFlag`); `--file-scoped` maps to `Paths`. `Task.Coverage` is
  `complete|partial` per task; there is no run-level coverage field (the granularity plan's
  `PlanJSON.coverage` is still open per its §11c). `verdict.go` marks every file-granularity task
  `CoverageComplete`, so a complete task says nothing about the files or units the selection left
  out.
- `ExecutionResult.Command` is the full command line; from plan 3, `Processes`, `FileResults`,
  `Files`, `Cached`, `ParseFailed`, `TaskID`, extraction outcomes and `OutputTail` per process
  exist; from plan 5, the gate hook and the `reported`/`gates` flags.
- `uievent.Event` is one flat struct for every event type with every counter `omitempty`
  (granularity plan §11.4 forbids nested structures); `JSONLSink.Emit` ignores encode errors;
  in JSON-L mode human output is muted (`ui.Quiet()`) and stdout is contractually clean
  (`cli-commands.md`, global flags).
- Exit sites and codes: `cmd/root.go`'s single site; plan 2 adds `internal/exitcode` with 2 and 4.
- `internal/lsp/convert.go` converts line-based formatting edits whose columns are deliberately
  zero; it is not diagnostic-position infrastructure and stays as it is. The LSP plan's
  `position.go` (UTF-16 conversion of diagnostics) is not written yet — it lands here as
  `internal/textpos`, and the LSP plan's diagnostic adapter consumes it.
- GitHub facts (index §6): alerts live per (ref, category, tool); a tool omitted from an upload
  is untouched; the merge key is (ruleId, `primaryLocationLineHash`).

---

## 2. The model (`internal/report`)

```go
package report

const SchemaVersion = "datamitsu.report/1"

type Run struct {
    Schema      string        `json:"schema"`
    Datamitsu   Producer      `json:"datamitsu"`   // version (ldflags), configuration name (Config.DisplayName)
    StartedAt   time.Time     `json:"startedAt"`   // UTC; SOURCE_DATE_EPOCH when set
    EndedAt     time.Time     `json:"endedAt"`
    Selection   Selection     `json:"selection"`   // mode, paths (root-relative), tools filter, fileScoped, excludedTools
    FailFast    bool          `json:"failFast"`    // the value the run used (after the report-implied override)
    Complete    bool          `json:"complete"`    // §3.1
    Incomplete  []Reason      `json:"incomplete"`  // run-level reasons: tools-filter, not-narrowable, operation-skipped
    Operations  []Operation   `json:"operations"`  // fix, lint; for check both, in order (D9)
    Exports     []Export      `json:"exports"`     // format, path, status (written|failed|refused|omitted), detail
    Environment CIEnvironment `json:"ci"`          // vendor, sha, ref, baseRef, prNumber — identifiers only (plan 7 fills)
}

type Operation struct {
    Name      string    `json:"name"`      // "fix" | "lint"
    Ran       bool      `json:"ran"`       // false: skipped after a failed fix under fail-fast
    Success   bool      `json:"success"`
    Duration  Millis    `json:"durationMs"`
    Skipped   []Skip    `json:"skipped"`   // planner skips: tool, reason, detail (the facts of PlanJSON.skipped)
    Cancelled []Cancel  `json:"cancelled"` // fail-fast: tool, dir, started (bool)
    Tools     []ToolRun `json:"tools"`
}

type ToolRun struct {
    Name        string       `json:"name"`
    App         AppRef       `json:"app"`        // name, kind, version as configured — no argv
    Parser      *ParserRef   `json:"parser"`     // module name, module version, schema, parser key, columnUnit; nil without one
    FailOn      string       `json:"failOn"`     // effective threshold of the operation
    GateActive  bool         `json:"gateActive"` // false under an older module (plan 5's guard)
    Category    string       `json:"category"`   // "" | "security" (descriptor)
    Complete    bool         `json:"complete"`
    Incomplete  []Reason     `json:"incomplete"` // §3.1 vocabulary; empty when Complete
    Invocations []Invocation `json:"invocations"`
}

type Invocation struct {                 // one per ProcessResult (plan 3)
    ID          string       `json:"id"`          // process id: <TaskID>#<n>
    TaskID      string       `json:"taskId"`
    Dir         string       `json:"dir"`         // root-relative
    Scope       string       `json:"scope"`
    Granularity string       `json:"granularity"`
    Arity       string       `json:"arity"`
    Coverage    string       `json:"coverage"`    // complete | partial (task)
    WholeUnit   bool         `json:"wholeUnit"`
    State       string       `json:"state"`       // ran | cached | verdict-hit | cancelled | not-started | setup-failed
    ExitCode    *int         `json:"exitCode"`    // nil unless ran
    Success     bool         `json:"success"`
    FailureKind string       `json:"failureKind"` // "" | exit | threshold | cancelled | setup
    Duration    Millis       `json:"durationMs"`
    Extraction  string       `json:"extraction"`  // parsed-clean | parsed-findings | parser-unavailable | parse-failed | truncated | none
    Provenance  string       `json:"provenance"`  // parser | format | fallback:<f> | none
    Files       []FileResult `json:"files"`       // root-relative paths, state, success, exitCode
    Findings    []Finding    `json:"findings"`
    Changes     []Change     `json:"changes"`     // plan 10: files this invocation changed (attributed through its file set)
    OutputTail  string       `json:"outputTail,omitempty"` // §3.4; own JSON only
}

// Operation also carries, for a fix operation (plan 10):
//   Changes         []Change  // changed files no invocation's file set claims
//   ChangesObserved bool      // the git snapshots succeeded; false with ChangesReason, never an empty list meaning "nothing"
//   ChangesScope    string    // what was observed: tracked and untracked under the root, ignored and submodules excluded
//   ChangesReason   string    // why not observed: no-fix-task | no-git | snapshot-failed
// Change is {path, kind: created|modified|deleted|reverted, patch: bool}.

type Finding struct {
    Fingerprint      string   `json:"fingerprint"`      // §3.2
    FingerprintBasis string   `json:"fingerprintBasis"` // line | row | none
    Tool             string   `json:"tool"`
    Source           string   `json:"source"`
    Code             string   `json:"code,omitempty"`
    RuleURL          string   `json:"ruleUrl,omitempty"`
    Severity         string   `json:"severity"`         // error | warning | info | hint
    Reported         bool     `json:"reported"`         // at or above the threshold (plan 5)
    Gates            bool     `json:"gates"`            // made its invocation fail (plan 5)
    Kind             string   `json:"kind"`             // issue | security | synthetic
    Message          string   `json:"message"`          // masked, ANSI-free
    Location         Location `json:"location"`
    Provenance       string   `json:"provenance"`
}

type Location struct {
    Path      string `json:"path"`            // root-relative with "/"; absolute when outside the root; "" for none
    Row, EndRow int  `json:"row","endRow"`
    Col, EndCol int  `json:"col","endCol"`     // in the parser's unit, as reported
    Unit      string `json:"unit"`            // utf-8 | utf-16 | utf-32 | ""
    Chars     *Span  `json:"chars,omitempty"` // start/end in code points, computed while the file was on disk
    Bytes     *Span  `json:"bytes,omitempty"` // in UTF-8 bytes
    Utf16     *Span  `json:"utf16,omitempty"` // in UTF-16 units
    Precision string `json:"precision"`       // exact | ascii | unknown
}
```

Everything is sorted before it is written: operations in run order, tools by name, invocations
by ID, files by path, findings by (path, row, col, source, code, message), so the document is
byte-stable for one input. Column representations are precomputed at build time through
`internal/textpos` so that `report render` on another machine, or after the checkout changed,
needs no source text (`Precision: unknown` when the unit was unknown on a non-ASCII line; a
renderer then omits the column). There is no `Command` and no environment in the model (R11).

---

## 3. Rules

### 3.1 Completeness (R8)

Per tool, three facts; each failure adds a reason:

| Fact       | Complete when                                                                                                         | Reasons                                                                                  |
| ---------- | --------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| scope      | the selection is `All` (no paths, no subtree, no `--file-scoped`) and every task of the tool has `coverage: complete` | `narrowed-selection`, `partial-unit`                                                     |
| execution  | every planned task of the tool ran to the end (not cancelled, not unstarted, not platform-skipped)                    | `cancelled`, `not-started`, `platform-skip`                                              |
| extraction | every process is `parsed-clean` or `parsed-findings`, or is a cache hit that replays a parsed-clean pass              | `no-extraction`, `parser-unavailable`, `parse-failed`, `truncated`, `unparsed-cache-hit` |

A complete task in one unit says nothing about the units the selection left out, which is why
scope requires `All`. A cache hit counts as extracted only when the entry was recorded under C1
(after plan 3's `c1v1` bump every hit of a parsed lint tool is); a hit of a tool without a parser
is `unparsed-cache-hit`. A tool without any parser and without a fallback (before plan 9) is
`no-extraction`. `Run.Complete` is true when every tool run is complete, every operation ran, and
the run-level reasons are empty: `tools-filter` (with `Selection.ExcludedTools` listing the tools
`--tools` left out — the listed tools' own runs are complete), `not-narrowable` (a planner skip
for a tool that could not run under the selection), `operation-skipped`. A planner skip for
`skip: true` is neither incomplete nor a finding.

**Plan-time refusal.** When the selection is narrowed (`Subtree`, `Paths`, `Empty`, `--tools`,
`--file-scoped`) and a requested report lists findings — every format except `sarif`: `json`,
`markdown` (plan 7), `junit`, `codequality`, `checkstyle`, `rdjsonl` (plan 8), `history` (plan 10) — the run exits 2 before anything runs, unless `--allow-partial`. SARIF is not refused: it
omits incomplete tools (index §6, plan 8). `--allow-partial` allows the write and keeps every
reason in the document; it never sets `Complete`.

**Keep-going.** Any `--report` sets fail-fast off for the run — SARIF included, because its
omission rule needs complete tools to write anything. An explicitly set `true` — the flag, or
`DATAMITSU_FAIL_FAST=true` (plan 2's `set` half) — together with a report is a usage error
(exit 2): a fail-fast run cannot produce a complete listing. The value
the run used is `Run.FailFast`; `datamitsu config runtime` reports the environment's own value.

### 3.2 Fingerprint (D4, R15)

```text
input   = "dmfp1" NUL tool NUL code NUL relPath NUL lineHash NUL ordinal
lineHash = hex(sha256(line))   where line = the finding's start line with trailing whitespace and CR removed
ordinal = decimal, 0-based, position among findings with an identical (tool, code, relPath, lineHash)
          ordered by (row, col, source, message)
output  = hex(sha256(input))   64 lowercase hex characters
```

- `tool` is the tool name, `code` the rule (empty allowed), `relPath` root-relative with `/`
  (an absolute path outside the root is used as is).
- When the file cannot be read (deleted, outside the root) `lineHash` is `row:<n>` and
  `FingerprintBasis` is `row`; a finding without a path (a batch without attribution, a
  synthetic finding) uses `relPath = ""` and `lineHash = ""`, `FingerprintBasis: none`.
- The message is not an input: a reworded message must not turn every alert into "fixed + new".
- Duplicates — the same (tool, code, relPath, lineHash, row, col, source, message) from two
  processes (overlapping chunks) — collapse to one finding before ordinals are assigned.
- The tool name being first is what keeps two tools' alerts apart in GitHub (index §6).
- Golden vectors in `internal/report/fingerprint_test.go`: a finding on a line with `é`, two
  findings on one line, a deleted file, a file-less finding, Windows and POSIX spellings of one
  path — each with its expected hex.

**Where it is computed.** Inside plan 5's gate hook, per process, right after parsing: the hook
reads the process's files' lines once through `internal/textpos` (memoized per run), assigns
fingerprints and the precomputed column representations, then applies the threshold. Plan 10's
baseline needs the fingerprint before the gate decides; this ordering makes that possible. The
accumulator receives processes with fingerprints already set.

### 3.3 Masking and what never enters the model (R11)

- `Command`/argv and the environment are not in the model. `AppRef` carries the app's name, kind
  and configured version only.
- `report.Mask` runs over **every string field** at serialization (one pass over the document,
  so a new field cannot forget it), replacing every occurrence of a secret-like value with `***`.
  Values come from the host environment, the app env and the operation env, for variables whose
  name matches `*TOKEN*`, `*SECRET*`, `*PASSWORD*`, `*CREDENTIAL*`, `*_KEY` (case-insensitive) and
  whose value is at least 8 characters. ANSI sequences are already stripped (plan 4).
- Audit in this plan: `tool_run` and `error` events carry no argv (they carry `tool`, `dir`,
  `msg`); `--explain=json` keeps its `args` because it is a plan for local debugging, and the
  reports guide says so.

### 3.4 Synthetic findings and the output tail (D15)

A process that `ran`, exited non-zero, and produced no finding gets one synthetic finding:
`Kind: synthetic`, `Severity: error`, no location, `Provenance: synthetic`, `Reported: false`,
`Gates: true`, and a **structured** message: `<tool> exited <code> without parsable findings`, or
for a tool whose descriptor `category` is `security`: `<tool> failed (exit <code>); output
withheld for a security tool`. It never carries captured output. A cancelled, unstarted or
threshold-failed process gets none (its findings or its state already explain it). Synthetic
findings produce no `diagnostic` event, like the terminal and the editor.

`OutputTail` (the last 4 KiB of the masked output) is kept on the invocation for the own JSON and
for JUnit's `<error>` only, and never for a `security` tool. Every other renderer and the JSON-L
stream get structured text only.

### 3.5 `check` is one document (D9)

`runSequential` builds one `Run` across the operations; a lint skipped after a failed fix appears
as `Operation{Name: "lint", Ran: false}` and adds `operation-skipped` to the run-level reasons.
`--explain=json` keeps its two documents.

### 3.6 `--report` and exit 5 (D1, D10, R7)

```text
--report <format>=<path>[?opt=value[&opt=value…]]
                                repeatable; this plan: json; plan 7: markdown; plan 8: sarif, codequality, junit, checkstyle, rdjsonl; plan 10: history, patch
                                options are renderer-specific (plan 8: sarif's category); an unknown option is exit 2
DATAMITSU_REPORT=json=out/run.json,sarif=out/run.sarif   the env twin; a flag naming the same format wins
--allow-partial                 §3.1 (env twin DATAMITSU_ALLOW_PARTIAL)
```

- The path is mandatory; `-` means stdout, allowed for one format per run, and switches the UI to
  quiet mode as `lsp servers --json` does. Two `-`, or `-` together with an explicit
  `--annotations` mode (plan 7), is a usage error; `--annotations auto` resolves to `off` when
  stdout is reserved.
- Writes are atomic (temp file in the target directory, rename) and happen after the last
  operation, whether or not tools failed: the run that fails is the one CI uploads.
- A write failure prints `error: report <format>: <path>: <cause>` (as a `report` event in JSON-L
  mode) and exits 5 when nothing else failed; when tools failed the exit stays 1 and the line is
  still printed (R7). Every export is recorded in `Run.Exports` and as a JSON-L `report` event
  `{type: "report", format, path, status: "written"|"failed"|"refused"|"omitted", msg}`.
- An invalid value of `DATAMITSU_REPORT`, `DATAMITSU_ALLOW_PARTIAL` or `DATAMITSU_EVENTS` is a
  usage error checked in the command layer (exit 2); the `internal/env` getters return the raw
  strings. `runtimeconfig.Effective` gains `Report`, `AllowPartial`, `Events`; all three join
  `environExcluded` and are not observation-only (index §1.2, registered details).
- The `json` renderer writes the model with `json.MarshalIndent`, two-space indent.

### 3.7 JSON-L `diagnostic` events (D12, R9)

- Under `--log-format jsonl`, one flat event per finding with `Reported: true`:
  `{type: "diagnostic", op_id: <task op id>, tool, dir, file, row, col, end_row, end_col,
severity, code, source, msg, fingerprint, provenance, reported: true, gates}`. `--events
diagnostics=all` (and `DATAMITSU_EVENTS=diagnostics=all`) emits every finding. Synthetic
  findings are never emitted.
- `uievent.Event` stays flat. New fields are pointers with `omitempty` (`Reported *bool`,
  `Gates *bool`, `FindingsError *int`, …) set only on the events that carry them: `tool_run done`
  carries `findings_error`, `findings_warning`, `findings_info`, `findings_hint` (explicit zero
  through the pointer) and `cached`; nothing appears on `phase`, `chunk` or `download`.
- The stream opens with `{type: "hello", op_id: "stream", schema: "datamitsu.report/1",
events: "diagnostic,report,…"}` (a comma-separated string, not an array, to respect the
  flat-envelope rule), so a reader can tell "no diagnostic events" from "zero findings".
- `JSONLSink.Emit` records the encoder's first error; the runner checks `Failed()` at the end
  and exits 1 with the error on stdout — a broken stderr is not an unwritten artifact and does
  not use exit 5.
- `datamitsu lsp` never emits `diagnostic` events: the editor gets `publishDiagnostics`.
- Events are emitted from the result callback, when a task's processes are folded into the
  accumulator, i.e. after each tool's process ended; the documentation says "after each tool
  finishes", not "as it prints".

### 3.8 Time

`StartedAt`/`EndedAt` and every timestamp a renderer writes come from `SOURCE_DATE_EPOCH` when it
is set (the reproducible-builds convention), otherwise from the clock. Durations are real. The
blackbox harness sets `SOURCE_DATE_EPOCH` and masks durations.

### 3.9 `report render`

`datamitsu report render --input <run.json> --format <format> [--output <path>|-]` converts an
own JSON document offline through the same renderers, applying the same completeness rule (a
document without the completeness fields is treated as incomplete, never as complete). This plan
ships the command with `json` as the only target (validation and re-serialization); plan 8 adds
the rest. It is a new leaf command and gets its blackbox test under `TestContractCompletenessGate`.

---

## 4. Interfaces

```go
// internal/report
type Accumulator struct{ … }                       // fed from the result callback; Build() at the end
func (a *Accumulator) AddTask(op string, result tooling.ExecutionResult)
func (a *Accumulator) Build(cfg *config.Config, sel Selection, opts BuildOptions) *Run
func Mask(run *Run, secrets []string)              // one pass over every string field
func Fingerprint(f *Finding, line []byte) (hex, basis string)
// internal/report/render: type Renderer interface{ Name() string; Render(w io.Writer, run *report.Run, o Options) error; OmitsIncompleteTools() bool }
// internal/report/render/json
// internal/textpos: LineAt(path string, row int) ([]byte, error) (memoized per run); Spans(line []byte, col, endCol int, unit Unit) (chars, bytes, utf16 Span, precision string)
// internal/tooling: the gate hook of plan 5 receives fingerprints and spans from report's helper before deciding
// internal/uievent: TypeDiagnostic, TypeReport, TypeHello; pointer fields with omitempty; (s *JSONLSink) Failed() error
// internal/env: Report(), AllowPartial(), Events() (raw); internal/runtimeconfig: Effective.Report, AllowPartial, Events
// internal/runner: Options.Reports []ReportSpec, AllowPartial bool, Events string
// internal/exitcode: Export = 5; ExportError
// cmd: --report, --allow-partial, --events; `report render`
```

Documentation: `cli-commands.md` (a new section "Reports": the flags, the env twins, the
completeness rule and the refusal, `--allow-partial`, exit 5, `report render`; the events
contract gains `hello`, `diagnostic`, `report` and the counters); `guides/architecture/execution.md`
(the model in one paragraph); a new guide `website/docs/guides/reports.md` (what a report is, what
it never contains, completeness, fingerprints) that plans 7 and 8 extend with per-CI recipes;
`task gen:llms-docs`; `CLAUDE.md` "Hashing Policy" (D4's amendment) and a "Reports" policy
paragraph (no argv, no environment, masking, nothing cached).

---

## 5. Work breakdown

**PR 1 — model, accumulator, `json`, `--report`.** §2, §3.5, §3.6 without the refusal, §3.8,
`internal/exitcode.Export`. Blackbox: `lint --report json=out.json` on plan 1's fixtures with the
document golden (durations masked, `SOURCE_DATE_EPOCH` set); `check` as one document; exit 5 on
an unwritable path; exit 1 with a written report when a tool failed; `-` with quiet output; the
`report` event; an invalid `DATAMITSU_REPORT` exits 2.

**PR 2 — completeness and the refusal.** §3.1: reasons, `Complete`, plan-time refusal with exit
2, `--allow-partial`, the keep-going implication and its usage error. Blackbox for each narrowing
form with `--report json=`.

**PR 3 — fingerprints, `internal/textpos`, the gate-hook integration.** §3.2 and the
`Location` spans. Unit tests: the golden vectors; stable under a line inserted above; distinct for
two equal messages on one line; equal on Windows and POSIX paths; row fallback when the file is
gone; the span table (ASCII, `é`, U+1F600, tab, CRLF, BOM, clamping, unknown unit).

**PR 4 — masking, synthetic findings, `OutputTail`.** §3.3, §3.4. Unit tests with a fake secret
in the environment; the security-category text; no synthetic finding for a cancelled or
threshold-failed process.

**PR 5 — JSON-L events.** §3.7. Blackbox: `hello` first, `diagnostic` for reported findings only,
`=all` for every finding, explicit zero counters on `tool_run done` and none elsewhere, no events
from `lsp`, exit 1 on a broken stderr.

**PR 6 — `report render` and the guide.** §3.9 and the documentation of §4.

---

## 6. Verification

- The own JSON of plan 1's fixtures round-trips: `report render --format json` reproduces it
  byte for byte, on another machine and after the checkout changed.
- Completeness: `lint src/a.ts --report json=x` exits 2 before running; with `--allow-partial` it
  writes and lists `narrowed-selection`; `--tools a --report json=x` exits 2, with
  `--allow-partial` keeps tool `a` complete and lists `tools-filter` at run level; a cancelled task
  marks its tool `cancelled`; `parse-failed` marks it; a tool without a parser is
  `no-extraction`; a unit tool with a partial task is `partial-unit`.
- `lint --report json=x --fail-fast=true` and `DATAMITSU_FAIL_FAST=true lint --report json=x` exit
  2; `lint --report json=x` runs with fail-fast off (plan 1's S2 twin: both tools run) and
  `Run.FailFast` is false.
- Fingerprints as in PR 3; the tool name first (two tools, same rule, same line → two
  fingerprints); `FingerprintBasis` set.
- Masking: a value from `DATAMITSU_TEST_TOKEN=abcdefgh12` in a tool's message renders as `***`;
  `OutputTail` absent for a `security` tool and its synthetic message is the fixed text.
- Events as in PR 5; plan 1's `AssertChains` extended with `diagnostic` events attributed to their
  task.
- `go test ./test/cli/ -count=2`; `datamitsu config runtime | jq .report`.

---

## 7. Risks

- **Reading files for fingerprints and spans** costs one read per file with findings; clean files
  cost nothing. Memoized per run.
- **Value masking is best-effort.** It catches what the environment names; a secret a tool
  prints that never was in an environment variable is not caught. The documentation says so,
  `OutputTail` is off for security tools, and synthetic messages carry no output.
- **A stale report file on the same path.** When the run refuses to write, an older file may
  still be uploaded by CI. Not deleted (it is the user's file); the documentation shows
  `if: always()` together with an exit-code check.

---

## 8. Non-goals

- No external formats (plan 8), no annotations or Markdown (plan 7), no history or diff (plan 10).
- No report settings in `datamitsu.config` (D1).
- No storage of any report in a cache.
- No changed-lines filtering (D11).
