---
title: "Migrate: fnm runtime → node runtime"
description: Breaking change — the fnm runtime kind was removed in favour of a direct, SHA-256-pinned Node.js archive. How to upgrade your wrapper config.
---

# Migrate: fnm runtime → node runtime

:::warning Breaking change (clean break)

The `fnm` runtime kind has been **removed**. datamitsu no longer downloads the
[fnm](https://github.com/Schniz/fnm) version-manager binary or shells out to
`fnm install`. Node.js is now acquired as a **direct, SHA-256-pinned archive** —
exactly like the JVM runtime downloads a JDK.

Any config or lock file that references kind `"fnm"` will no longer load. After
upgrading datamitsu, consumers must **re-init**:

```bash
datamitsu init
```

:::

## What changed

| Before (fnm)                                  | After (node)                                                        |
| --------------------------------------------- | ------------------------------------------------------------------- |
| Runtime kind `"fnm"`                          | Runtime kind `"node"`                                               |
| Downloads the fnm binary, runs `fnm install`  | Downloads a pinned Node.js archive, verifies SHA-256, extracts it   |
| Node integrity trusted the mirror's `SHASUMS` | Node pinned by `sha256` in `runtimes.json` (anchored in git)        |
| musl via a dynamic mirror + `FNM_*` env vars  | musl via **static** registry entries (unofficial-builds.nodejs.org) |
| App sub-object `fnm: { … }`                   | App sub-object `node: { … }`                                        |
| `config/src/fnmApps.json`                     | `config/src/nodeApps.json`                                          |
| `devtools pull-fnm`                           | `devtools pull-node`                                                |
| `devtools pull-runtimes --runtime fnm`        | `devtools pull-runtimes --runtime node`                             |

### Why

- **No artificial download timeout.** fnm hard-coded a 30-second download
  timeout. On Alpine/CI the ~34 MB musl tarball from the slow single-host musl
  mirror routinely blew that limit and failed `init`. The direct downloader has
  no such cap.
- **Supply-chain hash pinning.** fnm exposed no way to pin a Node hash. Node was
  the only runtime that could not be pinned. It now carries an explicit
  `sha256` per `os/arch/libc`, anchored in git — like `jvm` and `uv`.
- **No mirror machinery.** The `FNM_NODE_DIST_MIRROR` / `FNM_ARCH` mirror hack
  and the fnm shell-integration overhead are gone. The registry is the single
  source of `{url, hash}`.

## Update your wrapper config

### 1. App definitions: `fnm:` → `node:`

The app sub-object keeps the same shape; only the key name changes.

```js
// BEFORE
eslint: {
  kind: "fnm",
  fnm: {
    packageName: "eslint",
    version: "9.18.0",
    binPath: "node_modules/.bin/eslint",
    lockFile: "br:…",
  },
},

// AFTER
eslint: {
  kind: "node",
  node: {
    packageName: "eslint",
    version: "9.18.0",
    binPath: "node_modules/.bin/eslint",
    lockFile: "br:…",
  },
},
```

### 2. Runtime definition: regenerate with `pull-node`

Replace the hand-written fnm runtime (fnm-binary download + `nodeVersion` /
`pnpmVersion`) with a generated `node` archive entry. Run:

```bash
datamitsu devtools pull-runtimes config/src/runtimes.json --update --runtime node
```

This writes a `node` entry with a pinned `{ url, hash, contentType, binaryPath,
extractDir }` per platform and a `node: { nodeVersion, pnpmVersion, pnpmHash }`
block. See
[Maintaining Wrapper Packages → Bumping the Node.js runtime](./maintain-wrapper.md#node-apps-npm-devtools-pull-node)
for how the hashes are verified (glibc/darwin/windows GPG-verified against
nodejs.org's signed `SHASUMS256.txt.asc`; musl pinned from unofficial-builds).

### 3. Apps file rename: `fnmApps.json` → `nodeApps.json`

```bash
git mv config/src/fnmApps.json config/src/nodeApps.json
```

Update the package-version puller to target the new file:

```bash
datamitsu devtools pull-node config/src/nodeApps.json --update
```

### 4. Regenerate lock files

Node apps still require a `lockFile` (`pnpm-lock.yaml`, installed with
`--frozen-lockfile`). Regenerate after the bump:

```bash
datamitsu config lockfile <appName>
```

## Cache layout changes

If you reference cache paths in scripts or docs, note the renamed directories:

| Before                             | After                          |
| ---------------------------------- | ------------------------------ |
| `.apps/fnm/{app}/{hash}/`          | `.apps/node/{app}/{hash}/`     |
| `.runtimes/fnm-nodes/v{version}/`  | `.runtimes/node/{configHash}/` |
| `.runtimes/fnm-pnpm/{ver}/{hash}/` | `.runtimes/pnpm/{ver}/{hash}/` |

A `datamitsu store clear` followed by `datamitsu init` rebuilds everything under
the new layout.

## Alpine / musl

musl support no longer needs any configuration. The registry ships explicit
`musl` entries (`node-v<ver>-linux-{x64,arm64}-musl.tar.xz` from
unofficial-builds.nodejs.org), each SHA-256-pinned and selected automatically on
a musl host. The `FNM_NODE_DIST_MIRROR` and `FNM_ARCH` environment variables no
longer have any effect — remove them from your Dockerfile and CI config. See
[Use in Alpine Linux → Node.js (musl)](./use-in-alpine.md#nodejs-musl-static-archive-entries).
