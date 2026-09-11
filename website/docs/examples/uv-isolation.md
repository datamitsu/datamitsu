---
title: UV Isolation
description: Managing Python tools with UV runtime isolation for conflict-free environments
---

# UV Isolation Patterns

Examples of managing Python tools with UV runtime isolation.

## Basic UV App

A Python linter installed via UV with version pinning:

```typescript
const mapOfApps: BinManager.MapOfApps = {
  yamllint: {
    uv: {
      packageName: "yamllint",
      version: "1.38.0",
      lockFile: "br:...",
    },
  },
};
```

This creates an isolated UV tool environment at:

```
~/.cache/datamitsu/store/.apps/uv/yamllint/{hash}/
```

UV environment variables are set automatically:

- `UV_CACHE_DIR` - UV's download cache (per-app, under the app dir)
- `UV_PYTHON_INSTALL_DIR` - the managed CPython location, redirected into the store at `<store>/.uv/python/` (shared per-version) so the cached store is self-contained and each app's `.venv/bin/python` symlink resolves after a cache restore

## Mandatory Lock File

Every UV app must provide a `lockFile`. Normal config loading fails before any
download when it is absent. The field contains the app's `uv.lock` content:

```typescript
const mapOfApps: BinManager.MapOfApps = {
  yamllint: {
    uv: {
      packageName: "yamllint",
      version: "1.38.0",
      lockFile: "br:...", // brotli-compressed lock file content
    },
  },
};
```

For every UV app:

1. The lock file content is written to the app directory as `uv.lock`
2. UV runs with `--locked --no-build`, refusing to modify `uv.lock` or build an
   sdist
3. Content prefixed with `br:` is brotli-compressed and base64-encoded
4. The lock records hashes for the resolved artifacts, catching changes where the same version resolves to
   different transitive dependencies

To bootstrap one, first declare the package name and version, then run
`datamitsu config lockfile <appName>` and paste its output into `lockFile`. That
command deliberately permits the missing field while generating it; normal
commands do not.

## Multiple Python Tools

Each Python tool gets its own isolated environment:

```typescript
const mapOfApps: BinManager.MapOfApps = {
  yamllint: {
    uv: { packageName: "yamllint", version: "1.37.1", lockFile: "br:..." },
  },
  ruff: {
    uv: { packageName: "ruff", version: "0.8.6", lockFile: "br:..." },
  },
  mypy: {
    uv: { packageName: "mypy", version: "1.14.1", lockFile: "br:..." },
  },
};
```

Store structure:

```
~/.cache/datamitsu/store/.apps/uv/
  yamllint/{hash-a}/    # yamllint + its dependencies
  ruff/{hash-b}/        # ruff + its dependencies
  mypy/{hash-c}/        # mypy + its dependencies
```

No dependency conflicts between tools - each has its own virtual environment.

## System UV Fallback

If you prefer to use a system-installed UV instead of the managed binary:

```typescript
const mapOfRuntimes: BinManager.MapOfRuntimes = {
  uv: {
    kind: "uv",
    mode: "system",
    system: {
      command: "uv",
    },
  },
};
```

This uses the `uv` command from your PATH. The app isolation still works
the same way - only the runtime binary source changes.

## Using UV Tools in Tool Definitions

```typescript
const toolsConfig: config.MapOfTools = {
  yamllint: {
    name: "yamllint",
    operations: {
      lint: {
        app: "yamllint",
        args: ["-f", "parsable", "{files}"],
        scope: "per-file",
        globs: ["**/*.yml", "**/*.yaml"],
      },
    },
  },
};
```
