---
title: Runtime Management
description: Managing UV (Python), Node.js, JVM, and Go runtimes with datamitsu
---

# Runtime Management

datamitsu manages language runtimes alongside your tools. Instead of requiring team members to install specific versions of Python, Node.js, Java, or Go, datamitsu downloads and manages these runtimes automatically, creating isolated environments for each tool.

## Runtime Types

datamitsu supports four runtime types:

| Runtime  | Language | Package Manager | Use Case                                    |
| -------- | -------- | --------------- | ------------------------------------------- |
| **UV**   | Python   | uv              | Python tools like yamllint, ruff            |
| **Node** | Node.js  | pnpm            | npm packages like ESLint, Prettier          |
| **JVM**  | Java     | -               | JAR-based tools like openapi-generator      |
| **Go**   | Go       | go modules      | Go tools built from source like govulncheck |

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

The optional `systemVersion` field lets you manually invalidate the cache when the system runtime changes (e.g., after a system upgrade).

## UV Runtime (Python)

UV apps use [uv](https://github.com/astral-sh/uv) to create isolated Python environments for each tool.

### How UV Apps Work

1. datamitsu ensures the UV runtime is available (downloads it in managed mode)
2. Creates an isolated environment at `.apps/uv/{appName}/{hash}/`
3. Writes a `pyproject.toml` and runs `uv sync` to install the package
4. Executes the tool from the isolated environment

### Defining a UV App

```javascript
apps: {
  yamllint: {
    uv: {
      packageName: "yamllint",
      version: "1.35.1",
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

## Node Runtime (Node.js)

Node apps use a managed Node.js runtime — downloaded as a pinned, SHA-256-verified archive (exactly like the JVM runtime downloads a JDK) — together with [pnpm](https://pnpm.io/) as the package manager.

### How Node Apps Work

1. datamitsu downloads and verifies (SHA-256) the configured Node.js archive and extracts it
2. Downloads pnpm from the npm registry
3. Creates an isolated environment at `.apps/node/{appName}/{hash}/`
4. Runs `pnpm install` to set up the package
5. Executes the tool via Node.js

### Defining a Node App

```javascript
apps: {
  eslint: {
    node: {
      packageName: "eslint",
      version: "9.0.0",
      binPath: "node_modules/.bin/eslint",
    },
  },
}
```

### Node.js and pnpm Versions

Both versions are configured on the runtime:

```javascript
node: {
  nodeVersion: "26.2.0",
  pnpmVersion: "11.5.0",
  pnpmHash: "...",
}
```

Node.js versions are cached at `.runtimes/node/{configHash}/`, and pnpm is cached separately. A shared pnpm content-addressable store at `.pnpm-store/` deduplicates packages across apps.

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

Node and UV apps support lock files to ensure reproducible installs across environments. Lock files pin exact dependency versions so that `pnpm install` and `uv sync` produce identical results everywhere.

### Generating a Lock File

Use the `config lockfile` command to generate lock file content:

```bash
datamitsu config lockfile eslint
```

This outputs brotli-compressed, base64-encoded lock file content that you paste into your configuration:

```javascript
eslint: {
  node: {
    packageName: "eslint",
    version: "9.0.0",
    binPath: "node_modules/.bin/eslint",
    lockFile: "br:...",
  },
}
```

### How Lock Files Work

- **Node apps**: The lock file is written as `pnpm-lock.yaml` and pnpm runs with `--frozen-lockfile`, refusing to install if dependencies don't match
- **UV apps**: The lock file is written as `uv.lock` and uv runs with `--locked`, ensuring exact version matching

### Lock Files

Lock files are mandatory for all UV and node apps. If a UV or node app does not have a `lockFile` configured, validation will fail with an error. Use `datamitsu config lockfile <appName>` to generate a lock file for an app.

## Isolated Environments

Each runtime-managed app gets its own isolated environment. This prevents version conflicts between tools:

```
.apps/
├── uv/
│   ├── yamllint/{hash}/     # isolated Python env
│   └── ruff/{hash}/         # separate isolated env
├── node/
│   ├── eslint/{hash}/       # isolated Node.js env
│   └── prettier/{hash}/     # separate isolated env
└── jvm/
    └── openapi-generator/{hash}/  # JAR + metadata
```

The `{hash}` is computed from the runtime config, app config, OS, and architecture. Changing any of these (like upgrading a version) creates a new isolated environment, while the old one remains cached until explicitly cleared.

## Runtime Resolution

When an app references a runtime by name, datamitsu resolves it through this chain:

1. App-level runtime override (if specified)
2. Global default runtime for the app's kind

This allows you to have multiple runtimes of the same kind (e.g., different Node.js versions for different apps) while keeping a sensible default.

## Libc Detection and Alpine Linux

datamitsu detects the host libc at startup using a multi-stage process (ldd output, ELF interpreter, loader paths). This detection feeds into binary app resolution, where musl-specific binaries are preferred on musl systems.

Managed runtimes need extra handling on musl because their upstreams' default channels are glibc-oriented:

- **Node.js**: the default `nodejs.org/dist` archives are glibc-only. The registry therefore carries static musl entries (url + SHA-256) sourced from [unofficial-builds.nodejs.org](https://unofficial-builds.nodejs.org/download/release). On a musl host datamitsu downloads and verifies the musl Node archive directly, so node apps work on Alpine out of the box. musl Node additionally requires `libstdc++` (`apk add libstdc++`). See [Use in Alpine Linux](../how-to/use-in-alpine) for details.
- **Python** (UV): uv downloads glibc Python builds
- **JDK** (JVM): Temurin JDK releases are glibc-only

### Automatic Fallback to System Mode

When running on a musl system (e.g., Alpine Linux), datamitsu automatically detects when a managed runtime lacks a musl-compatible binary and falls back to system mode if the corresponding system binary is available on PATH.

The fallback logic works as follows:

1. Detects the host is musl
2. Checks if the runtime's managed binaries include a musl variant
3. If no musl binary exists, looks for the system binary (`node`, `uv`, or `java`) via PATH
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
    node: { nodeVersion: "26.2.0", pnpmVersion: "11.5.0", pnpmHash: "..." },
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
