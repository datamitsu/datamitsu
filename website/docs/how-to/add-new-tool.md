---
title: Add a New Tool
description: Step-by-step guide to adding binary, UV, FNM, and JVM tools to datamitsu
---

# Add a New Tool

This guide walks through adding each type of tool to your datamitsu configuration.

## Adding a Binary App

Binary apps are standalone executables downloaded directly from release URLs.

### 1. Find the release URLs

Locate the download URLs for each platform you need. Most tools publish releases on GitHub with platform-specific archives.

### 2. Get the SHA-256 hashes

Download each archive and compute its SHA-256 hash:

```bash
curl -L -o tool.tar.gz "https://github.com/org/tool/releases/download/v1.0.0/tool_linux_amd64.tar.gz"
sha256sum tool.tar.gz
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
                url: "https://github.com/org/mytool/releases/download/v1.0.0/mytool_darwin_amd64.tar.gz",
                hash: "abc123...",
                contentType: "tar.gz",
              },
              arm64: {
                url: "https://github.com/org/mytool/releases/download/v1.0.0/mytool_darwin_arm64.tar.gz",
                hash: "def456...",
                contentType: "tar.gz",
              },
            },
            linux: {
              amd64: {
                url: "https://github.com/org/mytool/releases/download/v1.0.0/mytool_linux_amd64.tar.gz",
                hash: "789abc...",
                contentType: "tar.gz",
              },
            },
          },
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

### 4. Test the tool

```bash
datamitsu init
datamitsu exec mytool --version
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
```

The `runtime` field references a UV runtime defined in `runtimes`. The default configuration already includes one.

### 3. Generate a lock file (recommended)

Lock files ensure reproducible installs:

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
datamitsu exec yamllint --version
```

## Adding an FNM App (Node.js)

FNM apps are npm packages managed with pnpm in isolated Node.js environments.

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
        fnm: {
          packageName: "prettier",
          version: "3.2.0",
          binPath: "node_modules/.bin/prettier",
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

### 3. Generate a lock file (recommended)

```bash
datamitsu config lockfile prettier
```

Add the output to your app definition as the `lockFile` field.

### 4. Add managed config links (optional)

If the app provides configuration files that your projects need, add `links`:

```javascript
prettier: {
  fnm: {
    packageName: "prettier",
    version: "3.2.0",
    binPath: "node_modules/.bin/prettier",
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
datamitsu exec prettier --version
```

## Adding a JVM App

JVM apps are JAR files executed with a managed JDK.

### 1. Find the JAR URL

Locate the direct download URL for the JAR file, typically from Maven Central or the project's releases.

### 2. Get the SHA-256 hash

```bash
curl -L -o tool.jar "https://repo1.maven.org/maven2/org/example/tool/1.0.0/tool-1.0.0.jar"
sha256sum tool.jar
```

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
```

### 4. Test the tool

```bash
datamitsu init
datamitsu exec openapi-generator version
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
