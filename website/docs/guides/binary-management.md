---
title: Binary Management
description: How datamitsu manages binary downloads, hash verification, and caching
---

# Binary Management

datamitsu downloads, verifies, and caches tool binaries so your entire team uses the exact same versions across all platforms. Once configured, binaries are managed automatically with no manual installation required.

## How It Works

When you run a tool through datamitsu (e.g., `datamitsu exec lefthook`), the binary manager:

1. Checks the local cache for a matching binary
2. If not cached, downloads the binary from the configured URL
3. Verifies the SHA-256 hash of the downloaded file
4. Extracts the binary from its archive format
5. Caches the result for future use
6. Executes the binary with your arguments

All of this happens transparently. After the first download, subsequent runs use the cached binary instantly.

## Defining a Binary App

Binary apps are defined in your configuration with platform-specific URLs and hashes:

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  return {
    ...config,
    apps: {
      ...config.apps,
      lefthook: {
        binary: {
          binaries: {
            darwin: {
              amd64: {
                unknown: {
                  url: "https://github.com/evilmartians/lefthook/releases/download/v1.6.1/lefthook_1.6.1_MacOS_x86_64.gz",
                  hash: "abc123...",
                  contentType: "gz",
                },
              },
              arm64: {
                unknown: {
                  url: "https://github.com/evilmartians/lefthook/releases/download/v1.6.1/lefthook_1.6.1_MacOS_arm64.gz",
                  hash: "def456...",
                  contentType: "gz",
                },
              },
            },
            linux: {
              amd64: {
                glibc: {
                  url: "https://github.com/evilmartians/lefthook/releases/download/v1.6.1/lefthook_1.6.1_Linux_x86_64.gz",
                  hash: "789abc...",
                  contentType: "gz",
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
```

The `binaries` map uses a three-level nested structure: `os → arch → libc → BinaryOsArchInfo`. Linux platforms use `"glibc"` or `"musl"` as the libc key; non-Linux platforms use `"unknown"`.

## Hash Verification

Every binary download requires a SHA-256 hash. This is a strict security requirement -- datamitsu will refuse to download any binary without a hash.

The hash is verified after download and before extraction. If the hash doesn't match, the download is rejected and an error is returned. This protects against:

- Corrupted downloads
- Tampered binaries
- Supply chain attacks

Hashes are plain SHA-256 hex strings:

```javascript
hash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
```

## Supported Archive Formats

datamitsu supports extracting binaries from these archive formats:

| Format    | Extension  | Description                |
| --------- | ---------- | -------------------------- |
| `tar.gz`  | `.tar.gz`  | Gzip-compressed tar        |
| `tar.xz`  | `.tar.xz`  | XZ-compressed tar          |
| `tar.bz2` | `.tar.bz2` | Bzip2-compressed tar       |
| `tar.zst` | `.tar.zst` | Zstandard-compressed tar   |
| `tar`     | `.tar`     | Uncompressed tar           |
| `zip`     | `.zip`     | Zip archive                |
| `gz`      | `.gz`      | Single gzip file           |
| `bz2`     | `.bz2`     | Single bzip2 file          |
| `xz`      | `.xz`      | Single xz file             |
| `zst`     | `.zst`     | Single zstandard file      |
| `binary`  | -          | Raw binary (no extraction) |

## Platform Support

datamitsu supports a three-dimensional target model:

- **Operating Systems**: darwin (macOS), linux, freebsd, openbsd, windows
- **Architectures**: amd64, arm64, aarch64
- **Libc** (Linux only): glibc, musl, unknown

On Linux, datamitsu automatically detects the libc implementation (glibc vs musl) via multi-stage detection (ldd parsing, ELF interpreter reading, loader path globbing). When a musl-specific binary is available, it is selected automatically. When only a glibc binary exists, datamitsu falls back with a warning.

Not every tool needs to support all platforms. Define only the OS/arch/libc combinations that the tool provides downloads for. If a user tries to run a tool on an unsupported platform, they get a clear error message.

## Caching

Binaries are cached in a content-addressable store under the datamitsu cache directory:

```
{cache}/.bin/{name}/{configHash}/
```

The cache key (`configHash`) is computed from:

- The binary's URL, hash, and format
- The resolved target (OS, architecture, and libc)

This means changing any configuration detail (like upgrading to a new version) or running on a different libc variant automatically invalidates the cache and triggers a fresh download. Glibc and musl binaries get separate cache entries.

### Cache Location

The cache is stored in the standard user cache directory:

- **All platforms**: `~/.cache/datamitsu/store/`

You can override this location by setting the `DATAMITSU_CACHE_DIR` or `XDG_CACHE_HOME` environment variable.

Use `datamitsu store path` to see the exact path, and `datamitsu store clear` to remove all cached binaries.

## Concurrent Downloads

When running `datamitsu init`, binaries are downloaded concurrently. The default concurrency is 3, controllable via the `DATAMITSU_CONCURRENCY` environment variable:

```bash
DATAMITSU_CONCURRENCY=10 datamitsu init
```

Progress bars are displayed during downloads so you can see what's being fetched.

## Version Checks

Apps can optionally configure version checks used by `datamitsu devtools verify-all`:

```javascript
lefthook: {
  binary: {
    // ... binaries definition
  },
  versionCheck: {
    args: ["version"],  // default is ["--version"]
  },
}
```

Set `versionCheck: { disabled: true }` to skip version checking for tools that don't support it.

## ExtractDir Mode

Some tools (like JDK distributions) need their entire archive extracted as a directory tree rather than a single binary. Set `extractDir: true` in the OS/arch info to enable this:

```javascript
binary: {
  binaries: {
    linux: {
      amd64: {
        glibc: {
          url: "https://example.com/jdk-21_linux-x64.tar.gz",
          hash: "...",
          contentType: "tar.gz",
          extractDir: true,
        },
      },
    },
  },
}
```
