---
title: JavaScript API
description: Programmatic API reference for the @datamitsu/datamitsu npm package
---

# JavaScript API

The `@datamitsu/datamitsu` npm package provides a programmatic API for integrating datamitsu into scripts, build pipelines, and code generation workflows.

## Installation

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

<Tabs>
  <TabItem value="pnpm" label="pnpm" default>

    ```bash
    pnpm add @datamitsu/datamitsu
    ```

  </TabItem>
  <TabItem value="npm" label="npm">

    ```bash
    npm install @datamitsu/datamitsu
    ```

  </TabItem>
  <TabItem value="yarn" label="yarn">

    ```bash
    yarn add @datamitsu/datamitsu
    ```

  </TabItem>
  <TabItem value="bun" label="bun">

    ```bash
    bun add @datamitsu/datamitsu
    ```

  </TabItem>
  <TabItem value="deno" label="deno">

    ```bash
    deno add npm:@datamitsu/datamitsu
    ```

  </TabItem>
</Tabs>

The package requires Node.js 18+ and uses ES modules.

## Quick Start

```javascript
import { fix, lint, version } from "@datamitsu/datamitsu";

// Get the installed version
const v = await version();
console.log(v.version); // "1.2.3"

// Run fix on specific files
const result = await fix({ files: ["src/app.ts"] });
console.log(result.success); // true

// Get a structured execution plan
const plan = await fix({ explain: "json" });
if (plan.success) {
  console.log(plan.plan.groups); // PlanJSON structure
}
```

A default export is also available:

```javascript
import datamitsu from "@datamitsu/datamitsu";

await datamitsu.fix({ files: ["src/app.ts"] });
await datamitsu.lint({ explain: "json" });
const tools = await datamitsu.exec();
```

## Output Modes

All commands that produce output support two modes controlled by the `stdio` option:

- **`"pipe"`** (default) - Captures stdout/stderr and returns them in the result object. Use this when you need to process or parse the output programmatically.
- **`"inherit"`** - Streams output directly to the parent process terminal. Use this for interactive CLI wrappers or when you want real-time output.

```javascript
// Capture output for processing
const result = await fix({ stdio: "pipe" });

// Stream output to terminal
await fix({ stdio: "inherit" });
```

## Commands

### fix()

Auto-fix files using configured tools.

```typescript
function fix(options?: FixOptions): Promise<FixResult>;
```

**Options:**

| Option         | Type                                                 | Default         | Description                                       |
| -------------- | ---------------------------------------------------- | --------------- | ------------------------------------------------- |
| `files`        | `string[]`                                           | `[]`            | Target files to fix                               |
| `explain`      | `false \| true \| "summary" \| "detailed" \| "json"` | `false`         | Output format. `"json"` returns parsed `PlanJSON` |
| `fileScoped`   | `boolean`                                            | `false`         | Apply file-scoped fixes only                      |
| `tools`        | `string[]`                                           | `[]`            | Limit to specific tools                           |
| `cwd`          | `string`                                             | `process.cwd()` | Working directory                                 |
| `config`       | `string[]`                                           | `[]`            | Additional config file paths                      |
| `beforeConfig` | `string[]`                                           | `[]`            | Config files loaded before auto-discovery         |
| `noAutoConfig` | `boolean`                                            | `false`         | Disable auto-discovery of config at git root      |
| `stdio`        | `"pipe" \| "inherit"`                                | `"pipe"`        | Output handling mode                              |

**Result:**

The return type depends on the `explain` option and whether the command succeeds:

```typescript
// On failure
{ success: false; error: string; exitCode?: number; raw: SpawnRaw }

// On success without explain
{ success: true; exitCode: number; raw: SpawnRaw }

// On success with explain (text modes)
{ success: true; output: string; raw: SpawnRaw }

// On success with explain="json"
{ success: true; plan: PlanJSON; raw: SpawnRaw }
```

**Examples:**

```javascript
import { fix } from "@datamitsu/datamitsu";

// Fix all files
const result = await fix();

// Fix specific files
await fix({ files: ["src/app.ts", "src/utils.ts"] });

// Get execution plan as JSON
const plan = await fix({ explain: "json" });
if (plan.success) {
  for (const group of plan.plan.groups) {
    for (const pg of group.parallelGroups) {
      for (const task of pg.tasks) {
        console.log(`${task.toolName}: ${task.fileCount} files`);
      }
    }
  }
}

// Fix with specific tools only
await fix({ tools: ["prettier", "eslint"], files: ["src/app.ts"] });

// Use custom config
await fix({ config: ["/path/to/custom.config.js"] });
```

---

### lint()

Validate files without making changes.

```typescript
function lint(options?: LintOptions): Promise<LintResult>;
```

**Options:** Same as [`fix()`](#fix) (`LintOptions` has the same fields as `FixOptions`).

**Result:** Same structure as [`fix()`](#fix).

**Examples:**

```javascript
import { lint } from "@datamitsu/datamitsu";

// Lint all files
const result = await lint();
if (!result.success) {
  console.error("Lint failed:", result.error);
  process.exit(1);
}

// Get detailed lint plan
const plan = await lint({ explain: "json" });
```

---

### check()

Run fix then lint in a single process with shared context. Fails on fix error without continuing to lint.

```typescript
function check(options?: CheckOptions): Promise<CheckResult>;
```

**Options:** Same as [`fix()`](#fix) (`CheckOptions` has the same fields as `FixOptions`).

**Result:** Same structure as [`fix()`](#fix).

**Examples:**

```javascript
import { check } from "@datamitsu/datamitsu";

// Run full check (fix + lint)
const result = await check();
if (!result.success) {
  console.error("Check failed:", result.error);
}

// Check specific files with plan output
const plan = await check({ explain: "json", files: ["src/app.ts"] });
```

---

### exec()

Execute a managed binary or list available tools.

```typescript
function exec(appName?: string, options?: ExecOptions): Promise<ExecResult>;
```

**Options:**

| Option         | Type                  | Default         | Description                                  |
| -------------- | --------------------- | --------------- | -------------------------------------------- |
| `args`         | `string[]`            | `[]`            | Arguments to pass to the app                 |
| `cwd`          | `string`              | `process.cwd()` | Working directory                            |
| `config`       | `string[]`            | `[]`            | Additional config file paths                 |
| `beforeConfig` | `string[]`            | `[]`            | Config files loaded before auto-discovery    |
| `noAutoConfig` | `boolean`             | `false`         | Disable auto-discovery of config at git root |
| `stdio`        | `"pipe" \| "inherit"` | `"pipe"`        | Output handling mode                         |

**Result:**

```typescript
// On failure
{ success: false; error: string; exitCode?: number; raw: SpawnRaw }

// List tools (no appName)
{ success: true; tools: ToolInfo[]; raw: SpawnRaw }

// Execute app (with appName)
{ success: true; stdout: string; stderr: string; exitCode: number; raw: SpawnRaw }
```

**Examples:**

```javascript
import { exec } from "@datamitsu/datamitsu";

// List all available tools
const list = await exec();
if (list.success) {
  for (const tool of list.tools) {
    console.log(`${tool.name} [${tool.type}] - ${tool.details}`);
  }
}

// Execute a managed binary
const result = await exec("golangci-lint", {
  args: ["run", "./..."],
});

// Stream output from a managed binary
await exec("prettier", {
  args: ["--write", "src/**/*.ts"],
  stdio: "inherit",
});
```

#### ToolInfo

Returned when calling `exec()` without an app name:

```typescript
interface ToolInfo {
  name: string; // Tool name (e.g., "golangci-lint")
  type: string; // Tool type: "binary" | "uv" | "fnm" | "jvm" | "shell"
  details: string; // Description or version info
}
```

#### parseToolList()

A utility function for manually parsing tool list output:

```typescript
function parseToolList(output: string): ToolInfo[];
```

```javascript
import { parseToolList } from "@datamitsu/datamitsu";

// Parse raw output from `datamitsu exec`
const tools = parseToolList(rawOutput);
```

---

### cache

Object with three methods for cache management.

#### cache.clear()

Clear the project cache.

```typescript
function cache.clear(options?: CacheClearOptions): Promise<CacheClearResult>;
```

**Options:**

| Option   | Type      | Default         | Description                                    |
| -------- | --------- | --------------- | ---------------------------------------------- |
| `all`    | `boolean` | `false`         | Clear all project caches, not just current     |
| `dryRun` | `boolean` | `false`         | Preview what would be cleared without clearing |
| `cwd`    | `string`  | `process.cwd()` | Working directory                              |

**Result:**

```typescript
// On failure
{
  success: false;
  error: string;
  exitCode: number;
  raw: SpawnRaw;
}

// On success
{
  success: true;
  message: string;
  raw: SpawnRaw;
}
```

**Examples:**

```javascript
import { cache } from "@datamitsu/datamitsu";

// Clear current project cache
await cache.clear();

// Clear all caches
await cache.clear({ all: true });

// Dry run
const preview = await cache.clear({ dryRun: true });
console.log(preview.message);
```

#### cache.path()

Get the global cache directory path.

```typescript
function cache.path(): Promise<CachePathResult>;
```

**Result:**

```typescript
// On failure
{
  success: false;
  error: string;
  exitCode: number;
  raw: SpawnRaw;
}

// On success
{
  success: true;
  path: string;
  raw: SpawnRaw;
}
```

**Example:**

```javascript
const result = await cache.path();
if (result.success) {
  console.log(result.path); // e.g., "/home/user/.cache/datamitsu"
}
```

#### cache.pathProject()

Get the current project's cache directory path.

```typescript
function cache.pathProject(options?: { cwd?: string }): Promise<CachePathResult>;
```

**Example:**

```javascript
const result = await cache.pathProject();
if (result.success) {
  console.log(result.path); // e.g., "/home/user/.cache/datamitsu/projects/abc123"
}
```

---

### version()

Get the installed datamitsu version.

```typescript
function version(): Promise<VersionResult>;
```

**Result:**

```typescript
// On failure
{
  success: false;
  error: string;
  exitCode: number;
  raw: SpawnRaw;
}

// On success
{
  success: true;
  version: string;
  raw: SpawnRaw;
}
```

**Example:**

```javascript
import { version } from "@datamitsu/datamitsu";

const result = await version();
if (result.success) {
  console.log(`datamitsu v${result.version}`);
}
```

## Types Reference

### PlanJSON

Returned by fix/lint/check when using `explain: "json"`. Represents the full execution plan.

```typescript
interface PlanJSON {
  operation: string; // "fix", "lint", or "check"
  rootPath: string; // Git repository root
  cwdPath: string; // Current working directory
  groups: GroupJSON[]; // Ordered execution groups
}

interface GroupJSON {
  priority: number; // Execution priority
  parallelGroups: ParallelGroupJSON[]; // Groups within this priority
}

interface ParallelGroupJSON {
  canRunInParallel: boolean; // Whether tasks can run concurrently
  tasks: TaskJSON[]; // Tasks in this parallel group
}

interface TaskJSON {
  toolName: string; // Tool identifier
  app: string; // Binary/application name
  args: string[]; // Command arguments
  scope: string; // Execution scope
  batch: boolean; // Whether files are batched
  workingDir: string; // Task working directory
  globs: string[]; // File glob patterns
  files: string[]; // Matched file paths
  fileCount: number; // Number of matched files
}
```

### SpawnRaw

Available on all results via the `raw` field, providing access to the underlying process output:

```typescript
interface SpawnRaw {
  stdout: string; // Standard output
  stderr: string; // Standard error
  exitCode: number; // Process exit code
  failed: boolean; // Whether the process failed
}
```

## Error Handling

The API uses an explicit `success` flag pattern instead of exceptions:

- **Spawn errors** (binary not found, permission denied) throw exceptions
- **Tool errors** (non-zero exit code) return `{ success: false, error, exitCode }`

```javascript
import { fix } from "@datamitsu/datamitsu";

try {
  const result = await fix({ files: ["src/app.ts"] });

  if (!result.success) {
    // Tool reported an error (e.g., lint violations)
    console.error("Fix failed:", result.error);
    console.error("Exit code:", result.exitCode);
    // Raw output available for debugging
    console.error("stderr:", result.raw.stderr);
    process.exit(1);
  }

  // Success
  console.log("Fixed successfully");
} catch (err) {
  // Binary not found or spawn error
  console.error("Could not run datamitsu:", err.message);
  process.exit(1);
}
```

## Use Cases

### Code Generation + Fix

```javascript
import { writeFile } from "node:fs/promises";
import { fix } from "@datamitsu/datamitsu";

// Generate code
const code = generateTypeScriptCode();
await writeFile("src/generated.ts", code);

// Auto-fix formatting and lint issues
const result = await fix({ files: ["src/generated.ts"] });
if (!result.success) {
  console.error("Could not fix generated file:", result.error);
}
```

### CI Lint Check

```javascript
import { lint } from "@datamitsu/datamitsu";

const result = await lint();
if (!result.success) {
  console.error(result.error);
  process.exit(1);
}
```

### Build Script Integration

```javascript
import { check, version } from "@datamitsu/datamitsu";

const v = await version();
console.log(`Running datamitsu v${v.version}`);

const result = await check({ stdio: "inherit" });
process.exit(result.success ? 0 : 1);
```
