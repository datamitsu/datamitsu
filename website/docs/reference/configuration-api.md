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
      pnpmVersion: "11.5.0",
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
  pnpmVersion: string; // e.g., "11.5.0"
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
  };
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
- `signer` optionally pins the sigstore keyless identity of the bundle publisher. When set, pull must verify the signature before layout (not yet supported by the current builds — a set `signer` currently fails the seed rather than silently skipping the check).

See the [OCI Bundles guide](/docs/guides/oci-bundles) for the trust model and the seeding lifecycle.

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

Missing or empty hashes are treated as configuration errors.
