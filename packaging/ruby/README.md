# datamitsu

<p align="center">
  <a href="https://rubygems.org/gems/datamitsu"><img src="https://img.shields.io/gem/v/datamitsu" alt="Gem"></a>
  <a href="https://github.com/datamitsu/datamitsu/actions/workflows/pr-checks.yml"><img src="https://github.com/datamitsu/datamitsu/actions/workflows/pr-checks.yml/badge.svg" alt="CI"></a>
  <a href="https://codecov.io/gh/datamitsu/datamitsu"><img src="https://codecov.io/gh/datamitsu/datamitsu/graph/badge.svg" alt="codecov"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/datamitsu/datamitsu"><img src="https://api.securityscorecards.dev/projects/github.com/datamitsu/datamitsu/badge" alt="OpenSSF Scorecard"></a>
  <a href="https://github.com/datamitsu/datamitsu/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>

Configuration management and binary distribution tool. JavaScript-configurable tool orchestration written in Go.

## Installation

Add to your Gemfile:

```ruby
gem "datamitsu"
```

Or install directly:

```bash
gem install datamitsu
```

## Usage

After installation, the `datamitsu` command is available:

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

## Documentation

For full documentation, visit: https://datamitsu.com

## Platform Support

This gem includes pre-compiled binaries for:

- Linux (x86_64, ARM64)
- macOS (x86_64, ARM64)
- Windows (x86_64, ARM64)
- FreeBSD (x86_64, ARM64)

## License

MIT License - see https://github.com/datamitsu/datamitsu/blob/main/LICENSE
