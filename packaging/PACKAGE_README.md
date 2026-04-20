# datamitsu

<p align="center">
  <img src="https://datamitsu.com/img/logo.png" alt="datamitsu" width="400" />
</p>

<p align="center">
  <a href="https://github.com/datamitsu/datamitsu/actions/workflows/pr-checks.yml"><img src="https://github.com/datamitsu/datamitsu/actions/workflows/pr-checks.yml/badge.svg" alt="build"></a>
  <a href="https://goreportcard.com/report/github.com/datamitsu/datamitsu"><img src="https://goreportcard.com/badge/github.com/datamitsu/datamitsu?v=2" alt="Go Report Card"></a>
  <img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT">
</p>

> Your toolchain deserves a home.

> **Alpha**: This project is in alpha. The configuration API is not yet stabilized and may change between versions.

A configuration management and binary distribution tool for development tools. It downloads, verifies (SHA-256), and manages binaries and runtime-managed tools across platforms using JavaScript-powered configuration.

- **Secure.** Mandatory SHA-256 verification for all downloads. No exceptions.
- **Isolated.** Per-app environments with content-addressable storage.
- **Reproducible.** Cached binaries, lock files, and platform-specific builds.

📖 [Documentation](https://datamitsu.com)

## Install

With **npm**:

```bash
npm install @datamitsu/datamitsu
```

With **Go** (>= 1.23):

```bash
go install github.com/datamitsu/datamitsu@latest
```

**[Installation guide](https://datamitsu.com/docs/getting-started/installation)** with more installation options.

## Usage

Initialize your toolchain and run checks:

#### TL;DR

```bash
# Initialize tools (downloads binaries, creates .datamitsu/ configs)
npx datamitsu init

# Run fix then lint
npx datamitsu check

# Or run separately
npx datamitsu fix
npx datamitsu lint

# Execute a managed binary
npx datamitsu exec shellcheck script.sh
```

#### More details

- [**Quick Start Guide**](https://datamitsu.com/docs/getting-started/quick-start)
- [**CLI Commands**](https://datamitsu.com/docs/reference/cli-commands)
- [**Configuration API**](https://datamitsu.com/docs/reference/configuration-api)

## Why datamitsu

- ### **Security-first binary management**

All binaries require SHA-256 verification. No hash, no download. [docs](https://datamitsu.com/docs/guides/architecture)

```javascript
export function getConfig(prev) {
  return {
    ...prev,
    apps: {
      hadolint: {
        binaries: {
          linux: {
            amd64: {
              glibc: {
                url: "https://github.com/hadolint/hadolint/releases/download/v2.14.0/hadolint-linux-x86_64",
                hash: "6bf226944684f56c84dd014e8b979d27425c0148f61b3bd99bcc6f39e9dc5a47",
                contentType: "binary",
              },
            },
          },
        },
      },
    },
  };
}
```

- ### **Isolated environments per app**

Each app gets its own environment with content-addressable storage. Multiple versions coexist. [docs](https://datamitsu.com/docs/guides/runtime-management)

```javascript
export function getConfig(prev) {
  return {
    ...prev,
    apps: {
      eslint: {
        fnm: {
          packageName: "eslint",
          version: "10.0.0",
        },
        // Cached at: .apps/fnm/eslint/{hash}/node_modules
      },
      "eslint-legacy": {
        fnm: {
          packageName: "eslint",
          version: "9.17.0",
        },
        // Cached at: .apps/fnm/eslint-legacy/{hash}/node_modules
      },
    },
  };
}
```

- ### **Managed configuration distribution**

Distribute configs from runtime-managed apps via symlinks. [docs](https://datamitsu.com/docs/guides/managed-configs)

```javascript
export function getConfig(prev) {
  return {
    ...prev,
    apps: {
      "my-eslint-config": {
        fnm: {
          packageName: "@myorg/eslint-config",
          version: "2.0.0",
        },
        links: {
          "eslint-config": "dist/eslint.config.js",
        },
      },
    },
  };
}
```

Creates `.datamitsu/` with symlinks:

```
.datamitsu/
└── eslint-config → ../.apps/fnm/my-eslint-config/{hash}/dist/eslint.config.js
```

- ### **Layered configuration**

Chain configs with inheritance and remote config support. [docs](https://datamitsu.com/docs/guides/configuration-layers)

```javascript
// datamitsu.config.ts
export function getRemoteConfigs() {
  return [
    {
      url: "https://example.com/base-config.ts",
      hash: "sha256:abc123...",
    },
  ];
}

export function getConfig(prev) {
  return {
    ...prev,
    apps: {
      ...prev.apps,
      // Override or add apps here
    },
  };
}
```

- ### **JavaScript configuration engine**

Full goja runtime with built-in APIs for path manipulation, formatting (YAML/TOML/INI), and hashing. [docs](https://datamitsu.com/docs/reference/configuration-api)

```javascript
export function getConfig(prev) {
  return {
    ...prev,
    init: {
      "lefthook.yaml": {
        content: (context) => {
          const existing = YAML.parse(context.existingContent || "");
          return YAML.stringify({
            ...existing,
            "pre-commit": {
              commands: {
                "datamitsu-fix": { run: "datamitsu fix" },
              },
            },
          });
        },
      },
    },
  };
}
```

- ### **Programmatic API**

If you need to integrate datamitsu into your build scripts or tools, a type-safe JavaScript API is available. [docs](https://datamitsu.com/docs/reference/js-api)

```javascript
import { fix, lint } from "@datamitsu/datamitsu";

await fix({ files: ["src/generated.ts"] });
const result = await lint({ explain: "json" });
```

---

## Documentation

Full documentation is available at [https://datamitsu.com](https://datamitsu.com)

**Getting Started:**

- [Installation](https://datamitsu.com/docs/getting-started/installation)
- [Quick Start Guide](https://datamitsu.com/docs/getting-started/quick-start)
- [About datamitsu](https://datamitsu.com/docs/about) — Why datamitsu exists

**Guides:**

- [Runtime Management](https://datamitsu.com/docs/guides/runtime-management)
- [Managed Configs](https://datamitsu.com/docs/guides/managed-configs)
- [Configuration Layers](https://datamitsu.com/docs/guides/configuration-layers)
- [Architecture](https://datamitsu.com/docs/guides/architecture)

**Reference:**

- [CLI Commands](https://datamitsu.com/docs/reference/cli-commands)
- [Configuration API](https://datamitsu.com/docs/reference/configuration-api)
- [JavaScript API](https://datamitsu.com/docs/reference/js-api)
- [Comparison with mise/moon/Nx](https://datamitsu.com/docs/reference/comparison)

## Support datamitsu

❤️ [Sponsor datamitsu](https://datamitsu.com/sponsor)

## License

MIT
