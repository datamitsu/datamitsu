---
title: Installation
description: How to install datamitsu on your system
---

# Installation

## Prerequisites

- **Go 1.25.2+** — Required to build from source
- **Git** — datamitsu uses your git root to locate configuration files and manage project-scoped caches
- **Platform support**: Linux (amd64, arm64), macOS (amd64, arm64). Windows support is available but requires Developer Mode for symlinks.

## Build from Source

Clone the repository and build:

```bash
git clone https://github.com/datamitsu/datamitsu.git
cd datamitsu
go build
```

This produces a `datamitsu` binary in the current directory. Move it to a directory in your `PATH`:

```bash
mv datamitsu /usr/local/bin/
```

### Using pnpm

If you have pnpm installed, you can also build via:

```bash
pnpm build
```

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

- [Quick Start](./quick-start.md) — Create your first configuration and run tools
- [Core Concepts](./core-concepts.md) — Understand how datamitsu manages tools
