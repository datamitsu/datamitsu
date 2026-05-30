# fnm musl Support — Auto-detect musl and Fetch musl Node.js Builds

## Overview

On musl hosts (Alpine Linux), datamitsu's fnm runtime installs **glibc** Node.js
binaries from the default mirror (`nodejs.org`). Those binaries install fine but
**cannot execute** on musl because their ELF interpreter
(`/lib64/ld-linux-x86-64.so.2`) does not exist there. The failure surfaces as a
misleading "no such file or directory":

```text
error: failed to install runtime apps with links: failed to install commitlint:
failed to install FNM app "commitlint": fork/exec
/home/datamitsu/.cache/datamitsu/store/.runtimes/fnm-nodes/v26.2.0/installation/bin/node:
no such file or directory
```

(Observed in datamitsu-config CI, `Docker Build Test (alpine)`, run 26687705192.)

**Fix:** when the host libc is musl, configure fnm to download musl-linked Node
from the unofficial-builds mirror by injecting two environment variables into the
`fnm install` child process — **only when the user has not already set them**:

```bash
FNM_NODE_DIST_MIRROR=https://unofficial-builds.nodejs.org/download/release
FNM_ARCH=<arch>-musl   # x64-musl on amd64, arm64-musl on arm64
```

**Benefits:**

- Alpine/musl Docker builds and runtimes work out of the box — no per-wrapper config.
- Fixes every musl user at once instead of each wrapper repeating the workaround.
- Extends datamitsu's existing musl-awareness (which already auto-falls-back the
  fnm _binary itself_) into the Node.js _download_ path.

**User-chosen scope: "Both"** — implement auto-detection in datamitsu core AND
document the env-override as a supported escape hatch (for custom/air-gapped
mirrors). The user always wins: an explicitly set `FNM_NODE_DIST_MIRROR` /
`FNM_ARCH` is never overridden.

## Context (from discovery)

**Files/components involved:**

- `internal/runtimemanager/fnm.go` — `installNodeVersionOnce` (L293-344) is the
  **only** place datamitsu spawns `fnm install`; today it sets just `FNM_DIR`
  (L311-314).
- `internal/target/target.go` — `Target{OS, Arch, Libc}`, `DetectHost()`, and the
  `LibcMusl` / `LibcGlibc` / `LibcUnknown` constants.
- `internal/runtimemanager/runtimemanager.go` — `rm.hostTarget` (set via
  `target.DetectHost()` in `New()`); existing musl fallback logging
  (`resolveEffectiveRuntimeConfig` L66-126, `GetRuntimePath` L185-190).
- `internal/runtimemanager/fnm_test.go` — test conventions for this package.

**Related patterns found:**

- `buildEnvWithOverrides(base, overrides)` merges env maps (uv.go) — reuse it.
- `newTestRMWithTarget(runtimes, hostTarget)` (runtimemanager_test.go L1170)
  injects an arbitrary host `Target` — lets tests simulate musl without Alpine.
- `log.Warn/Info` with `zap.String(...)` is the established logging style for
  libc-related decisions.

**Dependencies identified:**

- fnm reads `FNM_NODE_DIST_MIRROR` and `FNM_ARCH` from its environment.
- unofficial-builds.nodejs.org publishes both `x64-musl` and `arm64-musl`
  (verified for v26.2.0: `node-v26.2.0-linux-{x64,arm64}-musl.tar.{gz,xz}`).
  No musl builds for other arches (arm/armv7l, 386, ppc64le, …) → leave fnm on
  its default mirror for those.

## Development Approach

- **Testing approach: TDD (tests first)** — write failing table-driven tests for
  the pure helpers, then implement until green.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in it.
- **CRITICAL: all tests must pass before starting the next task** — no exceptions.
- Keep helpers pure (no process spawning) so they are unit-testable; isolate the
  `exec.Command` call to a thin wiring layer.
- Maintain backward compatibility (glibc hosts and explicit-env users see no change).

## Testing Strategy

- **Unit tests** (required, per task):
  - `muslFNMArch` — arch→token mapping incl. unsupported arches.
  - `buildFNMInstallEnv` / `applyMuslNodeDist` — full matrix: glibc no-op,
    musl+supported-arch sets both vars, musl+unsupported-arch no-op, `LibcUnknown`
    no-op, and **env precedence** (user-set `FNM_NODE_DIST_MIRROR` / `FNM_ARCH`
    via `t.Setenv` are preserved).
- **E2E tests**: none — this project has no UI e2e harness; the `fnm install`
  subprocess is not exercised in unit tests (kept behind the pure env-builder).

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update this plan if implementation deviates from the original scope.

## What Goes Where

- **Implementation Steps** (`[ ]`): datamitsu code, tests, datamitsu's own docs
  (website/docs), and datamitsu's **own** `docker/Dockerfile.alpine` — the image
  the wrapper builds `FROM`, i.e. **where Node actually runs**.
- **Post-Completion** (no checkboxes): re-running the wrapper's CI and bumping its
  pinned datamitsu version. The wrapper needs **no** Dockerfile change — it
  inherits `libstdc++` from the datamitsu base image.

## Implementation Steps

### Task 1: Add `muslFNMArch` arch-mapping helper (pure)

- [x] write table-driven test `TestMuslFNMArch` in `internal/runtimemanager/fnm_test.go`:
      `amd64`→`"x64-musl"`, `arm64`→`"arm64-musl"`, and `arm`/`386`/`ppc64le`/`""`→`""`
- [x] run test — confirm it FAILS (function not yet defined)
- [x] implement `const fnmMuslNodeDistMirror = "https://unofficial-builds.nodejs.org/download/release"`
      and `func muslFNMArch(goarch string) string` in `internal/runtimemanager/fnm.go`
- [x] run `go test ./internal/runtimemanager/...` — must pass before Task 2

### Task 2: Add musl env-builder with user-env precedence (pure)

- [x] write test `TestBuildFNMInstallEnv` (uses `t.Setenv`):
  - glibc host → map contains only `FNM_DIR`
  - musl + `amd64` → adds `FNM_NODE_DIST_MIRROR` + `FNM_ARCH=x64-musl`
  - musl + `arm64` → adds `FNM_ARCH=arm64-musl`
  - musl + unsupported arch (`arm`) → only `FNM_DIR`
  - `LibcUnknown` host → only `FNM_DIR`
  - musl + user-set `FNM_NODE_DIST_MIRROR` → value preserved, not overridden
  - musl + user-set `FNM_ARCH` → value preserved, not overridden
- [x] run test — confirm it FAILS
- [x] implement `func buildFNMInstallEnv(host target.Target, fnmDir string) map[string]string`
      in `fnm.go`: starts from `{"FNM_DIR": fnmDir}`, and when `host.Libc == target.LibcMusl`
      and `muslFNMArch(host.Arch) != ""`, sets each var only if absent via `os.LookupEnv`
- [x] add `"github.com/datamitsu/datamitsu/internal/target"` import to `fnm.go`
- [x] run `go test ./internal/runtimemanager/...` — must pass before Task 3

### Task 3: Wire env-builder into `installNodeVersionOnce` + observability

- [x] write/extend test asserting the install path uses `buildFNMInstallEnv` for
      a musl `rm.hostTarget` (construct via `newTestRMWithTarget`; assert the
      resulting env map carries the musl mirror/arch). Keep the assertion at the
      env-map level — do NOT spawn real `fnm`.
- [x] run test — confirm it FAILS / reflects new behavior
- [x] in `installNodeVersionOnce`, replace the inline
      `buildEnvWithOverrides(os.Environ(), map[string]string{"FNM_DIR": fnmDir})`
      with `buildEnvWithOverrides(os.Environ(), buildFNMInstallEnv(rm.hostTarget, fnmDir))`
- [x] add a one-time `log.Info` (zap) when the musl override is applied:
      message e.g. `"configuring fnm for musl Node.js builds"` with
      `zap.String("mirror", …)` and `zap.String("arch", …)`; skip the log when the
      user supplied the env vars themselves
- [x] run `go test ./internal/runtimemanager/...` — must pass before Task 4

### Task 4: Add `libstdc++` to datamitsu's Alpine image (`docker/Dockerfile.alpine`)

The wrapper builds `FROM ghcr.io/datamitsu/datamitsu:<v>-alpine`, so Node runs
inside **datamitsu's** Alpine image, not the wrapper's. musl Node from
unofficial-builds dynamically links `libstdc++` (and `libgcc`), which base
`alpine` lacks — without it the build fails at the next step even after the
mirror fix.

- [ ] add `libstdc++` to the existing `apk add --no-cache` block in
      `docker/Dockerfile.alpine` (L22-24)
- [ ] **keep `git`** — it is NOT redundant despite go-git/v6: datamitsu shells out
      to the `git` binary for git-root detection (`internal/traverser/git.go`
      `GetGitRoot`, `internal/facts/facts.go`) and `git diff --cached`
      (`internal/runner/runner.go`); go-git/v6 is used only in
      `internal/traverser/q.go` + `cmd/exec_todo.go`. The wrapper's `RUN git init`
      also needs the binary.
- [ ] `libgcc` is pulled transitively by `libstdc++` — no separate entry needed
- [ ] glibc image `docker/Dockerfile` is unaffected (glibc Node is not remirrored)
- [ ] (no Go unit test applies) acceptance is the Alpine image build in Task 6

### Task 5: Document the musl behavior + escape hatch (datamitsu docs)

- [ ] search `website/docs` for existing musl/Alpine runtime docs
      (`grep -ri "musl\|alpine" website/docs`); pick the closest runtime/FNM page
- [ ] document: on musl hosts datamitsu auto-selects unofficial musl Node builds;
      override via `FNM_NODE_DIST_MIRROR` / `FNM_ARCH` (escape hatch for custom or
      air-gapped mirrors); note these env vars always take precedence
- [ ] add the **Alpine `libstdc++` requirement** note (musl Node dynamically links
      `libstdc++`; `apk add libstdc++`) so users don't hit the next failure
- [ ] run docs build/lint if the project has one (e.g. `pnpm` docs script); fix issues

### Task 6: Verify acceptance criteria

- [ ] verify all Overview requirements implemented (musl→musl Node, env precedence, glibc unchanged)
- [ ] verify edge cases: `LibcUnknown`, unsupported arch, user-set env
- [ ] run full Go test suite: `go test ./...`
- [ ] run linter (golangci-lint via datamitsu) — all issues fixed
- [ ] `pnpm build` then `go build` succeeds (JS embed step required — `go install` does not work)
- [ ] build the Alpine image (`docker/Dockerfile.alpine`) for `linux/amd64` and
      `linux/arm64`; in the image confirm a managed FNM app's `node --version`
      runs (proves musl Node executes + `libstdc++` present), not just installs

## Technical Details

**Arch mapping (`muslFNMArch`):**

| GOARCH | FNM_ARCH     | musl build exists? |
| ------ | ------------ | ------------------ |
| amd64  | `x64-musl`   | yes                |
| arm64  | `arm64-musl` | yes (experimental) |
| others | `""` (no-op) | no                 |

**Env precedence:** use `os.LookupEnv` (not `buildEnvWithOverrides`, which always
overwrites) to decide whether to inject each var, so an explicitly-set value wins.

**Env-var access policy:** `FNM_NODE_DIST_MIRROR` and `FNM_ARCH` are **third-party
(fnm)** env vars, not `DATAMITSU_*`. Per CLAUDE.md this makes direct
`os.LookupEnv` acceptable (same class as `PATH`, `GITHUB_TOKEN`) — no
`internal/env` wrapper required.

**Security policy:** datamitsu does **not** download Node.js directly — fnm does,
and fnm verifies the downloaded tarball against the mirror's `SHASUMS256.txt`.
Switching the mirror keeps fnm's own integrity check intact. This is outside
datamitsu's mandatory-hash-field policy (which governs datamitsu's _own_ network
downloads), so no `hash` field is involved here. Call this out in the PR so a
reviewer doesn't read it as bypassing the hash policy.

**Processing flow:**

```text
installNodeVersionOnce(fnmPath, nodeVersion, cacheRoot)
  fnmDir := {cacheRoot}/.runtimes/fnm-nodes
  env := buildFNMInstallEnv(rm.hostTarget, fnmDir)   // {FNM_DIR}[, FNM_NODE_DIST_MIRROR, FNM_ARCH]
  cmd := exec.Command(fnmPath, "install", nodeVersion)
  cmd.Env = buildEnvWithOverrides(os.Environ(), env)
  cmd.Run()
```

## Post-Completion

_Items requiring the separate wrapper repo or a datamitsu release — informational only._

**Release dependency (the fix reaches the wrapper only after a datamitsu release):**

- This change lands in datamitsu; the wrapper consumes it only once a new datamitsu
  **release + ghcr `:*-alpine` image** is published. The wrapper's Alpine build
  stays red until it points at that image.

**datamitsu-config wrapper repo:**

- **No Dockerfile change needed for `libstdc++`** — the wrapper builds `FROM
ghcr.io/datamitsu/datamitsu:<v>-alpine` and inherits it from the base image once
  Task 4 ships.
- Bump the wrapper's pinned datamitsu version (`package.json` + the `FROM` tag,
  currently `0.0.16-alpine`) to the release containing this fix.
- The wrapper does **not** need to set `FNM_NODE_DIST_MIRROR` / `FNM_ARCH`
  manually — datamitsu auto-detects musl. It may still pin them for a custom mirror.
- Re-run the `Docker Build Test (alpine)` job (PR #27) to confirm `datamitsu init
--all` completes on both `linux/amd64` and `linux/arm64`.
