# Ignore Rules

> Reference for .datamitsuignore syntax and usage

datamitsu supports `.datamitsuignore` files to disable specific tools for matching file patterns. These files work similarly to `.gitignore` but target tool execution rather than version control.

## File Location

`.datamitsuignore` lives at the repository git root — the only file whose rules apply. Datamitsu reads its configuration only at the root, and the same is true for the ignore file: a `.datamitsuignore` in any subdirectory is a dead artifact.

```
repo/
└── .datamitsuignore          # The one file whose rules apply
```

A `.datamitsuignore` below the git root does not change what runs. The core
[config-placement check](../guides/configuration.md#config-placement) reports
every such file as an error in `lint` and `check` — move its rules into the
root file (see [Migrating nested rules](#migrating-nested-rules)) and delete it.

### Migrating nested rules

Formerly, `.datamitsuignore` files cascaded: a subdirectory file's rules applied
to its own subtree, with deeper rules overriding shallower ones. To carry a
nested file's rules into the root file, prefix each relative glob with the
directory the file lived in:

| Nested file            | Nested rule     | Root-file rule      |
| ---------------------- | --------------- | ------------------- |
| `src/.datamitsuignore` | `*.go: tool`    | `src/*.go: tool`    |
| `src/.datamitsuignore` | `**/*.md: tool` | `src/**/*.md: tool` |

One caveat: a nested rule that itself starts with `**/` matched from its own
directory's ancestors too. The nested `src/.datamitsuignore` rule
`**/src/*.go: tool` matched `src/a.go`; after moving to the root file, keep both
`src/*.go: tool` and `src/**/src/*.go: tool` to preserve that. There is no
mechanical translation for every accepted glob — check each rule against the
[glob reference](#glob-pattern-reference) after moving it.

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

All rules apply from the repository root, in the order they were added:

1. The built-in rule `.datamitsu/**: *` applies first — tools never run on the managed symlinks directory
2. Config-defined `ignoreRules` apply next
3. The root `.datamitsuignore`'s rules apply last
4. Positive rules add tools to the disabled set
5. Negative (`!`) rules remove tools from the disabled set

A later rule wins over an earlier one, so the root file can re-enable what the
configuration disabled and the other way around.

### Example

```
.datamitsuignore:
  **/*.md: eslint, prettier
  !docs/**/*.md: eslint
```

Results:

- `README.md` — eslint disabled, prettier disabled (first rule applies)
- `docs/guide.md` — eslint enabled (second rule re-enables), prettier disabled (first rule still applies)
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

A catch-all rule fully disables a per-project tool even when it is the tool's
**only** project (or when no project of the tool's type is detected). The tool is
omitted from the plan entirely rather than resurrected with the full file list —
this is what makes the [opt-in model](#generating-an-all-disabled-file) reliable.

## Generating an all-disabled file

For incremental adoption, you can start with **every tool disabled** and enable
them one at a time. Preview the generated file without writing or running fix:

```bash
datamitsu config reconcile --opt-in-tools --dry-run
```

Then write it as part of the normal reconciliation:

```bash
datamitsu config reconcile --opt-in-tools
```

As part of reconciliation, this writes a `.datamitsuignore` at your git root
that disables all configured tools via a single catch-all rule. The default
post-reconciliation fix then respects that opt-in state:

```bash
# Generated by `datamitsu config reconcile --opt-in-tools`.
# All configured tools are disabled by default (opt-in model).
# Enable a tool by removing it from the list below (or add "!**/*: <tool>").
**/*: eslint, golangci-lint, hadolint, prettier, shellcheck
```

Enable a tool by **removing its name** from the list. The generated file
enumerates each tool name explicitly (rather than `**/*: *`) because re-enabling a
single tool over a `*`-disable does not work — a `!**/*: <tool>` line only clears
that tool's key, leaving the wildcard disable in place.

The file is written in canonical form, so it stays stable under
`datamitsu fix`. Reconciliation refuses to overwrite an existing
`.datamitsuignore` so your manual selections are never lost. Add `--dry-run` to
preview the file without writing it.

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
