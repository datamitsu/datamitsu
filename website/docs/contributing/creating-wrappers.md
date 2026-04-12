---
title: Creating Wrapper Packages
description: Build your own datamitsu config distribution package for your team or organization
---

# Creating Wrapper Packages

This guide explains how to build wrapper packages — the second level of datamitsu's architecture — to distribute standardized tool configurations to your team, company, or the open source community.

## What are Wrapper Packages?

Wrapper packages are language-specific packages (npm, gem, pypi, etc.) that bundle:

- Concrete tool versions and configurations
- Opinionated defaults for linters, formatters, and other tools
- Project setup files (configs, ignore files, etc.)
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
- Setup orchestration
- File patching engine

**The core does NOT provide:**

- Concrete tool versions
- Linter configurations
- Opinionated defaults

### Level 2: Wrappers (your package)

Your wrapper package provides:

- Full tool suite definitions (golangci-lint, eslint, prettier, etc.)
- Configured defaults for all tools
- Setup files and configurations
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
  Contains: tool versions, configs, setup files

End-user projects
  ↓
  Install: npm install @company/dev-standards
  Config loads: datamitsu.config.ts (optional overrides)
  Run: datamitsu setup && datamitsu check
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
 * @param {import('datamitsu').Config} prev
 * @returns {import('datamitsu').Config}
 */
function getConfig(prev) {
  return {
    ...prev,
    apps: {
      ...prev.apps,
      // Define your tools here
      "golangci-lint": {
        type: "binary",
        binary: {
          binaries: {
            linux: {
              amd64: {
                url: "https://github.com/golangci/golangci-lint/releases/download/v1.55.0/golangci-lint-1.55.0-linux-amd64.tar.gz",
                hash: "<sha256-hash>",
                contentType: "tar.gz",
                binaryPath: "golangci-lint-1.55.0-linux-amd64/golangci-lint",
              },
            },
            darwin: {
              amd64: {
                url: "https://github.com/golangci/golangci-lint/releases/download/v1.55.0/golangci-lint-1.55.0-darwin-amd64.tar.gz",
                hash: "<sha256-hash>",
                contentType: "tar.gz",
                binaryPath: "golangci-lint-1.55.0-darwin-amd64/golangci-lint",
              },
              arm64: {
                url: "https://github.com/golangci/golangci-lint/releases/download/v1.55.0/golangci-lint-1.55.0-darwin-arm64.tar.gz",
                hash: "<sha256-hash>",
                contentType: "tar.gz",
                binaryPath: "golangci-lint-1.55.0-darwin-arm64/golangci-lint",
              },
            },
          },
        },
        operations: {
          lint: {
            args: ["run"],
          },
        },
      },
    },

    setup: {
      ...prev.setup,
      // Add setup files
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
globalThis.getMinVersion = () => "1.0.0";
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
      prettier: {
        /* ... */
      },

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
    config.apps["some-ci-tool"] = {
      /* ... */
    };
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

## Using the Standard Agent Prompt

datamitsu core provides a standard agent prompt (AGENTS.md content) that wrapper packages can reference and customize. This ensures consistent AI assistant instructions across all projects using datamitsu while allowing wrappers to extend them with company-specific guidance.

### Accessing the Prompt

The prompt is available via shared storage:

```javascript
function getConfig(input) {
  const agentPrompt = input.sharedStorage["datamitsu-agent-prompt"];

  return {
    ...input,
    init: {
      ...input.init,
      "AGENTS.md": {
        content: () => agentPrompt,
        scope: "git-root",
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

### Pattern 1: Use Directly

The simplest approach is to use the default prompt as-is:

```javascript
function getConfig(input) {
  // Prompt is already in AGENTS.md by default
  // No changes needed unless you want to customize
  return input;
}
globalThis.getConfig = getConfig;
```

### Pattern 2: Extend with Custom Instructions

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
    init: {
      ...input.init,
      "AGENTS.md": {
        linkTarget: ".datamitsu/company-agents",
        scope: "git-root",
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

### Pattern 3: Reference Core Bundle

For demo purposes, datamitsu core creates a bundle with the agent prompt. You can reference it directly:

```javascript
function getConfig(input) {
  return {
    ...input,
    init: {
      ...input.init,
      "AGENTS.md": {
        linkTarget: ".datamitsu/datamitsu-guide",
        scope: "git-root",
      },
    },
  };
}
globalThis.getConfig = getConfig;
```

After running `datamitsu init`, users will see `.datamitsu/datamitsu-guide.md` with the standard prompt.

### Benefits

- **Consistency**: All projects get the same base agent instructions
- **Extensibility**: Wrappers can add company/team-specific guidance
- **Automatic updates**: When datamitsu updates the prompt, wrappers inherit changes automatically
- **Zero maintenance**: No need to duplicate or manually sync prompt content

### Example Use Cases

1. **Startup defaults**: Use the default prompt for standard projects
2. **Enterprise standards**: Extend with company coding policies and tool requirements
3. **Team workflows**: Customize with team-specific git workflow or review processes
4. **Multi-environment**: Different prompts for development vs. production contexts

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
