---
title: Use Remote Configs
description: How to create, share, and inherit remote configuration files
---

# Use Remote Configs

Remote configs let you share datamitsu configuration across multiple repositories. A team or organization can maintain a central configuration that individual projects inherit and extend.

## Creating a Remote Config

A remote config is a standard datamitsu config file hosted at an HTTPS URL. It exports `getMinVersion()` and `getConfig()` like any local config:

```javascript
// https://config.myorg.com/datamitsu/base.js
function getConfig(config) {
  return {
    ...config,
    apps: {
      ...config.apps,
      // organization-standard tools
    },
    tools: {
      ...config.tools,
      // organization-standard tool definitions
    },
    ignoreRules: [...config.ignoreRules, "vendor/**/*: *"],
  };
}

globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "1.0.0";
```

Host this file on any HTTPS server accessible to your team.

## Using a Remote Config

Reference remote configs by exporting a `getRemoteConfigs()` function from your local config:

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

// datamitsu.config.js
function getRemoteConfigs() {
  return [
    {
      url: "https://config.myorg.com/datamitsu/base.js",
      hash: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
    },
  ];
}
globalThis.getRemoteConfigs = getRemoteConfigs;

function getConfig(config) {
  // config already includes remote config changes
  return {
    ...config,
    // project-specific overrides
  };
}

globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "1.0.0";
```

## Hash Verification

Every remote config requires a SHA-256 hash. This is a strict security requirement -- datamitsu refuses to load any remote config without a valid hash.

To get the hash of a remote config:

```bash
curl -sL "https://config.myorg.com/datamitsu/base.js" | sha256sum
```

Copy the hash into the `hash` field of your remote config reference. When the remote config changes, you must update the hash in every project that references it.

## Config Inheritance Chain

Remote configs are resolved depth-first before the referencing config's `getConfig()` runs. Remote configs can themselves reference other remote configs, creating an inheritance chain:

```
Organization base config (remote)
  ↓ getRemoteConfigs()
Team-specific config (remote)
  ↓ getRemoteConfigs()
Project config (local datamitsu.config.js)
```

Each layer receives the accumulated configuration from all previous layers and can extend or override it.

Circular dependencies are detected and produce an error.

## Wrapper Package Pattern

For npm-based monorepos, you can distribute configuration as an npm package:

```javascript
// @myorg/datamitsu-config/index.js
function getRemoteConfigs() {
  return [
    {
      url: "https://config.myorg.com/datamitsu/base.js",
      hash: "abc123...",
    },
  ];
}
globalThis.getRemoteConfigs = getRemoteConfigs;

function getConfig(config) {
  return {
    ...config,
    // organization-specific overrides on top of the remote base
  };
}

globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "1.0.0";
```

Projects use it via `--before-config`:

```bash
datamitsu check --before-config ./node_modules/@myorg/datamitsu-config/index.js
```

This pattern separates the shared configuration (the npm package) from the remote base config, allowing the remote base to be updated independently.

## Caching

Remote configs are cached locally to avoid network requests on every run:

- Cache location: `{store}/.remote-configs/`
- Cache validity is determined by hash: if the cached content matches the expected SHA-256 hash, it is used without a network request
- When the hash in your config changes (pointing to a new version), datamitsu fetches the updated content

## Security

Remote config loading includes several security measures:

- SHA-256 hash verification is mandatory -- missing or mismatched hashes cause an error
- HTTPS-to-HTTP redirects are rejected
- Response size is limited to 10 MiB
- Requests have a 30-second timeout

## Skipping Remote Configs

For offline development or testing, skip remote config resolution:

```bash
# Skip remote configs during verify-all
datamitsu devtools verify-all --no-remote
```

## Updating Remote Configs

When a remote config is updated, each consuming project needs to update its hash:

1. Publish the new remote config
2. Compute the new SHA-256 hash
3. Update the `hash` field in each project's `getRemoteConfigs()` return value
4. Commit the hash update

This explicit update step is intentional -- it ensures that config changes are reviewed and deployed deliberately, not automatically.
