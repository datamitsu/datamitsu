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

## Managed Runtimes (FNM, UV, JVM)

Managed runtimes do not have musl-native binaries available. Upstream projects (Node.js via FNM, Python via UV, JDK via Temurin) do not provide musl-native binaries through the channels datamitsu uses.

### Automatic Fallback

datamitsu automatically handles this. When running on a musl system with managed runtimes that only have glibc binaries, datamitsu checks if the corresponding system binary is available on PATH. If found, it automatically switches to system mode instead of downloading an incompatible glibc binary.

The system binaries checked for each runtime:

| Runtime | System binary looked up | Alpine package                          |
| ------- | ----------------------- | --------------------------------------- |
| FNM     | `fnm`                   | Not in default repos (install manually) |
| UV      | `uv`                    | Not in default repos (install manually) |
| JVM     | `java`                  | `openjdk17`                             |

:::note
FNM and UV are not available in Alpine's default package repositories. For these runtimes on Alpine, use the [manual system mode override](#manual-system-mode-override) to configure system mode explicitly, or install the runtime binary from its upstream release.
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
      fnm: {
        ...config.runtimes.fnm,
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

A typical Alpine-based CI Dockerfile using manual system mode for FNM:

```dockerfile
FROM alpine:3.20

RUN apk add --no-cache git go nodejs npm python3 openjdk17

# Build datamitsu
COPY . /workspace
WORKDIR /workspace
RUN go build -o /usr/local/bin/datamitsu

# Initialize - binary apps auto-detect musl, JVM auto-fallbacks to system mode
# FNM and UV require manual system mode config (see manual override section above)
RUN datamitsu init
```

Binary apps automatically detect musl and select the appropriate variant. For JVM, datamitsu auto-detects the system `java` binary. For FNM and UV, configure system mode in your wrapper config (see [manual system mode override](#manual-system-mode-override) above).

## Checking Detection Results

Use the `facts().libc` value in your config to verify detection:

```javascript
// In datamitsu.config.ts
console.log("Detected libc:", facts().libc);
```

The `devtools apps list` command shows which binary variant was resolved for each app on the current system.

## Troubleshooting

### `fcntl64: symbol not found` or similar errors

This means a glibc binary is running on a musl system. For binary apps, check if a musl variant is available for the tool and add it to the config. For managed runtimes, the JVM runtime auto-falls back to system `java` if installed (`apk add openjdk17`). For FNM and UV, configure system mode manually in your wrapper config since these binaries are not available in Alpine's default repositories. If no musl variant or system binary exists, the tool cannot run on Alpine without a glibc compatibility layer.

### Detection returns `unknown`

If libc detection returns `unknown`, datamitsu cannot determine the libc and will use neutral scoring. This can happen in minimal containers without `ldd` or with statically linked init binaries. The resolver will still select the best available candidate but cannot prefer musl-specific builds.
