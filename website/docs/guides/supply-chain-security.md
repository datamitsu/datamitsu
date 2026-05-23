---
title: Supply Chain Security
description: How datamitsu enforces supply chain integrity for pnpm, UV, and Go dependencies
---

# Supply Chain Security

datamitsu treats supply chain integrity as a non-negotiable property of every install. This guide explains the defenses applied to pnpm (FNM apps), UV (Python apps), and Go dependencies, and how to configure overrides when a real workload requires them.

## Hash Verification (All Downloads)

Every artifact downloaded from the internet must have a SHA-256 hash. This applies to binary apps, managed runtimes, JVM JAR files, and remote config files. If a hash is missing, datamitsu refuses to download — there is no permissive fallback mode.

The hash is verified before the artifact is unpacked or executed. Lock files are mandatory for all UV and FNM apps; the lock file content itself is hash-verified as part of the app config.

See the [Binary Management](./binary-management.md) guide for the verification pipeline applied to binary apps.

## pnpm (FNM Apps)

pnpm 11 introduces strict supply chain defaults that block lifecycle scripts for unapproved packages. datamitsu integrates with these defaults rather than disabling them: when an FNM app is installed, datamitsu writes a `pnpm-workspace.yaml` containing a secure baseline. Any per-app overrides supplied via `App.files["pnpm-workspace.yaml"]` are shallow-merged on top.

### Recommended Defaults

The baseline `pnpm-workspace.yaml` that datamitsu writes for every FNM app:

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
    fnm: {
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

For users who want to write a secure `pnpm-workspace.yaml` into a project repository (not into a datamitsu-managed FNM app environment), the default `config.js` publishes the recommended defaults via `sharedStorage["pnpm-workspace-defaults"]`. Your config can read, extend, and write them.

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

This pattern is for **repo-level** configuration — a project's own `pnpm-workspace.yaml`. It is separate from the per-app FNM merge above, which is fully automatic.

### Lockfile Enforcement

FNM apps require a `lockFile` field. datamitsu runs `pnpm install --frozen-lockfile`, so any drift between the lock file and `package.json` fails the install. Regenerate lock files after version bumps via `datamitsu config lockfile <appName>`.

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

## Go

datamitsu does not manage Go dependencies directly — `go` itself does. The relevant defenses live in standard Go tooling and should be enforced in CI:

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

Go's checksum database ([sum.golang.org](https://sum.golang.org/)) is consulted by default during `go mod download`, providing transparency-log-backed verification on top of `go.sum`.

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
- [PNPM Patterns](../examples/pnpm-patterns.md) — workspace and dependency examples for FNM apps
- [UV Isolation](../examples/uv-isolation.md) — Python app isolation patterns
- [Binary Management](./binary-management.md) — hash verification for binary apps and runtimes
