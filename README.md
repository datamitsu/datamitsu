<!-- This file is intentionally minimal. Full documentation lives in website/docs/ -->

# datamitsu

<p align="center">
  <img src="website/static/img/logo.png" alt="datamitsu" width="400" />
</p>

<p align="center">
  <a href="https://github.com/datamitsu/datamitsu/actions/workflows/ci.yml"><img src="https://github.com/datamitsu/datamitsu/actions/workflows/ci.yml/badge.svg" alt="build"></a>
  <a href="https://goreportcard.com/report/github.com/datamitsu/datamitsu"><img src="https://goreportcard.com/badge/github.com/datamitsu/datamitsu?v=2" alt="Go Report Card"></a>
  <img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT">
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
