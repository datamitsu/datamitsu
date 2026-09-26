# Plan 8: Interchange formats — SARIF, JUnit, GitLab Code Quality, Checkstyle, reviewdog

**Status:** ready for implementation. Plan 8 of `2026-09-26-unified-results.md`; implements D5/R6,
D6 (the file-format rows), the format side of R8, and the SARIF requirements measured in the
index §6. No decisions open.
**Date:** 2026-09-26.
**Depends on:** plan 6 (the model with precomputed column spans, completeness, fingerprints,
`--report`, `report render`). Independent of plan 7 and of plan 9 (a report is rendered from the
model; it does not care how the findings were parsed).
**Related:** `internal/report/render/{sarif,junit,codequality,checkstyle,rdjsonl,common}`,
`cmd/report.go`, `website/docs/guides/reports.md`, `.github/workflows/pr-checks.yml`; the
throwaway repository `datamitsu/sarif-alert-lifecycle-test` (live verification).

> **Why this exists.** Every CI has a place for findings that a log line cannot reach: GitHub
> code scanning and Jenkins Warnings NG read SARIF; GitLab shows Code Quality in the merge request;
> GitLab, Jenkins, Azure, CircleCI, Buildkite and Bitbucket all read JUnit; reviewdog turns rdjsonl
> into review comments on any forge. All of them keep state or compare runs, so the one thing a
> writer must never do is claim more than the run covered. Plan 6 made that claim computable per
> tool. This plan writes the formats, SARIF first, with the rules the experiment established.

---

## 0. The idea in one paragraph

Five renderers over `report.Run`, each a pure function of the model with one format-specific
concern: SARIF writes one run per (category, tool) under `automationDetails.id = "datamitsu/"`,
omits every tool whose completeness is not established, splits at GitHub's twenty runs per file,
and puts the R15 fingerprint where GitHub matches alerts; JUnit makes a test case of every file
and fails it only when the file failed the gate; GitLab Code Quality, Checkstyle and rdjsonl are
straight mappings with the severity table of the index, and every one of them ships beside a
small completeness companion because their own shapes have no place for the claim. The listing
formats refuse a run narrowed at plan time unless `--allow-partial` (plan 6); `report render`
converts an own JSON document into any of them offline, from the precomputed coordinates, under
the same rule. SARIF is validated once live through the upload action with the exact fields it
emits.

---

## 1. Grounding — verified facts

- GitHub code scanning (index §6, measured): alert lifecycle per (ref, category,
  `tool.driver.name`); an omitted tool's alerts are untouched; two runs of one tool in one category
  are rejected; category is `automationDetails.id` up to the last `/`; the merge key is
  (`ruleId`, `partialFingerprints.primaryLocationLineHash`); known tool names are normalized.
- **The action's `category` input does not override a file's id.** `codeql-action`'s
  `populateRunAutomationDetails` (`src/upload-lib.ts`, read 2026-09-26) sets
  `run.automationDetails` only when the run has none. A file that carries `datamitsu/` keeps it
  whatever the workflow passes; index §6 is corrected in this docs PR.
- **GitHub SARIF limits** (docs, read 2026-09-26): 20 runs per file, 25 000 results per run (a
  run over the limit is cut to its "top 5 000 by severity", which would silently close the
  alerts of the rest), 25 000 rules per run, 10 MB gzipped per upload (rejected above). The
  wrapper has more than twenty lint tools.
- GitLab Code Quality reads a CodeClimate-shaped JSON array from `artifacts:reports:codequality`,
  merges the reports of several jobs, and compares the merged report of the merge-request pipeline
  with the target branch's; `fingerprint` must be unique within a report; severities are `info`,
  `minor`, `major`, `critical`, `blocker`; `location.path` must be repository-relative without
  `./`; `location` carries `lines{begin,end}` or `positions{begin{line,column},end{…}}`.
- JUnit XML has no normative schema; the shape consumers agree on is `testsuites` → `testsuite`
  (`name`, `tests`, `failures`, `errors`, `skipped`, `time`, `timestamp`) → `testcase`
  (`classname`, `name`, `time`) with `failure`, `error`, `skipped`, `system-out`. Jenkins marks a
  build unstable on one `failure`; GitLab's tests widget shows new/fixed against the base.
- Checkstyle XML: `checkstyle` → `file name` → `error line column severity message source`;
  severities `error`, `warning`, `info`. Jenkins Warnings NG computes new/fixed against a
  reference build for it, as for SARIF.
- reviewdog's rdjsonl is one `Diagnostic` JSON object per line (`message`, `location{path,
range{start{line,column}, end}}`, `severity` `ERROR|WARNING|INFO`, `source{name,url}`,
  `code{value,url}`, `suggestions`); columns are UTF-8 bytes and the range end is exclusive
  (`proto/rdf/reviewdog.proto`, confirmed by review).
- `go.mod` has no JSON Schema or XSD validator; there is no maintained pure-Go XSD validator;
  the wrapper manages neither `check-jsonschema` nor `xmllint`.
- Plan 6: `Finding.Gates`/`Reported`, `ToolRun.Complete`/`Incomplete`, `FileResult`,
  `Cancelled`, `Skipped`, masking, synthetic findings with a structured message, the precomputed
  `Location.Chars/Bytes/Utf16` spans, `report render`, the plan-time refusal, the
  `<format>=<path>[?opt=value…]` grammar, `SOURCE_DATE_EPOCH`. Plan 5: `Parser.ColumnUnit`,
  `Finding.RuleURL`, `Category`.

---

## 2. Design

### 2.1 Common rules (`internal/report/render/common`)

- **Which operation.** SARIF, Code Quality, Checkstyle and rdjsonl render the **lint** operation
  when the run has one, otherwise the fix operation (a `fix`-only run): a `check` would otherwise
  hold one tool twice, which GitHub rejects, and lint's findings are the state after the fix.
  JUnit renders every operation, one suite per `<op>/<tool>`.
- **Paths.** Root-relative with `/`; a finding outside the root keeps its cleaned absolute path
  where the format can carry one (SARIF as a `file://` URI without `uriBaseId`, Checkstyle,
  rdjsonl) and is omitted with accounting where it cannot (Code Quality).
- **Columns** come from the model's precomputed spans, never from the checkout: SARIF, Checkstyle
  and Code Quality use `Chars` (code points), rdjsonl `Bytes`. A span whose `precision` is not
  `exact` yields no column. `report render` therefore reproduces a direct `--report` byte for
  byte on any machine.
- **Severity** per the index §2.3. A finding below the operation's threshold is still written
  where the format lists findings (SARIF, CQ, Checkstyle, rdjsonl): consumers filter by level
  themselves; only JUnit's pass/fail follows the gate.
- **Synthetic findings (D15, R11).** Their structured message and nothing else: SARIF
  `invocations[].toolExecutionNotifications` (`level: error`); JUnit `<error>` with the masked
  output tail (own JSON and JUnit are the only places for it, never for a `security` tool); CQ,
  Checkstyle and rdjsonl carry one file-less entry where the shape allows it (Checkstyle under a
  `<file name="">`, rdjsonl without `location`), and CQ omits it with accounting (a path is
  required).
- **Incompleteness (R8).** SARIF omits the tool's run. Every other format is written in full and
  gets a **companion** `<path>.completeness.json` — `{schema: "datamitsu.completeness/1",
complete, incomplete: [reasons], tools: [{name, complete, incomplete}], exports}` — because a
  CodeClimate array, a Checkstyle tree and an rdjsonl stream have no field for the claim, and a
  consumer that reads the artifact alone cannot tell a clean run from a missing tool; JUnit
  carries the same facts in suite `properties` as well. One `warn` `log` line per incomplete
  tool and an `Exports` entry. The recipes say what each consumer will conclude from an
  incomplete artifact (GitLab shows a missing finding as "fixed") and that the companion is what
  a job should check before it publishes.
- Timestamps from the model (`SOURCE_DATE_EPOCH` aware); durations as measured.

### 2.2 SARIF 2.1.0

```json
{
  "version": "2.1.0",
  "$schema": "https://json.schemastore.org/sarif-2.1.0.json",
  "runs": [
    {
      "automationDetails": { "id": "datamitsu/" },
      "columnKind": "unicodeCodePoints",
      "tool": {
        "driver": {
          "name": "eslint",
          "version": "9.12.0",
          "informationUri": "…",
          "rules": [
            {
              "id": "no-unused-vars",
              "shortDescription": { "text": "no-unused-vars" },
              "helpUri": "…"
            }
          ]
        }
      },
      "invocations": [
        {
          "executionSuccessful": true,
          "exitCode": 1,
          "startTimeUtc": "…",
          "endTimeUtc": "…",
          "workingDirectory": { "uri": "packages/api" },
          "properties": { "datamitsu": { "state": "ran", "extraction": "parsed-findings" } }
        }
      ],
      "results": [
        {
          "ruleId": "no-unused-vars",
          "ruleIndex": 0,
          "level": "warning",
          "message": { "text": "…" },
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": { "uri": "src/a.ts", "uriBaseId": "%SRCROOT%" },
                "region": { "startLine": 12, "startColumn": 5, "endLine": 12, "endColumn": 9 }
              }
            }
          ],
          "partialFingerprints": {
            "primaryLocationLineHash": "<R15 hex>",
            "datamitsu/v1": "<R15 hex>"
          },
          "properties": { "provenance": "parser", "gates": true }
        }
      ]
    }
  ]
}
```

- **One run per (category, tool).** Every invocation of a tool across units and directories folds
  into one run; `invocations[]` lists them. `tool.driver.name` is the tool's configured name;
  `version` the app's configured version; `informationUri` the app's `officialUrl` when known.
  `columnKind` is `unicodeCodePoints`, which is what the `Chars` span counts (SARIF §3.14.27
  requires the declaration for text results).
- **Category.** `automationDetails.id` is `datamitsu/` (trailing slash; index §6). `--report
sarif=<path>?category=<name>` writes `<name>/`. The documentation says to set the category
  **in the file**, through that option, because the action's input cannot override it (§1); a
  matrix job passes `?category=datamitsu-${{ matrix.os }}`.
- **Omission rule (R8).** A tool whose `ToolRun.Complete` is false is not written; the run for it
  is absent, its alerts stay open on GitHub, one `warn` line names the tool and the reasons, and
  `Exports` records it. A tool with more than 25 000 results is omitted too (`too-many-results`)
  rather than cut, because a cut run closes the alerts it dropped. A file with zero runs is still
  written, with a warning; the live check records what the upload does with it.
- **File limits.** With at most twenty written runs, `sarif=<file>` writes one file. With more,
  the plan-time check knows the tool count and requires a directory: `sarif=<dir>/` writes
  `<dir>/datamitsu-<n>.sarif`, tools sorted by name and chunked by twenty, so the assignment is
  stable and every file goes to the same category (a tool absent from one upload but present in
  another of the same job is untouched by the first and updated by the second, per index §6);
  `sarif=<file>` with more than twenty tools exits 2 before anything runs with the message to use
  a directory. `upload-sarif` accepts a directory. The 10 MB gzipped cap is documented.
- **Fingerprints.** The R15 value goes into `partialFingerprints.primaryLocationLineHash`, the
  only key GitHub matches on, and is repeated under `datamitsu/v1` for readers that know our
  name. Because the tool is the first fingerprint input, two tools never share an alert.
- **Rules.** `ruleId` is the finding's `code`, or `<tool>/unknown` when empty; `rules[]` lists
  each distinct id once with `helpUri` from `RuleURL` when present; `ruleIndex` points at it.
- **Paths.** `uriBaseId: "%SRCROOT%"` with a relative `uri`; no `originalUriBaseIds` (it would
  carry the absolute root, which the model does not expose); an outside-root finding is an
  absolute `file://` URI without `uriBaseId`.
- **Level.** `error`/`warning`/`note` per the index table; `properties.gates` carries the
  threshold decision so a consumer can filter on it.
- **Documentation shipped with this renderer** (`reports.md`): the workflow needs
  `security-events: write` (this repository's workflow grants `contents: read` only); pull
  requests from forks cannot upload; private repositories need the code-scanning licence; a
  tool that is never uploaded again (removed, renamed — `driver.name` is the config key — or
  skipped by condition) keeps its alerts open forever, and `DELETE /repos/{o}/{r}/code-scanning/
analyses/{id}?confirm_delete` is the escape hatch that erases that history; `if: always()`
  together with an exit-code check so a refused write does not upload a stale file.
- **Live verification, once, before this renderer's PR merges,** through
  `github/codeql-action/upload-sarif` in `datamitsu/sarif-alert-lifecycle-test`: upload the
  renderer's output for plan 1's fixture run; upload a second run with one tool omitted and one
  finding removed; assert through the alerts API that the omitted tool's alerts stayed open and
  the removed finding's alert is `fixed`, matching on the `primaryLocationLineHash` the renderer
  wrote; upload two files of the same category with disjoint tools from one job; upload a file
  whose id is `datamitsu/` with `category: other` set on the action and confirm the file's id
  won; upload a zero-run file; upload a `check --report sarif=` document. The commands and
  responses are archived in that repository and linked from the PR.

### 2.3 JUnit XML (R6)

- `testsuites name="datamitsu"`; one `testsuite` per (operation, tool), named `<op>/<tool>`, with
  `tests`, `failures`, `errors`, `skipped`, `time`, `timestamp`, and `properties` for
  `complete`, `incomplete` reasons, `cached` count.
- One `testcase` per `FileResult`: `classname="<tool>"`, `name="<root-relative path>"`,
  `time="<seconds>"`:
  - a file with a gating finding on it → `<failure type="threshold" message="N findings at or
above failOn=<level>">` whose text lists them as `path:row:col: severity source(code): message`;
  - non-gating findings on a passing file go to `<system-out>` as text;
  - cached and clean files, including the clean members of a failed whole-unit invocation, are
    plain passes.
- A failed invocation with no gating finding on any file (a tool that exited non-zero on
  findings below the threshold, or without findings) gets **one** extra case named after the
  invocation's directory (`<dir>` or `.`): `<failure type="exit" message="exit <code>">` with its
  findings listed in the text when it has any, `<error type="exit" message="exit <code>">` with
  the masked output tail when it has none (never for a `security` tool). Nothing else fails: a
  failed tsc unit does not fail hundreds of clean member files.
- One `testcase` per cancelled or skipped task, named after the task, with
  `<skipped message="cancelled: fail-fast|platform-skip|narrowed|skip: true"/>`.
- A file-less finding of a whole-unit invocation attaches to the extra case named after the
  directory (created for it when the invocation passed).
- No `system-err` with raw output except the `<error>` case above.

### 2.4 GitLab Code Quality

Array of `{ "type": "issue", "check_name": "<source>/<code>" (or `<source>`), "description":
"<message>", "categories": ["Style"] (or `["Security"]`for`category: security`), "severity":
<index table>, "fingerprint": "<R15 hex>", "location": { "path": "<root-relative>", "positions":
{ "begin": { "line", "column" }, "end": { … } } } }` — `positions` when the `Chars` span is exact,
`lines: {begin, end}` otherwise. Findings without a path and findings outside the root are
omitted with accounting (GitLab requires a repository-relative path). Fingerprints are unique
within the file (plan 6 collapses duplicates before ordinals). Incomplete tools are kept, warned
about, and named in the companion (§2.1): omitting them would show their findings as "fixed" in
the merge-request widget.

### 2.5 Checkstyle XML

`<checkstyle version="datamitsu-<version>">`, one `<file name="<root-relative or absolute>">` per
file, one `<error line column severity message source="<source>/<code>"/>` per finding (column
omitted when not exact); a synthetic finding under `<file name="">`. Companion as §2.1. The
refusal applies (Warnings NG reference builds).

### 2.6 reviewdog rdjsonl

One line per finding: `message`, `location.path`, `location.range` (byte columns from the
`Bytes` span, exclusive end per `rdf`), `severity`, `source{name, url}`, `code{value, url}`;
`original_output` omitted; `suggestions` never written (deferred, see §6). reviewdog itself is
stateless, but R8 makes the refusal uniform for every listing format, and the companion is
written like for the others.

### 2.7 `report render` targets and recipes

`report render --format sarif|junit|codequality|checkstyle|rdjsonl` from an own JSON document;
the completeness fields of the document drive the same omission and refusal (`--allow-partial`
accepted); the coordinates come from the document, so no checkout is needed. The guide
`website/docs/guides/reports.md` gains one recipe per consumer, each shipped in the PR of its
renderer: GitHub (`upload-sarif` with `?category=`, `security-events: write`, `if: always()`,
`fetch-depth: 2`, the directory form), GitLab (`artifacts: reports: codequality:` and `junit:`,
and the companion check), Jenkins (`recordIssues` with `sarif`/`checkStyle`, `junit`), Azure
(`PublishTestResults@2`, the SARIF SAST Scans Tab extension), CircleCI (`store_test_results`),
Buildkite (Test Engine JUnit), Bitbucket (`test-results/`), reviewdog (`-f rdjsonl`). Each recipe
pairs with `lint --fail-fast=false`.

### 2.8 Validation without new runtime dependencies

- JSON formats (SARIF, CQ, rdjsonl): the schemas are vendored with source URL and version under
  the renderer's `testdata`; validation runs in tests through a **test-only** Go dependency
  (`github.com/santhosh-tekuri/jsonschema/v6`, imported from `_test.go` files only — a
  dependency decision recorded in PR 1's description for the owner). Should the owner decline,
  the fallback is structural assertions in Go plus a `check-jsonschema` step in CI.
- XML formats (JUnit, Checkstyle): no XSD validation; Go tests assert the structure (well-formed
  through `encoding/xml` decoding, required attributes, counts equal to the gate). The XSD
  Jenkins' JUnit plugin ships is linked from the renderer's doc comment for reference only.

---

## 3. Work breakdown

**PR 1 — common helpers and SARIF.** §2.1, §2.2, the companion writer, offline validation
(§2.8), goldens on plan 1's fixtures, the live verification recorded in the PR, `reports.md`'s
GitHub section; `pr-checks.yml` gains `security-events: write` for that job, `--report
sarif=lint-sarif/` and the upload step (dogfood at checkpoint R2 together with plan 7).

**PR 2 — JUnit.** §2.3 with its recipes.

**PR 3 — GitLab Code Quality.** §2.4 with its recipe.

**PR 4 — Checkstyle and rdjsonl.** §2.5, §2.6 with their recipes; rdjsonl also run through
`reviewdog -f rdjsonl -reporter local` in a gated e2e test, reviewdog being a hash-pinned managed
app of the test's own config.

**PR 5 — `report render` targets.** §2.7's command side and the offline reproduction tests.

PRs 2–4 are independent after PR 1 and can be reviewed in parallel.

---

## 4. Verification

- Every renderer: byte-stable goldens (`-count=2`) for the fixture run with `SOURCE_DATE_EPOCH`;
  validation per §2.8; `report render` of the own JSON reproduces the same bytes as the direct
  `--report`, on a machine without the checkout and after the source files changed.
- SARIF: one run per tool; `columnKind` present; a non-BMP character before the finding gives
  the code-point column; an incomplete tool omitted with a `warn` line and an `Exports` entry; a
  tool with 25 001 results omitted; `automationDetails.id` ends with `/` and `?category=`
  changes it; `primaryLocationLineHash` equals the finding's fingerprint; two tools with the same
  rule and line produce distinct fingerprints; 21 tools with `sarif=<file>` exit 2 and with
  `sarif=<dir>/` write two files with a stable split; an outside-root finding is a `file://` URI;
  `check --report sarif=` writes lint only; the live check of §2.2.
- JUnit: a clean file passes; a file with a gating finding fails with the finding in the text; a
  file with only sub-threshold findings passes with them in `<system-out>`; a failed whole-unit
  invocation with clean members fails one extra case and no member; a failed tool without
  findings gives `<error>`; cancelled tasks are `<skipped>`; the failure count matches the gate,
  not the finding count; `check` gives suites for both operations.
- CQ: unique fingerprints; severities per table; `positions` for exact spans and `lines`
  otherwise; an outside-root finding omitted and counted; a narrowed run refused (exit 2) without
  `--allow-partial` and written with it, the companion listing the reasons; an incomplete tool
  kept and named in the companion.
- Checkstyle and rdjsonl: columns per unit, dropped when not exact; rdjsonl accepted by reviewdog
  in the e2e test; companions written.

---

## 5. Risks

- **A consumer's schema drifts** (GitLab's CQ schema has versions). The vendored schema names its
  version; a failing validation is a signal, not a blocker, and the guide names the version.
- **GitHub changes the lifecycle or the limits.** The live check in PR 1 is repeatable; the
  experiment repository stays until the owner deletes it, and the check is re-run when the
  upload action is bumped.
- **JUnit consumers mark builds unstable on any failure**, which is why `<failure>` follows the
  gate and not the severity name.
- **Companions are one more file** in every artifact directory; the recipes show the glob.

---

## 6. Non-goals

- No HTTP uploads from the core; no SonarQube, GitLab SAST, Bitbucket Code Insights, Gerrit
  writers; no HTML (index §5).
- No rdjsonl `suggestions`: deferred to a plan of its own, not to plan 10 (which makes no
  promise about them either).
- No format-specific severity overrides; the table is the contract.
