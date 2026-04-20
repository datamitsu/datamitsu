# datamitsu - Python Package

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
