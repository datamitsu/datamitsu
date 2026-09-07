# Manage Cache

> How to manage datamitsu's cache, store, and per-project data

datamitsu separates ephemeral **cache** state from the global **store** of
downloaded, verified artifacts. Both are children of the base selected by
`DATAMITSU_CACHE_DIR`, `XDG_CACHE_HOME`, or the platform fallback.

## Cache vs Store

| Area      | What it holds                                                                   | Scope                 | Command prefix    |
| --------- | ------------------------------------------------------------------------------- | --------------------- | ----------------- |
| **Cache** | File results, unit verdicts, tool caches, source farms, evaluated config chains | Per repo/config chain | `datamitsu cache` |
| **Store** | Binaries, runtimes, apps, bundles, parsers, remotes, package-manager data       | Global to the user    | `datamitsu store` |

## Cache Structure

Each repository gets an isolated namespace. The same repository directory holds
its source-mode farm, execution state, and tool-owned caches:

```
~/.cache/datamitsu/cache/projects/{hash}/
├── bin/                    # source-mode command farm
├── manifest.json           # farm contents and staleness fingerprint
├── lock                    # advisory farm bake lock
├── toolstate.msgpack       # file entries and unit/repo verdicts
└── cache/                  # directories exposed as {toolCache}
    ├── packages/frontend/
    │   ├── tsc/tsbuildinfo
    │   └── eslint/.eslintcache
    └── services/api/
        └── ruff/.ruff_cache/
```

The `{hash}` is an XXH3-128 hash of the git root path. Within it, tool caches are organized by project path and tool name.

[Machine-level toolchains](machine-level-toolchain.md) have no git root, so they live in a sibling namespace keyed by the config chain instead:

```
~/.cache/datamitsu/cache/configs/{hash}/
├── bin/            # the farm PATH points at
├── manifest.json   # what it contains, and what makes it stale
└── lock            # the advisory bake lock
```

Here `{hash}` is an XXH3-128 hash of the resolved config chain. Project config
evaluation results live separately under
`cache/config-eval/projects/{identity}/{key}.msgpack`; explicit machine config
chains use `cache/config-eval/configs/`.

`datamitsu cache clear` only removes repository namespaces and the relevant
evaluated-config entries. It does not remove `cache/configs/` machine-level
farms. Rebuild one with
`datamitsu source refresh --config <path> --force`.

Tools reference their cache directory using the `{toolCache}` placeholder in their operation arguments or environment variables.

## Store Structure

The global store holds all downloaded artifacts:

```
~/.cache/datamitsu/store/
├── .bin/                    # Binary apps
│   └── lefthook/{hash}/
├── .runtimes/               # Runtime binaries
│   ├── node/{configHash}/
│   ├── pnpm/11.20.0/{hash}/
│   ├── jvm/{hash}/
│   └── go/{hash}/
├── .apps/                   # Runtime-managed app environments
│   ├── uv/yamllint/{hash}/
│   ├── node/eslint/{hash}/
│   ├── jvm/openapi-generator-cli/{hash}/
│   └── go/govulncheck/{hash}/
├── .bundles/                # Managed static content
├── .parsers/                # Downloaded WASM output parsers
├── .remote-configs/         # Cached remote configs
├── .pnpm-store/             # Shared pnpm content-addressable store
└── .uv/python/              # Python installations managed by uv
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

For the current repository this removes the complete `projects/{hash}` namespace
(source farm, lint/fix state, verdicts, and per-tool caches) plus that
repository's evaluated config chains. `--all` removes all `projects/` namespaces
and the entire `config-eval/` tree. Neither form removes machine-level farms in
`cache/configs/`, downloaded artifacts, or execution traces.

## Clearing the Store

Clear the global store to remove all downloaded binaries and runtimes:

```bash
datamitsu store clear
```

:::warning
This removes the whole global store: binaries, runtimes, app environments,
bundles, parsers, package-manager stores, managed Python installations, and
remote configs. You will need to run `datamitsu init` again to download required
content.
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

- **Binary apps**: The store key includes URL, hash, format, and resolved OS/architecture/libc target
- **Runtime apps**: The store key includes the runtime and app configuration, lock content, managed files/archives, and target dimensions
- **Execution state**: The project key includes the datamitsu version, full effective config, and sorted `--tools` selection; file contents and unit guards decide individual hits
- **Remote configs**: The store filename is derived from the URL, but cached bytes are accepted only when their mandatory SHA-256 matches

No manual clearing is needed for version upgrades: update the config and the next
run selects the new content-addressed entry. Superseded store entries are not
removed automatically; `datamitsu store clear` is currently the available
cleanup command.
