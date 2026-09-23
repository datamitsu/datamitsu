# Managed Configs

> Using managed configuration files, symlinks, and the .datamitsu/ directory

datamitsu can distribute configuration files from runtime-managed apps (Bun/Node/UV) to your project via symlinks. This lets you share tool configurations like ESLint configs, Prettier configs, or any other files that tools need to find at your project root.

## The .datamitsu/ Directory

When you run `datamitsu init`, it creates a `.datamitsu/` directory at your git root containing symlinks to files inside the install directories of installed apps:

```
project-root/
├── .datamitsu/
│   ├── datamitsu.config.d.ts  # auto-generated type definitions
│   ├── configs/               # ejectable managed configs the project has not ejected
│   ├── eslint-config → {store}/.apps/node/my-eslint-config/{hash}/dist/eslint.config.js
│   └── prettier-config → {store}/.apps/node/my-prettier-config/{hash}/.prettierrc.json
├── eslint.config.js          # imports from .datamitsu/eslint-config
└── .prettierrc.json          # symlink via ManagedConfig
```

The `.datamitsu/` directory is:

- Recreated atomically on each `datamitsu init` run; `configs/` is carried over, and rewritten by init itself
- Listed in `.gitignore` (not committed to the repository)
- Contains an auto-generated `.gitignore` file with `*` as a defensive measure — prevents accidental commits if `.datamitsu/` is not listed in your root `.gitignore`
- Contains a `datamitsu.config.d.ts` file with TypeScript type definitions for IDE autocomplete when editing `datamitsu.config.js`/`.ts`/`.mjs` files
- Verified after creation (symlink existence, correct target, target file exists)

## App Links

Apps declare links using the `links` field, which maps link names to relative paths within the app's install directory:

```javascript
apps: {
  "my-eslint-config": {
    node: {
      packageName: "@myorg/eslint-config",
      version: "2.0.0",
      binPath: "node_modules/.bin/eslint",
      lockFile: "br:...",
    },
    links: {
      "eslint-config": "dist/eslint.config.js",
      "eslint-plugin": "dist/plugin.js",
    },
  },
}
```

This creates symlinks at `.datamitsu/eslint-config` and `.datamitsu/eslint-plugin` pointing to the respective files in the app's install directory.

### When links are created

`.datamitsu/` links follow installation — a link exists only once its source app is installed:

- **Eager apps (default)** — every app that declares `links` is installed during `datamitsu init`, so its links are created then. This covers apps a tool runs, as well as apps whose links are consumed by git hooks or `managedConfigs` entries (for example, commitlint, run by the commit-msg hook with its config imported from a `.datamitsu/` symlink).
- **Lazy apps** — an app that sets `lazy: true` is **not** installed at init. It installs the first time you run it with `datamitsu exec <app>`, and its `.datamitsu/` links are materialized at that point. Use this for user-invoked CLIs whose dependencies are heavy and aren't needed until the app is actually run (for example, a presentation tool like slidev, which would otherwise pull a headless browser at init).

Mark an app `lazy` only when nothing else depends on it being present right after `init` — a tool, hook, or generated config that references the app's link needs the app eager (the default).

### Path Safety

Link paths are validated for security:

- No absolute paths allowed
- No parent directory traversal (`..`)
- No symlink escapes outside the install directory

## App Files

Apps can also include static file content that gets written to the install directory before the package manager runs:

```javascript
apps: {
  "my-tool": {
    node: {
      packageName: "my-tool",
      version: "1.0.0",
      binPath: "node_modules/.bin/my-tool",
      lockFile: "br:...",
    },
    files: {
      ".npmrc": "registry=https://registry.myorg.com\n",
      "tsconfig.json": '{"compilerOptions": {"target": "es2022"}}',
    },
  },
}
```

## App Archives

For distributing full directory trees, apps support archives via the `archives` field:

### Inline Archives

Small directory trees can be embedded directly in the configuration as brotli-compressed tar archives:

```javascript
archives: {
  "config-files": {
    inline: "tar.br:...",  // brotli-compressed, base64-encoded tar
  },
}
```

Generate inline archives with:

```bash
datamitsu devtools pack-inline-archive ./my-config-dir
```

### External Archives

Larger archives can be downloaded from URLs with mandatory SHA-256 hash verification:

```javascript
archives: {
  "config-bundle": {
    url: "https://example.com/config-v2.tar.gz",
    hash: "abc123def456789012345678901234567890123456789012345678901234abcd",
    format: "tar.gz",
  },
}
```

### Extraction Order

1. Archives are extracted first (sorted alphabetically by name; later archives overwrite earlier ones for overlapping paths)
2. Files are written next (always overwrite archive contents)
3. The runtime package manager runs last (`pnpm install` / `uv sync`)

## Using tools.Config.linkPath()

In your JavaScript configuration, use `tools.Config.linkPath()` to compute relative paths from a project directory to a `.datamitsu/` symlink:

```javascript
const managedConfigs = {
  "eslint.config.js": {
    content: (context) => {
      const configPath = tools.Config.linkPath(
        "my-eslint-config", // app name
        "eslint-config", // link name
        context.cwdPath, // from this directory
      );
      return `import config from "${tools.Path.forImport(configPath)}";\nexport default config;\n`;
    },
  },
};
```

The `tools.Config.linkPath()` function validates that the link name exists and belongs to the specified app.

## Using tools.Path APIs

The Path API helps generate correct file paths in configuration files:

```javascript
// Join path segments
tools.Path.join("src", "config", "eslint.js");
// → "src/config/eslint.js"

// Make a path suitable for ES module imports
tools.Path.forImport(tools.Path.join(context.datamitsuDir, "eslint.config.js"));
// → "./.datamitsu/eslint.config.js"
// (forImport adds ./ prefix needed by ES module import statements)
```

`tools.Path.forImport()` ensures relative paths start with `./` or `../`, which JavaScript/TypeScript `import` statements require. It's idempotent -- paths already starting with `./` or `../` are returned unchanged.

## Managed Config Files and Root Symlinks

Beyond `.datamitsu/` links, `managedConfigs` entries describe files and
symlinks owned directly in your project. Preview their reconciliation with
`datamitsu config reconcile --dry-run`; run `datamitsu config reconcile` to
write them and then run `datamitsu fix`.

```javascript
const managedConfigs = {
  // Write file content, associated with the eslint tool
  ".eslintrc.js": {
    tools: ["eslint"],
    content: (context) => `module.exports = { /* ... */ };`,
  },
  // Create a symlink instead of writing content
  ".prettierrc": {
    tools: ["prettier"],
    linkTarget: ".datamitsu/prettier-config",
  },
};
```

Optionally associate an entry with one or more tools via `tools`. When you run
[`datamitsu config reconcile --tools <names>`](../reference/cli-commands.md#config-reconcile), only the
config files whose `tools` intersect the selected set are considered and
everything else is left untouched — ideal for iterating on a single tool's config
without rewriting your whole project. Entries with no `tools` (shared
infrastructure like `.gitignore` or `lefthook.yaml`) are skipped whenever
`--tools` is passed. Add `--dry-run` to preview the scoped plan, or
`--skip-fix` to write it without running the scoped fix afterward.

The `content()` function receives a context object with:

- `projectTypes` - detected project types in the directory
- `projectLocations` - every detected `{ type, path }` pair, relative to the git root; populated during reconciliation
- `rootPath` - git repository root
- `cwdPath` - current working directory
- `isRoot` - whether cwdPath is the repository root
- `datamitsuDir` - relative path from cwdPath to `{rootPath}/.datamitsu/`
- `existingContent` - previous config layer's generated content for this file (undefined if no prior layer generated content)
- `originalContent` - unmodified content of the file as it exists on disk during reconciliation
- `existingPath` - path to the existing file on disk during reconciliation, if it exists
- `placement` - `"repo"` for a file written into the repository, `"internal"` for a render into `.datamitsu/configs/`
- `outputPath`, `outputDir` - where the returned content is written
- `datamitsuDirFromOutput` - relative path from `outputDir` to `{rootPath}/.datamitsu/`, for references the file resolves against its own location

### Keeping tool configs out of the repository

Most tools need their config only to run. An entry marked `ejectable` lives in
`.datamitsu/configs/` instead of the repository, and the tool is handed its path
through the `{managedConfig:<key>}` placeholder:

```javascript
const managedConfigs = {
  ".yamlfmt.yaml": {
    scope: "git-root",
    tools: ["yamlfmt"],
    ejectable: true,
    content: () => "formatter:\n  type: basic\n",
  },
};
// tools.yamlfmt.operations.fix.args: ["-conf", "{managedConfig:.yamlfmt.yaml}", "{files}"]
```

Who writes the file depends on where it lives:

| Placement                                | Written by                   | Changed by                                 |
| ---------------------------------------- | ---------------------------- | ------------------------------------------ |
| `.datamitsu/configs/<key>` (not ejected) | `datamitsu init`             | the configuration; never edit it           |
| `<key>` in the repository (ejected)      | `datamitsu config reconcile` | you, and reconcile's merge on the next run |

A project that needs to change the file — to allow a gitleaks finding, say — ejects
it by naming the tool:

```javascript
return { ...input, ejectConfigs: ["gitleaks"] };
```

then runs `datamitsu config reconcile --tools gitleaks` to write
`.gitleaks.toml` into the repository, where the tool now reads it. Removing the
tool from `ejectConfigs` reverses it: reconciliation deletes the repository copy
if it holds nothing of the project's own, and refuses — naming the file —
if it does. Until the file is where the configuration says, `lint` and `fix`
report which command to run instead of running the tool against a missing or
ignored file.

Only tool operations receive the path. `datamitsu exec <app>`, source-mode
shims, CI actions and editor extensions that run a tool themselves do not, so an
author keeps every file those read by name — `.editorconfig`, ESLint and Prettier
configs, `lefthook.yaml` — in the repository. See the
[configuration reference](../reference/configuration-api.md#keeping-configs-out-of-the-repository-ejectable--ejectconfigs)
for the exact rules.

### Detecting upstream drift (`expectChainHash`)

Configs are produced by **layering**: a shared config (plus any remote/before
layers) generates a baseline, and your root config — at the git root, or an
explicit `--config` — runs last on top, where you apply project-specific
overrides. Those overrides are written against a particular baseline. If the
shared config later changes, a plain `datamitsu config reconcile` re-derives the file and can
silently overwrite your tweaks.

Set `expectChainHash` on a root-layer entry to pin the baseline your overrides
assume — the XXH3-128 hash of the content entering your layer, _before_ your layer
transforms it:

```javascript
const managedConfigs = {
  "eslint.config.mjs": {
    expectChainHash: "xxh3:0a1b2c3d4e5f60718293a4b5c6d7e8f9",
    content: (context) => {
      /* overrides layered on top of the shared config */
    },
  },
};
```

`datamitsu config reconcile` recomputes that hash during both dry-runs and
normal reconciliation.
If it still matches, reconciliation proceeds; if the upstream chain **drifted**, reconciliation aborts **before writing
anything** and prints the new hash and incoming content so you can reconcile your
overrides and update the pin. The check is opt-in per file, honoured only on the
root layer, byte-for-byte (no normalization), and can be bypassed with
`datamitsu config reconcile --no-verify-hash`.

To get the initial value, declare the entry (a placeholder pin is fine) and read
the real hash with `datamitsu config chain-hash <file>` — it prints exactly what
the gate checks, so no fake-failure dance is needed. See the
[`expectChainHash` reference](../reference/configuration-api.md#pinning-the-upstream-chain-expectchainhash)
for the full contract.

:::note Which files to pin
Configs whose handler folds your own on-disk file into the result (e.g. a YAML
linter config merged with what you already have) will see the hash move when
**you** edit the file too, not just on upstream changes. Pinning is most useful
for files generated from a fixed template (the import-style shims like
`eslint.config.mjs`, `prettier.config.mjs`), where the incoming content is a pure
function of the upstream version.
:::

:::tip Architecture
Learn more about how datamitsu plans and schedules tool execution in [Task Planning](./architecture/planner.md).
:::

## Smart Init

`datamitsu init` uses smart initialization: it scans your tool definitions to find which apps are actually referenced, then installs only those apps that have Links defined. You don't need to mark apps as `required: true` for their links to work.

Use `--all` to download all configured binaries regardless of usage:

```bash
datamitsu init --all
```

## Windows Support

On Windows, symlinks require Developer Mode to be enabled. datamitsu uses symlinks only -- there is no fallback to file copies.
