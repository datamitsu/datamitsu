---
title: Managed Configs
description: Using managed configuration files, symlinks, and the .datamitsu/ directory
---

# Managed Configs

datamitsu can distribute configuration files from runtime-managed apps (node/UV) to your project via symlinks. This lets you share tool configurations like ESLint configs, Prettier configs, or any other files that tools need to find at your project root.

## The .datamitsu/ Directory

When you run `datamitsu init`, it creates a `.datamitsu/` directory at your git root containing symlinks to files inside the install directories of installed apps:

```
project-root/
├── .datamitsu/
│   ├── datamitsu.config.d.ts  # auto-generated type definitions
│   ├── eslint-config → ../.apps/node/my-eslint-config/{hash}/dist/eslint.config.js
│   └── prettier-config → ../.apps/node/my-prettier-config/{hash}/.prettierrc.json
├── eslint.config.js          # imports from .datamitsu/eslint-config
└── .prettierrc.json          # symlink via ConfigSetup
```

The `.datamitsu/` directory is:

- Recreated atomically on each `datamitsu init` run
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

- **Eager apps (default)** — every app that declares `links` is installed during `datamitsu init`, so its links are created then. This covers apps a tool runs, as well as apps whose links are consumed by git hooks or `setup`-generated files (for example, commitlint, run by the commit-msg hook with its config imported from a `.datamitsu/` symlink).
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
const init = {
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

## ConfigSetup and Root Symlinks

Beyond `.datamitsu/` links, the `setup` configuration creates files and symlinks directly in your project:

```javascript
const init = {
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
[`datamitsu setup --tools <names>`](../reference/cli-commands.md#setup), only the
config files whose `tools` intersect the selected set are regenerated and
everything else is left untouched — ideal for iterating on a single tool's config
without rewriting your whole project. Entries with no `tools` (shared
infrastructure like `.gitignore` or `lefthook.yaml`) are skipped whenever
`--tools` is passed.

The `content()` function receives a context object with:

- `projectTypes` - detected project types in the directory
- `rootPath` - git repository root
- `cwdPath` - current working directory
- `isRoot` - whether cwdPath is the repository root
- `datamitsuDir` - relative path from cwdPath to `{rootPath}/.datamitsu/`
- `existingContent` - previous config layer's generated content for this file (undefined if no prior layer generated content)
- `originalContent` - unmodified content of the file as it exists on disk (available during both config loading and `datamitsu setup`)
- `existingPath` - path to the existing file on disk (only available during `datamitsu setup`, undefined during config loading)

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
