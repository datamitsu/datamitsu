# datamitsu

<p align="center">
  <img src="https://datamitsu.com/img/logo.png" alt="datamitsu" width="400" />
</p>

Configuration management and binary distribution tool. JavaScript-configurable tool orchestration written in Go.

## Quick Start

```bash
docker run --rm -v "$(pwd):/workspace" datamitsu/datamitsu init
docker run --rm -v "$(pwd):/workspace" datamitsu/datamitsu check
```

## Tags

### Debian-based (default)

| Tag      | Description                    |
| -------- | ------------------------------ |
| `latest` | Latest stable release          |
| `stable` | Same as `latest`               |
| `X.Y.Z`  | Specific version               |
| `X.Y`    | Latest patch for minor version |
| `X`      | Latest minor for major version |

### Alpine-based

| Tag             | Description                    |
| --------------- | ------------------------------ |
| `latest-alpine` | Latest stable release (Alpine) |
| `stable-alpine` | Same as `latest-alpine`        |
| `X.Y.Z-alpine`  | Specific version (Alpine)      |

## Usage

### CI Integration

```yaml
# GitHub Actions
jobs:
  lint:
    runs-on: ubuntu-latest
    container: datamitsu/datamitsu:latest
    steps:
      - uses: actions/checkout@v4
      - run: datamitsu check
```

### Local Development

```bash
# Mount your project and run checks
docker run --rm -v "$(pwd):/workspace" datamitsu/datamitsu check

# Execute a specific managed tool
docker run --rm -v "$(pwd):/workspace" datamitsu/datamitsu exec shellcheck script.sh
```

## Image Variants

- **Debian** (`datamitsu/datamitsu:latest`): Based on `debian:trixie-slim`. Includes `git` and `ca-certificates`.
- **Alpine** (`datamitsu/datamitsu:latest-alpine`): Based on `alpine:3.23`. Smaller image, includes `git` and `ca-certificates`.

## Platform Support

Both variants are available for:

- `linux/amd64`
- `linux/arm64`

## Source & Documentation

- Documentation: https://datamitsu.com
- Source: https://github.com/datamitsu/datamitsu
- Issues: https://github.com/datamitsu/datamitsu/issues

## License

MIT License - see https://github.com/datamitsu/datamitsu/blob/main/LICENSE
