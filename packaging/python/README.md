# datamitsu

<p align="center">
  <a href="https://pypi.org/project/datamitsu/"><img src="https://img.shields.io/pypi/v/datamitsu" alt="PyPI"></a>
  <a href="https://github.com/datamitsu/datamitsu/actions/workflows/pr-checks.yml"><img src="https://github.com/datamitsu/datamitsu/actions/workflows/pr-checks.yml/badge.svg" alt="CI"></a>
  <a href="https://codecov.io/gh/datamitsu/datamitsu"><img src="https://codecov.io/gh/datamitsu/datamitsu/graph/badge.svg" alt="codecov"></a>
  <a href="https://coveralls.io/github/datamitsu/datamitsu?branch=main"><img src="https://coveralls.io/repos/github/datamitsu/datamitsu/badge.svg?branch=main" alt="Coverage Status"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/datamitsu/datamitsu"><img src="https://api.securityscorecards.dev/projects/github.com/datamitsu/datamitsu/badge" alt="OpenSSF Scorecard"></a>
  <a href="https://github.com/datamitsu/datamitsu/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>

Configuration management and binary distribution tool. JavaScript-configurable tool orchestration written in Go.

## Installation

Install via uv:

```bash
uv pip install datamitsu
```

Or use uv tool for global installation:

```bash
uv tool install datamitsu
```

## Usage

After installation, the `datamitsu` and `dm` commands are available:

```bash
# Initialize datamitsu in your project
datamitsu init

# Run checks (fix + lint)
datamitsu check

# Fix issues automatically
datamitsu fix

# Lint without fixing
datamitsu lint

# Get version
datamitsu version
```

You can also run datamitsu as a Python module:

```bash
python -m datamitsu version
```

## Documentation

For full documentation, visit: https://datamitsu.com

## Platform Support

This package includes pre-compiled binaries for:

- Linux (x86_64, ARM64)
- macOS (x86_64, ARM64)
- Windows (x86_64, ARM64)

## License

MIT License - see https://github.com/datamitsu/datamitsu/blob/main/LICENSE
