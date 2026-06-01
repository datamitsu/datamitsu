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

## Product Stage

- Project is in `alpha`.
- Breaking changes are acceptable when they improve correctness, safety, or simplify architecture.
- Do not block high-priority fixes for backward compatibility concerns unless explicitly requested.

**Breaking change: Cache/Store path separation** — `GetCachePath()` now returns `{base}/cache` and `GetStorePath()` returns `{base}/store` instead of both pointing to `{base}`. Users upgrading need to either move existing directories into the new structure or run `datamitsu store clear && datamitsu init` to re-download.

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

Currently no test commands are configured in package.json. Use standard Go testing:

```bash
go test ./...
```

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
