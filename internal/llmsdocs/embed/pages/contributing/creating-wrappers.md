# Creating Wrapper Packages

> Build your own datamitsu config distribution package for your team or organization

This guide explains how to build wrapper packages — the second level of datamitsu's architecture — to distribute standardized tool configurations to your team, company, or the open source community.

## What are Wrapper Packages?

Wrapper packages are language-specific packages (npm, gem, pypi, etc.) that bundle:

- Concrete tool versions and configurations
- Opinionated defaults for linters, formatters, and other tools
- Managed project files (configs, ignore files, etc.)
- Custom configuration logic in JavaScript/TypeScript

Think of wrappers as "datamitsu config distributions" — similar to how lefthook has npm/gem/pypi wrappers around its Go core.

## Two-Level Architecture

Understanding the architecture is crucial to building effective wrappers. To see how datamitsu executes tools internally, see [Architecture: Parallel Execution](../guides/architecture/execution.md).

### Level 1: The Core (datamitsu binary)

The `datamitsu` binary provides platform capabilities:

- Binary management with SHA-256 verification
- Programmable JavaScript configuration engine (goja)
- Config chaining with inheritance
- Tool operations (fix/lint/check)
- Safe managed-config reconciliation
- File patching engine

**The core does NOT provide:**

- Concrete tool versions
- Linter configurations
- Opinionated defaults

### Level 2: Wrappers (your package)

Your wrapper package provides:

- Full tool suite definitions (golangci-lint, eslint, prettier, etc.)
- Configured defaults for all tools
- Managed config files and generation rules
- Project type detection logic
- Custom config functions

## Distribution Model

```
datamitsu core (Go binary)
  ↓
  Installed globally or as dev dependency

Your wrapper package (@company/datamitsu-config)
  ↓
  Distributed via npm/gem/pypi
  Contains: tool versions, configs, managed config definitions

End-user projects
  ↓
  Install: npm install @company/dev-standards
  Config loads: datamitsu.config.ts (optional overrides)
  Run: datamitsu config reconcile && datamitsu lint
```

## Creating Your First Wrapper

### Step 1: Choose a Distribution Channel

Decide how you'll distribute your wrapper:

- **npm** — For JavaScript/TypeScript ecosystems (recommended for web projects)
- **gem** — For Ruby ecosystems
- **pypi** — For Python ecosystems
- **Multiple** — Publish the same config to multiple package managers

Example using npm:

```bash
mkdir my-datamitsu-config
cd my-datamitsu-config
npm init -y
```

### Step 2: Create the Configuration File

Create `config/datamitsu.config.js` (or `.ts`):

```javascript
/**
 * @param {config.Config} prev
 * @returns {config.Config}
 */
function getConfig(prev) {
  return {
    ...prev,
    apps: {
      ...prev.apps,
      // Define your tools here
      "golangci-lint": {
        binary: {
          binaries: {
            linux: {
              amd64: {
                glibc: {
                  url: "https://github.com/golangci/golangci-lint/releases/download/v1.55.0/golangci-lint-1.55.0-linux-amd64.tar.gz",
                  hash: "<sha256-hash>",
                  contentType: "tar.gz",
                  binaryPath: "golangci-lint-1.55.0-linux-amd64/golangci-lint",
                },
              },
            },
            darwin: {
              amd64: {
                unknown: {
                  url: "https://github.com/golangci/golangci-lint/releases/download/v1.55.0/golangci-lint-1.55.0-darwin-amd64.tar.gz",
                  hash: "<sha256-hash>",
                  contentType: "tar.gz",
                  binaryPath: "golangci-lint-1.55.0-darwin-amd64/golangci-lint",
                },
              },
              arm64: {
                unknown: {
                  url: "https://github.com/golangci/golangci-lint/releases/download/v1.55.0/golangci-lint-1.55.0-darwin-arm64.tar.gz",
                  hash: "<sha256-hash>",
                  contentType: "tar.gz",
                  binaryPath: "golangci-lint-1.55.0-darwin-arm64/golangci-lint",
                },
              },
            },
          },
        },
      },
    },
    tools: {
      ...prev.tools,
      "golangci-lint": {
        name: "golangci-lint",
        operations: {
          lint: {
            app: "golangci-lint",
            args: ["run"],
            scope: "per-project",
          },
        },
        projectTypes: ["golang-package"],
      },
    },

    managedConfigs: {
      ...prev.managedConfigs,
      // Add managed config files
      ".golangci.yml": {
        content: () => `
linters:
  enable:
    - gofmt
    - govet
    - staticcheck
`,
      },
    },
  };
}

globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

### Step 3: Use the `--before-config` Flag

The `--before-config` flag tells datamitsu to load your wrapper config before auto-discovering `datamitsu.config.js` at the git root.

This enables the layering:

```
embedded default config (datamitsu core)
  ↓
--before-config (your wrapper) ← loaded first
  ↓
auto-discovered config (user's datamitsu.config.js) ← can override wrapper
  ↓
--config (explicit overrides)
```

### Step 4: Create a Binary Wrapper

Create `bin/datamitsu` (executable shell script):

```bash
#!/bin/sh
# Find the datamitsu binary
DATAMITSU_BIN=$(which datamitsu)

if [ -z "$DATAMITSU_BIN" ]; then
  echo "Error: datamitsu binary not found in PATH"
  exit 1
fi

# Get the directory of this script
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONFIG_DIR="$(dirname "$SCRIPT_DIR")/config"

# Execute datamitsu with our config loaded before user config
exec "$DATAMITSU_BIN" --before-config "$CONFIG_DIR/datamitsu.config.js" "$@"
```

Make it executable:

```bash
chmod +x bin/datamitsu
```

### Step 5: Configure package.json

```json
{
  "name": "@company/datamitsu-config",
  "version": "1.0.0",
  "description": "Company-wide datamitsu tool configurations",
  "bin": {
    "datamitsu": "./bin/datamitsu"
  },
  "files": ["bin/", "config/"],
  "peerDependencies": {
    "datamitsu": "^0.1.0"
  },
  "keywords": ["datamitsu", "linting", "configuration", "tools"]
}
```

### Step 6: Publish Your Package

```bash
# Test locally first
npm link
cd ../test-project
npm link @company/datamitsu-config

# When ready, publish
npm publish
```

## Config Chaining and Inheritance

One of datamitsu's most powerful features is config chaining. Each config layer receives the previous layer as input.

### Extending a Base Config

```javascript
function getConfig(prev) {
  return {
    ...prev,
    apps: {
      ...prev.apps,
      // Add new tools
      prettier: {/* ... */},

      // Override existing tool
      "golangci-lint": {
        ...prev.apps["golangci-lint"],
        operations: {
          lint: {
            args: ["run", "--timeout=5m"], // Override args
          },
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

### Removing Tools

```javascript
function getConfig(prev) {
  const { unwantedTool, ...remainingApps } = prev.apps;

  return {
    ...prev,
    apps: remainingApps,
  };
}
globalThis.getConfig = getConfig;
```

### Conditional Configuration

Use JavaScript logic for dynamic configuration:

```javascript
function getConfig(prev) {
  const config = {
    ...prev,
    apps: { ...prev.apps },
  };

  // Add tools based on environment
  if (process.env.CI === "true") {
    config.apps["some-ci-tool"] = {/* ... */};
  }

  // Adjust settings based on OS
  if (process.platform === "darwin") {
    config.apps["golangci-lint"].operations.lint.args.push("--max-issues-per-linter=100");
  }

  return config;
}
globalThis.getConfig = getConfig;
```

## Remote Configs

Wrapper packages can reference remote configuration files for additional layering:

```javascript
/**
 * @returns {Array<{url: string, hash: string}>}
 */
function getRemoteConfigs() {
  return [
    {
      url: "https://config.company.com/datamitsu/base.ts",
      hash: "abc123...", // SHA-256 hash (mandatory)
    },
  ];
}
globalThis.getRemoteConfigs = getRemoteConfigs;

function getConfig(prev) {
  // prev includes the remote config merged in
  return {
    ...prev,
    // Your overrides here
  };
}

globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

Remote configs are resolved depth-first before your config is evaluated.

## Agent Prompts

datamitsu publishes two Markdown guides for AI agents through `sharedStorage`. The binary that evaluates the configuration produces both, so they always describe the version that loads it. The default configuration writes neither of them anywhere: your configuration decides where each one goes.

| Key                              | Written for                                                                                                                           | Where it belongs                                               |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- |
| `datamitsu-agent-prompt`         | Agents working in a repository that **uses** a datamitsu configuration                                                                | The agent instructions your wrapper ships to its consumers     |
| `datamitsu-config-author-prompt` | Agents working in a repository that **writes** one: a wrapper, or a project configuration that defines its own apps, tools or configs | The authoring repository only — never shipped to its consumers |

### Shipping the Consumer Prompt

Write the prompt into a project file through a managed config:

```javascript
function getConfig(input) {
  const agentPrompt = input.sharedStorage["datamitsu-agent-prompt"];

  return {
    ...input,
    managedConfigs: {
      ...input.managedConfigs,
      "AGENTS.md": {
        content: () => agentPrompt,
        scope: "git-root",
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

### Extending It with Custom Instructions

Create a bundle that combines the base prompt with company-specific guidance:

```javascript
function getConfig(input) {
  const basePrompt = input.sharedStorage["datamitsu-agent-prompt"];

  const customInstructions = `

## Company-Specific Guidelines

- Follow our coding standards at https://company.com/standards
- Always use our custom linter configurations
- Prefix commit messages with ticket numbers (e.g., "JIRA-123: Fix bug")
- Use TypeScript for all new JavaScript code
`;

  return {
    ...input,
    bundles: {
      ...input.bundles,
      "company-agents": {
        version: "1.0.0",
        files: {
          "AGENTS.md": basePrompt + customInstructions,
        },
        links: {
          "company-agents": "AGENTS.md",
        },
      },
    },
    managedConfigs: {
      ...input.managedConfigs,
      "AGENTS.md": {
        linkTarget: ".datamitsu/company-agents",
        scope: "git-root",
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

When datamitsu updates the prompt, `datamitsu init` rebuilds the bundle, so every project picks up the change without anyone syncing text by hand.

### Adding the Configuration-Author Guide

The configuration-author guide tells an agent how to write a layer correctly for this version of datamitsu: the JavaScript runtime, mandatory hashes and lock files, the `devtools pull-*` workflow, validation rules, and a list of recent changes a configuration should review. Because `datamitsu init` refreshes it on every upgrade, an agent working on your wrapper learns about new features from the binary itself.

Add it where the configuration is written. For a wrapper, that is the `datamitsu.config.*` at the wrapper repository's own git root — not the configuration the wrapper publishes, which would ship the guide to every consumer. Merge the bundle into that file's `getConfig`; the file keeps its own `getMinVersion()`, which every configuration must export:

```javascript
function getConfig(input) {
  return {
    ...input,
    bundles: {
      ...input.bundles,
      "config-author-guide": {
        files: {
          "datamitsu-config-author.md":
            input.sharedStorage?.["datamitsu-config-author-prompt"] ?? "",
        },
        links: {
          "ai/agents/datamitsu-config-author.md": "datamitsu-config-author.md",
        },
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

After `datamitsu init`, point the repository's own agent instructions at it once:

```markdown
Before changing the datamitsu configuration, read [.datamitsu/ai/agents/datamitsu-config-author.md](.datamitsu/ai/agents/datamitsu-config-author.md).
```

A project whose own `datamitsu.config.*` defines apps, tools or managed configs can adopt it the same way.

## Testing Your Wrapper

### Local Testing

```bash
# In your wrapper directory
npm link

# In a test project
npm link @company/datamitsu-config

# Test commands
datamitsu init
datamitsu check
```

### CI Testing

Create a test project in your wrapper repo:

```
my-datamitsu-config/
├── config/
│   └── datamitsu.config.js
├── bin/
│   └── datamitsu
├── test-project/
│   ├── package.json (depends on your wrapper)
│   └── sample-files/
└── package.json
```

In CI:

```yaml
test:
  script:
    - npm install
    - cd test-project
    - datamitsu init
    - datamitsu check
```

## Versioning Strategy

### Semantic Versioning

Follow semver for your wrapper:

- **Major** — Breaking changes to config structure, removed tools
- **Minor** — New tools, new features, non-breaking changes
- **Patch** — Bug fixes, hash updates, documentation

### Example Changelog

```markdown
## 2.0.0 (Breaking)

- Removed deprecated `tslint` (use `eslint` instead)
- Changed `.prettierrc` format to use ES modules

## 1.5.0

- Added `hadolint` for Dockerfile linting
- Added `shellcheck` for shell script linting
- Updated `golangci-lint` to v1.55.0

## 1.4.1

- Updated `eslint` hash for security patch
- Fixed `prettier` args on Windows
```

## Publishing to Multiple Package Managers

You can publish the same config to npm, gem, and pypi simultaneously:

### npm (JavaScript)

```json
{
  "name": "@company/datamitsu-config",
  "bin": {
    "datamitsu": "./bin/datamitsu"
  }
}
```

### gem (Ruby)

```ruby
# datamitsu-config.gemspec
Gem::Specification.new do |spec|
  spec.name          = "datamitsu-config"
  spec.version       = "1.0.0"
  spec.executables   = ["datamitsu"]
end
```

### pypi (Python)

```python
# setup.py
setup(
    name="datamitsu-config",
    version="1.0.0",
    scripts=["bin/datamitsu"],
)
```

The config file and bin wrapper remain the same across all three.

## Real-World Example: shibanet0/datamitsu-config

The author maintains `shibanet0/datamitsu-config` as a reference wrapper:

- **npm package** — Distributed via npm for JavaScript projects
- **Full tool suite** — golangci-lint, prettier, eslint, shellcheck, hadolint, etc.
- **Project detection** — Detects Go, TypeScript, Rust, Dockerfile, etc.
- **Managed configs** — Symlinks for ESLint, Prettier configs
- **Remote configs** — Layers multiple config sources

[View source on GitHub](https://github.com/shibanet0/datamitsu-config)

## Best Practices

### Security

- **Always specify SHA-256 hashes** for all binaries (mandatory)
- **Pin exact versions** — Don't use "latest" or version ranges
- **Verify hashes** — Use `datamitsu devtools pull-github --verify-extraction` during development
- **Keep hashes updated** — Monitor security advisories for tool updates

### Maintainability

- **Document your tools** — Explain why each tool is included
- **Keep configs organized** — Separate concerns (linting, formatting, git hooks)
- **Test on all platforms** — Ensure Linux, macOS, Windows support
- **Provide examples** — Show users how to override your defaults
- **Automate version updates** — For version update workflows, see [Maintaining Wrapper Packages](../how-to/maintain-wrapper.md)

### User Experience

- **Minimize required setup** — Pre-configure as much as possible
- **Support customization** — Users should be able to override any setting
- **Migration guides** — Document breaking changes clearly
- **Clear error messages** — Help users fix common issues

## Next Steps

- Read [Using Wrappers](../guides/using-wrappers.md) to understand the user experience
- See [Configuration API Reference](../reference/configuration-api.md) for complete config options
- Check out [Examples](../examples/multiple-versions.md) for advanced patterns
- Join discussions on [GitHub](https://github.com/datamitsu/datamitsu/discussions)

Building wrapper packages is how datamitsu achieves its mission: configuration standardization across teams and projects. Your wrapper becomes the single source of truth for how your team builds software.
