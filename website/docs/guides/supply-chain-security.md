---
title: Supply Chain Security
description: How datamitsu enforces supply chain integrity for Bun, pnpm, UV, and Go dependencies
---

# Supply Chain Security

datamitsu treats supply chain integrity as a non-negotiable property of every install. This guide explains the defenses applied to pnpm (Bun and Node apps), UV (Python apps), and Go dependencies, and how to configure overrides when a real workload requires them.

## Hash Verification (All Downloads)

Every artifact downloaded from the internet must have a SHA-256 hash. This
includes binary apps, managed runtimes, JVM JAR files, external app and bundle
archives, pnpm itself, remote config files, and WASM output parsers. If a hash is
missing, datamitsu refuses to download — there is no permissive fallback mode.

The hash is verified before the artifact is unpacked or executed. Lock files
are mandatory for all Bun, Node, UV, and Go apps; their package managers then
enforce the integrity values carried by `pnpm-lock.yaml`, `uv.lock`,
or `go.sum`.

See the [Binary Management](./binary-management.md) guide for the verification pipeline applied to binary apps.

### Registry-Sourced Parser Modules

A [WASM output parser](./architecture/parsers.md) declares its module either as an `https` URL or as an OCI artifact (`oci: { ref, digest }`) — exactly one of the two. Declaring both, or neither, is a config error at load, and there is no fallback between them: a config that pins a registry never reaches back to a URL.

`hash` stays mandatory for both sources. For an OCI source it does double duty: it is also the expected digest of the artifact's single layer. datamitsu requires that layer to have digest `"sha256:" + hash`, so the registry digest chain and the config hash are the same number rather than two independent claims. A registry serving a correctly-digested manifest that points at different content is rejected before one payload byte is requested.

The module is therefore hash-checked three times on its way into the store:

1. **Manifest** — the manifest body is re-hashed against the pinned digest (the registry's `Docker-Content-Digest` header is never trusted), then its layer digest is compared against the config `hash`. Nothing is requested from the blob endpoint until both hold.
2. **Stream** — the blob bytes are hashed as they are written; a size or digest mismatch is a permanent failure, not a retry.
3. **File** — the finished file is verified against the config `hash` before it is published into the store.

After that the store is self-healing: a parser module already on disk is re-hashed against the declared `hash` on every use and discarded when it does not match — whatever put it there (a download, an OCI bundle seed, a restored CI cache, a copy by hand). A module is stored at `{store}/.parsers/{name}/{key}`, where the key is derived from the module's SHA-256 alone — so one entry resolves to one directory no matter which channel delivered the bytes.

:::note `--no-oci` does not disable a declared parser source
`--no-oci` (or `DATAMITSU_NO_OCI`) turns off OCI **bundle store seeding**, an accelerator that degrades gracefully when it is unavailable. A parser that declares an `oci` source keeps using it, because that is the only route to its bytes. `DATAMITSU_OFFLINE` is the single hard network gate.
:::

### A Registry `ref` Is a Trusted-Host Setting

The `ref` on a parser or a bundle decides which host datamitsu talks to, and datamitsu can be holding a `GITHUB_TOKEN` while it does. Changing a `ref` is a credential decision, not only a mirroring one.

That token is sent as Basic auth to a registry's token endpoint only when the configured registry host is `ghcr.io` **and** the token realm advertised in that registry's own `WWW-Authenticate` challenge is `ghcr.io`. Both conditions are required: the realm arrives in a header the registry controls, so checking the realm alone would let any host name `ghcr.io` as its realm and be handed a real GitHub token. A `ref` pointed at any other host pulls anonymously. The hash chain protects the bytes; it does not protect a credential handed to the wrong host.

List every registry reference a config pins, without touching the network:

```bash
datamitsu store refs --oci-only
# ghcr.io/datamitsu/datamitsu-parsers@sha256:...
```

### Signatures Are Not Verified

This build verifies no signatures at all — there is no sigstore dependency in the binary. A `signer` set on a bundle's `oci` or on a parser's `oci` is rejected at config load rather than accepted and ignored, so a config can never assert a guarantee the binary does not deliver.

Signatures still exist upstream: CI signs `checksums.txt` and the published parser artifact manifest with keyless cosign, and both can be verified out of band (`cosign verify-blob`, `cosign verify`). What the binary enforces is the hash chain above.

## Minimum Release Age (Version Selection)

When you pin a tool or runtime version with the `devtools pull-*` commands, datamitsu refuses to select a release that is too fresh. A brand-new release is the most likely to be a typosquat, a compromised publish, or an accidental break that gets yanked within hours. A short soak time lets the ecosystem catch and pull bad releases before you adopt them.

The global default is **10080 minutes (7 days)**. It applies to every command that selects a concrete version from a registry:

| Command         | Age-filtered registries                               |
| --------------- | ----------------------------------------------------- |
| `pull-github`   | GitHub releases                                       |
| `pull-node`     | npm                                                   |
| `pull-uv`       | PyPI                                                  |
| `pull-runtimes` | npm (pnpm), GitHub (Bun, uv, and JVM binary releases) |

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

Set `DATAMITSU_MIN_RELEASE_AGE` (in minutes) to change the effective default for all commands without passing `--min-age` each time. It also moves the [Go lock-file check](#release-age-at-lock-generation). `0` disables filtering globally.

```bash
# Require 14 days globally
DATAMITSU_MIN_RELEASE_AGE=20160 datamitsu devtools pull-uv apps/uvApps.json --update
```

Verify the effective value mechanically with `datamitsu config runtime`:

```bash
datamitsu config runtime | jq .minimumReleaseAgeMinutes                                   # -> 10080
DATAMITSU_MIN_RELEASE_AGE=20160 datamitsu config runtime | jq .minimumReleaseAgeMinutes   # -> 20160
```

:::note Distinct from the install-time filters
This version-selection filter — applied when _you_ pin versions with `pull-*` — is separate from the windows the package managers apply to an app's _transitive_ dependencies when they resolve them: the `minimumReleaseAge` key in `pnpm-workspace.yaml`, which pnpm applies to Bun and Node apps, and `exclude-newer`, which uv applies to UV apps. Both are 7 days, and `DATAMITSU_MIN_RELEASE_AGE` does not change them: what they resolve is written into a lock file, and a lock file has to come out the same whichever machine generates it. Change them per app instead; see [pnpm (Bun and Node Apps)](#pnpm-bun-and-node-apps) and [Release Age of Transitive Dependencies](#release-age-of-transitive-dependencies).
:::

### Transitive dependencies

`pull-*` ages only the version an app names. Everything that version depends on is resolved later, when `datamitsu config lockfile` generates the app's lock file, and each runtime covers it differently:

| App         | Transitive versions picked by | How the minimum release age reaches them                                                                                                                                                                       |
| ----------- | ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Bun, Node   | pnpm                          | `minimumReleaseAge: 10080` in the app's `pnpm-workspace.yaml` filters the whole tree, top-level package included. See [pnpm (Bun and Node Apps)](#pnpm-bun-and-node-apps).                                     |
| UV          | uv                            | `--exclude-newer P7D` filters the whole tree; the lock records the window and every install reuses it. See [Release Age of Transitive Dependencies](#release-age-of-transitive-dependencies).                  |
| Go          | minimal version selection     | Go has no such setting. `config lockfile` checks every resolved module against the effective minimum release age. See [Release Age at Lock Generation](#release-age-at-lock-generation).                       |
| JVM, binary | nothing — one file            | The artifact is a single file pinned by SHA-256, with no dependency tree. The age applies where its version is chosen: `pull-github` for binary apps. JAR versions are pinned by hand and are not age-checked. |

## Bun Apps

The Bun runtime is downloaded from an official GitHub release archive and verified against the SHA-256 digest published in that release's asset metadata. Bun apps use the same mandatory `pnpm-lock.yaml`, hardened workspace policy, and pinned native pnpm build as Node apps:

```bash
pnpm install --frozen-lockfile   # PATH starts with datamitsu's node → bun alias
```

The workspace policy blocks unapproved dependency lifecycle scripts, and `--frozen-lockfile` rejects any dependency graph that would modify the configured lock. Approved lifecycle scripts that call `node` resolve to the selected Bun through the alias, so installation never acquires Node. At execution, `--bun` keeps Node-shebang commands on the selected runtime and `--no-install` disables Bun's automatic dependency installation. datamitsu also disables Bun's automatic `.env` loading and supplies an empty Bun config when it executes the tool, preventing a target repository's `.env` files or `bunfig.toml` from changing a managed tool process. The app therefore cannot turn a cache miss into an unpinned network fetch during tool execution.

## pnpm (Bun and Node Apps)

The Node.js runtime itself is acquired as a direct, SHA-256-pinned archive download (like the JVM runtime), and pnpm — a native binary since pnpm 12 — is a runtime of its own, pinned the same way: one archive per platform from the pnpm GitHub release, each with the SHA-256 digest published in that release's asset metadata, referenced by the Node and Bun runtimes through `pnpmRuntime`. The toolchain executing your installs is integrity-verified before any package is fetched, and `datamitsu store refs` lists the pnpm archives as ordinary `runtime-binary` entries alongside the other downloads to mirror.

pnpm ships strict supply chain defaults (since pnpm 11) that block lifecycle scripts for unapproved packages. datamitsu integrates with these defaults rather than disabling them: when a Bun or Node app is installed, datamitsu writes a `pnpm-workspace.yaml` containing a secure baseline. Any per-app overrides supplied via `App.files["pnpm-workspace.yaml"]` are shallow-merged on top.

### Recommended Defaults

The baseline `pnpm-workspace.yaml` that datamitsu writes for every Bun and Node app:

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
| `strictDepBuilds`           | Blocks lifecycle scripts for unapproved packages (the pnpm default since v11)           |
| `blockExoticSubdeps`        | Rejects transitive deps from non-registry sources (git URLs, local paths) added by deps |
| `enablePrePostScripts`      | Disables `pre*`/`post*` script execution across all packages                            |
| `dangerouslyAllowAllBuilds` | Must stay `false`; setting `true` neutralizes `strictDepBuilds`                         |
| `minimumReleaseAge`         | Refuses to install packages published less than 7 days ago — defeats most typosquats    |
| `trustPolicy: no-downgrade` | Blocks unsigned/older provenance from replacing a previously-trusted version            |
| `lockfile`                  | Requires a lockfile in every install                                                    |
| `preferFrozenLockfile`      | Prefers frozen-lockfile semantics; datamitsu also passes `--frozen-lockfile` explicitly |

:::note Too-young dependencies stop the install

Since pnpm 12.3, `minimumReleaseAgeStrict` defaults to `true` whenever `minimumReleaseAge` is set explicitly, as datamitsu does. A dependency version younger than the cutoff is gated instead of being silently added to `minimumReleaseAgeExclude`, which in datamitsu's non-interactive installs stops the install. Wait until the version ages past the cutoff, or pin an older one.

:::

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

### Your Environment Cannot Override the Workspace

pnpm lets a `pnpm_config_<setting>` environment variable, in lower or upper case, win over `pnpm-workspace.yaml`. Exported for your own work, `pnpm_config_minimum_release_age=0` would drop the release-age window from every lock file you generate, and `pnpm_config_dangerously_allow_all_builds=true` would run dependency scripts at every install.

datamitsu therefore removes two groups of variables from the environment pnpm inherits: the one for every setting the merged `pnpm-workspace.yaml` holds, and the whole `pnpm_config_minimum_release_age*` family, whose exclude list loosens the window without touching `minimumReleaseAge` itself. Every other `pnpm_config_*` variable passes through — pnpm 12 reads its registry from `pnpm_config_registry`, not `npm_config_registry`. To change a setting for one app, set it in `App.files["pnpm-workspace.yaml"]`.

### Reusing Defaults in Project Repos via `sharedStorage`

For users who want to write a secure `pnpm-workspace.yaml` into a project repository (not into a datamitsu-managed Bun or Node app environment), the default `config.js` publishes the recommended defaults via `sharedStorage["pnpm-workspace-defaults"]`. Your config can read, extend, and write them.

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

UV apps use [uv](https://github.com/astral-sh/uv) to install Python packages into isolated environments. Several defenses are layered:

### `--locked` Enforces the Lock File

Every UV app requires `lockFile`, and `uv sync` runs with `--locked`. UV refuses
to modify `uv.lock` and exits non-zero if it would need to resolve versions
differently.

### `--no-build` Blocks Source Distributions

datamitsu also passes `--no-build`. UV will install only pre-built wheels —
never source distributions (sdists), which would otherwise execute arbitrary
build code during install.

The pair `--locked --no-build` is intentional. A locked dependency without a
wheel for the target platform fails rather than falling back to executing a
build backend.

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

### Release Age of Transitive Dependencies

`devtools pull-uv` ages only the package an app names. Everything that package depends on is resolved when `datamitsu config lockfile` generates the lock, and without a window uv takes the newest release it finds, including one uploaded minutes earlier. datamitsu passes one: `--exclude-newer P7D`, the built-in 7-day minimum release age. uv records it in the lock:

```toml
[options]
exclude-newer = "0001-01-01T00:00:00Z" # This has no effect and is included for backwards compatibility when using relative exclude-newer values.
exclude-newer-span = "P7D"
```

`uv sync --locked` accepts a lock only when it is given the same window again, in the same unit: `P7D` and `10080 minutes` count as different. So an install never passes the current default. It reads the window back from the lock's `[options]` and passes exactly that:

- a lock generated before datamitsu passed a window has no `[options]` and keeps installing without one;
- a lock generated since carries its window;
- changing the built-in default never invalidates a lock that is already published.

Configs that ship uv lock files adopt the window by regenerating them with `datamitsu config lockfile <appName>`. The window is the built-in default rather than `DATAMITSU_MIN_RELEASE_AGE`, because the lock records it and must come out the same whichever machine generates it.

A version younger than the window cannot resolve. `pull-uv` never selects one, but a hand-pinned version can; the error then names the minimum release age as the reason.

#### Per-App Window via `uv.toml`

An app that ships a `uv.toml`, through `App.files` or an archive, gets it as its uv configuration, for generating the lock and for installing it alike. `exclude-newer` there replaces the default window for that app. `exclude-newer-package` sets a cutoff for one package, which is the way out when a fix exists only in a release younger than the window: a timestamp, a duration, or `false` to exempt the package. The lock records both, and an install passes them back the same way.

```typescript
files: {
  "uv.toml": TOML.stringify({
    "exclude-newer-package": { "example-package": false },
  }),
}
```

Regenerate the lock file after changing it.

#### Your Own uv Settings Stay Out

uv runs with `--no-config`, or with `--config-file` pointing at the app's own `uv.toml`, so a user- or system-level `uv.toml` cannot change what a managed install resolves or checks the lock against. `UV_CONFIG_FILE`, `UV_EXCLUDE_NEWER` and `UV_EXCLUDE_NEWER_PACKAGE` are removed from the environment uv inherits; exported for your own work, any of them would otherwise make `uv sync --locked` reject every managed lock. Settings for reaching the network, such as `UV_NATIVE_TLS`, `SSL_CERT_FILE` or the proxy variables, still pass through, so set them in the environment rather than in a `uv.toml`.

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

### Release Age at Lock Generation

Go has no setting that makes resolution skip recent releases, but minimal version selection does most of the work: `go get` only takes versions that some `go.mod` in the graph names, so a dependency is normally older than the module that requires it. What remains is the version you pin — there is no `pull-*` command for Go apps — and an import that no `go.mod` requires, which `go get` resolves to the newest release.

`datamitsu config lockfile` therefore checks the result. After `go get`, it lists every module in the build (`go list -m -json all`) and fails if any is younger than the effective minimum release age, or reports no time at all:

```text
modules resolved for "tool" are younger than the minimum release age of 10080 minutes:
  example.com/tool v1.4.0 (2026-09-20T10:00:00Z)
pin an older version, or set DATAMITSU_MIN_RELEASE_AGE=0 to skip this check
```

Unlike the pnpm and uv windows, this check follows `DATAMITSU_MIN_RELEASE_AGE`, like the `pull-*` commands do. It only accepts or rejects a result, and nothing records the value, so the lock file comes out the same whatever it is set to.

The times come from the Go module proxy and are commit times, not publish times. The check stops an accidentally fresh pin. It cannot stop an attacker who backdates a commit.

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

For a UV app, set `exclude-newer` in the app's `uv.toml`, then regenerate its lock file:

```typescript
files: {
  "uv.toml": TOML.stringify({
    "exclude-newer": "30 days",
  }),
}
```

### CI Hardening

A reasonable CI pipeline for repositories that consume datamitsu:

```bash
datamitsu init  # on a fresh runner, materialize the hash-pinned store
datamitsu check # run the full check pipeline
go mod verify                    # if your repo has Go code
datamitsu exec govulncheck -- ./... # if your repo has Go code
```

For wrapper packages, also run `datamitsu devtools verify-all` to confirm every platform's binaries and runtimes still hash-match.

## Related

- [Maintaining Wrapper Packages](../how-to/maintain-wrapper.md) — keeping tool versions, hashes, and lock files current
- [PNPM Patterns](../examples/pnpm-patterns.md) — workspace and dependency examples for node apps
- [UV Isolation](../examples/uv-isolation.md) — Python app isolation patterns
- [Binary Management](./binary-management.md) — hash verification for binary apps and runtimes
