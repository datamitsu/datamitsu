---
title: Maintaining Wrapper Packages
description: Workflows for keeping wrapper package tool versions, runtimes, and hashes up to date using devtools commands
---

# Maintaining Wrapper Packages

This guide covers the day-to-day workflows for maintaining a datamitsu wrapper package — keeping tool versions current, verifying integrity across platforms, and automating updates.

## Overview

A **wrapper package** is a datamitsu configuration that bundles a curated set of tools for a specific use case (e.g., a company's standard linting stack). As a wrapper maintainer, your responsibilities include:

- Updating tool versions when new releases are published
- Regenerating lock files for reproducible installs
- Verifying SHA-256 hashes across all supported platforms
- Testing that updated tools work correctly

datamitsu provides `devtools` commands to automate these tasks.

## Global-install parity: declaring `getBeforeConfigs()`

A wrapper package typically ships a `bin/datamitsu` shim that injects `--before-config <shared config>` so the shared configuration loads ahead of the project's own. That shim only fires when datamitsu is invoked **through** it (e.g. via `pnpm exec datamitsu`). A **globally installed** datamitsu run inside the same repo never receives the flag — it skips the shared config, the effective configuration diverges, and the user has to hand-edit the command to reproduce the wrapper's behaviour.

`getBeforeConfigs()` closes that gap. Export it from the project's git-root config to declare the shared config as a local under-layer, so the global binary reproduces the wrapper automatically:

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getBeforeConfigs() {
  return [{ path: "./node_modules/@myorg/datamitsu-config/datamitsu.config.js" }];
}
globalThis.getBeforeConfigs = getBeforeConfigs;

function getConfig(config) {
  // config already includes the shared wrapper config's changes
  return { ...config };
}
globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

The declared path is inserted into the loading order immediately before the auto config — exact parity with the `--before-config` flag (init-layer merging, `getMinVersion()` checks, remote-config resolution all apply identically).

**When to use which:**

| Situation                                          | Mechanism                                                      |
| -------------------------------------------------- | -------------------------------------------------------------- |
| Wrapper-managed repo, always run via the shim      | The shim's `--before-config` is enough — declaring is optional |
| Same repo also run with a **global** `datamitsu`   | Declare `getBeforeConfigs()` so both invocations match         |
| Debugging — want to run the global binary directly | Declare `getBeforeConfigs()`; no command rewriting needed      |

**Precedence (the flag always wins):** when `--before-config` is passed on the CLI, `getBeforeConfigs()` is **not evaluated at all**. This is deliberate — when the wrapper shim is in use, the flag already loads the shared config, and skipping the declaration avoids loading it twice. So adding `getBeforeConfigs()` is safe even for repos that are sometimes run through the shim and sometimes via the global binary.

A few rules to keep in mind:

- Relative paths resolve against the git-root config's directory; absolute paths are used as-is. A missing file is a hard error.
- No hash is required — a local path is in the same trust domain as the root config, not a network download (unlike `getRemoteConfigs()`, which mandates a SHA-256 hash).
- It is honoured **only** in the auto-discovered git-root config. A `getBeforeConfigs()` declared inside the shared config itself (or any other layer) is ignored — there is no chaining.

See [Configuration → Declared Before-Configs](../guides/configuration.md#declared-before-configs-getbeforeconfigs) for the full reference.

## Updating Tool Versions

### Minimum release age (`--min-age`)

All `pull-*` commands refuse to select a release younger than the **minimum release age** (7 days / 10080 minutes by default). This soak time lets the ecosystem catch typosquats and compromised or broken publishes before you pin them. Every command accepts the shared `--min-age <minutes>` flag and prints the effective cutoff in its banner:

- `--min-age -1` (default) — use the global effective minimum release age
- `--min-age 0` — disable filtering and take the newest release
- `--min-age 43200` — custom cutoff (30 days)

```bash
# Default 7-day filter
datamitsu devtools pull-github apps/githubApps.json --update

# Require 30 days of soak time
datamitsu devtools pull-node apps/nodeApps.json --update --min-age 43200

# Bypass the filter (e.g. to adopt a same-day security release)
datamitsu devtools pull-github apps/githubApps.json --update --min-age 0
```

Set `DATAMITSU_MIN_RELEASE_AGE` (minutes) to change the default for every command. When no release is old enough, `pull-github` keeps an existing app's current tag (with a warning) but hard-errors on a brand-new app; `pull-node`/`pull-uv` skip the package with a warning; `pull-runtimes` hard-errors. See [Supply Chain Security → Minimum Release Age](../guides/supply-chain-security.md#minimum-release-age-version-selection) for the full behavior table and the registries it covers.

### Inspecting the effective runtime config (`datamitsu config runtime`)

`datamitsu config runtime` prints the full effective runtime configuration as JSON — the env-resolved view of execution limits, the per-app install timeout, and the minimum release age. Use it to mechanically confirm what the tool will actually run with, including any `DATAMITSU_*` overrides, before kicking off a pull or an install:

```bash
# Inspect the full snapshot
datamitsu config runtime

# Confirm the effective minimum release age (minutes)
datamitsu config runtime | jq .minimumReleaseAgeMinutes                                   # -> 10080

# Confirm an env override took effect
DATAMITSU_MIN_RELEASE_AGE=20160 datamitsu config runtime | jq .minimumReleaseAgeMinutes   # -> 20160

# Verify the per-app install timeout (seconds; 0 = disabled)
DATAMITSU_INSTALL_TIMEOUT=1200 datamitsu config runtime | jq .installTimeoutSeconds       # -> 1200
```

The snapshot is introspection-only — it is **not** injected into the config JS VM. Wrapper config authors who need to branch on the effective minimum release age inside their `config.js` read the bounded, frozen `datamitsuConfigInputs` global instead:

```js
function getConfig(config) {
  // Only `minimumReleaseAgeMinutes` is exposed today, and the object is frozen.
  if (datamitsuConfigInputs.minimumReleaseAgeMinutes === 0) {
    // age filtering is disabled — branch accordingly
  }
  return config;
}
```

`datamitsuConfigInputs` is a deliberately minimal allowlist: it carries only the runtime values config evaluation is permitted to depend on (currently just `minimumReleaseAgeMinutes`). The full `config runtime` snapshot is **never** exposed to config JS — that boundary keeps hidden config inputs from leaking into fingerprinting and caching.

### Binary Apps: `devtools pull-github`

Binary apps are downloaded directly from GitHub releases. Use `pull-github` to fetch the latest release versions and compute hashes automatically.

**Apply updates:**

```bash
datamitsu devtools pull-github apps/githubApps.json --update
```

With `--update`, the command fetches the latest release tags, downloads binaries for all platform tuples (Darwin/Linux/Windows/FreeBSD/OpenBSD on amd64/arm64, Linux with glibc/musl), computes SHA-256 hashes, fetches the repository description from GitHub API, and writes the results back to the JSON file.

**Verify binary extraction after update:**

```bash
datamitsu devtools pull-github apps/githubApps.json --update --verify-extraction
```

The `--verify-extraction` flag additionally downloads each binary and verifies it can be extracted correctly. This catches issues like changed archive structures or renamed binaries inside archives.

:::note Releases with mixed asset types
Some tools publish VS Code extensions (`.vsix`), Linux packages (`.deb`, `.rpm`), NuGet packages (`.nupkg`), Python wheels (`.whl`), Windows installers (`.msi`), or macOS installer packages (`.pkg`) alongside binary archives in the same GitHub release. datamitsu automatically excludes these non-executable formats before scoring, so only actual binaries compete for selection. No configuration is needed — the filtering is automatic.
:::

### Node Apps (npm): `devtools pull-node`

Node apps are npm packages managed via pnpm. Use `pull-node` to check for updates on the npm registry.

**Preview available updates:**

```bash
datamitsu devtools pull-node apps/nodeApps.json --update --dry-run
```

The `--update --dry-run` combination fetches the latest versions and shows what would change without writing to the file.

**Apply updates:**

```bash
datamitsu devtools pull-node apps/nodeApps.json --update
```

This queries the npm registry for each configured package, compares versions, and updates the JSON file with new versions and descriptions.

**Regenerate lock files after updating:**

After updating node app versions, regenerate their lock files to ensure reproducible installs:

```bash
datamitsu config lockfile prettier
datamitsu config lockfile eslint
```

Each command outputs brotli-compressed lock file content (with a `br:` prefix) that you paste into the app's `lockFile` field in your configuration.

### UV Apps (Python): `devtools pull-uv`

UV apps are Python packages installed in isolated environments. Use `pull-uv` to check for updates on PyPI.

**Preview available updates:**

```bash
datamitsu devtools pull-uv apps/uvApps.json --update --dry-run
```

**Apply updates:**

```bash
datamitsu devtools pull-uv apps/uvApps.json --update
```

This queries PyPI for each configured package, compares versions, and updates the JSON file.

**Regenerate lock files after updating:**

```bash
datamitsu config lockfile yamllint
```

### Runtimes: `devtools pull-runtimes`

Runtimes (Node.js, UV/Python, JVM/Temurin) need periodic updates too. Use `pull-runtimes` to fetch the latest runtime versions.

**Update all runtimes:**

```bash
datamitsu devtools pull-runtimes runtimes/runtimes.json --update
```

The `--update` flag is required as a safety guard — the command refuses to run without it.

**Update a specific runtime only:**

```bash
datamitsu devtools pull-runtimes runtimes/runtimes.json --update --runtime node
datamitsu devtools pull-runtimes runtimes/runtimes.json --update --runtime uv
datamitsu devtools pull-runtimes runtimes/runtimes.json --update --runtime jvm
```

**Preview changes without writing:**

```bash
datamitsu devtools pull-runtimes runtimes/runtimes.json --update --dry-run
```

The command fetches versions from upstream sources:

- **Node.js**: latest LTS version from endoflife.date API
- **PNPM**: latest version from npm registry
- **Python**: latest stable (non-EOL) version from endoflife.date API
- **Java (Temurin)**: latest major version from Adoptium API

It then downloads runtime binaries for all platform tuples, computes SHA-256 hashes, and deduplicates musl entries that are identical to glibc.

#### Bumping the Node.js runtime

The Node.js runtime is a direct, hash-pinned archive (like the JVM runtime), so bumping it means refreshing the pinned version and the per-platform hashes in `runtimes.json`:

```bash
datamitsu devtools pull-runtimes runtimes/runtimes.json --update --runtime node
```

This resolves the latest Node.js LTS, builds the archive URLs for every os/arch/libc combination, and records a SHA-256 hash for each:

- **glibc, macOS, and Windows** hashes are taken from nodejs.org's clearsigned `SHASUMS256.txt.asc` and GPG-verified against the embedded official Node.js release public keys. An invalid or untrusted signature aborts the pull, giving a strong supply-chain anchor.
- **musl** hashes come from unofficial-builds.nodejs.org's unsigned `SHASUMS256.txt` (Node publishes no signature for musl builds) and are pinned in git — the same trust model as the official `node:alpine` image.

After a Node.js or pnpm version bump, regenerate the lock files for your node apps so they resolve against the new runtime, then commit the refreshed `runtimes.json`:

```bash
datamitsu config lockfile eslint
datamitsu config lockfile prettier
```

#### Bumping the Go runtime

```bash
datamitsu devtools pull-runtimes runtimes.json --update --runtime go
```

The Go SDK archives and their per-file SHA-256 come from go.dev (`https://go.dev/dl/?mode=json`): HTTPS plus published SHA-256, no GPG — the same trust posture as the musl Node path. The pull preserves (or sets) `go.goVersion` in the runtime entry.

## Generating a Docker image: `devtools dockerfile`

If you publish a container image of your wrapper with every tool pre-installed, `devtools dockerfile` generates an optimized multi-stage Dockerfile from your config's tool list — so you never hand-write or hand-tune it.

```bash
datamitsu devtools dockerfile -o docker/Dockerfile
```

**What it generates.** A config-free shared base, then a `config-split` stage, then one build stage per binary app, per managed runtime, and per runtime-managed app (each inheriting its runtime stage). The final stage assembles the populated datamitsu store with `COPY --link`, one layer per app, and carries the full config for the entrypoint.

**Two layers of cache isolation.** Each app is its own `COPY --link` layer, so bumping a single app re-pulls only that layer instead of the whole image (pull-time, for your users). The generator also isolates the **build**: the base never carries the config, and the `config-split` stage slices the config into one minimal per-stage file (via [`devtools split-config`](../reference/cli-commands.md#devtools-split-config)) that each stage loads on its own. So editing one app — or regenerating/reformatting the whole config — re-runs only the cheap split plus the stages whose slice actually changed, instead of reinstalling every tool from scratch. Changing a runtime rebuilds that runtime and every app under it; changing the base datamitsu image rebuilds everything.

**Base image and digest pinning.** The base image is `ghcr.io/datamitsu/datamitsu` at the version of the datamitsu binary you run the command with — _not_ your `package.json`. That tag is resolved to a SHA-256 digest and pinned as `FROM …@sha256:…` so builds are reproducible.

Pinning is best-effort and never fails the command. If the registry is unreachable, you pass `--offline`, or you build against a non-release (`dev`/unstable) datamitsu, the `FROM` line is left unpinned and a warning is written both into the generated file and to stderr:

```bash
datamitsu devtools dockerfile -o docker/Dockerfile --offline
```

:::note
The generated file is **fully overwritten** on every run — there are no managed regions. It is a generated artifact you own: hand-edit it freely, but re-running the command discards those edits. Commit the generated file and protect it with the drift check below.
:::

**Build-time verification (on by default).** Each app stage runs the app's version check right after installing it, so an app that doesn't actually run fails the Docker build instead of shipping a broken image. This is the safe default; pass `--no-verify` to turn it off (for example, to speed up emulated cross-arch builds):

```bash
datamitsu devtools dockerfile -o docker/Dockerfile --no-verify
```

**Alpine variant.** Pass `--alpine` to target the musl base image (`…:<version>-alpine`):

```bash
datamitsu devtools dockerfile -o docker/Dockerfile.alpine --alpine
```

**OCI labels.** Supply your image's labels with repeatable `--label` flags. Keep them in the generate invocation (e.g. a `Taskfile` task) so they survive regeneration:

```bash
datamitsu devtools dockerfile -o docker/Dockerfile \
  --label org.opencontainers.image.title=my-config \
  --label org.opencontainers.image.source=https://github.com/me/my-config
```

**Build args and environment variables.** Declare build-time `ARG`s with repeatable `--arg` flags and runtime `ENV` vars with `--env`. In the final stage all `ARG`s are emitted **before** all `ENV`s, so an `ENV` value can reference an `ARG`. An `--arg` may be a bare `NAME` (its default is supplied by `docker build --build-arg NAME=…`) or `NAME=default`; an `--env` value may contain `=` (only the first separates key from value). Like labels, keep them in the generate invocation so they survive regeneration:

```bash
datamitsu devtools dockerfile -o docker/Dockerfile \
  --arg BUILD_DATE \
  --arg TZ=UTC \
  --env LANG=C.UTF-8
```

This emits, in the final stage:

```dockerfile
ARG BUILD_DATE
ARG TZ="UTC"
ENV LANG="C.UTF-8"
```

**Build-time variables (`--build-arg`).** `--arg`/`--env` only affect the **final** image — they are runtime knobs and do nothing for the build itself. To influence the install stages (for example raise the install timeout for heavy tools), use `--build-arg`. Each one is declared as an `ARG` (overridable via `docker build --build-arg`) and promoted to an `ENV` in a dedicated `dm-build` stage that every install stage derives from, so `datamitsu install` sees it. The final image is built `FROM dm-base` (not `dm-build`), so build args **never leak into the shipped image**:

```bash
datamitsu devtools dockerfile -o docker/Dockerfile \
  --build-arg DATAMITSU_INSTALL_TIMEOUT=1200 \
  --build-arg HTTP_PROXY
```

emits:

```dockerfile
FROM dm-base AS dm-build
ARG DATAMITSU_INSTALL_TIMEOUT="1200"
ENV DATAMITSU_INSTALL_TIMEOUT=$DATAMITSU_INSTALL_TIMEOUT
ARG HTTP_PROXY
ENV HTTP_PROXY=$HTTP_PROXY

FROM dm-build AS rt-go          # install inherits the ENV
RUN datamitsu install --runtime go
```

A bare `--build-arg NAME` (no default) takes its value from `docker build --build-arg NAME=…`. With no `--build-arg` flags the `dm-build` stage is omitted and install stages derive straight from `dm-base`.

:::warning Secrets
Do not pass tokens or other secrets via `--build-arg`: an `ARG` value lingers in the intermediate build layers (visible in the build cache). For credentials use `RUN --mount=type=secret` instead.
:::

**libc filtering and `--force-include`.** The image targets one libc — **musl** when you pass `--alpine`, **glibc** otherwise. Binary apps are filtered to those that ship a binary for that libc on every architecture they declare; a glibc-only binary cannot execute on a musl image, so such apps are dropped from the generated Dockerfile rather than failing the build. Each generation prints the dropped apps:

```text
Warning: excluded 47 app(s) with no musl binary (add via --force-include if universal): actionlint, age, … swag, …
```

Runtime-managed apps (node/uv/jvm/go) are never filtered — their runtime provides the libc.

Many dropped tools are actually **universal**: a statically-linked Go binary recorded as `glibc` (or a static-musl Rust binary recorded as `musl`) runs fine on the other libc, but the registry under-declares it. Triage the warning list and add the genuinely-universal ones back with `--force-include` (keep it in the generate invocation so it survives regeneration):

```bash
datamitsu devtools dockerfile -o docker/Dockerfile.alpine --alpine \
  --force-include jq,shfmt,golangci-lint,hadolint
```

A truly libc-specific tool (e.g. a dynamically-linked glibc binary like `swag`) should stay excluded — install it another way on that variant, or fix its registry entry if a musl build does exist.

**Pulling from a mirror.** To resolve the base-image digest from a registry other than `ghcr.io` (for example a pull-through cache), set `DATAMITSU_OCI_REGISTRY`:

```bash
DATAMITSU_OCI_REGISTRY=mirror.internal datamitsu devtools dockerfile -o docker/Dockerfile
```

### Keeping the generated Dockerfile fresh in CI

Because the output is fully generated, add a drift check that fails when the committed Dockerfile is stale. Run the generator with `--offline` so a re-pushed upstream tag (a moving digest) cannot cause false failures — the check then compares structure and version, not the live digest:

```yaml
- name: Check Dockerfiles are up to date
  run: |
    datamitsu devtools dockerfile -o docker/Dockerfile --offline
    datamitsu devtools dockerfile -o docker/Dockerfile.alpine --alpine --offline
    git diff --exit-code -- docker/Dockerfile docker/Dockerfile.alpine \
      || { echo "Dockerfiles are stale; run the generator and commit."; exit 1; }
```

In your publish workflow, generate with pinning enabled (drop `--offline`) so the pushed image's `FROM` is digest-pinned.

## Testing After Updates

### Verify cross-platform integrity

After updating any tool or runtime versions, run `verify-all` to check integrity across all platforms:

```bash
datamitsu devtools verify-all
```

This command:

1. Downloads and hash-verifies all binary apps for every configured platform
2. Downloads and hash-verifies all managed runtime binaries
3. Installs runtime-managed apps (node, UV, JVM) on the current platform
4. Runs version checks to confirm tools execute correctly

**Useful flags:**

```bash
# Skip version checks (faster, hash-only verification)
datamitsu devtools verify-all --no-version-check

# Increase download concurrency
datamitsu devtools verify-all --concurrency 8

# Skip entries that passed on the last run with unchanged config
datamitsu devtools verify-all --skip-passed

# Machine-readable output for CI pipelines
datamitsu devtools verify-all --json

# Skip remote config resolution
datamitsu devtools verify-all --no-remote
```

Results are cached incrementally in a state file. Using `--skip-passed` skips entries whose configuration fingerprint hasn't changed since the last successful verification, which speeds up repeated runs during development.

### Local smoke test

Always test locally before publishing:

```bash
# Re-download everything
datamitsu init

# Run the full check pipeline
datamitsu check

# Verify specific tools
datamitsu exec prettier -- --version
datamitsu exec eslint -- --version
```

## Automation

### GitHub Actions: periodic version checks

Set up automated version checking with a scheduled GitHub Actions workflow:

```yaml
name: Check for tool updates

on:
  schedule:
    - cron: "0 9 * * 1" # Every Monday at 9:00 UTC
  workflow_dispatch: # Allow manual triggers

jobs:
  check-updates:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup datamitsu
        run: |
          # Install datamitsu (adjust for your setup)
          go build -o datamitsu .

      # Note: pull-github does not support --dry-run.
      # Use pull-github --update in the automated update PR workflow instead.

      - name: Check node app updates
        run: ./datamitsu devtools pull-node apps/nodeApps.json --update --dry-run

      - name: Check UV app updates
        run: ./datamitsu devtools pull-uv apps/uvApps.json --update --dry-run

      - name: Check runtime updates
        run: ./datamitsu devtools pull-runtimes runtimes/runtimes.json --update --dry-run
```

### GitHub Actions: automated update PR

For a more hands-off workflow, create a workflow that applies updates and opens a pull request:

```yaml
name: Update tool versions

on:
  schedule:
    - cron: "0 9 1 * *" # First day of each month
  workflow_dispatch:

jobs:
  update:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup datamitsu
        run: go build -o datamitsu .

      - name: Update binary apps
        run: ./datamitsu devtools pull-github apps/githubApps.json --update

      - name: Update node apps
        run: ./datamitsu devtools pull-node apps/nodeApps.json --update

      - name: Update UV apps
        run: ./datamitsu devtools pull-uv apps/uvApps.json --update

      - name: Update runtimes
        run: ./datamitsu devtools pull-runtimes runtimes/runtimes.json --update

      - name: Verify all platforms
        run: ./datamitsu devtools verify-all

      - name: Create pull request
        uses: peter-evans/create-pull-request@v6
        with:
          title: "chore: update tool versions"
          body: "Automated tool version update. Review changes and verify locally before merging."
          branch: chore/update-tool-versions
          commit-message: "chore: update tool versions"
```

## Best Practices

### Semantic versioning

Follow semantic versioning for your wrapper package releases:

- **Patch** (1.0.x): tool version bumps with no configuration changes
- **Minor** (1.x.0): new tools added, new features in configuration
- **Major** (x.0.0): tools removed, breaking configuration changes, minimum version bumps

### Changelog

Keep a changelog documenting what changed in each release:

- Which tools were updated and to what version
- Any new tools added or removed
- Configuration changes that users need to be aware of
- Minimum datamitsu version changes (`getMinVersion()`)

### Update workflow

A typical update cycle looks like this:

1. Run `devtools pull-*` commands to detect and apply updates
2. Regenerate lock files for any updated node/UV apps
3. Run `devtools verify-all` to check cross-platform integrity
4. Run `datamitsu init && datamitsu check` locally
5. Commit, push, and create a release

### Migration guides

When making breaking changes (removing tools, changing configuration structure), provide a migration guide in your release notes explaining:

- What changed and why
- Step-by-step instructions to update
- The new minimum datamitsu version if changed

### Configuring pnpm Settings for Node Apps

Node apps run `pnpm install` in an isolated directory managed by datamitsu. datamitsu always writes a `pnpm-workspace.yaml` containing secure supply chain defaults before `pnpm install` runs. You can override or extend those defaults by supplying your own `pnpm-workspace.yaml` via `App.files` — datamitsu shallow-merges your keys on top of the baseline.

See the [Supply Chain Security](../guides/supply-chain-security.md) guide for the full list of baseline settings and what each one does.

:::caution Files are written only on initial install
`App.files` (including `pnpm-workspace.yaml`) are read on every install, but the install directory is only rebuilt when the app's config hash changes. If you change `files` after an app is already cached, run `datamitsu store clear` (or delete the app's subdirectory under `.apps/node/`) to force a reinstall.
:::

#### Apps without build scripts (zero config)

No `App.files["pnpm-workspace.yaml"]` is required. datamitsu writes the secure defaults automatically:

```js
const eslint = {
  node: {
    packageName: "eslint",
    binPath: "node_modules/.bin/eslint",
    version: "9.17.0",
    lockFile: "br:...",
  },
};
```

#### Apps that need build scripts (puppeteer, sharp, esbuild, etc.)

pnpm 11 blocks lifecycle scripts by default. If install fails with `ERR_PNPM_IGNORED_BUILDS`, allowlist the package via `allowBuilds`:

```js
const mmdc = {
  files: {
    "pnpm-workspace.yaml": YAML.stringify({
      allowBuilds: { puppeteer: true },
    }),
  },
  node: {
    packageName: "@mermaid-js/mermaid-cli",
    binPath: "node_modules/.bin/mmdc",
    version: "11.15.0",
    lockFile: "br:...",
  },
};
```

The merged result written to the app environment keeps all secure defaults (`strictDepBuilds: true`, `minimumReleaseAge: 10080`, etc.) **plus** the user's `allowBuilds` entry. Only the keys you explicitly set are overridden.

After adding `allowBuilds`, regenerate the lock file so pnpm can record the approved builds:

```bash
pnpm exec datamitsu config lockfile mmdc
```

#### Overriding a security key

User intent wins on every key. Setting `strictDepBuilds: false` disables strict build approval — use only when you understand the risk:

```js
files: {
  "pnpm-workspace.yaml": YAML.stringify({
    strictDepBuilds: false,
  }),
}
```

:::note Node and UV apps
For node apps, any `package.json` included in `App.files` will be overwritten by the datamitsu core when it writes the managed `package.json`. Use `pnpm-workspace.yaml` for customization — the core merges that file with secure defaults rather than overwriting it.

For UV apps, project-level settings are configured via `pyproject.toml`. However, any `pyproject.toml` included in `App.files` will be overwritten by the datamitsu core when it writes the managed `pyproject.toml`. This customization path is not available for UV apps.
:::
