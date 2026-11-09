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

The extension runs the `datamitsu` on your `PATH`, or the binary selected in Zed
settings. This unpublished development build has no download pins and refuses to
download a server. Published builds must pin a release and verify its archives
against canonical `sha256:<64 lowercase hex>` hashes before extraction.

To run the current checkout instead of an older installed server, build it with
`pnpm build` and point Zed at the resulting binary:

```jsonc
{
  "lsp": {
    "datamitsu": {
      "binary": { "path": "/path/to/datamitsu", "arguments": ["lsp"] },
    },
  },
}
```

`binary.env` overrides the server's environment after binary selection; it does
not change Zed's worktree PATH lookup. Use `binary.path` to select a server that
is not on the worktree's PATH.

The format policy is the language server's `initializationOptions.format`
(`widenTo`, `timeoutMs`, `tools`), set under
`lsp.datamitsu.initialization_options.format`. Zed stops waiting for a format
after five seconds and saves the file, so unless you or
`DATAMITSU_LSP_FORMAT_TIMEOUT_MS` set one, the extension sends `timeoutMs: 3000`:
no further group of fix tools starts after three seconds.

The current server provides formatting, not lint-on-save or editor diagnostics.
Its embedded fallback parser reads standard tool output to keep the execution
cache consistent with CLI runs; that does not publish diagnostics to Zed. Parser
modules declared by a configuration are not loaded by the language server.

Saving a file you have not edited writes the editor's text back after the format,
which undoes the fix on disk. Run `editor: format` on such a file instead.

## Development

- Install it with `zed: install dev extension`, pointing at this directory. Zed
  builds it with rustup's `wasm32-wasip2` target.
- `pnpm --filter @datamitsu/zed test` runs the host unit tests with the lock file.
- `pnpm --filter @datamitsu/zed build` builds the `wasm32-wasip2` component.
- `release.json` refuses downloads until release codegen supplies a version and
  archive pins. With the matching GoReleaser `checksums.txt` already available,
  run `VERSION=v<release> CHECKSUMS_FILE=/path/to/checksums.txt pnpm --filter @datamitsu/zed codegen`.
  The generator normalizes external checksums into canonical `hash` fields;
  missing, bare, uppercase or non-SHA-256 pins are rejected by the extension.
