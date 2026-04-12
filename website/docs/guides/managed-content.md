---
title: Managed Content (Bundles)
description: Using bundles to distribute managed content via symlinks with automatic cache invalidation
---

# Managed Content (Bundles)

Bundles are a way to distribute managed content (files, directory trees) to your repositories via symlinks. Unlike apps, bundles are not executable — they store static content in a hash-keyed cache directory and expose it through `.datamitsu/` symlinks.

## The Problem

Without bundles, `datamitsu init` writes files (e.g. `agents.md`, skill definitions) directly into repositories. When the content changes in the config, every repository must be manually patched — causing git conflicts or silent staleness.

With bundles, the content lives in a hash-keyed cache directory. The repository only holds a symlink into `.datamitsu/`. Running `datamitsu init` atomically updates the symlink to point to the new hash directory — no git conflicts, no manual patching.

## Defining a Bundle

Bundles are configured under the top-level `bundles` field in your config:

```javascript
const config = {
  bundles: {
    "agent-skills": {
      version: "1.0",
      files: {
        "agents.md": "# Agent instructions\n\nFollow these guidelines...",
        "skills/search.md": "# Search skill\n\nUse this skill to...",
      },
      links: {
        "agent-skills-dir": ".", // link to entire bundle directory
        "agents-md": "agents.md", // link to a single file
      },
    },
  },
};
```

### Bundle Fields

| Field      | Type                          | Description                                                  |
| ---------- | ----------------------------- | ------------------------------------------------------------ |
| `version`  | `string`                      | Version identifier (changes trigger cache invalidation)      |
| `files`    | `Record<string, string>`      | Filename to content mapping (written to install dir)         |
| `archives` | `Record<string, ArchiveSpec>` | Named archives (inline or external) extracted to install dir |
| `links`    | `Record<string, string>`      | Link name to relative path mapping for `.datamitsu/`         |

A bundle must have at least `files` or `archives` (or both). The `links` field defines what gets exposed via `.datamitsu/` symlinks.

## Directory Links

Link values can point to files or directories within the bundle's install directory. Use `"."` to link to the entire bundle directory:

```javascript
bundles: {
  "my-content": {
    version: "2.0",
    files: {
      "config.yaml": "key: value\n",
      "templates/main.html": "<html>...</html>",
      "templates/partial.html": "<div>...</div>",
    },
    links: {
      "my-content": ".",              // symlink to entire bundle dir
      "my-templates": "templates",    // symlink to a subdirectory
      "my-config": "config.yaml",     // symlink to a single file
    },
  },
}
```

After `datamitsu init`, the `.datamitsu/` directory will contain:

```
.datamitsu/
├── my-content → ../.bundles/my-content/{hash}/
├── my-templates → ../.bundles/my-content/{hash}/templates/
└── my-config → ../.bundles/my-content/{hash}/config.yaml
```

## How Cache Invalidation Works

Bundles are stored at `{cache}/.bundles/{name}/{hash}/`, where the hash is computed from:

- Bundle name
- Version string
- Content of all files
- Content/configuration of all archives

When you change the version, file content, or archive content, the hash changes. On the next `datamitsu init`:

1. A new directory is created with the new hash
2. Files and archives are written to the new directory
3. The `.datamitsu/` symlinks are updated to point to the new directory

This is atomic — the old content remains until the symlink is switched.

## Using Archives in Bundles

Bundles support the same archive types as apps:

### Inline Archives

Small directory trees embedded directly in the config:

```javascript
bundles: {
  "config-bundle": {
    version: "1.0",
    archives: {
      "configs": {
        inline: "tar.br:...",  // brotli-compressed, base64-encoded tar
      },
    },
    links: {
      "configs": ".",
    },
  },
}
```

Generate inline archives with:

```bash
datamitsu devtools pack-inline-archive ./my-directory
```

### External Archives

Larger content downloaded from URLs with mandatory SHA-256 hash verification:

```javascript
bundles: {
  "large-content": {
    version: "3.0",
    archives: {
      "data": {
        url: "https://example.com/content-v3.tar.gz",
        hash: "abc123...",  // SHA-256 (mandatory)
        format: "tar.gz",
      },
    },
    links: {
      "large-content": ".",
    },
  },
}
```

:::note
Bundles with only inline files/archives install regardless of `--skip-download`. Bundles with external archives respect `--skip-download`.
:::

## Bundle Management Commands

```bash
# List all configured bundles with install status
datamitsu devtools bundles list

# Show install path and file tree for a bundle
datamitsu devtools bundles inspect my-bundle

# Print the install directory path
datamitsu devtools bundles path my-bundle
```

## Bundles vs Apps

Bundles and apps serve different purposes:

| Feature       | Bundle                      | App                                |
| ------------- | --------------------------- | ---------------------------------- |
| Executable    | No                          | Yes (`datamitsu exec`)             |
| Runtime       | None                        | binary, uv, fnm, jvm, shell        |
| Content       | Files + archives            | Files + archives + package manager |
| Links         | Yes (`.datamitsu/`)         | Yes (`.datamitsu/`)                |
| Version check | No                          | Optional (`versionCheck`)          |
| Use case      | Static content distribution | Tool/binary management             |

Link names must be unique across both apps and bundles since they share the `.datamitsu/` directory.

## Shared Storage

Shared Storage is a `map[string]string` field on `Config` that flows through the config chain. It provides a standardized way to pass arbitrary data between config layers that doesn't fit the typed config structure.

### When to Use Shared Storage

- Passing vendored content (e.g., `llms.txt`) between config layers
- Feature flags that downstream configs can read
- Any key-value data that needs to survive config layer merging

### Example

In a root config (e.g., a remote config or `--before-config`):

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(input) {
  return {
    ...input,
    sharedStorage: {
      ...input.sharedStorage,
      "llms-txt": "# Project documentation for LLMs...",
      "feature-new-lint": "true",
    },
  };
}
```

In a downstream config (e.g., your repo's `datamitsu.config.js`):

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(input) {
  const llmsTxt = input.sharedStorage?.["llms-txt"];
  const useNewLint = input.sharedStorage?.["feature-new-lint"] === "true";

  return {
    ...input,
    // Use the shared data however you need
    bundles: {
      ...input.bundles,
      "llms-txt": llmsTxt
        ? {
            version: "1.0",
            files: { "llms.txt": llmsTxt },
            links: { "llms-txt": "llms.txt" },
          }
        : undefined,
    },
  };
}
```

### How It Works

`sharedStorage` is a regular field on `Config`. Each config layer receives the previous config as `input`, so `input.sharedStorage` contains whatever the previous layers set. Unlike structured fields like `apps` where replacement loses upstream data, `sharedStorage` is designed for explicit spread-and-extend patterns.

## Limitations

- Bundles require `datamitsu init` to refresh — changes to bundle content are not reflected until you re-run init
- Bundle link names share the `.datamitsu/` namespace with app links — names must be unique across both
- External archive bundles require network access during `datamitsu init` (unless `--skip-download` is used)
