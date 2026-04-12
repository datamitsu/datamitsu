---
title: Using Wrapper Packages
description: How to install and use datamitsu wrapper packages in your projects
---

# Using Wrapper Packages

Wrapper packages are pre-configured bundles of tools and configurations distributed via package managers (npm, gem, pypi). They allow teams to standardize development tooling across all projects with a single dependency.

## What are Wrapper Packages?

Instead of configuring linters, formatters, and other tools separately in each project, you install a wrapper package that brings everything pre-configured:

```bash
# Traditional approach (manual per project)
npm install eslint prettier @typescript-eslint/parser @typescript-eslint/eslint-plugin
# Then create .eslintrc.js, .prettierrc, etc.

# With datamitsu wrapper
npm install @company/datamitsu-config
datamitsu setup
# Everything configured automatically
```

Wrapper packages contain:

- Tool definitions (versions, download URLs, hashes)
- Pre-configured settings (ESLint rules, Prettier options, etc.)
- Setup files (configs, ignore files, git hooks)
- Project-specific customization logic

## Installing a Wrapper Package

### Step 1: Install datamitsu

First, ensure you have the datamitsu binary installed. See [Installation](../getting-started/installation.md).

### Step 2: Install the Wrapper Package

Install your team's wrapper package as a dev dependency:

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

<Tabs>
  <TabItem value="pnpm" label="pnpm" default>

    ```bash
    pnpm add -D @company/datamitsu-config
    ```

  </TabItem>
  <TabItem value="npm" label="npm">

    ```bash
    npm install --save-dev @company/datamitsu-config
    ```

  </TabItem>
  <TabItem value="yarn" label="yarn">

    ```bash
    yarn add -D @company/datamitsu-config
    ```

  </TabItem>
  <TabItem value="bun" label="bun">

    ```bash
    bun add -D @company/datamitsu-config
    ```

  </TabItem>
</Tabs>

The wrapper package typically includes a `datamitsu` binary in its bin/ folder that wraps the core binary with `--before-config`.

### Step 3: Initialize Tools

Download all configured tools:

```bash
# Using the wrapper's binary
npx datamitsu init

# Or if datamitsu is globally installed
datamitsu --before-config node_modules/@company/datamitsu-config/config/datamitsu.config.js init
```

This downloads and caches all binaries defined in the wrapper package.

### Step 4: Run Setup

Generate configuration files:

```bash
npx datamitsu setup
```

This creates config files (`.eslintrc.js`, `.prettierrc`, `.golangci.yml`, etc.) in your project based on the wrapper's setup definitions.

### Step 5: Run Checks

Run linters and fixers:

```bash
# Fix automatically fixable issues
npx datamitsu fix

# Run lint checks
npx datamitsu lint

# Run both (fix first, then lint)
npx datamitsu check
```

## Using the Wrapper Package

Once installed, the wrapper package becomes your team's standard toolchain.

### Running Commands

Use the wrapper's `datamitsu` binary:

```bash
# All commands automatically use the wrapper config
npx datamitsu init
npx datamitsu setup
npx datamitsu check
npx datamitsu exec golangci-lint run
```

### Integrating with npm Scripts

Add datamitsu commands to your `package.json`:

```json
{
  "scripts": {
    "lint": "datamitsu lint",
    "fix": "datamitsu fix",
    "check": "datamitsu check",
    "setup": "datamitsu setup"
  }
}
```

Then run via your package manager:

```bash
npm run lint
npm run fix
npm run check
```

### Integrating with CI/CD

Pre-cache tools in Docker for fast CI builds:

```dockerfile
FROM node:20-alpine

# Install wrapper package globally
RUN npm install -g @company/datamitsu-config

# Download ALL tools to global cache
RUN datamitsu init --all

# This layer is cached - only re-runs when tools change

# Copy project files
COPY . /app
WORKDIR /app

# Run checks (tools already cached)
RUN datamitsu check
```

Only changed tools re-download on updates, dramatically speeding up CI builds.

## Customizing Wrapper Configs

Wrapper packages provide opinionated defaults, but you can override them in your project.

### Project-Level Config

Create `datamitsu.config.ts` at your git root:

```typescript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(prev) {
  return {
    ...prev,
    apps: {
      ...prev.apps,
      // Disable a tool from the wrapper
      unwantedTool: undefined,

      // Override tool settings
      "golangci-lint": {
        ...prev.apps["golangci-lint"],
        operations: {
          lint: {
            args: ["run", "--timeout=10m"], // Longer timeout for this project
          },
        },
      },
    },
  };
}

globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

### Replace Setup Files

Override the wrapper's setup files by providing your own:

```typescript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(prev) {
  return {
    ...prev,
    setup: {
      ...prev.setup,
      ".eslintrc.js": {
        content: () => `
export default {
  // Your custom ESLint config
  rules: {
    "no-console": "error",
  },
};
`,
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

### Add Project-Specific Tools

Extend the wrapper with tools unique to your project:

```typescript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(prev) {
  return {
    ...prev,
    apps: {
      ...prev.apps,
      "custom-linter": {
        type: "binary",
        binary: {
          binaries: {
            linux: {
              amd64: {
                url: "https://example.com/custom-linter-linux-amd64.tar.gz",
                hash: "<sha256>",
                contentType: "tar.gz",
                binaryPath: "custom-linter",
              },
            },
          },
        },
        operations: {
          lint: {
            args: ["check"],
          },
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

## Migration-Free Updates

One of datamitsu's key features is migration-free updates. Update your wrapper package without breaking your project customizations.

### How It Works

The patching mechanism merges new defaults with your project-specific changes:

1. **Wrapper provides base config** — Tool versions, default settings
2. **Your project customizes** — Overrides specific settings
3. **Wrapper updates** — New tool versions, new defaults
4. **Re-run setup** — Customizations preserved, new defaults applied

### Example Workflow

```bash
# Initial install
npm install @company/datamitsu-config@1.0.0
datamitsu setup

# Customize .eslintrc.js manually
# ... make changes ...

# Months later: wrapper updates
npm update @company/datamitsu-config  # Now 2.0.0

# Re-run setup
datamitsu setup

# Your customizations preserved
# New defaults from wrapper applied
```

### What Gets Updated

- **Tool versions** — Automatically updated to wrapper's versions
- **New tools** — Added to your project if configured in wrapper
- **Default configs** — Merged with your customizations
- **Ignore patterns** — Merged with existing patterns

### What Gets Preserved

- **Manual customizations** — Changes you made to generated files
- **Project-specific overrides** — Settings in `datamitsu.config.ts`
- **Custom tools** — Tools you added locally

## Finding Wrapper Packages

### Official Wrappers

- **[@shibanet0/datamitsu-config](https://github.com/shibanet0/datamitsu-config)** — Reference implementation with Go, TypeScript, Rust, Docker tooling

### Company Wrappers

Your company may provide internal wrapper packages:

- `@company/dev-standards` — Company-wide tool standards
- `@team/frontend-config` — Team-specific configurations
- `@project/shared-config` — Project template configurations

### Community Wrappers

Search on npm, rubygems, or pypi for third-party wrappers:

```bash
npm search datamitsu
```

## Example: Using shibanet0/datamitsu-config

Install the reference wrapper:

```bash
npm install --save-dev shibanet0/datamitsu-config
```

Initialize and setup:

```bash
npx datamitsu init
npx datamitsu setup
```

This installs and configures:

- **Go tools** — golangci-lint
- **TypeScript tools** — eslint, prettier, tsc
- **Docker tools** — hadolint
- **Shell tools** — shellcheck
- **Git hooks** — lefthook

All pre-configured with opinionated defaults.

Customize for your project:

```typescript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

// datamitsu.config.ts
function getConfig(prev) {
  return {
    ...prev,
    apps: {
      ...prev.apps,
      // Use stricter golangci-lint timeout
      "golangci-lint": {
        ...prev.apps["golangci-lint"],
        operations: {
          lint: {
            args: ["run", "--timeout=5m"],
          },
        },
      },
    },
  };
}

globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

## Docker Optimization

Wrapper packages enable powerful Docker caching strategies.

### Pre-Cache All Tools

In your Dockerfile:

```dockerfile
FROM node:20-alpine

# Install wrapper globally
RUN npm install -g @company/datamitsu-config

# Download all tools (--all flag)
RUN datamitsu init --all

# This layer is cached until wrapper version changes
# Tools are stored in global cache, available to all projects

# Later: copy your project
COPY . /app
WORKDIR /app

# Install project dependencies
RUN npm install

# Run checks (tools already cached)
RUN datamitsu check
```

### Incremental Updates

When the wrapper updates:

- Only changed tools re-download
- Unchanged tools remain cached in the layer
- No full re-download on every build

This can reduce CI build times from minutes to seconds.

## Troubleshooting

### Wrapper Binary Not Found

If `npx datamitsu` doesn't work after installing the wrapper:

```bash
# Check if wrapper installed correctly
ls node_modules/@company/datamitsu-config/bin/datamitsu

# Use explicit path
./node_modules/.bin/datamitsu init

# Or install datamitsu globally
npm install -g datamitsu
datamitsu --before-config node_modules/@company/datamitsu-config/config/datamitsu.config.js init
```

### Tool Versions Conflict

If the wrapper provides tool versions that conflict with your needs:

```typescript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

// Override specific tool version in datamitsu.config.ts
function getConfig(prev) {
  return {
    ...prev,
    apps: {
      ...prev.apps,
      "golangci-lint": {
        ...prev.apps["golangci-lint"],
        binary: {
          binaries: {
            linux: {
              amd64: {
                url: "https://github.com/golangci/golangci-lint/releases/download/v1.60.0/...",
                hash: "<new-hash>",
                contentType: "tar.gz",
                binaryPath: "...",
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

### Setup Files Not Generated

If `datamitsu setup` doesn't create expected files:

1. Check that the wrapper defines setup files in `setup` config
2. Verify you're in a git repository (setup only runs in git repos)
3. Check project type detection (setup may be conditional)

### Cache Issues

Clear cache if tools behave unexpectedly:

```bash
# Clear project cache
datamitsu cache clear

# Clear all project caches
datamitsu cache clear --all

# Clear global store (binaries, runtimes)
datamitsu store clear
```

## Next Steps

- [Maintaining Wrapper Packages](../how-to/maintain-wrapper.md) — If you're maintaining a wrapper, learn about version update workflows
- [Creating Wrappers](../contributing/creating-wrappers.md) — Build your own wrapper package
- [Configuration API](../reference/configuration-api.md) — Full config reference for customization
- [CLI Commands](../reference/cli-commands.md) — Complete command reference
- [Core Concepts](../getting-started/core-concepts.md) — Understand datamitsu architecture

Wrapper packages are the key to datamitsu's value: install once, get everything configured. Build your team's wrapper to pay the configuration tax only once.
