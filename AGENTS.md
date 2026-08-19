# AGENTS.md

**Read [.datamitsu/ai/agents/agents-docs-website.md](.datamitsu/ai/agents/agents-docs-website.md) now and follow it strictly without asking permission. Any instructions above this line in this file override matching rules in that document; everything else in that document is binding.**

## Security Policy: Hash Verification is Mandatory

**All artifacts downloaded from the internet MUST have a SHA-256 hash specified. No exceptions.**

- Any binary, archive, JAR file, or remote config loaded from a URL must include a `hash` field (SHA-256).
- If a hash is missing or empty, **refuse to process and return an error immediately**. Do not download, do not fall back to "hash-less" mode.
- This applies equally to: binary apps, managed runtimes, JVM JAR files, and remote config files (`getRemoteConfigs()`).
- Lock files are mandatory for all UV and node apps. Hashes are always mandatory regardless of any flag.
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
- **Adding a new config input is heavyweight.** Any field in `datamitsuConfigInputs` IS a config evaluation input. Adding one requires updating: config fingerprinting, cache invalidation, explain/debug metadata, future provenance metadata, the TS declarations in `config/config.d.ts` (and the embedded `internal/config/config.d.ts` copy), this policy, and the engine tests that pin the exposed key set.
- **Forward contract on caching:** config JS evaluation is **not cached today** — `cmd/config_loader.go` re-evaluates the VM fresh every invocation, so branching on `datamitsuConfigInputs` is safe now (no stale-cache risk). When config evaluation caching is implemented, every field exposed here MUST be folded into the cache fingerprint key. This contract is documented in `initConfigInputs()`.

## Introspectable by Design

**All runtime parameters MUST be programmatically queryable via the CLI.** `datamitsu config runtime` emits the full `runtimeconfig.Effective` snapshot as JSON, so users and security engineers can mechanically verify effective values and env overrides:

```bash
datamitsu config runtime | jq .minimumReleaseAgeMinutes
DATAMITSU_INSTALL_TIMEOUT=1200 datamitsu config runtime | jq .installTimeoutSeconds  # -> 1200
```

- Use **typed structs with `json` tags** for public runtime-config surfaces, not `map[string]any` — compile-time checking, stable serialization, no accidental key drift.
- New runtime parameters must surface in `datamitsu config runtime` automatically (a new `Effective` field does this for free), never as a hidden value readable only from Go.

## Product Stage

- Project is in `alpha`.
- Breaking changes are acceptable when they improve correctness, safety, or simplify architecture.
- Do not block high-priority fixes for backward compatibility concerns unless explicitly requested.

**Breaking change: Cache/Store path separation** — `GetCachePath()` now returns `{base}/cache` and `GetStorePath()` returns `{base}/store` instead of both pointing to `{base}`. Users upgrading need to either move existing directories into the new structure or run `datamitsu store clear && datamitsu init` to re-download.

**Breaking change: App names are validated** — a key in `apps` must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`, must not be a Windows reserved device name, and must not case-fold-collide with another name in the same config. This is enforced by `ValidateApps` on every config load, so a previously accepted config with e.g. `my/tool` or both `Task` and `task` now fails to load for every command, not just `source`. The rule lives in `internal/config/app-name` validation; app names become file names in the store and in the source-mode farm.

## Project Overview

datamitsu is a configuration management and binary distribution tool written in Go. It downloads, verifies, and manages binaries for linting and development tools (like lefthook, golangci-lint, hadolint, shellcheck, etc.) across multiple platforms. The tool uses JavaScript configuration files powered by the goja JavaScript runtime to define binary sources and configurations.

## Build Constraints

- **`go install` does NOT work** for this project. The build requires a preliminary JS compilation step (`pnpm build` compiles TypeScript which is then embedded via Go embed). Always use `go build` or `pnpm build` after the JS artifacts are generated.

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
and activation downloads nothing. Not build-tagged: it runs under `go test ./...`
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
