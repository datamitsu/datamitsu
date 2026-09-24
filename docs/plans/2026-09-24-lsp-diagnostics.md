# Plan: Editor diagnostics from lint operations (`textDocument/publishDiagnostics`)

<!-- cspell:ignore WHATWG -->

**Status:** draft — awaiting owner decisions (§9).
**Date:** 2026-09-24.
**Related code:** `internal/lsp` (server, session, transport, policy, protocol), `cmd/lsp.go`,
`internal/tooling/executor.go`, `internal/cache/cache.go`, `internal/diagnostic`,
`internal/parsermanager`, `internal/runner/parser.go`, `internal/uievent`,
`parsers/datamitsu-parsers`, `editors/vscode`, and the wrapper config that ships as
`@shibanet0/datamitsu-config`.
**Builds on:** `docs/plans/2026-08-18-tool-invocation-granularity.md` (§5.5, §6 LSP policy, §11c,
§12).
**Lifts:** the granularity plan's §12 non-goal "No LSP concurrency rework and no diagnostics".
Accepting D1 confirms that. §12's reason for leaving the `diagnostics` block unoccupied still holds:
diagnostics get keys and environment variables of their own (§4.1, §4.2), and no diagnostics default
is taken from `format.*`.
**Depends on:** the LSP session work on `feat/lsp-session`, which is in progress and whose spec is
not owner-confirmed: root from `initialize`, configuration auto-reload, a reader goroutine plus one
worker, `$/cancelRequest` honoured at checkpoints, signal handling. §3.3 lists the statements of that
spec this plan amends.

> **Why this exists.** datamitsu already turns tool output into structured diagnostics: 95 WASM
> parsers, a core `Diagnostic` contract, an executor hook. The only consumer is the text printed
> under a failed CLI run; the language server loads no parser and says so. Diagnostics are harder
> than formatting in one specific way. Formatting's result is the file, which is current by
> construction. A diagnostic is a claim about a file, and the editor keeps showing it until
> something replaces it. Every silent gap in today's pipeline — a cache hit that looks like a clean
> run, a column in the wrong unit, a relative path — becomes a visible, persistent lie in the
> Problems panel, and an editor adds a gap of its own: one tool's result overwriting another's. This
> plan closes the core's gaps first, then stages the editor feature so the first release is small
> and correct.

---

## 0. The idea in one paragraph

When a file is saved, or opened unchanged, the server plans the project's **lint** operation for
that file from the repository root, as it does for formatting, and then applies an editor filter. A
separate `diagnostics` session policy decides what may run, on the same lattice as formatting: never
`repo`, and the author's `lsp: false` always wins. The run happens on a **lint lane** that
formatting never waits for. Each tool's output goes through its declared WASM parser, and positions
become 0-based UTF-16 against the bytes on disk, using a column unit the parser declares. Every
result has an **owner**: the tool plus the unit it covered whole, or the tool plus each file it was
handed. A result replaces only what the same owner published before, and only if it is newer. Files
it no longer reports are cleared, other tools' diagnostics are left alone, and the editor receives
the union per file. The cache may skip a run only when skipping cannot hide anything; that becomes
true once a pass is recorded only for a run whose every diagnostic was attributed and none to that
file, against the content the tool actually saw. A result that describes bytes no longer on disk is
dropped. No tool is killed while the session lives, and no unsaved buffer is ever linted.

---

## 1. Grounding — verified facts

Read at `main` e312cf1 unless marked otherwise. The **Session work** facts in §1.5 are from
`feat/lsp-session`, in progress. Wrapper facts come from `shibanet0/datamitsu-config` at 0e1a9cf,
except where the version this repository pins, `0.0.0-unstable.20260912.f201136`, differs (§1.7).
"Measured" means a scratch probe run during the research for this plan; none of it is in the
repository.

### 1.1 The pipeline exists; the language server wires none of it

- The executor parses through an injected `DiagnosticParser` (`internal/tooling/executor.go:73-83`,
  `SetParser` at `:128`). The runner injects one when the config declares `parsers`, unless
  `--no-parse` or `DATAMITSU_NO_PARSE` is set (`internal/runner/runner.go:270-276`,
  `internal/runner/parser.go:12-21`). Its adapter calls `ParseOutput`, then `diagnostic.ResolveAll`
  with the tool name as source (`parser.go:38-50`).
- The server builds its own executor with no parser (`internal/lsp/server.go:117`). Its package doc
  says it "deliberately implements NO diagnostics and loads NO WASM parsers"
  (`internal/lsp/protocol.go:1-6`). `--no-parse` has no effect there.
- The parser is declared per tool, not per operation (`internal/config/config.go:160-163`). It runs
  whatever the exit code, before the failure check (`executor.go:947` per file, `:1208` per batch
  chunk). Only `output: "stdout"` formatter runs are excluded (`:919`, `:1185`).
- A parser failure leaves `Diagnostics` empty, so it cannot be told apart from a clean run. It is
  logged as `log.Warn` on every failed invocation (`executor.go:800-807`). In the server every zap
  line becomes a JSON-L `log` event (granularity plan §11c), and the extension pops up the session's
  first `warn`.
- Parsers are lenient. `json_diag::extract_lenient` returns nothing when no JSON is found
  (`parsers/datamitsu-parsers/src/tools/json_diag.rs:100-127`), and line parsers skip lines they do
  not recognise. A parse therefore fails only on a WASM or host error; a change in a tool's output
  format reads as a clean run.
- An unknown parser name returns `[]`, not an error (`parsers/datamitsu-parsers/src/lib.rs:144-155`).
  Validation checks only the module reference (`internal/config/validate.go:812-820`), so a typo in
  the parser name gives silent, empty diagnostics.
- Diagnostics reach a person only under a failed run (`printFailedExecution`, `runner.go:1389`). A
  passing run's diagnostics are discarded.

### 1.2 Positions: the contract names no unit

- `Diagnostic` is 1-based, with no stated column unit and no end-column convention
  (`internal/diagnostic/diagnostic.go:45-64`).
- `Resolve`'s doc says 0 is coerced to 1 (`:69`); the code replaces only nil (`derefU32`,
  `:113-119`). Several parsers emit 0 on purpose:
  - col 0: `trivy.rs:75`, `reek.rs:75`;
  - end col 0: `rubocop.rs:77`;
  - 0: `npm_groovy_lint.rs:80`;
  - `spectral.rs` and `vacuum.rs` pass 0-based characters through; pylint is 0-based too.

  None of these is wired in the wrapper. A naive `-1` at the LSP boundary would give all of them
  negative positions.

- Column units differ per tool. Two are measured, and both belong to unit-granularity tools:
  - tsc reports **UTF-16 code units**: `a.ts(1,24)` with U+1F600 and `é` before the error. Bytes
    would give 27, code points 23.
  - golangci-lint reports **bytes**: col 25 on `\t/* ééé */ errors.New(`. Characters would give 22.

  No file-granularity tool is measured. eslint and dclint are UTF-16 and yamllint is code points by
  inference only; hadolint, actionlint and protolint were not checked; checkmake and dotenv-linter
  report rows only.

- The parser descriptor carries a name, description, URL and suggested invocations, but no unit
  (`parsers/datamitsu-parsers/src/capabilities.rs:44-55`). Go reads it through `DescribeParser`
  (`internal/parsermanager/capabilities.go:62`). The committed parser catalogue page is generated
  from it (`task gen:parsers-doc`).
- The server always advertises `positionEncoding: "utf-16"` (`server.go:626`). The only position
  conversion today refuses non-zero columns (`internal/lsp/convert.go:19-36`).

### 1.3 File paths come back in mixed forms

- A per-file run stamps its file on diagnostics that have none (`executor.go:809-813`). A batch run
  passes `""` (`:1208`) and gives the tool paths relative to its working directory
  (`makeRelativePaths`, `:1081`). The working directory is `task.ProjectPath`, falling back to the
  git root (`getWorkingDir`, `:1460-1468`).
- Measured: golangci-lint reports `Pos.Filename` relative to the run directory, and tsc, yamllint,
  cspell and dotenv-linter also report relative paths. eslint reports an absolute `filePath`.
  Nothing normalizes them. The golangci-lint measurement had the module at the run directory and no
  configuration file elsewhere, so it cannot tell "relative to the working directory" from
  golangci-lint v2's other path bases. The wrapper's lint operation passes no `--path-mode=abs`.
- The CLI hides the difference: it prints `File` relative to the result's working directory, only
  when `File` is absolute and the result does not start with `..` (`relativeToBase`,
  `runner.go:1336-1345`). A batch result whose diagnostics carry no file prints the raw output
  instead (`usableDiagnostics`, `:1372-1385`).

### 1.4 What a cache hit hides

- A pass is recorded from the exit status alone:
  - per-file entries through `updateCacheAfterSuccess` (`executor.go:724-763`, called at `:1027`,
    `:1104`, `:1121`, `:1133`), under a name that folds in a digest of the managed configs the
    operation reads, fixed before the tool starts (`perFileCacheTool`, `:700-721`);
  - unit verdicts through `recordVerdict` when `result.Success` (`:551-553`, `verdict.go:628`).

  Neither looks at `Diagnostics`, and the cache stores none.

- Three kinds of hit return `Success` with no diagnostics and no marker:
  - a verdict hit (`executor.go:523-534`);
  - a per-file task whose files are all cached (`:838-848`);
  - a batch task whose files are all cached (`:1069-1078`).

  `ExecutionResult` has no "cached" flag and no list of the files that actually ran
  (`internal/tooling/types.go:156-188`).

- **A pass hides nothing from the CLI, only from the editor.** The CLI prints diagnostics only for a
  failed run (§1.1), so a pass recorded over exit-0 findings changes nothing a CLI user sees. It
  hides them from any consumer that reads a hit as "clean".
- **Exit 0 with findings (wrapper):** hadolint runs per file, and its rendered config sets
  `failure-threshold: warning`, so `info` and `style` findings exit 0. The first run records a pass,
  and every later run hides those findings.
- **Findings that are never reported (wrapper):** eslint runs with `--quiet`, so its warnings never
  appear.
- **Tools that fail on any finding (wrapper):** tsc, golangci-lint, cspell and actionlint.
- **`markPassed` defect** (`internal/cache/cache.go:721-761`). It appends the tool to an existing
  entry without comparing the entry's `ContentHash`. It hashes only when it creates an entry, and
  only after the run. Reproduced:
  1. A passes on content X, giving `{X,[A]}`.
  2. The file becomes Y and B passes, giving `{X,[A,B]}`.
  3. The file is reverted to X: `ShouldRun(B)` is false, so B is skipped on content it never saw.

  `AfterFix` (`:482-533`) does compare hashes. The defect is not in the backlog.

- `ShouldRun` hashes the file under the cache's read lock, and only when an entry exists
  (`cache.go:407-468`). It does not use the process-wide content memo the verdict pass shares
  (`tooling.contentMemo`, `hash_memo.go`). For a read-only operation the verdict pass re-checks its
  inputs by stat after the run and re-hashes only a path whose stat is inconclusive
  (`recordVerdict`, `verdict.go:634-658`).
- **Nothing versions what an entry means.** The invalidation key is `ldflags.Version`, the config
  JSON and the selected tools (`cache.go:579-607`); every local build reports the version `dev`.
  The verdict identity carries its own prefix, `dmv1` (`verdict.go:45`).
- The CLI and the server share the per-file cache under the same invalidation key
  (`server.go:98-110`). A CLI `check` pass suppresses an editor run, and the reverse.

### 1.5 The server today, and the session work

- **Threading.** The server is single-threaded (`server.go:30-33`, loop at `:145-165`).
- **Not safe for two concurrent callers:**
  - The executor's callbacks are rewired on every request (`wireToolEvents`, `server.go:322-358`).
  - The executor keeps a per-`Execute` command-info memo on the struct (`executor.go:137-140`).
  - The planner's walk cache is unguarded (`internal/tooling/planner.go:217-236`). `SeedFiles`
    (`:196-201`) does nothing once the planner is initialized, and the walked list is private.

  The cache is internally locked (`cache.go:98`, `:104`).

- **Session work.**
  - Session state is a `session` struct (planner, BinManager, executor, cache, `fixWidenTo`,
    managed configs, tools) that a reload replaces whole. The swap closes the old session's cache at
    once (`internal/lsp/session.go`, `load`).
  - `newSession` builds a new runtime manager and BinManager on every load.
  - The loader (`lspLoader`, `cmd/lsp.go`) calls `os.Chdir` in both `Root` and `Load`. It builds its
    watch set from `ConfigChainFiles()`, a last-load-wins process global, like `resolvedRemoteURLs`
    and `configEvalNonDeterminism` (`cmd/config_loader.go:39-110`). Its comment calls the chdir safe
    "because the server serves one root and loads only on its single worker".
  - The watch set includes `.git/HEAD`, `pnpm-lock.yaml` and every auto-config candidate
    (`sourcefarm.WatchPaths`), so a branch switch or an install reloads at the next request, even
    when the evaluated config is unchanged.
  - The reader owns the document map and snapshots a document's text into each formatting job; the
    stop check reads one `active` request (`internal/lsp/transport.go`).
  - `Run` joins the worker before it returns. The first SIGTERM behaves like EOF; a second cancels
    the tools' context and exits after a 1 s grace; SIGPIPE is ignored (`cmd/lsp.go`).
- **The extension's stop.** `stop()` waits at most 2 s for `shutdown`, then always sends SIGTERM,
  and a new server may start at once (transport research §6).
- **No `didSave`.** `textDocumentSync` is `{openClose, change: full}` with no `save`
  (`server.go:627-630`, `protocol.go:254-257`), and unknown notifications are dropped
  (`server.go:596-601`). `textDocumentItem` decodes `uri` and `text`, not `version`
  (`protocol.go:186-189`).
- **No notifications from the server.** `conn` can send a reply or an error reply, and has no helper
  for a notification the server starts itself (`protocol.go:130-143`). It cannot send requests:
  `message` has no result fields (`protocol.go:47-55`).
- **Formatting's policy is `editorDecision`** (`server.go:473-527`):
  - a unit task outside the saved file's unit is dropped silently;
  - `lsp: false` vetoes, then `format.tools[name] === false`;
  - `repo` never runs;
  - a unit operation without `globs` runs only when opted in;
  - the project's `execution.widenTo.fix` caps every unit run, opted in or not: "The opt-in bypasses
    the session class, never the project" (`:517`).
- **The preflight.** `FormatFile` runs `managedconfig.CheckConfigFiles` after the editor filter and
  before `EnsureTools` (`server.go:223`). CLAUDE.md requires it before any cache is consulted, in
  the runner and the language server.
- **A running tool is never cancelled.** `executeWithWatchdog` checks only between priority groups
  (`server.go:289-295`), as granularity plan §6.2 requires, and the watchdog paragraphs of
  `vscode.md` and `cli-commands.md` promise it.
- **`diagnostics` is reserved.** `initializationOptions.diagnostics` is ignored on purpose
  (`internal/lsp/policy.go:98-99`; granularity plan §12).
- **The planner still plans repository-granularity lint for a one-file selection.** Its narrowing
  skip checks `cwd != root`, and the server plans from the root (`planner.go:406`). Only the editor
  filter keeps those tasks out. This is the open backlog item
  `docs/backlog/root-file-selection-does-not-skip-repository-verdicts.md` (`worth: yes`).
- **Measured costs on this repository** (granularity plan §11c): about 2.6 s for one eslint run, and
  152 s for a glob-less `golangci-lint fmt` over the Go module. Not measured: `golangci-lint run`,
  a whole-project tsc, parser compile and prewarm, and the server's memory with a second planner,
  executor and parser pool.

### 1.6 Real files only

- Formatting runs on the real file because temp copies and stdin break each tool's own project
  detection (`FormatFile` doc, `server.go:168-177`).
- No wrapper lint operation declares `input: "stdin"`. `stdinForOperation` would read the file from
  disk anyway (`executor.go:1676-1685`).
- So a diagnostic can only describe bytes on disk.
- VS Code keeps a UTF-8 BOM out of the buffer text (VS Code behaviour, not verified in this
  repository). A byte comparison of buffer and disk never matches for such a file. Formatting's
  `clean` check (`server.go:242-243`) has the same blind spot; that is pre-existing and not
  addressed here.

### 1.7 The wrapper's lint operations that have a parser

| tool          | scope       | granularity     | argv paths         | in the editor under this plan                 |
| ------------- | ----------- | --------------- | ------------------ | --------------------------------------------- |
| actionlint    | per-file    | file            | `{file}`           | Stage 1                                       |
| checkmake     | per-file    | file            | `{file}`           | Stage 1                                       |
| dclint        | repository  | file            | `{files}`          | Stage 1 (0e1a9cf only)                        |
| dotenv-linter | per-file    | file            | `{files}`          | Stage 1                                       |
| eslint        | per-project | file (declared) | `{files}`          | Stage 1                                       |
| hadolint      | per-file    | file            | `{file}`           | Stage 1                                       |
| protolint     | per-file    | file            | `{file}`           | Stage 1                                       |
| yamllint      | repository  | file            | `{files}`          | Stage 1                                       |
| harper-cli    | repository  | file            | `{files}`          | Stage 1 (pinned f201136 only)                 |
| vale          | repository  | file            | `{files}`          | Stage 1 (pinned f201136 only)                 |
| cspell        | per-project | unit            | `{files}`          | Stage 2a, narrowed to the saved file          |
| tsc           | per-project | unit            | none; has globs    | Stage 2a, the whole project                   |
| golangci-lint | per-project | unit            | none; **no globs** | Stage 2a, the whole module, only by D8/opt-in |

- **Which tools ship depends on the consumer's wrapper version.** At 0e1a9cf dclint has a parser,
  and harper-cli and vale are skipped. At the pinned f201136 dclint is skipped with no parser, while
  harper-cli and vale are active with parsers, on markdown globs
  (`node_modules/@shibanet0/datamitsu-config/datamitsu.config.base.js:11379-11398`,
  `:12118-12137`). In this repository Stage 1 would therefore run harper-cli and vale on every
  markdown save, and never dclint.
- Skipped in both: droast and knip (both repo).
- More than twenty active lint operations have no parser, among them ruff, oxlint, shellcheck,
  stylelint, typos and tflint.
- The `markdownlint_cli2` and `editorconfig_checker` parsers exist but throw the file name away, so
  they cannot attribute the diagnostics of a batch run. The `protolint` parser drops
  `FILE_NAMES_LOWER_SNAKE_CASE` as a temp-file false positive (`protolint.rs:76-79`), which is wrong
  for a run on the real file.

### 1.8 Events and the extension

- **Events.** `uievent.Event` is flat, and `done` carries tools, runs, failed and skipped
  (`internal/uievent/uievent.go:71-102`). `toolOpID` is run + tool + dir, so it is not unique per
  task. The code records this as a known limitation "deferred to the diagnostics phase"
  (`runner.go:1078-1092`).
- **Popups.** The extension pops up the session's first `warn` or `error` event (`progress.ts`,
  `notify`). It also shows a one-time hint for a `done` event with `op: "format"` and no runs
  (`events.ts`, `isEmptyFormat`).
- **Status bar.** `toolLabel` reads only `tool` and `dir` (`events.ts:218-221`).
- **Settings forwarded.** Only `datamitsu.format.*` is sent as `initializationOptions`
  (`options.ts:8`).
- **Output channel.** The extension hands its own log channel to the language client
  (`extension.ts:201`). A `window/logMessage` from the server would land next to the JSON-L notices
  the extension already writes there, and the client shows `window/showMessage` as a notification.
- **Rendering.** vscode-languageclient 10.1.1 renders pushed diagnostics without any extension code.
  `documentSelector` is `[{scheme:"file"}]` (`extension.ts:181`), so the server receives `didOpen`
  for every file-scheme document any extension opens, visible or not.
- **Other editors** read no JSON-L. A notice on stderr reaches a Neovim, Helix or Zed user only
  through their LSP log.

### 1.9 The reserved `lsp` entity

`LspEntry{Type: proxy|derived, App, ProjectTypes, Tool, Order}` is validated and has no behaviour
(`config.go:396-445`, `validate.go:617-665`). The reference documents `derived` as inheriting the
tool's globs and parser, with the example `hadolint: { type: "derived", tool: "hadolint" }`
(`configuration-api.md:1663-1681`). Formatting shipped without it: every fix operation is eligible
implicitly, and each operation can veto with `lsp: false` (`config.go:124-128`). No shipped config
declares an entry. The in-core d.ts copies and the wrapper's hand-maintained fork
(`src/datamitsu-config/datamitsu.config.d.ts`, unguarded by any test) declare `LspDerived`.

---

## 2. What the facts already settle

These are not decisions. Each one follows from a fact above or from a confirmed plan.

| Question                                                              | Answer                                      | Because                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| --------------------------------------------------------------------- | ------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Push (`publishDiagnostics`) or pull (3.17 `textDocument/diagnostic`)? | Push                                        | A save triggers the run, not a client request. A pull server would answer from the same store and would still need `workspace/diagnostic/refresh`, a server-to-client request; the transport cannot send those (§1.5). Every client renders push. Unit tools also report files nobody asked about.                                                                                                                                                                  |
| Negotiate `positionEncoding`?                                         | No: keep `utf-16` and convert on the server | VS Code accepts only UTF-16, so the conversion has to exist anyway. Negotiating UTF-8 for Neovim would add a second path to test with no visible gain.                                                                                                                                                                                                                                                                                                              |
| Lint unsaved buffers?                                                 | No                                          | §1.6.                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| Store diagnostics alongside a cached pass?                            | No                                          | Granularity plan §12: "The record is a boolean verdict plus its precondition, never output content." §3.7 does not need it.                                                                                                                                                                                                                                                                                                                                         |
| Kill a superseded or slow lint tool?                                  | Not while the session lives                 | Shipped: `executeWithWatchdog` "a running tool is never cancelled"; granularity plan §6.2, "Cancellation happens only at a priority-group boundary"; the watchdog paragraphs of `vscode.md` and `cli-commands.md`. Those promises protect fix tools, which write user files. Lint tools write only their own caches under `{toolCache}`. The session work cancels every tool on a second SIGTERM; what happens to a running lint tool when the session ends is D10. |
| Run repository-granularity lint in the editor?                        | Never                                       | Granularity plan §6.2. knip, droast and gitleaks answer whole-repository questions and take seconds to minutes.                                                                                                                                                                                                                                                                                                                                                     |
| Does `lsp: false` cover lint operations?                              | Yes; the published text says otherwise      | The Go field is operation-neutral: it vetoes "running this operation from the language server" (`config.go:124-128`). The published contract ties it to format-on-save and `format.*`: `config/config.d.ts:1108-1118` and its byte-identical embedded copy, `configuration-api.md:807` and `:1013-1015` ("Only fix operations run in the editor today"), and the wrapper's d.ts fork. Stage 1 rewords all of them.                                                  |
| Put the diagnostics session policy in `config.Config`?                | No                                          | Granularity plan §6.1: the whole config is hashed into the cache key, so an editor preference there would reset the shared cache.                                                                                                                                                                                                                                                                                                                                   |
| Send `version` with `publishDiagnostics`?                             | Leave it out                                | The field is optional. The diagnostics describe disk bytes, not a buffer version, and the server does not track versions (§1.5).                                                                                                                                                                                                                                                                                                                                    |

---

## 3. The model

### 3.1 Triggers

| Event                                           | Lint?                                      | Why                                                                                                                                                                                                                                                                                                                               |
| ----------------------------------------------- | ------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `didSave`                                       | yes, the saved file                        | The main trigger. Advertise `textDocumentSync.save: {includeText: false}` while diagnostics are enabled; the text is already on disk.                                                                                                                                                                                             |
| `didOpen`, buffer equal to disk                 | yes (D2)                                   | Nothing has linted the file in this session yet. A leading UTF-8 BOM on disk is ignored in the comparison (§1.6).                                                                                                                                                                                                                 |
| `didOpen`, buffer differs from disk             | no                                         | The result would describe bytes the user is not looking at, for example a dirty buffer restored by hot exit. Its first save lints it.                                                                                                                                                                                             |
| A clean-path format in which a tool ran         | yes                                        | The server rewrote the file, and the editor reloads it without sending `didSave` (`server.go:276-280`). On the dirty path nothing is enqueued: format-on-save is followed by the save's `didSave`, and a manual Format Document on a dirty buffer is linted at its next save.                                                     |
| `didChange`                                     | never                                      | §1.6.                                                                                                                                                                                                                                                                                                                             |
| Configuration reload (§3.3, L6)                 | the open documents of the owners it clears | Owners whose tool left the config, lost its eligibility, or changed its lint operation, `outputParser` or parser module are cleared. The diagnostics policy is re-resolved from the kept `initializationOptions`. A reload that changes none of that, such as one caused only by `.git/HEAD` or `pnpm-lock.yaml`, clears nothing. |
| A change outside the editor, found by the sweep | yes if open and buffer equals disk         | §3.8.                                                                                                                                                                                                                                                                                                                             |
| `didClose`                                      | no run                                     | Per-file owners withdraw their entries for the file (§3.8).                                                                                                                                                                                                                                                                       |

- **Debounce and merge.** The lane runs one job at a time. Triggers that arrive meanwhile, and in the
  150 ms after the first of them, merge into one pending job over the set of their paths, planned
  once as `Paths(P)`. Save All, and the format, save, `didSave` sequence, therefore cost one walk and
  one batch per tool. A pending job carries at most 64 paths from `didOpen`; further `didOpen`
  triggers are dropped with one `info` per session, and their first save lints them. `didSave` is
  never dropped.
- **Autosave.** `files.autoSave: afterDelay` saves about once a second while the user types, without
  running format-on-save. Per-file tools then re-run once per pending job, and a unit tool runs back
  to back, publishing after each run (§3.4).
- **A save of unchanged bytes re-runs.** Clean files already hit the per-file cache (§3.7). A file
  with findings re-runs, because its findings can depend on inputs other than its bytes: the managed
  or ejected tool config, other files that eslint's typed rules read, a `tsconfig`. Saving again is
  how a user asks for a re-check. Skipping it would cache a failure, which granularity plan §12
  refuses.

### 3.2 Which operations run

Formatting's decision function is generalized into one function that both lanes call, so the vetoes
and the lattice cannot drift apart. For each planned lint task, the first matching row wins:

| #   | Condition                                                           | Run? | Notice (once per tool and reason per session)               |
| --- | ------------------------------------------------------------------- | ---- | ----------------------------------------------------------- |
| 1   | The task's unit contains none of the job's paths                    | no   | none: other units are not this job's business               |
| 2   | The tool declares no `outputParser`                                 | no   | none (D7)                                                   |
| 3   | The operation says `lsp: false`                                     | no   | `lsp: false in config`                                      |
| 4   | `diagnostics.tools[name] === false`                                 | no   | `disabled by diagnostics.tools`                             |
| 5   | granularity `repo`                                                  | no   | `repository-wide, never in the editor`                      |
| 6   | granularity `file`                                                  | yes  |                                                             |
| 6a  | granularity `unit`, in Stage 1 only                                 | no   | `project-wide; not yet run in the editor`                   |
| 7   | granularity `unit`, no `globs`, not opted in by `diagnostics.tools` | no   | `declares no globs, so it cannot tell which files it lints` |
| 8   | `unit`, and the project's `execution.widenTo.lint` is `target`      | no   | `project policy is target`                                  |
| 9   | `unit`, opted in                                                    | yes  |                                                             |
| 10  | `unit`, and the session's `diagnostics.widenTo` is `target`         | no   | `project-wide, diagnostics policy is target`                |
| 11  | `unit`                                                              | yes  |                                                             |

- **Notice frequency.** Formatting reports what it left out on every request. Lint reports it once
  per session: saves are frequent, and a notice on every save would bury the output channel.
- **Row 5 is load-bearing.** The planner still plans repository-granularity lint for a file
  selection at the root (§1.5). The lane relies on this row, so the backlog item stays open.
- **The glob-less rule (row 7).** It is formatting's rule, for formatting's reason. The planner plans
  a glob-less operation for every file in its unit, so saving a TypeScript file in this repository
  would start `golangci-lint run` over the whole Go module. In the wrapper, that leaves Go lint
  diagnostics off until the user opts in or the wrapper adds globs (D8).
- **Many files, one tool.** A unit task whose argv carries the saved file (cspell's `{files}`)
  receives only that file (coverage `partial`), so its result is owned per file (§3.6). A unit task
  with no path on argv (tsc, golangci-lint) covers the whole unit, reports many files, and is owned
  per unit.
- **No parsers declared.** A snapshot whose config declares no `parsers` can run nothing, since
  every lint task falls under row 2. The lane then does nothing and says so once per snapshot.
  `save` stays advertised: capabilities cannot change after `initialize`, and a reload may add
  parsers.

### 3.3 The lint lane

**Why it is not the session work's single worker.** A running tool cannot be interrupted. eslint
alone takes 2.6 s here, and whole-unit tsc and golangci-lint take seconds to minutes. On one worker,
a save that arrives while a lint tool runs waits for that tool. The recommendation (D1) is a second
worker from the first stage.

Rules:

- **L1.** Formatting never waits for a lint tool. It may wait for a configuration load in progress
  (L6), which is bounded by the load.
- **L2.** The lint lane starts a job, and each priority group within a job, only while the format
  lane is idle, or once the format lane has been busy for 15 s without pause, so a hung formatter
  cannot starve diagnostics. A group that is already running finishes.
- **L3.** A clean-path format in which a tool ran enqueues a lint of that file (§3.1).
- **L4.** The lint lane never writes a user file. It never cancels a tool while the session lives.
- **L5.** A snapshot is immutable. Each job pins the snapshot it started under with a reference
  count. At a swap the old snapshot's cache is flushed; it is shut down when its last job ends.
- **L6.** Configuration loads are single-flight. One owner, `snapshotFor(ctx)` behind a mutex,
  recomputes the fingerprint, resolves the root, changes directory, loads, builds the snapshot and
  swaps the pointer. Both lanes call it at the start of every job; a lane that finds a load in
  progress waits for it and pins the result. Nothing else calls the loader, so loads still happen
  one at a time.
- **L7.** Once `shutdown`, `exit`, stdin EOF or the first SIGTERM is read, the lint lane drops its
  queued jobs and publishes nothing more. `shutdown` is answered once the format lane is idle, never
  waiting for the lint lane. A lint tool that is still running is handled by D10.

**What the lanes share.** The process working directory and the loader's globals, behind L6; the
snapshot pointer and the reload fingerprints; the execution cache; the BinManager and runtime
manager; the parser manager, used by the lint lane only; the open-documents index; the `uievent`
sink and op-id counter, both already concurrency-safe; and the `conn` writer.

What has to change to allow a second worker:

| Component                   | Today (main, then session work)                                                                          | With two lanes                                                                                                                                                                                                                                                                                                                               |
| --------------------------- | -------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Session state               | Fields on `Server`; then a `session` struct replaced whole by `load`, which closes the old cache at once | An immutable snapshot behind an atomic pointer, built only by the L6 owner, pinned per job, released as L5 says.                                                                                                                                                                                                                             |
| Loader                      | `Root` and `Load` chdir; watch set from the last-load-wins `ConfigChainFiles()`                          | Called only by the L6 owner.                                                                                                                                                                                                                                                                                                                 |
| Executor                    | One per session; callbacks rewired per request; command-info memo on the struct                          | One per lane per snapshot. Only the lint lane's executor has a parser.                                                                                                                                                                                                                                                                       |
| Planner                     | One per session; walk cache unguarded                                                                    | One per lane per snapshot. The lint planner may seed its walk from the format lane's walk of the same save: a new `Planner.WalkedFiles()` returns a copy, which the format lane leaves in a one-slot hand-off that the next lint job consumes if no load happened since. It is an optimisation, not a requirement.                           |
| Execution cache             | One per session, internally locked                                                                       | Shared by both lanes of a snapshot; rule C2 (§3.7) makes interleaved lint and fix writes safe. Across a swap two instances write one file, like the CLI and the server: `Save` merges from disk under granularity plan §5.8, and yield-to-foreign-key lets only the instance whose key the file holds write.                                 |
| BinManager, runtime manager | One per session; the session work builds new ones on every load                                          | Carried into the new snapshot while apps, bundles and runtimes are unchanged, so both lanes share one per-app singleflight. When they change, `EnsureTools` calls on the old and new managers are serialised by one server-wide lock until the old snapshot's last job ends: two managers installing one app in place can damage each other. |
| Parser manager              | None                                                                                                     | One, lint lane only, carried across snapshots while the `parsers` entries are unchanged, so a reload does not recompile the module. Prewarmed when the lane starts.                                                                                                                                                                          |
| Documents                   | Reader-owned map; the reader snapshots text into formatting jobs                                         | The reader also attaches a snapshot to each lint trigger: open or not, the text's XXH3, the client's URI. An open-documents index (canonical path to client URI), updated by the reader under a lock, is what publishing reads.                                                                                                              |
| Active request, stop flag   | One `active`; the stop check reads it                                                                    | One per lane.                                                                                                                                                                                                                                                                                                                                |
| Once-per-session notices    | Plain fields                                                                                             | Per lane, or atomic.                                                                                                                                                                                                                                                                                                                         |
| `conn` writes               | Guarded by a mutex                                                                                       | Unchanged. `publishDiagnostics` and `window/logMessage` are more writers.                                                                                                                                                                                                                                                                    |

**Statements of the session spec this amends:**

- C1, "exactly one worker": there are two.
- C7, "`Run` never returns while the worker is still running": it still joins the format worker; the
  lint lane is handled by L7 and D10.
- R1 and C1 justify the chdir by "loads happen only on the worker, one at a time": L6 keeps that
  true, no longer by having one worker.
- L2, the reload check "at the start of each formatting request": at the start of each job on either
  lane, through L6.
- L3, "shut the old execution cache down (flush), then" build the new one: flushed at the swap, shut
  down at the last release (L5).

**Checkpoints**, where a stopped job stops:

- at the start, after pinning the snapshot and running the sweep (§3.8);
- after planning and filtering;
- after the managed-config preflight, `CheckConfigFiles(root, snapshot.managedConfigs,
plan.ManagedConfigRefs())`, which runs before `EnsureTools` and before any cache is consulted;
- after `EnsureTools`, which always finishes its install;
- before each priority group;
- from Stage 2b, between the tasks of a group, through the executor stop hook the transport
  research calls B7/B8.

A stop never reaches a running tool's context while the session lives.

### 3.4 Staleness and superseding

- **Sequence numbers.** Each job gets a sequence number when it is queued. Each owner remembers the
  sequence of the result it last published. A result is published for an owner only when its
  sequence is higher.
- **Superseded jobs still publish.** A running job's results are published under the sequence rule,
  whether or not a newer job is pending; the newer job replaces them when it finishes. Dropping them
  would leave older results on screen, and under autosave a unit tool would publish nothing while
  the user types.
- **When a running job stops early.** Only when every path it was started for has a newer trigger
  in the pending job. It then stops at its next checkpoint; the tool already running finishes and
  its results are published under the sequence rule.
- **Bytes that moved.** A per-file owner's result is dropped when the file no longer hashes to what
  the tool read (§3.5, step 1). That covers a format rewriting the file during the lint, including a
  half-written in-place fix: the lint the format or its save triggers replaces it. A unit owner's
  result from a run that overlapped a format is published and replaced by that lint, after one more
  unit run.
- **Changes outside the editor** (a `git checkout`, a code generator) are noticed lazily: by the
  sweep at every job start (§3.8), and through the session work's reload tripwires, so a branch
  switch shows up at the next job. There is no file watching (§7).

### 3.5 Positions

**The core contract**, set in `internal/diagnostic` in Stage 0:

- `Row` and `Col` are 1-based. A missing value or 0 becomes 1, as the doc comment already promises.
- `EndRow` and `EndCol` are 1-based, and `EndCol` is **exclusive**. A missing end becomes a point
  span at the start, and so does an end before the start.
- `File` is absolute and cleaned.
- `Col` and `EndCol` are counted in the unit the parser declares (D5). The unit is not a field on
  each diagnostic.

**Where each conversion lives:**

| Conversion                                                                     | Lives in                                                           | Stage                                     |
| ------------------------------------------------------------------------------ | ------------------------------------------------------------------ | ----------------------------------------- |
| 0 becomes 1; an end before the start becomes a point                           | `diagnostic.Resolve`                                               | 0                                         |
| Relative `File` becomes absolute, against the invocation's working directory   | `tooling.parseFileDiagnostics`, which gains the working directory  | 0                                         |
| In a batch of exactly one file, a diagnostic with no file gets that file       | `tooling.parseFileDiagnostics`                                     | 0                                         |
| The column unit of each Stage 1 parser, measured                               | Parser module descriptor (`columnUnit`), read via `DescribeParser` | 1, with a module release and wrapper bump |
| tsc (`utf-16`) and golangci-lint (`utf-8`), already measured                   | the same descriptor, in the same module release                    | 1, used from 2a                           |
| 1-based column in the tool's unit becomes a 0-based UTF-16 character, clamped  | `internal/lsp/position.go`                                         | 1                                         |
| Path becomes a URI                                                             | a new `pathToURI` in `internal/lsp`, the inverse of `uriToPathFor` | 1                                         |
| 0-based parsers (spectral, vacuum, pylint); col 0 meaning "none" (trivy, reek) | the parser module                                                  | 3                                         |

**What Stage 0 changes in the CLI's output.** These are intended, and the tests and goldens are
updated with them:

- A row or column of 0 now prints as 1 (`file:R:0` becomes `file:R:1`) for trivy, reek, pylint,
  spectral, vacuum and npm-groovy-lint.
- A one-file batch whose parser reports no file prints parsed lines instead of the raw output,
  because `usableDiagnostics` now finds a file.
- A tool-reported path that climbs out of the working directory (`../x`) prints absolute, since
  `relativeToBase` refuses a `..` result; `./x` prints as `x`.

**UTF-16 conversion** (`internal/lsp/position.go`, pure and table-tested):

1. Read the reported file once per result, from disk after the run. For a per-file owner, drop the
   result when those bytes no longer hash to the hash rule C2 took before the run (the lint lane takes
   it for an operation with `cache: false` too): the tool read something else. Record the file's
   stat and XXH3 for the sweep (§3.8).
2. Drop a leading UTF-8 BOM before converting line 1, as the editor keeps it out of the buffer.
3. Split lines at `\n`. A trailing `\r` belongs to the line terminator. A lone `\r` is a line break
   in LSP but not for the tools; in such files positions follow the tools' reading.
4. A row past the last line becomes the last line. A column past the end of the line becomes the end
   of the line.
5. Turn column `c` in unit `u` into a byte offset within the line:
   - `utf-8`: `c-1` bytes, snapped back to a rune boundary;
   - `utf-32`: `c-1` runes;
   - `utf-16`: `c-1` UTF-16 units, where a surrogate pair counts as 2;
   - unknown: if the line is pure ASCII every unit agrees and the offset is exact; otherwise the
     range becomes the whole line.
6. The LSP character is the number of UTF-16 units in the prefix before that offset. An invalid
   UTF-8 sequence counts as one unit per maximal invalid subsequence, as the WHATWG UTF-8 decoder
   replaces it, not one per byte.
7. The LSP line is `row-1`. The exclusive end is converted the same way, and an end before the start
   is set to the start.

A tab is one unit in every encoding. A tool that reports tab-expanded display columns needs its
parser to convert them; the Stage 1 measurements include a tab to find out.

### 3.6 Ownership, publishing and clearing

| Task shape                                                                              | Owner(s)                          | What a result covers                                                                                                                                             |
| --------------------------------------------------------------------------------------- | --------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| arity `many` or `one`: file granularity, or a unit operation narrowed to the saved file | `(tool, file)` for each task file | Exactly those files. A file with no diagnostic is clean for that tool. A diagnostic naming any other file is dropped and counted.                                |
| arity `none` or `dir` with coverage `complete` (tsc, golangci-lint)                     | `(tool, unitDir)`                 | Its clearing scope is the task's `UnitMembers` plus the files it published last time: one of those it no longer reports is cleared. Files outside the unit: D11. |

Each owner clears only its own entries. A file's published set is the union over its owners, with
exact duplicates (same range, severity, code, source and message) collapsed, so nested units that
report the same error show it once.

What each kind of result does to its owner. Results are classified after attribution, so "nothing
publishable" means nothing left once file-less and out-of-scope diagnostics are dropped:

| Result                                                                                                | Effect on the owner                                                                                                                                                                                                                                                                 |
| ----------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| The tool ran, exited 0, and parsed to no diagnostics                                                  | Replaced with `[]`, which clears.                                                                                                                                                                                                                                                   |
| The tool ran and produced publishable diagnostics, whatever its exit status                           | Replaced.                                                                                                                                                                                                                                                                           |
| A cache hit                                                                                           | Replaced with `[]`. After Stage 0 a hit means "nothing to report" (§3.7).                                                                                                                                                                                                           |
| The parser failed (`ParseFailed`)                                                                     | Replaced with one Information diagnostic on line 1 of each file of the invocation (per-file owner) or of each of the job's paths inside the unit (unit owner): "`<tool>`: its output could not be parsed; its earlier diagnostics were withdrawn". One `warn` per tool per session. |
| The tool failed with nothing publishable (crash, bad config, missing binary, all diagnostics dropped) | The previous result stays. An `error` event carries the tail of the tool's output and the number of diagnostics dropped.                                                                                                                                                            |
| The managed-config preflight failed                                                                   | Nothing runs; previous results stay. One `error` per cause per session, telling the user to run `datamitsu init`.                                                                                                                                                                   |
| The output is over the parse cap (§3.8)                                                               | The previous result stays. One notice per tool per session.                                                                                                                                                                                                                         |
| The owner already published a newer result                                                            | Dropped (§3.4).                                                                                                                                                                                                                                                                     |
| A reload cleared the owner after the job pinned its snapshot                                          | Dropped: the result was made under a configuration that no longer holds (§3.1).                                                                                                                                                                                                     |
| A per-file owner's file no longer hashes to what the tool read                                        | Dropped (§3.5, step 1).                                                                                                                                                                                                                                                             |
| A diagnostic has no file                                                                              | Per-file owner: stamped by the single-file rule. Unit owner: dropped, and counted in `done`.                                                                                                                                                                                        |
| A diagnostic names a file outside the root                                                            | Dropped, and counted.                                                                                                                                                                                                                                                               |

A parse failure replaces the result, while a tool failure keeps it, because the two fail
differently. A parser that cannot read a tool's output fails on every run, so a kept result would
never be corrected. A tool failure is usually transient and already surfaces as an `error`.

**Publishing.** For every file whose set changed, send `publishDiagnostics` with the union of its
owners' diagnostics; an empty array clears. Diagnostics are sorted by line, character, source and
code, so the output is stable. `source` is the tool name, or the source the parser set; severity
passes through unchanged (1–4 on both sides). A tool's result never overwrites another tool's.

**URIs.** The session work canonicalizes paths with `EvalSymlinks`; a published URI must use a
spelling the client knows. The server keeps the client's folder URI (the first workspace folder, or
`rootUri`) together with that folder's canonical path. The folder can be a subdirectory of the root,
a submodule that climbed to its superproject, or reached through a symlink.

- For an open document: the URI the client sent. A reported path is matched to open documents by
  canonical path, case-insensitively on darwin and windows, so one file never gets two URIs.
- For another file under the canonical folder: the folder URI joined with the relative path.
- For any other file under the root: `file://` plus the canonical path.

`pathToURI` percent-encodes the path (space, `#`, `%`, non-ASCII), writes Windows drive letters and
UNC roots the way `uriToPathFor` reads them, and uses forward slashes.

### 3.7 Cache semantics

**Rule C1: record a pass only when the cache can replay it as "nothing to report".** This applies to
a **lint** operation whose tool declares an `outputParser` (D9):

- A per-file pass is recorded for a file only when the parser ran without error, every diagnostic of
  the invocation matched a file of that invocation (after making paths absolute and cleaning them),
  and none matched this file. A diagnostic that matches no file of the invocation — `./x` against
  another base, a symlink against its target, a path outside the invocation — blocks every pass of
  the invocation.
- A unit verdict is recorded only when the parser ran without error and the task produced no
  diagnostic at all.

Lint operations without a parser keep today's rule: exit 0 is a pass. So do fix operations: the
parser is declared per tool and also runs over fix output (§1.1), where text such as eslint's fix
report is not what it was written for, so its result must not decide a fix pass.

What follows from C1:

- A hit, whether per file or a verdict, now means "a run of this tool on these bytes reported
  nothing". The owner publishes `[]`. `ExecutionResult.Cached` then only feeds events and
  explanations; correctness does not depend on it.
- The cost is that a file with findings is never cached for a tool that exits 0 on them (hadolint's
  `info` and `style`, or any tool run with a lenient threshold). It re-runs on every CLI `check` as
  well, although the CLI shows exit-0 findings nowhere (§1.4). Tools that fail on findings never had
  such a pass, so nothing changes for them.
- C1 trusts the parser. A parser that reads a changed output format as empty (§1.1) records a pass
  that hides findings. Stage 0 guards this with clean-output fixtures for every wired parser; a
  format change is caught only when the tool is bumped and its fixture re-recorded.

**Rule C2: record against the bytes the tool saw (fixes `markPassed`).**

- The hash is taken **before** the run, by the executor, through the process-wide content memo
  (`memoShared`), for new entries too. The executor hands it to the cache as `seen`, with the size
  and modification time it was taken under, and the cache compares it with the entry without hashing
  under its lock.
- After the run the file is re-checked, as `verdictSnapshot.refresh` does for a read-only operation:
  by stat, re-hashing (with `memoRewrite`, never served from the memo) only when the stat differs or
  cannot rule out a same-length rewrite:
  - if it is unchanged and the entry's hash differs, the entry is replaced by `{seen, [tool]}`;
  - if it is unchanged and the entry's hash matches, the tool is added to the entry;
  - if it changed during the run, nothing is recorded.

This is granularity plan §5.5's torn-read rule, applied per file. With two lanes it is what makes a
lint pass recorded next to a concurrent format safe. The hashing cost stays one hash per file per
run: today `ShouldRun` hashes an existing entry before the run and `markPassed` hashes a new one
after it; the post-run check adds a stat.

**Upgrade.** C1 changes what an existing pass means, and every local build reports the version
`dev`. `calculateInvalidationKey` gains a constant cache-semantics component, bumped whenever what an
entry means changes. Entries recorded under the exit-status rule then miss once, per-file entries and
verdicts alike, on release and development builds.

**Verdicts written from the editor.** A whole-unit tsc run from the lint lane has coverage
`complete` and writes a verdict under granularity plan §5.5, as the CLI does. Granularity plan
§13.3 ("an LSP save must not write a unit verdict") guards against forged coverage: it applies to
partial runs, such as a narrowed cspell save, which still write none.

**Out of scope, bounded and pre-existing.** eslint's typed rules read files outside the per-file key.
A stale pass can therefore hide a finding caused by another file until the linted file itself
changes. The editor inherits the CLI's exposure here; nothing new is introduced.

### 3.8 Closing, unopened files, external changes, caps (D3)

**Recommended:**

- **Per-file owners** publish only the task's files, which are open. `didClose` withdraws their
  entries for the file.
- **Unit owners** publish every file they report inside the root, open or not, and keep them after a
  close. They are replaced or cleared only by the same owner's next result, or by the sweep.
- **The sweep.** For every published file the server keeps the stat and XXH3 of the bytes the
  conversion read (§3.5, step 1). At every job start, and after a reload, it stats each of them,
  within the caps:
  - the file is gone: clear it;
  - the stat moved and the bytes changed: clear it when it is closed; when it is open and its buffer
    equals disk, enqueue a lint of it.

  The cost is one stat per published file per job.

- **Caps per owner:** 500 files and 200 diagnostics per file, open documents first. When a cap
  applies, one `info` notice per owner.
- **Output passed to a parser:** at most 8 MiB. Over it, the previous result stays and one notice per
  tool is sent. A pooled parser instance that parsed more than 4 MiB is closed instead of returned
  to the pool, because WASM linear memory never shrinks and the server lives for a session.
- **The tail of a failed tool's output** in an `error` event is its last 20 lines, at most 4 KiB.

Per-file owners only ever report open files, so D3 starts to matter in Stage 2a, with tsc and
golangci-lint.

### 3.9 Events and notices

The lint lane reports as a `diagnostics` operation, not as `lint`. The CLI's `lint` done event counts
tools with findings as failed; here findings are the product.

| Event                  | When                            | Fields                                                                                                                                                                                                                             |
| ---------------------- | ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `phase` start          | a job starts                    | `op: "diagnostics"`, `op_id: "diag-N"`                                                                                                                                                                                             |
| `tool_run` start       | a task starts                   | `op: "diagnostics"`, `tool`, `dir`, `op_id` of the form `<diag-N>:<tool>:<dir>`. From Stage 2b it gets a `:<seq>` suffix, which fixes `runner.go:1078` for this lane.                                                              |
| `tool_run` done / fail | a task ends                     | `success` (the exit status), `duration_ms`, `diagnostics` (new: how many were published), `cached` (new)                                                                                                                           |
| `error`                | a tool failed without an answer | `msg` with the output tail and the dropped count (§3.8). The extension pops up the session's first one.                                                                                                                            |
| `log` `info`           | once per tool and reason        | Operations left out, caps applied, `didOpen` triggers dropped, the lane idle because no parser is declared.                                                                                                                        |
| `log` `warn`           | once per module or tool         | A parser module failed to load; a parse failure; an `outputParser.parser` name missing from the module's descriptor.                                                                                                               |
| `log` `error`          | once per cause                  | The managed-config preflight failed.                                                                                                                                                                                               |
| `done`                 | a job ends                      | `tools`, `runs`, `failed` (tools without an answer), `skipped`, `diagnostics`. A job stopped early ends with `status: "fail"`, `success: false`, `msg: "superseded"`, the convention the session work uses for a cancelled format. |

- **Findings never produce an `error` event.** Otherwise the first lint finding of each session
  would pop up an error notification.
- **Parse failures are reported once.** The executor logs each failed parse at `debug` and marks the
  result `ParseFailed`. The runner reports it once per tool per run, and the lint lane once per tool
  per session, so no zap line turns every failure into a `warn`.
- **Other editors.** For a client that does not read the JSON-L stream, the lane's `warn` and `error`
  notices and its one-time notices also go out as `window/logMessage`, and the session's first
  actionable failure (a module that failed to load, a preflight failure, a tool failed without an
  answer) as `window/showMessage`. The VS Code extension already shows these from JSON-L and passes
  its own channel to the client (§1.8), so it declares `jsonlEvents: true` (§4.1) and gets neither.

### 3.10 VS Code extension

- **Settings.**
  - `datamitsu.diagnostics.enabled` (boolean);
  - `datamitsu.diagnostics.widenTo` (`target` | `unit`), added in Stage 2a;
  - `datamitsu.diagnostics.tools` (object of tool name to boolean).

  They are forwarded as `initializationOptions.diagnostics`, under the same "only what the user
  set" rule as `buildInitializationOptions`, with `jsonlEvents: true` always. A change restarts the
  server, as format settings already do.

- **Status bar.** `toolLabel` prefixes the event's `op` when it is not `format`, so a lint run reads
  "diagnostics: tsc". This is an extension change; an older extension shows the plain tool name.
- **Output channel.** After start, the echoed diagnostics policy is logged next to the format
  policy.
- **Rendering.** Nothing to build; vscode-languageclient renders `publishDiagnostics`.
- **Text.** The extension description, README and docs stop saying "format only".

### 3.11 Multi-root

The session work's direction is one process per datamitsu root. For diagnostics that means:

- A server publishes only files inside its canonical root; anything else is dropped.
- VS Code keeps one diagnostic collection per client, so two servers never overwrite each other.
- Both servers can still publish for the same file. This happens when an unregistered nested
  repository or a linked worktree sits inside another root's tree, because the outer walk descends
  into it. A submodule is not affected, since it climbs to the superproject. Routing a file to the
  deepest root is multi-root work, not this plan's.

### 3.12 Is `derived` still needed? (D6)

No. Diagnostics derive implicitly from lint operations, in the same way formatting derived from fix
operations. A lint operation plus its tool's `outputParser` is everything `derived` was meant to
inherit (globs, project types, parser), and "this operation takes part in the editor" is already
`lsp` on the operation. What `order` means was never specified; under this plan's accumulation
model (§3.6) nothing would consume it. The reference example (`hadolint: { type: "derived", … }`)
suggests a user must declare an entry to get diagnostics, which becomes false.

`proxy` (fronting a real language server such as gopls) is a different problem. It stays reserved
for a plan of its own.

---

## 4. Interfaces

### 4.1 `initializationOptions` and the echo

```ts
interface DatamitsuInitializationOptions {
  format?: { widenTo?: "target" | "unit"; timeoutMs?: number; tools?: Record<string, boolean> };
  diagnostics?: {
    /** Default: DATAMITSU_LSP_DIAGNOSTICS, then true (D4). false: no lint lane at all. */
    enabled?: boolean;
    /** Stage 2a. Default: DATAMITSU_LSP_DIAGNOSTICS_WIDEN_TO, then "unit". "repo" is not in the enum. */
    widenTo?: "target" | "unit";
    /**
     * false vetoes a tool. true opts in a unit operation that declares no globs and lifts the
     * session's widenTo for it; it never lifts the project's execution.widenTo.lint.
     */
    tools?: Record<string, boolean>;
  };
  /** The client reads the JSON-L stream on stderr; notices are not repeated as LSP messages. */
  jsonlEvents?: boolean;
}
```

- Validation is lenient, as for `format`: a rejected key warns, and `initialize` never fails because
  of an option. The raw options are kept, and the policy is re-resolved after every reload.
- The effective policy is echoed in `capabilities.experimental.datamitsu.diagnostics` and in one
  `info` log line: `{enabled, widenTo, tools, limits}`. `limits` carries the compile-time constants
  of §3.1, §3.3 and §3.8: the debounce, the `didOpen` paths per job, the format-lane wait, the stall
  warning (§6), the caps, the parse cap and the pooled-instance threshold. They have no environment
  override, so they are outside `runtimeconfig.Effective`; the echo is where they are read.
- Priority, highest first, as the shipped `editorDecision` orders it:
  1. `lsp: false` on the operation;
  2. `diagnostics.tools[name] === false`;
  3. the project's `execution.widenTo.lint`, which caps every unit run, opted in or not;
  4. `diagnostics.tools[name] === true`, which bypasses the glob-less rule and the session class;
  5. the session's `widenTo`: `initializationOptions`, then the environment, then the default.

  The §3.2 table is the precise statement.

### 4.2 Environment and `runtimeconfig`

| Variable                             | Default | `runtimeconfig.Effective` field                                          |
| ------------------------------------ | ------- | ------------------------------------------------------------------------ |
| `DATAMITSU_LSP_DIAGNOSTICS`          | `true`  | `LspDiagnostics bool` `json:"lspDiagnostics"`                            |
| `DATAMITSU_LSP_DIAGNOSTICS_WIDEN_TO` | `unit`  | `LspDiagnosticsWidenTo string` `json:"lspDiagnosticsWidenTo"` (Stage 2a) |

These follow the Environment Variable and Runtime Config checklists: the defaults as compile-time
constants in `internal/runtimeconfig/runtimeconfig.go` (`LspDiagnostics = true`,
`LspDiagnosticsWidenTo = "unit"`), an `envVar` in `internal/env/e.go`, a getter in `env.go`, a field
wired in `Compute()`, and visibility in `datamitsu config runtime`. The server reads them through
`runtimeconfig.Get()`, falling back to the env getter the way `policy.go`'s `editorWidenTo` does.
Tests check that the keys are present, never how many there are. Like `DATAMITSU_LSP_FORMAT_*`,
neither is added to `environExcluded` or `observationExcluded`.

### 4.3 Server capabilities and wire types

```go
type textDocumentSyncOptions struct {
    OpenClose bool         `json:"openClose"`
    Change    int          `json:"change"`
    Save      *saveOptions `json:"save,omitempty"` // {includeText:false} while diagnostics are enabled
}

type publishDiagnosticsParams struct {
    URI         string          `json:"uri"`
    Diagnostics []lspDiagnostic `json:"diagnostics"` // never null: [] clears
}

type lspDiagnostic struct {
    Range    Range  `json:"range"`
    Severity int    `json:"severity"` // 1-4, unchanged from diagnostic.Severity
    Code     string `json:"code,omitempty"`
    Source   string `json:"source"`
    Message  string `json:"message"`
}
```

`conn` gains `notify(method string, params any) error`, which writes under the existing write
mutex. `publishDiagnostics`, `window/logMessage` and `window/showMessage` go through it.

### 4.4 Executor, cache and diagnostic

```go
// internal/tooling/types.go — ExecutionResult gains:
ParseFailed bool     // Stage 0: the declared parser failed on some invocation; empty Diagnostics is no answer
Files       []string // Stage 1: the task's files as planned, absolute; run or served from cache alike
Cached      bool     // Stage 1: no process ran: a verdict hit, or every file cached
WholeUnit   bool     // Stage 2a: argv did not depend on the selection; the result speaks for UnitDir
UnitDir     string   // Stage 2a

// internal/cache — the executor hashes before the run; the cache never hashes under its lock:
type Seen struct{ Hash string; Size int64; ModTime time.Time }
func (c *Cache) Check(file, tool string, op Operation, seen Seen, enabled bool) (run bool)
func (c *Cache) AfterLint(file, tool string, seen Seen, unchanged bool, enabled bool) error
```

Each field lands in the stage that first reads it. The `Diagnostic` doc comment records the
contract in §3.5, and `Resolve` implements its documented 0 → 1 rule.

### 4.5 Parser descriptor (Stage 1)

```rust
pub(crate) struct ToolCapability {
    // ...existing fields...
    /// Unit of `col`/`end_col` as the tool reports them, in LSP's vocabulary:
    /// "utf-8" (bytes), "utf-16" (code units), "utf-32" (code points);
    /// "" when not established by measurement.
    pub(crate) column_unit: &'static str,
}
```

- On the Go side, `parsermanager.ToolCapability` gains `ColumnUnit string json:"columnUnit,omitempty"`.
  The parser catalogue page shows it (`task gen:parsers-doc`).
- A module test requires every parser either to declare a unit or to appear on an explicit list of
  unknowns in the module, so a new parser cannot leave it empty by omission.
- A module that predates the field reads as unknown for every parser, and conversion then falls back
  to whole-line ranges on non-ASCII lines (§3.5). The server is therefore safe to ship ahead of the
  wrapper moving to the new module.
- The same descriptor also lets the server warn about an `outputParser.parser` name the module does
  not know, which today is silent (§1.1).

### 4.6 One decision function for both lanes

```go
// operationDecision reports whether an editor-triggered run of task goes ahead, and if not why.
// Formatting and diagnostics call the same function, so the vetoes and the lattice cannot drift.
func operationDecision(task tooling.Task, paths []string, projectPolicy config.WidenTo, p sessionPolicy) (run bool, reason string)

type sessionPolicy struct {
    WidenTo       config.WidenTo
    Tools         map[string]bool
    RequireParser bool // diagnostics: an operation whose tool has no outputParser never runs (D7)
    AdmitUnit     bool // false for diagnostics in Stage 1 (§3.2, row 6a)
}
```

### 4.7 `uievent`

`Event` gains `Diagnostics *int json:"diagnostics,omitempty"` and
`Cached bool json:"cached,omitempty"`. `tool_run` events from the lint lane carry the existing `Op`.

- `Diagnostics` is a pointer so that a clean run reports 0. That avoids the drop-zero trap the
  granularity plan noted for `skipped`.
- The fields are additive. The extension's `parseEvent` checks only `type` and `op_id`.

---

## 5. Staging

Every stage ships on its own, with its own docs and tests.

| Stage                                         | Contents                                                                                                                                                                                                                                                                                                                                                                                   | Depends on                                                                                                                                                                                       |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **0 — core correctness**                      | No editor change. C1, C2, the cache-semantics key component, `ParseFailed` and parse-failure reporting, `--no-parse` (D12), the `Resolve` fixes, absolute `File`, single-file stamping, clean-output fixtures.                                                                                                                                                                             | nothing                                                                                                                                                                                          |
| **1 — diagnostics on save, file granularity** | The lint lane with L1–L7, snapshots and single-flight reload, lifecycle, triggers with debounce and merge, reload handling, the preflight, `diagnostics.enabled`/`tools` with env and echo, the decision function (file granularity only), per-file ownership and close, UTF-16 conversion, `pathToURI`, measured `columnUnit` for the Stage 1 tools, events and notices, extension, docs. | Stage 0; the session work. Its column units reach users only with a parser module release carrying `columnUnit` and a wrapper bump of the hash-pinned `parsers` entry, with the owner's go-ahead |
| **2a — unit operations**                      | `diagnostics.widenTo` with env and `runtimeconfig`; the `execution.widenTo.lint` cap; the glob-less rule; unit ownership; unopened files, the sweep for closed files, caps; D11; editor verdicts; D8; the D4 measurements.                                                                                                                                                                 | Stage 1                                                                                                                                                                                          |
| **2b — per-task identity and stops**          | The executor stop predicate between tasks and chunks; per-task identity in the executor callbacks, so `tool_run` op ids are unique; the runner may adopt it, at the cost of golden churn.                                                                                                                                                                                                  | Stage 1                                                                                                                                                                                          |
| **3 — parser coverage**                       | Parser-module fixes and new parsers, one wrapper wiring at a time.                                                                                                                                                                                                                                                                                                                         | Stage 1 (independent of 2a and 2b)                                                                                                                                                               |
| **4 — `lsp` cleanup**                         | Remove `derived` (D6).                                                                                                                                                                                                                                                                                                                                                                     | nothing; ships whenever D6 is decided                                                                                                                                                            |

**Stage 0 — core correctness.**

- C2: the executor hashes before the run through the content memo; the cache compares and records
  without hashing under its lock; the post-run check is by stat, re-hashing only when inconclusive.
- C1, for lint operations whose tool has a parser, with the attribution rule of §3.7.
- A constant cache-semantics component in `calculateInvalidationKey`.
- `ParseFailed` on the result. The executor logs a failed parse at `debug`, and the runner reports it
  once per tool per run. The runner warns once when a parser module fails to load, naming how many
  tools will run uncached, and suggests `datamitsu devtools parsers prefetch`.
- `--no-parse` switches only the CLI's display to raw output; parsing for the cache decision stays on
  (D12).
- `Resolve`: 0 becomes 1, an end before the start becomes a point, and the exclusive end column is
  documented. `File` is made absolute against the invocation's working directory, and a single-file
  batch is stamped.
- Clean-output fixtures in the parser module: real clean output of every wired parser (actionlint,
  checkmake, dclint, dotenv-linter, eslint, hadolint, protolint, yamllint, cspell, tsc,
  golangci-lint, harper-cli, vale) parses to no diagnostic and no error.

Visible to CLI users, and named in the PR description:

- files with findings of a tool that exits 0 on them (hadolint `info`/`style`) re-run on every
  `check`;
- the three output changes of §3.5;
- one "output parser failed" warning per tool per run instead of one per invocation, and one warning
  when a parser module fails to load;
- `--no-parse` still loads parser modules (D12);
- every cache is cold once after the upgrade.

_Exit criterion:_ cold and warm `check` wall time on this repository and on the startup-cost plan's
large-repository benchmark, before and after. A warm run must not regress beyond noise.

_Docs:_ `guides/architecture/caching.md` (the C1 pass rule, the C2 pre- and post-run check, the
semantics component); `--no-parse` and `DATAMITSU_NO_PARSE` in `reference/cli-commands.md` (`:20`,
`:1692`) and `guides/architecture/parsers.md` (`:401-402`); `task gen:llms-docs` with its committed
`internal/llmsdocs/embed/` output.

_Tests:_

- The `markPassed` reproduction as a regression test: A on X, B on Y, revert, and B must run.
- A tool that exits 0 with a parsed diagnostic records neither a per-file pass nor a verdict. Use a
  fake `DiagnosticParser`; the interface already allows one.
- A parse failure records no pass.
- A diagnostic naming a path outside the invocation blocks every pass of that invocation; one
  spelled `./x` for the invocation's `x` matches.
- `--no-parse` prints raw output and still records by the parsed rule.
- A fix operation of a tool with a parser still records its pass from the exit status.
- The post-run check re-hashes only when the stat moved or is inconclusive; a file rewritten during
  the run records nothing.
- An entry recorded before the semantics bump misses.
- Relative `File` from a tsc-shaped batch resolves against the working directory; eslint's absolute
  `File` is left alone.
- A file-less diagnostic in a one-file chunk is stamped; in a two-file chunk it is not.
- `Resolve` table: 0 row, 0 col, end before start, missing end.
- `runner_diagnostic_test.go` and any affected golden updated for the three output changes.

**Stage 1 — diagnostics on save for file-granularity lint.**

- **Lane.** A second worker with its own planner and executor; L1–L7; the L6 reload owner; snapshots
  pinned and released as L5 says; managers and the parser manager carried across snapshots (§3.3);
  per-lane active request and stop flag; `Planner.WalkedFiles()` as an optional seed.
- **Lifecycle.** L7 and D10.
- **Triggers.** The `save` sync option; `didSave`, `didOpen` (D2) and clean-path post-format
  triggers, carrying the reader's snapshot; debounce and merge with the `didOpen` bound; reload
  handling (§3.1); the sweep for open documents.
- **Policy.** `diagnostics.enabled` and `diagnostics.tools` with env fallback, `runtimeconfig` and
  the echo; `operationDecision`, admitting file granularity only; the preflight checkpoint; the
  lane idle without parsers.
- **Publishing.** Per-file ownership, union and clearing; withdrawal on close; `pathToURI`.
- **Positions.** `position.go`. `columnUnit` measured for every Stage 1 tool of both wrapper
  versions (actionlint, checkmake, dclint, dotenv-linter, eslint, hadolint, protolint, yamllint,
  harper-cli, vale), with a committed probe per tool: an input file with `é`, U+1F600 and a tab
  before a finding, and the tool's captured output. tsc and golangci-lint go into the same module
  release. End-column exclusivity audited for the same parsers.
- **Events.** `conn.notify`; the §3.9 events; `window/logMessage` and `window/showMessage` unless
  `jsonlEvents`.
- **Extension.** The settings, `jsonlEvents`, the policy log line, the status label, the README.

Until the wrapper moves to the module release, ranges on non-ASCII lines cover the whole line.

What ships: diagnostics on save and on open for the parsed file-granularity lint operations of the
consumer's wrapper version: at 0e1a9cf actionlint, checkmake, dclint, dotenv-linter, eslint,
hadolint, protolint and yamllint; at the pinned f201136 the same without dclint, with harper-cli and
vale.

_Docs:_ `reference/cli-commands.md` (the `lsp` section, the events contract, editor snippets for
Neovim, Helix and Zed showing `initializationOptions.diagnostics` and `DATAMITSU_LSP_DIAGNOSTICS`);
`getting-started/installation/vscode.md`; `reference/configuration-api.md` `:807` and `:1013-1015`,
naming `diagnostics.widenTo` and `diagnostics.tools` next to `format.*`; the `lsp` field doc in both
`config.d.ts` copies (`TestConfigDTSCopiesAreByteIdentical`) and in the wrapper's d.ts fork, in
lockstep and with the owner's go-ahead; the phase box in `guides/architecture/parsers.md` (`:14-18`),
which still says real parsers "arrive in a later phase"; `reference/parser-catalog.md` through
`task gen:parsers-doc`; `lsp --help` and its summary line (`cmd/lsp.go`, which also moves
`root_help.txt`); the package doc in `protocol.go`; `task gen:llms-docs` with its committed
`embed/` output.

_Tests:_

- `position.go` table: ASCII; `é` in every unit; U+1F600 in every unit; a tab; CRLF; a BOM; a column
  past the end of the line; a row past the end of the file; unknown unit on ASCII and on non-ASCII
  lines; invalid UTF-8 counted per maximal subsequence.
- `pathToURI`, table-tested for every GOOS the way `uriToPathFor` is: space, `#`, `%`, non-ASCII,
  drive letters, UNC; a subdirectory folder, a submodule folder and a symlinked folder.
- Owners: two tools on one file, one of them fixed; a cache hit clears; a parse failure replaces the
  result with the Information diagnostic; a failure without diagnostics keeps it and emits `error`;
  `didClose` withdraws per-file entries.
- Staleness: a slow fake tool (a shell script that sleeps, then prints), with saves A and B of
  different bytes during the run: A's result is never published, B's is, and A's process ran to
  completion (a marker file). Saves A and A' of identical bytes: A's result is published, then
  replaced.
- L1: a formatting request made while a 10 s lint tool runs returns in formatting time.
- L2: a hung fake formatter does not keep the lint lane from starting after the bound.
- L6: both lanes see a changed fingerprint at the same moment, and exactly one load runs (`-race`). A
  reload during a running lint job leaves that job on its snapshot, and the old cache is shut down
  when it ends.
- Reload: a reload that vetoes a tool clears that tool's diagnostics; one that adds a tool named in
  `diagnostics.tools` re-resolves the policy and re-emits the policy line; a reload from `.git/HEAD`
  alone clears nothing; a job pinned to the old snapshot publishes nothing for a tool the reload
  cleared.
- Lifecycle: `shutdown` during a 10 s fake lint tool is answered within the extension's 2 s window,
  and no `publishDiagnostics` follows the reply; EOF during the same run returns within the bound,
  and the tool's process group received SIGTERM (D10).
- The preflight: a stale render publishes nothing and never takes a cache hit that clears.
- Policy: `lsp: false`, `tools: false`, a repo operation and a unit operation are all left out, each
  with one notice; a config without parsers leaves the lane idle with one notice. Formatting's
  decisions are unchanged by the generalized function (the existing `editorDecision` table, run
  through `operationDecision`).
- Blackbox (`test/cli/lsp_test.go`): `didSave`, then `publishDiagnostics`; the `initialize` result
  advertises `save` and echoes the diagnostics policy. The test seeds its isolated parser directory
  (`DATAMITSU_PARSERS_DIR`) with `internal/parsermanager/testdata/echo.wasm` at its content-addressed
  path, declared with its real SHA-256, which `storedModuleIsValid` accepts offline. The module
  bundles every real parser, so a shell-script tool that prints hadolint-shaped JSON exercises a
  real one. Until the fixture is rebuilt with `columnUnit` (`build:parsers`), the test asserts the
  unknown-unit fallback.
- Extension: `options.ts` forwards `diagnostics.*` and `jsonlEvents`; `toolLabel` prefixes `op`.
- `-race` clean.

**Stage 2a — unit lint operations.**

- **Policy.** `diagnostics.widenTo`, with env and `runtimeconfig`; the `execution.widenTo.lint` cap;
  the glob-less rule.
- **Publishing.** Unit ownership with its clearing scope; unopened files with caps (D3); the sweep
  for closed files; D11; files outside the root dropped.
- **Verdicts.** Whole-unit editor runs write verdicts (§3.7).
- **Wrapper, with the owner's go-ahead.** D8.
- **Measurements** for D4: per-save `golangci-lint run` and tsc on this repository and on the
  large-repository benchmark, parser prewarm, and the server's memory with the lane on. Before
  Stage 2a ships, check how Neovim and Helix handle `publishDiagnostics` for hundreds of unopened
  files.

_Docs:_ `diagnostics.widenTo`, `DATAMITSU_LSP_DIAGNOSTICS_WIDEN_TO` and `lspDiagnosticsWidenTo`,
the caps and unopened files, in `cli-commands.md` and `vscode.md`; `task gen:llms-docs`.

_Tests:_

- A tsc fixture with errors in `a.ts` and `b.ts`: fix `a.ts` and save, and only `a.ts` clears.
- An error in `b.ts` caused by `a.ts`: fix `a.ts`, and `b.ts` clears although it was never opened.
- A verdict hit clears the whole unit. A whole-unit tsc save writes a verdict; a narrowed cspell
  save writes none.
- A cap is honoured, with one notice.
- A glob-less unit operation runs only when opted in.
- Autosave: a fake 10 s unit tool with a save every second still publishes after each run.
- The sweep: lint a file, close it, delete it on disk, start any job, and its diagnostics clear.
- golangci-lint with a module in a subdirectory and the configuration at the root reports paths
  that resolve (D8).

**Stage 2b — per-task identity and stops.**

- The executor stop predicate, checked between tasks, files and chunks (B7/B8); the context handed
  to tools stays uncancelled.
- Per-task identity in the executor callbacks.

_Docs:_ the events contract in `cli-commands.md` (the `:<seq>` suffix).
_Tests:_ two tasks of one tool in one directory get distinct op ids; a stop between two tasks of a
group leaves the second not-yet-started; if the runner adopts the ids, its goldens are regenerated once.

**Stage 3 — parser coverage.** Each item is independent.

- **Module fixes:**
  - file attribution in `markdownlint_cli2` and `editorconfig_checker`;
  - remove `protolint`'s `FILE_NAMES_LOWER_SNAKE_CASE` skip, a temp-file workaround that is wrong
    for a run on the real file;
  - 1-based output from spectral, vacuum and pylint;
  - `None` instead of col 0 in trivy and reek;
  - `columnUnit` measured for every parser that has a fixture;
  - the `json_u32` fractional backlog item (`docs/backlog/fractional-position-becomes-line-zero.md`);
  - for report-style parsers, a module ABI distinction between "report found, empty" and "no report
    found", so a changed output format stops reading as clean.
- **New parsers for active tools:** ruff, oxlint, shellcheck, stylelint, typos, tflint.
- **Wrapper:** each `outputParser` wired separately.

_Docs:_ the parser catalogue through `task gen:parsers-doc`.
_Tests:_ the module's own fixtures, including clean output, plus one end-to-end LSP fixture per newly
wired tool.

**Stage 4 — `lsp` cleanup (D6).**

- Remove `LspTypeDerived` and `LspEntry.Tool` from Go, from both `config.d.ts` copies, from the
  wrapper's d.ts fork (in lockstep, with the owner's go-ahead) and from the reference; update the
  facet-rule comment in `validate.go`, which expects "a future lsp binding".
- Bump `configcache.FormatVersion`, because the cached `Config` shape changes. If the proxy plan's
  S1 lands in the same release, the two share one bump.
- Add a CLAUDE.md "Breaking change: `lsp` type `derived` removed" entry under Product Stage.

_Docs:_ the `lsp` record in `configuration-api.md`; `task gen:llms-docs`.
_Tests:_ a config that declares `type: "derived"` fails to load with a directed message.

---

## 6. Risks

Only risks with a mitigation or a trigger are listed; the rest are handled in §3.

1. **Duplicate diagnostics next to native tooling.** tsc duplicates the TypeScript server's errors,
   and eslint duplicates the ESLint extension's. The remedy is `datamitsu.diagnostics.tools`, and it
   must be documented. `lsp: false` is the wrong lever, because it vetoes for editors that have no
   TypeScript server.
2. **Uncached findings cost time.** Under C1, a file with findings is re-linted on every save and
   every `check` when its tool exits 0 on them. If the Stage 0 measurement shows a regression that
   matters, D9's alternative is the fallback.
3. **Parser drift reads as clean.** A tool bump that changes its output format parses to nothing and
   records a pass (§3.7). The clean-output fixtures catch it only when re-recorded with the bump;
   Stage 3's ABI distinction closes it for report-style parsers.
4. **CPU contention.** The lint lane uses the executor's parallel pool, and formatting competes with
   it. L2 keeps new lint work from starting while a format runs, but not tools already running. If
   that shows up in measurements, cap the lane's workers.
5. **Planner walks.** Each job re-walks the repository (`Invalidate`), on top of the format lane's
   walk for the same save. The startup-cost plan measured two walks at 44 ms on a large repository
   (`docs/plans/2026-08-26-planner-and-startup-cost.md:41`). The seed of §3.3 is the mitigation.
6. **A hung lint tool stalls the lane.** It is not killed while the session lives. After five
   minutes a `warn` says diagnostics are paused until it exits; restarting the server ends it (D10).
7. **Shared `{toolCache}`.** The lint lane introduces editor-side runs of lint tools against the
   CLI's `{toolCache}`, so an editor tsc and a CLI tsc can write one `tsbuildinfo` at once. Inside
   the server, `{toolCache}` is keyed by tool name, not operation: golangci-lint's `run --fix` on the
   format lane (when opted in) and `run` on the lint lane share one directory at the same time,
   which `--allow-parallel-runners` makes acceptable.

---

## 7. Non-goals

Explicit refusals, not deferred versions.

- **No lint of unsaved buffers.** No temp copies and no `didChange` runs: they break each tool's own
  project detection (§1.6). This is reopened only if a lint operation declares `input: "stdin"`, and
  then in a plan of its own.
- **No killing a lint tool while the session lives**, whether it is superseded, slow or hung.
- **No pull diagnostics** (LSP 3.17) in this plan (§2).
- **No diagnostics stored in the cache** (granularity plan §12).
- **No caching of failures.** A save of unchanged bytes re-runs (§3.1).
- **No code actions or quick fixes.** Parsers carry no fix edits, and eslint's `fix` is dropped.
- **No `relatedInformation`, `tags` or `codeDescription`.** No parser emits them.
- **No file watching** (`workspace/didChangeWatchedFiles`) for changes made outside the editor.
- **No debounce, cap or trigger settings.** The behaviour in §3.1 and §3.8 is fixed and echoed.
- **No fix operations on the lint lane.**
- **No diagnostics policy in `config.Config`.**
- **One root per process.** Multi-root routing belongs to the multi-root work.
- **No proxying of other language servers.** `proxy` stays reserved.

---

## 8. Verification

End-to-end checks with real tools and a real editor that must pass before the work is considered
done. The per-stage tests cover the rest.

1. **A finding that exits 0 stays visible.** Save a Dockerfile with a hadolint `warning`: a
   diagnostic appears on the right line. Fix it and save: it clears. Add an `info`-level finding
   and run `datamitsu check` from the CLI: the next save still shows it (C1).
2. **The `markPassed` regression**, run through the CLI.
3. **Columns.**
   - Stage 1: eslint and yamllint findings after U+1F600 and `é` on the same line land on the
     character VS Code shows.
   - Stage 2a: golangci-lint's byte column on `\t/* ééé */ errors.New(` and tsc's UTF-16 column map
     to the right character.
   - A parser with an unknown unit on a non-ASCII line underlines the whole line.
4. **Autosave.** With `files.autoSave: afterDelay`, typing in a TypeScript file keeps tsc's
   diagnostics updating after each run.
5. **Restart during a long run.** Changing a `datamitsu.diagnostics.*` setting while tsc runs
   restarts the server within the stop window, and no second tsc from the old server keeps running
   (D10).
6. **Branch switch.** Switch branches with files open and closed: open files are re-linted, and
   closed files whose bytes changed lose their diagnostics.
7. **Introspection.** `datamitsu config runtime | jq .lspDiagnostics` and `jq .lspDiagnosticsWidenTo`
   return the effective values, including env overrides; the `initialize` echo carries `limits`.
8. **Golden stability.** `go test ./test/cli/ -count=2` is byte-stable. The goldens that change are
   `test/cli/testdata/golden/root_help.txt`, whose `lsp` summary still says "formatting-only", and
   those Stage 0 names for the §3.5 output changes. `test/cli/lsp_test.go` gains assertions for the
   `save` sync option and the `experimental.datamitsu.diagnostics` echo.
9. **Race detector.** `go test -race ./internal/lsp/ ./internal/tooling/ ./internal/cache/` is clean.

---

## 9. Decisions for the owner

**D1. The lane in Stage 1.**

- **Recommended:** a second worker for lint from Stage 1, with the rules L1–L7. Accepting it lifts
  granularity plan §12's non-goal "No LSP concurrency rework and no diagnostics".
- **Alternative:** Stage 1 runs lint on the session work's single worker, with formatting first in
  the queue, and the second lane arrives in Stage 2a.
- **Cost of the alternative:** a format-on-save that arrives while a lint tool runs waits for that
  tool: up to 2.6 s of eslint here, and whatever harper-cli and vale take on a markdown save, on top
  of formatting's own time.
- **Cost of the recommendation:** the concurrency work lands in the first release: the single-flight
  reload owner, reference-counted snapshots, managers carried across reloads, the per-lane stop
  flag, and the lane's lifecycle.

**D2. Lint on open.**

- **Recommended:** yes, when the buffer equals the file on disk, with at most 64 `didOpen` paths per
  job.
- **Alternative A:** save only.
- **Alternative B:** lint on open only when the document becomes visible, which the extension would
  have to report through a custom notification.
- **Cost of the recommendation:** opening a workspace with restored tabs runs every eligible
  file-level linter once per tab, coalesced into few jobs. The server also receives `didOpen` for
  every file-scheme document any extension opens through `workspace.openTextDocument`, and for
  Neovim buffer loads (`:cdo`, quickfix), which the user may never see. On a fresh machine that
  triggers `EnsureTools` downloads when the workspace opens. The per-file cache makes clean files
  cheap, and C1 keeps files with findings uncached.
- **Cost of alternative A:** a file shows nothing until its first save, which reads as "no problems".
- **Cost of alternative B:** a custom notification that other editors do not send, so they would lint
  only on save.

**D3. Which files get diagnostics, and for how long.**

- **Recommended:** per-file results are withdrawn when the file is closed; unit results cover every
  file a unit tool reports inside the root, kept on close, capped at 500 files and 200 diagnostics
  per file per owner, and swept for external changes at every job start.
- **Alternative:** open documents only, with unit results for other files held in memory and
  published when the file opens.
- **Cost of the recommendation:** a closed file keeps a unit tool's diagnostics that a change made
  outside the editor made stale, until the next job's sweep or the owner's next run. Neovim creates a
  buffer for each unopened file it receives diagnostics for; that is checked before Stage 2a ships.
- **Cost of the alternative:** cross-file errors, a tsc error in `b.ts` caused by an edit to `a.ts`,
  stay invisible until `b.ts` is opened. That takes away most of what unit linting is for.
- Matters from Stage 2a.

**D4. Defaults.**

- **Recommended:** `enabled: true`, so every editor gets diagnostics without configuration and
  `DATAMITSU_LSP_DIAGNOSTICS=false` turns them off; `widenTo: "unit"`, mirroring formatting, confirmed
  only after the Stage 2a measurements.
- **Alternative for `enabled`:** off unless enabled.
- **Alternative for `widenTo`:** `target` by default, with unit tools run only when
  `diagnostics.tools` opts them in.
- **Cost of the recommendation:** a Neovim or Helix user who upgrades datamitsu gets lint runs on
  save without asking for them, told only by a `window/logMessage` policy line. In VS Code, tsc and
  eslint duplicates appear next to the native extensions (risk 1). Formatting's `unit` default was
  justified by Go's fix operations being unit-granular (granularity plan §6.2); for lint, `unit`
  means a whole-module golangci-lint and a whole-project tsc on every save, whose cost is not yet
  measured.
- **Cost of the alternatives:** off by default makes the feature invisible to anyone who does not
  read the release notes, and the extension would have to turn it on, a policy decided in the
  client. `target` by default leaves tsc and golangci-lint diagnostics off until each user opts in.

**D5. Where each parser's column unit is declared.**

- **Recommended:** a `columnUnit` field in the parser module's descriptor, measured per parser. The
  unit is a property of the tool's output format, so it is versioned with the parser code that reads
  that format: a parser change and its unit ship in one module release.
- **Alternative A:** a Go table keyed by parser name. It needs no module release, but the unit then
  lives apart from the code that parses the format, and a parser change can leave it wrong without
  either side noticing.
- **Alternative B:** `outputParser.columnUnit` in the config. It puts a tool-format fact on the
  config author, and it enters the hashed config, so every edit resets every user's cache.

**D6. The reserved `lsp` entity.**

- **Recommended:** remove the `derived` variant in Stage 4, and keep `proxy` reserved (§3.12).
- **Alternative:** keep both reserved.
- **Cost of the recommendation:** a breaking change to a declaration nobody uses. No shipped config
  declares an entry, and the product is alpha.
- **Cost of the alternative:** the reference keeps an example that implies diagnostics need a
  declaration they do not need.

**D7. Lint operations whose tool has no parser.**

- **Recommended:** they never run in the editor.
- **Alternative:** run them, and when one fails, publish one file-level diagnostic ("prettier
  --check failed") carrying the first line of its output.
- **Cost of the recommendation:** failures of more than twenty parser-less linters (ruff, shellcheck,
  oxlint, …) stay out of the editor until Stage 3 gives them parsers.
- **Cost of the alternative:** every formatter check (`prettier --check`, `oxfmt --check`,
  `golangci-lint fmt --diff`) turns into a squiggle. With format-on-save on, that is almost always
  noise, and the synthetic diagnostic points at line 1 rather than at the problem.

**D8. golangci-lint's lint operation in the wrapper.**

- **Recommended:** add the wrapper's Go globs to it (`goGlobs = ["**/*.go", "**/go.mod"]` at the
  pinned f201136; check the name at the commit being changed), and `--path-mode=abs`. Saving a `.go`
  file then lints its module without opt-in, saving a `.ts` or `README.md` file in a mixed
  repository no longer reaches it, and reported paths no longer depend on which base golangci-lint
  resolves them against (§1.3).
- **Alternative:** leave the wrapper alone. Go users opt in with
  `datamitsu.diagnostics.tools: {"golangci-lint": true}`, and Stage 2a measures the path base with a
  module in a subdirectory and the configuration at the root.
- **Cost of the recommendation:** a wrapper change. It is the remedy granularity plan §11c's first
  deviation assigns to the author: "`globs`, or better `{files}`". `{files}` is not an option for
  `golangci-lint run`, which lints packages, not a list of files, so globs are what is left.
- **Cost of the alternative:** no Go lint diagnostics by default, which is the most visible gap for
  this repository itself.

**D9. Rule C1 as a core rule.**

- **Recommended:** C1 in the core, one pass rule for the CLI and the editor (§3.7).
- **Alternative:** keep the exit-status rule in the core. The lint lane bypasses the per-file cache
  and verdicts for tools with a parser, runs them on every trigger, and records nothing.
- **Cost of the recommendation:** it changes the cache contract for every CLI user of a parsed lint
  tool. Files with findings of a tool that exits 0 on them re-run on every `check`, with no visible
  benefit in the CLI, which shows those findings nowhere.
- **Cost of the alternative:** every editor lint pays the tool's full cost, 2.6 s of eslint per save
  for a clean file, and a CLI pass keeps hiding exit-0 findings from any future consumer of the
  cache.

**D10. A running lint tool when the session ends.**

- **Recommended:** on `exit`, stdin EOF or the first SIGTERM, cancel the lint lane's tool context,
  which sends SIGTERM to each running lint tool's process group. `Run` waits for the lint lane at
  most the 1 s grace `cmd/lsp.go` already uses, then returns; a tool that ignores SIGTERM is left
  behind. While the session lives, no lint tool is killed. "A running tool is never interrupted"
  stays a formatting invariant: fix tools write user files, lint tools do not.
- **Alternative:** exit without waiting and leave the tool running as an orphan, whose output goes
  nowhere.
- **Cost of the recommendation:** a tool killed mid-write can leave a torn file in its
  `{toolCache}` (`tsbuildinfo`, the cspell cache). Whether each tool tolerates that is unverified;
  `datamitsu cache clear` removes it. On Windows only the direct child dies
  (`executor_windows.go`).
- **Cost of the alternative:** after every restart (a settings change, a window reload,
  `:LspRestart`) the old tool keeps running for up to minutes, in parallel with the new server's run
  of the same tool against the same `{toolCache}`: the concurrent writer the recommendation avoids,
  plus the CPU.

**D11. A unit tool's diagnostics for files outside its unit.**

- **Recommended:** publish them when they are inside the root, under the owner that reported them.
  They join its clearing scope, and exact duplicates across owners collapse (§3.6).
- **Alternative:** drop them and count them. A non-zero exit that leaves nothing publishable becomes
  "failed without an answer": the previous result stays, and an `error` names the dropped count.
- **Cost of the recommendation:** an error in a shared package can appear under two owners, once per
  unit that type-checks it, when the messages differ.
- **Cost of the alternative:** a tsc error in `../shared`, reached through `paths` in a TypeScript
  monorepo, never reaches the Problems panel, while `datamitsu lint` reports it.

**D12. `--no-parse` and `DATAMITSU_NO_PARSE`.**

- **Recommended:** decouple them from the parse decision. The executor always parses when a tool
  declares a parser, so C1 does not depend on a display flag; `--no-parse` only switches the CLI to
  raw output, which is what its documentation says it is for. The lint lane ignores it, and
  `DATAMITSU_LSP_DIAGNOSTICS=false` is the only editor switch.
- **Alternative:** keep them as they are. Under C1, parsed lint operations then record no pass while
  parsing is off; the effective `lspDiagnostics` folds `DATAMITSU_NO_PARSE` in, with the reason in
  the echo; `runtimeconfig.Effective` gains `noParse`; and `datamitsu lsp` defines what `--no-parse`
  does.
- **Cost of the recommendation:** a `--no-parse` run still fetches, compiles and runs parser modules.
- **Cost of the alternative:** a debugging flag gains two jobs, a CLI-visible slowdown (no pass
  recorded for any parsed lint tool) and a silent editor switch that `datamitsu config runtime`
  must be taught to report.
