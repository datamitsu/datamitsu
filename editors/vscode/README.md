# Datamitsu Toolkit

Format files with your project's [datamitsu](https://github.com/datamitsu/datamitsu)
config. The extension is a thin client around `datamitsu lsp` — a formatting-only
language server — so "Format Document" and format-on-save run the exact same
formatters as `datamitsu fix` on the command line, driven by one config.

It does not bundle any formatters or reimplement any formatting logic: it starts
`datamitsu lsp`, registers it as a document formatter, and shows long-running work
(tool downloads, installs) in the status bar by reading datamitsu's JSON-L event
stream.

## Requirements

A `datamitsu` binary. By default the extension uses one found on your `PATH`; if
there is none it downloads the version pinned to the extension build (verified by
SHA-256). The extension activates only in a workspace that has a
`datamitsu.config.{js,mjs,ts}`.

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

Because datamitsu's fix tools edit files in place, formatting runs on the file on
disk and therefore **also saves it**. The edits are computed as the diff between
your buffer and the fixed result, so the editor stays in sync. A buffer that
already matched the file on disk receives no edits: the file is fixed in place and
the editor reloads it.

## What runs on save

Each fix tool runs at its own granularity, and that decides whether a save runs it:

- **File-level** tools (prettier, shfmt, ...) always run, on the saved file only.
- **Project-wide** tools (golangci-lint, ...) run over the saved file's project
  under `datamitsu.format.widenTo: "unit"` (the default), never beyond what the
  project's own `execution.widenTo` allows. `"target"` leaves them out.
- **Project-wide tools without `globs`** are left out: they cannot tell which
  files they format, so they would run over the whole project on every save,
  whatever the file's type. The config author adds `globs` (such as
  `["**/*.go"]`); a user can opt one in with `datamitsu.format.tools`.
- **Repository-wide** tools never run on save; `datamitsu fix` runs them.

`lsp: false` on an operation in the datamitsu config keeps it out of the editor,
whatever the settings say. `datamitsu.format.tools` switches single tools by
name: `false` keeps one off save, `true` runs a project-wide one even under
`"target"` or without `globs`, still within the project's own
`execution.widenTo`.

A save must not hang the editor: once `datamitsu.format.timeoutMs` has elapsed, no
further group of tools starts. A running tool is never stopped, and downloads do
not count.

The status bar shows downloads, installs and each running tool. The **datamitsu**
output channel records the policy the server runs with, the tools a save left out
and why, failed tools and every warning, each at its level (`debug`, `info`,
`warn`, `error`); the first warning or error of a session also pops up once. A
save that ran no fix tool at all shows a one-time hint.

The language server reads the datamitsu config once, when it starts: after editing
it, run **datamitsu: Restart Language Server**. The extension restarts the server
by itself only when `datamitsu.format.*`, `datamitsu.path` or
`datamitsu.binaryMode` change.

See [the documentation](https://datamitsu.com/docs/getting-started/installation/vscode)
for the details.

## Settings

| Setting                      | Default | Description                                                                                               |
| ---------------------------- | ------- | --------------------------------------------------------------------------------------------------------- |
| `datamitsu.binaryMode`       | `auto`  | `auto` (PATH, else pinned download), `system` (PATH only), or `bundled` (always the pinned download).     |
| `datamitsu.path`             | `""`    | Explicit path to the `datamitsu` binary; overrides `binaryMode`.                                          |
| `datamitsu.format.widenTo`   | `unit`  | `unit` also runs project-wide fix tools for the saved file's project; `target` runs only file-level ones. |
| `datamitsu.format.timeoutMs` | `15000` | Milliseconds after which no further tool group starts on a save; `0` disables the watchdog.               |
| `datamitsu.format.tools`     | `{}`    | Per-tool switch by tool name: `false` keeps a tool off save, `true` opts a project-wide tool in.          |
| `datamitsu.trace.server`     | `off`   | Trace JSON-RPC traffic to the output channel.                                                             |

The `datamitsu.format.*` settings reach the server only when you set them, in user
or workspace settings. In a multi-root workspace the extension starts one server
for the whole window, so a folder's own `.vscode/settings.json` cannot set them.
An unset one leaves the server on `DATAMITSU_LSP_FORMAT_WIDEN_TO` /
`DATAMITSU_LSP_FORMAT_TIMEOUT_MS` from its environment, then on the default.

## Commands

- **datamitsu: Restart Language Server**
- **datamitsu: Show Output Channel**

## How it works

`datamitsu lsp` speaks LSP on stdout and emits a typed JSON-L status stream on
stderr. The extension owns the child process: it wires stdout/stdin to the
language client and reads stderr for the status bar. stdout therefore carries only
LSP traffic. See [the datamitsu repo](https://github.com/datamitsu/datamitsu) for
the language-server and logging design.
