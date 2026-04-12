---
title: Multiple Tool Versions
description: Run multiple versions of the same tool side-by-side in a monorepo using isolated environments
---

# Multiple Tool Versions

This example shows how to run ESLint v10 for most of your codebase while
keeping ESLint v9 with legacy plugins for a specific subdirectory.

## The Problem

A monorepo has modern code using flat config (`eslint.config.ts`) and a legacy
module still on `.eslintrc.js` with plugins that haven't been updated for
ESLint v10. You need both versions to coexist without interference.

## Configuration

### Runtimes

Define a managed FNM runtime. Both ESLint versions will share the same
runtime binary but get completely isolated `node_modules` directories.

```typescript
const mapOfRuntimes: BinManager.MapOfRuntimes = {
  fnm: {
    kind: "fnm",
    mode: "managed",
    fnm: {
      nodeVersion: "22.14.0",
      pnpmVersion: "10.5.2",
      pnpmHash: "<sha256>",
    },
    managed: {
      binaries: {
        darwin: {
          amd64: {
            contentType: "zip",
            hash: "<sha256>",
            url: "https://github.com/Schniz/fnm/releases/download/v1.39.0/fnm-macos.zip",
            binaryPath: "fnm",
          },
          arm64: {
            contentType: "zip",
            hash: "<sha256>",
            url: "https://github.com/Schniz/fnm/releases/download/v1.39.0/fnm-macos.zip",
            binaryPath: "fnm",
          },
        },
        linux: {
          amd64: {
            contentType: "zip",
            hash: "<sha256>",
            url: "https://github.com/Schniz/fnm/releases/download/v1.39.0/fnm-linux.zip",
            binaryPath: "fnm",
          },
        },
      },
    },
  },
};
```

### Apps

Define two separate apps for the two ESLint versions. Each gets its own
isolated environment because the version (and dependencies) differ, producing
different cache hashes.

```typescript
const mapOfApps: BinManager.MapOfApps = {
  // Modern ESLint v10 - no extra plugins needed
  eslint: {
    fnm: {
      packageName: "eslint",
      binPath: "node_modules/.bin/eslint",
      version: "10.0.0",
    },
  },

  // Legacy ESLint v9 with plugins that don't support v10 yet
  "eslint-legacy": {
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

### Tools

Configure tools to target different file globs. The modern ESLint runs on
everything except the legacy module; the legacy version runs only on
`old-module/`.

```typescript
const toolsConfig: config.MapOfTools = {
  eslint: {
    name: "ESLint (Modern)",
    operations: {
      fix: {
        app: "eslint",
        args: ["--fix", "{files}"],
        scope: "per-project",
        globs: ["**/*.{ts,tsx,js,jsx}"],
        // Excludes handled by eslint.config.ts ignores
      },
      lint: {
        app: "eslint",
        args: ["{files}"],
        scope: "per-project",
        globs: ["**/*.{ts,tsx,js,jsx}"],
      },
    },
    projectTypes: ["npm-package"],
  },

  "eslint-legacy": {
    name: "ESLint (Legacy)",
    operations: {
      fix: {
        app: "eslint-legacy",
        args: ["--fix", "{files}"],
        scope: "per-project",
        globs: ["old-module/**/*.{js,jsx}"],
      },
      lint: {
        app: "eslint-legacy",
        args: ["{files}"],
        scope: "per-project",
        globs: ["old-module/**/*.{js,jsx}"],
      },
    },
    projectTypes: ["npm-package"],
  },
};
```

## How Isolation Works

Each app's environment is stored under a hash-based path:

```
~/.cache/datamitsu/.apps/fnm/
  eslint/{hash-for-v10}/
    package.json          # {"dependencies": {"eslint": "10.0.0"}}
    node_modules/
      .bin/eslint         # ESLint v10 binary
      eslint/

  eslint-legacy/{hash-for-v9}/
    package.json          # {"dependencies": {"eslint": "9.17.0", ...plugins}}
    node_modules/
      .bin/eslint         # ESLint v9 binary
      eslint/
      eslint-plugin-import/
      eslint-plugin-react/
      @typescript-eslint/
```

The hash is computed from: `app name + version + sorted dependencies + runtime hash`.
Changing any of these inputs produces a new hash and a fresh environment.

## Cache Key Uniqueness

These configurations produce different cache keys because they differ in
version and dependencies:

| App           | Version | Dependencies | Hash        |
| ------------- | ------- | ------------ | ----------- |
| eslint        | 10.0.0  | (none)       | `a1b2c3...` |
| eslint-legacy | 9.17.0  | 4 plugins    | `d4e5f6...` |

Even if you had two apps with the same version but different dependencies,
they'd still get separate environments.

## Verification

After running `datamitsu init --all`:

```bash
# Both versions are installed in separate directories
ls ~/.cache/datamitsu/.apps/fnm/eslint/
ls ~/.cache/datamitsu/.apps/fnm/eslint-legacy/

# Each has its own node_modules with the correct version
datamitsu exec eslint -- --version
# v10.0.0

datamitsu exec eslint-legacy -- --version
# v9.17.0
```

## Different Runtime Versions

You can also use different runtime versions for the same tool type by defining
multiple runtimes:

```typescript
const mapOfRuntimes: BinManager.MapOfRuntimes = {
  "fnm-node20": {
    kind: "fnm",
    mode: "managed",
    fnm: {
      nodeVersion: "20.18.0",
      pnpmVersion: "10.5.2",
      pnpmHash: "abc123def456789012345678901234567890123456789012345678901234",
    },
    managed: {
      binaries: {
        // FNM binaries...
      },
    },
  },
  "fnm-node22": {
    kind: "fnm",
    mode: "managed",
    fnm: {
      nodeVersion: "22.12.0",
      pnpmVersion: "10.5.2",
      pnpmHash: "abc123def456789012345678901234567890123456789012345678901234",
    },
    managed: {
      binaries: {
        // FNM binaries...
      },
    },
  },
};

const mapOfApps: BinManager.MapOfApps = {
  eslint: {
    fnm: {
      packageName: "eslint",
      binPath: "node_modules/.bin/eslint",
      version: "10.0.0",
      runtime: "fnm-node22", // Explicit runtime reference
    },
  },
  "eslint-legacy": {
    fnm: {
      packageName: "eslint",
      binPath: "node_modules/.bin/eslint",
      version: "9.17.0",
      runtime: "fnm-node20", // Uses the older Node.js
    },
  },
};
```

When `runtime` is omitted, datamitsu uses the default runtime of the matching
kind (the first `fnm`-kind runtime it finds). Use explicit `runtime` references
when you need a specific version.
