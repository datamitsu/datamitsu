# Plan 7: GitHub and agent output — annotations, step summary, Markdown, `--output agent`

**Status:** ready for implementation. Plan 7 of `2026-09-26-unified-results.md`; implements D2
(the `github` mode; plan 10 adds `azure` and `teamcity`), D16, the agent side of D19, R10, and
the argv audit of R11. No decisions open.
**Date:** 2026-09-26.
**Depends on:** plan 6 (the model, completeness, masking, fingerprints, `report render`), plan 5
(`visibleFindings`), plan 4 (tools no longer print their own annotations under
`GITHUB_ACTIONS`), plan 1 (the harness strips the CI variables). **Unblocks:** plan 10 (Azure and
TeamCity reuse the selection and the escaping module).
**Related:** `internal/runner/runner.go` (results block, `printFailedExecution`,
`formatDiagnosticRelativeTo`), `internal/report`, `internal/engine` (`initFacts`),
`internal/facts`, `internal/configcache/key.go` (`Facts`), `cmd/{check,fix,lint}.go`,
`internal/env`, `internal/runtimeconfig`, `internal/clitest/run.go` (`strippedKey`),
`config/config.d.ts`, `config/src/prompts/datamitsu-agent-guide.md`,
`config/src/prompts/datamitsu-config-author-guide.md`, `.github/workflows/pr-checks.yml`.

> **Why this exists.** On GitHub a finding printed as `::error file=…,line=…::msg` becomes an
> annotation in the Checks tab and on the changed line of a pull request, with no token and no
> extra step. Today the core prints none; some tools print their own when they see
> `GITHUB_ACTIONS`, others do not, and the ten-per-type limit is spent by whichever tool prints
> first. An agent running `datamitsu check` reads frames, colours and progress bars that were
> designed for a person. Both consumers want the same model rendered differently, and the model
> now exists.

---

## 0. The idea in one paragraph

Detect the CI once (`internal/cienv`) and hand the answer to config JS as `facts().ci`. After the
last operation, select the annotations GitHub can show — ten per type, touched files first,
spread across files — escape them exactly as the runner parses them, keep every line of tool
output inside one `::stop-commands::` region so no tool can inject a command, and put everything
that did not fit into the step summary as Markdown. Add `--output agent`: one line per visible
finding, no frames, no colour, one summary line per operation — the same findings a person sees,
chosen by the same function, in the shape an agent reads.

---

## 1. Grounding — verified facts

- The GitHub runner reads stdout and stderr of every step line by line; a line starting with
  `::` is a workflow command. Parameters `file`, `line`, `col`, `endLine`, `endColumn`, `title`;
  message escaping `%`→`%25`, `\r`→`%0D`, `\n`→`%0A`, parameters additionally `:`→`%3A`,
  `,`→`%2C`; columns are dropped without a line or when `line != endLine`; a relative `file` is
  taken from the repository root; messages are cut by the runner at 4096 characters; ten
  annotations per type per step, the first ten kept (owner's research of 2026-09-26 against
  `actions/runner`; the per-type buckets were confirmed by both reviewers).
- `::stop-commands::<token>` … `::<token>::` suspends command processing; problem matchers are
  applied to every line regardless. `actions/setup-go` registers the matcher `go`;
  `actions/setup-node` registers `tsc`, `eslint-stylish` and `eslint-compact`. `::remove-matcher`
  removes a matcher for the rest of the **job**, so a later `go build` or `tsc` step would lose
  its annotations too — it is not used.
- `printFailedExecution` (`runner.go:1389-1440`) prints every raw line and every parsed line
  with the prefix `  │  ` (`:1424-1433`); the anchored `go`, `tsc` and `eslint-stylish` patterns
  cannot match a prefixed line; `eslint-compact` (`^(.+):\sline\s(\d+),\scol…`) still can, with
  the prefix inside its file group. `formatDiagnosticRelativeTo` (`:1321-1331`) interpolates the
  parsed message as is, newlines included.
- `$GITHUB_STEP_SUMMARY` is a file the step appends Markdown to, up to 1 MiB per step; several
  processes in one step share it.
- `actions/checkout` on `pull_request` checks out `refs/pull/N/merge` at depth 1: the merge
  commit's first parent (`HEAD^1`) is the **base** branch tip and its second the PR head; with
  `fetch-depth: 1` neither parent exists locally; `origin/<base>` does not exist at all. On
  `push`, `GITHUB_EVENT_PATH` names a JSON with `.before`, which may be many commits back.
- The blackbox harness strips only `CI`, `TERM`, `NO_COLOR`, `GOCOVERDIR` and `DATAMITSU_*`
  (`internal/clitest/run.go:128-135`); plan 1 extends the list by hand (`GITHUB_ACTIONS`,
  `TF_BUILD`, `TEAMCITY_VERSION`, the agent markers, the colour forcers). `GITHUB_STEP_SUMMARY`,
  `GITHUB_SHA`, `GITHUB_EVENT_NAME`, `GITEA_ACTIONS` and the other vendor variables still pass.
- `facts()` serializes `facts.Facts` through `DeterministicValue` (`engine.go:150-160`); the
  config-eval cache key mirrors a subset in `configcache.Facts` (`key.go:75-85`) and hashes the
  whole environment separately. A field derived from the environment adds no new key input.
- In JSON-L mode the event stream is stderr and stdout is contractually clean; `--report …=-`
  (plan 6) and `--explain=json` reserve stdout for a document. `uievent` has a `log` event with
  levels `debug|info|warn|error`.
- The agent guide tells agents to use `datamitsu check` and `exec`; nothing about output modes.
  This repository's CI runs `pnpm dm lint` (`pr-checks.yml:167-168`) with `contents: read` only
  (`:294`).

---

## 2. Design

### 2.1 `internal/cienv` and `facts().ci` (D16)

```go
package cienv
type Info struct {                 // the D16 shape; this is what config JS sees
    Vendor   string // github | gitlab | azure | teamcity | buildkite | bitbucket | jenkins | circleci | gitea | generic | ""
    IsCI     bool
    IsPR     bool
    SHA, Ref, BaseRef, PRNumber string
}
type Runtime struct {              // Go-only details the renderers need; never exposed to config JS
    EventName       string // github
    Workspace       string // GITHUB_WORKSPACE, CI_PROJECT_DIR, BUILD_SOURCESDIRECTORY, …
    StepSummaryPath string // GITHUB_STEP_SUMMARY
    EventPath       string // GITHUB_EVENT_PATH
}
func Detect(getenv func(string) string) (Info, Runtime)
func Variables() []string          // every name Detect reads, sorted — the harness strips them
```

Reads the vendors' own variables (`GITHUB_*`, `GITLAB_CI`/`CI_*`, `TF_BUILD`/`BUILD_*`/`SYSTEM_*`,
`TEAMCITY_VERSION`, `BUILDKITE_*`, `BITBUCKET_*`, `JENKINS_URL`, `CIRCLECI`, `GITEA_ACTIONS`,
`CI`) — third-party variables in their own package, as the policy allows. Gitea and Forgejo set
`GITHUB_ACTIONS=true` as well as `GITEA_ACTIONS=true` and support neither annotations nor the
step summary; `GITEA_ACTIONS` is checked first and yields `gitea`. `generic` means `CI` is set
and no vendor matched. Table-driven tests per vendor.

`facts().ci` is `Info` with camel-cased keys, a field of `facts.Facts` filled at collection;
`configcache.Facts` is deliberately **not** extended (a comment says so): `ci` derives from the
environment, which the key already hashes whole. Both `config.d.ts` copies and the wrapper's fork
declare it, and the config-author guide gets a "Recent changes" entry. It replaces the wrapper's
`isCI` (which reads `CI` only and misses Azure and TeamCity) — the wrapper change is its own PR
at checkpoint R2.

**Harness.** `clitest.strippedKey` strips every name of `cienv.Variables()` (plus the agent
markers and colour forcers of plan 1) and a test in `internal/clitest` pins that the two lists
agree, so a golden recorded in CI cannot differ from a local one and a test never appends to a
real job summary.

### 2.2 `--annotations` (D2)

```text
--annotations <auto|github|off>     default auto
DATAMITSU_ANNOTATIONS=<mode>        env twin; the flag wins; an invalid value is exit 2
```

- `auto` is `github` when `cienv.Vendor == "github"` and stdout carries no document; it is `off`
  under `--log-format jsonl`, with `--report …=-` and with `--explain=json`. An explicit
  `github` together with `--report …=-` or `--explain=json` is a usage error (exit 2); an explicit
  `github` under jsonl prints (stdout is not the stream's channel, and the workflow asked).
- Nothing is printed when no task executed (a plan-time refusal, an empty plan): the first
  command line is written only once the results block starts.
- `runtimeconfig.Effective.Annotations` holds the **requested** mode; the resolved mode is
  recorded in `Run.Exports` as `{format: "github-annotations", status: written|omitted, detail}`
  and in the `hello` event. `DATAMITSU_ANNOTATIONS` joins `environExcluded` (index §1.2,
  registered details: it changes what datamitsu prints about a run, not what a farm contains).
- Plan 10 adds `azure` and `teamcity` to the enum and to `auto`.

### 2.3 Selection (R10)

Annotations are selected **once, after the last operation**, from the whole `report.Run`:

1. Candidates: for every invocation, plan 5's `visibleFindings(proc)` — the reported findings,
   or every finding of a failed invocation whose findings all sit below its threshold, so a
   failure always explains itself — deduplicated by fingerprint across invocations. A synthetic
   finding is a candidate with its structured message and no file.
2. Touched files, GitHub only. `pull_request`: when `git rev-parse HEAD` equals `GITHUB_SHA` (the
   merge commit; a workflow that checked out `head.sha` would otherwise diff one commit of the
   branch) and `HEAD^1` resolves, `git diff --name-only HEAD^1 HEAD` — the base tip against the
   merge is the PR's change set. `push`: `.before` from the event file when it is not the null
   SHA and resolves locally, then `git diff --name-only <before> HEAD`. Otherwise the set is empty
   and one `info` line (a `log` event under jsonl) says why: `touched-file priority off: HEAD^1
not fetched (set fetch-depth: 2)`, `… HEAD is not the merge commit`, `… before commit not
fetched (set fetch-depth: 0)`. Two git calls at most, never a failure.
3. Order within each type bucket (error, warning, notice): touched files first; then round-robin
   over files (the first finding of every file, then the second, …); within a file by row,
   column, source, code, fingerprint; file-less candidates last. A total order, so the output is
   stable.
4. Budget per bucket: 10. When anything was left out, the notice bucket's first slot is
   `::notice::datamitsu: N more findings in the step summary` (`… and in <report path>` only
   when a listing report was requested), then nine findings.
5. Every candidate, selected or not, goes to the step summary (§2.5) up to its limit.

Multiple datamitsu processes in one step share GitHub's budget; the documentation says so.
Whether GitHub also caps annotations per job (about 50 by both reviewers' memory) is measured on
a live run before PR 2 merges, and the documentation states the measured value.

### 2.4 Printing

- `::error|warning|notice file=<path>,line=<row>[,col=<c>,endColumn=<e>][,endLine=<r>],title=<source(code)>::<message>`.
  `file` is root-relative with `/` and no leading `./` — always the git root, never the
  workspace: with `actions/checkout` `path: sub`, a workspace-relative path does not exist in
  the repository and the annotation misses the file. A finding outside the root gets no `file`.
  Columns only when `row == endRow` and the location's `Chars` span has `precision: exact` (code
  points; GitHub does not document a unit, and code points are what a person counts); `title`
  is `<source>(<code>)` or `<source>`; the message is masked, ANSI-free, escaped, and cut at 4096
  bytes on a rune boundary (bytes never exceed the runner's 4096 characters).
- **Command injection.** In `github` mode the whole results output of the run — raw frames,
  parsed lines, the agent tail of §2.6, everything derived from tool text — is printed inside one
  `::stop-commands::<token>` … `::<token>::` region, where the token is 32 hex characters from
  `crypto/rand` generated once per process. Annotations are printed after the closing token. A
  parsed message with a newline is therefore harmless in the frame and escaped in the annotation.
  Goldens normalize the token.
- **Matchers.** No matcher is removed (§1: job-wide side effect). Every raw and parsed line
  keeps the `  │  ` prefix in every output mode, the agent tail included in `github` mode; a unit
  test runs the four vendored matcher patterns (`go`, `tsc`, `eslint-stylish`, `eslint-compact`,
  fixtures with their upstream commit) against prefixed lines. `eslint-compact` is the accepted
  residual: it would annotate a prefixed line in eslint's compact format with a garbage path,
  which no wrapper tool prints; documented.
- Printed to stdout, once, before the closing wall-clock line.

### 2.5 Step summary and the Markdown renderer

- `--report markdown=<path>` renders: a header with the configuration name, selection and
  completeness; per operation a table of tools (status, runs, cached, findings by level); then
  findings grouped by severity then by file, `path:row:col — source(code): message`, with the
  hidden counters; then skipped and cancelled tasks; then incomplete tools with their reasons. A
  synthetic finding renders its structured message; no captured output appears (R11). The
  plan-time refusal of plan 6 §3.1 applies to `markdown` like every listing format.
- Under `cienv.Vendor == "github"` and a mode that is not explicitly `off`, the same rendering is
  appended to `$GITHUB_STEP_SUMMARY` best-effort — the summary never touches stdout, so a
  reserved stdout does not disable it. The budget is 1 MiB minus the file's current size (other
  processes append too); the rendering stops there with a footer that says how many findings
  were cut. When the variable is unset or the file cannot be written: one `warn` line (a `log`
  event under jsonl) and no exit-code change.

### 2.6 `--output agent` (D19)

```text
--output <human|agent>      default human
DATAMITSU_OUTPUT=<mode>     env twin; an invalid value is exit 2
```

`agent` prints, per operation and per invocation: one line per finding of `visibleFindings(proc)`
— the same function and the same set as `human` — as `path:row:col: <severity> <source>(<code>):
<message>` (columns omitted when unknown; CR and LF in the message rendered as the two characters
`\n`, so a record is one line); one line per skipped or cancelled task; for a failed invocation
without findings the structured line `<tool> exited <code> without parsable findings` followed by
its masked output tail (20 lines, `  │  `-prefixed, never for a `security` tool); and one summary
line `<op>: <tools> tools · <runs> runs · <failed> failed · <E> errors <W> warnings · <H>
hidden`. No frames, no colour, no progress, no banner. `--fail-fast=false` is not implied. The
closing wall-clock line of `check` stays. Plan 10 adds the changed-files lines.
`runtimeconfig.Effective.Output`; `DATAMITSU_OUTPUT` joins `environExcluded` (an agent that
exports it must not move the farm staleness key on every shell switch).

### 2.7 Documentation and dogfood

- `website/docs/guides/reports.md` (from plan 6) gains the GitHub recipe: `lint --fail-fast=false
--report sarif=…` with `upload-sarif`, `fetch-depth: 2`, what the ten annotations are and where
  the rest goes, the `if: always()` pattern; the note that CI runs `lint`, not `check` (index
  §5); the shared budget; the `eslint-compact` residual.
- `cli-commands.md`: `--annotations`, `--output`, `--report markdown`, `DATAMITSU_ANNOTATIONS`,
  `DATAMITSU_OUTPUT`, the usage errors, the `hello` event's capability list.
- The agent guide gains two lines: run checks as `datamitsu check --fail-fast=false --output
agent`; `exec` runs a tool raw, with no parsing and no reports. The config-author guide gets
  `facts().ci`.
- `.github/workflows/pr-checks.yml`: `pnpm dm lint --fail-fast=false` (annotations are `auto`), and
  `fetch-depth: 2` on that job's checkout. The SARIF upload joins in plan 8.
- R11 audit: `tool_run` and `error` events carry no argv; `--explain=json` keeps its `args` (a
  plan for local debugging, and the guide says so) — the same statement as plan 6 §3.3.
- Each PR ships the documentation of its surface (index §4.3), and `task gen:llms-docs`.

---

## 3. Interfaces

```go
// internal/cienv: Detect(getenv) (Info, Runtime); Variables() []string
// internal/facts: Facts.CI cienv.Info
// internal/report/render/github: Select(run *report.Run, touched map[string]bool) Selection; Print(w, sel); Escape*(…)
// internal/report/render/markdown
// internal/runner: Options.Annotations, Output string; the stop-commands region; the summary append; TouchedFiles(rt cienv.Runtime) (map[string]bool, reason string)
// internal/env: Annotations(), Output() (raw); internal/runtimeconfig: Effective.Annotations, Output
// internal/clitest: strippedKey uses cienv.Variables()
```

---

## 4. Work breakdown

**PR 1 — `cienv`, `facts().ci`, the harness.** §2.1 with the d.ts copies, the wrapper-fork note,
the author-guide entry, the `strippedKey` test; documentation of `facts().ci`.

**PR 2 — annotations.** §2.2–§2.4: flag and env, selection, printing, escaping, the
stop-commands region, the matcher fixtures, the touched-files diff, the stdout rules; the
`reports.md` GitHub section. Blackbox with `GITHUB_ACTIONS=true`, `GITHUB_SHA` and
`GITHUB_EVENT_NAME` set explicitly (the harness strips them): goldens with the token normalized;
unit tests for escaping, budget, round-robin, the notice slot, the diff-base decision per event;
`auto` off with `--report json=-`; `--annotations github --report json=-` exits 2. The job-cap
check on a live run, recorded in the PR.

**PR 3 — Markdown and the step summary.** §2.5 with its documentation.

**PR 4 — `--output agent`, guides, dogfood.** §2.6, the guides of §2.7, the workflow change.

---

## 5. Verification

- Escaping table (`%`, newlines, `:` and `,` in titles); columns dropped when rows differ or the
  span is not exact; path root-relative under a checkout with `path: sub` (unit test with a
  workspace above the root); a message over 4096 bytes cut on a rune boundary.
- Budget: 25 errors across five files, two of them touched → the touched files' findings come
  first, then one per remaining file, ten in all; the notice slot printed first when anything was
  left out, without the report clause when no report was requested.
- Stop-commands: a tool that prints `::error title=x::y` produces no annotation and the line
  appears prefixed inside the region; a parsed JSON diagnostic whose message contains a newline
  followed by `::error::x` produces one annotation with `%0A` and no second one.
- Matchers: the four vendored patterns match none of the prefixed `go`/`tsc`/`eslint-stylish`
  fixture lines; the `eslint-compact` residual is asserted as such.
- `--annotations auto` prints nothing without `GITHUB_ACTIONS`, nothing under `--log-format
jsonl`, nothing with `--report json=-`, and everything with `--annotations github` under jsonl;
  nothing when the plan is empty.
- Step summary: written when the variable points at a writable file; one `warn` and exit
  unchanged when it does not; the 1 MiB budget honours a pre-filled file; written under jsonl.
- `--output agent`: byte-stable golden for plan 1's S2/S5 fixtures with `--fail-fast=false`; the
  same finding set as `--output human` for an exit-1, warnings-only invocation under `failOn:
error` (both print every warning) and for an ordinary gating run; a message with a newline is
  one record.
- `facts().ci.vendor` reads `github` in a config evaluated with `GITHUB_ACTIONS=true` and
  `gitea` with `GITEA_ACTIONS=true` set as well.
- Dogfood: a PR of this repository with an injected lint failure shows the annotations and the
  summary (recorded in PR 4's description).

---

## 6. Risks

- **Double annotations** from `eslint-compact` or a future `setup-*` matcher: accepted and
  documented; the prefix defeats every anchored pattern.
- **Shallow clones** silence the touched-file priority; the info line says how to fix it.
- **Budget shared across processes** in one step cannot be coordinated; documented.

---

## 7. Non-goals

- No GitHub Checks API, no PR comments, no SARIF upload from the core (plan 8 writes the file;
  the action uploads it).
- No changed-lines filtering (D11).
- No Azure or TeamCity printing (plan 10), no HTML.
- No automatic `--output agent` from agent markers: explicit only (a person in an IDE terminal
  carries the same markers).
- No `compact` report format: `--output agent` is a terminal mode, not a renderer.
