---
title: VS Code Extension
description: Install the Datamitsu Toolkit extension for VS Code, Cursor, VSCodium, and other VS Code-based editors
---

# VS Code Extension

[![VS Code Marketplace](https://img.shields.io/visual-studio-marketplace/v/datamitsu.datamitsu-toolkit?label=VS%20Code%20Marketplace&color=0066b8)](https://marketplace.visualstudio.com/items?itemName=datamitsu.datamitsu-toolkit)
[![Open VSX](https://img.shields.io/open-vsx/v/datamitsu/datamitsu-toolkit?label=Open%20VSX&color=a60ee5)](https://open-vsx.org/extension/datamitsu/datamitsu-toolkit)

**Datamitsu Toolkit** brings datamitsu into your editor: "Format Document" and
format-on-save run your project's fix tools on the real file — the exact same
formatters as `datamitsu fix` on the command line, driven by one config.

The extension is a thin client around `datamitsu lsp`. It does not bundle any
formatters or reimplement any formatting logic: it starts the language server,
registers it as a document formatter, and shows long-running work (tool
downloads, installs, tool runs) in the status bar.

## Install

- **VS Code** — install [Datamitsu Toolkit from the Marketplace](https://marketplace.visualstudio.com/items?itemName=datamitsu.datamitsu-toolkit),
  or search for "Datamitsu Toolkit" in the Extensions view (`Ctrl+Shift+X`).
- **Cursor, Windsurf, VSCodium, and other VS Code-based editors** — install
  [Datamitsu Toolkit from Open VSX](https://open-vsx.org/extension/datamitsu/datamitsu-toolkit),
  the registry these editors use. Searching "Datamitsu Toolkit" in the editor's
  Extensions view finds the same extension.

Or from the command line:

```bash
code --install-extension datamitsu.datamitsu-toolkit
```

## Requirements

A `datamitsu` binary. By default the extension uses one found on your `PATH`; if
there is none, it downloads the version pinned to the extension build and
verifies it by SHA-256 before running it.

The extension activates only in a workspace that has a datamitsu config
(`datamitsu.config.js`, `datamitsu.config.mjs`, or `datamitsu.config.ts`).

## Usage

Both triggers run your project's fix tools on the real file — which ones is
described in [What runs on save](#what-runs-on-save):

- **Format Document** (`Shift+Alt+F`), or set datamitsu as the default formatter.
- **On save** — enable format-on-save and pick datamitsu as the formatter:

```jsonc
{
  "editor.formatOnSave": true,
  "[go]": { "editor.defaultFormatter": "datamitsu.datamitsu-toolkit" },
  "[typescript]": { "editor.defaultFormatter": "datamitsu.datamitsu-toolkit" },
}
```

Because datamitsu's fix tools edit files in place, formatting runs on the file
on disk and therefore also saves it. A buffer with unsaved changes is written
first, and the edits the editor receives are the diff between that buffer and
the fixed result. A buffer that already matched the file on disk receives no
edits: the file is fixed in place and the editor reloads it. Either way the
editor stays in sync.

## What runs on save

Every fix operation has a
[granularity](../../reference/configuration-api.md#granularity) — the smallest
set of files its result is complete for — and that decides whether a save runs
it:

| Granularity | Example         | On save                                                                          |
| ----------- | --------------- | -------------------------------------------------------------------------------- |
| `file`      | prettier, shfmt | Always, on the saved file only                                                   |
| `unit`      | golangci-lint   | Over the saved file's project, unless `widenTo` is `target` or it has no `globs` |
| `repo`      | syncpack        | Never: a repository-wide fix does not run on save, `datamitsu fix` runs it       |

A save never runs a tool over another project: only the project that contains
the saved file is in reach.

Three controls adjust this. A `false` from the config or from
`datamitsu.format.tools` always wins:

- **`lsp: false`** on an operation in the config is the
  [author's veto](../../reference/configuration-api.md#keeping-an-operation-out-of-the-editor-lsp):
  that operation never runs from the editor, whatever the settings below say.
- **`datamitsu.format.tools`** is a per-tool switch, keyed by the tool's name in
  the config's `tools`. `false` keeps a tool off save. `true` runs a project-wide
  tool even when `widenTo` is `target` or it declares no `globs` — but never
  further than the project's own
  [`execution.widenTo.fix`](../../reference/configuration-api.md#executionwidento)
  allows, and never for a repository-wide tool.
- **`datamitsu.format.widenTo`** is `unit` (the default) or `target`. It can only
  narrow what the project allows: when the project sets
  `execution.widenTo.fix` to `target`, no project-wide tool runs on save under
  either value.

```jsonc
{
  // Only tools that take the saved file itself...
  "datamitsu.format.widenTo": "target",
  "datamitsu.format.tools": {
    // ...plus this project-wide formatter,
    "golangci-lint": true,
    // and never this one.
    "eslint": false,
  },
}
```

### Project-wide tools without globs

A project-wide operation that declares no `globs` does not run on save. Without
them it cannot tell which files it formats, so it would claim every save in its
project: saving a TypeScript file that lives in a Go module would run a
glob-less `golangci-lint fmt` over the whole module, holding up the formatters
the file actually needs. The output channel names such a tool with the reason
`declares no globs, so it cannot tell which files it formats`. Two remedies:

- **For the config author** — add `globs` to the operation (such as
  `["**/*.go"]`), so only the files it formats bring it in. A tool whose result
  for one file does not depend on its neighbours can also take `{files}` and
  declare [`granularity: "file"`](../../reference/configuration-api.md#granularity),
  so a save runs it on the saved file alone.
- **For you** — opt the tool in with `datamitsu.format.tools`. It then runs over
  the saved file's project on every save there, whatever the file's type, and
  still no further than the project's own `execution.widenTo.fix` allows.

### The watchdog

A save must not hang the editor. Tools run in
[priority groups](../../guides/architecture/execution.md#two-layer-execution-model),
and once `datamitsu.format.timeoutMs` (default `15000`) has elapsed no further
group starts. The first group always runs, a running tool is never interrupted —
a formatter killed halfway would leave a file carrying some tools' edits but not
others' — and time spent downloading or installing a tool does not count. When
the watchdog stops a save, a warning names the tools that did not run. `0`
disables it.

### Where notices appear

- The **status bar** shows downloads, installs, and each tool while it runs.
- The **datamitsu output channel** (**datamitsu: Show Output Channel**) records
  the policy the server runs with, the tools a save left out and why, failed
  tools, and every warning — the server's own and those datamitsu logs while it
  loads the config or installs a tool. Each line appears at its level: `debug`,
  `info`, `warn` or `error`. Debug lines need both sides lowered: the server's
  level (`DATAMITSU_LOG_LEVEL=debug` in the environment VS Code starts from) and
  the channel's (**Developer: Set Log Level...**).
- The first warning or error of a session also pops up once, with a **Show
  Output** action.
- A save that runs no fix tool at all — none applies to the file's type, or the
  policy left them all out — shows a one-time hint.

### After a config change

The language server reads `datamitsu.config.*` once, when it starts. After
editing the config, run **datamitsu: Restart Language Server**. The extension
restarts the server by itself only when its own settings change:
`datamitsu.format.*`, `datamitsu.path`, or `datamitsu.binaryMode`.

Until it is restarted, a session keeps formatting with the config it started
with. Once a `datamitsu` command has cached results under a different
configuration, the session stops writing the shared cache — so it cannot
discard those results — and warns once. The same happens, as a note in the
output channel rather than a warning, when the CLI runs another `datamitsu`
version than the editor: point `datamitsu.path` at the project's binary to
share one cache.

## Settings

| Setting                      | Default | Description                                                                                               |
| ---------------------------- | ------- | --------------------------------------------------------------------------------------------------------- |
| `datamitsu.binaryMode`       | `auto`  | `auto` (PATH, else pinned download), `system` (PATH only), or `bundled` (always the pinned download).     |
| `datamitsu.path`             | `""`    | Explicit path to the `datamitsu` binary; overrides `binaryMode`.                                          |
| `datamitsu.format.widenTo`   | `unit`  | `unit` also runs project-wide fix tools for the saved file's project; `target` runs only file-level ones. |
| `datamitsu.format.timeoutMs` | `15000` | Milliseconds after which no further tool group starts on a save; `0` disables the watchdog.               |
| `datamitsu.format.tools`     | `{}`    | Per-tool switch by tool name: `false` keeps a tool off save, `true` opts a project-wide tool in.          |
| `datamitsu.trace.server`     | `off`   | Trace JSON-RPC traffic to the output channel.                                                             |

The `datamitsu.format.*` settings reach the server only when you set them — in
user or workspace settings. In a single-folder window, the folder's
`.vscode/settings.json` is the workspace settings. In a multi-root workspace the
extension starts one server for the whole window, so a folder's own settings
file cannot set them: put them in the `.code-workspace` file. An unset one
leaves the server on `DATAMITSU_LSP_FORMAT_WIDEN_TO` or
`DATAMITSU_LSP_FORMAT_TIMEOUT_MS` from its environment, and then on the default
above. The output channel shows the values the session actually runs with.

Other editors can pass the same settings to `datamitsu lsp` directly — see
[`lsp`](../../reference/cli-commands.md#lsp).

## Commands

- **datamitsu: Restart Language Server**
- **datamitsu: Show Output Channel**

## Source

The extension lives in the datamitsu monorepo under
[`editors/vscode`](https://github.com/datamitsu/datamitsu/tree/main/editors/vscode).
Each release also attaches the `.vsix` to the
[GitHub Release](https://github.com/datamitsu/datamitsu/releases) for manual
installation.
