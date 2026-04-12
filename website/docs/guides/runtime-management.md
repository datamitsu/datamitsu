---
title: Runtime Management
description: Managing UV (Python), FNM (Node.js), and JVM runtimes with datamitsu
---

# Runtime Management

datamitsu manages language runtimes alongside your tools. Instead of requiring team members to install specific versions of Python, Node.js, or Java, datamitsu downloads and manages these runtimes automatically, creating isolated environments for each tool.

## Runtime Types

datamitsu supports three runtime types:

| Runtime | Language | Package Manager | Use Case                               |
| ------- | -------- | --------------- | -------------------------------------- |
| **UV**  | Python   | uv              | Python tools like yamllint, ruff       |
| **FNM** | Node.js  | pnpm            | npm packages like ESLint, Prettier     |
| **JVM** | Java     | -               | JAR-based tools like openapi-generator |

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

## FNM Runtime (Node.js)

FNM apps use [fnm](https://github.com/Schniz/fnm) to manage Node.js versions and [pnpm](https://pnpm.io/) as the package manager.

### How FNM Apps Work

1. datamitsu ensures FNM is available and installs the configured Node.js version
2. Downloads pnpm from the npm registry
3. Creates an isolated environment at `.apps/fnm/{appName}/{hash}/`
4. Runs `pnpm install` to set up the package
5. Executes the tool via Node.js

### Defining an FNM App

```javascript
apps: {
  eslint: {
    fnm: {
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
fnm: {
  nodeVersion: "22.14.0",
  pnpmVersion: "10.5.2",
  pnpmHash: "...",
}
```

Node.js versions are cached at `.runtimes/fnm-nodes/v{version}/`, and pnpm is cached separately. A shared pnpm content-addressable store at `.pnpm-store/` deduplicates packages across apps.

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

## Lock Files

FNM and UV apps support lock files to ensure reproducible installs across environments. Lock files pin exact dependency versions so that `pnpm install` and `uv sync` produce identical results everywhere.

### Generating a Lock File

Use the `config lockfile` command to generate lock file content:

```bash
datamitsu config lockfile eslint
```

This outputs brotli-compressed, base64-encoded lock file content that you paste into your configuration:

```javascript
eslint: {
  fnm: {
    packageName: "eslint",
    version: "9.0.0",
    binPath: "node_modules/.bin/eslint",
    lockFile: "br:...",
  },
}
```

### How Lock Files Work

- **FNM apps**: The lock file is written as `pnpm-lock.yaml` and pnpm runs with `--frozen-lockfile`, refusing to install if dependencies don't match
- **UV apps**: The lock file is written as `uv.lock` and uv runs with `--locked`, ensuring exact version matching

### Lock Files

Lock files are mandatory for all UV and FNM apps. If a UV or FNM app does not have a `lockFile` configured, validation will fail with an error. Use `datamitsu config lockfile <appName>` to generate a lock file for an app.

## Isolated Environments

Each runtime-managed app gets its own isolated environment. This prevents version conflicts between tools:

```
.apps/
├── uv/
│   ├── yamllint/{hash}/     # isolated Python env
│   └── ruff/{hash}/         # separate isolated env
├── fnm/
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

Managed runtimes (FNM, UV, JVM) do **not** have musl-native binaries available through automatic binary management. Upstream projects do not provide musl-native runtime binaries through the channels datamitsu uses:

- **Node.js** (FNM): Official builds are glibc-only
- **Python** (UV): uv downloads glibc Python builds
- **JDK** (JVM): Temurin JDK releases are glibc-only

### Automatic Fallback to System Mode

When running on a musl system (e.g., Alpine Linux), datamitsu automatically detects when a managed runtime lacks a musl-compatible binary and falls back to system mode if the corresponding system binary is available on PATH.

The fallback logic works as follows:

1. Detects the host is musl
2. Checks if the runtime's managed binaries include a musl variant
3. If no musl binary exists, looks for the system binary (`fnm`, `uv`, or `java`) via PATH
4. If found, automatically switches to system mode using the system binary
5. If not found, falls through to the existing glibc fallback behavior

Note that `fnm` and `uv` are not available in Alpine's default package repositories. The auto-fallback is most useful for the JVM runtime (where `apk add openjdk17` provides the `java` binary). For FNM and UV on Alpine, use [manual system mode](#manual-system-mode) configuration instead.

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
  fnm: {
    kind: "fnm",
    mode: "system",
    system: { command: "fnm" },
    fnm: { nodeVersion: "22.14.0", pnpmVersion: "10.5.2" },
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
