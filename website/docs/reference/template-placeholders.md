---
title: Template Placeholders
description: Reference for template placeholders in tool operation arguments
---

# Template Placeholders

Tool operation arguments support template placeholders that datamitsu resolves before executing the tool. These allow you to reference file paths, project directories, and cache locations dynamically.

## Available Placeholders

| Placeholder   | Resolves To                           | Typical Use Case            |
| ------------- | ------------------------------------- | --------------------------- |
| `{file}`      | Single file path (per-file scope)     | `"{file}"`                  |
| `{files}`     | Separate arguments per file           | `"{files}"`                 |
| `{root}`      | Git repository root                   | `"{root}/.config"`          |
| `{cwd}`       | Per-project working directory         | `"{cwd}/src"`               |
| `{toolCache}` | Per-project, per-tool cache directory | `"{toolCache}/tsbuildinfo"` |

## `{file}`

Expands to the path of a single file. Used in tools with `scope: "per-file"`.

**As entire argument** — expands to a single argument:

```javascript
args: ["{file}"];
// File: "src/main.js"
// Result: ["src/main.js"]
```

**Embedded in a string** — replaced inline:

```javascript
args: ["--input={file}"];
// File: "src/main.js"
// Result: ["--input=src/main.js"]
```

## `{files}`

Expands to multiple file paths. Used in tools with `scope: "per-project"` or `scope: "repository"` when `batch` is enabled.

**As entire argument** — expands to multiple separate arguments:

```javascript
args: ["--check", "{files}"];
// Files: ["src/main.js", "src/util.js"]
// Result: ["--check", "src/main.js", "src/util.js"]
```

**Embedded in a string** — files are joined with spaces:

```javascript
args: ["--files={files}"];
// Files: ["src/main.js", "src/util.js"]
// Result: ["--files=src/main.js src/util.js"]
```

## `{root}`

Expands to the git repository root path. Always resolves to the same value regardless of which project is being processed.

```javascript
args: ["--config", "{root}/.config/tool.yml"];
// Root: "/home/user/repo"
// Result: ["--config", "/home/user/repo/.config/tool.yml"]
```

Use `{root}` to reference shared configuration files or resources at the repository level.

## `{cwd}`

Expands to the per-project working directory. For `scope: "per-project"` tools, this is the detected project root. Falls back to the git root when the project path is empty (e.g., repository-scope tools).

```javascript
args: ["--project", "{cwd}/tsconfig.json"];
// Per-project (packages/frontend): "/home/user/repo/packages/frontend/tsconfig.json"
// Repository scope: "/home/user/repo/tsconfig.json"
```

Use `{cwd}` to reference project-specific files within a monorepo.

## `{toolCache}`

Expands to an isolated, per-project, per-tool cache directory. The path is computed using an XXH3-128 hash of the git root to ensure uniqueness.

```
~/.cache/datamitsu/projects/{xxh3_128(gitRoot)}/cache/{relativeProjectPath}/{toolName}/
```

```javascript
args: ["--cache-dir", "{toolCache}"];
// Tool: "eslint", Project: "packages/frontend"
// Result: ["--cache-dir", "~/.cache/datamitsu/projects/a1b2c3/cache/packages/frontend/eslint/"]
```

Each tool and each project gets its own cache directory, preventing conflicts in monorepos.

If the cache path computation fails, the literal string `{toolCache}` is preserved unchanged.

## Usage in Tool Configuration

Placeholders are used in the `args` field of tool operations:

```javascript
const toolsConfig = {
  prettier: {
    name: "prettier",
    operations: {
      fix: {
        app: "prettier",
        args: ["--write", "--cache", "--cache-location", "{toolCache}", "{files}"],
        globs: ["**/*.{js,ts,json,md}"],
        scope: "per-project",
      },
      lint: {
        app: "prettier",
        args: ["--check", "{files}"],
        globs: ["**/*.{js,ts,json,md}"],
        scope: "per-project",
      },
    },
  },
  "golangci-lint": {
    name: "golangci-lint",
    operations: {
      lint: {
        app: "golangci-lint",
        args: ["run", "--config", "{root}/.golangci.yml", "{cwd}/..."],
        globs: ["**/*.go"],
        scope: "repository",
      },
    },
  },
};
```

## Expansion Order

Placeholders are resolved in this order:

1. `{files}` — expands to multiple arguments or inline
2. `{file}` — expands to single argument or inline
3. `{root}` — string replacement
4. `{cwd}` — string replacement
5. `{toolCache}` — computed and replaced

Multiple placeholders can appear in a single argument. All are resolved in the order above.
