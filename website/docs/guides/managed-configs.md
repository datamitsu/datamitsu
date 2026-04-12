---
title: Managed Configs
description: Using managed configuration files, symlinks, and the .datamitsu/ directory
---

# Managed Configs

datamitsu can distribute configuration files from runtime-managed apps (FNM/UV) to your project via symlinks. This lets you share tool configurations like ESLint configs, Prettier configs, or any other files that tools need to find at your project root.

## The .datamitsu/ Directory

When you run `datamitsu init`, it creates a `.datamitsu/` directory at your git root containing symlinks to files inside app install directories:

```
project-root/
├── .datamitsu/
│   ├── datamitsu.config.d.ts  # auto-generated type definitions
│   ├── eslint-config → ../.apps/fnm/my-eslint-config/{hash}/dist/eslint.config.js
│   └── prettier-config → ../.apps/fnm/my-prettier-config/{hash}/.prettierrc.json
├── eslint.config.js          # imports from .datamitsu/eslint-config
└── .prettierrc.json          # symlink via ConfigInit
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
    fnm: {
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
    fnm: {
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

## ConfigInit and Root Symlinks

Beyond `.datamitsu/` links, the `init` configuration creates files and symlinks directly in your project:

```javascript
const init = {
  // Write file content
  ".eslintrc.js": {
    content: (context) => `module.exports = { /* ... */ };`,
  },
  // Create a symlink instead of writing content
  ".prettierrc": {
    linkTarget: ".datamitsu/prettier-config",
  },
};
```

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
