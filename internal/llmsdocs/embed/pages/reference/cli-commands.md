# CLI Commands

> Complete reference for all datamitsu CLI commands

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

When called without arguments, lists all available tools grouped by type (binary, uv, node, jvm, go, shell).

An app is installed on demand the first time you `exec` it. If that app declares `links`, its `.datamitsu/` symlinks are created at the same time — this is how a `lazy: true` link-app that `init` deferred gets its managed-config links on first use.

**Examples:**

```bash
# List all available tools
datamitsu exec

# Run golangci-lint
datamitsu exec golangci-lint run ./...

# Run eslint via the managed Node.js runtime
datamitsu exec eslint --fix src/
```

## install

Install one or more managed apps (and optionally runtimes) into the store **without executing them**. Where `exec` prepares and runs an app, `install` materializes its files and exits — the building block for the per-app stages of a generated Dockerfile (see [devtools dockerfile](#devtools-dockerfile)).

```bash
datamitsu install [app...]
```

| Flag               | Description                                                        |
| ------------------ | ------------------------------------------------------------------ |
| `--runtime <name>` | Install the named runtime(s) only, without any app (repeatable)    |
| `--no-verify`      | Skip the post-install version check (verification runs by default) |

By default, each installed app's version command (`--version`, or its configured `versionCheck.args`) is run after installation, so a broken install fails the command instead of producing a broken artifact. Apps whose version check is disabled, and shell apps, are skipped. Pass `--no-verify` to skip the check.

**Examples:**

```bash
# Install a single tool
datamitsu install shellcheck

# Install only the node runtime (no app)
datamitsu install --runtime node

# Install and verify each tool actually runs
datamitsu install eslint prettier --verify
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
3. Installs runtime-managed apps (node/UV/JVM) that are referenced by tools
4. Creates `.datamitsu/` symlinks for the config files of installed link-apps — an app marked `lazy: true` is deferred and gets its links on first `datamitsu exec` instead
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

| Flag                         | Description                                                                                                                    |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `--explain [mode]`           | Show execution plan without running. Modes: `summary` (default), `detailed`, `json`                                            |
| `--file-scoped`              | Only process git staged files                                                                                                  |
| `--tools <list>`             | Comma-separated list of tools to run                                                                                           |
| `--fail-on-skip`             | Exit non-zero if any tool is skipped because its binary is unavailable for this platform (see [Skipped tools](#skipped-tools)) |
| `--widen-to <level>`         | Limit how far work may widen beyond what you asked for: `target`, `unit` or `repo` (see [Narrowed runs](#narrowed-runs))       |
| `--require-coverage <level>` | Exit non-zero unless the run answered completely: `unit` or `repo` (see [Narrowed runs](#narrowed-runs))                       |

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

# In CI: fail if a tool you rely on has no binary for the runner's platform
datamitsu check --fail-on-skip
```

### Skipped tools

:::info
A tool is reported as **skipped** (not run, not failed) for one of two reasons:

- **Disabled in config** — the tool sets `skip: true` (optionally with a `skipReason`).
- **No binary for this platform** — the tool's binary has no build for the current
  OS/architecture/libc. This is a soft skip: the run still succeeds.

Skipped tools appear as `⊘ <tool> skipped (<reason>)` lines and a `· N skipped`
count in the summary footer, and as a `skipped` array in `--explain=json`.

`--fail-on-skip` makes the run exit non-zero **only** for platform skips (a tool
you expected to run had no binary). Intentional `skip: true` tools never fail the run.

A third reason appears when you narrow a run — see [Narrowed runs](#narrowed-runs).
:::

### Narrowed runs

Naming files, or running from a subdirectory, asks for less than the whole
repository. Most tools follow: a formatter given one file formats that file.
Some cannot. `syncpack` compares versions **across** `package.json` files, so a
one-file answer is not a smaller answer, it is a wrong one.

datamitsu decides this per operation, from the tool's
[granularity](configuration-api.md#granularity). A narrowable tool runs on what
you asked for. One whose verdict covers the whole repository is reported as
skipped:

```console
$ cd packages/api
$ datamitsu fix ./swagger.json
✓ oxfmt                packages/api/swagger.json
⊘ syncpack             skipped (whole-repository verdict — cannot narrow)
```

Before this behaviour existed those tools were dropped silently — absent from the
report, from `--explain`, from everything. If you were relying on that silence,
the tools have not changed; only the reporting has.

**`--widen-to`** overrides how far the core may go beyond your request, in either
direction — you typed it for this one run, so it wins over the project's policy:

```bash
# Only what I named. Anything needing more is reported, not run.
datamitsu fix ./swagger.json --widen-to=target

# Run the whole-repository tools anyway.
datamitsu fix ./swagger.json --widen-to=repo
```

The policy governs unit-scoped tools too, not only whole-repository ones. A tool
whose command line carries no path — `go fmt ./...`, `golangci-lint run`, and most
formatters that find their own files — reads its entire project whatever you
named, so naming one file runs it over that file's project and no other. Under
`target` it is reported rather than run, because a whole project is more than what
you named. Tools that take a `{files}` list are already narrowed by that list and
are unaffected.

Set the project default in config with
[`execution.widenTo`](configuration-api.md#executionwidento).

**`--require-coverage`** turns an incomplete answer into a failure, for CI:

```bash
# Every unit the run touched was fully analysed.
datamitsu check --require-coverage=unit

# ...and the run covered the repository at all.
datamitsu check --require-coverage=repo
```

`unit` fails if a tool could not be narrowed, if a task covered only part of its
unit, or if the host lacked a binary. `repo` adds that the run must have targeted
the whole repository — without that clause, `check --require-coverage pkg/x.ts`
would pass having checked one package of nine.

A tool you disabled with `skip: true` never counts against coverage: a repository
that opts a tool out has given its answer, not an incomplete one.

It works under `--explain` too, since coverage is decided while planning — a CI
gate that costs one plan and downloads nothing. It cannot be combined with
`--tools`, which would make the assertion cover only the selected subset.

## fix

Run fix operations on files.

```bash
datamitsu fix [files...]
```

| Flag                         | Description                                                                                                                    |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `--explain [mode]`           | Show execution plan without running. Modes: `summary` (default), `detailed`, `json`                                            |
| `--file-scoped`              | Only process git staged files                                                                                                  |
| `--tools <list>`             | Comma-separated list of tools to run                                                                                           |
| `--fail-on-skip`             | Exit non-zero if any tool is skipped because its binary is unavailable for this platform (see [Skipped tools](#skipped-tools)) |
| `--widen-to <level>`         | Limit how far work may widen beyond what you asked for: `target`, `unit` or `repo` (see [Narrowed runs](#narrowed-runs))       |
| `--require-coverage <level>` | Exit non-zero unless the run answered completely: `unit` or `repo` (see [Narrowed runs](#narrowed-runs))                       |

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

| Flag                         | Description                                                                                                                    |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `--explain [mode]`           | Show execution plan without running. Modes: `summary` (default), `detailed`, `json`                                            |
| `--file-scoped`              | Only process git staged files                                                                                                  |
| `--tools <list>`             | Comma-separated list of tools to run                                                                                           |
| `--fail-on-skip`             | Exit non-zero if any tool is skipped because its binary is unavailable for this platform (see [Skipped tools](#skipped-tools)) |
| `--widen-to <level>`         | Limit how far work may widen beyond what you asked for: `target`, `unit` or `repo` (see [Narrowed runs](#narrowed-runs))       |
| `--require-coverage <level>` | Exit non-zero unless the run answered completely: `unit` or `repo` (see [Narrowed runs](#narrowed-runs))                       |

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

| Flag               | Description                                                                                                                                                                |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--dry-run`        | Show what would be done without making changes                                                                                                                             |
| `--skip-fix`       | Skip running fix after setup                                                                                                                                               |
| `--opt-in-tools`   | Also generate an all-disabled `.datamitsuignore` so tools are opt-in (enable them by removing names from the list)                                                         |
| `--tools`          | Comma-separated list of tools to scope setup to (only their config files are written)                                                                                      |
| `--no-verify-hash` | Skip [`expectChainHash`](./configuration-api.md#pinning-the-upstream-chain-expectchainhash) verification (write even when a pinned config drifted from its upstream chain) |

Setup detects project types, generates configuration files, and optionally runs fix afterward.

With `--tools`, setup is scoped to the named tools: it (re)generates only the
config files associated with them (via each setup entry's [`tools`](./configuration-api.md#config-setup-setup) field)
and leaves everything else untouched — other tools' configs and unassociated
infrastructure files (`.gitignore`, `lefthook.yaml`, `pnpm-workspace.yaml`, …)
are skipped. The post-setup fix is scoped to the same tools. This is the
recommended way to iterate on a single tool's config without rewriting your whole
project. Unknown tool names fail fast before anything is written; a selected tool
that owns no config file is reported and skipped (not an error).

With `--opt-in-tools`, setup runs the normal flow and then writes a
`.datamitsuignore` at the git root that disables **every** configured tool via a
single `**/*: <tool names>` rule. You then enable tools one at a time by removing
their names from that list. The file is written before the post-setup fix (so fix
respects the opt-in state), and setup refuses to overwrite an existing
`.datamitsuignore`. See [Generating an all-disabled file](./ignore-rules.md#generating-an-all-disabled-file).

Any config file that pins [`expectChainHash`](./configuration-api.md#pinning-the-upstream-chain-expectchainhash)
is verified before setup writes anything: if its upstream chain drifted from the
pinned hash, setup aborts with a report (the expected/actual hash and the full
incoming content) and **no files are written**. Review the change against your
overrides, update the pin, and re-run — or pass `--no-verify-hash` to write
regardless. The check runs in `--dry-run` too.

**Examples:**

```bash
# Set up configs
datamitsu setup

# Preview changes
datamitsu setup --dry-run

# Set up configs and start with all tools disabled (opt-in model)
datamitsu setup --opt-in-tools

# Regenerate only golangci-lint's config (e.g. .golangci.yml); nothing else is touched
datamitsu setup --tools golangci-lint

# Preview a tool-scoped setup
datamitsu setup --tools golangci-lint --dry-run

# Scope to several tools at once
datamitsu setup --tools golangci-lint,prettier
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

### config runtime

Display the full effective runtime configuration snapshot as JSON — env-resolved execution limits, the per-app install timeout, and the minimum release age. This is the introspection/debug surface: it reflects what the program runs with right now, including environment overrides. It is **not** the surface injected into the config JS VM (config JS receives only the minimal allowlisted `datamitsuConfigInputs`).

```bash
datamitsu config runtime
```

**Examples:**

```bash
# Inspect the full effective runtime config
datamitsu config runtime

# Mechanically verify a single value (and that env overrides apply)
datamitsu config runtime | jq .minimumReleaseAgeMinutes
DATAMITSU_INSTALL_TIMEOUT=1200 datamitsu config runtime | jq .installTimeoutSeconds  # -> 1200
```

### config chain-hash

Print the XXH3-128 chain hash that `datamitsu setup` verifies for managed config files — the hash of the content entering each file's root (topmost) config layer. Copy it into a setup entry's [`expectChainHash`](./configuration-api.md#pinning-the-upstream-chain-expectchainhash) to pin the upstream baseline your overrides were written against, without having to trigger a drift error to read it.

```bash
datamitsu config chain-hash [file...]
```

The value is the input to the **topmost** layer, so declare your own entry for the file first (a placeholder `expectChainHash` is enough), then read the real hash here. With no arguments every setup file is listed as `file  hash`; with exactly one file only its bare hash is printed, which is convenient for scripting.

**Examples:**

```bash
# List the chain hash of every setup file
datamitsu config chain-hash

# Print just one file's hash (capture it into a variable / your config)
datamitsu config chain-hash eslint.config.mjs
pin=$(datamitsu config chain-hash eslint.config.mjs)
```

### config lockfile

Generate lock file content for a runtime-managed app (node/UV).

```bash
datamitsu config lockfile [appName]
```

Without arguments, lists all apps that support lock files. With an app name, reinstalls the app from scratch and outputs the lock file content as a brotli-compressed, base64-encoded string for embedding in configuration.

**Examples:**

```bash
# List apps that support lock files
datamitsu config lockfile

# Generate lock file for a node app
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

| Flag                  | Description                                                                                                                                                                                                                |
| --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--update`            | Fetch latest release tags before updating                                                                                                                                                                                  |
| `--verify-extraction` | Verify that downloaded archives extract correctly                                                                                                                                                                          |
| `--min-age <minutes>` | Minimum release age before a version is eligible (`-1` = global default of `10080`, `0` = disable, positive = custom). See [Minimum Release Age](/docs/guides/supply-chain-security#minimum-release-age-version-selection) |

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

Before scoring, the detector filters out two categories of assets: checksum files (`.sha256`, `.md5`, `.sha512`, etc.) and non-executable package formats (`.vsix`, `.deb`, `.rpm`, `.nupkg`, `.whl`, `.msi`, `.pkg`). This prevents IDE extensions or package-manager bundles from outscoring actual binaries when releases mix executable and non-executable assets.

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

### devtools pull-node

Pull latest npm package versions from the npm registry. Requires a file argument specifying the path to the node apps JSON file. Descriptions are always fetched from the registry. If the file doesn't exist, an empty `{}` JSON file is created automatically.

```bash
datamitsu devtools pull-node <file>
datamitsu devtools pull-node config/src/nodeApps.json
datamitsu devtools pull-node config/src/nodeApps.json --update
```

| Flag                  | Description                                                                                                                                                                                                                |
| --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--update`            | Update versions in the JSON file with latest from npm                                                                                                                                                                      |
| `--dry-run`           | Show results without writing to file                                                                                                                                                                                       |
| `--min-age <minutes>` | Minimum release age before a version is eligible (`-1` = global default of `10080`, `0` = disable, positive = custom). See [Minimum Release Age](/docs/guides/supply-chain-security#minimum-release-age-version-selection) |

**Examples:**

```bash
# Preview available npm updates without modifying files
datamitsu devtools pull-node config/src/nodeApps.json --dry-run

# Apply updates from npm registry
datamitsu devtools pull-node config/src/nodeApps.json --update

# After updating, regenerate lock files for affected apps
datamitsu config lockfile prettier
datamitsu config lockfile eslint
```

:::tip See also
For the full node app update workflow including lock file regeneration, see [Maintaining Wrapper Packages — Node Apps](/docs/how-to/maintain-wrapper#node-apps-npm-devtools-pull-node).
:::

### devtools pull-uv

Pull latest Python package versions from PyPI. Requires a file argument specifying the path to the UV apps JSON file. Descriptions are always fetched from the registry. If the file doesn't exist, an empty `{}` JSON file is created automatically.

```bash
datamitsu devtools pull-uv <file>
datamitsu devtools pull-uv config/src/uvApps.json
datamitsu devtools pull-uv config/src/uvApps.json --update
```

| Flag                  | Description                                                                                                                                                                                                                |
| --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--update`            | Update versions in the JSON file with latest from PyPI                                                                                                                                                                     |
| `--dry-run`           | Show results without writing to file                                                                                                                                                                                       |
| `--min-age <minutes>` | Minimum release age before a version is eligible (`-1` = global default of `10080`, `0` = disable, positive = custom). See [Minimum Release Age](/docs/guides/supply-chain-security#minimum-release-age-version-selection) |

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

Pull runtime configurations (Node, UV, JVM, Go) with latest versions from upstream releases. Fetches latest releases from upstream, computes SHA-256 hashes, and writes the result to `<file>`.

```bash
datamitsu devtools pull-runtimes --update <file>
```

| Flag                  | Description                                                                                                                                                                                                                                                                                                                 |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--update`            | Required. Fetch latest versions from upstream before updating                                                                                                                                                                                                                                                               |
| `--dry-run`           | Show what would be updated without writing files                                                                                                                                                                                                                                                                            |
| `--runtime <name>`    | Update only the specified runtime (`node`, `uv`, `jvm`, or `go`)                                                                                                                                                                                                                                                            |
| `--min-age <minutes>` | Minimum release age before a version is eligible (`-1` = global default of `10080`, `0` = disable, positive = custom). Applies to specific-version sources (GitHub releases, npm pnpm), not major-version-line lookups. See [Minimum Release Age](/docs/guides/supply-chain-security#minimum-release-age-version-selection) |

The command detects binaries for all platform combinations (OS/Arch/Libc). For Linux, both glibc and musl variants are detected when upstream provides separate binaries. If a musl binary is identical to the glibc variant (same URL and hash), the musl entry is deduplicated.

**Version sources:**

- **node**: latest Node.js LTS resolved automatically; archives + SHA-256 from nodejs.org/dist (glibc/darwin/windows, GPG-verified via SHASUMS256.txt.asc) and unofficial-builds.nodejs.org (musl); pnpm from the npm registry
- **UV**: Python stable from endoflife.date, UV binary from GitHub
- **JVM**: Java version from Adoptium API, Temurin JDK from GitHub
- **go**: latest stable Go release + per-file SHA-256 from go.dev (`https://go.dev/dl/?mode=json`); HTTPS with published SHA-256, no GPG (the git-pinned hash is the integrity anchor, same trust model as the musl Node path)

**Examples:**

```bash
# Update all runtimes
datamitsu devtools pull-runtimes --update config/src/runtimes.json

# Update only UV runtime
datamitsu devtools pull-runtimes --update --runtime uv config/src/runtimes.json

# Update only Go runtime
datamitsu devtools pull-runtimes --update --runtime go config/src/runtimes.json

# Preview changes without writing
datamitsu devtools pull-runtimes --update --dry-run config/src/runtimes.json
```

:::tip See also
For the full runtime update workflow and CI automation, see [Maintaining Wrapper Packages — Runtimes](/docs/how-to/maintain-wrapper#runtimes-devtools-pull-runtimes).
:::

### devtools dockerfile

Generate an optimized, digest-pinned multi-stage Dockerfile from the loaded config. Emits a config-free base, a `config-split` stage, then one build stage per binary app, per managed runtime, and per runtime-managed app (each inheriting its runtime stage), then assembles the populated store with `COPY --link` — one cacheable layer per app, so bumping one app re-pulls only its layer. The base never carries the config and each stage loads only its own slice (see [devtools split-config](#devtools-split-config)), so editing or regenerating the config rebuilds only the stages whose slice actually changed instead of every tool.

```bash
datamitsu devtools dockerfile -o docker/Dockerfile
```

| Flag                     | Description                                                                                                    |
| ------------------------ | -------------------------------------------------------------------------------------------------------------- |
| `-o, --output <path>`    | **Required.** Output Dockerfile path including filename, fully overwritten on each run                         |
| `--alpine`               | Target the Alpine (musl) base image variant (`<version>-alpine`)                                               |
| `--offline`              | Skip base-image digest resolution; emit an unpinned `FROM` with a warning                                      |
| `--no-verify`            | Do not run apps' version checks in their build stages (verification is on by default)                          |
| `--config-js <path>`     | Pre-built config file COPYed into the image (default `datamitsu.config.js`)                                    |
| `--repo <repo>`          | Override the base image repository (default: the repo this datamitsu build was published under)                |
| `--label <key=value>`    | OCI label for the final image (repeatable)                                                                     |
| `--arg <name[=val]>`     | Build `ARG` declared before `ENV` in the final stage; bare name or name=default (repeatable)                   |
| `--build-arg <n[=v]>`    | Build-time `ARG`→`ENV` in a `dm-build` stage so install stages inherit it; not in the final image (repeatable) |
| `--env <key=value>`      | `ENV` var baked into the final image (repeatable)                                                              |
| `--force-include <apps>` | Keep binary apps that lack a binary for the target libc (comma-separated, repeatable)                          |
| `--emit-oci-map <path>`  | Also write the layer→subtree map JSON consumed by the OCI bundle annotation post-process                       |

**libc filtering.** The generated image targets one libc — **musl** with `--alpine`, **glibc** otherwise — and binary apps are filtered to those that ship a binary for it on every arch they declare. A glibc-only binary can't execute on a musl image (and vice versa), so incompatible apps are dropped from the Dockerfile and listed in a warning. Runtime-managed apps (node/uv/jvm/go) are unaffected — their runtime carries the libc. Statically-linked tools that run on any libc but are under-declared in the registry (e.g. a static Go binary recorded as glibc-only) can be added back with `--force-include name1,name2`.

The base image **repository and tag are baked into the datamitsu binary at release time**, so the `FROM` points at the exact image this build came from — including across release channels, where the stable image (`datamitsu/datamitsu:<version>`) and the unstable image (`datamitsu/datamitsu-unstable:<unstable-tag>`) live in different repositories under different tags. The registry host defaults to `ghcr.io` (override with `DATAMITSU_OCI_REGISTRY`); `--repo` overrides the repository for mirrors or forks. The tag is resolved to a SHA-256 digest and pinned as `FROM …@sha256:…`. Pinning is best-effort and never fails the command: `--offline`, an unreachable registry, or a non-release (`dev`/unstable) build leave the `FROM` unpinned with a warning. The output file is fully overwritten (no managed regions).

**Examples:**

```bash
# Generate the glibc and Alpine variants
datamitsu devtools dockerfile -o docker/Dockerfile
datamitsu devtools dockerfile -o docker/Dockerfile.alpine --alpine

# Offline (no digest pinning) — useful for CI drift checks
datamitsu devtools dockerfile -o docker/Dockerfile --offline

# Resolve the base digest from a mirror registry
DATAMITSU_OCI_REGISTRY=mirror.internal datamitsu devtools dockerfile -o docker/Dockerfile
```

:::tip See also
For the full image-publishing workflow and CI drift guard, see [Maintaining Wrapper Packages — Generating a Docker image](/docs/how-to/maintain-wrapper#generating-a-docker-image-devtools-dockerfile).
:::

### devtools split-config

Write one minimal config slice per app and per runtime into a directory. Each slice is a self-contained config defining exactly one stage's target — a single binary, a single runtime, or a single runtime-managed app plus the runtime it installs under — that `install --config` can load on its own.

```bash
datamitsu devtools split-config -o ./slices
```

| Flag                 | Description                                          |
| -------------------- | ---------------------------------------------------- |
| `-o, --output <dir>` | **Required.** Output directory for the config slices |

This is the build-cache primitive behind [devtools dockerfile](#devtools-dockerfile): the generated Dockerfile runs it in the `config-split` stage so every other stage loads only its own slice. Editing one app then changes only that app's slice — and so invalidates only that app's build cache — instead of busting the whole image. You rarely run it directly; it is documented because it appears in the generated Dockerfile. The config is read from the usual sources (`--config` / `--before-config` / auto-discovery).

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

### devtools parsers

Inspect the WASM output-parser modules declared in the [`parsers`](./configuration-api.md#output-parsers-parsers)
config and the tools they can parse. Each module self-describes (via its WASM
`describe` export) which tools it parses, how to invoke each, its upstream URL, and
its build-injected version — so this is the source of truth, not the config.

`list` aggregates every configured parser into a **deduplicated** catalog (a module
declared by N tools is described once); `inspect` shows the full detail for one
tool. Both accept:

- `--json` — machine-readable output, for driving configs or build pipelines.
- `--wasm <path>` — describe a local `.wasm` file directly, with no config or
  network access (handy in CI and release tooling).

```bash
# Human-readable catalog of all tools the configured parsers can parse
datamitsu devtools parsers list

# Machine-readable catalog (tools is [] when none are configured)
datamitsu devtools parsers list --json

# Full detail for one tool
datamitsu devtools parsers inspect hadolint

# Describe a locally built module without any config
datamitsu devtools parsers list --wasm ./parsers/target/wasm32-unknown-unknown/release/datamitsu_parsers.wasm
```

`run` pipes a tool's raw output (from stdin) through its parser and prints the
structured diagnostics — the quickest way to develop or debug a parser against
real output (parsers are not yet wired into the lint pipeline). It reads stdout
from stdin; pass `--stderr-file` / `--exit-code` for parsers that use them, and
`--wasm <path>` to use a local module instead of a configured one.

```bash
# Run eslint through datamitsu, then parse its JSON into diagnostics
datamitsu exec eslint -- --format json file.js \
  | datamitsu devtools parsers run eslint --wasm ./datamitsu_parsers.wasm --exit-code 1
```

`prefetch` downloads and SHA-256 verifies the configured parser modules into the
store, without compiling or running anything. Each module lands at its
content-addressed store path, so it can be `COPY`ed into an OCI-bundle layer (the
generated Dockerfile's parser stages do exactly this) and found offline later. A
module already on disk whose bytes still match the declared SHA-256 is a no-op —
and one whose bytes do not is discarded and fetched again, whatever put it
there.

```bash
# Fetch every module declared in the parsers config
datamitsu devtools parsers prefetch

# Fetch only a subset
datamitsu devtools parsers prefetch datamitsu-parsers
```

### devtools tools

Inspect the tools declared in the config — the fix/lint units datamitsu plans and
runs. `list` shows each tool's operations, project types, the app it runs, and its
output parser; `inspect` shows full per-operation detail. Both accept `--json`.

```bash
# All configured tools: name [operations] (projectTypes) → parser
datamitsu devtools tools list

# Machine-readable
datamitsu devtools tools list --json

# Full detail for one tool (per-operation app/scope/globs/args)
datamitsu devtools tools inspect eslint
```

### Troubleshooting devtools commands

**File not found errors:**

If the JSON file argument doesn't exist, `pull-github`, `pull-node`, and `pull-uv` create an empty file automatically. However, `pull-runtimes` requires the `--update` flag to write — running without it produces an error.

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

### store seed

Pull the [OCI bundle](/docs/guides/oci-bundles) declared by the config's top-level `oci` key into the global store — no docker/podman required. Without arguments the **whole** bundle is pulled (airgap seeding); with `--apps` only the layers of the named tools plus their runtime dependencies are pulled.

```bash
# Full pull from the config's oci declaration
datamitsu store seed

# Selective pull
datamitsu store seed --apps golangci-lint,prettier

# Explicit digest-pinned reference (overrides the config)
datamitsu store seed ghcr.io/owner/tool-store@sha256:0123…cdef
```

| Flag            | Description                                                                       |
| --------------- | --------------------------------------------------------------------------------- |
| `--apps <list>` | Seed only the layers of these apps and their runtime dependencies (repeatable)    |
| `--resolve-tag` | Allow a `:<tag>` reference: resolve it to a digest first and print it for pinning |

Every manifest body and blob is verified against its SHA-256 descriptor before extraction; seeded single-file binaries and JVM jars are additionally re-hashed against the published hashes from the config. A repeated full pull is a no-op (zero network requests).

Blob downloads have no overall timeout — only an attempt that delivers zero bytes for 2 minutes is aborted and retried, so large layers on slow links finish instead of being cut off mid-stream. The `store` commands also work without a usable git context (no `git` binary, `dubious ownership` in containers): a broken project repo only skips the project-level config with a warning.

### store status

Show what the declared bundle contains for this platform and which configured apps it covers versus which still require the network.

```bash
datamitsu store status
datamitsu store status --json
```

### store refs

List every artifact the effective config fetches from outside the machine: OCI references (the bundle and any registry-sourced parser modules) first, then the hash-pinned https downloads. It answers "what do I have to mirror?" and touches no network, so it runs inside a firewall.

```bash
# Everything this config pulls from anywhere
datamitsu store refs

# Just the registry side — the input to a mirroring loop
datamitsu store refs --oci-only | xargs -n1 -I{} crane copy {} harbor.corp/dm/{}

# Every artifact with its mandatory hash and platform
datamitsu store refs --json | jq '.https[] | select(.kind == "app-binary")'
```

| Flag         | Description                                                           |
| ------------ | --------------------------------------------------------------------- |
| `--json`     | Emit the references as JSON, including each artifact's mandatory hash |
| `--oci-only` | List only OCI references, omitting the hash-pinned https downloads    |

The output is sorted and byte-stable, so it is safe to pipe or diff between config revisions. Runtime archives (node, uv, the JDK) resolve from a generated manifest at install time rather than from the config, so they are not listed.

### store import

Seed the store from a local [OCI image layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) directory (as produced by `crane pull --format=oci`, `oras copy --to-oci-layout`, or `skopeo copy`) — fully offline bundle transfer. Blobs are verified against the digest chain exactly like a registry pull.

```bash
datamitsu store import ./bundle-layout
datamitsu store import ./bundle-layout --digest sha256:0123…cdef
```

## version

Print the version number.

```bash
datamitsu version
```

## llms

Print this documentation as plain markdown, for feeding to an AI agent.

The docs are compiled into the binary, so they always match the version you are
running and are served without network access. Content goes to stdout and
diagnostics to stderr, so the output pipes straight into a model's context.

With no arguments it prints an index of every page, where each entry is the exact
argument that fetches it — an agent can read the index and choose its next call
without knowing anything about the site's URLs.

```bash
# Index of every available page
datamitsu llms

# One page, by its documentation path
datamitsu llms about
datamitsu llms contributing/brand-guidelines

# A unique page name may be abbreviated
datamitsu llms brand-guidelines

# A directory's own page is addressed by the directory
datamitsu llms getting-started/installation

# A URL copied from the website works too
datamitsu llms https://datamitsu.com/docs/guides/architecture/caching.md
```

Flags:

| Flag     | Description                                            |
| -------- | ------------------------------------------------------ |
| `--list` | List every page slug, one per line                     |
| `--json` | Emit JSON instead of markdown                          |
| `--web`  | Print the standard `llms.txt` index, with website URLs |

```bash
# Machine-readable page list, with titles and descriptions
datamitsu llms --list --json

# Provenance of the embedded snapshot
datamitsu llms --json | jq .pageSetHash

# One page as JSON, body included
datamitsu llms about --json
```

Exit codes: `0` on success, `2` for a usage error, `3` for an unknown or
ambiguous page. An unknown page suggests the nearest matches on stderr and
leaves stdout empty, so a failed fetch never pollutes a pipe.

## lsp

Run a Language Server Protocol server on stdin/stdout, so an editor can format
files through datamitsu's configured tools.

```bash
datamitsu lsp
```

This is normally launched by an editor rather than by hand — see the
[VS Code extension](../getting-started/installation/vscode.md) for the packaged
integration.

## source

Put the project's declared toolchain on `PATH` for the current shell, so every
tool runs as an ordinary command at the version this repository pins.

```bash
# bash / zsh
eval "$(datamitsu source bash)"
eval "$(datamitsu source zsh)"

# fish
datamitsu source fish | source
```

Activation downloads nothing — a tool is fetched the first time it is actually
run. Switching branches needs no re-activation: the next tool invocation notices
the config changed and re-resolves itself.

`source` requires a project. Run inside a repository whose git root holds a
`datamitsu.config.{js,mjs,ts}`, or name one explicitly with the global
`--config` flag; outside a project it exits non-zero and writes nothing to
stdout.

stdout carries shell code and nothing else, so the output is always safe to
`eval`. Warnings — tools not downloaded yet, system binaries this shadows — go
to stderr.

## Environment Variables

| Variable                         | Description                                                                                     | Default                                             |
| -------------------------------- | ----------------------------------------------------------------------------------------------- | --------------------------------------------------- |
| `DATAMITSU_CACHE_DIR`            | Custom base directory for cache and store paths                                                 | `$XDG_CACHE_HOME/datamitsu` or `~/.cache/datamitsu` |
| `DATAMITSU_CONCURRENCY`          | Number of concurrent download workers                                                           | `3`                                                 |
| `DATAMITSU_INSTALL_TIMEOUT`      | Per-app install timeout in seconds (`0` = disabled)                                             | `600`                                               |
| `DATAMITSU_MIN_RELEASE_AGE`      | Minimum release age in minutes for `pull-*` version selection (`0` = disabled)                  | `10080`                                             |
| `DATAMITSU_MAX_PARALLEL_WORKERS` | Max parallel tool execution workers                                                             | `max(4, floor(NumCPU * 0.75))`, capped at 16        |
| `DATAMITSU_UNIT_CACHE_TTL`       | Minutes a cached project-level verdict stays trusted; `0` disables it                           | `1440` (24h)                                        |
| `DATAMITSU_LSP_FORMAT_WIDEN_TO`  | How far editor format-on-save may widen: `target` or `unit`                                     | `unit`                                              |
| `DATAMITSU_LOG_LEVEL`            | Log level (debug, info, warn, error)                                                            | `info`                                              |
| `DATAMITSU_TIMINGS`              | Enable detailed timing output (1=enabled, 0=disabled)                                           | `0`                                                 |
| `DATAMITSU_STARTUP_TIMINGS`      | Report per-phase startup/config-load durations to stderr (1=enabled, 0=disabled)                | `0`                                                 |
| `DATAMITSU_FORCE_GIT_SUBPROCESS` | Resolve the git root by forking `git` instead of walking the filesystem (1=enabled, 0=disabled) | `0`                                                 |
| `DATAMITSU_BINARY_COMMAND`       | Override binary command path                                                                    | -                                                   |
| `DATAMITSU_NO_SPONSOR`           | Suppress sponsor messages in CLI output                                                         | -                                                   |
| `DATAMITSU_OFFLINE`              | Refuse all network access (requires a pre-seeded store)                                         | -                                                   |
| `DATAMITSU_NO_OCI`               | Disable OCI bundle store **seeding** (twin of `--no-oci`); see the note below                   | -                                                   |
| `DATAMITSU_LIBC`                 | Override host libc detection (`glibc` or `musl`); affects store paths and OCI bundle selection  | auto-detected                                       |
| `DATAMITSU_OCI_REGISTRY`         | Registry host for base-image digest resolution in `devtools dockerfile`                         | `ghcr.io`                                           |
| `DATAMITSU_PARSERS_DIR`          | Override directory for downloaded WASM output-parser modules                                    | `{store}/.parsers`                                  |
| `NO_COLOR`                       | Disable color output                                                                            | -                                                   |
| `FORCE_COLOR`                    | Force color output                                                                              | -                                                   |

`DATAMITSU_FORCE_GIT_SUBPROCESS` applies to the config loader's memoized git-root
lookup, which is where the pure-Go walk runs; the command handlers use a separate
resolver that always forks `git`. See
[Startup and Config Load](../guides/architecture/startup.md).

:::note `--no-oci` is not a network switch
`--no-oci` / `DATAMITSU_NO_OCI` turns off OCI bundle **store seeding** — an
accelerator that degrades gracefully to fetching each artifact individually. It
does **not** disable a parser that declares an `oci` source: that is not an
accelerator but the only route to those bytes, so disabling it would leave no
way to get the module at all.

`DATAMITSU_OFFLINE` is the hard gate. It refuses every network operation,
including a registry-sourced parser, and expects a pre-seeded store.
:::
