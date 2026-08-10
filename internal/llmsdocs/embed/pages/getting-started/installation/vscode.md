# VS Code Extension

> Install the Datamitsu Toolkit extension for VS Code, Cursor, VSCodium, and other VS Code-based editors

[![VS Code Marketplace](https://img.shields.io/visual-studio-marketplace/v/datamitsu.datamitsu-toolkit?label=VS%20Code%20Marketplace&color=0066b8)](https://marketplace.visualstudio.com/items?itemName=datamitsu.datamitsu-toolkit)
[![Open VSX](https://img.shields.io/open-vsx/v/datamitsu/datamitsu-toolkit?label=Open%20VSX&color=a60ee5)](https://open-vsx.org/extension/datamitsu/datamitsu-toolkit)

**Datamitsu Toolkit** brings datamitsu into your editor: "Format Document" and
format-on-save run your project's `datamitsu fix` on the real file — the exact
same formatters as on the command line, driven by one config.

The extension is a thin client around `datamitsu lsp`. It does not bundle any
formatters or reimplement any formatting logic: it starts the language server,
registers it as a document formatter, and shows long-running work (tool
downloads, installs) in the status bar.

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

Both triggers run your project's `datamitsu fix` on the real file:

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
on disk and therefore also saves it. The edits are computed as the diff between
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

## Source

The extension lives in the datamitsu monorepo under
[`editors/vscode`](https://github.com/datamitsu/datamitsu/tree/main/editors/vscode).
Each release also attaches the `.vsix` to the
[GitHub Release](https://github.com/datamitsu/datamitsu/releases) for manual
installation.
