# UV Isolation

> Managing Python tools with UV runtime isolation for conflict-free environments

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

UV apps receive runtime-owned cache paths from their selected uv runtime:

- `.runtime-data/<runtime-name>/<identity>/uv-cache` stores uv's cache
- `.runtime-data/<runtime-name>/<identity>/python` stores the managed, pinned CPython installation and readiness record

Both paths follow the runtime identity, not the app name. Apps using the same
uv runtime identity share them; changing the runtime name or identity selects a
separate data root. The runtime manager sets these values for installation and
execution, overriding inherited cache variables. `HOME` and the caller's
working directory remain unchanged.

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
5. The install passes back the release-age window the lock recorded, and runs
   with `--no-config` (or the app's own `uv.toml`), so a user's uv settings
   cannot change it. See
   [Release Age of Transitive Dependencies](../guides/supply-chain-security.md#release-age-of-transitive-dependencies)

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
    uv: {
      pythonVersion: "3.15.2",
      pythonDownloads: {/* generated pinned catalog entries */},
    },
  },
};
```

This uses the `uv` command from your PATH to acquire and manage apps. Managed
CPython still comes from the configured `pythonVersion` and SHA-256-pinned
`pythonDownloads`; system mode selects uv itself, not a host Python interpreter.

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
