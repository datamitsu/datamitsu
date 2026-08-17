---
title: Configuration API
description: Complete reference for the datamitsu configuration API
---

# Configuration API

datamitsu uses JavaScript configuration files powered by the [goja](https://github.com/dop251/goja) JavaScript runtime. Configuration is defined in `datamitsu.config.js`, `datamitsu.config.mjs`, or `datamitsu.config.ts` at your git repository root.

## Required Exports

Every config file must export:

- **`getMinVersion()`** — Returns a semver string specifying the minimum datamitsu version required. Checked before `getConfig()` is called. Configs without this function fail to load.
- **`getConfig(config)`** — Receives the previous layer's config and returns a new `Config` object.
- **`getRemoteConfigs()`** _(optional)_ — Returns remote parent configs to load before this config.
- **`getBeforeConfigs()`** _(optional)_ — Returns local config files to load as under-layers, the config-declared equivalent of the `--before-config` flag. Honoured **only** in the auto-discovered git-root config, and **not evaluated** when a `--before-config` flag is passed (the flag wins).

```typescript
function getMinVersion(): string;
function getConfig(config: Config): Config;
function getRemoteConfigs(): Array<{ url: string; hash: string }>;
function getBeforeConfigs(): Array<{ path: string }>;
```

The special version `"dev"` (used when running from source) is treated as `v0.0.0`.

## Config Structure

The `getConfig()` function returns a `Config` object:

```typescript
interface Config {
  apps?: BinManager.MapOfApps;
  runtimes?: BinManager.MapOfRuntimes;
  bundles?: Record<string, Bundle>;
  tools?: MapOfTools;
  parsers?: Record<string, Parser>;
  lsp?: Record<string, LspProxy | LspDerived>;
  projectTypes?: MapOfProjectTypes;
  setup?: MapOfConfigSetup;
  initCommands?: MapOfInitCommands;
  ignoreRules?: string[];
  sharedStorage?: Record<string, string>;
  oci?: OCIRef;
}
```

## Config Loading Order

Configuration is loaded in layers, each receiving the previous result as input:

```
default (embedded config.js)
  ↓ [getRemoteConfigs() resolved depth-first, if exported]
--before-config flags  (for wrappers/libraries)
  ↓ [getRemoteConfigs() resolved depth-first]
declared before-configs  (auto config's getBeforeConfigs(), only when no --before-config flag)
  ↓ [getRemoteConfigs() resolved depth-first]
auto (datamitsu.config.{js,mjs,ts} at git root)
  ↓ [getRemoteConfigs() resolved depth-first]
--config flags  (for CI/testing overrides)
  ↓
final Config
```

- Each source must export `getMinVersion()` — version is checked before `getConfig()` runs (fail-fast)
- Each source can export `getRemoteConfigs()` returning `Array<{url: string, hash: string}>` for recursive parent resolution
- The auto git-root config can export `getBeforeConfigs()` returning `Array<{path: string}>` to load local files as under-layers (parity with `--before-config`). It is honoured only at the git-root layer and skipped entirely when a `--before-config` flag is present; relative paths resolve against the git-root config's directory, no hash is required (local files are not downloads)
- `ignoreRules` use append semantics across config layers
- Circular remote config dependencies are detected and produce an error

## Apps (`apps`)

Apps define the tools datamitsu manages. The app kind is determined by which sub-object is present (`binary`, `uv`, `node`, `jvm`, `go`, or `shell`).

### App Kinds

| Kind     | Sub-object | Description                                       |
| -------- | ---------- | ------------------------------------------------- |
| `binary` | `binary`   | Self-managed binaries downloaded from URLs        |
| `uv`     | `uv`       | Python packages installed via managed UV runtime  |
| `node`   | `node`     | npm packages installed via managed Node.js + pnpm |
| `jvm`    | `jvm`      | Java applications executed via managed JDK        |
| `go`     | `go`       | Go tools built from source via managed Go SDK     |
| `shell`  | `shell`    | Shell commands with custom environment            |

### Common App Fields

All app kinds share these optional fields:

```typescript
interface AppCommon {
  required?: boolean; // Whether the app is required for init
  files?: Record<string, string>; // filename → static content
  links?: Record<string, string>; // linkName → relativePath in install dir
  archives?: Record<string, ArchiveSpec>; // name → archive specification
  env?: Record<string, string>; // Custom environment variables (all app kinds)
  versionCheck?: {
    disabled?: boolean; // Skip version check in verify-all
    args?: string[]; // Override default ["--version"] args
  };
}
```

#### Custom environment variables (`env`)

The optional `env` field applies to **every** app kind (binary, uv, node, jvm,
go, shell). It is injected both at **install time** (for the uv/node/go
dependency-install phase) and at **run time** (every app type).

Values support placeholder expansion, performed in Go and never written into the
committed config:

- `${STORE}` → the shared datamitsu store path (cleaned by `datamitsu store clear`).
- `${APP_DIR}` → this app's install directory (per-app, config-hashed).

**Precedence:** any key already set by datamitsu or the runtime wins. A user
config can never relocate the pnpm store, uv cache, `GOPATH`, etc.

```javascript
const apps = {
  slidev: {
    node: {
      packageName: "@slidev/cli",
      // ...
    },
    // Redirect playwright to download browsers into the datamitsu store
    env: { PLAYWRIGHT_BROWSERS_PATH: "${STORE}/.playwright/browsers" },
  },
};
```

> **Migration (alpha breaking change):** `env` was previously a field on
> `AppConfigShell` (`shell.env`). It has moved to the top-level `env` field
> shared by all app kinds. Move any `shell: { env: {...} }` up one level to
> `env: {...}` on the app.

### Binary Apps

Binary apps download platform-specific executables with hash verification.

```javascript
const apps = {
  "golangci-lint": {
    binary: {
      version: "2.1.0",
      binaries: {
        linux: {
          amd64: {
            glibc: {
              url: "https://github.com/golangci/golangci-lint/releases/download/v2.1.0/golangci-lint-2.1.0-linux-amd64.tar.gz",
              hash: "abc123...", // SHA-256 (mandatory)
              contentType: "tar.gz",
              binaryPath: "golangci-lint-2.1.0-linux-amd64/golangci-lint",
            },
          },
        },
        darwin: {
          arm64: {
            unknown: {
              url: "https://github.com/golangci/golangci-lint/releases/download/v2.1.0/golangci-lint-2.1.0-darwin-arm64.tar.gz",
              hash: "def456...",
              contentType: "tar.gz",
              binaryPath: "golangci-lint-2.1.0-darwin-arm64/golangci-lint",
            },
          },
        },
      },
    },
  },
};
```

**Binary-specific fields:**

```typescript
interface AppConfigBinary {
  version?: string;
  binaries: Partial<
    Record<OsType, Partial<Record<ArchType, Partial<Record<LibcType, BinaryOsArchInfo>>>>>
  >;
}

interface BinaryOsArchInfo {
  url: string;
  hash: string; // SHA-256 hash (mandatory)
  contentType: BinContentType;
  binaryPath?: string; // Path to binary within archive
  extractDir?: boolean; // Extract entire archive to directory
}
```

The `binaries` map uses a three-level nested structure: `os → arch → libc → BinaryOsArchInfo`. Linux platforms use `"glibc"` or `"musl"` as the libc key; non-Linux platforms use `"unknown"`.

**Supported platforms:** `darwin/amd64/unknown`, `darwin/arm64/unknown`, `linux/amd64/glibc`, `linux/amd64/musl`, `linux/arm64/glibc`, `linux/arm64/musl`, `freebsd/amd64/unknown`, `openbsd/amd64/unknown`, `windows/amd64/unknown`, `windows/arm64/unknown`

**Supported archive types:** `tar.gz`, `tar.xz`, `tar.bz2`, `tar.zst`, `tar`, `zip`, `gz`, `bz2`, `xz`, `zst`, `binary`

### UV Apps (Python)

UV apps install Python packages in isolated environments using the managed UV runtime.

```javascript
const apps = {
  yamllint: {
    uv: {
      packageName: "yamllint",
      version: "1.35.1",
      requiresPython: ">=3.12", // optional, defaults to ">=3.12"
      lockFile: "br:...", // brotli-compressed lock file (required)
      runtime: "uv-default", // optional runtime override
    },
  },
};
```

**UV-specific fields:**

```typescript
interface AppConfigUV {
  packageName: string;
  version: string;
  requiresPython?: string; // Defaults to ">=3.12"
  lockFile: string; // Brotli-compressed with "br:" prefix (required)
  runtime?: string; // Runtime name override
}
```

### Node Apps (Node.js/npm)

Node apps install npm packages using the managed Node.js runtime and pnpm.

```javascript
const apps = {
  eslint: {
    node: {
      packageName: "eslint",
      version: "9.27.0",
      binPath: "node_modules/.bin/eslint",
      dependencies: {
        "typescript-eslint": "^8.32.0",
        "@eslint/js": "^9.27.0",
      },
      lockFile: "br:...", // brotli-compressed lock file (required)
      runtime: "node-default", // optional runtime override
    },
    links: {
      "eslint-config": "dist/eslint.config.js",
    },
  },
};
```

**Node-specific fields:**

```typescript
interface AppConfigNode {
  binPath: string; // Path to binary (e.g., "node_modules/.bin/eslint")
  packageName: string;
  version: string;
  lockFile: string; // Brotli-compressed with "br:" prefix (required)
  dependencies?: Record<string, string>;
  runtime?: string; // Runtime name override
}
```

### JVM Apps (Java)

JVM apps download JAR files and execute them via a managed JDK.

```javascript
const apps = {
  "openapi-generator-cli": {
    jvm: {
      version: "7.12.0",
      jarUrl:
        "https://repo1.maven.org/maven2/org/openapitools/openapi-generator-cli/7.12.0/openapi-generator-cli-7.12.0.jar",
      jarHash: "abc123...", // SHA-256 (mandatory)
      runtime: "jvm-default",
    },
  },
};
```

**JVM-specific fields:**

```typescript
interface AppConfigJVM {
  version: string;
  jarUrl: string;
  jarHash: string; // SHA-256 hash (mandatory)
  mainClass?: string; // When set, uses java -cp instead of java -jar
  runtime?: string; // Runtime name override
}
```

### Go Apps

Go apps build a command-line tool from source using the managed Go SDK. The lock file carries both `go.mod` and `go.sum` so the build is reproducible and hash-verified.

```javascript
const apps = {
  govulncheck: {
    go: {
      packageName: "golang.org/x/vuln/cmd/govulncheck",
      version: "v1.3.0",
      lockFile: "br:...", // brotli-compressed lock file (required)
      runtime: "go-default", // optional runtime override
    },
  },
};
```

**Go-specific fields:**

```typescript
interface AppConfigGo {
  packageName: string; // Go import path, e.g. "golang.org/x/vuln/cmd/govulncheck"
  version: string; // Module version query, e.g. "v1.3.0"
  lockFile: string; // Brotli-compressed JSON {"mod","sum"} with "br:" prefix (required)
  runtime?: string; // Runtime name override
}
```

The tool is built with `go build -trimpath -mod=readonly` in an isolated, hardened environment. See the [Supply Chain Security](../guides/supply-chain-security.md#go-go-apps) guide for the full defense model.

### Shell Apps

Shell apps wrap system commands. Set environment variables through the shared
top-level [`env` field](#custom-environment-variables-env).

```javascript
const apps = {
  "my-script": {
    shell: {
      name: "bash",
      args: ["-c", "echo hello"],
    },
    env: { MY_VAR: "value" },
  },
};
```

**Shell-specific fields:**

```typescript
interface AppConfigShell {
  name: string; // Command name
  args?: string[]; // Default arguments
}
```

## Bundles (`bundles`)

Bundles store static content (files, archives) in a hash-keyed cache directory and expose it through `.datamitsu/` symlinks. Unlike apps, bundles are not executable.

```typescript
interface Bundle {
  version?: string;
  files?: Record<string, string>;
  archives?: Record<string, ArchiveSpec>;
  links?: Record<string, string>;
}
```

| Field      | Type                          | Description                                             |
| ---------- | ----------------------------- | ------------------------------------------------------- |
| `version`  | `string`                      | Version identifier (changes trigger cache invalidation) |
| `files`    | `Record<string, string>`      | Filename to content mapping                             |
| `archives` | `Record<string, ArchiveSpec>` | Named archives (inline or external)                     |
| `links`    | `Record<string, string>`      | Link name to relative path in install dir               |

A bundle must have at least `files` or `archives`. Link names must be unique across both apps and bundles.

Link values can point to files or directories within the install dir. Use `"."` to link to the entire bundle directory.

**Example:**

```javascript
const bundles = {
  "agent-skills": {
    version: "1.0",
    files: {
      "agents.md": "# Agent instructions...",
      "skills/search.md": "# Search skill...",
    },
    links: {
      "agent-skills-dir": ".", // link to entire bundle directory
      "agents-md": "agents.md", // link to a single file
    },
  },
};
```

Install path: `{cache}/.bundles/{name}/{hash}/`

See [Managed Content (Bundles)](../guides/managed-content.md) for a full guide.

## Runtimes (`runtimes`)

Runtimes define how language-specific package managers are provisioned.

```typescript
interface RuntimeConfig {
  kind: "node" | "uv" | "jvm" | "go";
  mode: "managed" | "system";
  managed?: RuntimeConfigManaged; // Required for managed mode
  system?: RuntimeConfigSystem; // For system mode
  node?: RuntimeConfigNode; // Required when kind is "node"
  uv?: RuntimeConfigUV; // When kind is "uv"
  jvm?: RuntimeConfigJVM; // Required when kind is "jvm"
  go?: RuntimeConfigGo; // Required when kind is "go"
}
```

### Managed Mode

In managed mode, datamitsu downloads the runtime binary itself:

```javascript
const runtimes = {
  "node-default": {
    kind: "node",
    mode: "managed",
    managed: {
      binaries: {
        linux: {
          amd64: {
            glibc: {
              url: "https://nodejs.org/dist/v26.2.0/node-v26.2.0-linux-x64.tar.xz",
              hash: "abc123...", // SHA-256 (mandatory)
              contentType: "tar.xz",
              binaryPath: "node-v26.2.0-linux-x64/bin/node",
              extractDir: true,
            },
          },
        },
        // ... other platforms
      },
    },
    node: {
      nodeVersion: "26.2.0",
      pnpmVersion: "11.20.0",
      pnpmHash: "def456...", // SHA-256 of pnpm package
    },
  },
};
```

### System Mode

In system mode, the runtime uses the system-installed version:

```javascript
const runtimes = {
  "uv-system": {
    kind: "uv",
    mode: "system",
    system: {
      command: "uv",
      systemVersion: "1.0", // Bump to invalidate cache
    },
    uv: {
      pythonVersion: "3.12",
    },
  },
};
```

### Runtime Kind Configuration

**Node Runtime:**

```typescript
interface RuntimeConfigNode {
  nodeVersion: string; // e.g., "26.2.0"
  pnpmVersion: string; // e.g., "11.20.0"
  pnpmHash: string; // SHA-256 of pnpm package (mandatory)
}
```

**UV Runtime:**

```typescript
interface RuntimeConfigUV {
  pythonVersion?: string; // e.g., "3.14.3"
}
```

**JVM Runtime:**

```typescript
interface RuntimeConfigJVM {
  javaVersion: string; // e.g., "25"
}
```

**Go Runtime:**

```typescript
interface RuntimeConfigGo {
  goVersion: string; // Go SDK version to build with, e.g., "1.26.3"
}
```

## Tools (`tools`)

Tools define fix and lint operations that datamitsu executes.

```typescript
interface Tool {
  name: string;
  operations: Partial<Record<"fix" | "lint", ToolOperation>>;
  outputParser?: { module: string; parser: string }; // module = parsers entry; parser = dispatch key
  projectTypes?: string[]; // Restrict to specific project types
  skip?: boolean; // Report as skipped and never run (instead of omitting the tool)
  skipReason?: string; // Human-readable reason shown in the skipped report
}

interface ToolOperation {
  app: string; // App name from apps
  args: string[]; // Supports template placeholders ({file}, {files}, {root}, {cwd}, {toolCache})
  globs?: string[]; // File patterns (doublestar syntax; `!` negation not supported). Omit to match all discovered files.
  excludeGlobs?: string[]; // Patterns removed from the matched set (doublestar syntax)
  scope: "repository" | "per-project" | "per-file";
  batch?: boolean; // Batch files into single execution (default: true)
  priority?: number; // Execution order (lower = first, default: 0)
  invalidateOn?: string[]; // Files that invalidate cache
  env?: Record<string, string>; // Extra environment variables; values support {root}, {cwd}, {toolCache}
  input?: "file" | "stdin"; // How file content reaches the tool (default: "file")
  output?: "inplace" | "stdout"; // How the result is captured (default: "inplace")
}
```

**Example:**

```javascript
const toolsConfig = {
  prettier: {
    name: "prettier",
    operations: {
      fix: {
        app: "prettier",
        args: ["--write", "{files}"],
        globs: ["**/*.{js,ts,jsx,tsx,json,md,yaml,yml}"],
        scope: "per-project",
      },
      lint: {
        app: "prettier",
        args: ["--check", "{files}"],
        globs: ["**/*.{js,ts,jsx,tsx,json,md,yaml,yml}"],
        scope: "per-project",
      },
    },
    projectTypes: ["typescript", "javascript"],
  },
};
```

### Scope Types

| Scope         | Description                         | Working Directory |
| ------------- | ----------------------------------- | ----------------- |
| `repository`  | Runs once for the entire repository | Git root          |
| `per-project` | Runs once per detected project      | Project root      |
| `per-file`    | Runs once per matched file          | Project root      |

See [Template Placeholders](./template-placeholders.md) for the `{file}`, `{files}`, `{root}`, `{cwd}`, and `{toolCache}` placeholders available in `args` (and `{root}`, `{cwd}`, `{toolCache}` in `env`). Unknown placeholders fail config loading rather than being passed through to the tool.

### Skipping a tool (`skip` / `skipReason`)

Set `skip: true` to keep a tool in the config but report it as **skipped** rather
than running it. This is preferable to conditionally omitting the tool: an omitted
tool is invisible, whereas a skipped one is shown in the run summary (`⊘ <tool>
skipped (<reason>)`), the footer's `· N skipped` count, and the `--explain=json`
`skipped` array. Use `skipReason` to explain why.

A tool is **also** skipped automatically when its binary has no build for the
current OS/architecture/libc — a soft skip that no longer fails the run. See
[`--fail-on-skip`](./cli-commands.md#skipped-tools) to harden CI against that case.

```javascript
// BAD: conditionally omitting the tool — datamitsu never reports it was left out
const toolsConfig = {
  ...(facts().env.CI && { trufflehog: { name: "trufflehog", operations: { lint } } }),
};

// GOOD: keep the tool, mark it skipped with a reason
const toolsConfig = {
  trufflehog: {
    name: "trufflehog",
    skip: !facts().env.CI,
    skipReason: "runs in CI only",
    operations: { lint: { app: "trufflehog", scope: "repository" } },
  },
};
```

### Output Parser (`outputParser`)

A tool may reference a WASM [output parser](#output-parsers-parsers) to turn its
raw text output into structured results. The reference is an object with two
parts: **`module`** — the `parsers` entry to load (a specific WASM artifact, so
different versions are different entries) — and **`parser`** — the parser inside
that module to run (a name from `datamitsu devtools parsers list`). `module` must
name an existing `parsers` entry; a dangling reference is a config error.

```javascript
const toolsConfig = {
  hadolint: {
    name: "hadolint",
    outputParser: { module: "core", parser: "hadolint" },
    operations: { lint: { app: "hadolint", args: ["{file}"], scope: "per-file" } },
  },
  eslint: {
    name: "eslint",
    outputParser: { module: "core", parser: "eslint" },
    operations: {
      lint: { app: "eslint", args: ["--format", "json", "{file}"], scope: "per-file" },
    },
  },
};
```

Keeping `module` and `parser` separate is what makes multiple module versions
work: declare `parsers: { core: {…v2}, core_legacy: {…v1} }` and point each tool's
`outputParser.module` at the version it needs.

Output parsers are a Phase-1 _plumbing_ surface: the pipeline (declare → download
→ verify → load → invoke) ships now with a trivial `echo` parser, but real
diagnostic parsers (hadolint, yamllint, …) arrive in a later phase. See
[WASM Output Parsers](../guides/architecture/parsers.md) for the architecture.

### Formatting input/output modes (`input` / `output`)

By default a tool receives file paths as arguments (`input: "file"`) and either
mutates files in place or has its combined stdout+stderr captured for reporting
(`output: "inplace"`). A **formatter** that reads from standard input and writes
the formatted document to standard output uses the stdin → stdout → diff contract
instead:

| Field    | Values                    | Default     | Meaning                                                                |
| -------- | ------------------------- | ----------- | ---------------------------------------------------------------------- |
| `input`  | `"file"` \| `"stdin"`     | `"file"`    | `"stdin"` pipes the target file's content to the tool's standard input |
| `output` | `"inplace"` \| `"stdout"` | `"inplace"` | `"stdout"` captures stdout (kept apart from stderr) as the new content |

When `output: "stdout"`, datamitsu treats the tool's stdout as the full new file
text, computes a **minimal line-based diff** against the original, and applies
only the changed lines — no change yields no edits. This keeps the formatter
sandboxed from the filesystem and produces precise edits (reusable as LSP
`TextEdit` ranges later). Use it with `scope: "per-file"`.

```javascript
// BAD: a stdin-only formatter forced through the in-place vehicle — it never
// receives the file and its formatted output is swallowed into the report.
const toolsConfig = {
  shfmt: {
    name: "shfmt",
    operations: {
      fix: { app: "shfmt", args: ["{file}"], scope: "per-file" },
    },
  },
};

// GOOD: feed content on stdin, capture stdout, let the core diff + apply.
const toolsConfig = {
  shfmt: {
    name: "shfmt",
    operations: {
      fix: {
        app: "shfmt",
        args: [], // tool reads stdin, writes stdout
        scope: "per-file",
        input: "stdin",
        output: "stdout",
      },
    },
  },
};
```

See [Formatting pipeline](../guides/tooling-system.md#formatting-stdin--stdout--diff)
for the end-to-end flow.

## Project Types (`projectTypes`)

Project types define markers used to detect project boundaries in the repository.

```javascript
const projectTypes = {
  typescript: {
    markers: ["package.json", "tsconfig.json"],
  },
  golang: {
    markers: ["go.mod"],
  },
  python: {
    markers: ["pyproject.toml", "setup.py"],
  },
};
```

## Config Setup (`setup`)

Config setup entries define configuration files that `datamitsu setup` generates.

```typescript
interface ConfigSetup {
  content?: (context: ConfigContext) => string;
  deleteOnly?: boolean; // Only delete, don't create
  expectChainHash?: string; // Pin the upstream-chain hash; abort setup on drift
  linkTarget?: string; // Create symlink instead of writing content
  otherFileNameList?: string[]; // Conflicting files to delete
  projectTypes?: string[]; // Restrict to project types
  scope?: "project" | "git-root"; // Where to create: "project" (default) or "git-root" (once at root)
  tools?: string[]; // Tool name(s) this config belongs to; enables `setup --tools` scoping
}
```

The optional `tools` field associates a config file with one or more tools (by
name, matching keys in [`tools`](#tools-tools)). `datamitsu setup --tools <names>`
then regenerates only the config files whose `tools` intersect the selected set —
every other config, including unassociated infrastructure files, is left
untouched. Omit `tools` for files not tied to a single tool (`.gitignore`,
`lefthook.yaml`); those are skipped whenever `--tools` is passed.

### Pinning the upstream chain (`expectChainHash`)

Config files are generated by layering: a shared config (and any
`getRemoteConfigs()` / `getBeforeConfigs()` layers) runs first, and **your** root
config — the one at the git root, or an explicit `--config` — runs last on top.
When you override a generated config in your root layer, those overrides assume a
particular upstream baseline. If the shared config later changes that baseline, a
plain `datamitsu setup` would re-derive the file and could silently drop your
overrides.

`expectChainHash` guards against exactly that. Set it on a root-layer entry to
pin the XXH3-128 hash of the **content entering your layer** — the output of the
whole upstream chain, _before_ your layer transforms it:

```javascript
const setup = {
  "eslint.config.mjs": {
    // Pin the upstream baseline this override was written against.
    expectChainHash: "xxh3:0a1b2c3d4e5f60718293a4b5c6d7e8f9",
    content: (context) => {
      /* your overrides on top of the shared config */
    },
  },
};
```

On every `datamitsu setup` (including `--dry-run`), the hash is recomputed and
compared. When it matches, setup proceeds normally. When the upstream chain has
**drifted**, setup aborts **before writing anything** and prints the new hash
plus the full incoming content, so you can review the change against your
overrides, update the pin, and re-run. Key properties:

- **Opt-in, per file.** Files without `expectChainHash` behave exactly as before.
  To attach a pin, the file must have an entry in your root config (an entry with
  only `expectChainHash` and no `content` is valid).
- **Root layer only.** A pin is honoured solely on the topmost (root) layer;
  pins on intermediate layers are ignored.
- **Byte-for-byte.** The incoming content is hashed verbatim with no
  normalization — any change (upstream _or_ a handler that folds in your on-disk
  file) moves the hash. Format: `"xxh3:<32-hex>"` (a bare 32-hex value is also
  accepted).
- **Bypass.** Pass `datamitsu setup --no-verify-hash` to skip the check and write
  regardless of drift.

To obtain the value the first time, declare the entry with a placeholder pin and
read the real hash with [`datamitsu config chain-hash <file>`](./cli-commands.md#config-chain-hash)
(it prints exactly what the gate checks, no error needed):

```bash
pin=$(datamitsu config chain-hash eslint.config.mjs)  # -> xxh3:…
```

The `content` function receives a context object:

```typescript
interface ConfigContext {
  cwdPath: string; // Current working directory
  rootPath: string; // Git repository root
  datamitsuDir: string; // Relative path from cwdPath to .datamitsu/
  isRoot: boolean; // Is cwdPath the git root?
  projectTypes: string[]; // Detected project types
  existingContent?: string; // Previous layer's generated content (if any)
  existingPath?: string; // Current file path (if exists)
  originalContent?: string; // Unmodified content from disk
}
```

**Example:**

```javascript
const init = {
  "eslint.config.js": {
    projectTypes: ["typescript", "javascript"],
    tools: ["eslint"], // scope `setup --tools eslint` to this file
    content: (context) => {
      const configPath = tools.Path.forImport(
        tools.Path.join(context.datamitsuDir, "eslint.config.js"),
      );
      return `import config from "${configPath}";\nexport default config;\n`;
    },
    otherFileNameList: [".eslintrc", ".eslintrc.json", ".eslintrc.yml"],
  },
};
```

## Init Commands (`initCommands`)

Init commands run shell commands during `datamitsu init`.

```typescript
interface InitCommand {
  command: string; // App name from apps
  args: string[]; // Command arguments
  projectTypes?: string[]; // Restrict to project types
}
```

**Example:**

```javascript
const initCommands = {
  lefthook: {
    command: "lefthook",
    args: ["install"],
  },
};
```

## Archives

Apps can bundle directory trees via inline or external archives.

```typescript
interface ArchiveSpec {
  inline?: string; // Brotli-compressed tar: "tar.br:..." prefix
  url?: string; // External archive URL
  format?: string; // Required for external: "tar", "tar.gz", etc.
  hash?: string; // SHA-256 required for external archives
}
```

**Extraction order:** Archives extracted first (sorted alphabetically), then `files` written (files overwrite archive contents). Package manager runs after both.

Archives are supported on UV apps, node apps, and bundles.

## Ignore Rules

The `ignoreRules` field accepts `.datamitsuignore`-syntax rules:

```javascript
const config = {
  ignoreRules: ["vendor/**/*: *", "**/*.generated.go: golangci-lint"],
};
```

See [Ignore Rules](./ignore-rules.md) for the full syntax reference.

## Shared Storage (`sharedStorage`)

A `map[string]string` field that flows through the config chain as ordinary JS input. Use it to pass arbitrary data between config layers that doesn't fit the typed config structure.

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

// Setting values in a config layer
function getConfig(input) {
  return {
    ...input,
    sharedStorage: {
      ...input.sharedStorage,
      "llms-txt": "# Project documentation...",
      "feature-flag": "true",
    },
  };
}
globalThis.getConfig = getConfig;

// Reading values in a downstream config layer
function getConfig(input) {
  const llmsTxt = input.sharedStorage?.["llms-txt"];
  // Use the value...
  return { ...input };
}
globalThis.getConfig = getConfig;
```

See [Managed Content - Shared Storage](../guides/managed-content.md#shared-storage) for usage examples.

## OCI Bundle (`oci`)

Pins the [OCI bundle](/docs/guides/oci-bundles) that seeds the tool store: the registry repository plus the mandatory SHA-256 digest.

```typescript
interface OCIRef {
  ref: string; // full reference incl. registry host, e.g. "ghcr.io/owner/tool-store"
  digest: string; // "sha256:" + 64 lowercase hex characters — mandatory
  signer?: {
    identity: string; // sigstore certificate identity (e.g. a workflow ref)
    issuer: string; // OIDC issuer URL
  }; // rejected at config load — see below
}
```

```javascript
function getConfig(input) {
  return {
    ...input,
    oci: {
      ref: "ghcr.io/owner/tool-store",
      digest: "sha256:6c3c624b58dbbcd3c0dd82b4c53f04194d1247c6eebdaab7c610cf7d66709b3b",
    },
  };
}
```

Rules:

- `ref` must include the registry host as its first segment — there is no default host and no docker.io shorthand. Tags and digests inside `ref` are rejected; content is pinned exclusively by `digest`.
- `oci` chains through the layers **as a scalar**: the last layer that set or spread it wins, and the effective config holds at most one declaration. A layer that rebuilds its output without `{...input}` silently drops the inherited value (a debug log flags this); reset explicitly with `oci: undefined` or `oci: null`.
- `signer` is **rejected at config load**: `oci: signer is set but signature verification is not implemented in this build; remove signer (the pinned digest still verifies the bundle bytes)`. This build has no sigstore dependency and verifies no signatures at all, so a config that pins a signer would assert a guarantee the binary does not deliver. The field is reserved so the eventual implementation reuses one vocabulary across bundles and [parsers](#registry-source-oci). Previously a set `signer` loaded and failed later, at seed time; it now fails at load.

See the [OCI Bundles guide](/docs/guides/oci-bundles) for the trust model and the seeding lifecycle.

## Output Parsers (`parsers`)

Parsers are Rust→WASM modules that extract structured results from a tool's raw
text output. A `parsers` entry is a **hash-pinned data artifact** — modeled on
`ArchiveSpec`/`Bundle`, _not_ on an app: there is no runtime and no lockfile,
because it is data, not a process. It is fetched, SHA-256 verified,
content-addressed in the store, and loaded into a sandboxed WASM runtime
(wazero).

```typescript
interface Parser {
  hash: string; // SHA-256 (64 lowercase hex) — mandatory for every source
  url?: string; // URL of the .wasm module
  oci?: ParserOCI; // registry-sourced module
  // Exactly one of `url` or `oci`. No `version` field — see note below.
}
```

| Field  | Type        | Description                                                       |
| ------ | ----------- | ----------------------------------------------------------------- |
| `hash` | `string`    | SHA-256 hash, 64 lowercase hex — **mandatory** for every source   |
| `url`  | `string`    | URL of the `.wasm` module. Exactly one of `url` or `oci`          |
| `oci`  | `ParserOCI` | Module pulled from an OCI registry. Exactly one of `url` or `oci` |

The entity is intentionally **source + hash only**. A module reports its own
build-injected version (and the tools it parses) through its WASM `describe`
export, surfaced by [`datamitsu devtools parsers list`](./cli-commands.md#devtools-parsers).
The module is the single source of truth, so there is no `version` field to drift
from reality.

A tool opts into a parser via [`tool.outputParser`](#output-parser-outputparser).
The same parser referenced by several tools is downloaded once.

```javascript
function getConfig(input) {
  return {
    ...input,
    parsers: {
      echo: {
        url: "https://github.com/owner/repo/releases/download/v1.2.3/datamitsu_parsers_1.2.3.wasm",
        hash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      },
    },
  };
}
```

The SHA-256 `hash` is **mandatory** for every source per the
[security policy](#security-requirements) — an empty or malformed hash is a
config error, not a warning (`parser "echo": hash is required (SHA-256)` and
`parser "echo": hash must be a valid SHA-256 hex string (64 lowercase hex characters)`).

The module is delivered through two channels. As a versioned asset on the GitHub
Release (`datamitsu_parsers_<version>.wasm`): take the `url` from that release
asset and the `hash` from the release's signed `checksums.txt`. Or as an OCI
artifact in a registry: take `oci.ref` and `oci.digest` from the published
artifact, with the same `hash` — see [Registry source (`oci`)](#registry-source-oci)
below. See [WASM Output Parsers](../guides/architecture/parsers.md) for the trust
model.

```javascript
// BAD: missing hash — refused at config load
parsers: {
  echo: { url: "https://example.com/datamitsu_parsers.wasm" },
}

// GOOD: mandatory SHA-256 hash from the release's signed checksums.txt
parsers: {
  echo: {
    url: "https://example.com/datamitsu_parsers.wasm",
    hash: "0123…cdef", // 64 lowercase hex
  },
}
```

### One source per entry (`url` or `oci`)

An entry declares **exactly one** source. Both, or neither, is a config error at
load:

- neither — `parser "echo": exactly one of url or oci is required`
- both — `parser "echo": url and oci are mutually exclusive (declare one source; use a config layer to override)`

This is deliberately not a fallback chain. An air-gapped organization has to be
able to prove there is no `github.com` egress left, which a "try the registry,
then the URL" declaration would quietly undo. Switching an entry to a registry is
what config layers are for: a downstream layer replaces the entry with the `oci`
form instead of adding a second source to it.

```javascript
// BAD: two sources in one entry — refused at config load
parsers: {
  echo: {
    url: "https://example.com/datamitsu_parsers.wasm",
    oci: { ref: "ghcr.io/datamitsu/datamitsu-parsers", digest: "sha256:89ab…4567" },
    hash: "0123…cdef",
  },
}

// GOOD: a downstream layer replaces the entry, keeping the same hash
function getConfig(input) {
  return {
    ...input,
    parsers: {
      ...input.parsers,
      echo: {
        oci: { ref: "ghcr.io/datamitsu/datamitsu-parsers", digest: "sha256:89ab…4567" },
        hash: input.parsers.echo.hash,
      },
    },
  };
}
```

### Registry source (`oci`)

`oci` pulls the module from an OCI registry instead of over HTTPS, so an
organization that mirrors one registry gets diagnostics parsing along with
everything else. It mirrors the [OCI bundle](#oci-bundle-oci) reference
vocabulary — same grammar, same digest form, same `signer` rejection.

```typescript
interface ParserOCI {
  ref: string; // full reference incl. registry host, e.g. "ghcr.io/datamitsu/datamitsu-parsers"
  digest: string; // "sha256:" + 64 lowercase hex characters — mandatory
  signer?: {
    identity: string; // sigstore certificate identity (e.g. a workflow ref)
    issuer: string; // OIDC issuer URL
  }; // rejected at config load — see below
}
```

| Field    | Type        | Description                                                                                                         |
| -------- | ----------- | ------------------------------------------------------------------------------------------------------------------- |
| `ref`    | `string`    | Full repository reference including the registry host. Tags and digests inside the ref are rejected — **mandatory** |
| `digest` | `string`    | Artifact manifest digest, `"sha256:"` + 64 lowercase hex — **mandatory**                                            |
| `signer` | `OCISigner` | **Rejected at config load** — this build verifies no signatures                                                     |

```javascript
function getConfig(input) {
  return {
    ...input,
    parsers: {
      echo: {
        oci: {
          ref: "ghcr.io/datamitsu/datamitsu-parsers",
          digest: "sha256:89abcdef0123456789abcdef0123456789abcdef0123456789abcdef01234567",
        },
        hash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      },
    },
  };
}
```

Rules:

- `ref` must include the registry host as its first segment — there is no default
  host and no docker.io shorthand. A ref without a host is rejected with
  `oci.ref … must include the registry host as its first segment (e.g. ghcr.io/owner/repo)`,
  and a tag or a digest inside the ref with
  `oci.ref … is not a valid repository reference (expected host[:port]/path, lowercase, no tag and no digest)`.
- Content is pinned exclusively by `digest`, which is mandatory:
  `oci.digest is required (sha256:<64 hex>)`, or
  `oci.digest … must be "sha256:" followed by 64 lowercase hex characters` when it
  is malformed.
- `hash` stays mandatory and does double duty: it is **also** the expected layer
  blob digest. The artifact's single layer must have digest `"sha256:" + hash`, so
  the registry digest chain and the config hash are the same number. A manifest
  that points at other content is rejected before one payload byte is requested,
  and the same `hash` is checked again on the downloaded stream and once more on
  the file on disk.
- `signer` is rejected at load, on this surface and on the bundle's:
  `oci.signer is set but signature verification is not implemented in this build`.
  datamitsu verifies no signatures at all; the mandatory `hash` is what guarantees
  the bytes.

Because the store key under `{store}/.parsers/{name}/` is derived from `hash`
alone, an entry resolves to **one** store directory however the module arrived — a release URL, a registry, or a
layer of an OCI bundle — which is what makes a bundle-seeded and a registry-pulled
module interchangeable. A module already on disk is re-hashed against the declared
`hash` before use and discarded when it does not match, whatever put it there.

[`--no-oci` / `DATAMITSU_NO_OCI`](./cli-commands.md#environment-variables) disables OCI
_bundle_ seeding, an accelerator; it does **not** disable a declared parser `oci`
source, which is the only route to those bytes. `DATAMITSU_OFFLINE` is the single
hard network gate. [`datamitsu store refs`](./cli-commands.md#store-refs) lists
every artifact the effective config pins, so a mirror can be filled without
guessing.

## LSP Servers (`lsp`) — reserved

:::info Reserved surface
`lsp` is a **declaration-only** entity reserved for a later phase. The types load
and are structurally validated today, but there is **no runtime behavior** in
this release. Declaring `lsp` entries does nothing yet.
:::

Each entry is one of two discriminated shapes:

```typescript
// Wrap a standalone language-server app, scoped by project types.
interface LspProxy {
  type: "proxy";
  app: string; // app name (in `apps`) providing the server
  projectTypes: string[]; // must be non-empty
  order?: number; // precedence; ties break alphabetically by entry name
}

// Reuse an existing tool's projectTypes/globs and its outputParser.
interface LspDerived {
  type: "derived";
  tool: string; // tool name (in `tools`) — must exist
  order?: number; // precedence; ties break alphabetically by entry name
}
```

Structural validation today: `type` must be `"proxy"` or `"derived"`; a `proxy`
requires `app` plus a non-empty `projectTypes`; a `derived` requires a `tool`
that exists in `tools`. The optional `order` controls precedence, with **ties
broken alphabetically by entry name** (the same `sort.Strings`-by-name convention
the planner uses).

```javascript
function getConfig(input) {
  return {
    ...input,
    lsp: {
      "go-lsp": { type: "proxy", app: "gopls", projectTypes: ["golang"] },
      hadolint: { type: "derived", tool: "hadolint" },
    },
  };
}
```

## JavaScript APIs

The following APIs are available in configuration files.

### Format Utilities

```javascript
// YAML
YAML.parse(text);
YAML.stringify(value);

// TOML
TOML.parse(text);
TOML.stringify(value);

// INI
INI.parse(text);
INI.stringify(sections);
INI.toRecord(sections);
```

### Path Utilities

```javascript
// Join path segments
tools.Path.join("src", "components", "Button.tsx");

// Get absolute path
tools.Path.abs("relative/path");

// Get relative path (basePath defaults to git root)
tools.Path.rel(targetPath, basePath);

// Convert to ES module import path (ensures ./ or ../ prefix)
tools.Path.forImport(tools.Path.join(context.datamitsuDir, "eslint.config.js"));
// → "./.datamitsu/eslint.config.js"
```

### Config Link Utilities

```javascript
// Get relative path from a file to a managed config link
tools.Config.linkPath("eslint", "eslint-config", fromPath);
```

### Ignore Utilities

```javascript
// Parse .gitignore-style content
const groups = tools.Ignore.parse(content);

// Stringify back with optional group ordering
const output = tools.Ignore.stringify(groups, groupOrder);
```

### Platform Information

```javascript
const info = facts();
// info.os       → "linux", "darwin", "windows", etc.
// info.arch     → "amd64", "arm64"
// info.libc     → "glibc", "musl", "unknown" (Linux-only detection)
// info.isInGitRepo → true/false
// info.isMonorepo  → true/false
// info.env      → environment variables
```

## Security Requirements

All artifacts downloaded from the internet must have a SHA-256 hash specified:

- Binary apps: `hash` field on each platform entry
- JVM apps: `jarHash` field
- External archives: `hash` field
- Node runtime (pnpm): `pnpmHash` field
- Output parsers: `hash` field on each `parsers` entry, whichever source it declares
- OCI-sourced parsers: `oci.digest` on the entry, **plus** the same mandatory `hash` — which the artifact's single layer must carry as its blob digest

Missing or empty hashes are treated as configuration errors.
