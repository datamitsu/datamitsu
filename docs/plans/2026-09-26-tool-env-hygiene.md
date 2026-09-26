# Plan 4: Tool environment hygiene — what a tool is allowed to learn from the environment

**Status:** ready for implementation. Plan 4 of `2026-09-26-unified-results.md`; implements D3,
R1 and R2. No decisions open.
**Date:** 2026-09-26.
**Depends on:** plan 1 (goldens; the harness strips the same variables). **Unblocks:** plan 7
(annotations are safe only once tools stop printing their own). Must not be in flight at the
same time as plan 5: both bump `configcache.FormatVersion` and edit the `config.d.ts` copies.
**Related:** `internal/tooling/executor.go` (`buildCommand`, `mergeEnvLayers`,
`parseFileDiagnostics`), `internal/color/color.go` (`ChildEnvHints`),
`internal/tooling/verdict.go` (`verdictIdentity`), `internal/tooling/executor.go`
(`perFileCacheTool`), `internal/config` (`ToolOperation`, validation), `config/config.d.ts` and
its embedded copy, `internal/configcache`, `website/docs/reference/configuration-api.md`,
`config/src/prompts/datamitsu-config-author-guide.md`.

> **Why this exists.** Tools read the environment to decide what to print. With
> `GITHUB_ACTIONS=true` oxlint, sqruff, pinact and editorconfig-checker v4 switch to
> `::error file=…` lines; under an agent's marker (`AI_AGENT`, `CLAUDECODE`, …) oxlint switches to
> a one-line "agent" format; with `CI` or `FORCE_COLOR` several tools colour their output even
> into a pipe. The executor hands every tool the whole environment of the datamitsu process, plus
> `FORCE_COLOR=1` and `CLICOLOR_FORCE=1` when its own colours are on. Every switch that changes the
> text a WASM parser reads makes a lenient parser read "clean", and under C1 that becomes a cache
> pass. The wrapper pins formats with flags tool by tool; that catches the present and not the
> next tool that learns to detect CI.

---

## 0. The idea in one paragraph

In `fix`, `lint` and `check` the executor strips one CI variable, the known agent markers and the
colour-forcing variables from the environment it gives a tool, sets `NO_COLOR=1` last, and
strips ANSI sequences from the captured streams before any parser sees them. `CI` and every other
vendor variable stay. A tool that needs a stripped variable gets it back through a new operation
field, `inheritEnv`, with the host's real value; `env` keeps setting fixed values. The list of what
is stripped lives in one Go file, generates its documentation page, and is tested against the
agent-detection list the tools themselves use. `exec` is untouched: it is a transparent runner and
must stay one.

---

## 1. Grounding — verified facts

- `buildCommand(ctx, cmdInfo, args, workingDir, toolOpEnv)` (`internal/tooling/executor.go:569-590`)
  merges `cmd.Environ()` (the whole process environment) → `clr.ChildEnvHints()` → app env →
  operation env. `ChildEnvHints` (`internal/color/color.go:75-93`) returns `FORCE_COLOR=1` and
  `CLICOLOR_FORCE=1` when datamitsu's own colour is enabled and the user has not set them. A host
  `FORCE_COLOR` passes through untouched, and Node's colour detection consults `FORCE_COLOR`
  before `NO_COLOR`. `exec` uses its own path (`binmanager.go:936`, `:961`, `:987`,
  `mergeExecEnv`); the language server's format lane goes through `buildCommand`.
- Nothing strips ANSI sequences from captured output before parsing; the only ANSI regex in the
  tree is the test normalizer.
- `verdictIdentity` (`internal/tooling/verdict.go:41-58`) hashes `op.Env` as sorted `KEY=value`
  pairs. The per-file cache is keyed by `perFileCacheTool` (the tool name, or a managed-config
  digest, `executor.go:706`) under the global invalidation key, which serializes the
  configuration (`cache.go:600`) — a host value is not configuration and enters neither. The
  inherited environment enters verdict guards only through the prefix allowlist of granularity
  plan §5.4 (`GO*`, `NODE_*`, `ESLINT_*`, …); `CI` and `GITHUB_*` are not in it, so stripping them
  changes no key.
- `facts().env` is the whole environment minus observation-only variables
  (`internal/facts/facts.go:136-165`) and is hashed into the config-eval cache key. This plan
  changes the children's environment only; datamitsu's own environment and everything config JS
  sees are unchanged.
- The wrapper's `datamitsu.config.base.js` contains no reference to `GITHUB_ACTIONS` except the
  `env: { GITHUB_ACTIONS: "false" }` it sets on pinact's operation to keep pinact from printing
  annotations; gitleaks, zizmor and trufflehog read `GITHUB_TOKEN`/`GH_TOKEN`, which stay.
- Measured behaviour of the wrapper's tools under these variables is in the owner's research of
  2026-09-26: only oxlint changes for agent markers; `GITHUB_ACTIONS` changes editorconfig-checker,
  oxlint, sqruff, pinact and yamllint (without `-f`); `CI` changes colour for several tools and
  the findings of knip. oxlint's detector (`agent_detection.rs`) also reads `PATH` (`.pi/agent`),
  `EDITOR` (`devin`) and `TERM_PROGRAM` (`kiro`), which no stripping can address.
- Plan 1's `BaseEnv` strips the same variables from the blackbox harness, so goldens recorded in
  CI or under an agent do not differ from local ones.

---

## 2. Design

### 2.1 What the executor does in `fix|lint|check`

Applied in `buildCommand`, in this order: strip from the OS layer; add back `inheritEnv`; layer
app env, then operation env (explicit values win, as today); finally set `NO_COLOR=1` — the one
value nothing may override.

| Group                 | Variables                                                                                                                                                                                             | Action                             |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------- |
| CI format switch      | `GITHUB_ACTIONS`                                                                                                                                                                                      | removed                            |
| agent markers, exact  | `AI_AGENT`, `AGENT`, `CLAUDECODE`, `CLAUDE_CODE`, `CLAUDE_CODE_CHILD_SESSION`, `GEMINI_CLI`, `CURSOR_AGENT`, `OPENCODE`, `AUGMENT_AGENT`                                                              | removed                            |
| agent markers, prefix | `CODEX_*`, `COPILOT_*`, `JUNIE_*`                                                                                                                                                                     | removed                            |
| colour forcing        | `FORCE_COLOR`, `CLICOLOR_FORCE` — from the host as well as datamitsu's own hints                                                                                                                      | removed; hints no longer added     |
| colour                | `NO_COLOR`                                                                                                                                                                                            | set to `1` after every other layer |
| kept on purpose       | `CI`, every other `GITHUB_*`, `GITLAB_CI`, `CI_*`, `TF_BUILD`, `TEAMCITY_VERSION`, `BUILDKITE*`, `BITBUCKET_*`, `JENKINS_URL`, `REPL_ID`, `CURSOR_TRACE_ID`, `TERM`, `PATH`, `EDITOR`, `TERM_PROGRAM` | untouched                          |

`REPL_ID` and `CURSOR_TRACE_ID` are kept because they are set for every process on Replit and in
every Cursor terminal, humans included: they are not agent markers, and stripping them would change
tools for people. `CI` is kept because tools legitimately branch on it (knip's plugin behaviour,
tools that look up a PR base) and because stripping it would make a run in CI look local to them.

**ANSI.** `parseFileDiagnostics` strips CSI sequences from both captured streams before handing
them to any parser; the raw streams are kept for the frame. This protects parsers from a tool
that colours its output regardless of `NO_COLOR` (line parsers would otherwise misread), and it
makes plan 9's fallback independent of tool colour behaviour.

The cost of `NO_COLOR=1`: the raw output of a tool without a parser, shown in a red frame, loses
its colours in a TTY. With plan 9's fallback "without a parser" is rare, and parsed output must be
colour-free anyway.

### 2.2 The list is one file

`internal/toolenv/list.go`:

```go
package toolenv

type Entry struct{ Name, Why string }

// Stripped names and prefixes. Each entry says which tool reacts to it and how; the
// documentation page is generated from this file.
var exactNames = []Entry{ {"GITHUB_ACTIONS", "editorconfig-checker, oxlint, sqruff, pinact and yamllint switch to GitHub annotations"}, … }
var prefixes   = []Entry{ {"CODEX_", "OpenAI Codex CLI marker"}, … }

func Apply(base []string, inherit []string, layers ...map[string]string) []string // strip, re-add inherit, layer, NO_COLOR last
func Stripped(name string) bool
func Entries() (exact, prefixes []Entry)
```

`task gen:toolenv-doc` renders a whole page, `website/docs/reference/tool-environment.md`, the
way `gen:parsers-doc` renders the parser catalogue (write the page, then `pnpm dm fix` on it), so
prettier owns its formatting; `configuration-api.md`'s `env` section links to it. A Go test
renders the page and compares it with the committed file after the same formatting step the
task applies, following the `llmsdocs/embed` freshness pattern.

`list_test.go` holds a snapshot of the names oxlint's `agent_detection.rs` reads
(`AI_AGENT`, `CLAUDECODE`/`CLAUDE_CODE`, `REPL_ID`, `GEMINI_CLI`, `CODEX_SANDBOX`/`CODEX_THREAD_ID`,
`COPILOT_CLI`, `OPENCODE`, `JUNIE_*`, `CURSOR_AGENT`) and asserts every name is either stripped
or in an explicit keep list with a reason (`REPL_ID`); its `PATH`/`EDITOR`/`TERM_PROGRAM`
heuristics are listed as unaddressable. The snapshot is a string constant with the upstream
commit it was taken from; updating it is a deliberate change.

### 2.3 `inheritEnv` (R1)

```ts
interface ToolOperation {
  /**
   * Host environment variables to hand to the tool even though datamitsu strips them by
   * default (GITHUB_ACTIONS, agent markers, FORCE_COLOR). Names only: the tool sees the host's
   * real value, and nothing when the host has none. Use `env` to set a fixed value instead.
   *
   * @example
   *   inheritEnv: ["GITHUB_ACTIONS"];
   */
  inheritEnv?: string[];
}
```

- Go: `ToolOperation.InheritEnv []string` (`json:"inheritEnv,omitempty"`). Validation: each name
  matches `^[A-Z_][A-Z0-9_]*$`; duplicates are an error; `NO_COLOR` and `PATH` are rejected
  (`PATH` is rejected in `env` today for the same reason: it would replace the runtime-owned
  `PATH`), and so is any `DATAMITSU_*` name (`internal/tooling` must not read datamitsu's own
  variables outside `internal/env`). `NO_COLOR` is rejected in `env` as well. A name that is not
  stripped is allowed (harmless, and future-proof against list changes).
- Per operation, not per tool: pinact's lint prints annotations and its fix does not.
- Semantics: after stripping, for each name in `inheritEnv` that exists in the host environment,
  the host `NAME=value` is added back — a present empty value is `NAME=`, an absent variable adds
  nothing. `env` is layered after that and still wins; `NO_COLOR=1` is applied last.
- **Identities.** The resolved inherited pairs (`NAME=value`, sorted) enter `verdictIdentity`
  exactly as `op.Env` does, and their XXH3 (through `internal/hashutil`) is folded into the
  per-file cache identity `perFileCacheTool` produces, next to the managed-config digest. The
  value is what changes the tool's behaviour; a name alone is not an identity (index R1). The
  environment captured for the identity is the one the process is started with.
- Schema checklist: validation, both `config.d.ts` copies (`TestConfigDTSCopiesAreByteIdentical`),
  the wrapper's d.ts fork in lockstep with the owner's go-ahead, `configcache.FormatVersion` bump
  (the encoded `Config` shape changes), a "Recent changes" entry in the config-author guide.

### 2.4 What does not change

- `datamitsu exec`: the user expects oxlint under `exec` in GitHub Actions to print its own
  `::error` lines. The difference between `exec` and `lint` is documented.
- The language server's format lane uses the same executor and gets the same hygiene; it never
  ran with `GITHUB_ACTIONS` in practice, so nothing visible changes there.
- The wrapper's format pins (`--format=default`, `--format human`) stay as insurance; the pinact
  workaround can be removed after this plan is released (release checkpoint R2 in the index).

---

## 3. Interfaces

```go
// internal/toolenv
func Apply(base []string, inherit []string, layers ...map[string]string) []string
func Stripped(name string) bool
func Entries() (exact, prefixes []Entry)
// internal/tooling/executor.go: buildCommand gains the operation's InheritEnv and calls toolenv.Apply
//   in place of ChildEnvHints + mergeEnvLayers; parseFileDiagnostics strips CSI sequences before parsing.
// internal/tooling/verdict.go: verdictIdentity appends sorted inherited NAME=value pairs.
// internal/tooling/executor.go: perFileCacheTool folds xxh3(sorted inherited pairs).
// internal/config: ToolOperation.InheritEnv []string; validateInheritEnv; env rejects NO_COLOR.
```

Documentation: `configuration-api.md` (`env`: "tools do not see `GITHUB_ACTIONS`, agent markers
or `FORCE_COLOR`; return one with `inheritEnv`", a link to the generated page; the new
`inheritEnv` record); `reference/tool-environment.md` (generated); `guides/architecture/execution.md`
(the environment a tool receives, in one paragraph); `cli-commands.md` (`exec` passes the
environment through unchanged, `fix|lint|check` do not); the config-author guide ("Recent
changes": the stripping and `inheritEnv`, `after v<latest tag>`); `task gen:llms-docs`;
`CLAUDE.md` "Product Stage": breaking entry "tools run by `fix|lint|check` no longer see
`GITHUB_ACTIONS`, agent markers or `FORCE_COLOR`, and always get `NO_COLOR=1`; use `inheritEnv`".

---

## 4. Work breakdown

**PR 1 — `inheritEnv`.** §2.3: field, validation (including the `NO_COLOR`/`PATH`/`DATAMITSU_*`
rejections and `NO_COLOR` in `env`), d.ts copies, `FormatVersion`, the two identities, the author
guide. Behaviourally a no-op until PR 2 strips something, so the documentation of PR 2 can point
at a field that exists. Unit tests: two runs differing only in an inherited value have different
verdict identities and different per-file identities; the warm per-file cache re-runs a process
when only an inherited host value changed; an absent inherited variable adds nothing.

**PR 2 — the list, the stripping, `NO_COLOR`, ANSI.** §2.1, §2.2: `internal/toolenv`,
`buildCommand`, no colour hints, host `FORCE_COLOR` stripped, `NO_COLOR=1` last, the CSI strip
before parsing, the generated page and its freshness test, the agent-detection snapshot test,
docs, the breaking entry. Blackbox: a shell tool that prints `GITHUB_ACTIONS=<v> AI_AGENT=<v>
FORCE_COLOR=<v> NO_COLOR=<v>` run with those variables set explicitly in the test environment:
the frame shows them empty/empty/empty/`1`; the same tool with `inheritEnv: ["GITHUB_ACTIONS"]`
shows the host value; `env: { NO_COLOR: "" }` fails validation; `exec` of the same app shows every
variable intact; a tool that prints an ANSI-coloured finding is parsed correctly.

---

## 5. Verification

- Unit: `Apply` strips exact names and prefixes, keeps `CI`, `GITHUB_TOKEN`, `REPL_ID`,
  `CURSOR_TRACE_ID`; removes a host `FORCE_COLOR`; sets `NO_COLOR=1` after app and operation env
  tried to set it otherwise; re-adds inherited names with host values, `NAME=` for a present
  empty value, nothing for an absent one; an explicit `env` value beats inheritance.
- Unit: the agent-detection snapshot test passes and fails when a name is removed from the list
  without a keep-list entry.
- Unit: identities as in PR 1.
- Blackbox as in PR 2; `go test ./test/cli/ -count=2`.
- The generated page equals the committed page after formatting.

---

## 6. Risks

- **A tool that legitimately needs `GITHUB_ACTIONS`.** None in the current wrapper; a third-party
  config that has one gets a breaking note and `inheritEnv`. The stripped set is deliberately
  small for this reason.
- **The agent-marker list goes stale**, and oxlint's `PATH`/`EDITOR`/`TERM_PROGRAM` heuristics
  cannot be stripped at all: a user with `.pi/agent` on `PATH` still gets oxlint's agent format.
  The snapshot test pins what oxlint reads today; the wrapper's format pins remain the second
  line of defence, and the ANSI strip plus plan 9's format sniffer catch the switched formats.
- **Colour in raw frames.** Accepted cost (§2.1).

---

## 7. Non-goals

- No stripping of `CI`, vendor variables, `PATH`, `EDITOR`, `TERM_PROGRAM`, or anything a tool
  needs to find its PR base.
- No environment allowlisting (only what is listed is passed): tools need their toolchains'
  variables, and the verdict guards already know which ones matter.
- No `DATAMITSU_TOOL_ENV_PASSTHROUGH` or any env-level opt-out (R1 is config-level).
- No change to `exec`, to the language server's policy, or to the wrapper (its pins are removed
  in a wrapper PR at checkpoint R2).
