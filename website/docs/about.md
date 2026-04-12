---
title: About datamitsu
description: Learn why datamitsu exists and how it helps teams standardize their development toolchains
---

# About datamitsu

## Your toolchain deserves a home

Every stack comes with a configuration tax. You pay it on the first project, then the second, then every time a tool updates—and it breaks differently in each repo.

**datamitsu exists so you pay this tax only once.**

Not a boilerplate, not scattered across projects, not reinvented from scratch every time. datamitsu gives your toolchain one home — versioned, composable, and always one command away.

## What is datamitsu?

datamitsu is a **platform for building reproducible, security-first development tool distributions.**

It's NOT just another task runner or runtime manager.

It's a **foundation** for creating standardized tool configurations that teams can distribute as packages (npm, rubygem, pypi, etc).

Think of it as **"lefthook for linting/tooling ecosystems"**:

- Core binary provides secure tool delivery (SHA-256 verification)
- Wrappers provide opinionated configs for different ecosystems
- Teams install one package, get everything configured

## The Two-Level Architecture

datamitsu operates on a two-level architecture that separates the platform from the content:

### Level 1: The Core (datamitsu)

The `datamitsu` binary provides the platform capabilities:

- Binary management with SHA-256 verification
- Programmable JavaScript configuration (goja runtime)
- Declarative + imperative configuration approach
- Config chaining with inheritance
- Tool operations (fix/lint/check)
- Setup orchestration
- File patching (gitignore/dockerignore/package.json)

**What the core does NOT contain:**

- Concrete tool versions
- Linter configurations
- Opinionated defaults

### Level 2: Wrappers (npm/gem/pypi packages)

Wrapper packages provide the actual tool configurations:

- Full tool suite (eslint/prettier/golangci-lint/etc)
- Configured defaults for all tools
- VS Code recommendations
- Exportable config functions
- Project type detection
- Migration-free updates (via patching)

### Distribution Model

```
datamitsu core (Go binary)
  ↓
  Provides: binary mgmt, config engine, setup orchestration

Wrappers (language-specific packages)
  ↓
  Provide: tool versions, configs, project templates

Projects (end users)
  ↓
  Install: npm install @company/dev-standards
  Run: datamitsu setup && datamitsu check
```

This architecture enables teams to:

1. **Build once, distribute everywhere** — Create your company's tool standards as a package
2. **Version independently** — Update configs without waiting for tool releases
3. **Distribute via familiar channels** — Use npm, gem, pypi, or any package manager
4. **Enforce security policies** — Mandatory hash verification for all binaries

## Who is datamitsu for?

### Primary: Config Package Creators

**Who they are:**

- Platform engineers at companies
- Open source maintainers
- DevOps teams establishing standards

**What they need:**

- Create company-wide tool standards
- Distribute via familiar package managers
- Enforce security policies (hash verification)
- Version configs independently from tools

**Value proposition:**

"Build once, distribute everywhere. One package brings all tools and configs to every project."

### Secondary: Development Teams

**Who they are:**

- Teams tired of copy-paste configs
- Projects with multiple contributors
- Companies wanting standardization

**What they need:**

- One dependency = all tools configured
- No manual setup per project
- Docker-optimized CI/CD
- Migration-free updates

**Value proposition:**

"Install one dependency, get everything configured. Zero manual setup in new projects."

### Tertiary: Open Source Maintainers

**Who they are:**

- Project template creators
- Framework maintainers
- Tool distribution authors

**What they need:**

- Distribute project templates with tools
- Ensure contributors use correct versions
- Provide both CLI and programmatic access

**Value proposition:**

"Ship your project template with tools included. Contributors get the right setup automatically."

## What makes datamitsu unique?

### 1. Mandatory Security Verification

Every binary downloaded from the internet must have a SHA-256 hash specified. No exceptions. No bypassing. Hash verification happens before extraction, preventing malicious binaries and ensuring reproducibility.

### 2. Config Chaining and Inheritance

Layer configurations from base → company → team → project:

```javascript
// Project-specific override in datamitsu.config.ts
function getConfig(prev) {
  return {
    ...prev,
    apps: {
      ...prev.apps,
      // Remove a tool for this project
      "some-linter": undefined,
    },
  };
}

globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";
```

Configs receive the previous layer as input, enabling composition without duplication.

### 3. Docker CI/CD Optimization

Pre-cache all tools in a Docker layer for fast, reproducible builds:

```dockerfile
FROM node:20-alpine

# Install datamitsu + config wrapper
RUN npm install -g @company/datamitsu-config

# Download ALL tools to global store
RUN datamitsu init --all

# Save this layer
# Only changed tools re-download on updates
```

This dramatically speeds up CI builds by avoiding tool downloads on every run.

### 4. Migration-Free Updates

Update your config package, re-run setup, and your customizations survive:

```bash
# Update config wrapper
npm update @company/datamitsu-config

# Re-apply setup (preserves customizations!)
datamitsu setup
```

The patching mechanism merges new defaults with your project-specific changes.

### 5. Multi-Runtime Support

Manage tools from different ecosystems in one place:

- **Binary apps** — Download native binaries with hash verification
- **UV apps** — Python tools via managed UV runtime (e.g., yamllint)
- **FNM apps** — npm packages via FNM-managed Node.js + PNPM (e.g., eslint, prettier)
- **JVM apps** — Java applications via managed JDK runtime (e.g., openapi-generator-cli)
- **Shell apps** — Custom shell commands with environment variables

All managed with the same config API, all with hash verification.

## Where does datamitsu fit?

datamitsu is **NOT**:

- A task runner (use moon/nx/turborepo for that)
- A runtime manager (use mise/asdf/volta for that)
- A package manager (use npm/pnpm/cargo for that)
- A build system (use moon/bazel/gradle for that)

datamitsu **IS**:

- A **platform** for building config distributions
- A **foundation** for team standards
- A **delivery mechanism** for verified tools
- A **configuration system** with inheritance

For detailed comparisons with similar tools, see the [Comparison Guide](./reference/comparison.md).

## Use Cases

### Company-Wide Standards

**Scenario:** Enterprise wants all projects to use the same linters and formatters.

**With datamitsu:**

```bash
# Create @company/dev-standards package once
# Then in every project:
npm install @company/dev-standards
datamitsu setup  # Creates all configs automatically
```

**Result:**

- One source of truth for all configs
- Update package → all projects can update
- Enforce via package.json dependency

### Docker-Based CI/CD

**Scenario:** GitLab CI pipeline needs linting tools.

**With datamitsu:**

```dockerfile
# Base image - run once
RUN datamitsu init --all
# Layer cached with all tools

# Project build - runs every time
RUN datamitsu check
# Only new/changed tools download
```

**Result:**

- Fast CI builds (tools pre-cached in layer)
- Reproducible (hash-verified binaries)
- Incremental updates (only changed tools re-download)

### Open Source Project Template

**Scenario:** Framework wants contributors to have consistent setup.

**With datamitsu:**

```json
{
  "dependencies": {
    "@framework/dev-tools": "^1.0.0"
  }
}
```

Run `npm install && npx datamitsu setup` → Everything configured automatically.

**Result:**

- Contributors get identical setup
- No "how to configure" docs needed
- Updates propagate via package version bump

## Learn More

- [Architecture Deep Dive](./guides/architecture/index.md) — Understand the internal execution model, task planning, parallelization, and caching strategies
- [Comparison Guide](./reference/comparison.md) — How datamitsu compares to mise, moon, and other tools
- [Creating Wrappers](./contributing/creating-wrappers.md) — Build your own config distribution package
- [Using Wrappers](./guides/using-wrappers.md) — Consume wrapper packages in your projects
- [Core Concepts](./getting-started/core-concepts.md) — Understand how datamitsu works
