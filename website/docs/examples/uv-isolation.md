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
    },
  },
};
```

This creates an isolated UV tool environment at:

```
~/.cache/datamitsu/.apps/uv/yamllint/{hash}/
```

UV environment variables are set automatically:

- `UV_TOOL_DIR` - where UV stores installed tools
- `UV_TOOL_BIN_DIR` - where UV places tool binaries
- `UV_CACHE_DIR` - UV's download cache

## Lock File for Reproducibility

For reproducible installs, provide a `lockFile` with the UV lock file content:

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

When `lockFile` is present:

1. The lock file content is written to the app directory as `uv.lock`
2. UV runs with `--locked`, refusing to modify `uv.lock`
3. Content prefixed with `br:` is brotli-compressed and base64-encoded
4. This catches supply-chain changes where the same version resolves to
   different transitive dependencies

To generate lock file content, run `datamitsu config lockfile <appName>`.

## Multiple Python Tools

Each Python tool gets its own isolated environment:

```typescript
const mapOfApps: BinManager.MapOfApps = {
  yamllint: {
    uv: { packageName: "yamllint", version: "1.37.1" },
  },
  ruff: {
    uv: { packageName: "ruff", version: "0.8.6" },
  },
  mypy: {
    uv: { packageName: "mypy", version: "1.14.1" },
  },
};
```

Store structure:

```
~/.cache/datamitsu/.apps/uv/
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
