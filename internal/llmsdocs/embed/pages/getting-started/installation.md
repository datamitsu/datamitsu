# Installation

> How to install datamitsu on your system

## Prerequisites

- **Git** — datamitsu uses your git root to locate configuration files and manage project-scoped caches
- **Platform support**: Linux (amd64, arm64), macOS (amd64, arm64), Windows (amd64, arm64). Windows requires Developer Mode for symlinks.

## Install Methods

- [Homebrew](./homebrew.md) — macOS and Linux
- [Winget](./winget.md) — Windows
- [Scoop](./scoop.md) — Windows
- [npm](./npm.md) — any platform with Node.js
- [PyPI](./pypi.md) — any platform with Python
- [RubyGems](./rubygems.md) — any platform with Ruby
- [Docker](./docker.md) — official images on Docker Hub and GHCR (Debian and Alpine variants)
- [GitHub Releases](./github-releases.md) — direct binary downloads, deb/rpm/apk packages, checksum + cosign verification
- [Build from Source](./source.md) — requires Go 1.25.2+

## CI

- [GitHub Actions](../../how-to/use-in-github-actions.md) — the official `setup-datamitsu` action installs the CLI, provisions tools, and runs your checks in a single step

## Editor Integrations

- [VS Code Extension](./vscode.md) — Datamitsu Toolkit for VS Code, Cursor,
  Windsurf, VSCodium, and other VS Code-based editors: format on save with your
  datamitsu config. Works standalone too — if no `datamitsu` is on `PATH`, it
  downloads a pinned, SHA-256-verified binary.

## Verify Installation

Check that datamitsu is available:

```bash
datamitsu --help
```

You should see the available commands listed, including `exec`, `init`, `check`, `setup`, and others.

## Global Cache Directory

datamitsu stores data under `~/.cache/datamitsu/` (or `$XDG_CACHE_HOME/datamitsu/`), split into two subdirectories:

- **`store/`** — Downloaded binaries (`.bin/`), runtime binaries (`.runtimes/`), runtime-managed app environments (`.apps/`), remote configs (`.remote-configs/`)
- **`cache/`** — Per-project tool caches (`projects/`), verify state (`.verify-state/`)

You can view the cache path with:

```bash
datamitsu cache path
```

## Next Steps

- [Quick Start](../quick-start.md) — Create your first configuration and run tools
- [Core Concepts](../core-concepts.md) — Understand how datamitsu manages tools
