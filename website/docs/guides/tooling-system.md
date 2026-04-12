---
title: Tooling System
description: Understanding the fix, lint, and check operations in datamitsu
---

# Tooling System

datamitsu orchestrates your development tools (linters, formatters, type checkers) through a unified system. You define tool operations in your configuration, and datamitsu handles file discovery, task planning, parallel execution, and output formatting.

:::tip Deep Dive
For a deep dive into task planning and execution, see [Architecture](./architecture/index.md).
:::

## Operations

datamitsu supports three operations that you run through CLI commands:

| Command           | Operation | Purpose                                           |
| ----------------- | --------- | ------------------------------------------------- |
| `datamitsu fix`   | fix       | Auto-fix code issues (formatting, import sorting) |
| `datamitsu lint`  | lint      | Report code issues without modifying files        |
| `datamitsu check` | check     | Run fix then lint in sequence                     |

`datamitsu check` is the most common command -- it fixes what it can, then reports remaining issues. If fix fails, lint is skipped.

## Defining Tools

Tools are defined in the `tools` record of your configuration. Each tool has a name and operations:

```javascript
const tools = {
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
  "golangci-lint": {
    name: "golangci-lint",
    projectTypes: ["golang-package"],
    operations: {
      lint: {
        app: "golangci-lint",
        args: ["run", "--timeout", "5m"],
        scope: "repository",
        globs: ["**/*.go"],
      },
    },
  },
};
```

## Scopes

Tools operate at different scopes depending on how they process files:

### Per-Project Scope (with file batching)

The tool runs once per detected project and receives a batch of matching file paths. datamitsu discovers files (by extension), groups them by project, and passes them to the tool:

```javascript
prettier: {
  name: "Prettier",
  operations: {
    fix: {
      app: "prettier",
      args: ["--write", "{files}"],
      scope: "per-project",
      globs: ["**/*.{js,ts,css,md}"],
    },
    lint: {
      app: "prettier",
      args: ["--check", "{files}"],
      scope: "per-project",
      globs: ["**/*.{js,ts,css,md}"],
    },
  },
}
```

The `{files}` placeholder expands to the list of matching files.

### Per-Project Scope (without file lists)

The tool runs once per detected project directory, without receiving file lists:

```javascript
tsc: {
  name: "TypeScript",
  projectTypes: ["npm-package"],
  operations: {
    lint: {
      app: "tsc",
      args: ["--noEmit"],
      scope: "per-project",
      globs: ["**/*.{ts,tsx}"],
    },
  },
}
```

In a monorepo, this runs `tsc` separately in each directory that has a `package.json`.

### Repository Scope

The tool runs once from the git root, regardless of monorepo structure:

```javascript
"golangci-lint": {
  name: "golangci-lint",
  projectTypes: ["golang-package"],
  operations: {
    lint: {
      app: "golangci-lint",
      args: ["run"],
      scope: "repository",
      globs: ["**/*.go"],
    },
  },
}
```

## Template Placeholders

Tool operation arguments support placeholders that datamitsu resolves before execution:

| Placeholder   | Description                            |
| ------------- | -------------------------------------- |
| `{file}`      | Single file path (per-file scope)      |
| `{files}`     | Expands to separate arguments per file |
| `{root}`      | Git repository root                    |
| `{cwd}`       | Per-project working directory          |
| `{toolCache}` | Per-project, per-tool cache directory  |

See the [Template Placeholders reference](/docs/reference/template-placeholders) for detailed usage.

## Per-Operation Environment Variables

Each operation can set environment variables:

```javascript
operations: {
  lint: {
    app: "golangci-lint",
    args: ["run"],
    scope: "per-project",
    globs: ["**/*.go"],
    env: {
      "GOLANGCI_LINT_CACHE": "{toolCache}",
    },
  },
}
```

Environment variables are merged in layers: OS env -> color hints -> app env -> operation env. Later layers override earlier ones.

## Parallel Execution

datamitsu runs tools in parallel across projects. The maximum number of parallel workers is controlled by `DATAMITSU_MAX_PARALLEL_WORKERS` (default: `max(4, floor(NumCPU * 0.75))`, capped at 16).

## Fail-Fast Behavior

When a tool fails, datamitsu immediately cancels all remaining tasks:

1. The failing tool's error is captured
2. A cancellation signal is sent to prevent new tasks from starting
3. Already-running processes are cleaned up via process group signals
4. Only the independent failure is shown -- cascading cancellations are filtered out

This means you see the actual error without noise from tasks that were cancelled as a side effect.

## Output Handling

datamitsu follows a single-print-layer rule:

- Tool executors capture all stdout/stderr silently into results
- The runner is the only component that prints output to the user
- Failed tools show a structured error block with: tool name, scope, directory, command, exit code, and captured output

## Filtering

You can narrow what datamitsu processes:

### By Tool

Run only specific tools:

```bash
datamitsu check --tools eslint,prettier
```

### By Scope

Run only file-scoped tools:

```bash
datamitsu check --file-scoped
```

### By Directory

When you run datamitsu from a subdirectory, it automatically restricts scope:

- Repository-scope tasks are skipped entirely
- Per-project tasks run only for projects within the subdirectory
- Per-file tasks process only files within the subdirectory

## Explain Mode

Use `--explain` to see what datamitsu would run without executing anything:

```bash
datamitsu check --explain
```

This shows the planned tasks, matched files, and commands that would be executed.

## Ignore Rules

You can disable specific tools for certain files or directories using `.datamitsuignore` files or config-defined ignore rules. See the [Ignore Rules reference](/docs/reference/ignore-rules) for details.

## Monorepo Support

datamitsu is designed for monorepos with multiple projects. Each project gets:

- Its own tool execution with isolated working directory
- Its own cache namespace at `~/.cache/datamitsu/projects/{hash}/cache/{projectPath}/{toolName}/`
- Independent results and error reporting

See the [Core Concepts](/docs/getting-started/core-concepts#monorepo-support) page for more on monorepo architecture.

## Bundled Operations

datamitsu includes built-in lint and fix operations for its own file formats:

- `.datamitsuignore` files are automatically formatted during fix operations
- `.datamitsuignore` files are validated during lint operations (unknown tool names produce warnings)

These bundled operations run before your configured tools.
