---
title: CLI Commands
description: Complete reference for all datamitsu CLI commands
---

# CLI Commands

## Global Flags

These flags apply to all commands:

| Flag                      | Description                                                                                                       |
| ------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `--config <file>`         | Additional configuration file(s) to load and merge (can be specified multiple times)                              |
| `--before-config <file>`  | Configuration file(s) to load before auto-discovery (for wrappers/libraries)                                      |
| `--no-auto-config`        | Disable auto-discovery of datamitsu.config.\{js,mjs,ts\} at git root                                              |
| `--binary-command <name>` | Override the binary command name (for npm package wrappers). Also settable via `DATAMITSU_BINARY_COMMAND` env var |

## exec

Execute a managed binary with all environment variables passed through.

```bash
datamitsu exec <appName> [args...]
```

When called without arguments, lists all available tools grouped by type (binary, uv, fnm, jvm, shell).

**Examples:**

```bash
# List all available tools
datamitsu exec

# Run golangci-lint
datamitsu exec golangci-lint run ./...

# Run eslint via FNM-managed Node.js
datamitsu exec eslint --fix src/
```

## init

Download binaries, install runtime-managed apps, and run initialization commands.

```bash
datamitsu init
```

Must be run from the git repository root.

| Flag                       | Description                                        |
| -------------------------- | -------------------------------------------------- |
| `--all`                    | Download all binaries (both required and optional) |
| `--skip-download`          | Skip binary downloads                              |
| `--fail-on-download-error` | Stop if any binary download fails                  |
| `--dry-run`                | Show what would be done without making changes     |

Download concurrency is controlled via the `DATAMITSU_CONCURRENCY` env var (default: 3).

**What init does:**

1. Detects project types in the repository
2. Downloads required binaries and runtimes
3. Installs runtime-managed apps (FNM/UV/JVM) that are referenced by tools
4. Creates `.datamitsu/` symlinks for managed config files
5. Runs configured init commands (e.g., `lefthook install`)

**Examples:**

```bash
# Standard initialization
datamitsu init

# Download all tools including optional ones
datamitsu init --all

# Preview what would happen
datamitsu init --dry-run
```

## check

Run fix followed by lint in a single process with shared context. If fix fails, lint is skipped.

```bash
datamitsu check [files...]
```

| Flag               | Description                                                                         |
| ------------------ | ----------------------------------------------------------------------------------- |
| `--explain [mode]` | Show execution plan without running. Modes: `summary` (default), `detailed`, `json` |
| `--file-scoped`    | Only process git staged files                                                       |
| `--tools <list>`   | Comma-separated list of tools to run                                                |

**Examples:**

```bash
# Check entire project
datamitsu check

# Check specific files
datamitsu check src/main.go src/handler.go

# Check only staged files (useful in git hooks)
datamitsu check --file-scoped

# Preview what would run
datamitsu check --explain
```

## fix

Run fix operations on files.

```bash
datamitsu fix [files...]
```

| Flag               | Description                                                                         |
| ------------------ | ----------------------------------------------------------------------------------- |
| `--explain [mode]` | Show execution plan without running. Modes: `summary` (default), `detailed`, `json` |
| `--file-scoped`    | Only process git staged files                                                       |
| `--tools <list>`   | Comma-separated list of tools to run                                                |

**Examples:**

```bash
# Fix entire project
datamitsu fix

# Fix specific files
datamitsu fix src/main.go

# Fix only with specific tools
datamitsu fix --tools prettier,eslint
```

## lint

Run lint operations on files.

```bash
datamitsu lint [files...]
```

| Flag               | Description                                                                         |
| ------------------ | ----------------------------------------------------------------------------------- |
| `--explain [mode]` | Show execution plan without running. Modes: `summary` (default), `detailed`, `json` |
| `--file-scoped`    | Only process git staged files                                                       |
| `--tools <list>`   | Comma-separated list of tools to run                                                |

**Examples:**

```bash
# Lint entire project
datamitsu lint

# Lint specific files
datamitsu lint src/main.go

# Show detailed execution plan
datamitsu lint --explain detailed
```

## setup

Set up configuration files for detected project types.

```bash
datamitsu setup
```

| Flag         | Description                                    |
| ------------ | ---------------------------------------------- |
| `--dry-run`  | Show what would be done without making changes |
| `--skip-fix` | Skip running fix after setup                   |

Setup detects project types, generates configuration files, and optionally runs fix afterward.

**Examples:**

```bash
# Set up configs
datamitsu setup

# Preview changes
datamitsu setup --dry-run
```

## config

Configuration management commands.

### config show

Display the current configuration as JSON.

```bash
datamitsu config show
```

### config types

Display the TypeScript type definitions file (`config.d.ts`).

```bash
datamitsu config types
```

### config lockfile

Generate lock file content for a runtime-managed app (FNM/UV).

```bash
datamitsu config lockfile [appName]
```

Without arguments, lists all apps that support lock files. With an app name, reinstalls the app from scratch and outputs the lock file content as a brotli-compressed, base64-encoded string for embedding in configuration.

**Examples:**

```bash
# List apps that support lock files
datamitsu config lockfile

# Generate lock file for an FNM app
datamitsu config lockfile eslint
```

## devtools

Developer utility commands for maintaining datamitsu configurations.

### devtools pull-github

Update binary configurations from GitHub releases. Requires a file argument specifying the path to the GitHub apps JSON file. If the file doesn't exist, an empty appstate structure is created automatically. Fetches repository descriptions from the GitHub API and stores them in the output JSON.

```bash
datamitsu devtools pull-github <file>
datamitsu devtools pull-github config/src/githubApps.json
datamitsu devtools pull-github config/src/githubApps.json --update
```

| Flag                  | Description                                       |
| --------------------- | ------------------------------------------------- |
| `--update`            | Fetch latest release tags before updating         |
| `--verify-extraction` | Verify that downloaded archives extract correctly |

The command scans releases for all platform combinations using OS/Arch/Libc target tuples. For Linux, both glibc and musl variants are detected separately. The output JSON uses a nested three-level storage structure:

```json
{
  "binaries": {
    "linux": {
      "amd64": {
        "glibc": { "url": "...", "hash": "...", "contentType": "tar.gz" },
        "musl": { "url": "...", "hash": "...", "contentType": "tar.gz" }
      }
    },
    "darwin": {
      "arm64": {
        "unknown": { "url": "...", "hash": "...", "contentType": "tar.gz" }
      }
    }
  }
}
```

Non-Linux platforms use `unknown` as the libc key. If a musl variant is not found for a Linux target, that entry is simply omitted.

**Examples:**

```bash
# Check for new releases without modifying the file
datamitsu devtools pull-github config/src/githubApps.json

# Update to latest releases
datamitsu devtools pull-github config/src/githubApps.json --update

# Update and verify that archives extract correctly
datamitsu devtools pull-github config/src/githubApps.json --update --verify-extraction
```

:::tip See also
For a complete workflow including CI automation, see [Maintaining Wrapper Packages — Binary Apps](/docs/how-to/maintain-wrapper#binary-apps-devtools-pull-github).
:::

### devtools pull-fnm

Pull latest npm package versions from the npm registry. Requires a file argument specifying the path to the FNM apps JSON file. Descriptions are always fetched from the registry. If the file doesn't exist, an empty `{}` JSON file is created automatically.

```bash
datamitsu devtools pull-fnm <file>
datamitsu devtools pull-fnm config/src/fnmApps.json
datamitsu devtools pull-fnm config/src/fnmApps.json --update
```

| Flag        | Description                                           |
| ----------- | ----------------------------------------------------- |
| `--update`  | Update versions in the JSON file with latest from npm |
| `--dry-run` | Show results without writing to file                  |

**Examples:**

```bash
# Preview available npm updates without modifying files
datamitsu devtools pull-fnm config/src/fnmApps.json --dry-run

# Apply updates from npm registry
datamitsu devtools pull-fnm config/src/fnmApps.json --update

# After updating, regenerate lock files for affected apps
datamitsu config lockfile prettier
datamitsu config lockfile eslint
```

:::tip See also
For the full FNM app update workflow including lock file regeneration, see [Maintaining Wrapper Packages — FNM Apps](/docs/how-to/maintain-wrapper#fnm-apps-npm-devtools-pull-fnm).
:::

### devtools pull-uv

Pull latest Python package versions from PyPI. Requires a file argument specifying the path to the UV apps JSON file. Descriptions are always fetched from the registry. If the file doesn't exist, an empty `{}` JSON file is created automatically.

```bash
datamitsu devtools pull-uv <file>
datamitsu devtools pull-uv config/src/uvApps.json
datamitsu devtools pull-uv config/src/uvApps.json --update
```

| Flag        | Description                                            |
| ----------- | ------------------------------------------------------ |
| `--update`  | Update versions in the JSON file with latest from PyPI |
| `--dry-run` | Show results without writing to file                   |

**Examples:**

```bash
# Preview available PyPI updates without modifying files
datamitsu devtools pull-uv config/src/uvApps.json --dry-run

# Apply updates from PyPI
datamitsu devtools pull-uv config/src/uvApps.json --update

# After updating, regenerate lock files for affected apps
datamitsu config lockfile yamllint
```

:::tip See also
For the full UV app update workflow including lock file regeneration, see [Maintaining Wrapper Packages — UV Apps](/docs/how-to/maintain-wrapper#uv-apps-python-devtools-pull-uv).
:::

### devtools pull-runtimes

Pull runtime configurations (FNM, UV, JVM) with latest versions from upstream releases. Fetches latest releases from GitHub, computes SHA-256 hashes, and writes the result to `<file>`.

```bash
datamitsu devtools pull-runtimes --update <file>
```

| Flag               | Description                                                   |
| ------------------ | ------------------------------------------------------------- |
| `--update`         | Required. Fetch latest versions from upstream before updating |
| `--dry-run`        | Show what would be updated without writing files              |
| `--runtime <name>` | Update only the specified runtime (`fnm`, `uv`, or `jvm`)     |

The command detects binaries for all platform combinations (OS/Arch/Libc). For Linux, both glibc and musl variants are detected when upstream provides separate binaries. If a musl binary is identical to the glibc variant (same URL and hash), the musl entry is deduplicated.

**Version sources:**

- **FNM**: Node.js LTS from endoflife.date, PNPM from npm registry, FNM binary from GitHub
- **UV**: Python stable from endoflife.date, UV binary from GitHub
- **JVM**: Java version from Adoptium API, Temurin JDK from GitHub

**Examples:**

```bash
# Update all runtimes
datamitsu devtools pull-runtimes --update config/src/runtimes.json

# Update only UV runtime
datamitsu devtools pull-runtimes --update --runtime uv config/src/runtimes.json

# Preview changes without writing
datamitsu devtools pull-runtimes --update --dry-run config/src/runtimes.json
```

:::tip See also
For the full runtime update workflow and CI automation, see [Maintaining Wrapper Packages — Runtimes](/docs/how-to/maintain-wrapper#runtimes-devtools-pull-runtimes).
:::

### devtools verify-all

Cross-platform config integrity checker. Downloads and hash-verifies binary apps and managed runtimes for all configured platforms.

```bash
datamitsu devtools verify-all
```

| Flag                 | Description                                                         |
| -------------------- | ------------------------------------------------------------------- |
| `--no-version-check` | Skip version command execution and comparison                       |
| `--concurrency <n>`  | Concurrent download workers (default: `DATAMITSU_CONCURRENCY` or 3) |
| `--json`             | Output machine-readable JSON                                        |
| `--skip-passed`      | Skip checks whose config is unchanged and passed last run           |
| `--no-remote`        | Skip loading remote configs                                         |

### devtools pack-inline-archive

Pack a directory into a brotli-compressed tar archive for use in inline archive configs.

```bash
datamitsu devtools pack-inline-archive <directory>
```

Output is written to stdout in `tar.br:` format. Archives are deterministic.

### devtools apps list

List all configured apps with their type, version, description, and install status. For binary apps, the resolved target (including libc variant) is determined by the current host's target detection. This helps verify which binary variant would be selected on the current system.

```bash
datamitsu devtools apps list
```

### devtools apps inspect

Show install path and file tree for an installed app. For binary apps, the inspect output reflects the resolved target for the current host, including any libc fallback that may have occurred during resolution.

```bash
datamitsu devtools apps inspect <name>
```

### devtools apps path

Print the install directory path for an app.

```bash
datamitsu devtools apps path <name>
```

### devtools bundles list

List all configured bundles with name, version, and install status.

```bash
datamitsu devtools bundles list
```

### devtools bundles inspect

Show install path and file tree for a bundle (collapses heavy directories).

```bash
datamitsu devtools bundles inspect <name>
```

### devtools bundles path

Print the install directory path for a bundle.

```bash
datamitsu devtools bundles path <name>
```

### Troubleshooting devtools commands

**File not found errors:**

If the JSON file argument doesn't exist, `pull-github`, `pull-fnm`, and `pull-uv` create an empty file automatically. However, `pull-runtimes` requires the `--update` flag to write — running without it produces an error.

**GitHub API rate limits:**

When `pull-github` or `pull-runtimes` fails with HTTP 403 or 429 errors, set the `GITHUB_TOKEN` environment variable to authenticate and increase the rate limit:

```bash
export GITHUB_TOKEN=ghp_your_token_here
datamitsu devtools pull-github config/src/githubApps.json --update
```

**Hash mismatches:**

If `verify-all` reports hash mismatches, the upstream binary may have changed without a version bump (a re-released tag). Re-run the corresponding `pull-*` command with `--update` to fetch fresh hashes:

```bash
datamitsu devtools pull-github config/src/githubApps.json --update
datamitsu devtools verify-all
```

**Network errors:**

All devtools commands require network access to fetch from GitHub, npm, or PyPI. If you're behind a proxy, ensure `HTTPS_PROXY` is set. For intermittent failures, retry the command — downloads are idempotent.

## cache

Manage the per-project cache for linting and fixing operations.

### cache clear

Clear cache data.

```bash
datamitsu cache clear
```

| Flag        | Description                                             |
| ----------- | ------------------------------------------------------- |
| `--all`     | Clear all project caches (not just the current project) |
| `--dry-run` | Show what would be deleted without deleting             |

### cache path

Print the absolute path to the global cache directory.

```bash
datamitsu cache path
```

### cache path project

Print the absolute path to the current project's cache directory.

```bash
datamitsu cache path project
```

## store

Manage the global binary and runtime store.

### store path

Print the absolute path to the global store directory.

```bash
datamitsu store path
```

### store clear

Remove the entire global store directory including all binaries, runtimes, apps, and remote configs.

```bash
datamitsu store clear
```

:::warning
This removes all downloaded binaries and runtimes. You will need to run `datamitsu init` again afterward.
:::

## version

Print the version number.

```bash
datamitsu version
```

## Environment Variables

| Variable                         | Description                                           | Default                                             |
| -------------------------------- | ----------------------------------------------------- | --------------------------------------------------- |
| `DATAMITSU_CACHE_DIR`            | Custom base directory for cache and store paths       | `$XDG_CACHE_HOME/datamitsu` or `~/.cache/datamitsu` |
| `DATAMITSU_CONCURRENCY`          | Number of concurrent download workers                 | `3`                                                 |
| `DATAMITSU_MAX_PARALLEL_WORKERS` | Max parallel tool execution workers                   | `max(4, floor(NumCPU * 0.75))`, capped at 16        |
| `DATAMITSU_LOG_LEVEL`            | Log level (debug, info, warn, error)                  | `info`                                              |
| `DATAMITSU_TIMINGS`              | Enable detailed timing output (1=enabled, 0=disabled) | `0`                                                 |
| `DATAMITSU_BINARY_COMMAND`       | Override binary command path                          | -                                                   |
| `DATAMITSU_NO_SPONSOR`           | Suppress sponsor messages in CLI output               | -                                                   |
| `NO_COLOR`                       | Disable color output                                  | -                                                   |
| `FORCE_COLOR`                    | Force color output                                    | -                                                   |
