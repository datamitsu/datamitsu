# Plan 5: Severity contract — honest levels from the parsers, one threshold in the core

**Status:** ready for implementation. Plan 5 of `2026-09-26-unified-results.md`; implements D19,
D20, R3, R4 and module release 1 (descriptor fields `severities`, `url`, `category`,
`columnUnit`, `kind`; the `source`/`code` norm; the position-base audit). No decisions open.
**Date:** 2026-09-26.
**Depends on:** plan 3 (C1 — the threshold stays out of the verdict identity only because a pass
means "no finding of any level"; `ParseFailed`; `Processes`; the released module fixture of plan
1). **Unblocks:** plan 6 (the `reported`/`gates` flags of a finding), plan 9 (descriptor and
release cadence). Must not be in flight at the same time as plan 4 (both bump
`configcache.FormatVersion`).
**Related:** `parsers/datamitsu-parsers/src/{capabilities.rs,diagnostic.rs,severity.rs,tools/*.rs}`,
`internal/parsermanager/{capabilities.go,runtime.go}`, `internal/diagnostic/diagnostic.go`,
`internal/tooling/executor.go` (`parseFileDiagnostics`, the gate hook), `internal/runner/runner.go`
(frames, footer), `internal/config` (`ToolOperation.failOn`), `internal/env`,
`internal/runtimeconfig`, `config/config.d.ts`, `packaging/parsers-catalog.ts`, the wrapper
`@shibanet0/datamitsu-config` (release checkpoint R1).

> **Why this exists.** Eighteen of the ~95 parsers hard-code a severity — ten say "warning" for
> every finding (golangci-lint, markdownlint, protolint, codespell, tfsec, mdl, …), eight say
> "error" (knip, kube-linter, cue vet, …), two of them throw away a level the tool prints
> (golangci-lint `Severity`, tfsec `LOW/MEDIUM/HIGH/CRITICAL`), and a few more label arbitrary
> stderr as an error. The cause is one line in the core: a finding without a level is a Warning,
> and a failing run rendered as a page of warnings looked wrong, so parser authors fixed it in
> the parser (knip says so in its header). A per-operation threshold — "fail on warnings too" —
> is worthless while the levels are invented. This plan makes the parsers extract levels only,
> gives the core one rule for the rest, and then adds the threshold.

---

## 0. The idea in one paragraph

A parser sets a severity only from a token the tool printed and declares in its descriptor which
tokens the tool has; a finding without a token is none. The core resolves none by the process's
outcome: Error when the tool failed, Warning when it passed — the tool's exit code is the only
thing it ever said about seriousness. On top of that one rule, every operation gets `failOn`
(default `error`): a process fails when a finding at or above it exists, on top of the tool's own
exit status, never instead of it; the terminal shows the findings at or above the threshold and
counts the rest; a global `--fail-on` can only make a run stricter. Because the rule depends on
the module telling the truth, the gate is active only when the module's descriptor is at schema
2 or later, and an explicit threshold with an older module is reported once per run, not
silently ignored.

---

## 1. Grounding — verified facts

- **Hard-coded levels** (`parsers/datamitsu-parsers/src/tools/*.rs`): `Some(WARNING)` for every
  finding in `codespell`, `buildifier` (its `warnings[]` array), `tfsec`, `markdownlint_cli2`,
  `reek`, `markdownlint`, `mdl` (`mdl.rs:32-35`), `proselint`, `golangci_lint`, `protolint`,
  `textidote`; `Some(ERROR)` for every finding in `knip`, `opacheck`, `cue_fmt`, `ltrs`,
  `kube_linter`, `perlimports`; `Some(ERROR)` for **tokenless** branches — arbitrary stderr text
  in `checkstyle` (`checkstyle.rs:66`), buildifier's stderr location line (`buildifier.rs:101`),
  protolint's non-JSON or malformed output (`protolint.rs:45`, `:50`, `:103`); no severity ever in
  `checkmake`, `zsh`, `bean_check`. The doc comments say "ported from none-ls, severity fixed to
  …" for the ports; `knip.rs:21-25` states the core rationale. The crate's descriptor test of
  §2.1, not this list, is the authority on which modules are affected.
- **Levels thrown away:** golangci-lint issues carry `Severity` (empty unless the config has
  `severity:` rules); tfsec results carry `severity` with `LOW|MEDIUM|HIGH|CRITICAL`.
- **Core policy today:** `fallbackSeverity = SeverityWarning` in
  `internal/diagnostic/diagnostic.go:23-27` with the comment "an un-leveled finding should not
  silently gate a build at error severity". `Resolve(raw, source)` does not know the exit code;
  `parseFileDiagnostics` does (`executor.go:788`). `diagnostic.Severity` is `uint8`, 1–4.
- **Descriptor:** `ToolCapability{name, description, url, operations}` (`capabilities.rs:44-55`),
  `SCHEMA_VERSION = 1`; the Go mirror (`internal/parsermanager/capabilities.go:15-32`) ignores
  unknown fields and never checks the version; `runtime_test.go:104` asserts the fixture's
  `schemaVersion == 1`. `task gen:parsers-doc` runs the Go catalogue command and then
  `packaging/parsers-catalog.ts` (`Taskfile.yaml:76-85`), which renders no columns beyond the
  current fields. `RawDiagnostic` (`runtime.go:22-36`) and `Diagnostic` carry no URL.
- **`source` vs `code`:** `golangci_lint.rs` writes `source = "golangci-lint: <FromLinter>"` and
  leaves `code` empty; every SARIF `ruleId` of its findings would be the same, and the fingerprint
  of R15 would not distinguish `errcheck` from `govet`.
- **Positions:** spectral, vacuum and pylint pass 0-based columns through (`spectral.rs:84`);
  several parsers use `end_col = col + 1` for an inclusive end. Plan 3 §2.1 leaves the base and
  end conventions to the module.
- **Display:** the runner never filters by severity (`severityColor` only, `runner.go:1348`);
  findings are shown only under a failed run. **Gate:** pass/fail is the process exit status.
- **Wrapper:** eslint runs with `--quiet` (warnings never printed), hadolint with
  `failure-threshold: warning` (LSP plan §1.4).
- **Validation** checks only that `outputParser.module` exists (`validate.go:812-822`).

---

## 2. Design

### 2.1 Module release 1 — the parser side (Rust)

Additive changes only; the response ABI stays v1 (a JSON array) so the released core keeps
reading the new module, and plan 9's v2 comes later.

1. **Rule:** a parser sets `severity` only from a token the tool emitted: a level word, a numeric
   level, a `severity`/`Severity` field, or a key that is one (`errors[]`, `warnings[]`).
   Otherwise `None` — including for stderr text, decode failures and location lines that carry
   no level; exit-aware resolution (§2.2) makes those errors when the run failed. A crate test
   enforces it through the descriptor: a parser whose `severities` is empty never returns `Some`
   on any fixture, one whose `severities` is non-empty returns only values that map from that
   vocabulary, and every fixture pair of plan 3 is run through it.
2. **Changes per module:**
   - to `None`: `markdownlint`, `markdownlint_cli2`, `mdl`, `protolint` (all branches),
     `codespell`, `reek`, `proselint`, `textidote`, `perlimports`, `kube_linter`, `ltrs`, `knip`
     (its test `knip.rs:245-248` becomes "never sets severity"), `cue_fmt`, `checkstyle`'s stderr
     branches, `buildifier`'s stderr location branch;
   - read the real level: `golangci_lint` (`Severity`: `error`/`warning`/`info` → 1–3, empty →
     `None`), `tfsec` (`CRITICAL`, `HIGH` → error; `MEDIUM` → warning; `LOW` → info);
   - keep an explicit token: `zsh` (`parse error` → error), `opacheck` (`errors[]`),
     `buildifier` (`warnings[]`), `checkstyle` (SARIF `level`).
3. **Rule identity norm:** `source` is the tool name; `code` is the rule and is set whenever the
   tool prints one. `golangci_lint`: `code = FromLinter`, always and only that (a fixture pins it;
   the sub-linter's own rule id, when golangci-lint prints one, goes into the message, not into
   `code`, because `code` is a fingerprint input). `RawDiagnostic.url` (optional) carries a rule
   URL where the tool prints one (tfsec, buildifier).
4. **Position audit:** every parser reports 1-based rows and columns and an exclusive end
   (spectral, vacuum, pylint add 1; the `col + 1` idiom becomes an exclusive end or `None`), so
   plan 3's contract holds for the module as a whole.
5. **Descriptor (`ToolCapability`, schema 2):**
   ```rust
   pub(crate) severities: &'static [&'static str], // the tool's level vocabulary; empty = none
   pub(crate) column_unit: &'static str,           // "utf-8" | "utf-16" | "utf-32" | "" (LSP plan §4.5)
   pub(crate) category: &'static str,              // "" | "security"
   pub(crate) kind: &'static str,                  // "tool" now; "format" in plan 9
   ```
   `SCHEMA_VERSION` becomes 2. The `column_unit` values are measured for the Stage 1 tools of the
   LSP plan in this release, with an explicit list of unknowns in the module test.
6. Tests: fixtures per changed parser (golangci-lint with and without `Severity`, tfsec with
   four levels, knip, zsh, `cue_fmt`, checkstyle stderr, protolint malformed output, each with
   exit 0 and exit ≠ 0 variants where the branch exists); the descriptor consistency test of
   item 1; the position audit table.

The module ships through `release.yml` as usual; the wrapper bumps its hash at checkpoint R1.

### 2.2 The core's rule for a missing level (D20), and the URL

`diagnostic.Resolve(raw, source, failed bool)`: a `nil` severity becomes `SeverityError` when
`failed` (the process exited non-zero) and `SeverityWarning` otherwise; an explicit level is never
changed by the exit code. `parseFileDiagnostics` passes `exitCode != 0`. The doc comment on
`Diagnostic` replaces the none-ls/efm note with this rule and its reason: for a tool without a
level vocabulary the exit code is the only statement of seriousness it makes.

`parsermanager.RawDiagnostic` gains `URL *string` (`json:"url,omitempty"`), `diagnostic.Diagnostic`
gains `URL string`, and `Resolve` copies it; an end-to-end fixture (tfsec) proves the URL survives
WASM decoding, resolution, aggregation and the own JSON of plan 6.

The rule is correct with the released v1 module too (its hard-coded levels are explicit and stay
as they are), so this part does not wait for the module release.

### 2.3 `failOn` — the per-operation threshold

```ts
interface ToolOperation {
  /**
   * The lowest severity that fails the run: "error" (default), "warning", "info" or "hint".
   * The tool's own exit code always fails the run too; failOn only adds failures, never removes
   * them. The terminal shows findings at this severity and above.
   *
   * @example
   *   failOn: "warning";
   */
  failOn?: "error" | "warning" | "info" | "hint";
}
```

- Go: `config.Severity` (a string type with the four values) and
  `config.Severity.Level() diagnostic.Severity` for comparisons;
  `ToolOperation.FailOn config.Severity` (`json:"failOn,omitempty"`), validated;
  `config.EffectiveFailOn(op, global config.Severity) config.Severity` returns the stricter of the
  operation's value (default `error`) and the global raise. Schema checklist: validation, both
  `config.d.ts` copies, the wrapper's fork, `configcache.FormatVersion`, the author guide's "Recent
  changes".
- **Where the gate runs.** The executor gains a gate hook,
  `Executor.SetGate(func(task Task, proc *ProcessResult) GateDecision)`, called after
  `parseFileDiagnostics` for every process. This plan's implementation applies the threshold;
  plan 6 computes fingerprints inside the same hook (so plan 10's baseline can act before the
  decision is committed). `GateDecision{Failed bool; Reason string; Gating []int}` names the
  findings that gate.
- **Gate.** A process whose exit code is 0 and which has a finding with `severity.Level() <=
failOn.Level()` is marked failed with `FailureReason: FailureReasonThreshold` (new) and the
  error `"<n> findings at or above failOn=<level>"`; `FileResult.Success` is false for the files
  the gating findings belong to; under fail-fast the per-file loop stops after it as it does after
  a non-zero exit. A non-zero exit stays a failure at any threshold. The gate applies to **fix
  and lint** operations alike: the parser already runs over fix output, and a fix run that leaves
  findings above the threshold is a failed fix.
- **Flags on findings.** Every finding gets `reported` (severity at or above the effective
  threshold — what the terminal shows) and `gates` (`reported` and the gate was active for its
  process — what made it fail). Consumers that count failures read `gates`; consumers that show
  the threshold set read `reported`.
- **Not in any cache identity.** Under C1 a pass is recorded only when no finding of any level
  exists, so it is valid at every threshold (index §1.2, registered details).
- **Guard.** The gate is active for a process only when the module that parsed it reports
  `describe.schemaVersion >= 2` (the contract that makes levels honest); with an older module the
  process exit code alone decides and `gates` stays false. When any operation's effective
  threshold is not the default — from `failOn` or from the global raise — and at least one of its
  processes ran under an older module, the runner prints **one** warning per run:
  `failOn ignored for <tool>, <tool>: parser module predates the severity contract`. Neither
  silent (an explicit threshold must not vanish) nor breaking for a user whose wrapper still pins
  the old module.

### 2.4 `--fail-on` — the global raise (R4)

```text
datamitsu fix|lint|check --fail-on <error|warning|info|hint>
DATAMITSU_FAIL_ON=<level>
```

`internal/env` getter `FailOn() string` (empty when unset; the raw value), validated in the
command layer — an invalid flag or environment value is a usage error, exit 2, because the
getter's silent fallback would turn a CI typo into a missing gate. `runtimeconfig.Effective.FailOn
string` (`json:"failOn"`, empty when unset); `runner.Options.FailOn`; the executor receives the
resolved global value with the gate hook. The effective threshold of an operation is the stricter
of its `failOn` and the global value; the global value can never lower it. `DATAMITSU_FAIL_ON`
joins `environExcluded` (index §1.2, registered details) and is not observation-only. Tests in
`env_test.go` (unset, each valid value, an invalid value returned raw) and
`runtimeconfig_test.go` (default, override, key present, stable JSON).

### 2.5 Display follows the threshold (D19, R3)

- For every process the terminal prints its `reported` findings, sorted by severity, file, row,
  column, source, code. A failed process prints them in the red frame as today. A **passed**
  process can hold `reported` findings only when the guard disabled the gate (an older module);
  then, and only when the operation's effective threshold is not the default, they print in a
  yellow frame, so a disabled gate is visible for the user who asked for a threshold, and the
  output of a user who asked for nothing does not change.
- Hidden findings are counted, never dropped: the tool line carries per-level counters for the
  findings below the threshold (`· 3 warnings`), the frame ends with one faint line
  `+ 12 warnings, 3 info hidden (failOn=error)`, and the footer sums them.
- **A failure explains itself.** A failed process whose findings all sit below the threshold
  (a tool that fails on warnings, `yamllint --strict`, `eslint --max-warnings 0`) prints every
  finding; a failed process without findings prints raw output as today. There is never an
  empty red frame. This rule is one function, `visibleFindings(proc)`, that plan 7's agent mode
  calls too.
- No `--min-severity` and no `DATAMITSU_MIN_SEVERITY` (R3).

### 2.6 Breaking behaviour, named

With the default `failOn: error`, a tool that exits 0 with a finding at level `error` now fails
the run: semgrep without `--error`, trivy without `--exit-code`, any tool whose parser maps a real
"error" token while the tool itself does not gate on it. This is the intended meaning of the
threshold and is listed under "Product Stage" in `CLAUDE.md`. Before the gate merges, the wrapper
is run against it through the dev-link loop and every tool that changes outcome is listed in the
PR description; the wrapper PR at checkpoint R1 sets `failOn` where the owner wants it and drops
eslint's `--quiet` (D21, owner's call).

---

## 3. Interfaces

```go
// internal/diagnostic
func Resolve(raw parsermanager.RawDiagnostic, source string, failed bool) Diagnostic
func ResolveAll(raws []parsermanager.RawDiagnostic, source string, failed bool) []Diagnostic
type Diagnostic struct { …; URL string }
// internal/parsermanager
type RawDiagnostic struct { …; URL *string `json:"url,omitempty"` }
type ToolCapability struct { …; Severities []string `json:"severities"`; ColumnUnit string `json:"columnUnit,omitempty"`; Category string `json:"category,omitempty"`; Kind string `json:"kind,omitempty"` }
// internal/config
type Severity string; func (s Severity) Level() diagnostic.Severity
func EffectiveFailOn(op ToolOperation, global Severity) Severity
// internal/tooling
type GateDecision struct { Failed bool; Reason string; Gating []int }
func (e *Executor) SetGate(func(task Task, proc *ProcessResult) GateDecision)
const FailureReasonThreshold FailureReason = …
// ProcessResult.Diagnostics entries carry Reported and Gates (set by the hook)
// internal/env: FailOn() string (DATAMITSU_FAIL_ON, environExcluded); internal/runtimeconfig: Effective.FailOn
// internal/runner: Options.FailOn string; visibleFindings(proc) []diagnostic.Diagnostic
```

Documentation: `configuration-api.md` (`failOn` on `ToolOperation`, with the "what gates / what is
shown" sentence and the guard); `cli-commands.md` (`--fail-on`, `DATAMITSU_FAIL_ON`, the display
rules, the counters, exit 2 on an invalid value); `guides/architecture/parsers.md` (the severity
rule for parsers, the descriptor fields, the table "vocabulary / none" generated by
`gen:parsers-doc`, the position audit); `reference/parser-catalog.md` regenerated with the new
columns; the config-author guide ("Recent changes": `failOn`, the module release); `task
gen:llms-docs`; `CLAUDE.md` "Product Stage" (the breaking entry of §2.6) and the parsers policy
paragraph (a parser never invents a level).

---

## 4. Work breakdown

**PR 1 — core reads schema 2.** The Go mirror of the descriptor fields, `describe` decoding for
schemas 1 and 2, `packaging/parsers-catalog.ts` rendering the new columns, `runtime_test.go`
asserting `schemaVersion >= 1` for the current fixture and exactly 1 for the released one. No
Rust change; the released fixture and the current fixture both pass.

**PR 2 — module: honest levels, the norm, the audit, the descriptor.** §2.1 entirely (Rust), the
rebuilt `echo.wasm`, the regenerated catalogue page. The core is already able to read it.

**PR 3 — core: exit-aware resolution and the URL.** §2.2. Plan 1's goldens change where a parser
with no level (S8 gains a `checkmake`-parsed scenario through `SeedParserModule`) used to show a
Warning under a failed run.

**PR 4 — `failOn` schema and `--fail-on`.** §2.3 field/validation/d.ts/FormatVersion/guide,
§2.4 flag/env/runtimeconfig/`environExcluded`. No gate yet; the field is echoed in `--explain`.

**PR 5 — the gate.** §2.3 hook, gate, guard and warning, `FailureReasonThreshold`, the flags on
findings; the wrapper dev-link run and the list of outcome changes in the description; the
breaking entry.

**PR 6 — display.** §2.5 frames, counters, footer, `visibleFindings`; goldens.

Checkpoint R1 (release + wrapper bump) follows PR 6.

---

## 5. Verification

- Crate: the descriptor test (empty vocabulary → never `Some`; non-empty → only mapped values)
  passes for every module against both fixtures of each pair; the position audit table passes;
  fixtures for golangci-lint (with/without `Severity`), tfsec (four levels), knip, zsh,
  `cue_fmt`, checkstyle stderr, protolint malformed output.
- `Resolve`: `nil` + failed → Error; `nil` + passed → Warning; explicit level unchanged by either;
  `URL` copied.
- Descriptor decoding: schema 1 (the released module in `testdata`) and schema 2 (`echo.wasm`)
  both decode; `devtools parsers list` shows the new columns for 2 and blanks for 1.
- Gate: exit 0 + error-level finding → failed with `FailureReasonThreshold` and `gates: true`;
  exit 0 + warning with `failOn: warning` → failed; exit 0 + warning with default → passed,
  `reported: false`; exit 1 with no findings → failed at any threshold; `--fail-on=warning` raises
  an operation at `error`, an operation at `hint` is not lowered; a fix operation with a
  threshold finding fails; per-file: the failing file's `FileResult.Success` is false and the
  loop stops under fail-fast; old module (schema 1) + explicit `failOn: warning` → passed by exit
  code, `gates: false`, one warning line naming the tool; old module + `--fail-on=warning` → the
  same warning; `DATAMITSU_FAIL_ON=warnings` → exit 2.
- Display: passed process with reported findings under a disabled gate and a raised threshold →
  yellow frame; the same with the default threshold → no frame; failed with 2 errors + 5 warnings
  at default → two lines and `+ 5 warnings hidden`; failed with only warnings at default → all
  printed; `DATAMITSU_FAIL_ON=hint` shows everything and gates on it.
- `datamitsu config runtime | jq .failOn`.
- Blackbox goldens byte-stable; the wrapper dev-link run reported in PR 5.

---

## 6. Risks

- **Outcome changes in real configs** (§2.6): mitigated by the wrapper run before merge and the
  breaking entry; the guard keeps users on an old module unaffected.
- **Two modules, two answers.** A config pinned to the old module and one pinned to the new give
  different results for `failOn`; the guard makes the old one explicit in the terminal when a
  threshold was asked for, and `devtools parsers list` shows which contract a module carries.
- **Volume of warnings once eslint's `--quiet` goes** (D21): under C1 every file with a warning
  re-runs on each `check`. Measured at checkpoint R1; the owner's policy is to fix or disable
  noisy rules or raise `failOn`.

---

## 7. Non-goals

- No ABI v2 (`recognized`, `format`) and no format parsers: plan 9.
- No column conversion: the descriptor carries the unit; plan 6's `internal/textpos` converts.
- No `--min-severity`; no per-tool display override.
- No new parsers (LSP plan Stage 3 keeps its list).
