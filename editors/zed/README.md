# Datamitsu Toolkit for Zed

Format files with your project's [datamitsu](https://github.com/datamitsu/datamitsu)
config. The extension is a thin client around `datamitsu lsp`, the same language
server the VS Code extension runs: a format runs your project's fix tools on the
real file, exactly as `datamitsu fix` does.

## Usage

Route formatting to datamitsu, for example in the repository's `.zed/settings.json`:

```jsonc
{
  "format_on_save": "on",
  "formatter": { "language_server": { "name": "datamitsu" } },
}
```

The extension runs the `datamitsu` on your `PATH`. Without one it downloads the
datamitsu release its build pins and verifies it by SHA-256 before running it. To
run a particular binary:

```jsonc
{
  "lsp": {
    "datamitsu": {
      "binary": { "path": "/path/to/datamitsu", "arguments": ["lsp"] },
    },
  },
}
```

The format policy is the language server's `initializationOptions.format`
(`widenTo`, `timeoutMs`, `tools`), set under
`lsp.datamitsu.initialization_options.format`. Zed stops waiting for a format
after five seconds and saves the file, so unless you or
`DATAMITSU_LSP_FORMAT_TIMEOUT_MS` set one, the extension sends `timeoutMs: 3000`:
no further group of fix tools starts after three seconds.

Saving a file you have not edited writes the editor's text back after the format,
which undoes the fix on disk. Run `editor: format` on such a file instead.

## Development

- Install it with `zed: install dev extension`, pointing at this directory. Zed
  builds it with rustup's `wasm32-wasip2` target.
- `cargo test` runs the unit tests on the host.
- `release.json` is a placeholder that refuses to download anything. To try the
  download, pin a published release:

  ```bash
  gh release download v0.4.0 -R datamitsu/datamitsu -p checksums.txt -D /tmp/datamitsu-v0.4.0
  VERSION=v0.4.0 CHECKSUMS_FILE=/tmp/datamitsu-v0.4.0/checksums.txt node scripts/gen-release.ts
  ```
