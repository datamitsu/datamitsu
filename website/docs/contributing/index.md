---
title: Contributing to datamitsu
description: How to contribute to datamitsu development, documentation, and ecosystem
---

# Contributing to datamitsu

Thank you for your interest in contributing to datamitsu! This project is in alpha stage and welcomes contributions from the community.

:::info[Alpha Stage]
datamitsu is in active development. The configuration API is not yet stabilized and may change between versions. Breaking changes are acceptable when they improve correctness, safety, or simplify architecture.
:::

## Ways to Contribute

### Code Contributions

Contribute to the core datamitsu platform:

- **Bug fixes** — Help identify and fix issues
- **Feature implementations** — Work on planned features from GitHub issues
- **Performance improvements** — Optimize binary downloads, cache handling, etc.
- **Platform support** — Add support for new OS/architecture combinations

**Getting started:**

1. Fork the repository on GitHub
2. Clone your fork: `git clone https://github.com/yourusername/datamitsu`
3. Create a feature branch: `git checkout -b feat/your-feature`
4. Make your changes and test them
5. Submit a pull request with a clear description

### Documentation Contributions

Improve the documentation:

- **Fix typos and errors** — Even small fixes help
- **Add examples** — Share how you use datamitsu
- **Write guides** — Help others solve common problems
- **Improve clarity** — Make complex topics easier to understand

All documentation lives in `website/docs/` and uses Docusaurus. See [Brand Guidelines](./brand-guidelines.md) for voice and style guidance.

### Wrapper Package Creation

Build and share wrapper packages for specific ecosystems:

- **Company standards** — Create internal tool configurations
- **Framework integrations** — Provide ready-to-use configs for frameworks
- **Language ecosystems** — Build opinionated defaults for Go, Rust, TypeScript, etc.

See [Creating Wrappers](./creating-wrappers.md) for a complete guide.

### Community Support

Help other users:

- **Answer questions** — On GitHub Discussions or issues
- **Share use cases** — Blog about how you use datamitsu
- **Report bugs** — Help us identify and fix problems
- **Provide feedback** — Share your experience and suggestions

## Development Setup

### Prerequisites

- Go 1.21 or later
- Node.js 20 or later (for website development)
- pnpm (for website dependencies)

### Building from Source

```bash
# Clone the repository
git clone https://github.com/datamitsu/datamitsu
cd datamitsu

# Build the binary
go build

# Run tests
go test ./...
```

### Working on Documentation

```bash
# Navigate to website directory
cd website

# Install dependencies
pnpm install

# Start development server
pnpm start

# Build for production
pnpm build
```

The documentation site will be available at `http://localhost:3000`.

## Contribution Guidelines

### Code Style

- **English-only** — All code, comments, and documentation must be in English
- **Minimal comments** — Code should be self-documenting; add comments only when necessary
- **Follow Go conventions** — Use `gofmt`, follow standard Go idioms
- **Security-first** — All binaries must have SHA-256 hashes; no exceptions

See CLAUDE.md for detailed code commenting guidelines.

### Documentation Style

- **English-only** — No exceptions
- **Clear and concise** — Avoid jargon; explain technical concepts clearly
- **Use examples** — Show, don't just tell
- **Maintain voice** — See [Brand Guidelines](./brand-guidelines.md)

### Commit Messages

Write clear, descriptive commit messages:

```
fix: address hash verification bug in binmanager

Add additional validation for empty hash strings before
downloading binaries. Prevents panic when config has
missing hash field.

Fixes #123
```

### Pull Request Process

1. **Ensure tests pass** — Run `go test ./...` before submitting
2. **Update documentation** — Document new features or API changes
3. **Keep PRs focused** — One feature or fix per PR
4. **Write clear descriptions** — Explain what and why, not just how
5. **Respond to feedback** — Be open to suggestions and changes

## Documentation Requirements

**All user-facing changes require documentation updates in the same PR.**

This includes:

- **User-facing features** → Update `website/docs/` with examples and guides
- **CLI commands** → Update `website/docs/reference/cli-commands.md`
- **Configuration options** → Update `website/docs/reference/configuration-api.md`
- **New examples** → Add to `website/docs/examples/`
- **Breaking changes** → Document migration path in docs and create blog post in `website/blog/`

Documentation is not optional — it is a required deliverable for every user-facing change.

## Project Resources

- **Repository:** [github.com/datamitsu/datamitsu](https://github.com/datamitsu/datamitsu)
- **Issues:** [GitHub Issues](https://github.com/datamitsu/datamitsu/issues)
- **Discussions:** [GitHub Discussions](https://github.com/datamitsu/datamitsu/discussions)
- **Documentation:** [datamitsu.com](https://datamitsu.com)

## Code of Conduct

Be respectful, constructive, and professional in all interactions. We aim to create a welcoming environment for contributors of all backgrounds and experience levels.

## Questions?

If you have questions about contributing:

- Open a [GitHub Discussion](https://github.com/datamitsu/datamitsu/discussions)
- Check existing [GitHub Issues](https://github.com/datamitsu/datamitsu/issues)
- Read the [documentation](https://datamitsu.com)

We appreciate your contributions!
