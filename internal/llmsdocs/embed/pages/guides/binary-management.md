# Binary Management

> How datamitsu downloads, verifies, and stores binary apps

datamitsu downloads, verifies, and stores tool binaries so your entire team uses the exact same versions across all platforms. Once configured, binaries are managed automatically with no manual installation required.

## How It Works

When you run a tool through datamitsu (e.g., `datamitsu exec lefthook`), the binary manager:

1. Checks the global store for a matching binary
2. If absent, downloads the binary from the configured URL
3. Verifies the SHA-256 hash of the downloaded file
4. Extracts the binary from its archive format
5. Stores the result at a content-addressed path
6. Executes the binary with your arguments

All of this happens transparently. After the first download, subsequent runs use the stored binary instantly.

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
globalThis.getMinVersion = () => "0.0.1";
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

Not every tool needs to support all platforms. Define only the OS/arch/libc
combinations for which the tool publishes downloads. A direct
`datamitsu exec <app>` fails with a platform error when none matches; a
`fix`/`lint`/`check` plan reports that tool as skipped. Add `--fail-on-skip` in
CI when an unavailable platform binary must fail the run.

## Content-Addressed Storage

Binaries live in the global content-addressed store:

```
{store}/.bin/{name}/{configHash}/
```

On Windows a single-file binary is stored as `{configHash}.exe`: Windows starts a program only by its
extension, so a file without one could be downloaded and verified but never run. Archives extracted
as a directory keep the bare hash on every platform.

The store key (`configHash`) is computed from:

- The binary's URL, hash, format, and extraction metadata
- The resolved target (OS, architecture, and libc)

This means changing any configuration detail (like upgrading to a new version) or running on a different libc variant selects a new store path and triggers a fresh download. Glibc and musl binaries get separate entries.

### Store Location

By default, the store is a child of the standard user cache directory:

- **All platforms**: `~/.cache/datamitsu/store/`

You can override this location by setting the `DATAMITSU_CACHE_DIR` or `XDG_CACHE_HOME` environment variable.

Use `datamitsu store path` to see the exact path, and `datamitsu store clear` to remove all stored binaries.

## Concurrent Downloads

When running `datamitsu init`, binaries are downloaded concurrently. The default concurrency is 3, controllable via the `DATAMITSU_CONCURRENCY` environment variable:

```bash
DATAMITSU_CONCURRENCY=10 datamitsu init
```

Progress bars are displayed during downloads so you can see what's being fetched.

## Download Timeouts and Retries

Downloads have **no overall timeout** — a large archive on a slow link is a healthy download that simply takes a while. Instead, each attempt is watched for progress: if no data arrives for 2 minutes, the attempt is aborted with a `stalled: no data received` error and retried automatically (up to 4 attempts with exponential backoff).

The end-to-end time of a single app install (download + extract + verify) is bounded by `DATAMITSU_INSTALL_TIMEOUT` (600 seconds by default; `0` disables the deadline). On very slow connections raise it for the affected runs:

```bash
DATAMITSU_INSTALL_TIMEOUT=3600 datamitsu init
```

The effective value is introspectable via `datamitsu config runtime | jq .installTimeoutSeconds`.

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

Some tools need their entire archive extracted as a directory tree rather than a single binary: a JDK distribution, or `protoc`, which finds its well-known types in the `include/` directory beside its own binary and fails on `import "google/protobuf/timestamp.proto"` when only the executable is installed. Set `extractDir: true` in the OS/arch info, and name the command inside the tree with `binaryPath`:

```javascript
binary: {
  binaries: {
    linux: {
      amd64: {
        glibc: {
          url: "https://github.com/protocolbuffers/protobuf/releases/download/v36.2/protoc-36.2-linux-x86_64.zip",
          hash: "0000000000000000000000000000000000000000000000000000000000000000", // replace with the expected SHA-256
          contentType: "zip",
          binaryPath: "bin/protoc",
          extractDir: true,
        },
      },
    },
  },
}
```

The whole tree is installed under one store directory and `binaryPath` is what runs, so the binary sees its files where the archive put them. `binaryPath` is required with `extractDir`, must be exact, and the `contentType` must be a tar or zip archive. Install fails when the archive does not contain the path, and `devtools verify-all` checks that the path is an executable.

Exact means more than it does for a single-file entry. Extracting one file finds `binaryPath` by its last component when the full path is not in the archive, so a guessed path like `protoc-36.2/protoc` extracts `bin/protoc` and passes `--verify-extraction`; an extracted directory is not searched, so the same guess fails the install. `devtools pull-github` writes neither `extractDir` nor a path it could not guess, so set both after loading a registry, per operating system:

```javascript
for (const [os, archMap] of Object.entries(apps.protoc.binary.binaries)) {
  for (const libcMap of Object.values(archMap)) {
    for (const entry of Object.values(libcMap)) {
      entry.extractDir = true;
      entry.binaryPath = os === "windows" ? "bin/protoc.exe" : "bin/protoc";
    }
  }
}
```
