# Supply Chain Security (pnpm 11 + UV + Go)

## Overview

- pnpm 11 defaults to `strictDepBuilds: true`, blocking lifecycle scripts for unapproved packages. This breaks FNM app installs (e.g., mmdc/puppeteer) with `ERR_PNPM_IGNORED_BUILDS`
- Auto-generate secure `pnpm-workspace.yaml` with recommended defaults per FNM app environment; user's `App.files["pnpm-workspace.yaml"]` is shallow-merged on top as overrides
- Expose recommended defaults via `sharedStorage["pnpm-workspace-defaults"]` so JS configs can access, extend, and reuse them
- No new fields in `AppConfigFNM` — users configure via existing `App.files` mechanism
- Harden UV app installs with `--no-build` support
- Document supply chain best practices for pnpm, UV, and Go

## Context (from discovery)

- Files/components involved:
  - `internal/config/config.js` — default config; add `sharedStorage["pnpm-workspace-defaults"]` entry
  - `internal/runtimemanager/fnm.go` — `installFNMAppOnce()`, `buildPNPMInstallArgs()`, new merge logic
  - `internal/runtimemanager/uv.go` — `installUVAppOnce()` (add `--no-build` flag)
  - `cmd/config_lockfile.go` — lockfile regeneration (must generate workspace yaml)
  - `config/config.d.ts` — TypeScript type docs for sharedStorage key
  - Website docs — supply chain documentation
- `sharedStorage` is `map[string]string` — a key-value store passed through config layers; each layer can read and extend it
- The current `App.files` mechanism already allows passing `pnpm-workspace.yaml`, but there are no secure defaults and no merge behavior
- pnpm 11 reads all config from `pnpm-workspace.yaml` (not `.npmrc` except auth)

## Design Decision: sharedStorage + Auto-Merge

**Two independent mechanisms serving different purposes:**

1. **FNM app environments (automatic, no user action needed)**: The Go FNM installer always starts with hardcoded recommended defaults. If the user provided `App.files["pnpm-workspace.yaml"]` with per-app overrides (e.g., `allowBuilds`), Go shallow-merges it on top. This ensures security settings are always present without user effort.

2. **Repo-level pnpm-workspace.yaml via `sharedStorage`**: The default `config.js` (Go-embedded) publishes `sharedStorage["pnpm-workspace-defaults"]` — a YAML string with recommended security settings. This is NOT for per-app FNM configs (those are handled automatically). The purpose is for users who want to write a secure `pnpm-workspace.yaml` into a **project repository** during setup — e.g., via a Bundle that patches a third-party repo, or via files written during `datamitsu init`.

**Why `sharedStorage` (not RuntimeConfig)?**

- `sharedStorage` is the existing mechanism for passing arbitrary data between config layers
- RuntimeConfig is typed per-runtime (FNM/UV/JVM) — pnpm workspace config doesn't belong there
- Users already use sharedStorage pattern (e.g., `datamitsu-agent-prompt`)

**Merge strategy (FNM apps):** User wins (shallow merge per top-level key). If user sets `strictDepBuilds: false`, it overrides the recommended default. This respects user intent.

**FNM app install flow:**

1. Go FNM installer starts with hardcoded recommended defaults
2. If `App.files["pnpm-workspace.yaml"]` exists, shallow-merge user values on top
3. Write merged result to app environment before `pnpm install`
4. User's `pnpm-workspace.yaml` entry is consumed by merge — NOT written as raw file
5. If no user override — defaults alone are written (backward compatible)

**sharedStorage flow (repo-level):**

1. Default `config.js` publishes `sharedStorage["pnpm-workspace-defaults"]` = YAML string
2. User's config layer reads it: `YAML.parse(config.sharedStorage["pnpm-workspace-defaults"])`
3. User extends with project-specific settings and writes to repo via Bundle/files

## Development Approach

- **Testing approach**: TDD — write failing tests first, then implement fixes
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility (existing configs keep working — they just get secure defaults)

## Testing Strategy

- **Unit tests**: required for every task
- Test workspace YAML generation with defaults only
- Test merge: user overrides specific keys (e.g., `allowBuilds`), recommended defaults remain for unset keys
- Test merge: user overrides security setting (e.g., `strictDepBuilds: false`) — user wins
- Test backward compatibility: no `files["pnpm-workspace.yaml"]` → defaults only
- Test sharedStorage contains the expected YAML string
- Test UV install args with `--no-build`

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Add pnpm-workspace-defaults to sharedStorage in default config

- [ ] define the recommended pnpm workspace config as a JS object in `internal/config/config.js` (or its source):
  ```javascript
  const pnpmWorkspaceDefaults = {
    strictDepBuilds: true,
    blockExoticSubdeps: true,
    enablePrePostScripts: false,
    dangerouslyAllowAllBuilds: false,
    minimumReleaseAge: 10080, // 7 days in minutes
    trustPolicy: "no-downgrade",
    lockfile: true,
    preferFrozenLockfile: true,
  };
  ```
- [ ] add `"pnpm-workspace-defaults": YAML.stringify(pnpmWorkspaceDefaults)` to the sharedStorage in `getConfig()`
- [ ] write test: verify default config output contains `sharedStorage["pnpm-workspace-defaults"]` with expected YAML (all 8 keys)
- [ ] run tests — must pass

### Task 2: Write failing tests for Go-side workspace merge (TDD red)

- [ ] add test in `internal/runtimemanager/fnm_test.go`: `defaultPNPMWorkspaceConfig()` returns map with all 8 keys: `strictDepBuilds: true`, `blockExoticSubdeps: true`, `enablePrePostScripts: false`, `dangerouslyAllowAllBuilds: false`, `minimumReleaseAge: 10080`, `trustPolicy: "no-downgrade"`, `lockfile: true`, `preferFrozenLockfile: true`
- [ ] add test: `mergePNPMWorkspaceConfig(defaults, "")` → returns defaults unchanged
- [ ] add test: `mergePNPMWorkspaceConfig(defaults, userYAML)` with user `allowBuilds: {puppeteer: true}` → merged result has both defaults and user's allowBuilds
- [ ] add test: user sets `strictDepBuilds: false` → merged result has `strictDepBuilds: false` (user wins)
- [ ] add test: `buildPNPMWorkspaceForApp(files)` extracts `pnpm-workspace.yaml` from files, merges with defaults, returns final YAML string
- [ ] run tests — new tests must FAIL

### Task 3: Implement Go-side workspace merge

- [ ] in `internal/runtimemanager/fnm.go`: add `defaultPNPMWorkspaceConfig() map[string]any` — returns recommended defaults map (same values as JS-side)
- [ ] add `mergePNPMWorkspaceConfig(base map[string]any, userYAML string) (map[string]any, error)` — parses user YAML and shallow-merges on top of base
- [ ] add `buildPNPMWorkspaceForApp(files map[string]string) (string, error)` — orchestrates: get defaults → merge with user's `files["pnpm-workspace.yaml"]` entry → marshal to YAML string
- [ ] run tests — must pass including merge tests from Task 2

### Task 4: Integrate workspace generation into FNM app installer

- [ ] in `installFNMAppOnce()`: after writing `package.json` and lock file, call `buildPNPMWorkspaceForApp(files)` and write result to `pnpm-workspace.yaml` in the app environment
- [ ] remove `pnpm-workspace.yaml` from the files map before calling `WriteAppFiles()` to avoid double-write (the merge consumes it)
- [ ] write integration test: install with no user workspace config → defaults are written
- [ ] write integration test: install with user workspace config → merged result is written
- [ ] run tests — must pass

### Task 5: Update config lockfile command for pnpm 11

- [ ] in `cmd/config_lockfile.go`: when regenerating lockfiles, also generate `pnpm-workspace.yaml` using `buildPNPMWorkspaceForApp(files)` before running `pnpm install`
- [ ] ensure lockfile regeneration works with packages that have build scripts (user must have `allowBuilds` in their `files["pnpm-workspace.yaml"]`)
- [ ] write test for lockfile regeneration with workspace yaml
- [ ] run tests — must pass

### Task 6: Harden UV app installs

- [ ] in `internal/runtimemanager/uv.go` `installUVAppOnce()`: add `--no-build` flag when lockfile is present (wheels only, no arbitrary code execution during install)
- [ ] document trade-off: `--no-build` prevents sdist builds; if a package has no wheel for the platform, install fails (this is intentional for security)
- [ ] write test for UV install args with and without lockfile
- [ ] run tests — must pass

### Task 7: Verify acceptance criteria

- [ ] verify FNM: app with `files["pnpm-workspace.yaml"]` containing `allowBuilds: {puppeteer: true}` installs successfully
- [ ] verify FNM: app without any workspace config installs (backward compat, packages without build scripts)
- [ ] verify FNM: user's overrides are merged correctly with defaults
- [ ] verify sharedStorage: JS config can read `config.sharedStorage["pnpm-workspace-defaults"]` and parse it
- [ ] verify UV: `--no-build` flag is passed when lockfile is present
- [ ] run full test suite (`go test ./...`)
- [ ] run linter if configured

### Task 8: Document supply chain best practices

- [ ] create `website/docs/guides/supply-chain-security.md` with sections:
  - **pnpm (FNM apps)**: how datamitsu auto-generates secure `pnpm-workspace.yaml`, how to use `sharedStorage["pnpm-workspace-defaults"]`, how to override via `App.files`, `allowBuilds` configuration, pnpm 11 `strictDepBuilds` / `blockExoticSubdeps` / `minimumReleaseAge`, lockfile enforcement
  - **UV apps**: `--locked` / `--frozen` / `--no-build`, hash verification, `exclude-newer`
  - **Go**: `go.sum` verification, `go mod verify`, `-mod=readonly`, `govulncheck`, sumdb
  - Common patterns: how datamitsu enforces hash verification for all downloads (CLAUDE.md policy)
- [ ] update `website/docs/how-to/maintain-wrapper.md` with pnpm-workspace.yaml override example for FNM apps with build-requiring dependencies (puppeteer, sharp, etc.)
- [ ] add workspace override example to `website/docs/examples/pnpm-patterns.md`

### Task 9: [Final] Update documentation references

- [ ] verify `docs/architecture.md` doesn't need updates
- [ ] verify `config/config.d.ts` JSDoc for `App.files` mentions pnpm-workspace.yaml merge behavior
- [ ] add JSDoc comment for `sharedStorage["pnpm-workspace-defaults"]` key in config.d.ts

## Technical Details

### sharedStorage entry (JS-side, in default config.js)

```javascript
const pnpmWorkspaceDefaults = {
  strictDepBuilds: true,
  blockExoticSubdeps: true,
  enablePrePostScripts: false,
  dangerouslyAllowAllBuilds: false,
  minimumReleaseAge: 10080, // 7 days in minutes
  trustPolicy: "no-downgrade",
  lockfile: true,
  preferFrozenLockfile: true,
};

function getConfig(config) {
  return {
    ...config,
    // ... apps, runtimes ...
    sharedStorage: {
      ...config.sharedStorage,
      "datamitsu-agent-prompt": DATAMITSU_AGENT_GUIDE,
      "pnpm-workspace-defaults": YAML.stringify(pnpmWorkspaceDefaults),
    },
  };
}
```

### Go-side defaults (same values, hardcoded in fnm.go)

```go
func defaultPNPMWorkspaceConfig() map[string]any {
    return map[string]any{
        "strictDepBuilds":            true,
        "blockExoticSubdeps":         true,
        "enablePrePostScripts":       false,
        "dangerouslyAllowAllBuilds":  false,
        "minimumReleaseAge":          10080, // 7 days in minutes
        "trustPolicy":               "no-downgrade",
        "lockfile":                   true,
        "preferFrozenLockfile":       true,
    }
}
```

### Merge behavior

```yaml
# Go defaults:                        User's App.files["pnpm-workspace.yaml"]:
  strictDepBuilds: true              #   allowBuilds:
  blockExoticSubdeps: true           #     puppeteer: true
  enablePrePostScripts: false        #   strictDepBuilds: false  ← user override
  dangerouslyAllowAllBuilds: false   #
  minimumReleaseAge: 10080           #
  trustPolicy: "no-downgrade"        #

# Merged result (written to app environment):
  strictDepBuilds: false             # ← user wins
  blockExoticSubdeps: true           # ← default preserved
  enablePrePostScripts: false        # ← default preserved
  dangerouslyAllowAllBuilds: false   # ← default preserved
  minimumReleaseAge: 10080           # ← default preserved
  trustPolicy: "no-downgrade"        # ← default preserved
  allowBuilds:                       # ← user addition
    puppeteer: true
```

### User-facing config examples

**FNM app with build scripts (per-app override, auto-merged with defaults):**

```typescript
// User only specifies what they need — security defaults are auto-applied by Go
const mapOfApps: BinManager.MapOfApps = {
  mmdc: {
    files: {
      "pnpm-workspace.yaml": YAML.stringify({
        allowBuilds: { puppeteer: true },
      }),
    },
    fnm: {
      packageName: "@mermaid-js/mermaid-cli",
      binPath: "node_modules/.bin/mmdc",
      version: "11.15.0",
      lockFile: "br:...",
    },
  },
};
```

**FNM app without build scripts (zero config — defaults applied automatically):**

```typescript
// No files["pnpm-workspace.yaml"] needed — Go writes secure defaults automatically
const mapOfApps: BinManager.MapOfApps = {
  eslint: {
    fnm: {
      packageName: "eslint",
      binPath: "node_modules/.bin/eslint",
      version: "9.17.0",
      lockFile: "br:...",
    },
  },
};
```

**Repo-level setup via sharedStorage (write pnpm-workspace.yaml into a project repo):**

```typescript
function getConfig(config: BinManager.Config): BinManager.Config {
  // Read datamitsu's security defaults from sharedStorage
  const pnpmDefaults = YAML.parse(config.sharedStorage?.["pnpm-workspace-defaults"] ?? "{}");

  // Extend with org-specific settings for the project repo
  const repoPnpmConfig = {
    ...pnpmDefaults,
    packages: ["packages/*"],
    optimisticRepeatInstall: true,
    resolutionMode: "lowest-direct",
    allowBuilds: { esbuild: true, sharp: true },
  };

  return {
    ...config,
    bundles: {
      "project-pnpm-config": {
        files: {
          // Written to the project repo during setup
          "pnpm-workspace.yaml": YAML.stringify(repoPnpmConfig),
        },
        links: { "pnpm-workspace": "pnpm-workspace.yaml" },
      },
    },
  };
}
```

### UV hardening

Current: `uv sync --no-install-project [--locked]`
After: `uv sync --no-install-project --no-build [--locked]`

`--no-build` prevents building source distributions, eliminating arbitrary code execution during install. Only pre-built wheels are accepted.

### Go best practices (documentation only)

```bash
# CI pipeline
go mod verify                    # Verify checksums
go mod tidy && git diff --exit-code  # Ensure go.mod/go.sum are current
GOFLAGS=-mod=readonly go build ./...  # No silent dependency changes
govulncheck ./...                # Vulnerability scan
```

## Post-Completion

**Manual verification:**

- Run `datamitsu config lockfile mmdc` with `files["pnpm-workspace.yaml"]` containing `allowBuilds: {puppeteer: true}` and verify lockfile regeneration works
- Run `datamitsu exec mmdc --version` to verify mmdc installs with puppeteer builds approved
- Verify existing configs without workspace overrides continue to work (backward compatibility)
- Verify `config.sharedStorage["pnpm-workspace-defaults"]` is accessible in JS config

**External system updates:**

- Update datamitsu-config repo: add `files["pnpm-workspace.yaml"]` with `allowBuilds` entries for apps that need build scripts (mmdc, etc.)
- Consider adding `minimumReleaseAge: 1440` to recommended defaults (future enhancement)
