---
title: Configure Linters
description: How to set up and configure linters and formatters with datamitsu
---

# Configure Linters

This guide shows how to set up common linters and formatters in datamitsu's tooling system.

## Setting Up golangci-lint

golangci-lint is a Go linter aggregator. Since it operates on Go modules rather than individual files, configure it with `scope: "repository"`.

### 1. Ensure the app is defined

The default configuration includes golangci-lint as a binary app. If you need a custom version, override it in your config:

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  return {
    ...config,
    apps: {
      ...config.apps,
      "golangci-lint": {
        binary: {
          binaries: {
            // platform-specific URLs and hashes
          },
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

### 2. Define the tool

```javascript
const tools = {
  ...config.tools,
  "golangci-lint": {
    name: "golangci-lint",
    projectTypes: ["golang-package"],
    operations: {
      lint: {
        app: "golangci-lint",
        args: ["run", "--timeout", "5m"],
        scope: "repository",
        globs: ["**/*.go"],
        env: {
          GOLANGCI_LINT_CACHE: "{toolCache}",
        },
      },
    },
  },
};
```

Key points:

- `scope: "repository"` runs it once from the git root, not per-project
- `{toolCache}` gives golangci-lint an isolated cache directory
- Only a `lint` operation is defined since golangci-lint doesn't auto-fix

## Setting Up ESLint via FNM

ESLint is a JavaScript/TypeScript linter managed through the FNM runtime.

### 1. Define the app

```javascript
apps: {
  ...config.apps,
  eslint: {
    fnm: {
      packageName: "eslint",
      version: "9.0.0",
      binPath: "node_modules/.bin/eslint",
      lockFile: "br:...",  // generated via: datamitsu config lockfile eslint
    },
    links: {
      "eslint-config": "dist/eslint.config.js",
    },
  },
}
```

### 2. Define the tool

```javascript
const tools = {
  ...config.tools,
  eslint: {
    name: "ESLint",
    projectTypes: ["npm-package"],
    operations: {
      fix: {
        app: "eslint",
        args: ["--fix", "{files}"],
        scope: "per-project",
        globs: ["**/*.{js,ts,jsx,tsx}"],
      },
      lint: {
        app: "eslint",
        args: ["{files}"],
        scope: "per-project",
        globs: ["**/*.{js,ts,jsx,tsx}"],
      },
    },
  },
};
```

### 3. Reference the config link

In your project's ESLint config, use `tools.Config.linkPath()` to reference the managed configuration:

```javascript
// In a ConfigInit content function
const eslintConfigPath = tools.Path.forImport(
  tools.Config.linkPath("eslint", "eslint-config", context.cwdPath),
);
```

## Setting Up Ruff via UV

Ruff is a fast Python linter and formatter managed through the UV runtime.

### 1. Define the app

```javascript
apps: {
  ...config.apps,
  ruff: {
    uv: {
      packageName: "ruff",
      version: "0.3.0",
      lockFile: "br:...",  // generated via: datamitsu config lockfile ruff
    },
  },
}
```

### 2. Define the tool

```javascript
const tools = {
  ...config.tools,
  ruff: {
    name: "Ruff",
    projectTypes: ["python"],
    operations: {
      fix: {
        app: "ruff",
        args: ["format", "{files}"],
        scope: "per-project",
        globs: ["**/*.py"],
      },
      lint: {
        app: "ruff",
        args: ["check", "{files}"],
        scope: "per-project",
        globs: ["**/*.py"],
      },
    },
  },
};
```

## Configuring Ignore Rules

Use ignore rules to skip tools for specific files or directories.

### Using .datamitsuignore files

Create a `.datamitsuignore` file in any directory:

```bash
# Disable all tools for vendor code
vendor/**/*: *

# Disable prettier for auto-generated files
**/*.generated.ts: prettier

# Disable eslint for test fixtures
tests/fixtures/**/*: eslint, prettier
```

### Using config-defined ignore rules

Add ignore rules directly in your configuration:

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  return {
    ...config,
    ignoreRules: [
      "vendor/**/*: *",
      "**/*.generated.go: golangci-lint, gofmt",
      "!vendor/patches/**/*: *", // re-enable for patches
    ],
  };
}
globalThis.getConfig = getConfig;
```

### Re-enabling tools

Use the `!` prefix to re-enable tools disabled by broader rules:

```bash
# Disable eslint for all markdown
**/*.md: eslint

# But re-enable for documentation
!docs/**/*.md: eslint
```

See the [Ignore Rules reference](/docs/reference/ignore-rules) for full syntax details.

## Previewing Tool Execution

Before running tools, use `--explain` to see what would execute:

```bash
# Summary of planned tasks
datamitsu check --explain

# Detailed breakdown
datamitsu check --explain detailed

# Machine-readable output
datamitsu check --explain json
```

## Running Specific Tools

Use `--tools` to run only certain tools:

```bash
# Run only ESLint and Prettier
datamitsu check --tools eslint,prettier

# Fix only with Ruff
datamitsu fix --tools ruff
```

## Git Hook Integration

Use `--file-scoped` to check only staged files in a pre-commit hook:

```bash
datamitsu check --file-scoped
```

This pairs well with tools like lefthook for git hook management.
