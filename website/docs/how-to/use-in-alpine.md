---
title: Use in Alpine Linux
description: How datamitsu handles Alpine Linux and musl libc systems with automatic detection and binary selection
---

# Use in Alpine Linux

Alpine Linux uses musl libc instead of the more common glibc. Datamitsu automatically detects musl and selects the correct binary variant when available.

## How It Works

### Automatic Libc Detection

When datamitsu starts, it detects the host libc using a multi-stage process:

1. **ldd --version** — parses output for "musl libc" or "GNU libc"
2. **ELF interpreter** — reads the PT_INTERP header from the current binary for `ld-musl` or `ld-linux`
3. **Loader paths** — checks for musl/glibc loader files on disk

The first successful detection wins. If all stages fail, libc is reported as `unknown`.

On non-Linux systems (macOS, Windows, FreeBSD), libc detection is skipped and always returns `unknown`.

The detected libc is available in JavaScript configs via `facts().libc`:

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  const libc = facts().libc; // "glibc", "musl", or "unknown"
  // ...
}
globalThis.getConfig = getConfig;
```

### Target-Based Binary Resolution

Datamitsu uses a three-dimensional target model for binary selection: **OS**, **Arch**, and **Libc**.

Each target is represented as a tuple like `linux/amd64/musl` or `darwin/arm64/unknown`.

When selecting a binary for a tool, the resolver scores all available candidates:

| Signal        | Weight | Requirement           |
| ------------- | ------ | --------------------- |
| OS match      | +1000  | Required (0 if wrong) |
| Arch match    | +100   | Required (0 if wrong) |
| Libc exact    | +10    | Preferred             |
| Libc unknown  | +5     | Neutral               |
| Libc mismatch | +1     | May work via compat   |

The highest-scoring candidate is selected. Ties are broken alphabetically for determinism.

### Fallback Behavior

When a musl-specific binary is not available but a glibc variant exists, datamitsu falls back to the glibc binary and emits a warning:

```
WARN: typst: musl binary not found, falling back to glibc variant
```

This fallback may or may not work depending on the specific binary. Statically linked glibc binaries often work on musl, but dynamically linked ones typically fail with errors like `fcntl64: symbol not found`.

To avoid fallback issues, prefer tools that provide musl-specific builds or statically linked binaries.

### Nested Storage Format

Binary definitions use a three-level nested structure: `os → arch → libc → BinaryInfo`.

```javascript
binaries: {
  linux: {
    amd64: {
      glibc: {
        url: "https://example.com/tool_linux_amd64.tar.gz",
        hash: "abc123...",
        contentType: "tar.gz",
      },
      musl: {
        url: "https://example.com/tool_linux_amd64_musl.tar.gz",
        hash: "def456...",
        contentType: "tar.gz",
      },
    },
  },
  darwin: {
    arm64: {
      unknown: {
        url: "https://example.com/tool_darwin_arm64.tar.gz",
        hash: "789abc...",
        contentType: "tar.gz",
      },
    },
  },
}
```

Key rules:

- The libc level is always present, even for non-Linux platforms
- Non-Linux platforms use `unknown` as the libc key
- Linux platforms typically have `glibc` and optionally `musl` variants
- All config sources (JSON and inline TypeScript) use the three-level format directly

### Cache Isolation

Cache paths include the resolved target, so glibc and musl binaries are cached separately. A tool resolved as `linux/amd64/musl` gets a different cache path than the same tool resolved as `linux/amd64/glibc`, even on the same machine.

This prevents cache conflicts when switching between container environments or testing with different libc variants.

## Managed Runtimes (Node, UV, JVM)

datamitsu applies two distinct musl-specific mechanisms to managed runtimes:

- **The Node.js archive that datamitsu downloads** — the registry pins musl-linked Node.js archives directly, selected automatically on a musl host (see [Node.js (musl): static archive entries](#nodejs-musl-static-archive-entries)).
- **A runtime's own binary** (`node`, `uv`, `java`) — when the managed config has no musl variant for that executable, datamitsu falls back to the system binary on PATH (see [Automatic Fallback](#automatic-fallback)).

### Node.js (musl): static archive entries

datamitsu acquires Node.js as a direct, hash-pinned archive — it downloads the archive named in the registry, verifies its SHA-256 hash, and extracts it itself (exactly like the JVM runtime downloads a JDK). There is no version-manager binary to shell out to, no `node install` step, and no dynamic mirror logic.

The registry (`runtimes.json`) carries explicit musl entries for Node.js. On Linux they are the `node-v<ver>-linux-{x64,arm64}-musl.tar.xz` archives published by [unofficial-builds.nodejs.org](https://unofficial-builds.nodejs.org/download/release); glibc, macOS, and Windows entries come from `nodejs.org/dist`. Every entry — musl included — is pinned with a SHA-256 hash that datamitsu verifies before extraction.

On a musl host datamitsu selects the `musl` libc entry automatically via libc detection. There is **nothing to configure and no environment variables to set** — musl support works out of the box in managed mode.

**Supported musl architectures:** `amd64` (`x64`) and `arm64` — the only architectures unofficial-builds publishes. Other architectures (arm/armv7l, 386, ppc64le, …) have no musl entry; on those datamitsu falls back to a system `node` on PATH if one is present.

**Integrity does not depend on the mirror.** The hashes are pinned in git and refreshed by maintainers via `devtools pull-runtimes --runtime node`, so an archive is rejected unless it matches the recorded SHA-256 — the mirror being trustworthy at download time is not required.

#### libstdc++ is required on Alpine

musl Node.js from the unofficial mirror is **dynamically linked** against `libstdc++` (and `libgcc`), which the base `alpine` image does not include. Without it `node` fails to start. Install it:

```sh
apk add --no-cache libstdc++
```

`libgcc` is pulled in transitively, so no separate entry is needed. datamitsu's own `*-alpine` Docker image already bundles `libstdc++`, so wrappers building `FROM ghcr.io/datamitsu/datamitsu:<version>-alpine` inherit it with no Dockerfile change.

#### No configuration needed for musl

Musl support requires no setup: the registry already pins the musl archives, and datamitsu selects them automatically on a musl host. There are no mirror or arch environment variables to set.

### Automatic Fallback

datamitsu automatically handles this. When running on a musl system with managed runtimes that only have glibc binaries, datamitsu checks if the corresponding system binary is available on PATH. If found, it automatically switches to system mode instead of downloading an incompatible glibc binary.

The system binaries checked for each runtime:

| Runtime | System binary looked up | Alpine package                          |
| ------- | ----------------------- | --------------------------------------- |
| Node    | `node`                  | Managed musl archive — no apk needed    |
| UV      | `uv`                    | Not in default repos (install manually) |
| JVM     | `java`                  | `openjdk17`                             |

:::note
Node runs in managed mode on Alpine via the static musl archive (the registry pins musl-linked Node.js), so no system fallback or apk package is needed. UV is not available in Alpine's default package repositories — for UV on Alpine, use the [manual system mode override](#manual-system-mode-override) to configure system mode explicitly, or install the `uv` binary from its upstream release.
:::

When a system binary is found, you will see a log message like:

```
INFO: automatic fallback to system mode  runtime=jvm  reason="musl binary unavailable"  system_command=/usr/bin/java
```

If the system binary is not found, datamitsu falls through to the existing glibc fallback behavior (which may fail at runtime with errors like `fcntl64: symbol not found`).

### Manual System Mode Override

If you prefer explicit control, you can still configure runtimes in system mode manually:

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  return {
    ...config,
    runtimes: {
      ...config.runtimes,
      node: {
        ...config.runtimes.node,
        mode: "system",
      },
      uv: {
        ...config.runtimes.uv,
        mode: "system",
      },
      jvm: {
        ...config.runtimes.jvm,
        mode: "system",
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

Manual system mode configuration takes precedence over automatic fallback since the runtime is already in system mode.

## Dockerfile Example

A typical Alpine-based CI Dockerfile using manual system mode for UV:

```dockerfile
FROM alpine:3.20

RUN apk add --no-cache git go nodejs npm python3 openjdk17

# Build datamitsu
COPY . /workspace
WORKDIR /workspace
RUN go build -o /usr/local/bin/datamitsu

# Initialize - binary apps auto-detect musl, Node uses its managed musl archive,
# JVM auto-fallbacks to system mode
# UV requires manual system mode config (see manual override section above)
RUN datamitsu init
```

Binary apps automatically detect musl and select the appropriate variant. Node runs in managed mode via its static musl archive. For JVM, datamitsu auto-detects the system `java` binary. For UV, configure system mode in your wrapper config (see [manual system mode override](#manual-system-mode-override) above).

## Checking Detection Results

Use the `facts().libc` value in your config to verify detection:

```javascript
// In datamitsu.config.ts
console.log("Detected libc:", facts().libc);
```

The `devtools apps list` command shows which binary variant was resolved for each app on the current system.

## Troubleshooting

### `fcntl64: symbol not found` or similar errors

This means a glibc binary is running on a musl system. For binary apps, check if a musl variant is available for the tool and add it to the config. For managed runtimes, the Node runtime uses its pinned musl archive automatically, and the JVM runtime auto-falls back to system `java` if installed (`apk add openjdk17`). For UV, configure system mode manually in your wrapper config since the `uv` binary is not available in Alpine's default repositories. If no musl variant or system binary exists, the tool cannot run on Alpine without a glibc compatibility layer.

### Detection returns `unknown`

If libc detection returns `unknown`, datamitsu cannot determine the libc and will use neutral scoring. This can happen in minimal containers without `ldd` or with statically linked init binaries. The resolver will still select the best available candidate but cannot prefer musl-specific builds.
