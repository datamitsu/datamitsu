---
title: Manage Cache
description: How to manage datamitsu's cache, store, and per-project data
---

# Manage Cache

datamitsu uses two storage areas: a per-project **cache** for tool results and a global **store** for downloaded binaries and runtimes. Understanding these helps you troubleshoot issues and manage disk space.

## Cache vs Store

| Area      | What it holds                      | Scope       | Command prefix    |
| --------- | ---------------------------------- | ----------- | ----------------- |
| **Cache** | Lint/fix results, tool cache files | Per-project | `datamitsu cache` |
| **Store** | Binaries, runtimes, apps, remotes  | Global      | `datamitsu store` |

## Cache Structure

Each project gets its own isolated cache namespace:

```
~/.cache/datamitsu/cache/projects/{hash}/cache/
├── packages/
│   ├── frontend/
│   │   ├── tsc/tsbuildinfo
│   │   └── eslint/.eslintcache
│   └── backend/
│       └── golangci-lint/
└── services/
    └── api/
        └── ruff/.ruff_cache/
```

The `{hash}` is an XXH3-128 hash of the git root path. Within it, tool caches are organized by project path and tool name.

Tools reference their cache directory using the `{toolCache}` placeholder in their operation arguments or environment variables.

## Store Structure

The global store holds all downloaded artifacts:

```
~/.cache/datamitsu/store/
├── .bin/                    # Binary apps
│   └── lefthook/{hash}/
├── .runtimes/               # Runtime binaries
│   ├── fnm/{hash}/
│   ├── fnm-nodes/v22.14.0/
│   ├── fnm-pnpm/10.5.2/{hash}/
│   └── jvm/{hash}/
├── .apps/                   # Runtime-managed app environments
│   ├── uv/yamllint/{hash}/
│   └── fnm/eslint/{hash}/
├── .remote-configs/         # Cached remote configs
└── .pnpm-store/             # Shared pnpm content-addressable store
```

## Viewing Cache Paths

```bash
# Global cache directory
datamitsu cache path

# Current project's cache directory
datamitsu cache path project

# Global store directory
datamitsu store path
```

## Clearing the Cache

Clear per-project tool caches when lint/fix results seem stale:

```bash
# Clear current project's cache
datamitsu cache clear

# Preview what would be deleted
datamitsu cache clear --dry-run

# Clear caches for all projects
datamitsu cache clear --all
```

This removes lint/fix result caches and per-tool cache files (like `.eslintcache` or tsbuildinfo). It does not remove downloaded binaries or runtimes.

## Clearing the Store

Clear the global store to remove all downloaded binaries and runtimes:

```bash
datamitsu store clear
```

:::warning
This removes everything: binaries, runtimes, app environments, and remote config caches. You will need to run `datamitsu init` again to re-download everything.
:::

## When to Clear Cache

**Clear the project cache** (`datamitsu cache clear`) when:

- Lint or fix results seem incorrect or stale
- You've changed tool configurations and want a fresh run
- You're debugging tool behavior

**Clear the store** (`datamitsu store clear`) when:

- You want to reclaim disk space
- A binary or runtime download seems corrupted
- You've changed binary URLs or hashes and want a clean state

**You usually don't need to clear anything** because:

- Changing app versions or hashes in your config automatically creates new cache entries
- The old entries stay around but aren't used

## Cache Invalidation

datamitsu automatically invalidates caches when configuration changes:

- **Binary apps**: Cache key includes URL, hash, format, OS, and architecture. Upgrading a version creates a new cache entry
- **Runtime apps**: Cache key includes runtime config, app config, OS, and architecture. Changing the package version or runtime version creates a new environment
- **Remote configs**: Cached by URL; cache validity is determined by hash match (no TTL)

No manual cache management is needed for version upgrades -- just update the config and the next run uses the new version.
