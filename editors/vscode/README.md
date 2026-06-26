# datamitsu for VS Code

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

Both triggers run the same thing — your project's `datamitsu fix` on the real
file:

- **Format Document** (`Shift+Alt+F`), or set datamitsu as the default formatter.
- **On save** — enable format-on-save and pick datamitsu as the formatter:

  ```jsonc
  {
    "editor.formatOnSave": true,
    "[go]": { "editor.defaultFormatter": "datamitsu.datamitsu" },
    "[typescript]": { "editor.defaultFormatter": "datamitsu.datamitsu" },
  }
  ```

Because datamitsu's fix tools edit files in place, formatting runs on the file on
disk and therefore **also saves it**. The edits are computed as the diff between
your buffer and the fixed result, so the editor stays in sync.

## Settings

| Setting                  | Default | Description                                                                                           |
| ------------------------ | ------- | ----------------------------------------------------------------------------------------------------- |
| `datamitsu.binaryMode`   | `auto`  | `auto` (PATH, else pinned download), `system` (PATH only), or `bundled` (always the pinned download). |
| `datamitsu.path`         | `""`    | Explicit path to the `datamitsu` binary; overrides `binaryMode`.                                      |
| `datamitsu.trace.server` | `off`   | Trace JSON-RPC traffic to the output channel.                                                         |

## Commands

- **datamitsu: Restart Language Server**
- **datamitsu: Show Output Channel**

## How it works

`datamitsu lsp` speaks LSP on stdout and emits a typed JSON-L status stream on
stderr. The extension owns the child process: it wires stdout/stdin to the
language client and reads stderr for the status bar. stdout therefore carries only
LSP traffic. See [the datamitsu repo](https://github.com/datamitsu/datamitsu) for
the language-server and logging design.
