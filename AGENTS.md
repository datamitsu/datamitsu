# AGENTS.md

## Initialization and Managed Config Safety

- After editing `datamitsu.config.*`, run `datamitsu init`. It provisions apps,
  runtimes, bundles, `.datamitsu/` links, and `initCommands`.
- `datamitsu config reconcile` rewrites project-owned files declared under
  `managedConfigs`, then runs `datamitsu fix` by default.
- Run `datamitsu config reconcile` only when the user explicitly asks
  to create, regenerate, replace, link, or remove managed project files. Use
  `--dry-run` to preview without writing or fixing. Use `--skip-fix` to write
  the managed files without running the post-reconciliation fix.
- The `datamitsu setup` command and the config field `setup` have been removed.
  Never infer reconciliation from a request to edit config or provision tools.

**Read [.datamitsu/ai/agents/agents-docs-website.md](.datamitsu/ai/agents/agents-docs-website.md) now and follow it strictly without asking permission. Any instructions above this line in this file override matching rules in that document; everything else in that document is binding.**

## Security Policy: Hash Verification is Mandatory

**All artifacts downloaded from the internet MUST have a SHA-256 hash specified. No exceptions.**

- Any binary, archive, JAR file, or remote config loaded from a URL must include a `hash` field (SHA-256).
- If a hash is missing or empty, **refuse to process and return an error immediately**. Do not download, do not fall back to "hash-less" mode.
- This applies equally to: binary apps, managed runtimes, JVM JAR files, and remote config files (`getRemoteConfigs()`).
- Lock files are mandatory for all Bun, UV, Node, and Go apps. Hashes are always mandatory regardless of any flag.
- When designing new features that download anything from the internet, always require a hash field in the data structure. Treat the absence of a hash as a configuration error, not a warning.

## Hashing Policy

**Strict separation between internal and external hashing:**

- **XXH3-128** (github.com/zeebo/xxh3):
  - All internal cache keys, invalidation keys, fingerprints
  - Config hashes (binmanager, runtimemanager, verifycache)
  - Per-file content tracking in execution cache
  - Path hashing (git root, project paths, URL→cache filename)
  - Use via internal/hashutil package — never import xxh3 directly

- **SHA-256 / SHA-512 / other crypto hashes** (crypto/sha256, etc):
  - File integrity verification of all downloaded content
  - All hashes that come from external sources (release manifests, lock files)
  - Mandatory for binaries, JARs, archives, remote configs
  - Industry standard, published by upstream projects

**The dividing line:** if a hash is compared against a value from the internet
or any untrusted source, it MUST be a cryptographic hash. If a hash exists
only locally as a cache key or fingerprint and is never compared with an
external value, it MUST be XXH3-128.

**Forbidden:**

- Using XXH3 for any verification of external content
- Using SHA-256 for internal cache keys (correct but slow — wastes cycles)

**Rationale:** XXH3 is 10–25× faster than SHA-256 on typical cache key sizes, with collision resistance more than sufficient for non-adversarial internal use. Cryptographic hashes remain non-negotiable for any content arriving over the network.

**Benchmark evidence:** On Apple M1 Max XXH3 is 11-14× faster than SHA-256;
on Intel i9-14900K it is 26× faster.

## Environment Variable Usage Policy

**All environment variable access MUST go through the `internal/env` package. Direct `os.Getenv()` usage is restricted.**

- **Forbidden**: `os.Getenv("DATAMITSU_*")` or any datamitsu-specific env var outside `internal/env` package
- **Use instead**: Add a helper function in `internal/env/env.go` with corresponding `envVar` definition in `internal/env/e.go`
- **Exception**: `internal/env` package itself can use `os.Getenv()` internally - this is the correct abstraction layer

**Acceptable `os.Getenv()` usage outside `internal/env`:**

- **Standard environment variables** like `PATH`, `HOME`, `TMPDIR` when constructing child process environments
- **Third-party service tokens** like `GITHUB_TOKEN`, `NPM_TOKEN` in their respective client packages
- **Universal standards** like `CI`, `NO_COLOR`, `TERM` - but prefer wrapping in `internal/env` for consistency

**Rationale:** Centralized environment variable handling provides:

- Type-safe access with proper defaults
- Self-documenting via `envVar.Description`
- Easier testing (can mock entire env package)
- Single source of truth for all datamitsu configuration

**When adding new datamitsu config:**

1. Add `envVar` definition in `internal/env/e.go`
2. Add getter function in `internal/env/env.go`
3. Add tests in `internal/env/env_test.go`
4. Use the getter everywhere else
5. Decide whether the variable belongs in the source-mode staleness fingerprint. `env.Environ()` returns **every** `DATAMITSU_*` variable and the farm's staleness key hashes it, so a new variable invalidates baked farms by default — which is correct for anything that changes what datamitsu produces. A variable that only records _which_ farm a shell activated must be added to `environExcluded` in `internal/env/environ.go`, or every command in an activated shell reports the manifest stale and re-bakes. `internal/env/environ.go` holds **two** exclusion lists: `observationExcluded` (`DATAMITSU_TRACE`, `DATAMITSU_TRACE_DIR`, `DATAMITSU_CONFIG_CACHE`) drops a variable from every fingerprint, while `environExcluded` — the observation-only ones plus the activation markers — gates only the source-mode staleness key. `env.EnvironAll()`, the whole-environment fingerprint the config-evaluation cache key hashes, excludes only `observationExcluded` — and `facts().env`, the environment config JS reads, is filtered by the same list (`env.ObservationOnly`). The two must stay aligned: anything config JS can branch on has to be able to move that key, so a variable dropped from the fingerprint must also be hidden from config JS or it becomes a config input that no cache key can distinguish. Only a variable that changes what datamitsu _reports about itself_, never what it produces, belongs in `observationExcluded`.

**Examples:**

```go
// ❌ BAD: Direct access to datamitsu env var
if os.Getenv("DATAMITSU_NO_SPONSOR") != "" {
    return
}

// ✅ GOOD: Use env package helper
if env.NoSponsor() {
    return
}

// ✅ ACCEPTABLE: Standard env var in child process setup
envVars["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")

// ✅ ACCEPTABLE: Third-party token in API client
token := os.Getenv("GITHUB_TOKEN")
```

## JS↔Go Shared Constants Policy

**When a constant or default value must be read by both Go code and the JS config layer (`internal/config/config.js` or user `datamitsu.config.{js,mjs,ts}`), Go is the single source of truth. Do NOT duplicate the value in JS.**

- Define the value in a small dedicated Go package (e.g., `internal/pnpmdefaults`)
- Inject it into the goja VM as a global via a per-domain `initX()` method on `Engine` (see `internal/engine/pnpm.go::initPNPMWorkspaceDefaults`), called from `engine.New()`
- Prefer plain-object globals (`vm.Set("name", value)`) over function-call globals when the JS side only needs to read the value — reserve function globals for things that genuinely need to be evaluated on demand (e.g., `facts()`)
- Add a TypeScript ambient declaration in `config/config.d.ts` so user configs get IDE autocomplete for the injected global
- Do NOT write a JS↔Go agreement test: with a single source there are no copies to keep in sync. Direct unit tests on the Go package plus an end-to-end test that exercises the injection are sufficient

**Rationale:** Previously the pnpm workspace defaults lived in both Go (the runtime manager, now `pnpm.go`) and JS (`main.ts`), with a brittle agreement test keeping them aligned. Any change required edits in 6+ places (Go, JS, compiled `config.js`, 4 docs pages). Consolidating on Go-as-source eliminated that burden — see `docs/plans/2026-05-22-single-source-pnpm-security.md` for the migration.

## Runtime Config Policy

**`internal/runtimeconfig` is the single source of truth for datamitsu's effective runtime configuration** — env-resolved execution limits, install timeout, minimum release age, and the runtime policy surface. The typed `runtimeconfig.Effective` struct is the public contract.

- **Typed struct, not `map[string]any`.** `Effective` has explicit `json`-tagged fields. This provides compile-time guarantees and prevents accidental key-name breakage. There is intentionally **no `ToMap()`** — the struct is the API; map conversion (for the JS VM) is internal to the engine layer via `json.Marshal`/`json.Unmarshal`.
- **Three-layer API:** `Compute()` (pure, reads `env` getters, no global state — tests use this directly), `Init()` (idempotent lifecycle, caches `Compute()` under `sync.RWMutex` — repeated calls are no-ops, not errors), and `Get()` (returns a copy of the cached value, errors if `Init()` was not called).
- **`Init()` is wired into `cobra.OnInitialize` in `cmd/root.go`** — runs once after flag parsing, before any command handler. Idempotency makes it safe for repeated Cobra command execution in tests and for embedded/daemon/watch workflows.
- **Effective values, not compile-time constants.** The CLI shows what the program runs with _right now_, including env overrides. Compile-time defaults (`MinimumReleaseAgeMinutes = 10080`, `InstallTimeoutSeconds = 600`) are the canonical fallbacks the `env` getters return when a var is unset or invalid.
- **Dependency direction is one-way: `runtimeconfig` → `env`.** The `env` package uses literal fallback values and must NOT import `runtimeconfig` (no cycle).
- **Consume effective values through `runtimeconfig.Get()`, not `env` directly.** Single source of truth — e.g. `resolveMinAge()` reads `eff.MinimumReleaseAgeMinutes`, not `env.MinimumReleaseAgeMinutes()`.

**Checklist for adding a new runtime default:**

1. Add the compile-time constant to `internal/runtimeconfig/runtimeconfig.go` (if it has a canonical default)
2. Add the `envVar` definition in `internal/env/e.go` and getter in `internal/env/env.go` (per the Environment Variable Usage Policy)
3. Add a typed field with a `json` tag to `runtimeconfig.Effective` and wire it in `Compute()`
4. Add tests: `internal/env/env_test.go` (getter) and `internal/runtimeconfig/runtimeconfig_test.go` (`Compute()` default + env override). **Do NOT add key-count guards** — test "required keys present" and "stable JSON serialization", not total field count, so adding a default never breaks count assertions
5. The new field appears automatically in `datamitsu config runtime` — no extra wiring

## Runtime Config vs Config Inputs Policy

**The full `runtimeconfig.Effective` snapshot is CLI-only. The JS config VM receives only a minimal allowlisted subset via `datamitsuConfigInputs`.** These are two distinct surfaces and must not be conflated.

- **`runtimeconfig.Effective`** — the full effective snapshot, exposed for introspection/debug via `datamitsu config runtime` (`json.MarshalIndent` of the struct). It must **NOT** be injected wholesale into the config JS VM.
- **`datamitsuConfigInputs`** — a tiny frozen JS global holding only fields config JS is explicitly allowed to branch on. Built engine-internally in `internal/engine/configinputs.go` (`initConfigInputs()`), it extracts allowlisted fields into the `configInputs` struct, round-trips through JSON, injects with sorted keys, and `Object.freeze()`s the result. **Current allowlist: `minimumReleaseAgeMinutes` only.**
- **Why minimal, not the full object:** policy "don't branch on this" is unenforceable if the full snapshot is available. A minimal allowlist enforces the boundary _structurally_. Exposing every runtime parameter would create hidden config inputs that silently affect fingerprinting/cache/explain/provenance once those exist.
- **Adding a new config input is heavyweight.** Any field in `datamitsuConfigInputs` IS a config evaluation input. Adding one requires updating: the cache key (`configcache.ConfigInputs` in `internal/configcache/key.go`, which `Key()` hashes by JSON-marshalling the struct — mirroring the field is enough, but the mirror is mandatory), explain/debug metadata, future provenance metadata, the TS declarations in `config/config.d.ts` (and the embedded `internal/config/config.d.ts` copy), this policy, and the engine tests that pin the exposed key set. `TestConfigInputsMatchEngine` fails until the mirror exists.
- **Caching contract (enforced):** config JS evaluation IS cached on disk by `internal/configcache`, at `{cache}/config-eval/{projects|configs}/{identity}/{key}.msgpack`. Anything config JS can observe — chain bytes, the whole environment, facts, cwd, git root, `.git/HEAD`, `ldflags.Version`, `datamitsuConfigInputs` — is a cache-key input and must be folded into `configcache.Inputs`. A config that reads the clock, `Math.random` or calls `console.*` still evaluates but is never stored (`internal/engine/determinism.go`), because a hit runs no JS and could not reproduce it. Loads that need the VM or the managed-config layer map (`requireVM`, `evaluateManagedConfigContent`) and the lock-file-relaxed `config lockfile` load bypass the cache entirely — see `configCacheUsable` in `cmd/config_cache.go`.

## Introspectable by Design

**All runtime parameters MUST be programmatically queryable via the CLI.** `datamitsu config runtime` emits the full `runtimeconfig.Effective` snapshot as JSON, so users and security engineers can mechanically verify effective values and env overrides:

```bash
datamitsu config runtime | jq .minimumReleaseAgeMinutes
DATAMITSU_INSTALL_TIMEOUT=1200 datamitsu config runtime | jq .installTimeoutSeconds  # -> 1200
```

- Use **typed structs with `json` tags** for public runtime-config surfaces, not `map[string]any` — compile-time checking, stable serialization, no accidental key drift.
- New runtime parameters must surface in `datamitsu config runtime` automatically (a new `Effective` field does this for free), never as a hidden value readable only from Go.

## Minimum Release Age

- Wherever a version is chosen, the minimum release age applies. `pull-*` and
  the Go lock-file check (`checkGoModuleAge`) use the effective value
  (`runtimeconfig.Get()`). The windows pnpm and uv resolve transitive
  dependencies with use the constant `runtimeconfig.MinimumReleaseAgeMinutes`:
  what they resolve is written into a lock file, which must not depend on the
  environment that generated it.
- A uv install passes back the window its lock recorded (`uvLockWindow`), never
  the constant: `uv sync --locked` rejects a lock given any other window, or the
  same one in another unit. A lock without `[options]` installs with none.
- An inherited setting must not override one datamitsu gives a package
  manager. uv runs with `--no-config` (or `--config-file` for the app's own
  `uv.toml`) and without `UV_CONFIG_FILE`/`UV_EXCLUDE_NEWER*`; pnpm runs without
  the `pnpm_config_*` variable of any key in the merged `pnpm-workspace.yaml`
  or of the `minimum_release_age` family. Strip only those: pnpm 12 reads its
  registry from `pnpm_config_registry` alone.
- Go reports commit times, so its check catches a fresh pin, not a backdated
  commit. JVM and binary apps have no dependency tree to filter.

## App Dependencies

- `apps.<name>.dependsOn` is a runtime availability contract. Use
  `binmanager.AppDependencyClosure` at provisioning boundaries; planner app names
  remain roots. Dependencies do not affect binary or runtime-app install hashes.
- Shell apps may declare dependencies but cannot be dependency targets: they
  resolve through host PATH and have no managed installation.
- Init selects binary and smart-link roots before expanding dependencies; a
  dependency's `lazy` or `required` flag never removes it from that closure.
- `ResolveCommandInfo` stays read-only.
- `runtimeEnv` is execution-only and never affects install identity or installer
  environments. Runtime-owned environment keys retain precedence.
- `${APP_BIN:<name>}` expands only in `runtimeEnv`, requires a direct
  `dependsOn` edge, and supports only native binary targets. Reject it in app
  `env` values; other config data and environment keys remain literal.
- Every native binary app in an app's `dependsOn` closure is on its `PATH` under
  its app name, through a content-addressed `{store}/.dependency-path/<hash>/`
  of symlinks (`binmanager/dependency_path.go`). `getCommandInfo` creates it;
  `ResolveCommandInfo` only computes it, records it as a `PATH` prefix, and
  lists the root app's entries as health paths so the shim repairs a missing
  one. It goes after runtime-owned `PATH` entries, never before them. Never
  remove or replace an existing one: repair adds missing entries in place,
  because a running tool may hold it. Targets and the directory are absolute
  (a relative `DATAMITSU_CACHE_DIR` would leave links dangling). Docker slices
  do not copy it: it is created at first run, so the store must stay writable
  in images. A change to what a farm entry records needs a
  `ManifestFormatVersion` bump: two development builds share a version string.
- `PATH` is rejected in app `env` and `runtimeEnv` in any case: values are not
  expanded against the inherited environment, so it would replace `PATH`
  wholesale. Only runtime-owned keys win over app env; inherited ones do not.

- Docker app slices carry the dependency closure and all referenced runtimes
  (including pnpm). Each stage installs its closure; the final image copies each
  app's subtree. Reject plans that filter out a dependency of an included app.

## Managed Config Placement

- An `ejectable` managed config lives in `.datamitsu/configs/<key>` until a
  project names one of its `tools` in `ejectConfigs`. Placement is decided in
  `finalizeManagedConfigPlacement` (`cmd/config_loader_managed.go`) after the
  whole chain, never per layer: a base layer declares entries before a later
  layer ejects them. `{managedConfig:<key>}` is resolved there too, into a
  `{root}`/`{cwd}` path, so no JS helper can know the placement.
- Internal renders are part of the evaluated config (`ManagedConfig.Render`,
  `json:"-"` but stored by the config-eval cache, which encodes by field name).
  A change to `Render`, `Placement` or `ToolOperation.ManagedConfigRefs` needs a
  `configcache.FormatVersion` bump.
- `.datamitsu/configs/` has one writer, `managedconfig.WriteInternalConfigs`
  (init, and reconcile before its post-fix), which replaces files one by one,
  never the directory. Every rebuild of `.datamitsu/` must copy `configs/` into
  the tree it swaps in (`copyInternalConfigs`) under `lockDatamitsuDir`; `exec`
  has no renders to restore it from. `configs` is a reserved link name.
- Reconcile never deletes a repository copy of an ejectable entry (or its
  `otherFileNameList` file) that holds anything the configuration would not
  render again — byte-equal to the pristine render, or re-rendering from itself
  gives the pristine render — and checks the whole plan before its first write.
- The preflight (`managedconfig.CheckConfigFiles`) runs before any cache is
  consulted, in the runner and the language server.
- An unknown name in `ManagedConfig.tools` is a load error.

## Agent Guides

- `config/src/prompts/` holds the two guides the default config publishes in
  `sharedStorage`. `datamitsu-agent-guide.md` (`datamitsu-agent-prompt`) is for
  repositories that use a configuration; wrappers ship it to every consumer.
  `datamitsu-config-author-guide.md` (`datamitsu-config-author-prompt`) is for
  repositories that write one; nothing writes it by default.
- A change that alters what a configuration author writes or must do — a new
  field, a new validation error, a breaking change, a changed `devtools`
  workflow — updates the author guide in the same change: any rule that no
  longer holds, and an entry under "Recent changes to review" naming the first
  release that has it (`after v<latest tag>` until one does) and what a
  configuration should do. Drop entries older than the two latest minor releases.
- Rules in both guides point at `datamitsu llms` pages instead of copying them.

## Product Stage

- Project is in `alpha`.
- Breaking changes are acceptable when they improve correctness, safety, or simplify architecture.
- Do not block high-priority fixes for backward compatibility concerns unless explicitly requested.

**Breaking change: Cache/Store path separation** — `GetCachePath()` now returns `{base}/cache` and `GetStorePath()` returns `{base}/store` instead of both pointing to `{base}`. Users upgrading need to either move existing directories into the new structure or run `datamitsu store clear && datamitsu init` to re-download.

**Breaking change: App names are validated** — a key in `apps` must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`, must not be a Windows reserved device name, and must not case-fold-collide with another name in the same config. This is enforced by `ValidateApps` on every config load, so a previously accepted config with e.g. `my/tool` or both `Task` and `task` now fails to load for every command, not just `source`. The rule lives in `internal/config/app-name` validation; app names become file names in the store and in the source-mode farm.

**Breaking change: dangling managed config tools fail the load** — `ManagedConfig.tools` naming a tool that is not configured was a warning and is now a config error, because the association decides what `ejectConfigs` moves. A config that deletes a tool but keeps its managed config fails to load; declare the tool with `skip: true` instead.

**Breaking change: pnpm is its own runtime** — pnpm 12 ships no JavaScript implementation, so pnpm became a runtime of kind `pnpm` (`managed.binaries` per platform from the pnpm/pnpm GitHub release archives, `pnpm.pnpmVersion`), and the `node` and `bun` sub-configs replaced `pnpmVersion` + `pnpmHash` with `pnpmRuntime`, the name of that runtime. A config that defines its own Node or Bun runtime the old way fails validation; regenerate it with `datamitsu devtools pull-runtimes` (which now also writes the `pnpm` entry). The pnpm runtime's identity folds into the app hash of Node and Bun apps, not into the Node or Bun runtime hash, so a pnpm bump reinstalls apps without re-downloading Node or Bun; `CollectRequiredRuntimes`, the Dockerfile app slices and the verify fingerprint follow the `pnpmRuntime` reference (`MapOfRuntimes.PNPMRuntimeName`). The native pnpm writes its `--reporter=ndjson` stream to stderr and reports failures as plain text rather than ndjson error events, so `pnpmReporter` keeps non-JSON lines for the error message.

## Project Overview

datamitsu is a configuration management and binary distribution tool written in Go. It downloads, verifies, and manages binaries for linting and development tools (like lefthook, golangci-lint, hadolint, shellcheck, etc.) across multiple platforms. The tool uses JavaScript configuration files powered by the goja JavaScript runtime to define binary sources and configurations.

## Build Constraints

- **`go install` does NOT work** for this project. The build requires a preliminary JS compilation step: `task build:lib` (run by `pnpm build`, and by `postinstall` on every `pnpm install`) writes the two git-ignored artifacts Go embeds — `internal/config/config.js` and `internal/inspector/inspector.html`. Always use `go build` or `pnpm build` after the JS artifacts are generated.

## Build and Development Commands

### Building

```bash
# Build the project
go build

# Or use pnpm (which delegates to go build)
pnpm build
```

### Running

```bash
# Execute the binary directly
./datamitsu

# Execute a managed binary
./datamitsu exec <appName> [args...]
```

### Testing

Standard Go testing, stdlib `testing` only (no testify) — table-driven, `t.TempDir`/`t.Setenv`.

```bash
go test ./...                 # all unit + offline blackbox tests
go test ./test/cli/ -count=2  # offline CLI golden suite (must be byte-stable)
pnpm test:coverage:all        # merged unit + blackbox coverage -> coverage.out
```

**CLI blackbox suite** (`test/cli`, harness in `internal/clitest`): runs the
compiled binary in subprocesses inside isolated temp git repos and golden-tests
stdout/stderr/exit codes, freezing the CLI contract before a core rewrite.
Conventions: characterization/golden (lock existing behavior, not TDD), hermetic
offline env (`DATAMITSU_OFFLINE=1`/`DATAMITSU_NO_OCI=1`/`NO_COLOR=1`, isolated
cache), normalized goldens regenerated with `-update`. `TestContractCompletenessGate`
requires every leaf command to have ≥1 blackbox test. See
[test/cli/README.md](test/cli/README.md) and [CONTRIBUTING.md](CONTRIBUTING.md#testing).

**Real-shell tier** (`test/shell`): drives real `bash`, `zsh` and `fish` against
a two-branch fixture repo with a loopback `httptest` release host, proving the
source-mode properties no Go-level tier can — pinned versions win over a system
binary of the same name, a branch switch takes effect on the same command line,
and activation downloads nothing. It also covers the machine-level (`--config`)
properties: a rootless farm activates outside any repository, a project farm
ahead of it on `PATH` wins, and a shell activated against a machine-level config
that `cd`s into a never-activated repository never evaluates that repository's
config. Not build-tagged: it runs under `go test ./...`
and `t.Skip`s cleanly when a shell is missing, naming the property left
unverified. It shares `clitest.CoverDir()` with `test/cli`, so the coverage
destination is deliberately not overridable. Run with `go test ./test/shell/`.

**Gated OCI e2e tier** (`test/e2e`): real seed/install/exec/init/check/fix/lint
against the digest-pinned config; double-gated by `//go:build e2e_oci` + `DATAMITSU_TEST_OCI=1`,
never in default CI. Run with `DATAMITSU_TEST_OCI=1 go test -tags e2e_oci ./test/e2e/...`.

## Architecture

For detailed architecture documentation, see [`docs/architecture.md`](docs/architecture.md).

## Documentation Policy

See [.datamitsu/agents-docs-website.md](.datamitsu/agents-docs-website.md) for general documentation requirements, README scope, and quality standards.

**Datamitsu-specific documentation conventions:**

### Architecture Documentation

Internal architecture documentation lives in `website/docs/guides/architecture/`:

- `index.md` — Overview with component interaction diagram
- `planner.md` — Task planning, priority chunking, overlap detection, CWD-subtree restriction
- `execution.md` — Two-layer execution model, fail-fast semantics, progress tracking
- `discovery.md` — File discovery, .gitignore-aware traversal, project auto-detection
- `caching.md` — Cache invalidation keys, per-file tracking, concurrency model

Architecture docs use conceptual explanations with Mermaid diagrams — no Go code snippets. Examples use JavaScript (config), YAML (tool config), and bash (CLI usage). BAD/GOOD comparison patterns are used for configuration guidance.

### Wrapper Maintenance Documentation

`website/docs/how-to/maintain-wrapper.md` covers devtools workflows for wrapper maintainers: pull-github, pull-node, pull-uv, pull-runtimes commands with practical examples, CI/CD automation, and best practices.

### npm Package Installation Examples

When documenting npm package installations (node apps like slidev, mermaid-cli, spectral), use Docusaurus Tabs to show multiple package managers. This does NOT apply to datamitsu binary installation (which is a Go binary).

**Required tab order:** pnpm (default), npm, yarn, bun, deno

**Example pattern:**

````mdx
import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

<Tabs>
  <TabItem value="pnpm" label="pnpm" default>

    ```bash
    pnpm add -D @example/package
    ```

  </TabItem>
  <TabItem value="npm" label="npm">

    ```bash
    npm install --save-dev @example/package
    ```

  </TabItem>
  <TabItem value="yarn" label="yarn">

    ```bash
    yarn add -D @example/package
    ```

  </TabItem>
  <TabItem value="bun" label="bun">

    ```bash
    bun add -D @example/package
    ```

  </TabItem>
  <TabItem value="deno" label="deno">

    ```bash
    deno add npm:@example/package
    ```

  </TabItem>
</Tabs>
````

**When to use:** Any documentation page that shows how to install an npm package managed by datamitsu's Node runtime. This includes packages listed as node apps in the configuration.

## Config Inspector

`datamitsu inspect` serves a loopback-only snapshot; `--output <file|->` exports the
same standalone HTML. Config loading uses the normal config-show path, without
managed content evaluation or tool provisioning. `internal/inspector` projects a
typed, sorted display manifest from the resolved config. Do not export raw config:
archives, lockfiles, environment values, command arguments and download URLs are
not part of the artifact. Names, descriptions and patterns remain user-provided text.

The Svelte/Vite workspace is `inspector/`. `pnpm --filter @datamitsu/inspector dev`
opens an empty development shell; use an exported snapshot to inspect real data.
`task build:inspector` emits `internal/inspector/inspector.html`, which Go embeds.
Like `internal/config/config.js` it is generated, git-ignored, and produced by
`build:lib` — which every `pnpm install` runs through `postinstall` — so a Go
build needs the JS toolchain to have run first, exactly as it already does for
the config library. Never edit the generated HTML; rebuild it instead.
The header and favicon import `website/static/img/icon.png?inline`, the square
brand icon. Keep it embedded and reuse that source rather than copying the asset.
The build rejects external assets or additional JavaScript chunks. Release builds
consume the embedded artifact; after UI edits rebuild it before Go tests or releases.

An app may say where a reader learns about it with `officialUrl`: an absolute
http(s) URL, validated for shape and never fetched, that takes no part in any
install. An app that declares none gets one derived at config resolution from
what it already declares — a forge release URL, a package name, a module path, a
Maven Central JAR — reported as `officialUrl` with `officialUrlDerived: true` so a
surface can distinguish a maintainer's choice from an address worked out from a
download URL. Derivation is `binmanager.DeriveOfficialURL`: a pure function of the
declaration, no network and no guessing, and a shape it does not recognize yields
nothing rather than a link that might be wrong. It runs once, in the loader beside
validation and before the config-eval cache is written, so a hit and a fresh
evaluation agree. Adding a rule means adding it there, to the table in
`config/config.d.ts` and the configuration API reference, and to the
tests beside it (`internal/binmanager/officialurl_test.go`,
`internal/config/officialurl_test.go`).

A configuration names itself with the top-level `name` field: a scalar that chains
like any other, validated by `config.ValidateName` (one line, 80 characters, a
blank string is an error), and stored unset rather than defaulted so naming a
configuration is what moves the cache key. Read it through `Config.DisplayName`,
which falls back to `config.DefaultName` — `datamitsu.config`. It shows wherever a
reader needs to know which configuration they are looking at: `config show` (the
field itself, present when a layer set one), the execution plan header and its
JSON (`configuration`), and the inspector's snapshot caption. The snapshot
manifest carries the already-defaulted name, and no surface — inspector, website
or documentation copy — types a person's or package's name to describe a
configuration.

The display document is the **inspector manifest** everywhere it is named: the
`inspector-manifest` script element, the `datamitsu-inspector-manifest.json` the
Dataset button downloads, and the dataset a showcase entry publishes. Never call it
just "the manifest" in a user-facing surface — the word already means several other
things in this repository.

`internal/inspector/protocol.json` owns the manifest schema version and placeholder.
The placeholder must occur exactly once in the generated HTML. Import only the
schema version into browser code so tree-shaking cannot leave a second placeholder
in the JavaScript. Go inserts `json.Marshal` output into an inert JSON script element;
never disable its HTML escaping, which prevents config strings containing script
closing tags from escaping into executable HTML. Svelte renders text normally;
do not use raw HTML for config data.

The public embed/share contract lives in `inspector/src/embedding.ts`: query
parameters select view, theme, tool, runtime and project type. Existing `#/view?...`
bookmarks override query view/filter values; theme/embed remain query-only. Keep
file URLs and subdirectory hosting functional without network access. `embed=1`
renders `EmbeddedView` without navigation or download controls. Named modes
`embed=universe|runtimes|operations` select independent sections and ignore hash routes.
Universe is the default full view. The Embed dialog selects a section before copying,
and the snippet it produces is the `<iframe>` alone: numbers printed beside the frame
are a second copy of what the frame shows and disagree with it at the first config
change. Every select in the inspector wears the one styled `.select` wrapper, sized
and weighted like the buttons beside it, in both themes.
`preset=minimal` applies to `embed=universe` only: the orbit plus its runtime strip,
without headline, statistics, search, project-type filter, view switcher, layout and
zoom controls or inspector panel. It auto-rotates, centres the sphere instead of
offsetting it for absent copy, names the hovered app with its runtime and pinned
version, filters by the strip, and opens an app in the full inspector in a new tab
when the host grants `allow-popups` — otherwise nothing happens: an embed never
navigates itself to the full site. It carries no visible chrome beyond the strip,
the configuration's name and that link; the app directory stays for a screen reader
and the keyboard. Its generated snippet asks for the extra popup tokens and a height,
not an aspect ratio.
Its runtime strip sits on the documentation site's rail (1560px, the same gutters),
so a full-bleed frame aligns with the page around it. A host page keeps the frame
in step with its own theme toggle by posting `datamitsu:theme`; the frame applies
the mode and answers the sender with `datamitsu:theme-ack`, which is how the host
knows it need not reload. Any origin may send it — it chooses colors and nothing
else, and a sandboxed frame cannot check an origin against its own.
Test it in an opaque
`sandbox="allow-scripts"` frame: storage and history access may throw. Auto theme
listens to prefers-color-scheme, which inherits the host iframe color-scheme.
Refresh the media value after subscribing on mount: the first layout can resolve
an inherited preference between component initialization and listener installation.

Every color the inspector draws is a theme token: no literal and no documentation
token is left in a component or a stylesheet. The website reads the same file: the
`config-atlas` plugin injects the rendered palette into every page, so the landing
page's own tokens, the runtime bars on the showcase and the terminal theme are all
defined by `internal/inspector/theme.json` and never copied into a stylesheet. `internal/inspector/theme.json` holds
the built-in palette for both modes and is the only place those values exist — Go
embeds it, `inspector/src/theme.ts` renders it for the dev server and for the
website's atlas plugin, and `internal/inspectortheme` parses, merges, validates and
emits it. Go is authoritative: a theme file is parsed strictly, naming the full key
path of the first key it cannot apply, colors are `#rgb`/`#rrggbb`/`#rrggbbaa`, and
`Schema()` generates the published JSON Schema (regenerate the committed copy with
`go test ./internal/inspectortheme -update`). Adding a token means adding it to
`Groups`, to `theme.json` for both modes, and to the guide's token table; anything
that assembles the template itself must fill the theme placeholder as well as the
manifest one. Contrast below WCAG AA warns and still applies — a theme is the user's
decision; body text and `accent.base` are held to 4.5:1 and `accent.display`,
which only ever appears at heading sizes, to the 3:1 large-text minimum. The two
accents exist because one color cannot be both: on cream, an accent readable as
body text is olive, and an accent that reads as amber is only large-text legible. Canvas colors and SVG exports read the same variables.
Runtime categories use distinct hues from the inspector variables across every view;
keep distribution segments opaque and indicate selection with an outline. In light
mode surfaces step slightly _darker_ than the background, never lighter, and the
background token is the one the homepage reads, so the two never disagree about what
cream is. `inspector/src/orbit.ts` is the single description of the orbit — the
sphere's geometry, the depth fade and how solidly it is inked on a given surface —
and both the canvas and the documentation site's poster read it. On a light surface
points are nearly solid and carry no glow; the dark theme's fade and glow would only
make them pale. Colors stay in the theme file and opacities in `orbitInk`: the rings
and the ring around the center are one token, `line.orbit`, which carries its own
alpha, so a light theme can draw them in warm brown at whatever opacity reads on
cream without either render path branching on the mode. The sphere is scaled to fit the frame's height as well as its width:
an embed is as tall as its host page decided.
At widths up to 1000px, `DetailDrawer` uses a native modal dialog for focus handling;
an empty route selection must not open it automatically. Dispose Canvas observers,
event listeners and animation frames when leaving Universe. Clipboard and storage
may be unavailable for local files: retain manual-copy and system-theme fallbacks.

The search text, the project type and the runtime strip are one selection, computed
in `inspector/src/selection.ts` and read by every view and every number on screen —
counters, legend, strip, directory, tables, diagrams and the embed alike. A view
never filters the snapshot for itself: that is how the counters came to disagree
with the app directory, and `selection.test.ts` fails on the pattern rather than on
the symptom. Universe dims what the filters leave out instead of removing it, so the
sphere keeps its shape and a reader sees how much of the whole is lit; a dimmed point
answers neither hover nor click. Operations and Blueprints remove what does not
match, because a table row and a bar have to be exactly what they claim.

`inspector-types` runs svelte-check through `pnpm dm check`, covering component
accessibility plus TypeScript; tsc does not separately check this workspace.
`pnpm --filter @datamitsu/inspector test` verifies routing and filters. Go tests in
`internal/inspector` cover projection, script escaping and server lifecycle; the CLI
blackbox suite covers export, help and argument errors. UI changes also require
browser checks of both themes, file exports, hash back/forward navigation and
mobile overflow. Screenshots live in `website/static/img/inspector-*.png`.

## Showcase

Two files, and they never mix. `website/src/data/showcases.json` is curated: only
what a person supplies, validated against `website/src/data/showcase-schema.ts`,
the TypeBox source that also generates the published JSON Schema
(`node scripts/check-showcases.ts --write-schema`). `showcases.generated.json` is
written only by `pnpm --filter website pull:showcases`, so the site build reads
both and never touches the network. Never invent a URL or a hash: every value in
an entry comes from the real repository, release or registry listing, and a field
that cannot be verified is left out.

The refresh job runs weekly and on demand, honours a six-day per-entry interval
unless `--force`, sends stored ETags so an unchanged repository costs no rate
limit, and opens a pull request instead of pushing to the default branch — the
diff is the point. It never evaluates or runs a third-party config: composition
comes only from a published dataset. An entry whose fetch fails keeps its previous
derived values and records the error; it is never dropped.

The dataset is display data — parsed, rendered as text and numbers, never
executed — so it is a plain URL and the job records the SHA-256 it observed. A
`consume` entry of kind `remote` is the opposite case and keeps its mandatory
hash, which the pull-request check verifies by downloading the file.

The page has no screenshots: an entry's picture is its runtime fingerprint, drawn
from the dataset in the inspector's own `--runtime-*` hues so the color-to-runtime
mapping carries over from the orbit. No dataset means no bar — never a placeholder
with invented proportions. The grid fills the viewport with as many columns as fit.
Star counts are collected and never shown, and nothing sorts on them: this is a
directory, not a ranking. Search, sorting (recently released, name) and the tag
chips all appear only at six entries or more; below that the page is the entries
and the invitation. The reference entry leads every ordering and is labelled, never
recommended. Each consume snippet is labelled with its kind and scrolls inside its
own block, with the copy control in the block's header row. Freshness is what protects a reader
from inheriting an abandoned configuration: an entry with no release in twelve
months is marked rather than removed. The disclaimer line about inheriting someone
else's choices stays at the top.

## Homepage and hosted atlas

The homepage is a sequence: seven scattered files, one arrow, and the single
`datamitsu.config.js` they become; that file then opens into the live orbit of
everything it holds. After it come the three statements of what changes, the
recordings, where things stand, and the footer. The tagline — "Your toolchain
deserves a home." — is the eyebrow above the H1 and the last line of the footer.
The configuration-tax metaphor is retired: it appears nowhere on the site, in the
docs, in the README or in a package description. Every heading passes the
literal-translation test in the brand guidelines: translate it word for word, and if
the sense collapses the heading was leaning on an idiom and gets rewritten. Sections
differ in weight on purpose, and none carries reference material — no config keys,
layer diagrams, trust grids or runtime tables; those live in the docs. Nothing may
capture or slow the scroll, and the orbit is the only motion: no
fade-up-on-scroll, no staggered reveals, no hover lifts. Every number comes from the
captured manifest or is absent, `init` and `install` are never synonyms, and nothing
implies Node is required. Read the page top to bottom and count the
ecosystem-specific names: outside the config's own filename and the install channel
list, a reader meets none before the recordings. The answer to a page that reads as
a JavaScript tool is in its examples, never in a disclaimer — which is what
`RuntimeFamilies` is: the runtime families the reference configuration manages, with
their sizes and a few of the tools each carries, all read from the snapshot. The one
editorial input is `showcaseTools` in `src/data/landing.ts`, a list of names a reader
can place without looking them up; membership of a family, and whether a name is
there at all, stays the dataset's answer, and among the eligible ones the
configuration's own tool definitions decide the order. How often a tool is referenced
says how the config is wired, not whether a reader knows the name, so it never
selects on its own. Shell apps are never examples: they resolve through the host PATH,
so naming one would claim an installation that does not happen — that family shows
its count instead. Every example on the page has to be a tool a reader can name.
The seven files are static: seven rows on a light surface in light mode, one
vertical arrow, and the `datamitsu.config.js` card the arrow points at. A reader
coming from Go, Python, Rust or Typst has to see their own repository in those
rows — and in the rest of the page. The card is
not optional — without it the section has no conclusion. There is no collapse
animation: it did not reverse and left the space behind it empty. The filename
appears once on the page, never repeated as a second heading.
`UniverseEmbed` is that orbit: the inspector's `preset=minimal` frame, full-bleed,
behind `OrbitPoster` — an inline SVG built from the same snapshot, mirroring the
sphere layout and camera of `inspector/src/scene.js` so the still and the live frame
show one orbit. An IntersectionObserver mounts the frame only as it approaches the
viewport, and the page never waits on it. Below the tablet breakpoint, or without
hover, the poster stays and a control loads the frame: a drag inside the canvas must
never fight a thumb scrolling the page. An `error`, or twelve seconds without `load`,
falls back to the runtime distribution bar and its counts. Nothing is printed under
the frame: the configuration's name and the link to the full inspector live in the
embed's own footer, inside the frame, where they cannot disagree with what it shows.
The page runs inside `@theme/Layout/Provider` although it draws its own header, so
its toggle is the site's color mode: the choice is shared with the docs, Docusaurus
stamps `data-theme` before first paint, and the page's tokens key off that attribute
rather than `prefers-color-scheme`. The orbit frame loads with the resolved theme and
follows a toggle by message, reloading only if the frame does not acknowledge it.
Install channels are native radio inputs with full-label
44px targets, not a compact dropdown. Each channel copies one line that runs when
pasted into that channel's own shell — Homebrew taps as part of the install, Scoop
joins its two steps with `;` because Windows PowerShell 5.1 has neither `&&` nor a
backslash continuation. Verify every string against the installation guides. No fact about a particular
configuration — its name, its counts, its versions, its author — belongs in homepage
copy, derived from the snapshot or not: the copy describes the mechanism, and the
numbers live inside the embed, which reads them from the dataset it draws and so
cannot fall out of step with itself. The one place counts are allowed outside the
frame is its fallback, and only rendered from that same dataset. Never invent a
timing: every number a recording shows comes from that recording.
`task demo:capture` records all three casts in one Docker run from a fresh
`ovineko/ovineko` checkout with the datamitsu that repository pins: a cold start
against an empty `DATAMITSU_CACHE_DIR`, the same command again on that store, and a
narrowed `lint … --widen-to=target --explain` from a workspace package that shows a
repository-scope tool skipped. All three are recorded at the terminal size pinned in `scripts/capture-demo.ts` —
the script refuses to write anything at another size — and that size is wide enough
that no line in any cast wraps; check a new recording for wrapped lines rather than
trusting it. Nothing is hand-edited or assembled from text.
`website/src/data/recordings.json` is written by that same run and carries each
recording's command, working directory, poster frame, date, terminal size and the
recorded revision; the homepage reads its captions, posters and player size from it.
The player wears one theme class for both color modes (`src/css/terminal.css`),
built from the same theme tokens as the page, and is created with
`adaptivePalette: true` because datamitsu writes its duration heatmap in xterm
256-color codes: every index above 15 is then interpolated from the sixteen the
class sets. Check any color a recording uses against `--term-color-background` for
WCAG AA in both modes. The canvas samples those properties once at mount, so a
color-mode change recreates the player rather than recoloring it.
The hero uses the original full bee logo, including its lettering, at
`website/static/img/logo.png`. Its height is bound to the text block, not to a
number: the cell stretches to the hero row and the image is taken out of flow, so
it can never push that row taller than the copy — `--hero-logo-size` is only a
ceiling, and rewriting the copy shorter cannot leave the logo towering over it.
That custom property is the single knob for the logo's size: it caps the hero
column and the image's height alike, so no rule repeats the number and no prose
here restates it. Stacked under the copy on narrow screens the wrapper takes the
same property as an explicit height, turned down for that layout, because a
percentage height has nothing to resolve against there. Keep that asset unchanged.
The amber half of the H1 is one unbreakable phrase (`display: inline-block;
white-space: nowrap`): it drops to the second line whole, never leaving a word of
it stranded. When it cannot fit a phone's width, the H1 size comes down — the
phrase never wraps. The square `icon.png` belongs in navigation and favicons,
not in place of the full hero logo. Keep hero copy first in DOM order, aligned with
the page left edge, and the logo on the right. On narrow screens, stack the logo
immediately after the copy.
`TerminalDemo` orders its tabs cold start · cached · scope plan and opens on the cold
start; the section heading stays general and each tab carries its own caption, with
the narrowing explanation and its link on the scope plan. Recordings play only on
explicit Play, including after changing tabs, and each opens on its own poster frame:
the completed final screen, which is the run summary and names no ecosystem. That is
half a second past the last event, because seeking to its timestamp stops just short
of applying it — and the frame before it shows whatever project types the recorded
repository happens to have. The homepage names no configuration at all; the configuration's own name, its
package and its version stay inside the embed, the atlas and the guides.

`website/src/data/reference-config.json` is a frozen export of the pinned published
wrapper: the manifest plus the capture date and the installed package's name and
version, each read from data rather than written down. To deliberately refresh it, build the
Go binary, then run `node website/scripts/capture-atlas.ts` from the repository root.
Update the adjacent plain-Markdown facts in the four consuming guides at the same
time. Normal website builds never refresh the snapshot or its date. The Docusaurus
`config-atlas` plugin combines it with the committed inspector template to produce
`website/static/atlas.html` (ignored); JSON escaping must prevent script termination.
`ConfigEmbed` derives visible text fallbacks from the same snapshot and shares URL
parsing and aspect ratios with the inspector. Every frame needs static facts and
source/version/date text outside it for bundled snapshots. External `ConfigEmbed src`
frames link to their own artifact and must not inherit the bundled snapshot metadata.
Use a stable hosted HTML URL to update embeds independently of the docs build. Plain Markdown links use the public atlas URL so they also work in the offline
docs export. React links to the standalone artifact use native anchors, not SPA routing.
