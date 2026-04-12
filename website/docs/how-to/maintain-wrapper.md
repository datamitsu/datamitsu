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

## Updating Tool Versions

### Binary Apps: `devtools pull-github`

Binary apps are downloaded directly from GitHub releases. Use `pull-github` to fetch the latest release versions and compute hashes automatically.

**Apply updates:**

```bash
datamitsu devtools pull-github apps/githubApps.json --update
```

With `--update`, the command fetches the latest release tags, downloads binaries for all platform tuples (Darwin/Linux/Windows/FreeBSD/OpenBSD on amd64/arm64, Linux with glibc/musl), computes SHA-256 hashes, and writes the results back to the JSON file.

**Verify binary extraction after update:**

```bash
datamitsu devtools pull-github apps/githubApps.json --update --verify-extraction
```

The `--verify-extraction` flag additionally downloads each binary and verifies it can be extracted correctly. This catches issues like changed archive structures or renamed binaries inside archives.

### FNM Apps (npm): `devtools pull-fnm`

FNM apps are Node.js packages managed via pnpm. Use `pull-fnm` to check for updates on the npm registry.

**Preview available updates:**

```bash
datamitsu devtools pull-fnm apps/fnmApps.json --update --dry-run
```

The `--update --dry-run` combination fetches the latest versions and shows what would change without writing to the file.

**Apply updates:**

```bash
datamitsu devtools pull-fnm apps/fnmApps.json --update
```

This queries the npm registry for each configured package, compares versions, and updates the JSON file with new versions and descriptions.

**Regenerate lock files after updating:**

After updating FNM app versions, regenerate their lock files to ensure reproducible installs:

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

Runtimes (FNM/Node.js, UV/Python, JVM/Temurin) need periodic updates too. Use `pull-runtimes` to fetch the latest runtime versions.

**Update all runtimes:**

```bash
datamitsu devtools pull-runtimes runtimes/runtimes.json --update
```

The `--update` flag is required as a safety guard — the command refuses to run without it.

**Update a specific runtime only:**

```bash
datamitsu devtools pull-runtimes runtimes/runtimes.json --update --runtime fnm
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

## Testing After Updates

### Verify cross-platform integrity

After updating any tool or runtime versions, run `verify-all` to check integrity across all platforms:

```bash
datamitsu devtools verify-all
```

This command:

1. Downloads and hash-verifies all binary apps for every configured platform
2. Downloads and hash-verifies all managed runtime binaries
3. Installs runtime-managed apps (FNM, UV, JVM) on the current platform
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

      - name: Check FNM app updates
        run: ./datamitsu devtools pull-fnm apps/fnmApps.json --update --dry-run

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

      - name: Update FNM apps
        run: ./datamitsu devtools pull-fnm apps/fnmApps.json --update

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
2. Regenerate lock files for any updated FNM/UV apps
3. Run `devtools verify-all` to check cross-platform integrity
4. Run `datamitsu init && datamitsu check` locally
5. Commit, push, and create a release

### Migration guides

When making breaking changes (removing tools, changing configuration structure), provide a migration guide in your release notes explaining:

- What changed and why
- Step-by-step instructions to update
- The new minimum datamitsu version if changed
