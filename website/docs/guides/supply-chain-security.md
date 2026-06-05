---
title: Supply Chain Security
description: How datamitsu enforces supply chain integrity for pnpm, UV, and Go dependencies
---

# Supply Chain Security

datamitsu treats supply chain integrity as a non-negotiable property of every install. This guide explains the defenses applied to pnpm (node apps), UV (Python apps), and Go dependencies, and how to configure overrides when a real workload requires them.

## Hash Verification (All Downloads)

Every artifact downloaded from the internet must have a SHA-256 hash. This applies to binary apps, managed runtimes, JVM JAR files, and remote config files. If a hash is missing, datamitsu refuses to download — there is no permissive fallback mode.

The hash is verified before the artifact is unpacked or executed. Lock files are mandatory for all UV and node apps; the lock file content itself is hash-verified as part of the app config.

See the [Binary Management](./binary-management.md) guide for the verification pipeline applied to binary apps.

## Minimum Release Age (Version Selection)

When you pin a tool or runtime version with the `devtools pull-*` commands, datamitsu refuses to select a release that is too fresh. A brand-new release is the most likely to be a typosquat, a compromised publish, or an accidental break that gets yanked within hours. A short soak time lets the ecosystem catch and pull bad releases before you adopt them.

The global default is **10080 minutes (7 days)**. It applies to every command that selects a concrete version from a registry:

| Command         | Age-filtered registries                         |
| --------------- | ----------------------------------------------- |
| `pull-github`   | GitHub releases                                 |
| `pull-node`     | npm                                             |
| `pull-uv`       | PyPI                                            |
| `pull-runtimes` | npm (pnpm), GitHub (uv and JVM binary releases) |

Major-version-line lookups are **not** age-filtered, because they select a release _line_ rather than a specific build: the Node.js LTS line and Python stable line (endoflife.date), the Temurin major version (Adoptium API), and the Go release listing (go.dev). Only the concrete binary release or package version chosen within those lines passes through the age filter.

### The `--min-age` flag

Every `pull-*` command accepts `--min-age <minutes>`:

- `--min-age -1` (the default) — use the global effective minimum release age
- `--min-age 0` — disable age filtering and take the latest release
- `--min-age 43200` — a custom cutoff (here, 30 days)

```bash
# Pin only releases at least 7 days old (the default)
datamitsu devtools pull-github apps/githubApps.json --update

# Require 30 days of soak time
datamitsu devtools pull-node apps/nodeApps.json --update --min-age 43200

# Bypass the filter and take the newest release
datamitsu devtools pull-github apps/githubApps.json --update --min-age 0
```

Each command prints the effective cutoff in its status banner (`Minimum release age: 10080 minutes`, or `disabled` when set to 0).

### When no release is old enough

If every available release is younger than the cutoff, datamitsu's behavior depends on whether a safe fallback exists:

- **`pull-github`** — an _existing_ app keeps its current tag with a warning; a _new_ app (no prior binary) is a **hard error**, since there is nothing safe to pin.
- **`pull-node` / `pull-uv`** — the package is skipped with a warning and keeps its current version.
- **`pull-runtimes`** — a hard error, since runtimes must resolve to a concrete version.

The error message always points at the `--min-age 0` escape hatch.

### Overriding the global default

Set `DATAMITSU_MIN_RELEASE_AGE` (in minutes) to change the effective default for all commands without passing `--min-age` each time. `0` disables filtering globally.

```bash
# Require 14 days globally
DATAMITSU_MIN_RELEASE_AGE=20160 datamitsu devtools pull-uv apps/uvApps.json --update
```

Verify the effective value mechanically with `datamitsu config runtime`:

```bash
datamitsu config runtime | jq .minimumReleaseAgeMinutes                                   # -> 10080
DATAMITSU_MIN_RELEASE_AGE=20160 datamitsu config runtime | jq .minimumReleaseAgeMinutes   # -> 20160
```

:::note Distinct from the pnpm `minimumReleaseAge` setting
This version-selection filter — applied when _you_ pin versions with `pull-*` — is separate from the `minimumReleaseAge` key in `pnpm-workspace.yaml`, which pnpm applies when it resolves a node app's _transitive_ dependencies at install time. Both default to 7 days; see [pnpm (Node Apps)](#pnpm-node-apps) for the install-time setting.
:::

## pnpm (Node Apps)

The Node.js runtime itself is acquired as a direct, SHA-256-pinned archive download (like the JVM runtime), and pnpm is pinned by SHA-256 as well — so the toolchain executing your installs is integrity-verified before any package is fetched.

pnpm 11 introduces strict supply chain defaults that block lifecycle scripts for unapproved packages. datamitsu integrates with these defaults rather than disabling them: when a node app is installed, datamitsu writes a `pnpm-workspace.yaml` containing a secure baseline. Any per-app overrides supplied via `App.files["pnpm-workspace.yaml"]` are shallow-merged on top.

### Recommended Defaults

The baseline `pnpm-workspace.yaml` that datamitsu writes for every node app:

```yaml
strictDepBuilds: true
blockExoticSubdeps: true
enablePrePostScripts: false
dangerouslyAllowAllBuilds: false
minimumReleaseAge: 10080 # 7 days in minutes
trustPolicy: no-downgrade
lockfile: true
preferFrozenLockfile: true
```

What each setting does:

| Setting                     | Purpose                                                                                 |
| --------------------------- | --------------------------------------------------------------------------------------- |
| `strictDepBuilds`           | Blocks lifecycle scripts for unapproved packages (the pnpm 11 default)                  |
| `blockExoticSubdeps`        | Rejects transitive deps from non-registry sources (git URLs, local paths) added by deps |
| `enablePrePostScripts`      | Disables `pre*`/`post*` script execution across all packages                            |
| `dangerouslyAllowAllBuilds` | Must stay `false`; setting `true` neutralizes `strictDepBuilds`                         |
| `minimumReleaseAge`         | Refuses to install packages published less than 7 days ago — defeats most typosquats    |
| `trustPolicy: no-downgrade` | Blocks unsigned/older provenance from replacing a previously-trusted version            |
| `lockfile`                  | Requires a lockfile in every install                                                    |
| `preferFrozenLockfile`      | Uses `--frozen-lockfile` semantics when a lockfile is present                           |

### Per-App Overrides via `App.files`

When a package legitimately needs build scripts (puppeteer, sharp, esbuild, etc.), allowlist it via `App.files["pnpm-workspace.yaml"]`. Your overrides are shallow-merged on top of the defaults — keys you do not set keep the secure baseline.

```typescript
const mapOfApps: BinManager.MapOfApps = {
  mmdc: {
    files: {
      "pnpm-workspace.yaml": YAML.stringify({
        allowBuilds: { puppeteer: true },
      }),
    },
    node: {
      packageName: "@mermaid-js/mermaid-cli",
      binPath: "node_modules/.bin/mmdc",
      version: "11.15.0",
      lockFile: "br:...",
    },
  },
};
```

The merged file written to the app environment includes both the secure defaults (e.g., `strictDepBuilds: true`) and the user's `allowBuilds` entry. The original `pnpm-workspace.yaml` from `App.files` is consumed by the merge and never written as a raw file.

### Overriding a Security Setting

User intent wins. If you explicitly set a key, it overrides the default — including security keys. For example, setting `strictDepBuilds: false` disables strict build approval. Use this sparingly and only when you understand the trade-off.

```typescript
files: {
  "pnpm-workspace.yaml": YAML.stringify({
    strictDepBuilds: false, // overrides the default
  }),
}
```

### Reusing Defaults in Project Repos via `sharedStorage`

For users who want to write a secure `pnpm-workspace.yaml` into a project repository (not into a datamitsu-managed node app environment), the default `config.js` publishes the recommended defaults via `sharedStorage["pnpm-workspace-defaults"]`. Your config can read, extend, and write them.

```typescript
function getConfig(config: BinManager.Config): BinManager.Config {
  const pnpmDefaults = YAML.parse(config.sharedStorage?.["pnpm-workspace-defaults"] ?? "{}");

  const repoPnpmConfig = {
    ...pnpmDefaults,
    packages: ["packages/*"],
    allowBuilds: { esbuild: true, sharp: true },
  };

  return {
    ...config,
    bundles: {
      "project-pnpm-config": {
        files: {
          "pnpm-workspace.yaml": YAML.stringify(repoPnpmConfig),
        },
        links: { "pnpm-workspace": "pnpm-workspace.yaml" },
      },
    },
  };
}
```

This pattern is for **repo-level** configuration — a project's own `pnpm-workspace.yaml`. It is separate from the per-app node merge above, which is fully automatic.

### Lockfile Enforcement

Node apps require a `lockFile` field. datamitsu runs `pnpm install --frozen-lockfile`, so any drift between the lock file and `package.json` fails the install. Regenerate lock files after version bumps via `datamitsu config lockfile <appName>`.

## UV (Python Apps)

UV apps use [uv](https://github.com/astral-sh/uv) to install Python packages into isolated environments. Two install-time defenses are layered:

### `--locked` Enforces the Lock File

When a `lockFile` is present, `uv sync` runs with `--locked`. UV refuses to modify `uv.lock` and exits non-zero if it would need to resolve versions differently.

### `--no-build` Blocks Source Distributions

When a `lockFile` is present, datamitsu also passes `--no-build`. UV will install only pre-built wheels — never source distributions (sdists), which would otherwise execute arbitrary build code during install.

The pair `--locked --no-build` is intentional. With a lock file you know exactly which versions will be installed, so you can be confident wheels exist for your target platforms. Without a lock file, `--no-build` is omitted because wheel availability cannot be predicted.

**Trade-off**: if a locked version has no wheel for your platform (e.g., a niche arch, or a pure-Python package without a wheel), the install fails. This is intentional — the failure surfaces a security-relevant gap rather than silently executing setup.py.

```typescript
const mapOfApps: BinManager.MapOfApps = {
  yamllint: {
    uv: {
      packageName: "yamllint",
      version: "1.38.0",
      lockFile: "br:...", // mandatory; triggers --locked + --no-build
    },
  },
};
```

### Hash Verification

UV's lock file format embeds hashes for every resolved artifact. Combined with `--locked`, this gives end-to-end integrity for the dependency tree.

## Go (Go Apps)

Go apps build a command-line tool from source with the managed Go SDK. Three layers of defense apply before the resulting binary runs.

### Hash-Pinned Go SDK

The Go toolchain itself is downloaded as a SHA-256-pinned archive (like the Node.js and JVM runtimes), so the compiler building your tool is integrity-verified before any source is fetched. `GOTOOLCHAIN=local` is forced, so the pinned SDK can never silently swap itself for a different toolchain version.

### Mandatory Lock File (`go.mod` + `go.sum`)

Go apps require a `lockFile` field. datamitsu stores it as a JSON wrapper carrying both `go.mod` and `go.sum`, writes both files into an isolated build directory, and builds with `-mod=readonly`. Any drift between the resolved modules and the pinned `go.sum` fails the build instead of silently rewriting it. The checksum database ([sum.golang.org](https://sum.golang.org/)) is consulted via `GOSUMDB`, and `GOPRIVATE`/`GONOPROXY`/`GONOSUMDB`/`GOINSECURE` are explicitly cleared so no module can opt out of verification.

```typescript
const mapOfApps: BinManager.MapOfApps = {
  govulncheck: {
    go: {
      packageName: "golang.org/x/vuln/cmd/govulncheck",
      version: "v1.3.0",
      lockFile: "br:...", // mandatory; carries go.mod + go.sum
    },
  },
};
```

### Reproducible Builds

The build runs `go build -trimpath -mod=readonly`. `-trimpath` strips local filesystem paths so the output is reproducible, and `-mod=readonly` forbids any `go.mod`/`go.sum` mutation. Regenerate the lock file after a version bump via `datamitsu config lockfile <appName>`.

### Securing Your Own Repository's Go Code

datamitsu manages Go _apps_ (tools it builds for you); your repository's own Go modules are still managed by `go` itself. Enforce the standard defenses in CI:

```bash
# Verify go.sum checksums against the local module cache
go mod verify

# Ensure go.mod and go.sum are current and committed
go mod tidy && git diff --exit-code

# Build without silently mutating go.mod or downloading new modules
GOFLAGS=-mod=readonly go build ./...

# Scan for known vulnerabilities in dependencies and stdlib
govulncheck ./...
```

## Common Patterns

### Adding a Package that Requires Build Scripts

1. Identify the package — the install error will name it: `ERR_PNPM_IGNORED_BUILDS`
2. Add it to `App.files["pnpm-workspace.yaml"]` under `allowBuilds`
3. Regenerate the lock file: `pnpm exec datamitsu config lockfile <appName>`
4. Commit both the config change and the new `lockFile` value

### Tightening `minimumReleaseAge`

The 7-day default catches typosquats and rushed compromised releases. For higher-security environments, override per app:

```typescript
files: {
  "pnpm-workspace.yaml": YAML.stringify({
    minimumReleaseAge: 43200, // 30 days
  }),
}
```

### CI Hardening

A reasonable CI pipeline for repositories that consume datamitsu:

```bash
datamitsu init --no-cache       # fresh install, all hashes re-verified
datamitsu check                  # run the full check pipeline
go mod verify                    # if your repo has Go code
govulncheck ./...                # if your repo has Go code
```

For wrapper packages, also run `datamitsu devtools verify-all` to confirm every platform's binaries and runtimes still hash-match.

## Related

- [Maintaining Wrapper Packages](../how-to/maintain-wrapper.md) — keeping tool versions, hashes, and lock files current
- [PNPM Patterns](../examples/pnpm-patterns.md) — workspace and dependency examples for node apps
- [UV Isolation](../examples/uv-isolation.md) — Python app isolation patterns
- [Binary Management](./binary-management.md) — hash verification for binary apps and runtimes
