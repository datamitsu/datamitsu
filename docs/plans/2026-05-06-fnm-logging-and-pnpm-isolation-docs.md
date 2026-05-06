# FNM Logging and pnpm Settings via Files

## Overview

Two independent improvements:

1. **Remove `--silent` from pnpm install** — the flag suppresses all pnpm output including
   errors. On install failure the user only sees "exit status 1" with no context. UV already
   streams output in real time — pnpm/FNM should behave the same way.

2. **Documentation: controlling pnpm settings via `files`** — wrapper maintainers can include
   `pnpm-workspace.yaml` in FNM apps via `App.files` to manage per-app pnpm supply chain
   settings: `verifyDepsBeforeRun`, `enablePrePostScripts`, `onlyBuiltDependencies`, etc.
   The mechanism is already supported by the core via `WriteAppFiles` — it just needs to be
   documented.

## Context (from discovery)

- **Primary files**:
  - `internal/runtimemanager/fnm.go` — `buildPNPMInstallArgs` (line 530), `installFNMAppOnce` (lines 378–497)
  - `internal/runtimemanager/fnm_test.go` — `TestBuildPNPMInstallArgs` (lines 968–993)
  - `website/docs/how-to/maintain-wrapper.md` — Best Practices section (line 274+)
- **UV comparison**: `internal/runtimemanager/uv.go` — already streams without `--silent` (lines 126–127)
- **Mechanism confirmed**: `files` in `App` are written via `WriteAppFiles` before the core writes `package.json`; `pnpm-workspace.yaml` is never overwritten by the core — including it via `files` works correctly

## Development Approach

- **Testing approach**: Regular (tests already exist; update expected values)
- Small, logically independent changes
- Tests must pass before moving to the next task

## Testing Strategy

- **Unit tests**: update existing `TestBuildPNPMInstallArgs` — remove `--silent` from expected values
- No UI/e2e tests for these changes

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Update this file if implementation deviates from plan

## What Goes Where

- **Implementation Steps**: code and documentation changes in this repository
- **Post-Completion**: verification in the wrapper repository (`datamitsu-config`)

## Implementation Steps

### Task 1: Remove `--silent` from pnpm install args

- [x] in `internal/runtimemanager/fnm.go`, function `buildPNPMInstallArgs` (line 530): remove `"--silent"` from the args slice
- [x] confirm `cmd.Stdout = os.Stderr` and `cmd.Stderr = os.Stderr` remain in place (lines 477–478, already present)
- [x] in `internal/runtimemanager/fnm_test.go`, test `TestBuildPNPMInstallArgs` (lines 968–993): remove `"--silent"` from expected values in both sub-tests (`without lockfile` and `with lockfile`)
- [x] run `go test ./internal/runtimemanager/...` — all tests must pass

### Task 2: Document pnpm security settings via `files`

- [x] in `website/docs/how-to/maintain-wrapper.md`, Best Practices section (after line 309): add `### Configuring pnpm Settings for FNM Apps`
- [x] section explains: a wrapper maintainer can include `pnpm-workspace.yaml` via `App.files` to control per-app pnpm supply chain settings (`verifyDepsBeforeRun`, `enablePrePostScripts`, `onlyBuiltDependencies`, etc.)
- [x] minimal example: `files: { "pnpm-workspace.yaml": "packages: []\n" }`
- [x] extended example with security settings (`verifyDepsBeforeRun: install`)
- [x] note on UV: project-level settings are configured via `pyproject.toml`, but any `pyproject.toml` included in `files` will be overwritten by the core — this customization path is not available for UV
- [x] final check: `go test ./internal/runtimemanager/...`

### Task 3: Verify acceptance criteria

- [ ] `buildPNPMInstallArgs` does not contain `--silent`
- [ ] `go test ./internal/runtimemanager/...` — all tests green
- [ ] `maintain-wrapper.md` contains the new section with code examples

## Technical Details

**`buildPNPMInstallArgs` before/after:**

```go
// Before:
args := []string{pnpmCjsPath, "install", "--silent"}
// With lockfile: [pnpmCjsPath, "install", "--silent", "--frozen-lockfile"]

// After:
args := []string{pnpmCjsPath, "install"}
// With lockfile: [pnpmCjsPath, "install", "--frozen-lockfile"]
```

**Documentation example (minimal):**

```js
const myApp = {
  files: {
    "pnpm-workspace.yaml": "packages: []\n",
  },
  fnm: { ... },
};
```

**Documentation example (with security settings):**

```js
const myApp = {
  files: {
    "pnpm-workspace.yaml": [
      "packages: []",
      "verifyDepsBeforeRun: install",
      "",
    ].join("\n"),
  },
  fnm: { ... },
};
```

## Post-Completion

**Verify in the wrapper repo** (`datamitsu-config`):

- Run `DATAMITSU_LOG_LEVEL=debug datamitsu config lockfile eslint` — on failure the full pnpm output should now be visible
- Add `pnpm-workspace.yaml` via `files` to the relevant FNM apps
