---
title: Introduction
slug: /intro
description: datamitsu - a platform for building reproducible, security-first development tool distributions
---

# datamitsu

**Your toolchain deserves a home.**

datamitsu is a platform for building reproducible, security-first development tool distributions. It manages binaries, runtimes, and configuration files so teams can standardize their tooling across all projects with a single package.

For a comprehensive overview of why datamitsu exists and what makes it unique, see [About datamitsu](./about.md).

:::caution[Alpha Software]
datamitsu is in alpha. The configuration API is not yet stabilized and may change between versions.
:::

## Key Features

- **Security-first binary management** — SHA-256 hash verification for every binary
- **Programmable JavaScript configuration** — Full programmatic control via the goja runtime
- **Config chaining and inheritance** — Layer configs from base → company → team → project
- **Multi-runtime support** — Python (UV), Node.js (FNM/PNPM), JVM, and native binaries
- **Monorepo-aware** — Per-project isolation for caches and tool execution
- **Docker-optimized** — Pre-cache all tools in a Docker layer for fast CI builds

## Quick Example

Create a `datamitsu.config.ts` at your git root:

```typescript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(prev) {
  return {
    ...prev,
    apps: {
      ...prev.apps,
      "golangci-lint": {
        binary: {
          binaries: {
            linux: {
              amd64: {
                glibc: {
                  url: "https://github.com/golangci/golangci-lint/releases/download/v2.1.0/golangci-lint-2.1.0-linux-amd64.tar.gz",
                  hash: "<sha256-hash>",
                  contentType: "tar.gz",
                  binaryPath: "golangci-lint-2.1.0-linux-amd64/golangci-lint",
                },
              },
            },
          },
        },
      },
    },
  };
}

globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

Then initialize and run:

```bash
# Download all configured tools
datamitsu init

# Run lint checks
datamitsu check
```

## How It Works

datamitsu uses a [two-level architecture](./about.md#the-two-level-architecture): the core binary provides secure tool delivery and a config engine, while wrapper packages (npm/gem/pypi) provide concrete tool versions and opinionated defaults. Teams install one package and get everything configured.

## Next Steps

- [Installation](./getting-started/installation.md) — Install datamitsu on your system
- [Quick Start](./getting-started/quick-start.md) — Get up and running in minutes
- [Core Concepts](./getting-started/core-concepts.md) — Understand how datamitsu works

---

:::tip[Want to Learn More?]

- **[About datamitsu](./about.md)** — Understand why datamitsu exists and what makes it unique
- **[Comparison Guide](./reference/comparison.md)** — See how datamitsu compares to mise, moon, Nx, and other tools
- **[Using Wrappers](./guides/using-wrappers.md)** — Learn how to consume config distribution packages
- **[Contributing](./contributing/index.md)** — Help improve datamitsu or build your own wrapper package

:::
