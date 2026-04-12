---
title: PNPM Patterns
description: Managing Node.js tools that require plugins or peer dependencies with PNPM isolation
---

# PNPM Multi-Package Patterns

Examples of managing Node.js tools that require plugins or peer dependencies.

## Basic PNPM App

A simple Node.js tool with no extra dependencies:

```typescript
const mapOfApps: BinManager.MapOfApps = {
  mmdc: {
    fnm: {
      packageName: "@mermaid-js/mermaid-cli",
      binPath: "node_modules/.bin/mmdc",
      version: "11.12.0",
    },
  },
};
```

This generates a `package.json` in the isolated environment:

```json
{
  "dependencies": {
    "@mermaid-js/mermaid-cli": "11.12.0"
  }
}
```

## Apps with Plugin Dependencies

Many Node.js tools rely on plugins installed as siblings in `node_modules`.
Use the `dependencies` field to install them together:

```typescript
const mapOfApps: BinManager.MapOfApps = {
  eslint: {
    fnm: {
      packageName: "eslint",
      binPath: "node_modules/.bin/eslint",
      version: "9.17.0",
      dependencies: {
        "eslint-plugin-import": "2.31.0",
        "eslint-plugin-react": "7.37.3",
        "@typescript-eslint/eslint-plugin": "8.18.2",
        "@typescript-eslint/parser": "8.18.2",
      },
    },
  },
};
```

Generated `package.json`:

```json
{
  "dependencies": {
    "eslint": "9.17.0",
    "eslint-plugin-import": "2.31.0",
    "eslint-plugin-react": "7.37.3",
    "@typescript-eslint/eslint-plugin": "8.18.2",
    "@typescript-eslint/parser": "8.18.2"
  }
}
```

All packages are installed in the same `node_modules`, so ESLint can discover
its plugins through normal Node.js resolution.

## Spectral with Custom Rulesets

Spectral (OpenAPI linter) often needs custom ruleset packages:

```typescript
const mapOfApps: BinManager.MapOfApps = {
  spectral: {
    fnm: {
      packageName: "@stoplight/spectral-cli",
      binPath: "node_modules/.bin/spectral",
      version: "6.14.2",
      dependencies: {
        "@stoplight/spectral-owasp-ruleset": "2.0.1",
      },
    },
  },
};
```

## Lock File for Reproducibility

Pin the exact dependency tree with a lock file:

```typescript
const mapOfApps: BinManager.MapOfApps = {
  eslint: {
    fnm: {
      packageName: "eslint",
      binPath: "node_modules/.bin/eslint",
      version: "10.0.0",
      lockFile: "br:...", // brotli-compressed lock file content
    },
  },
};
```

When `lockFile` is present:

- PNPM runs with `--frozen-lockfile`, refusing to modify `pnpm-lock.yaml`
- The lock file content is written to the app directory before installation
- Content prefixed with `br:` is brotli-compressed and base64-encoded

Without `lockFile`, PNPM resolves dependencies fresh and generates a new lockfile.

To generate lock file content:

1. Run `datamitsu config lockfile <appName>` to generate compressed lock file content
2. Add the output to your config's `lockFile` field

## PNPM Store Isolation

Each app environment has isolated PNPM store paths:

```
~/.cache/datamitsu/.apps/fnm/eslint/{hash}/
  package.json
  pnpm-lock.yaml
  node_modules/
    .bin/eslint          # Executable symlink
    eslint/
    eslint-plugin-*/
  .pnpm-store/           # Content-addressable store (shared dedup)
```

PNPM's content-addressable store means identical packages across apps are
hard-linked rather than duplicated, saving disk space while maintaining
isolation.

## Environment Variables

datamitsu sets these PNPM environment variables for isolation:

- `npm_config_store_dir` - PNPM content-addressable store location
- `npm_config_virtual_store_dir` - Virtual store for the project
- `npm_config_global_dir` - Global packages directory

These prevent any interference with system-level PNPM configurations.
