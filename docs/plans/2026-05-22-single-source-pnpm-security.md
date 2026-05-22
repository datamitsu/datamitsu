# Single Source of Truth for pnpm Workspace Security Defaults

## Overview

The 8 pnpm workspace security settings (`strictDepBuilds`, `blockExoticSubdeps`, `enablePrePostScripts`, `dangerouslyAllowAllBuilds`, `minimumReleaseAge`, `trustPolicy`, `lockfile`, `preferFrozenLockfile`) are currently defined independently in Go (`fnm.go`) and JS (`config.js`/`main.ts`), then repeated verbatim in 4 documentation files. This creates a maintenance burden — any change must be made in 6+ places.

**Goal:** Single definition in Go core. JS reads from Go-injected global. Documentation references one canonical page.

**What changes:**

- Go injects `pnpmWorkspaceDefaults` into the goja VM before running config scripts
- JS `config.js` reads the injected global instead of hardcoding the object
- `config/src/main.ts` removes the hardcoded `pnpmWorkspaceDefaults` const
- The JS↔Go agreement test is removed (no separate copies to sync)
- Docs consolidated: full table only in `supply-chain-security.md`; other pages link there

## Context (from discovery)

**Files/components involved:**

| File                                           | Current role                                             | After                                            |
| ---------------------------------------------- | -------------------------------------------------------- | ------------------------------------------------ |
| `internal/runtimemanager/fnm.go`               | `defaultPNPMWorkspaceConfig()` — Go authoritative source | **Stays** — single source of truth               |
| `config/src/main.ts`                           | Hardcoded `pnpmWorkspaceDefaults` object                 | **Remove** hardcoded object, read from global    |
| `internal/config/config.js` (compiled)         | Contains hardcoded copy from main.ts                     | **Generated** — will use injected global         |
| `internal/engine/engine.go`                    | VM initialization, `facts()` injection                   | **Add** `pnpmWorkspaceDefaults` injection        |
| `cmd/config_loader.go`                         | Orchestrates config loading                              | **Minor** — ensure injection happens before eval |
| `internal/runtimemanager/fnm_test.go`          | `TestPNPMWorkspaceDefaults_JSGoAgreement`                | **Remove** test (no longer needed)               |
| `website/docs/guides/supply-chain-security.md` | Full 8-key table + YAML block                            | **Keep** as canonical reference                  |
| `website/docs/examples/pnpm-patterns.md`       | Full YAML block of merged output                         | **Replace** with short note + link               |
| `docs/architecture.md`                         | Inline list of all 8 defaults                            | **Replace** with reference                       |
| `website/docs/how-to/maintain-wrapper.md`      | Mentions defaults                                        | **Replace** with reference                       |
| `config/config.d.ts`                           | JSDoc lists defaults by name                             | **Simplify** JSDoc                               |

**Pattern used:** Same as `facts()` — Go calls `vm.Set("name", value)` before executing config scripts. Already proven in `engine.go:115-118`.

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change

## Testing Strategy

- **Unit tests**: required for every task
- Verify that `sharedStorage["pnpm-workspace-defaults"]` still contains correct YAML after injection
- Verify `defaultPNPMWorkspaceConfig()` values flow through to JS config output
- Verify FNM app install still merges correctly

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Move pnpm defaults injection from JS to Go engine

The core change: Go injects defaults into the goja VM, JS reads the global instead of hardcoding.

- [x] Export `defaultPNPMWorkspaceConfig()` from `internal/runtimemanager/fnm.go` — rename to `DefaultPNPMWorkspaceConfig()` or create a new package-level accessor (since engine.go needs it)
  - **Decision:** moved the map to a new shared `internal/pnpmdefaults` package (`pnpmdefaults.Defaults()`); both `runtimemanager.defaultPNPMWorkspaceConfig` and the new engine init read from it. Avoids tying `engine` to `runtimemanager` and keeps `engine.New("")` callable without extra options.
- [x] In `internal/engine/engine.go`: add an init method (like `initFacts()`) that calls `vm.Set("pnpmWorkspaceDefaults", defaults)` to inject the map as a JS global
- [x] Call the new init method during engine construction (alongside `initFacts()`)
- [x] Write test: create a goja VM with the injection, run a script that reads `pnpmWorkspaceDefaults`, verify all 8 keys match `DefaultPNPMWorkspaceConfig()`
- [x] Write test: verify `pnpmWorkspaceDefaults` is a plain object accessible in JS (not a function call like `facts()`)
- [x] Run tests — must pass before next task

### Task 2: Update JS config to use injected global instead of hardcoded object

- [x] In `config/src/main.ts`: remove the `const pnpmWorkspaceDefaults = { ... }` object (lines 4-13)
- [x] In `config/src/main.ts`: update `getConfig()` to use the global `pnpmWorkspaceDefaults` directly — it's already available via `vm.Set()` from Task 1
  - The line `"pnpm-workspace-defaults": YAML.stringify(pnpmWorkspaceDefaults)` stays as-is syntactically — it just reads from the injected global now
  - Also added a `const pnpmWorkspaceDefaults: Record<string, unknown>` declaration to `config/config.d.ts` so TS understands the global
- [x] Rebuild `internal/config/config.js` (run the build/compile step that generates it from `config/src/`)
- [x] Write test: load the default config via full config chain, verify `sharedStorage["pnpm-workspace-defaults"]` contains all 8 keys with correct values
  - Added `TestSharedStoragePNPMWorkspaceDefaultsRoundTrip` in `cmd/config_loader_test.go` that goes through `loadConfigWithPaths` and parses the YAML against `pnpmdefaults.Defaults()`
  - Updated `internal/config/config_test.go::TestDefaultConfigPNPMWorkspaceDefaults` to inject the global (script no longer self-defines it)
  - Updated `internal/runtimemanager/fnm_test.go::TestPNPMWorkspaceDefaults_JSGoAgreement` to inject the global (kept passing for now; Task 3 removes it entirely)
- [x] Run tests — must pass before next task

### Task 3: Remove the JS↔Go agreement test

The agreement test exists only because there were two independent copies. With a single source, it's unnecessary.

- [x] Remove `TestPNPMWorkspaceDefaults_JSGoAgreement` from `internal/runtimemanager/fnm_test.go`
- [x] Remove `pnpmWorkspaceValueEqual` helper if no other tests use it
  - **Kept** — helper is still used by 21 other pnpm-workspace assertions in the same file; removing it would be unrelated churn.
- [x] Verify no other tests depend on these functions
  - Confirmed via grep: only `pnpmWorkspaceValueEqual` is referenced elsewhere (kept); the `goja` import (only used by the removed test) was removed too.
- [x] Run tests — must pass before next task

### Task 4: Consolidate documentation — canonical page stays, others link

- [ ] `website/docs/guides/supply-chain-security.md` — **keep as-is** (canonical reference with full table and YAML block)
- [ ] `website/docs/examples/pnpm-patterns.md` — remove the full YAML block showing all 8 defaults; replace with a short note saying "datamitsu applies secure defaults automatically" + link to supply-chain-security.md; keep the `allowBuilds` example (that's the useful part)
- [ ] `docs/architecture.md` — replace the inline parenthetical listing all 8 keys with a shorter reference: mention that defaults are defined in `defaultPNPMWorkspaceConfig()` in `fnm.go` and link to supply-chain-security.md for the full list
- [ ] `website/docs/how-to/maintain-wrapper.md` — already references supply-chain-security.md; verify no inline duplication of the 8 keys remains; clean up if needed
- [ ] `config/config.d.ts` — simplify the JSDoc on `sharedStorage` and `files` to not enumerate all 8 keys; reference the guide page instead
- [ ] Review changes for broken links or missing context
- [ ] Run website build if applicable to verify no markdown errors

### Task 5: Verify acceptance criteria

- [ ] Verify `defaultPNPMWorkspaceConfig()` in `fnm.go` is the ONLY place the 8 defaults are defined as code
- [ ] Verify `config.js` no longer contains a hardcoded copy
- [ ] Verify `sharedStorage["pnpm-workspace-defaults"]` still works for downstream configs
- [ ] Verify FNM app install applies defaults correctly (existing fnm tests should cover this)
- [ ] Run full test suite (`go test ./...`)
- [ ] Run linter — all issues must be fixed

### Task 6: [Final] Update documentation

- [ ] Update `docs/architecture.md` if the injection mechanism needs documenting (how pnpmWorkspaceDefaults flows from Go → JS)
- [ ] Update CLAUDE.md if any new patterns emerged

## Technical Details

**Injection mechanism:**

```go
// In engine.go or config_loader.go, before running any config script:
vm.Set("pnpmWorkspaceDefaults", runtimemanager.DefaultPNPMWorkspaceConfig())
```

**JS side (after change):**

```typescript
// config/src/main.ts — no more hardcoded object
// pnpmWorkspaceDefaults is a global injected by Go

function getConfig(config: config.Config): config.Config {
  return {
    ...config,
    sharedStorage: {
      ...config.sharedStorage,
      "datamitsu-agent-prompt": DATAMITSU_AGENT_GUIDE,
      "pnpm-workspace-defaults": YAML.stringify(pnpmWorkspaceDefaults),
    },
  };
}
```

**Data flow after consolidation:**

```
Go: DefaultPNPMWorkspaceConfig() map[string]any
    ↓ vm.Set("pnpmWorkspaceDefaults", ...)
JS: global pnpmWorkspaceDefaults → YAML.stringify → sharedStorage
    ↓ config output
Go: config.SharedStorage["pnpm-workspace-defaults"]
    ↓ downstream configs can read & extend

Go: DefaultPNPMWorkspaceConfig() → mergePNPMWorkspaceConfig() → pnpm-workspace.yaml
    (FNM app install — no JS involved, same single source)
```

## Post-Completion

**Manual verification:**

- Install an FNM app and inspect the generated `pnpm-workspace.yaml` to confirm defaults applied
- Build a downstream config that reads `sharedStorage["pnpm-workspace-defaults"]` and verify it works

**Dependency consideration:**

- `config/src/main.ts` change means the compiled `config.js` must be regenerated
- If there are external wrapper configs that import `pnpmWorkspaceDefaults` by name (unlikely — it's not exported), they would break. The sharedStorage API is the public contract and stays stable.
