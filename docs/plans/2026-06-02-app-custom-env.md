# Custom env support for all app types (`App.Env`)

## Overview

Add a single, uniform user-defined environment-variable field on **every** app
type (binary, uv, node, jvm, go, shell), injected **both at install time and at
run time**, with placeholder expansion for datamitsu-managed paths.

- **Problem:** only shell apps can set env today (`AppConfigShell.Env`). Other
  runtimes hardcode their env (`getNodeEnvVars`, `getUVEnvVars`, `getGoEnvVars`),
  so there's no way to e.g. redirect playwright to a datamitsu-controlled
  browsers dir, or set any tool-specific var.
- **Goal:** one top-level `App.Env: map[string]string`, honored by all runtimes.
  Example: `env: { PLAYWRIGHT_BROWSERS_PATH: "${STORE}/.playwright/browsers" }`
  so playwright downloads browsers into the datamitsu store — analogous to how uv
  pins `UV_PYTHON_INSTALL_DIR = {store}/.uv/python`
  ([internal/runtimemanager/uv.go:23](../../internal/runtimemanager/uv.go#L23)).
- **Integration / choke points (verified):**
  - **Run-time, ALL types:** `BinManager.GetCommandInfo` is the single dispatch
    both run paths (`GetExecCmd` ~840, `Exec` ~866) call before
    `mergeExecEnv(os.Environ(), cmdInfo.Env)`. Merging `App.Env` into the returned
    `CommandInfo.Env` there covers every app type in one place.
  - **Install-time:** only uv/node/go have a real dependency-install phase whose
    env matters (e.g. playwright's postinstall runs during `pnpm install`).
    Each runs its own install command with `buildEnvWithOverrides` — inject there
    (node ~228, uv ~153, go ~242/345).
- **Backward compat / migration:** `AppConfigShell.Env` is folded into `App.Env`
  (alpha — breaking changes acceptable per project policy). Shell apps move their
  env to the top-level field.

## Context (from discovery)

- Files/components:
  - `internal/binmanager/binmanager.go` — add `App.Env`; remove
    `AppConfigShell.Env`; merge expanded `App.Env` into `CommandInfo.Env` inside
    `GetCommandInfo`; shell construction (~552) uses `App.Env`.
  - `internal/runtimemanager/node.go` (~228 install, ~297 run via CommandInfo),
    `uv.go` (~153 install, ~254 run), `go.go` (~242/345 install, run) — pass and
    merge `app.Env` at install; run-time is covered by the GetCommandInfo merge.
  - `internal/env/` — new `ExpandPlaceholders(value, appDir string) string`
    helper (low-level pkg, no import cycle).
  - `config/config.d.ts` — add `env?` to `interface App` (~667); remove from
    `interface AppConfigShell` (~775).
  - Tests: `*_test.go` in `internal/binmanager` and `internal/runtimemanager`,
    plus `internal/env`.
- Patterns:
  - `getUVEnvVars` / `UV_PYTHON_INSTALL_DIR` ([uv.go:16](../../internal/runtimemanager/uv.go#L16))
    — the shared-store-path template.
  - uv install-time env test `TestInstallTimeEnvIncludesPythonInstallDir`
    ([uv_test.go:48](../../internal/runtimemanager/uv_test.go#L48)) — test shape to mirror.
  - `mergeExecEnv` ([binmanager.go:900](../../internal/binmanager/binmanager.go#L900)),
    `buildEnvWithOverrides` ([uv.go:258](../../internal/runtimemanager/uv.go#L258)).
- Dependency: `internal/env.GetStorePath`.

## Development Approach

- **Testing approach: TDD (tests first).**
- Small, focused tasks; finish each fully before the next.
- **Every task includes new/updated tests** (success + error/edge).
- **All tests pass before the next task.**
- Keep this plan in sync if scope shifts.
- `App.Env` optional → apps without it behave exactly as today.

## Testing Strategy

- **Unit tests** (Go) only — no e2e in this repo for this change.
- Cover: placeholder expansion; reserved/runtime keys win over custom env;
  run-time merge for each app kind (binary, node, uv, go, jvm, shell);
  install-time merge for uv/node/go; nil `Env` backward compat; shell migration.

## Progress Tracking

- `[x]` immediately on done; `➕` new tasks; `⚠️` blockers.

## What Goes Where

- **Implementation Steps** — Go patch, Go tests, TS type. All in this repo.
- **Post-Completion** — slidev config in the _datamitsu-config_ repo (`src/**`),
  applied only with explicit user confirmation.

## Implementation Steps

### Task 1: Placeholder expansion helper in `internal/env`

- [x] write tests for `env.ExpandPlaceholders(value, appDir)`: `${STORE}` →
      `GetStorePath()`, `${APP_DIR}` → `appDir`, both in one string, repeated
      tokens, no-placeholder passthrough, empty string
- [x] implement `ExpandPlaceholders` (two `strings.ReplaceAll` over the tokens)
- [x] run `go test ./internal/env/...` — must pass before Task 2

### Task 2: Add `App.Env`, remove `AppConfigShell.Env`

- [x] write/adjust tests: `App.Env` round-trips JSON (`env`); shell apps read env
      from `App.Env`; struct compiles without `AppConfigShell.Env`
- [x] add `Env map[string]string \`json:"env,omitempty"\``to`App`
- [x] remove `Env` from `AppConfigShell`; update shell `CommandInfo` construction
      (~552) to use `App.Env`
- [x] update any in-repo shell app definitions / fixtures that set `shell.env`
      (none found — only test fixture updated)
- [x] run `go test ./internal/binmanager/...` — must pass before Task 3

### Task 3: Run-time merge in `GetCommandInfo` (all app types)

- [x] write tests: for binary, node, uv, go, jvm and shell apps, a custom
      `App.Env` entry (with `${STORE}`/`${APP_DIR}`) appears expanded in the
      returned `CommandInfo.Env`; a custom key that collides with a
      datamitsu/runtime-set key does **not** override it (runtime wins); nil
      `Env` leaves `CommandInfo.Env` unchanged
- [x] in `GetCommandInfo`, after building the per-type `CommandInfo`, merge
      expanded `App.Env` into `CommandInfo.Env` with runtime keys taking
      precedence (only add keys not already set). Use the app's computed install
      path for `${APP_DIR}`
- [x] run `go test ./internal/binmanager/...` — must pass before Task 4

### Task 4: Install-time merge for uv / node / go

- [x] write tests mirroring `TestInstallTimeEnvIncludesPythonInstallDir` for
      node, uv and go: the install command env (`buildEnvWithOverrides` output)
      contains the expanded custom var; reserved runtime keys still win
- [x] thread `app.Env` into each install path and merge expanded values into the
      install env (node ~228, uv ~153, go ~242/345), reserved keys override
- [x] run `go test ./internal/runtimemanager/...` — must pass before Task 5

### Task 5: TypeScript type

- [x] add `env?: Record<string, string>;` to `interface App` (~667) in
      `config/config.d.ts`, with JSDoc documenting `${STORE}`/`${APP_DIR}` and the
      reserved-key precedence
- [x] remove `env?` from `interface AppConfigShell` (~775)
- [x] run `pnpm build` — lib build (`build:lib`) succeeds; full `pnpm build`
      packaging step needs cross-platform binaries (unrelated)

### Task 6: Verify acceptance criteria

- [x] custom env injected at install AND run for uv/node/go; at run for
      binary/jvm/shell (covered by binmanager/runtimemanager tests)
- [x] placeholders expand; runtime/reserved keys protected; nil `Env` backward
      compatible; shell migration complete (covered by tests)
- [x] `go test ./...` passes
- [x] `go vet ./...` / linter clean
- [x] `go build` succeeds

### Task 7: [Final] Documentation

- [x] document `App.Env` + placeholders + precedence where app config options are
      described; note the `shell.env` → `App.Env` migration (alpha breaking change)

## Technical Details

- `App.Env map[string]string` — optional, JSON `env`. Applies to all app kinds.
- Placeholders (expanded in Go, never written into committed `config.js`):
  - `${STORE}` → `env.GetStorePath()` (shared; cleaned by `datamitsu store clear`).
  - `${APP_DIR}` → the app's install dir (per-app, config-hashed).
- Precedence rule (uniform): custom `App.Env` is merged so that any key already
  set by datamitsu/the runtime wins — a user config can never relocate the pnpm
  store, uv cache, GOPATH, etc.
- Run-time: single merge inside `GetCommandInfo` covers every type. Install-time:
  per-runtime merge for uv/node/go (the only kinds with a dependency-install env).

## Post-Completion

_Manual / external — no checkboxes._

**datamitsu-config (`src/**`) — apply only with user confirmation:\*\*

- slidev `node` block: `env: { PLAYWRIGHT_BROWSERS_PATH: "${STORE}/.playwright/browsers" }`,
  add `playwright-chromium` to `dependencies`.
- slidev `pnpm-workspace.yaml`: `allowBuilds: { "playwright-chromium": true }` so
  the postinstall downloads the browser into the store at install time.
  **Conscious trade-off:** unverified (no SHA-256) browser download via a pnpm
  build script — permitted through the explicit `allowBuilds` trust mechanism,
  not the hashed `Archives` path. Switch to a pinned-SHA `Archives` entry if strict
  verification is later required (the env patch is reused unchanged).
- Regenerate the slidev lockfile (`datamitsu config lockfile slidev`).

**Manual verification:**

- `datamitsu exec slidev export slides.md --output deck.pdf` yields a valid PDF
  with the browser resolved from `{store}/.playwright/browsers`.
- `datamitsu store clear` removes the downloaded browser.
