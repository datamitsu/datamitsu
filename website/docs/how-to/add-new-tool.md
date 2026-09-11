---
title: Add a New Tool
description: Step-by-step guide to adding binary, UV, Node, JVM, and Go tools to datamitsu
---

# Add a New Tool

This guide walks through adding each type of tool to your datamitsu configuration.

## Adding a Binary App

Binary apps are standalone executables downloaded directly from release URLs.

### 1. Find the release URLs

Locate the download URLs for each platform you need. Most tools publish releases on GitHub with platform-specific archives.

### 2. Obtain and verify the SHA-256 hashes

Obtain each archive's SHA-256 from an authenticated upstream release manifest
or signature before downloading it. Never treat a digest computed only after an
untrusted download as the trust anchor. Then verify the download against that
expected value:

```bash
EXPECTED_SHA256="<sha256-from-the-upstream-manifest>"
curl -L -o tool.tar.gz "https://github.com/org/tool/releases/download/v1.0.0/tool_linux_amd64.tar.gz"
printf '%s  %s\n' "$EXPECTED_SHA256" tool.tar.gz | sha256sum --check -
```

### 3. Add the app definition

Add the app to `apps` in your config file:

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  return {
    ...config,
    apps: {
      ...config.apps,
      mytool: {
        binary: {
          binaries: {
            darwin: {
              amd64: {
                unknown: {
                  url: "https://github.com/org/mytool/releases/download/v1.0.0/mytool_darwin_amd64.tar.gz",
                  hash: "<sha256>",
                  contentType: "tar.gz",
                  binaryPath: "mytool",
                },
              },
              arm64: {
                unknown: {
                  url: "https://github.com/org/mytool/releases/download/v1.0.0/mytool_darwin_arm64.tar.gz",
                  hash: "<sha256>",
                  contentType: "tar.gz",
                  binaryPath: "mytool",
                },
              },
            },
            linux: {
              amd64: {
                glibc: {
                  url: "https://github.com/org/mytool/releases/download/v1.0.0/mytool_linux_amd64.tar.gz",
                  hash: "<sha256>",
                  contentType: "tar.gz",
                  binaryPath: "mytool",
                },
              },
            },
          },
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

### 4. Test the tool

```bash
datamitsu init
datamitsu exec mytool -- --version
```

### 5. Verify cross-platform hashes (optional)

Use `devtools verify-all` to download and verify hashes for all configured platforms:

```bash
datamitsu devtools verify-all
```

## Adding a UV App (Python)

UV apps are Python packages installed in isolated environments.

### 1. Find the package on PyPI

Check that your tool is available on [PyPI](https://pypi.org) and note the version.

### 2. Add the app definition

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  return {
    ...config,
    apps: {
      ...config.apps,
      yamllint: {
        uv: {
          packageName: "yamllint",
          version: "1.35.1",
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

The `runtime` field references a UV runtime defined in `runtimes`. The default configuration already includes one.

### 3. Generate the mandatory lock file

The `config lockfile` command deliberately permits this app's initially missing
`lockFile` so it can generate one. All normal commands reject the config until
you paste the result back:

```bash
datamitsu config lockfile yamllint
```

Copy the output into your app definition:

```javascript
yamllint: {
  uv: {
    packageName: "yamllint",
    version: "1.35.1",
    lockFile: "br:...",  // paste the output here
  },
},
```

### 4. Test the tool

```bash
datamitsu init
datamitsu exec yamllint -- --version
```

## Adding a Node App (Node.js)

Node apps are npm packages managed with pnpm in isolated Node.js environments.

### 1. Find the package on npm

Check that your tool is available on [npm](https://www.npmjs.com) and note the version.

### 2. Add the app definition

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  return {
    ...config,
    apps: {
      ...config.apps,
      prettier: {
        node: {
          packageName: "prettier",
          version: "3.2.0",
          binPath: "node_modules/.bin/prettier",
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

### 3. Generate the mandatory lock file

```bash
datamitsu config lockfile prettier
```

Add the output to your app definition as the `lockFile` field.

### 4. Add managed config links (optional)

If the app provides configuration files that your projects need, add `links`:

```javascript
prettier: {
  node: {
    packageName: "prettier",
    version: "3.2.0",
    binPath: "node_modules/.bin/prettier",
    lockFile: "br:...",
  },
  links: {
    "prettier-config": "dist/prettier.config.js",
  },
},
```

After `datamitsu init`, this creates a symlink at `.datamitsu/prettier-config` pointing to the file inside the app's install directory.

### 5. Test the tool

```bash
datamitsu init
datamitsu exec prettier -- --version
```

## Adding a JVM App

JVM apps are JAR files executed with a managed JDK.

### 1. Find the JAR URL

Locate the direct download URL for the JAR file, typically from Maven Central or the project's releases.

### 2. Obtain the SHA-256 hash

Obtain the digest from the repository's authenticated checksum metadata or a
signed upstream release manifest. As with binary apps, set `jarHash` before
allowing datamitsu to download the JAR; a missing hash is a config error.

### 3. Add the app definition

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  return {
    ...config,
    apps: {
      ...config.apps,
      "openapi-generator": {
        jvm: {
          jarUrl:
            "https://repo1.maven.org/maven2/org/openapitools/openapi-generator-cli/7.0.0/openapi-generator-cli-7.0.0.jar",
          jarHash: "...",
          version: "7.0.0",
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

### 4. Test the tool

```bash
datamitsu init
datamitsu exec openapi-generator version
```

## Adding a Go App

Go apps are built from a pinned module version with a managed, hash-verified Go
SDK. Their lock payload contains both `go.mod` and `go.sum`.

### 1. Add the package and version

The initial definition may omit `lockFile` only while running the generation
command:

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  return {
    ...config,
    apps: {
      ...config.apps,
      govulncheck: {
        go: {
          packageName: "golang.org/x/vuln/cmd/govulncheck",
          version: "v1.3.0",
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

### 2. Generate the mandatory lock file

```bash
datamitsu config lockfile govulncheck
```

Paste the output into the app:

```javascript
govulncheck: {
  go: {
    packageName: "golang.org/x/vuln/cmd/govulncheck",
    version: "v1.3.0",
    lockFile: "br:...", // contains go.mod + go.sum
  },
},
```

The subsequent build uses `go build -trimpath -mod=readonly`; dependency drift
that would modify either module file fails instead.

### 3. Test the tool

```bash
datamitsu init
datamitsu exec govulncheck -- --version
```

## Wiring a Tool to the Tooling System

After adding an app, you can wire it into the fix/lint/check workflow by adding a tool definition:

```javascript
const tools = {
  ...config.tools,
  mytool: {
    name: "mytool",
    projectTypes: ["golang-package"],
    operations: {
      lint: {
        app: "mytool",
        args: ["check", "{files}"],
        scope: "per-project",
        globs: ["**/*.go"],
      },
      fix: {
        app: "mytool",
        args: ["fix", "{files}"],
        scope: "per-project",
        globs: ["**/*.go"],
      },
    },
  },
};
```

Now `datamitsu check` will include your tool in its workflow. See the [Tooling System guide](/docs/guides/tooling-system) for details on scopes, placeholders, and environment variables.

## Version Checks

Add a `versionCheck` to enable `datamitsu devtools verify-all` to check your tool:

```javascript
mytool: {
  binary: {
    // ... binaries definition
  },
  versionCheck: {
    args: ["version"],  // default is ["--version"]
  },
}
```

Set `versionCheck: { disabled: true }` for tools that don't support version reporting.
