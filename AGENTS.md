# AGENTS.md

**Read [.datamitsu/agents-docs-website.md](.datamitsu/agents-docs-website.md) now and follow it strictly without asking permission. Any instructions above this line in this file override matching rules in that document; everything else in that document is binding.**

## Security Policy: Hash Verification is Mandatory

**All artifacts downloaded from the internet MUST have a SHA-256 hash specified. No exceptions.**

- Any binary, archive, JAR file, or remote config loaded from a URL must include a `hash` field (SHA-256).
- If a hash is missing or empty, **refuse to process and return an error immediately**. Do not download, do not fall back to "hash-less" mode.
- This applies equally to: binary apps, managed runtimes, JVM JAR files, and remote config files (`getRemoteConfigs()`).
- Lock files are mandatory for all UV and FNM apps. Hashes are always mandatory regardless of any flag.
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

## Product Stage

- Project is in `alpha`.
- Breaking changes are acceptable when they improve correctness, safety, or simplify architecture.
- Do not block high-priority fixes for backward compatibility concerns unless explicitly requested.

**Breaking change: Cache/Store path separation** — `GetCachePath()` now returns `{base}/cache` and `GetStorePath()` returns `{base}/store` instead of both pointing to `{base}`. Users upgrading need to either move existing directories into the new structure or run `datamitsu store clear && datamitsu init` to re-download.

## Migrated From CLAUDE.md

# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

See also: [README.md](README.md)

## Project Overview

datamitsu is a configuration management and binary distribution tool written in Go. It downloads, verifies, and manages binaries for linting and development tools (like lefthook, golangci-lint, hadolint, shellcheck, etc.) across multiple platforms. The tool uses JavaScript configuration files powered by the goja JavaScript runtime to define binary sources and configurations.

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

### Core Components

**JavaScript Configuration Engine** ([internal/engine/](internal/engine/))

- Uses goja JavaScript runtime to evaluate configuration files
- Configuration defined in [internal/config/config.js](internal/config/config.js)
- Exposes console API, format utilities (YAML, TOML, INI), and tools API to JS runtime
- Allows dynamic configuration through JavaScript with type definitions in config.d.ts
- Every config file must export `getMinVersion()` returning a semver string; the loader validates the current datamitsu version meets this minimum before proceeding

**Target Resolution** ([internal/target/](internal/target/))

- Three-dimensional target model: OS + Arch + Libc (glibc/musl/unknown)
- `Target` struct with `OS`, `Arch`, `Libc` fields; canonical format `os/arch/libc` via `String()`
- `DetectHost()` returns current system target; calls `DetectLibc()` on Linux, `LibcUnknown` on other OS
- Multi-stage libc detection (`libc_detection.go`): ldd --version parsing → ELF PT_INTERP reading → loader path globbing → fallback to "unknown"
- `Resolver` scores candidates: OS match +1000, Arch match +100, Libc exact +10, Libc unknown +5, Libc mismatch +1; score 0 if OS or Arch doesn't match
- `ResolvedTarget` tracks resolution source (`ResolutionExact` or `ResolutionFallback`) with `FallbackInfo` (requested target + reason string)
- `Candidate` struct pairs a `Target` with arbitrary binary info for resolution
- Deterministic tiebreaking: alphabetical sort by `Target.String()` when scores are equal
- `DiagnosticInfo` struct captures full resolution chain (host → requested → resolved → cache path) for debugging
- Key files: `target.go`, `resolver.go`, `libc_detection.go`, `detection.go`, `diagnostics.go`

**Binary Manager** ([internal/binmanager/](internal/binmanager/))

- Downloads binaries from URLs with hash verification (SHA256)
- Supports multiple archive formats: tar.gz, tar.xz, tar.bz2, tar.zst, tar, zip, gz, bz2, xz, zst, and raw binaries
- `ExtractDir` mode: when `BinaryOsArchInfo.ExtractDir` is true, extracts entire archive to a directory (used by JVM runtimes for full JDK trees) instead of a single binary file
- Platform-specific binaries for darwin/linux/freebsd/openbsd/windows across amd64/arm64/aarch64
- **Target-aware resolution**: Uses `target.Resolver` to select best binary candidate from nested storage (os → arch → libc → BinaryInfo); `parseBinaryCandidates()` converts nested map to `[]target.Candidate`; `getBinaryInfo()` calls `resolver.Resolve()` and emits fallback warnings
- Caches binaries in `{store}/.bin/{name}/{configHash}` with lazy loading; config hash includes resolved target (OS, Arch, Libc) for cache isolation between glibc and musl variants
- Can execute binaries through `Exec()` method with env passthrough
- `GetExecCmd(name, args)` returns a prepared `*exec.Cmd` without executing it. Returns `(nil, nil)` for shell apps. Used by `devtools verify-all` for version checks
- `App.VersionCheck *AppVersionCheck`: optional per-app version check configuration. `Disabled: true` skips version check; `Args` overrides default `["--version"]`. Used by `devtools verify-all`
- `verify.go`: Public verification utilities (`VerifyBinaryExtraction`, `DownloadFileForVerify`, `VerifyFileHashPublic`) used by `devtools verify-all` for cross-platform hash verification without touching the cache
- Supports concurrent downloads with configurable concurrency (see `InstallWithConcurrency()`)
- Shows progress bars during downloads using github.com/schollz/progressbar

**App Types**
Five types of applications are supported:

1. `binary` - Self-managed binaries with URLs, hashes, and archive formats
2. `uv` - Python packages via managed UV runtime (e.g., yamllint)
3. `fnm` - npm packages via FNM-managed Node.js + PNPM (e.g., @mermaid-js/mermaid-cli, slidev, spectral)
4. `jvm` - JVM applications via managed JDK runtime (e.g., openapi-generator-cli). Downloads JAR files with hash verification and executes via `java -jar`
5. `shell` - Shell commands with custom env

**Runtime Manager** ([internal/runtimemanager/](internal/runtimemanager/))

- Manages runtime binaries (UV, FNM, JVM) with hash verification and caching
- Supports `managed` mode (download runtime binary) and `system` mode (use system-installed)
- Automatic musl fallback: `resolveEffectiveRuntimeConfig()` detects when host is musl and managed config lacks a musl binary; if the system binary (`fnm`, `uv`, or `java`) is found via `lookPathFunc`, automatically overrides to system mode. Called by `GetRuntimePath`, `InstallRuntimes`, `ResolveRuntime`, and `GetAppPath`. `lookPathFunc` field on `RuntimeManager` enables test injection
- Creates isolated per-app environments: `.apps/{runtime}/{app}/{hash}/`
- Runtime resolution: app-level override -> global default by kind
- Uses `RuntimeAppManager` interface to avoid circular dependency with BinManager
- Cache keys use XXH3-128 hash of runtime config + app config + OS + arch
- `RuntimeConfigFNM` (NodeVersion, PNPMVersion), `RuntimeConfigUV` (PythonVersion), and `RuntimeConfigJVM` (JavaVersion) on `RuntimeConfig` hold version info per runtime kind
- Lockfiles are mandatory for all UV/FNM apps; `ValidateApps` enforces that all UV/FNM apps have a `LockFile` configured
- FNM runtime: downloads FNM binary, uses it to install Node.js versions, downloads PNPM from npm registry, runs pnpm install (with `--silent`) in isolated app environments
- UV runtime: uses project-based workflow (writes pyproject.toml + `uv sync`) instead of `uv tool install`; supports optional `--python {pythonVersion}` via `RuntimeConfigUV.PythonVersion`; `requires-python` in pyproject.toml resolved from app `RequiresPython` -> fallback `>=3.12` (decoupled from pythonVersion)
- JVM runtime: downloads Temurin JDK archives per platform with `ExtractDir: true`, extracts full JDK tree to `.runtimes/jvm/{hash}/`; apps download JAR files with SHA256 verification to `.apps/jvm/{app}/{hash}/`; executes via `java -jar`
- `RuntimeConfigSystem.SystemVersion`: optional string for manual cache invalidation when using system-mode runtimes; included in system runtime hash calculation
- Node.js versions cached at `.runtimes/fnm-nodes/v{version}/`, PNPM versions at `.runtimes/fnm-pnpm/{version}/{pnpmHash}/`
- Shared PNPM content-addressable store at `.pnpm-store/` for deduplication
- Lock file compression: `lockfileenc.go` provides `CompressLockFile` (brotli level 11 + base64 with `br:` prefix) and `DecompressLockFile` for lock file storage in config
- Key files: `runtimemanager.go`, `hash.go`, `uv.go`, `fnm.go`, `jvm.go`, `lockfileenc.go`, `archiveenc.go`

**Managed Config Files** ([internal/managedconfig/](internal/managedconfig/))

- Distributes configuration files from runtime-managed apps (FNM/UV) to monorepo projects via symlinks in `.datamitsu/` at git root
- Apps declare `Files map[string]string` (filename -> static content) and `Links map[string]string` (linkName -> relativePath) on the `App` struct
- Links map link names to relative paths within the app's install directory (e.g., `"eslint-config": "dist/eslint.config.js"`)
- Links do not require `required: true` — apps are installed during init based on tool usage scanning (smart init)
- `CreateDatamitsuLinks(gitRoot, apps, resolver, bundles, bundleResolver, dryRun)` removes and recreates `.datamitsu/` atomically, creating symlinks from `.datamitsu/{linkName}` to `{installRoot}/{relativePath}` for both apps and bundles
- Path traversal protection via `validateLinkPath()`: rejects absolute paths, parent traversal (`..`), and symlink escapes outside install directory
- Uses `InstallRootResolver` interface (implemented by BinManager) to get install paths without circular dependencies
- `WriteAppFiles(installPath, files, archives)` writes archives (alphabetically) then file content to install directories before package managers run. Files take precedence over archives for overlapping paths
- `ComputeInstallPath(appName)` computes install path by hash; `GetInstallRoot(appName)` verifies it exists
- Validation via `ValidateApps()` in `internal/config/validate.go`: checks link paths are safe relative paths (no traversal), ensures linkName uniqueness across apps
- Symlink targets can be files or directories (directory symlinks supported for bundles linking entire directories)
- All symlinks use relative paths for portability

**Archive Support**

Apps can bundle full directory trees via the `Archives map[string]*ArchiveSpec` field on `App`:

- **Inline archives**: Brotli-compressed tar archives encoded as `tar.br:{base64}` (up to 50 MiB decompressed). Uses same brotli level 11 + base64 pattern as lock file compression
- **External archives**: Downloaded from URLs with mandatory SHA-256 hash verification (formats: tar, tar.gz, tar.xz, tar.bz2, tar.zst)
- **Extraction order**: Archives extracted first (sorted alphabetically by name — later archives overwrite earlier ones for overlapping paths), then Files written (Files always overwrite archive contents). Runtime package manager runs after both (pnpm install / uv sync)
- **Symlinking**: Any relative path within the install directory can be referenced in `Links` (e.g., `links: {"my-config": "dist/eslint.config.js"}`)
- **Restriction**: Archives only supported on UV/FNM apps (same constraint as Files/Links)
- **Validation**: Enforced in `ValidateApps()` - inline must have `tar.br:` prefix and valid content; external requires URL, SHA-256 hash (64 lowercase hex), and format
- **Security**: Path traversal protection via `validateArchivePath`, symlink escape prevention, per-file and total size limits (2 GiB total)
- **Cache invalidation**: Archive content (inline data or url+hash+format) included in XXH3-128 cache key via `hashFilesAndArchives()`
- Key files: `archiveenc.go` (compress/decompress), `extract.go` (`extractArchiveToPath`), `binmanager.go` (`WriteAppFiles`, `extractArchives`)

**Bundles** ([internal/binmanager/bundle.go](internal/binmanager/bundle.go))

- Bundles are a top-level Config concept (separate from apps) for managing non-executable content (files/archives) with symlinks
- `Bundle` struct: `Version string`, `Files map[string]string`, `Archives map[string]*ArchiveSpec`, `Links map[string]string`
- `Config.Bundles map[string]*Bundle` added to Config; `BinManager.mapOfBundles` initialized in `New()`
- Hash: XXH3-128 of `name\0version\0HashFilesAndArchives(files, archives)` — links are not part of the hash (they are metadata for symlink creation)
- Install path: `{store}/.bundles/{name}/{hash}/`
- `installBundle()` uses `WriteAppFiles()` to write files/archives; cleanup on failure via `os.RemoveAll`
- `InstallBundles(ctx, skipExternalArchives)` installs all bundles; inline-only bundles always install, external archives respect `--skip-download`
- `HasExternalArchives()` checks if any archive in the bundle has a URL
- Validation via `ValidateBundles()` in `internal/config/validate.go`: bundles must have files or archives, file keys validated for path traversal, link names unique across both apps and bundles
- `ComputeBundlePath(name)` and `GetBundleRoot(name)` for path computation and existence checks
- Key files: `bundle.go`, `hash.go` (`calculateBundleHash`, `HashFilesAndArchives`)

**Shared Storage**

- `Config.SharedStorage map[string]string` — arbitrary key-value storage that flows through the config chain as ordinary JS input
- Any config layer can read/write via `input.sharedStorage`; standard JS spread pattern: `{ ...input, sharedStorage: { ...input.sharedStorage, "key": "value" } }`
- No Go-level merge semantics — relies on JS to spread correctly (a config that replaces `sharedStorage` without spreading will drop previous keys)

**Config Links JS API** ([internal/engine/tools/config.go](internal/engine/tools/config.go))

- Registers `tools.Config.linkPath(appName, linkName, fromPath)` in the goja JS VM
- Returns relative path from `fromPath` to `.datamitsu/{linkName}` for use in generated config files
- Validates linkName exists and is owned by the specified appName

**Path JS API** ([internal/engine/tools/path.go](internal/engine/tools/path.go))

- `tools.Path.join(...paths)`: joins path segments using OS separator
- `tools.Path.abs(path)`: returns absolute path
- `tools.Path.rel(targetPath, basePath?)`: returns relative path; defaults basePath to rootPath (git root or cwd if not in git)
- `tools.Path.forImport(path)`: converts a relative path to ES module import-compatible format by ensuring it starts with `./` or `../`; panics on absolute paths; idempotent

**Template Placeholder Expansion**

Tool operation arguments support template placeholders that the executor resolves before execution. Implemented in `replacePlaceholders()` in `internal/tooling/executor.go`.

| Placeholder   | Resolves To                                | Use Case                    |
| ------------- | ------------------------------------------ | --------------------------- |
| `{file}`      | Single file path (per-file scope)          | `"{file}"`                  |
| `{files}`     | Separate arguments per file                | `"{files}"`                 |
| `{root}`      | Git repository root (or cwd if not in git) | `"{root}/.config"`          |
| `{cwd}`       | Per-project working directory              | `"{cwd}/src"`               |
| `{toolCache}` | Per-project, per-tool cache directory      | `"{toolCache}/tsbuildinfo"` |

- `{file}` and `{files}` when used as entire argument expand to separate args; when embedded in a string, they are replaced inline
- `{toolCache}` resolves to `~/.cache/datamitsu/cache/projects/{xxh3_128(gitRoot)}/cache/{relativeProjectPath}/{toolName}/`; computed per task from `task.ProjectPath` and `task.ToolName`; when computation fails, the literal `{toolCache}` is preserved
- `{cwd}` falls back to `rootPath` when `projectPath` is empty
- Facts struct exposes platform/environment info (`os`, `arch`, `libc`, `isInGitRepo`, `isMonorepo`, `env`) via `facts()` in JS; `libc` is "glibc", "musl", or "unknown" (Linux-only detection); path fields were removed in favor of template placeholders

**Datamitsu Ignore** ([internal/datamitsuignore/](internal/datamitsuignore/))

- `.datamitsuignore` files disable specific tools for matching file patterns
- `Parse(content)` parses rules: each line is `<glob>: <tool1>, <tool2>` or `!<glob>: <tool1>, <tool2>` (inversion)
- `ParseRules(lines []string)` parses a slice of rule strings (used for config-defined ignore rules without file I/O)
- Wildcard `*` as tool name disables all tools for matching files
- `Matcher` collects rules per directory, exposes `IsDisabled(toolName, relFilePath)` and `IsProjectDisabled(toolName, relProjectDir)` - rules applied root-to-file-dir order
- Integrated into Planner: per-file tasks check `ignoreMatcher.IsDisabled()`, per-project tasks check `ignoreMatcher.IsProjectDisabled()`
- Config-defined ignore rules (`Config.IgnoreRules`) are passed to `NewPlanner` and merged with file-based rules at root level

**Bundled Linters/Fixers** ([internal/bundled/](internal/bundled/))

- Built-in lint and fix operations for datamitsu-owned file formats, run as part of `RunSequential` before user-configured tools
- `.datamitsuignore` linter/fixer (`datamitsuignore.go`):
  - `FindIgnoreFiles(rootPath)` walks the directory tree (skipping `.git/`, `node_modules/`, `vendor/`, and other heavy directories) and returns all `.datamitsuignore` file paths
  - `RunFix(rootPath)` normalizes formatting to canonical form `{!}{glob}: {tool1}, {tool2}`. Writes atomically (temp file + rename) only if content changed. Parse errors cause immediate failure
  - `RunLint(rootPath, tools)` validates all `.datamitsuignore` files: parse errors cause immediate failure; formatting deviations and unknown tool names produce yellow warnings on stderr but do not fail
- In `RunSequential`: `RunFix` runs when operations include `fix` (not in `--explain` mode), errors logged as warnings; `RunLint` runs always (printing warnings for unknown tools/formatting), errors halt execution only when operations include `lint`
- Key files: `datamitsuignore.go`

**Remote Config** ([internal/remotecfg/](internal/remotecfg/))

- Fetches, caches, and resolves remote configuration files declared via `getRemoteConfigs()` in JS configs
- `FetchRemoteConfig(url, expectedHash)`: HTTP GET with 30s timeout, SHA-256 hash verification, 10 MiB size limit, HTTPS-to-HTTP redirect rejection
- `CachedConfigPath(cacheDir, url)`: returns `{cacheDir}/.remote-configs/{xxh3_128(url)}.ts`
- `LoadCached(path)`: reads cached file content
- `SaveCached(path, content)`: atomic write (temp file + rename)
- `Resolve(url, expectedHash, cacheDir)`: orchestrates cache lookup and fetching — cache hit when hash matches (no TTL), cache miss triggers fetch + verify + save
- Hash is always required — missing or empty hash is an immediate error per security policy
- Key files: `fetch.go`, `cache.go`, `resolver.go`

**Registry Clients** ([internal/registry/](internal/registry/))

- `npm.go`: Fetches latest package version and description from npm registry (registry.npmjs.org)
- `pypi.go`: Fetches latest package version and description from PyPI (pypi.org)
- `nodejs.go`: Fetches latest Node.js LTS version from endoflife.date API; falls back to hardcoded version on failure
- `python.go`: Fetches latest stable Python version from endoflife.date API; filters out EOL releases; falls back to hardcoded version on failure
- `temurin.go`: Fetches latest Temurin JDK major version from Adoptium API (`api.adoptium.net`); falls back to hardcoded version on failure
- Used by devtools pull-fnm, pull-uv, and pull-runtimes commands

**Verify Cache** ([internal/verifycache/](internal/verifycache/))

- Incremental result cache for `devtools verify-all`
- State file stored at `{cache}/.verify-state/{xxh3_128(cwd)}.json`, keyed by CWD
- `StateManager` provides thread-safe `ShouldSkip(key, fingerprint)` and `Record(key, fingerprint, status, errMsg)` with `sync.RWMutex`; writes state to disk after each `Record` call (atomic temp file + rename)
- `LoadState` / `SaveState`: JSON persistence with sorted entry keys for deterministic output
- `StatePath(cacheDir, cwd)`: computes state file path using XXH3-128 of CWD
- Fingerprinting via XXH3-128 over `\0`-separated fields: `FingerprintBinary`, `FingerprintRuntime`, `FingerprintRuntimeApp`, `FingerprintVersionCheck`
- Entry key helpers: `BinaryEntryKey`, `RuntimeEntryKey`, `RuntimeAppEntryKey`, `VersionCheckEntryKey` (format: `{type}:{name}:{os}:{arch}`)
- Key files: `cache.go`, `fingerprint.go`

**Asset Detector** ([internal/detector/](internal/detector/))

- Scoring-based system for detecting OS, Arch, and Libc from release asset filenames
- Three separate pattern layers: `patterns_os.go` (OS-only), `patterns_arch.go` (Arch-only), `patterns_libc.go` (Libc-only)
- `ScoreAsset()` evaluates a filename against all pattern layers independently (no cross-contamination between dimensions)
- `LibcPatterns`: detects musl (matches "musl", "alpine") and glibc (matches "gnu", "glibc") from filenames
- Used by `devtools pull-github` to match release assets to target tuples
- Key files: `patterns_os.go`, `patterns_arch.go`, `patterns_libc.go`, `scoring.go`

**File Traverser** ([internal/traverser/](internal/traverser/))

- Walks directory trees respecting .gitignore rules
- Finds git repository root and collects gitignore patterns
- Custom gitignore matcher implementation in [internal/traverser/git.go](internal/traverser/git.go)

**Programmatic API** ([programmable-api/js/](programmable-api/js/))

- TypeScript source in `programmable-api/js/src/`, compiled via tsdown to `programmable-api/js/dist/`
- `dist/` copied to `packaging/npm/datamitsu/lib/` for npm publishing (`lib/` is gitignored, generated output only)
- Provides `@datamitsu/datamitsu` npm package exports: `fix`, `lint`, `check`, `exec`, `cache`, `version`
- Workspace member in `pnpm-workspace.yaml` (`@datamitsu/programmable-api-js`)
- Build flow: `pnpm build` (tsdown) -> `pnpm copy-to-package` (copies dist to `packaging/npm/datamitsu/lib/`)
- Tests: `node --experimental-test-module-mocks --import tsx --test src/**/*.test.ts`
- Key files: `index.ts` (entry point with named + default exports), `spawn.ts` (process spawning via tinyexec), `tool-command.ts` (shared command factory for fix/lint), `json.ts` (JSON extraction from CLI output), `types.ts` (shared interfaces: PlanJSON, SpawnRaw)
- `check.ts` handles multi-JSON extraction (fix+lint plans) separately from the shared factory
- Imports `getExePath` from `@datamitsu/datamitsu/get-exe.js` to locate the Go binary

**CLI Commands** ([cmd/](cmd/))

- Built with cobra framework
- `exec` - Execute managed binaries (see [cmd/exec.go](cmd/exec.go))
- `setup` - Setup configuration files for detected project types (see [cmd/setup.go](cmd/setup.go))
  - Uses `ConfigInit.Scope` field: `scope: "git-root"` configs run exactly once at git root; `scope: "project"` (default) configs run per detected project
- `check` - Runs fix then lint in a single process with shared context (see [cmd/check.go](cmd/check.go))
  - Supports `--explain`, `--file-scoped`, `--tools` flags (same as fix/lint)
  - Fails on fix error without continuing to lint
  - Uses `runner.RunSequential()` for shared initialization and context reuse
- `init` - Downloads required binaries and runs initialization commands (see [cmd/init.go](cmd/init.go))
  - Must be run from git root (guard in `checkInitGitRoot()`)
  - Smart init: scans tool definitions to find referenced apps, installs only those with Links defined (via `scanReferencedApps()`)
  - Installs bundles before creating symlinks; inline-only bundles install regardless of `--skip-download`
  - After downloads, creates `.datamitsu/` symlinks for both apps and bundles via `managedconfig.CreateDatamitsuLinks()`
  - Supports `--all` flag to download all binaries (both required and optional)
  - Supports `--skip-download` flag to skip binary downloads
  - Supports `--fail-on-download-error` flag to stop on download failures
  - Supports `--dry-run` flag to preview actions without making changes
  - Concurrency controlled via `DATAMITSU_CONCURRENCY` env var (default: 3)
- `config` - Parent command for configuration management subcommands (see [cmd/config.go](cmd/config.go))
  - `config lockfile [appName]` - Generates lock file content for FNM/UV apps (see [cmd/config_lockfile.go](cmd/config_lockfile.go))
    - Without args: lists available FNM/UV apps grouped by kind
    - With appName: deletes app cache, reinstalls, reads generated lock file (pnpm-lock.yaml or uv.lock)
    - Output is brotli-compressed + base64-encoded (br: prefix) for config embedding
    - Errors for binary/shell/jvm apps (no dependency manifest)
- `devtools` - Developer utility commands for config maintenance (see [cmd/devtools.go](cmd/devtools.go))
  - `devtools pull-github <file>` - Pull latest GitHub release versions for binary apps; requires file argument (path to githubApps.json); fetches repository description from GitHub API; supports `--update`, `--verify-extraction`; creates empty appstate if file doesn't exist; works directly with JSON file (no config loading); uses os/arch/libc target tuples for platform iteration; creates nested storage structure (os → arch → libc → BinaryInfo)
  - `devtools pull-fnm <file>` - Pull latest npm package versions for FNM apps (see [cmd/devtools_fnm.go](cmd/devtools_fnm.go)); requires file argument (path to fnmApps.json); always fetches descriptions from npm registry; supports `--dry-run`, `--update`; works directly with JSON file (no config loading); creates empty `{}` if file doesn't exist
  - `devtools pull-uv <file>` - Pull latest PyPI package versions for UV apps (see [cmd/devtools_uv.go](cmd/devtools_uv.go)); requires file argument (path to uvApps.json); always fetches descriptions from PyPI registry; supports `--dry-run`, `--update`; works directly with JSON file (no config loading); creates empty `{}` if file doesn't exist
  - `devtools pull-runtimes <file>` - Pull runtime configurations (FNM, UV, JVM) from upstream releases (see [cmd/devtools_pull_runtimes.go](cmd/devtools_pull_runtimes.go)); requires file argument (path to runtimes.json); fetches latest releases from GitHub, computes SHA-256 hashes, writes to `<file>`; supports `--update` (required), `--dry-run`, `--runtime <name>` (fnm, uv, or jvm); version sources: Node.js LTS from endoflife.date, PNPM from npm registry, Python stable from endoflife.date, Java from Adoptium API; deduplicates musl binaries identical to glibc
  - `devtools pack-inline-archive <dir>` - Pack a directory into an inline archive (`tar.br:` format) for use in config. Warns at 10 MiB, errors at 50 MiB decompressed size. Output to stdout
  - `devtools apps list` - List all configured apps with type, version, description, and install status
  - `devtools apps inspect <name>` - Show install path and file tree for an app (collapses node_modules/.venv)
  - `devtools apps path <name>` - Print install directory path for an app
  - `devtools bundles list` - List all configured bundles with name, version, and install status
  - `devtools bundles inspect <name>` - Show install path and file tree for a bundle
  - `devtools bundles path <name>` - Print install directory path for a bundle
  - `devtools verify-all` - Cross-platform config integrity checker (see [cmd/devtools_verify.go](cmd/devtools_verify.go)); downloads and hash-verifies binary apps and managed runtimes for all configured platforms, installs runtime-managed apps and bundles on current platform, optionally runs version checks; supports `--no-version-check`, `--concurrency`, `--json`, `--skip-passed`, `--no-remote`; results persisted incrementally to a state file in `{cache}/.verify-state/`; `--skip-passed` skips entries whose config fingerprint is unchanged and last status was "ok", showing them as "cached"; `--no-remote` skips remote config resolution
- `cache` - Manage per-project caches (see [cmd/cache.go](cmd/cache.go))
  - `cache clear` - Clears the current project's cache (lint/fix results + tool caches). By default clears only the current project
    - Supports `--all` flag to clear caches for all projects
    - Supports `--dry-run` flag to preview what would be deleted without deleting
  - `cache path` - Prints the absolute path to the global cache directory
  - `cache path project` - Prints the absolute path to the current project's cache directory
- `store` - Manage the global binary and runtime store (see [cmd/store.go](cmd/store.go))
  - `store path` - Prints the absolute path to the global store directory (`env.GetStorePath()`)
  - `store clear` - Removes the entire store directory via `os.RemoveAll`; includes all binaries, runtimes, apps, and remote configs. Refuses dangerous paths (`/`, `$HOME`). No confirmation prompt

### Config Loading Order

Configuration is loaded in layers, each receiving the previous result as input:

```text
default (embedded config.js)
  ↓ [getRemoteConfigs() resolved depth-first, if exported]
--before-config flags  (for wrappers/libraries)
  ↓ [getRemoteConfigs() resolved depth-first]
auto (datamitsu.config.js, datamitsu.config.mjs or datamitsu.config.ts at git root)
  ↓ [getRemoteConfigs() resolved depth-first]
--config flags  (for CI/testing overrides)
  ↓
final Config
```

- `--before-config`: config files loaded before auto-discovery, intended for wrapper packages and shared libraries
- `--no-auto-config`: disables auto-discovery of `datamitsu.config.js`/`datamitsu.config.mjs`/`datamitsu.config.ts` at git root
- Auto-discovery: finds `datamitsu.config.js`, `datamitsu.config.mjs` or `datamitsu.config.ts` at git root; if more than one exist → error; if none → no auto-config
- Each source must export `getMinVersion()` returning a semver string; version is validated against `ldflags.Version` via `version.CompareVersions()` before `getConfig()` is called
- Each source can export `getRemoteConfigs()` returning `Array<{url: string, hash: string}>` for recursive parent resolution
- `IgnoreRules` use append semantics across config layers (previous rules prepended to new)
- Circular remote config dependencies are detected and produce an error
- `--no-remote` flag on `devtools verify-all` skips remote config resolution
- `loadConfig()` returns a 4-tuple: `(*config.Config, *config.InitLayerMap, *goja.Runtime, error)`

**Eager Content Evaluation** (`internal/config/init_eval.go`, `internal/config/init_layer.go`):

- `content()` functions in Init entries are evaluated eagerly during config loading, not during setup
- `InitLayerMap` tracks the history of each Init entry across all config layers
- `InitLayerHistory` stores ordered `InitLayerEntry` items (LayerName, GeneratedContent) and the final `ConfigInit` metadata
- `EvaluateInitContent()` calls each layer's `content()` functions, passing the previous layer's output as `context.existingContent`
- `MergeInitLayers()` records evaluated content into the layer map after each config source is processed
- Evaluation is best-effort: entries whose `content()` throws are silently skipped; the installer falls back to disk-based generation for those entries
- `context.existingContent` contains the previous layer's generated content (not disk content); `context.originalContent` contains unmodified disk content (available during both eager evaluation and setup)
- The installer checks `InitLayerMap` first; if history exists for a file, it uses `GetLastGeneratedContent()` instead of calling `content()` again

### Key Data Flow

1. Configuration JS file is loaded and executed in goja VM
2. `getConfig()` function returns a Config struct with apps and runtimes definitions
3. BinManager is initialized with MapOfApps; RuntimeManager is initialized with MapOfRuntimes
4. When `exec <appName>` is called:
   - BinManager delegates to RuntimeManager for uv/fnm/jvm apps
   - For binary apps: checks cache, downloads/verifies hash, extracts, executes
   - For uv apps: resolves UV runtime, downloads if needed, installs app in isolated env, executes
   - For fnm apps: resolves FNM runtime, installs Node.js via FNM, downloads PNPM from npm registry, installs app dependencies, executes via node
   - For jvm apps: resolves JVM runtime (Temurin JDK), downloads JAR with hash verification, executes via `java -jar`

### Configuration Structure

The [internal/config/config.js](internal/config/config.js) file defines:

- `mapOfRuntimes`: Runtime definitions (UV, FNM, JVM) with managed binary URLs and hashes per platform
- `mapOfApps`: All available binaries/tools with URLs, hashes, and platform support
- `ignoreGroups`: Categorized ignore patterns (Dependencies, Build outputs, Cache, Testing, Logs, Environment, Security, IDE & OS, Golang specific)
- `init`: Configuration for initializing tool configs like lefthook.yml
- `ignoreRules`: Optional `string[]` of `.datamitsuignore`-syntax rules applied alongside file-based rules; merged via append across config layers

Tools API exposed to JS includes ignore pattern parsing/stringifying utilities (`tools.Ignores`), `tools.Config.linkPath()` for computing relative paths to `.datamitsu/` symlinks, and `tools.Path` for path manipulation.

**Version Requirement** (`getMinVersion()`):

Every config file must export a `getMinVersion()` function that returns a semver string specifying the minimum datamitsu version required. The config loader calls this function before `getConfig()` and fails immediately if the current version is too old. Example:

```javascript
export function getMinVersion() {
  return "1.2.0";
}
```

- `getMinVersion()` is mandatory — configs without it fail to load with: "config must export getMinVersion() function returning semver string"
- Version must be valid semver (e.g., "1.2.3" or "v1.2.3")
- When the current version is below the requirement, error includes both versions and upgrade instructions
- The special version "dev" (used when running from source) is treated as v0.0.0, so it passes version checks only when the required version is also v0.0.0 or lower
- Each config layer (default, before-config, auto, explicit) is checked independently; failure is fail-fast

## Important Implementation Details

### Binary Hash Verification

- All binaries must have SHA256 hashes defined
- Hash verification happens during download before extraction
- Config hash is calculated from binary info + resolved target (OS + Arch + Libc) to determine cache path
- Different resolved targets (e.g., glibc vs musl) produce different cache paths for isolation

### Platform Support

- OS types: darwin, linux, freebsd, openbsd, windows
- Arch types: amd64, arm64, aarch64
- Libc types: glibc, musl, unknown (Linux-only dimension; non-Linux always "unknown")
- Platform detection uses Go's runtime.GOOS and runtime.GOARCH; libc detected via `target.DetectLibc()` on Linux
- Mapped through [internal/syslist/](internal/syslist/) for OS/Arch; [internal/target/](internal/target/) for full target resolution
- Binary storage uses nested structure: `os → arch → libc → BinaryOsArchInfo` (mandatory libc level for all platforms)

### Monorepo Support

**Datamitsu is designed to run in monorepos with multiple independent projects.**

Key characteristics:

- **Project detection**: `project.Detector` ([internal/project/](internal/project/)) scans for project markers (`package.json`, `go.mod`, `Cargo.toml`, `pyproject.toml`, etc.) throughout the repository tree
- **Per-project execution**: Tools run separately for each detected project with isolated working directories set via `Task.ProjectPath`
- **Cache isolation**: Each project gets its own cache namespace: `~/.cache/datamitsu/cache/projects/{xxh3_128(gitRoot)}/cache/{relativeProjectPath}/{toolName}/`
- **Repository-scope tasks**: Some tools (e.g., `golangci-lint` with `scope: repository`) run once from git root with `ProjectPath = ""`
- **CWD-subtree restriction**: When running from subdirectory, only projects within that subtree are processed (see Planner CWD-Subtree Restriction)

**Example monorepo structure:**

```
repo/
├── packages/
│   ├── frontend/  (TypeScript project - has package.json)
│   ├── backend/   (Go project - has go.mod)
│   └── shared/    (TypeScript library - has package.json)
└── services/
    ├── api/       (Go project - has go.mod)
    └── worker/    (Python project - has pyproject.toml)
```

**Tool execution in monorepos:**

- `tsc` (TypeScript compiler) runs 3 times: once each in `packages/frontend/`, `packages/backend/` (if it has TS), and `packages/shared/`
- Each execution gets isolated cache: `cache/packages/frontend/tsc/`, `cache/packages/backend/tsc/`, `cache/packages/shared/tsc/`
- `golangci-lint` with `scope: repository` runs once from repo root: cache at `cache/golangci-lint/`
- No cache conflicts between projects or tools

**Cache structure example:**

```
~/.cache/datamitsu/cache/projects/{hash}/cache/
├── packages/
│   ├── frontend/
│   │   ├── tsc/tsbuildinfo
│   │   └── eslint/.eslintcache
│   ├── backend/
│   │   └── golangci-lint/cache.json
│   └── shared/
│       ├── tsc/tsbuildinfo
│       └── prettier/.prettiercache
└── services/
    ├── api/
    │   └── golangci-lint/cache.json
    └── worker/
        └── ruff/.ruff_cache/
```

**Architectural principle**: **Isolation by project is a first-class requirement.** All features must respect project boundaries in monorepos. This applies to:

- Cache paths (per-project, per-tool isolation)
- Working directories (each task runs in its project root)
- File traversal (respects project boundaries for per-project scope)
- Configuration (project-specific tool configs via managed config links)

### Logging

Uses uber-go/zap structured logging throughout. Logger initialization in [internal/logger/](internal/logger/).

### Environment Handling

- **Cache/Store separation**: `getBasePath()` resolves the base directory (`DATAMITSU_CACHE_DIR` → `XDG_CACHE_HOME` → `~/.cache/datamitsu`). `GetCachePath()` returns `{base}/cache` (project execution state). `GetStorePath()` returns `{base}/store` (downloaded binaries, runtimes, apps)
- Cache path and bin path utilities in [internal/env/](internal/env/)
- Runtime path helpers in [internal/env/runtime.go](internal/env/runtime.go): `GetRuntimesPath()`, `GetRuntimeBinaryPath()`, `GetAppsPath()`, `GetAppEnvPath()`, `GetPNPMStorePath()`, `GetNodeBinaryPath()`, `GetPNPMPath()`, `GetProjectCachePath()`
- Store-related paths (`GetBinPath`, `GetRuntimesPath`, `GetAppsPath`, `GetPNPMStorePath`) use `GetStorePath()`
- Cache-related paths (`GetProjectCachePath`) use `GetCachePath()`
- Binary caching uses stable hash-based paths for reproducibility
- Runtime-managed apps cached in `{store}/.apps/{runtime}/{app}/{hash}/` with isolated environments
- `DATAMITSU_MAX_PARALLEL_WORKERS` defaults to a dynamic CPU-based value: `max(4, floor(NumCPU * 0.75))`, capped at 16. Set this env var to override.
- **No forced CI=true**: The tool never overrides CI environment variable. Child processes inherit the system's CI state naturally.
- **Layered env merge order** (in executor.buildCommand): OS env -> color hints -> app env (cmdInfo.Env) -> ToolOperation.Env. Later layers override earlier ones.
- **ToolOperation.Env**: Per-tool-per-operation environment variables can be set in JS config via `env` field on tool operations.

### Color Support

- Color utilities in [internal/color/](internal/color/) using `github.com/fatih/color`
- Respects user environment variables in order: `NO_COLOR` (disables), `FORCE_COLOR` (enables), `CLICOLOR_FORCE` (enables), `CLICOLOR=0` (disables), then falls back to TTY detection
- Never overrides user-set color env vars; only sets defaults when absent
- Child process color hints: when color is enabled and user hasn't set `FORCE_COLOR`/`CLICOLOR_FORCE`, these are injected into child process environments so they emit ANSI despite writing to buffers
- `clr.Init()` is called in `cmd/root.go Execute()` at startup

### Output Architecture (Single-Print-Layer Rule)

- **Executor is silent**: `internal/tooling/executor.go` captures all stdout/stderr into `ExecutionResult.Output`, never prints directly
- **Runner prints once**: `internal/runner/runner.go` is the sole place that prints tool output to the user
- **Structured error format**: Failed executions display tool name, scope, directory, working directory, command, exit code, and output in a bordered block
- **Cobra silence**: Root command has `SilenceUsage: true` and `SilenceErrors: true` to prevent usage text on runtime errors
- **Fail-fast**: On first tool failure, context is cancelled to prevent new tasks from starting; already-running processes are cleaned up via process group signals
- **FailureReason tracking**: `ExecutionResult.FailureReason` distinguishes independent tool failures (`FailureReasonIndependent`) from cascading terminations (`FailureReasonCancelled`). The runner's `groupResultsByTool()` filters out cancelled results so only independent failures are shown to the user

### Managed Config and Symlinks

- **Two-layer symlink creation**: App-level links (`.datamitsu/` via `App.Links` + `CreateDatamitsuLinks`) and config-level links (root via `ConfigInit.LinkTarget`)
- **`.datamitsu/` directory**: Recreated atomically on each `init` run (remove + recreate). Listed in `.gitignore`. A `.gitignore` file containing `*` is automatically created inside `.datamitsu/` as a defensive measure — prevents accidental commits if users forget to add `.datamitsu/` to their root `.gitignore`. A `datamitsu.config.d.ts` file is written with embedded TypeScript type definitions (from `internal/config/config.d.ts` via `config.GetDefaultConfigDTS()`) to provide IDE autocomplete for config files. After creation, all symlinks are verified (existence, correct target, target file exists) — verification failure is a hard error
- **Strict app installation**: Uninstalled apps with links cause `CreateDatamitsuLinks()` to return an error immediately (no silent skipping)
- **ConfigInit.LinkTarget**: When set on a `ConfigInit` entry, the installer creates a symlink instead of writing content. Target is resolved relative to the symlink's directory
- **Lock files**: FNM apps support `LockFile` field (written as `pnpm-lock.yaml` with `--frozen-lockfile`); UV apps support `LockFile` (written as `uv.lock` with `--locked` flag). Lock file content can be brotli-compressed with `br:` prefix (see `lockfileenc.go`)
- **Validation-first**: `ValidateApps()` returns `([]string, error)` -- warnings and validation errors. Runs immediately after config load in `loadConfigWithPaths`, catching link path traversal errors and lockfile requirements before execution. Warns when UV runtime is in system mode without pythonVersion set
- **Links independence**: Links do not require `required: true`. Smart init installs apps based on tool usage, not Required flag
- **Windows**: Symlinks only, no fallback. Requires Developer Mode
- **Installer JS context**: The `content()` function in `ConfigInit` receives a context object with `projectTypes`, `rootPath`, `cwdPath`, `isRoot`, and `datamitsuDir` (relative path from `cwdPath` to `{rootPath}/.datamitsu/`)
- **Import path generation**: `tools.Path.forImport(path)` ensures relative paths are valid ES module imports. JavaScript/TypeScript `import` statements require relative paths to start with `./` or `../`, but `tools.Path.join(context.datamitsuDir, "file.js")` returns `.datamitsu/file.js` (missing `./` prefix). Wrapping with `forImport()` fixes this: `tools.Path.forImport(tools.Path.join(context.datamitsuDir, "eslint.config.js"))` produces `./.datamitsu/eslint.config.js`. The function is idempotent — paths already starting with `./` or `../` are returned unchanged

### Planner CWD-Subtree Restriction

When running from a subdirectory (cwd != git root), the planner restricts its scope:

- **Repository-scope** tasks are skipped entirely (they only run from git root)
- **Per-project** tasks are restricted to projects whose paths are within the cwd subtree
- **Per-file** tasks are restricted to files within the cwd subtree
- When running from git root (cwd == rootPath), behavior is unchanged -- all scopes operate on the full repository

Implemented via `isUnderCwd()`, `filterFilesToCwd()`, and `filterProjectLocationsToCwd()` in `internal/tooling/planner.go`. Both `rootPath` and `cwdPath` are normalized with `filepath.Clean` in `NewPlanner`.

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

`website/docs/how-to/maintain-wrapper.md` covers devtools workflows for wrapper maintainers: pull-github, pull-fnm, pull-uv, pull-runtimes commands with practical examples, CI/CD automation, and best practices.

### npm Package Installation Examples

When documenting npm package installations (FNM apps like slidev, mermaid-cli, spectral), use Docusaurus Tabs to show multiple package managers. This does NOT apply to datamitsu binary installation (which is a Go binary).

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

**When to use:** Any documentation page that shows how to install an npm package managed by datamitsu's FNM runtime. This includes packages listed as FNM apps in the configuration.
