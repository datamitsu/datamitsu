---
title: Ignore Rules
description: Reference for .datamitsuignore syntax and usage
---

# Ignore Rules

datamitsu supports `.datamitsuignore` files to disable specific tools for matching file patterns. These files work similarly to `.gitignore` but target tool execution rather than version control.

## File Location

Place `.datamitsuignore` files anywhere in your repository. Rules apply to files in the same directory and all subdirectories. Multiple files at different levels of the directory tree are combined, with deeper rules taking precedence.

```
repo/
├── .datamitsuignore          # Rules for entire repo
├── src/
│   └── .datamitsuignore      # Additional rules for src/
└── vendor/
    └── .datamitsuignore      # Rules for vendor/
```

## Syntax

Each line follows the format:

```
{glob}: {tool1}, {tool2}
```

| Component    | Required | Description                                                  |
| ------------ | -------- | ------------------------------------------------------------ |
| `!` prefix   | No       | Inversion operator — re-enables tools for matching files     |
| Glob pattern | Yes      | File matching pattern (supports `**` for recursive matching) |
| `:`          | Yes      | Separator between pattern and tool list                      |
| Tool list    | Yes      | Comma-separated tool names to disable                        |

### Comments and Blank Lines

```bash
# This is a comment
# Blank lines are ignored

**/*.md: prettier
```

### Disabling Specific Tools

Disable one or more tools for files matching a pattern:

```bash
# Disable prettier for markdown files
**/*.md: prettier

# Disable multiple tools for generated files
**/*.generated.go: golangci-lint, gofmt

# Disable eslint for test fixtures
tests/fixtures/**/*: eslint
```

### Wildcard Tool Name

Use `*` to disable all tools for matching files:

```bash
# Disable all tools for vendor directory
vendor/**/*: *

# Disable all tools for build output
dist/**/*: *
```

### Inversion (Re-enabling)

Use the `!` prefix to re-enable tools that were disabled by a broader rule:

```bash
# Disable eslint for all markdown
**/*.md: eslint

# But re-enable it for docs
!docs/**/*.md: eslint
```

The `!` prefix with `*` clears the entire disabled set for matching files:

```bash
# Disable all tools for vendor
vendor/**/*: *

# But re-enable everything for our vendored patches
!vendor/patches/**/*: *
```

## Rule Application Order

Rules are applied from the root directory toward the file's directory:

1. Root-level `.datamitsuignore` rules apply first
2. Subdirectory rules apply on top, in depth order
3. Within the same directory, rules apply in file order (top to bottom)
4. Positive rules add tools to the disabled set
5. Negative (`!`) rules remove tools from the disabled set

This means deeper rules override shallower ones, and later rules in the same file override earlier ones.

### Example

```
repo/.datamitsuignore:
  **/*.md: eslint, prettier

repo/docs/.datamitsuignore:
  !**/*.md: eslint
```

Results:

- `README.md` — eslint disabled, prettier disabled (root rule applies)
- `docs/guide.md` — eslint enabled (docs rule re-enables), prettier disabled (root rule still applies)
- `src/main.js` — no tools disabled (pattern doesn't match)

## Config-Defined Ignore Rules

In addition to `.datamitsuignore` files, you can define ignore rules in your configuration:

```javascript
/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(config) {
  return {
    ...config,
    ignoreRules: ["vendor/**/*: *", "**/*.generated.go: golangci-lint"],
  };
}
```

Config-defined rules are merged with file-based rules. Rules from earlier config layers are prepended to later layers (append semantics).

## Per-Project Scope

For tools with `scope: "per-project"`, ignore rules are evaluated by checking if the tool would be disabled for a synthetic file in that project directory. This means:

- Extension-specific patterns like `**/*.md: eslint` do not disable `eslint` for an entire project
- Catch-all patterns like `**/*: eslint` or `vendor/**/*: *` do disable tools for entire projects

## Glob Pattern Reference

datamitsu uses [doublestar](https://github.com/bmatcuk/doublestar) for glob matching:

| Pattern     | Matches                                     |
| ----------- | ------------------------------------------- |
| `*`         | Any characters within a single path segment |
| `**`        | Zero or more path segments                  |
| `?`         | Any single character                        |
| `[abc]`     | Any character in the set                    |
| `[a-z]`     | Any character in the range                  |
| `{foo,bar}` | Either `foo` or `bar`                       |

### Common Patterns

```bash
# All files with a specific extension
**/*.md: prettier

# All files in a specific directory
vendor/**/*: *

# Files at a specific depth
src/*: eslint

# Multiple extensions
**/*.{js,ts,jsx,tsx}: eslint

# Specific filename anywhere
**/Makefile: prettier
```

## Formatting

datamitsu enforces a canonical format for `.datamitsuignore` files. Running `datamitsu fix` normalizes formatting automatically:

- Canonical format: `{!}{glob}: {tool1}, {tool2}`
- Single space after colon
- Single space after each comma
- Leading blank lines removed
- Consecutive blank lines collapsed
- Trailing newline preserved

## Linting

`datamitsu lint` validates `.datamitsuignore` files and reports:

- Parse errors (missing colon, empty pattern, empty tool list) — causes failure
- Formatting deviations from canonical form — warning
- Unknown tool names not in your configuration — warning
