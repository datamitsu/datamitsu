# NPM Packaging for datamitsu

This directory contains the infrastructure for packaging datamitsu as an npm package with platform-specific binaries.

## Structure

```
packaging/
├── npm/
│   ├── datamitsu/              # Main npm package
│   │   ├── package.json
│   │   ├── bin/index.js        # Executable wrapper
│   │   └── get-exe.js          # Platform detection
│   └── templates/
│       └── platform-package.json  # Template for platform packages
├── pack.ts                     # Packaging script
└── README.md                   # This file
```

## How It Works

The packaging follows the same approach as [lefthook](https://github.com/evilmartians/lefthook):

1. **Main Package** (`@datamitsu/datamitsu`) - Contains the wrapper scripts and declares platform-specific packages as `optionalDependencies`
2. **Platform Packages** (`@datamitsu/datamitsu-<os>-<arch>`) - Each contains a single compiled binary for a specific platform

When users install `@datamitsu/datamitsu`, npm automatically installs only the appropriate platform package for their system.

## Usage

### Prepare packages for publishing

```bash
# Set version (required)
export VERSION=1.0.0

# Build binaries and prepare all npm packages
pnpm pack:prepare

# For prerelease versions (alpha, beta, rc)
VERSION=1.0.0-alpha-1 pnpm pack:prepare
VERSION=1.0.0-beta-1 pnpm pack:prepare
VERSION=1.0.0-rc-1 pnpm pack:prepare
```

This will:

- Clean previous builds
- Compile Go binaries for all platforms (darwin/linux/windows/freebsd on amd64/arm64)
- Embed version into binaries using `-ldflags` (accessible via `datamitsu version`)
- Create platform-specific npm packages with binaries
- Update version numbers in all package.json files
- Automatically determine npm tag (prerelease versions use `next`, stable uses `latest`)

### Publish to npm (dry-run)

```bash
# Test publishing without actually uploading
# Note: Version is auto-detected from package.json
pnpm pack:publish:dry
```

### Publish to npm (for real)

```bash
# Actually publish to npm registry
# Note: Version is auto-detected from package.json
pnpm pack:publish

# The publish command automatically:
# - Reads version from packaging/npm/datamitsu/package.json
# - Determines correct npm tag (next for prerelease, latest for stable)
# - Skips already published versions
```

### All-in-one command

```bash
# Clean, build, prepare, and dry-run publish
VERSION=1.0.0 pnpm pack:all
```

## Supported Platforms

- macOS (darwin) - x64, arm64
- Linux - x64, arm64
- Windows - x64, arm64
- FreeBSD - x64, arm64

## Platform Package Naming

Platform packages follow this naming convention:

- `@datamitsu/datamitsu-darwin-arm64` - macOS ARM64 (Apple Silicon)
- `@datamitsu/datamitsu-darwin-x64` - macOS x64 (Intel)
- `@datamitsu/datamitsu-linux-arm64` - Linux ARM64
- `@datamitsu/datamitsu-linux-x64` - Linux x64
- `@datamitsu/datamitsu-windows-x64` - Windows x64
- `@datamitsu/datamitsu-windows-arm64` - Windows ARM64
- `@datamitsu/datamitsu-freebsd-x64` - FreeBSD x64
- `@datamitsu/datamitsu-freebsd-arm64` - FreeBSD ARM64

## Publishing Workflow

1. Update version in your code if needed
2. Run `VERSION=x.y.z pnpm pack:prepare`
3. Test locally by installing from the prepared package
4. Run `pnpm pack:publish:dry` to verify everything looks correct
5. Run `pnpm pack:publish` to publish to npm registry

## Requirements

- Node.js and pnpm installed
- Go toolchain installed
- npm account with publish access (for actual publishing)

## Commands Reference

| Command                 | Description                         |
| ----------------------- | ----------------------------------- |
| `pnpm pack:clean`       | Remove previous build artifacts     |
| `pnpm pack:build`       | Build Go binaries for all platforms |
| `pnpm pack:prepare`     | Build and prepare all npm packages  |
| `pnpm pack:publish:dry` | Test publish without uploading      |
| `pnpm pack:publish`     | Publish to npm registry             |
| `pnpm pack:all`         | Run all steps including dry-run     |

## Package README

The package README (`PACKAGE_README.md`) is shared across all wrapper packages via symlinks:

- `npm/datamitsu/README.md` → `../../PACKAGE_README.md`

This ensures consistency across distribution formats (npm, future Ruby gems, Python packages, etc.).

**Content guidelines:**

- Keep it minimal and focused on core value
- Show configuration examples, not extensive API docs
- Link to full documentation at datamitsu.com
- Follow lefthook-style structure: Install → Usage → Why → Docs

## Notes

- The main package is defined in `npm/datamitsu/package.json`
- Platform packages are generated dynamically from templates
- Platform-specific packages have their own README (symlinked to root README)
- Binary verification happens via `get-exe.js` at runtime
- All packages share the same version number
