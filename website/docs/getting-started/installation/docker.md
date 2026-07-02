---
title: Docker
description: Run datamitsu from the official Docker images on Docker Hub or GitHub Container Registry
---

# Docker

Official images are published to both registries on every stable release:

- **Docker Hub** — `docker.io/datamitsu/datamitsu`
- **GitHub Container Registry** — `ghcr.io/datamitsu/datamitsu`

```bash
docker run --rm -v "$PWD:/workspace" datamitsu/datamitsu:latest lint
```

The image's entrypoint is the `datamitsu` binary, the working directory is
`/workspace`, and it runs as a non-root `datamitsu` user (UID 1000). Mount your
project at `/workspace` and pass any CLI command as arguments.

## Variants

- **Debian** (default) — glibc-based, e.g. `datamitsu/datamitsu:latest`
- **Alpine** — musl-based, suffixed `-alpine`, e.g. `datamitsu/datamitsu:latest-alpine`

## Tags

| Tag                    | Meaning                            |
| ---------------------- | ---------------------------------- |
| `latest`               | Latest stable release              |
| `stable`               | Latest stable release (alias)      |
| `0.1.13` / `0.1` / `0` | Pinned semver (full, minor, major) |
| `unstable`             | Latest unstable build (Docker Hub) |

Unstable builds are also published to `ghcr.io/datamitsu/datamitsu-unstable`.

Both `linux/amd64` and `linux/arm64` are supported via multi-arch manifests.

## Examples

```bash
# Lint the current project (pinned version, GHCR)
docker run --rm -v "$PWD:/workspace" ghcr.io/datamitsu/datamitsu:0.1.13 lint

# Fix formatting in place
docker run --rm -v "$PWD:/workspace" datamitsu/datamitsu:latest fix

# Inspect the effective runtime configuration
docker run --rm datamitsu/datamitsu:latest config runtime
```

:::tip
Tool downloads land in the container's datamitsu store and are lost when the
container exits. For repeated runs, persist the store with an extra volume:
`-v datamitsu-store:/home/datamitsu/.cache/datamitsu`.
:::
