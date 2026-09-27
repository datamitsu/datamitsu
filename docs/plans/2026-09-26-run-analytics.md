# Plan 10: Run analytics — history, diff and baseline, fix changes, Azure and TeamCity

**Status:** ready for implementation. Plan 10 of `2026-09-26-unified-results.md`; implements
D14/R13, the `azure` and `teamcity` modes of D2 and their rows of D6, and closes the analytics
scope with explicit completion criteria. No decisions open.
**Date:** 2026-09-26.
**Depends on:** plan 6 (the model, fingerprints computed in the gate hook, `--report`,
`Operation.Changes`), plan 7 (annotation selection and the escaping module, reused for Azure and
TeamCity), plan 5 (the gate hook), plan 3 (per-file results).
**Related:** `internal/report`, `internal/report/render/{history,azure,teamcity,patch}`,
`cmd/report.go`, `internal/runner/runner.go` (parallel-group boundaries for the git snapshot),
`internal/tooling/executor.go` (`executeGroup`, `FileResult`), `internal/gitutil`.

> **Why this exists.** A run answers "what is wrong now". Three questions need two runs or more:
> what is new since the last run (a baseline), how the numbers move over time (history), and what
> a fix operation actually changed (an agent has to re-read those files). Two CI systems remain
> that read log commands rather than files. All of it is derivable from the model; none of it
> belongs in a cache.

---

## 0. The idea in one paragraph

`--report history=<file.jsonl>` appends one summary line per run. `datamitsu report diff <a.json>
<b.json>` lists new, fixed, unchanged and unknown findings by fingerprint, per tool, and calls a
disappearance "fixed" only where the second run is complete. `--baseline <file>` makes a run gate
only on findings absent from the baseline — matched inside the gate hook before the decision is
committed, and never turning a tool's non-zero exit into a pass. A fix operation records which
files it changed by comparing an immutable git snapshot taken at every parallel-group boundary,
attributes the changes through the group's disjoint file sets, and says what it observed and
what it could not. Azure Pipelines and TeamCity get their log-command renderers with the
selection rules of plan 7 and their own injection defences.

---

## 1. Grounding — verified facts

- Plan 6's model carries fingerprints assigned in plan 5's gate hook, `Invocation.Changes` and
  `Operation.Changes` with `ChangesObserved`/`ChangesScope`, and the `patch` and `history` report
  names in its grammar. Plan 3's `FileResult` has no captured text: `textdiff.Edit`
  (`internal/textdiff/textdiff.go:36-40`) stores a range and the new text, not the removed text,
  so a unified diff cannot be rebuilt after the file was overwritten.
- `executeGroup` (`executor.go:236-289`) splits a priority group into parallel groups
  (`detectParallelGroups`) and runs them **one after another**; inside one parallel group the
  tasks have disjoint file sets. There is no other sub-structure.
- `git status --porcelain` without `-uall` collapses an untracked directory to one entry, never
  lists ignored files, and refreshes the index — with `index.lock` held by a `git commit` running
  a `pre-commit` hook that refresh fails; `--no-optional-locks` avoids it. `computeRootPath`
  (`engine.go:163-172`) falls back to the working directory when there is no git root, so a run
  without git exists.
- **Azure Pipelines** (agent sources, read 2026-09-26): `Command.cs` finds `##vso[` with
  `IndexOf` anywhere in a line, not only at its start; `CommandStringConvertor` escapes `;`→`%3B`,
  `\r`→`%0D`, `\n`→`%0A`, `]`→`%5D` and `%`→`%AZP25` (decoded by default since 2021); the agent
  keeps ten issues per type (`error`, `warning`; no `info`) per task (confirmed by review against
  `ExecutionContext.cs`). Command: `##vso[task.logissue type=…;sourcepath=…;linenumber=…;
columnnumber=…;code=…]<message>`.
- **TeamCity** (service-messages documentation, read 2026-09-26): escaping `'`→`|'`, `|`→`||`,
  `\n`→`|n`, `\r`→`|r`, `[`→`|[`, `]`→`|]`, other characters `|0xNNNN`; a message needs no
  particular position on the line; `##teamcity[disableServiceMessages]` …
  `[enableServiceMessages]` suspend parsing for the current step; `inspectionType` needs `id`,
  `name`, `description`, `category`; `inspection` needs `typeId`, `file`, optional `line`,
  `message`, `SEVERITY` in `INFO|ERROR|WARNING|WEAK WARNING`; `buildProblem` needs `description`
  and takes an `identity` of at most 60 Java-identifier characters. `TF_BUILD` and
  `TEAMCITY_VERSION` identify the vendors (plan 7's `cienv`); tools still see `TEAMCITY_VERSION`
  (D3) and some plugins switch their format on it.

---

## 2. Design

### 2.1 `history`

`--report history=<path>` appends one JSON line: `{schema: "datamitsu.history/1", startedAt,
datamitsu: {version, configuration}, ci: {vendor, sha, ref}, selection: {mode, fileScoped,
toolsFiltered}, complete, failFast, operations: [{name, ran, success, durationMs, tools: [{name,
runs, cached, failed, complete, findings: {error, warning, info, hint}}]}]}` — counts and
durations, never findings, paths or the selection's paths. The file is the user's (never under
the cache); the write is one `O_APPEND` write of one line, which is atomic on a local filesystem
and documented as best-effort on network filesystems. Validation is the typed Go struct: the
line decodes into `report.HistoryLine` and a test asserts the required keys and a stable
serialization. A trend is built outside datamitsu.

### 2.2 `report diff`, `report baseline`, `--baseline`

- **Baseline document** `datamitsu.baseline/1`: `{schema, fingerprint: "dmfp1", createdAt,
datamitsu: {version, configuration}, source: {startedAt, ci: {sha, ref}, complete, incomplete},
fingerprints: [sorted hex]}`. `datamitsu report baseline <run.json> --output <baseline.json>`
  writes it from an own report. An incomplete source is allowed with a `warn` line (fewer
  fingerprints only means fewer suppressed findings) and recorded in `source`.
- `--baseline <file>` on `lint|check` accepts a baseline document or an own report (from which
  the set is derived); another `schema` or another `fingerprint` version is exit 2. The file is
  loaded **before execution**. Inside plan 5's gate hook, after fingerprints are assigned and
  before the threshold decides, a finding whose fingerprint is in the set gets `Baselined: true`,
  `Reported: false`, `Gates: false`; the gate then sees only the rest, so a baselined error in
  an exit-0 invocation fails nothing and cancels nothing under fail-fast. The terminal prints the
  baselined count in a faint line; JUnit puts them in `<system-out>`, SARIF in
  `properties.baselined`. **The process exit code is untouched:** a tool that exited non-zero
  still fails the run. That limits what a baseline can do: eslint, golangci-lint and most
  linters exit 1 on the findings they print, so "fail only on new findings" works only for tools
  that exit 0 and gate through `failOn` — the guide says so and recommends the tool's
  exit-zero flag (`--exit-zero`, `--exit-code 0`) together with `failOn` for a baselined project.
  `Finding.Baselined` is an additive field of `datamitsu.report/1`.
- `datamitsu report diff <before.json> <after.json> [--format json|markdown]` compares **per
  tool** and only reports with the same `fingerprint` version: `new` (in after only), `unchanged`,
  `moved` (same fingerprint, another row), `fixed` (in before only, **and** the tool is complete
  in after with the selection `All`), otherwise `unknown` with the after-report's reasons; a tool
  present in one report only is `unobserved` with every finding listed. Exit 0; the output is the
  document.

### 2.3 `Changes` of a fix operation (R13)

- **Scope.** `ChangesScope` states what is observed: tracked and untracked files under the root,
  ignored files excluded, submodule contents excluded, a rename reported as `deleted` plus
  `created`. Outside that scope nothing is claimed.
- **Snapshot.** `git --no-optional-locks status --porcelain=v2 -z -uall` (inherits
  `GIT_INDEX_FILE` when a hook set it) plus the XXH3 of every dirty file's content, stored in an
  **immutable** map that the executor's content memo never replaces. One snapshot before the
  first parallel group that holds a fix task, one at every parallel-group boundary after it, one
  after the last — also after a failure or a cancel (a formatter may have written part of its
  files). Compared over the **union** of before and after paths: `created` (dirty only after),
  `modified` (hash moved), `deleted`, `reverted` (dirty before, clean after — a formatter that
  restored HEAD's content). When git is absent, the root is not a repository, or a snapshot
  fails: `ChangesObserved: false` with the reason, never an empty list that means "nothing".
- **Attribution.** A changed file inside the file set of a task of the parallel group that ran
  between the two snapshots belongs to that invocation (`Invocation.Changes`); any other file
  belongs to the operation (`Operation.Changes`, plan 6). The file sets of one parallel group
  are disjoint, so attribution is exact within a group.
- **Patches.** `FileResult.Patch` is a unified diff captured **at apply time**, while both
  versions exist, for stdout-mode formatters — and only when `--report patch=` was requested
  (`Options.CapturePatches`), because patches hold source text. `--report patch=<path>` writes
  them in invocation order, masked; a file formatted by two sequential formatters has two hunks in
  order. In-place formatters have no patch (the executor never sees both versions); their files
  appear in `Changes` with `patch: false`.
- **Consumers:** `--output agent` prints `fix changed N files:` with the paths (the agent must
  re-read them) and `fix changes not observed: <reason>` otherwise; Markdown and the step
  summary get a "changed files" section; the own JSON carries everything.
- Cost: one `git status` per parallel-group boundary on a warm index; measured on the large
  benchmark; skipped entirely (with `ChangesObserved: false`, reason `no-fix-task`) when the
  operation contains no fix task.

### 2.4 Azure and TeamCity (D2, D6)

- `--annotations azure`: `##vso[task.logissue type=error|warning;sourcepath=<root-relative>;
linenumber=<row>;columnnumber=<chars col>;code=<source(code)>]<message>` with plan 7's selection
  (touched files from `git diff --name-only origin/<SYSTEM_PULLREQUEST_TARGETBRANCH>...HEAD` when
  that ref resolves, else off with the reason), the index's severity table (`info`/`hint`
  omitted), ten per type; the property escaping of §1 and `%`→`%AZP25` in the message; `auto`
  resolves to `azure` under `TF_BUILD`. **Injection:** the agent parses `##vso[` anywhere in a
  line, so every tool-derived line the runner prints (frames, parsed lines, the agent tail) has
  `##vso[` rewritten to `##vso [` in `azure` mode; the rewrite is documented.
- `--annotations teamcity`: `inspectionType` once per (tool, code) with the rule URL as
  description when known, `inspection` per finding with the four severities of the table, no
  budget (TeamCity has none); `buildProblem` for a synthetic finding with `identity` derived from
  the tool name (Java-identifier characters, at most 60) and the structured message as
  `description`; `auto` under `TEAMCITY_VERSION`. **Injection:** the results output is wrapped in
  `##teamcity[disableServiceMessages]` … `[enableServiceMessages]`, and the inspection lines are
  printed after the enable line.
- Both reuse plan 7's `Select`, the escaping module gains the two escapers with tables, and the
  stdout rules of plan 7 §2.2 apply (`auto` off when stdout carries a document; an explicit mode
  with `--report …=-` is exit 2).

### 2.5 Completion criteria

This plan is complete when: history lines decode into the typed struct with the required keys;
`report diff` on plan 1's fixture runs (before/after a fixed finding) lists exactly one `fixed`,
and lists `unknown` for a cancelled tool's findings; `report baseline` followed by `lint
--baseline` silences the baselined finding, still fails on a non-zero exit, and does not cancel
the next task under default fail-fast; a fix run on a fixture that changes two files reports
both with `ChangesObserved: true` and a formatter that restores a dirty file reports `reverted`;
a run whose `git status` is made to fail reports `ChangesObserved: false`; Azure and TeamCity
goldens are byte-stable and their escaping tables pass; a tool printing `##vso[task.setvariable
…]` or `##teamcity[buildProblem …]` sets nothing. Anything beyond (HTML, Sonar, GitLab SAST,
Bitbucket, rdjsonl suggestions) is a new plan, not an extension of this one.

---

## 3. Interfaces

```go
// internal/report: HistoryLine; Baseline (datamitsu.baseline/1); LoadBaseline(path) (set, fingerprintVersion, error)
// internal/report/render/history, render/patch
// internal/report/diff: Diff(before, after *report.Run) Result   // per tool: new, fixed, unchanged, moved, unknown, unobserved
// internal/tooling: the gate hook receives the baseline set; Finding.Baselined; FileResult.Patch (captured when Options.CapturePatches)
// internal/gitutil: Snapshot(root string, env []string) (Snapshot, error) — porcelain v2, -uall, no optional locks; Snapshot.Diff(other) []Change
// internal/runner: snapshots at parallel-group boundaries of a fix operation; Options.Baseline, CapturePatches
// internal/report/render/{azure,teamcity}: Print(w, sel); escape tables; the ##vso[ rewrite; the disable/enable wrapper
// cmd: report diff, report baseline (leaf commands with blackbox tests); --baseline; --annotations azure|teamcity
```

Documentation, shipped per PR: `cli-commands.md` (`--report history|patch`, `report diff`,
`report baseline`, `--baseline` with its limitation, `--annotations azure|teamcity`);
`guides/reports.md` (baselines: when to regenerate, the exit-zero recommendation; the Azure and
TeamCity recipes with their injection notes; changed files for agents); the agent guide (re-read
the files `fix changed` lists); `guides/architecture/execution.md` (the snapshot boundaries);
`task gen:llms-docs`.

---

## 4. Work breakdown

**PR 1 — `history`.** §2.1 with its documentation.

**PR 2 — `report baseline`, `--baseline`, `report diff`.** §2.2, the blackbox tests of the two
leaf commands (`TestContractCompletenessGate`), the guide section.

**PR 3 — `Changes`, the snapshot, patches.** §2.3 with the agent and Markdown consumers and
`--report patch`.

**PR 4 — Azure and TeamCity.** §2.4 with the recipes.

All four are independent of each other.

---

## 5. Verification

As §2.5, plus: `go test ./test/cli/ -count=2`; `report diff` is symmetric in its counts for two
complete reports (`new` of a→b equals `fixed` of b→a); a baseline with a foreign schema or
fingerprint version exits 2; a baseline built from an incomplete run warns and works;
`--annotations auto` picks `azure` and `teamcity` from the environment and `off` elsewhere;
Azure's `%AZP25`, `%3B`, `%0D`, `%0A`, `%5D` table and TeamCity's `|` table; the snapshot under
`GIT_INDEX_FILE` and with `index.lock` present; an untracked directory with two new files
reports two `created` entries; a patch of a file formatted twice has two hunks in order.

---

## 6. Risks

- **Baselines rot.** A committed baseline hides findings forever if nobody prunes it; `report
diff` against the current run shows what it still hides, and the guide says to regenerate it on
  purpose, not on schedule.
- **Baselines cannot silence exit codes** (§2.2): documented with the exit-zero recommendation.
- **`git status` on a huge repository** costs seconds when the index is cold, once per
  parallel-group boundary; measured on the large benchmark before PR 3 merges.
- **Azure's `##vso [` rewrite** alters a raw line by one space; documented as the price of an
  agent that parses commands anywhere on a line.

---

## 7. Non-goals

- No HTML report, no SonarQube, GitLab SAST, Bitbucket or Gerrit writers.
- No rdjsonl `suggestions` (a later plan if wanted; plan 8 makes no promise either).
- No storage of history or baselines anywhere but the paths the user names.
- No automatic baseline creation.
- No patches for in-place formatters.
