<!-- This file is intentionally minimal. Full documentation lives in website/docs/ -->

# datamitsu

<p align="center">
  <img src="website/static/img/logo.png" alt="datamitsu" width="400" />
</p>

<p align="center">
  <a href="https://github.com/datamitsu/datamitsu/releases/latest"><img src="https://img.shields.io/github/v/release/datamitsu/datamitsu" alt="GitHub Release"></a>
  <a href="https://www.npmjs.com/package/@datamitsu/datamitsu"><img src="https://img.shields.io/npm/v/@datamitsu/datamitsu" alt="npm"></a>
  <a href="https://pypi.org/project/datamitsu/"><img src="https://img.shields.io/pypi/v/datamitsu" alt="PyPI"></a>
  <a href="https://rubygems.org/gems/datamitsu"><img src="https://img.shields.io/gem/v/datamitsu" alt="Gem"></a>
  <a href="https://github.com/datamitsu/datamitsu/actions/workflows/pr-checks.yml"><img src="https://github.com/datamitsu/datamitsu/actions/workflows/pr-checks.yml/badge.svg" alt="CI"></a>
  <a href="https://codecov.io/gh/datamitsu/datamitsu"><img src="https://codecov.io/gh/datamitsu/datamitsu/graph/badge.svg" alt="codecov"></a>
  <a href="https://coveralls.io/github/datamitsu/datamitsu?branch=main"><img src="https://coveralls.io/repos/github/datamitsu/datamitsu/badge.svg?branch=main" alt="Coverage Status"></a>
  <a href="https://goreportcard.com/report/github.com/datamitsu/datamitsu"><img src="https://goreportcard.com/badge/github.com/datamitsu/datamitsu?v=2" alt="Go Report Card"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/datamitsu/datamitsu"><img src="https://api.securityscorecards.dev/projects/github.com/datamitsu/datamitsu/badge" alt="OpenSSF Scorecard"></a>
  <a href="https://github.com/datamitsu/datamitsu/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>

> Your toolchain deserves a home.

> **Alpha**: This project is in alpha. The configuration API is not yet stabilized and may change between versions.

Every stack comes with a configuration tax. You pay it on the first project, then the second, then every time a tool updates—and it breaks differently in each repo. **datamitsu exists so you pay this tax only once.**

A platform for building reproducible, security-first development tool distributions. It downloads, verifies (SHA-256), and manages binaries and runtime-managed tools across platforms, using JavaScript-powered configuration with inheritance and chaining. Install one package, get everything configured.

## Quick Start

```bash
# Build from source
go build

# Initialize tools
./datamitsu init

# Run checks
./datamitsu check
```

## Documentation

Full documentation is available at [https://datamitsu.com](https://datamitsu.com) or locally in [`website/docs/`](website/docs/).

**Getting Started:**

- [Installation](website/docs/getting-started/installation.md)
- [Quick Start Guide](website/docs/getting-started/quick-start.md)
- [About datamitsu](website/docs/about.md) — Why datamitsu exists and what makes it unique

**Reference:**

- [CLI Commands](website/docs/reference/cli-commands.md)
- [Configuration API](website/docs/reference/configuration-api.md)
- [Comparison with mise/moon/Nx](website/docs/reference/comparison.md)

## Contributing

Contributions are welcome! See the [Contributing Guide](website/docs/contributing/index.md) to get started.

- [Brand Guidelines](website/docs/contributing/brand-guidelines.md) — Voice, tone, and style
- [Creating Wrapper Packages](website/docs/contributing/creating-wrappers.md) — Build config distributions

## License

MIT
