# Runtime Management

> Managing Bun, UV (Python), Node.js, JVM, and Go runtimes with datamitsu

datamitsu manages language runtimes alongside your tools. Instead of requiring team members to install specific versions of Bun, Python, Node.js, Java, or Go, datamitsu downloads and manages these runtimes automatically, creating isolated environments for each tool.

## Runtime Types

datamitsu supports five runtime types:

| Runtime  | Language   | Package Manager | Use Case                                    |
| -------- | ---------- | --------------- | ------------------------------------------- |
| **Bun**  | JavaScript | pnpm            | npm packages executed directly with Bun     |
| **UV**   | Python     | uv              | Python tools like yamllint, ruff            |
| **Node** | Node.js    | pnpm            | npm packages like ESLint, Prettier          |
| **JVM**  | Java       | -               | JAR-based tools like openapi-generator      |
| **Go**   | Go         | go modules      | Go tools built from source like govulncheck |

:::tip Updating Runtimes
To update runtime versions using devtools workflows, see [Maintaining Wrapper Packages](../how-to/maintain-wrapper.md).
:::

## Managed vs System Mode

Each runtime can operate in two modes:

### Managed Mode (Default)

datamitsu downloads and manages the runtime binary itself. This ensures everyone uses the exact same runtime version:

```javascript
runtimes: {
  uv: {
    kind: "uv",
    mode: "managed",
    managed: {
      binaries: {
        linux: {
          amd64: {
            glibc: {
              url: "https://github.com/astral-sh/uv/releases/download/0.6.0/uv-x86_64-unknown-linux-gnu.tar.gz",
              hash: "...",
              contentType: "tar.gz",
              binaryPath: "uv",
            },
          },
        },
      },
    },
    uv: {
      pythonVersion: "3.12",
    },
  },
}
```

### System Mode

Use the runtime already installed on the system. This is useful when the runtime is managed externally:

```javascript
runtimes: {
  uv: {
    kind: "uv",
    mode: "system",
    system: {
      command: "uv",
      systemVersion: "1.0",
    },
    uv: {
      pythonVersion: "3.12",
    },
  },
}
```

The optional `systemVersion` field moves runtime-dependent apps to a new store
key when the system runtime changes (for example, after a system upgrade).

## UV Runtime (Python)

UV apps use [uv](https://github.com/astral-sh/uv) to create isolated Python environments for each tool.

### How UV Apps Work

1. datamitsu ensures the UV runtime is available (downloads it in managed mode)
2. Creates an isolated environment at `{store}/.apps/uv/{appName}/{hash}/`
3. Writes a `pyproject.toml` and runs `uv sync` to install the package
4. Executes the tool from the isolated environment

### Defining a UV App

```javascript
apps: {
  yamllint: {
    uv: {
      packageName: "yamllint",
      version: "1.35.1",
      lockFile: "br:...", // required; generated with config lockfile
    },
  },
}
```

### Python Version

The Python version is configured on the runtime, not individual apps:

```javascript
uv: {
  pythonVersion: "3.12",
}
```

If using system mode without specifying `pythonVersion`, datamitsu will warn you since the Python version becomes implicit.

### Store Layout and the Managed Python Interpreter

In managed mode, uv downloads a CPython interpreter and reuses it across uv apps. datamitsu redirects that interpreter into the store at `<store>/.uv/python/` (via `UV_PYTHON_INSTALL_DIR`), one CPython per version shared across apps, alongside uv's per-app cache. This keeps the store **self-contained**: when the store is cached (e.g. in CI) and restored onto a fresh runner, each app's `.venv/bin/python` symlink still resolves, because the interpreter it points at travels with the store rather than living in uv's default data dir (`~/.local/share/uv/python`).

datamitsu also validates the venv interpreter at install time. If `.venv/bin/python` dangles (for example, after restoring a partial or stale cache), the venv is treated as not installed and rebuilt, so a broken interpreter self-heals instead of being trusted forever.

## Bun Runtime

Bun apps use [pnpm](https://pnpm.io/) for dependency installation and a managed [Bun](https://bun.sh/) runtime for JavaScript execution. pnpm is a native binary, and the lifecycle scripts it runs reach Bun through a `node` alias, so selecting Bun does not download Node merely to install dependencies. Bun remains a separate app kind: selecting it is an explicit configuration choice and does not change how Node apps run. Managed x64 targets use Bun's standard optimized archives and therefore require AVX2-capable hardware.

### How Bun Apps Work

1. datamitsu downloads and verifies the SHA-256-pinned Bun release archive
2. Creates an isolated environment at `{store}/.apps/bun/{appName}/{hash}/`
3. Acquires the pnpm runtime named by the Bun runtime's `pnpmRuntime` (a SHA-256-pinned native pnpm archive for the host platform) and writes `package.json`, `pnpm-workspace.yaml`, and the configured `pnpm-lock.yaml`
4. Runs `pnpm install --frozen-lockfile` with datamitsu's `node` → Bun alias first on `PATH`, so lifecycle scripts that call `node` run on Bun
5. Executes the configured JavaScript entrypoint with `bun run --bun --no-install`

Both Bun and Node apps share pnpm's content-addressable store at `{store}/.pnpm-store/`, while every app keeps its own `node_modules` directory. During execution, `--bun` and the same alias keep Node-shebang commands launched by the tool on the selected runtime.

The alias at `.datamitsu-runtime-bin/node` is a link to the Bun executable rather than a wrapper script, because Bun emulates the node CLI only when it is invoked under the name `node` — called under its own name it would read `node build` as its bundler and `node install` as its package manager.

Runtime auto-install is disabled, and datamitsu passes `--no-env-file` plus an empty Bun config when it executes the tool, so a target repository's `.env` files and `bunfig.toml` cannot change a managed tool's behavior. The same guards travel in `BUN_OPTIONS`, which every Bun process started under the app inherits, so they hold for the `node` processes a lifecycle script or the tool starts itself too.

### Defining a Bun App

```javascript
apps: {
  eslint: {
    bun: {
      packageName: "eslint",
      version: "10.9.0",
      binPath: "node_modules/eslint/bin/eslint.js",
      lockFile: "br:...", // required; generated with config lockfile
    },
  },
}
```

### Bun Version

The Bun version is configured on the runtime:

```javascript
bun: {
  bunVersion: "1.4.1",
}
```

## Node Runtime (Node.js)

Node apps use a managed Node.js runtime — downloaded as a pinned, SHA-256-verified archive (exactly like the JVM runtime downloads a JDK) — together with [pnpm](https://pnpm.io/) as the package manager.

### How Node Apps Work

1. datamitsu downloads and verifies (SHA-256) the configured Node.js archive and extracts it
2. Acquires the pnpm runtime named by the Node runtime's `pnpmRuntime` (a SHA-256-pinned native pnpm archive for the host platform)
3. Creates an isolated environment at `{store}/.apps/node/{appName}/{hash}/`
4. Runs `pnpm install --frozen-lockfile` with the managed Node.js first on `PATH`, so lifecycle scripts run on the pinned interpreter
5. Executes the tool via Node.js

### Defining a Node App

```javascript
apps: {
  eslint: {
    node: {
      packageName: "eslint",
      version: "9.0.0",
      binPath: "node_modules/.bin/eslint",
      lockFile: "br:...", // required; generated with config lockfile
    },
  },
}
```

### JavaScript Runtimes and the pnpm Runtime

Since pnpm 12, pnpm is a native binary with no JavaScript implementation, so it is a runtime of its own (kind `pnpm`), defined once and referenced by every Node and Bun runtime through `pnpmRuntime`:

```javascript
runtimes: {
  pnpm: {
    kind: "pnpm",
    mode: "managed",
    managed: {
      binaries: {
        linux: {
          amd64: {
            glibc: {
              url: "https://github.com/pnpm/pnpm/releases/download/v12.4.1/pnpm-linux-x64.tar.gz",
              hash: "66e98862...", // SHA-256 (mandatory)
              contentType: "tar.gz",
              binaryPath: "pnpm",
              extractDir: true,
            },
          },
        },
        // ... other platforms — `datamitsu devtools pull-runtimes` generates the full map
      },
    },
    pnpm: { pnpmVersion: "12.4.1" },
  },
  node: {
    kind: "node",
    mode: "managed",
    managed: { binaries: { /* Node.js archives */ } },
    node: { nodeVersion: "26.2.0", pnpmRuntime: "pnpm" },
  },
  bun: {
    kind: "bun",
    mode: "managed",
    managed: { binaries: { /* Bun archives */ } },
    bun: { bunVersion: "1.4.1", pnpmRuntime: "pnpm" },
  },
}
```

`pnpmRuntime` is required and must name a runtime of kind `pnpm`. Several Node and Bun runtimes can share one pnpm runtime or point at different ones. No app runs on a pnpm runtime: an app whose `runtime` names one fails validation.

The archives come from the pnpm GitHub release and are extracted whole: besides the binary they carry the `node-gyp` that pnpm uses to build native dependencies. As with any managed runtime, datamitsu downloads only the entry for the host platform and falls back from musl to glibc; a system pnpm works too (`mode: "system", system: { command: "pnpm" }`, where `pnpmVersion` becomes optional). `datamitsu init` downloads the pnpm runtime along with the Node or Bun runtime whenever a required app needs it. pnpm is only needed to install apps, so it is not part of the runtimes a built Docker image or OCI bundle ships for execution.

Runtimes, pnpm included, are stored under `{store}/.runtimes/{name}/{configHash}/`. The pnpm runtime's identity is part of every Node and Bun app's environment hash but not of the Node or Bun runtime's own hash, so a pnpm bump reinstalls the apps without downloading Node or Bun again. A shared pnpm content-addressable store at `{store}/.pnpm-store/` deduplicates packages across Bun and Node apps.

:::warning Breaking change: pnpm moved into its own runtime

A Node or Bun runtime that still pins pnpm with `pnpmVersion` and `pnpmHash` (the SHA-256 of the npm `pnpm` tarball, which only works for pnpm 11 and earlier) no longer loads, because it has no `pnpmRuntime`. Add a runtime of kind `pnpm` and point the Node and Bun runtimes at it — `datamitsu devtools pull-runtimes <file> --update` writes all three entries. Existing `pnpm-lock.yaml` lock files (`lockfileVersion: '9.0'`) keep working unchanged.

:::

## JVM Runtime (Java)

JVM apps download JAR files and execute them with a managed JDK.

### How JVM Apps Work

1. datamitsu downloads a Temurin JDK distribution (full directory extraction)
2. Downloads the app's JAR file with SHA-256 verification
3. Executes via `java -jar` (or `java -cp <jar> <mainClass>` when `mainClass` is specified)

### Defining a JVM App

```javascript
apps: {
  "openapi-generator": {
    jvm: {
      jarUrl: "https://repo1.maven.org/maven2/org/openapitools/openapi-generator-cli/7.0.0/openapi-generator-cli-7.0.0.jar",
      jarHash: "...",
      version: "7.0.0",
    },
  },
}
```

### Java Version

Configured on the runtime definition:

```javascript
jvm: {
  javaVersion: "21",
}
```

## Go Runtime

Go apps build a command-line tool from source using a managed Go SDK, producing a single self-contained binary.

### How Go Apps Work

1. datamitsu downloads and verifies (SHA-256) the pinned Go SDK and extracts it
2. Writes the app's `go.mod` and `go.sum` (from its lock file) into an isolated build directory
3. Builds with `go build -trimpath -mod=readonly` in a hardened environment (`GOTOOLCHAIN=local`, `GOSUMDB` enabled, `GOPRIVATE`/`GONOPROXY`/`GONOSUMDB`/`GOINSECURE` cleared)
4. Executes the built binary directly

### Defining a Go App

```javascript
apps: {
  govulncheck: {
    go: {
      packageName: "golang.org/x/vuln/cmd/govulncheck",
      version: "v1.3.0",
      lockFile: "br:...", // carries go.mod + go.sum (required)
    },
  },
}
```

### Go SDK Version

Configured on the runtime definition:

```javascript
go: {
  goVersion: "1.26.3",
}
```

A Go app's lock file is a JSON wrapper carrying both `go.mod` and `go.sum`. Because the build runs with `-mod=readonly`, any drift from the pinned `go.sum` fails the build instead of silently rewriting it. Regenerate it after a version bump with `datamitsu config lockfile <appName>`. See [Supply Chain Security](./supply-chain-security.md#go-go-apps) for the full defense model.

## Lock Files

Bun, Node, UV, and Go apps require lock files. They pin exact dependencies and their
integrity data so installs and builds are reproducible across environments.

### Generating a Lock File

First declare the app's package and version. Then use the special
`config lockfile` command to generate its lock content:

```bash
datamitsu config lockfile eslint
```

This outputs brotli-compressed, base64-encoded lock file content that you paste into your configuration:

```javascript
eslint: {
  bun: {
    packageName: "eslint",
    version: "10.9.0",
    binPath: "node_modules/eslint/bin/eslint.js",
    lockFile: "br:...",
  },
}
```

### How Lock Files Work

- **Bun and Node apps**: The lock file is written as `pnpm-lock.yaml` and pnpm runs with `--frozen-lockfile`, refusing to install if dependencies don't match. pnpm runs as the native binary of the referenced pnpm runtime, with the selected Bun or Node runtime serving lifecycle scripts
- **UV apps**: The lock file is written as `uv.lock` and uv runs with `--locked --no-build`, ensuring exact version matching and permitting wheels only
- **Go apps**: The JSON payload is expanded into `go.mod` and `go.sum`, and the tool is built with `go build -trimpath -mod=readonly`

The generation command deliberately loads config without enforcing the missing
field so it can create that field. Every normal config load rejects a Bun, Node,
UV, or Go app without `lockFile` before downloading or executing anything.

## Isolated Environments

Each runtime-managed app gets its own isolated environment. This prevents version conflicts between tools:

```
{store}/.apps/
├── bun/
│   └── eslint/{hash}/       # isolated Bun app
├── uv/
│   ├── yamllint/{hash}/     # isolated Python env
│   └── ruff/{hash}/         # separate isolated env
├── node/
│   ├── eslint/{hash}/       # isolated Node.js env
│   └── prettier/{hash}/     # separate isolated env
└── jvm/
    └── openapi-generator/{hash}/  # JAR + metadata
```

The `{hash}` covers the runtime identity, app configuration (including the lock
file and any files or archives), and resolved target. Changing one creates a new
isolated environment; the old entry remains in the store until it is cleared.

## Runtime Resolution

When an app references a runtime by name, datamitsu resolves it through this chain:

1. App-level runtime override (if specified)
2. Global default runtime for the app's kind

This allows you to have multiple runtimes of the same kind (e.g., different Node.js versions for different apps) while keeping a sensible default.

## Libc Detection and Alpine Linux

datamitsu detects the host libc at startup using a multi-stage process (ldd output, ELF interpreter, loader paths). This detection feeds into binary app resolution, where musl-specific binaries are preferred on musl systems.

Managed runtimes need extra handling on musl because their upstreams' default channels are glibc-oriented:

- **Bun**: official releases provide separate glibc and musl archives for both amd64 and arm64; datamitsu pins and selects them directly
- **pnpm**: releases provide glibc and musl builds for both amd64 and arm64; datamitsu pins both and selects by the detected libc
- **Node.js**: the default `nodejs.org/dist` archives are glibc-only. The registry therefore carries static musl entries (url + SHA-256) sourced from [unofficial-builds.nodejs.org](https://unofficial-builds.nodejs.org/download/release). On a musl host datamitsu downloads and verifies the musl Node archive directly, so node apps work on Alpine out of the box. musl Node additionally requires `libstdc++` (`apk add libstdc++`). See [Use in Alpine Linux](../how-to/use-in-alpine) for details.
- **Python** (UV): uv downloads glibc Python builds
- **JDK** (JVM): Temurin JDK releases are glibc-only

### Automatic Fallback to System Mode

When running on a musl system (e.g., Alpine Linux), datamitsu automatically detects when a managed runtime lacks a musl-compatible binary and falls back to system mode if the corresponding system binary is available on PATH.

The fallback logic works as follows:

1. Detects the host is musl
2. Checks if the runtime's managed binaries include a musl variant
3. If no musl binary exists, looks for the system binary (`node`, `uv`, `java`, or `pnpm`) via PATH
4. If found, automatically switches to system mode using the system binary
5. If not found, falls through to the existing glibc fallback behavior

Note that the Node runtime ships static musl archive entries in the registry, so it installs in managed mode on Alpine out of the box — no system fallback needed. `uv` is not available in Alpine's default package repositories, so the auto-fallback does not help there; for UV on Alpine, use [manual system mode](#manual-system-mode) configuration instead. The auto-fallback is most useful for the JVM runtime (where `apk add openjdk17` provides the `java` binary).

When auto-fallback triggers, you will see a log message:

```
INFO: automatic fallback to system mode  runtime=jvm  reason="musl binary unavailable"  system_command=/usr/bin/java
```

### Manual System Mode

If you prefer explicit control, you can still configure runtimes in **system mode** manually:

```sh
# Install runtimes via apk
apk add nodejs npm python3 openjdk17
```

```javascript
runtimes: {
  node: {
    kind: "node",
    mode: "system",
    system: { command: "node" },
    node: { nodeVersion: "26.2.0", pnpmRuntime: "pnpm" },
  },
  pnpm: {
    kind: "pnpm",
    // pnpm can stay pinned and downloaded while Node comes from the system,
    // or use mode: "system" with system: { command: "pnpm" }
    mode: "managed",
    managed: { binaries: { /* per-platform pnpm archives */ } },
    pnpm: { pnpmVersion: "12.4.1" },
  },
  uv: {
    kind: "uv",
    mode: "system",
    system: { command: "uv" },
    uv: { pythonVersion: "3.12" },
  },
  jvm: {
    kind: "jvm",
    mode: "system",
    system: { command: "java" },
    jvm: { javaVersion: "17" },
  },
}
```

Binary apps (type `binary`) are not affected by this limitation. They use target-based resolution with automatic libc detection and will select musl-specific builds when available. See [Use in Alpine Linux](../how-to/use-in-alpine) for details.
