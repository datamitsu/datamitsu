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

```typescript
function getMinVersion(): string;
function getConfig(config: Config): Config;
function getRemoteConfigs(): Array<{ url: string; hash: string }>;
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
  init?: MapOfConfigInit;
  initCommands?: MapOfInitCommands;
  ignoreRules?: string[];
  sharedStorage?: Record<string, string>;
}
```

## Config Loading Order

Configuration is loaded in layers, each receiving the previous result as input:

```
default (embedded config.js)
  ↓ [getRemoteConfigs() resolved depth-first, if exported]
--before-config flags  (for wrappers/libraries)
  ↓ [getRemoteConfigs() resolved depth-first]
auto (datamitsu.config.{js,mjs,ts} at git root)
  ↓ [getRemoteConfigs() resolved depth-first]
--config flags  (for CI/testing overrides)
  ↓
final Config
```

- Each source must export `getMinVersion()` — version is checked before `getConfig()` runs (fail-fast)
- Each source can export `getRemoteConfigs()` returning `Array<{url: string, hash: string}>` for recursive parent resolution
- `ignoreRules` use append semantics across config layers
- Circular remote config dependencies are detected and produce an error

## Apps (`apps`)

Apps define the tools datamitsu manages. The app kind is determined by which sub-object is present (`binary`, `uv`, `fnm`, `jvm`, or `shell`).

### App Kinds

| Kind     | Sub-object | Description                                           |
| -------- | ---------- | ----------------------------------------------------- |
| `binary` | `binary`   | Self-managed binaries downloaded from URLs            |
| `uv`     | `uv`       | Python packages installed via managed UV runtime      |
| `fnm`    | `fnm`      | npm packages installed via FNM-managed Node.js + PNPM |
| `jvm`    | `jvm`      | Java applications executed via managed JDK            |
| `shell`  | `shell`    | Shell commands with custom environment                |

### Common App Fields

All app kinds share these optional fields:

```typescript
interface AppCommon {
  required?: boolean; // Whether the app is required for init
  files?: Record<string, string>; // filename → static content
  links?: Record<string, string>; // linkName → relativePath in install dir
  archives?: Record<string, ArchiveSpec>; // name → archive specification
  versionCheck?: {
    disabled?: boolean; // Skip version check in verify-all
    args?: string[]; // Override default ["--version"] args
  };
}
```

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

### FNM Apps (Node.js/npm)

FNM apps install npm packages using FNM-managed Node.js and PNPM.

```javascript
const apps = {
  eslint: {
    fnm: {
      packageName: "eslint",
      version: "9.27.0",
      binPath: "node_modules/.bin/eslint",
      dependencies: {
        "typescript-eslint": "^8.32.0",
        "@eslint/js": "^9.27.0",
      },
      lockFile: "br:...", // brotli-compressed lock file (required)
      runtime: "fnm-default", // optional runtime override
    },
    links: {
      "eslint-config": "dist/eslint.config.js",
    },
  },
};
```

**FNM-specific fields:**

```typescript
interface AppConfigFNM {
  packageName: string;
  version: string;
  binPath: string; // Path to binary (e.g., "node_modules/.bin/eslint")
  dependencies?: Record<string, string>;
  lockFile: string; // Brotli-compressed with "br:" prefix (required)
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

### Shell Apps

Shell apps wrap system commands with custom environment variables.

```javascript
const apps = {
  "my-script": {
    shell: {
      name: "bash",
      args: ["-c", "echo hello"],
      env: { MY_VAR: "value" },
    },
  },
};
```

**Shell-specific fields:**

```typescript
interface AppConfigShell {
  name: string; // Command name
  args?: string[]; // Default arguments
  env?: Record<string, string>;
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
  kind: "fnm" | "uv" | "jvm";
  mode: "managed" | "system";
  managed?: RuntimeConfigManaged; // Required for managed mode
  system?: RuntimeConfigSystem; // For system mode
  fnm?: RuntimeConfigFNM; // Required when kind is "fnm"
  uv?: RuntimeConfigUV; // When kind is "uv"
  jvm?: RuntimeConfigJVM; // Required when kind is "jvm"
}
```

### Managed Mode

In managed mode, datamitsu downloads the runtime binary itself:

```javascript
const runtimes = {
  "fnm-default": {
    kind: "fnm",
    mode: "managed",
    managed: {
      binaries: {
        linux: {
          amd64: {
            glibc: {
              url: "https://github.com/Schniz/fnm/releases/download/v1.38.1/fnm-linux.zip",
              hash: "abc123...",
              contentType: "zip",
              binaryPath: "fnm",
            },
          },
        },
        // ... other platforms
      },
    },
    fnm: {
      nodeVersion: "24.14.0",
      pnpmVersion: "10.31.0",
      pnpmHash: "def456...", // SHA-256 of PNPM tarball
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

**FNM Runtime:**

```typescript
interface RuntimeConfigFNM {
  nodeVersion: string; // e.g., "24.14.0"
  pnpmVersion: string; // e.g., "10.31.0"
  pnpmHash: string; // SHA-256 of PNPM package (mandatory)
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

## Tools (`tools`)

Tools define fix and lint operations that datamitsu executes.

```typescript
interface Tool {
  name: string;
  operations: Partial<Record<"fix" | "lint", ToolOperation>>;
  projectTypes?: string[]; // Restrict to specific project types
}

interface ToolOperation {
  app: string; // App name from apps
  args: string[]; // Supports template placeholders
  globs: string[]; // File patterns (gitignore-style)
  scope: "repository" | "per-project" | "per-file";
  batch?: boolean; // Batch files into single execution (default: true)
  priority?: number; // Execution order (lower = first, default: 0)
  invalidateOn?: string[]; // Files that invalidate cache
  env?: Record<string, string>; // Extra environment variables
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

See [Template Placeholders](./template-placeholders.md) for the `{file}`, `{files}`, `{root}`, `{cwd}`, and `{toolCache}` placeholders available in `args`.

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

## Config Init (`init`)

Config init entries define configuration files that `datamitsu setup` generates.

```typescript
interface ConfigInit {
  content?: (context: ConfigContext) => string;
  deleteOnly?: boolean; // Only delete, don't create
  linkTarget?: string; // Create symlink instead of writing content
  otherFileNameList?: string[]; // Conflicting files to delete
  projectTypes?: string[]; // Restrict to project types
  scope?: "project" | "git-root"; // Where to create: "project" (default) or "git-root" (once at root)
}
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

Archives are supported on UV apps, FNM apps, and bundles.

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
- PNPM runtime: `pnpmHash` field

Missing or empty hashes are treated as configuration errors.
