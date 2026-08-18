---
title: Template Placeholders
description: Reference for template placeholders in tool operation arguments and environment values
---

# Template Placeholders

Tool operation **arguments** (`args`) and **environment-variable values** (`env`) support template placeholders that datamitsu resolves before executing the tool. These let you reference file paths, project directories, and cache locations dynamically.

## Available Placeholders

| Placeholder   | Resolves To                           | `args` | `env` | Typical Use Case            |
| ------------- | ------------------------------------- | :----: | :---: | --------------------------- |
| `{file}`      | Single file path (per-file scope)     |   ✅   |  ❌   | `"{file}"`                  |
| `{files}`     | Separate arguments per file           |   ✅   |  ❌   | `"{files}"`                 |
| `{root}`      | Git repository root                   |   ✅   |  ✅   | `"{root}/.config"`          |
| `{cwd}`       | Per-project working directory         |   ✅   |  ✅   | `"{cwd}/src"`               |
| `{toolCache}` | Per-project, per-tool cache directory |   ✅   |  ✅   | `"{toolCache}/tsbuildinfo"` |
| `{target}`    | The single directory a scanner scans  |   ✅   |  ❌   | `"{target}"`                |

`{file}`, `{files}` and `{target}` are argument concepts and are **not**
available in `env` values (an environment variable holds a single string, not an
argument list). The path placeholders `{root}`, `{cwd}` and `{toolCache}` work in
both `args` and `env`.

:::warning Unknown placeholders are a hard error
datamitsu validates every `{placeholder}` in tool `args` and `env` at config-load
time. If you use a token that datamitsu does not substitute (a typo like
`{toolcache}`, an unsupported name like `{cache}`, or an `args`-only placeholder
such as `{file}` inside `env`), config loading **fails immediately** with a clear
message naming the tool, operation, and offending token — datamitsu never passes an
unsubstituted placeholder through to the tool. Shell brace groups (`{js,ts}`) and
Go templates (`{{.Path}}`) are not treated as placeholders and are left untouched.
:::

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

## Usage in environment values

The path placeholders also expand in `env` values, which is the correct way to
point a tool at its datamitsu-managed cache:

```javascript
const toolsConfig = {
  "golangci-lint": {
    name: "golangci-lint",
    operations: {
      lint: {
        app: "golangci-lint",
        args: ["run", "--allow-parallel-runners"],
        scope: "per-project",
        env: {
          // Resolves to an absolute, per-project cache path.
          GOLANGCI_LINT_CACHE: "{toolCache}",
        },
      },
    },
    projectTypes: ["golang-package"],
  },
};
```

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

## `{target}`

Marks the one directory a tool walks itself, as opposed to a list of files you
hand it. Scanners want this: `gitleaks dir`, `trivy fs`, `osv-scanner` all take a
path and do their own traversal.

```js
args: ["dir", "--redact", "--config", "{root}/.gitleaks.toml", "{target}"];
```

It exists because `{root}` is ambiguous. In `--config {root}/.gitleaks.toml` the
root is a path the tool _reads_; in `dir {root}` it is the thing it _scans_, and
nothing in the argument list distinguishes them. `{target}` says which one you
mean, and that is what lets datamitsu infer the operation's [arity](#arity)
exactly rather than guessing.

Handing a directory-scanning tool `{files}` instead is a silent mistake, not a
loud one. `gitleaks dir` accepts exactly one path and, given more, discards them
all and scans the working directory — same exit code, no warning, and your glob
and ignore rules never reached it.

`{target}` and `{file}`/`{files}` are mutually exclusive: a directory argument
and a file list are different shapes, and declaring both is a config error.

## Expansion Order

Placeholders are resolved in this order:

1. `{files}` — expands to multiple arguments or inline
2. `{file}` — expands to single argument or inline
3. `{root}` — string replacement
4. `{cwd}` — string replacement
5. `{toolCache}` — computed and replaced

Multiple placeholders can appear in a single argument. All are resolved in the order above.
