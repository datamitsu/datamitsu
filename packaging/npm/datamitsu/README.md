# @datamitsu/datamitsu

<p align="center">
  <img src="https://datamitsu.com/img/logo.png" alt="datamitsu" width="400" />
</p>

<p align="center">
  <a href="https://www.npmjs.com/package/@datamitsu/datamitsu"><img src="https://img.shields.io/npm/v/@datamitsu/datamitsu" alt="npm"></a>
  <a href="https://github.com/datamitsu/datamitsu/actions/workflows/pr-checks.yml"><img src="https://github.com/datamitsu/datamitsu/actions/workflows/pr-checks.yml/badge.svg" alt="CI"></a>
  <a href="https://codecov.io/gh/datamitsu/datamitsu"><img src="https://codecov.io/gh/datamitsu/datamitsu/graph/badge.svg" alt="codecov"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/datamitsu/datamitsu"><img src="https://api.securityscorecards.dev/projects/github.com/datamitsu/datamitsu/badge" alt="OpenSSF Scorecard"></a>
  <a href="https://github.com/datamitsu/datamitsu/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>

Configuration management and binary distribution tool. JavaScript-configurable tool orchestration written in Go.

## Installation

```bash
# pnpm (recommended)
pnpm add -D @datamitsu/datamitsu

# npm
npm install --save-dev @datamitsu/datamitsu

# yarn
yarn add -D @datamitsu/datamitsu

# bun
bun add -D @datamitsu/datamitsu
```

## Usage

```bash
# Initialize datamitsu in your project
npx datamitsu init

# Run checks (fix + lint)
npx datamitsu check

# Fix issues automatically
npx datamitsu fix

# Lint without fixing
npx datamitsu lint

# Execute a managed binary
npx datamitsu exec shellcheck script.sh
```

## Programmatic API

```javascript
import { fix, lint } from "@datamitsu/datamitsu";

await fix({ files: ["src/generated.ts"] });
const result = await lint({ explain: "json" });
```

## Platform Support

This package automatically installs the correct binary for your platform via optional dependencies:

- Linux (x86_64, ARM64)
- macOS (x86_64, ARM64)
- Windows (x86_64, ARM64)
- FreeBSD (x86_64, ARM64)

## Documentation

For full documentation, visit: https://datamitsu.com

## License

MIT License - see https://github.com/datamitsu/datamitsu/blob/main/LICENSE
