# Migrate Node runtime from fnm to direct archive (url + hash)

## Overview

Replace the two-hop Node.js runtime acquisition (download the **fnm** manager binary → shell out `fnm install <version>` → move node into cache) with a **direct archive download**, exactly like the existing **jvm** runtime: a per `os/arch/libc` `{url, hash}` entry in the registry, fetched, SHA-256-verified, and extracted by datamitsu itself.

**Problem it solves:**

- **No configurable download timeout / fragile musl path.** fnm hard-codes a 30s download timeout (`internal/runtimemanager/fnm.go:34`) and pulls musl Node from the slow single-host `unofficial-builds.nodejs.org` via a dynamic mirror hack (`fnm.go:294-340`). On Alpine/CI the 34 MB musl tarball at ~400 KB/s (~88 s) blows the 30 s limit → `init --all` fails. A direct downloader (jvm-style) has no such artificial limit.
- **No supply-chain hash pinning.** fnm exposes no way to pin/verify a node hash (no `--checksum` flag/env); at best it trusts the mirror's own SHASUMS. jvm/uv pin an explicit `sha256` in `runtimes.json` (anchored in git). Node is the only runtime that cannot be pinned today.
- **fnm-only musl binary gap + manager overhead.** The fnm manager binary itself has only a glibc build in the registry, plus shell-integration/`.node-version`/`fnm use` machinery that datamitsu never needs for a pinned version.

**Decisions locked in (planning):**

- **musl:** include musl as **static** registry entries (`unofficial-builds.nodejs.org` url + sha256), exactly like jvm includes Adoptium Alpine builds. **No dynamic mirror logic** — just url + hash.
- **Backward compatibility:** **clean break.** Remove the `fnm` runtime kind, `RuntimeConfigFNM`, and `internal/runtimemanager/fnm.go` entirely. Old configs/lockfiles referencing `fnm` are no longer read. Consumers re-init.
- **Testing:** regular (implement mirroring `jvm.go`, then mirror `jvm_test.go` patterns onto node).

**Integration:** Node keeps doing what it does today _after_ it is on PATH — install pnpm (already downloaded directly from the npm registry, not via fnm) and run `node <pnpm.cjs> install` to install npm-based tools (commitlint, eslint, …). Only the **node acquisition** changes from fnm to a direct archive; the pnpm + npm-app-install flow is reused (renamed `fnm` → `node`).

## Context (from discovery)

**Target pattern to copy — JVM (archive-based):**

- `internal/runtimemanager/jvm.go:115-168` — `downloadAndVerifyJAR()`: fetch URL → stream + SHA-256 → verify (`actual != expected` errors) → move → extract. **This is the template.**
- `cmd/devtools_pull_runtimes.go:559-647` — `detectJVMBinaries()`: fetches Temurin release assets, extracts sha256 from digest, builds the `binaries` map with `extractDir: true` and a computed `binaryPath` (e.g. `jdk-26.0.1+8/bin/java`).

**What we remove — FNM (manager-based):**

- `internal/runtimemanager/fnm.go` (765 lines): `installNodeVersionOnce()` (fnm.go:362-411) downloads the fnm binary, runs `exec.Command(fnmPath, "install", nodeVersion)`, then renames `node-versions/v{v}/installation` → `v{v}/installation`. Plus: musl mirror hack `buildFNMInstallEnv/muslFNMArch` (fnm.go:294-340), `ResponseHeaderTimeout: 30 * time.Second` (fnm.go:34), PATH integration (fnm.go:586,634), npm env (`getFNMEnvVars` fnm.go:52-59).
- **Reused (rename only):** `installPNPM()`/`downloadPNPMFromRegistry()` (fnm.go:69-167) and the npm-app-install flow (fnm.go:474-612: package.json build, pnpm-lock write, `node pnpm install`).

**Registry + config:**

- `config/src/runtimes.json` — the `fnm` entry (`nodeVersion` + `pnpmVersion`/`pnpmHash` + fnm-manager binaries) → becomes a `node` entry: same node-specific config fields + per `os/arch/libc` node-archive `{url, hash, contentType, binaryPath, extractDir:true}`.
- `internal/config/config.go:126-167` — `RuntimeKind` enum (`fnm`/`jvm`/`uv`), `RuntimeConfigFNM` (`NodeVersion`, `PNPMVersion`, `PNPMHash`), `RuntimeConfig` struct.
- `internal/binmanager/hashtype.go:37-51` — `BinaryOsArchInfo {URL, Hash, ContentType, BinaryPath, ExtractDir}` — node reuses this unchanged.
- `internal/binmanager/binmanager.go:24` — `MapOfBinaries = map[OsType]map[ArchType]map[string]BinaryOsArchInfo` (os → arch → libc).
- `internal/env/runtime.go:32-37` — `GetNodeBinaryPath()` — update for the extracted-archive layout.

**devtools / registry generation:**

- `cmd/devtools_pull_runtimes.go` — `pullFNMRuntime()` (380-416), `detectRuntimeBinaries()` (651-745), `buildFNMRuntimeJSON` (310-324). Needs a `pullNodeRuntime()` that fetches node URLs + sha256 per `os/arch/libc`.
- `cmd/devtools_fnm.go` (`pull-fnm` → npm package versions in `config/src/fnmApps.json`) — rename `fnm` → `node` (`pull-node`, `nodeApps.json`); functionally unchanged (it tracks npm tool versions, not node).
- `internal/target/` (`DetectHost`, `DetectLibc`) + `syslist` — os/arch/libc detection; `resolveLibcKey()` (runtimemanager.go:233-243) glibc-fallback. Reused as-is.

**Cross-cutting `fnm`/`node` references to update (clean break):**

- `config/config.go:130` `RuntimeKindFNM`; `runtimemanager.go:52` `systemCommandForKind`; `cmd/exec.go:71` `typeOrder`; `runtimemanager.go:350-481` FNM app-path/command/dependency branches; `hash.go:60-146` FNM hash parts + `calculateFNMAppHash`; `cmd/devtools_pull_runtimes.go:112-118,310-324` FNM pull.

**Tests (regular — mirror jvm patterns):**

- `internal/runtimemanager/fnm_test.go` (2007) → `node_test.go`; `runtimemanager_test.go` (1556), `hash_test.go` (830), `multiversion_test.go` (461), `cmd/devtools_pull_runtimes_test.go` (~500), `cmd/devtools_fnm_test.go` (~200). Drop obsolete musl-mirror-wiring tests (`fnm_test.go:118-325`). Framework: Go `testing` + mock `net/http` servers + fake-binary scripts + temp cache dirs.

**Wrinkle — archive format:** node linux/darwin archives are **`.tar.xz`** (musl on unofficial-builds is xz-only); the extractor handles `tar.gz`/`zip` today (`internal/binmanager/extract.go`). **xz decompression must be added.**

## Development Approach

- **Testing approach:** Regular (implement first mirroring `jvm.go`, then add tests mirroring `jvm_test.go`).
- **Build node alongside fnm first, remove fnm last** — keeps the repo compiling/green per task. Tasks 1-6 are additive (node path built + wired); Task 7 deletes fnm in one clean cut.
- Complete each task fully before the next. Small, focused changes.
- **CRITICAL: every task includes new/updated tests** (success + error/edge cases) as separate checklist items.
- **CRITICAL: all tests pass (`go test ./...`) before starting the next task.**
- **CRITICAL: update this plan file when scope changes during implementation.**
- No dynamic mirror logic, no fallback hosts: registry is the single source of `{url, hash}`.
- Download path must **not** impose a short total timeout (glibc=nodejs.org fast; musl=unofficial-builds ~400 KB/s/~88 s). Reuse jvm's downloader characteristics (no 30 s cap; connect/idle-style if any).

## Testing Strategy

- **Unit tests:** required every task. Use Go `testing` + `httptest` mock servers serving fake node archives (small synthetic `.tar.xz`/`.zip` with a `bin/node` stub) and known sha256. Mirror `jvm_test.go`/`devtools_pull_runtimes_test.go` patterns.
- Cover: archive download success; **sha256 mismatch → error**; already-installed cache hit (no re-download); extract of `.tar.xz` and `.zip`; os/arch/libc selection incl. musl entry + glibc fallback; devtools `pull-node` builds correct entries from mock SHASUMS.
- **E2E:** datamitsu has no UI e2e. Real end-to-end (build the Alpine/Debian Docker images, both arches) is **manual** (Post-Completion) — it needs network + multi-arch and can't run as a unit test.

## Progress Tracking

- Mark completed items `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]`): Go code, registry JSON, devtools, tests, in-repo docs — all inside this repo.
- **Post-Completion** (no checkboxes): manual multi-arch Docker builds, real-musl-host verification, and updates to consuming projects (e.g. `datamitsu-config`) that must re-init after the `fnm`→`node` kind change.

## Implementation Steps

### Task 1: Add `node` runtime kind + config struct (additive)

- [x] in `internal/config/config.go` add `RuntimeKindNode RuntimeKind = "node"` and `RuntimeConfigNode { NodeVersion, PNPMVersion, PNPMHash string }` (same shape as `RuntimeConfigFNM` minus fnm-manager specifics)
- [x] add a `Node *RuntimeConfigNode` field to `RuntimeConfig` (keep the existing `FNM` field for now — additive, repo stays green)
- [x] update kind validation/parsing so `"node"` is accepted alongside existing kinds (added a `RuntimeKindNode` arm to `ValidateRuntimes` in `internal/config/validate.go`)
- [x] write tests: parsing a `node` runtime entry from JSON populates `NodeVersion`/`PNPMVersion`/`PNPMHash` (success) — `TestRuntimeConfig_NodeField_JSONRoundTrip`, `TestRuntimeConfigNode_Fields`, `TestValidateRuntimes_Node_Valid`
- [x] write tests: invalid/missing node fields error cleanly (error cases); run `go test ./internal/config/...` — must pass before next task — `TestValidateRuntimes_Node_{MissingConfig,MissingNodeVersion,MissingPNPMVersion,MissingPNPMHash,InvalidPNPMHashFormat,InvalidNodeVersion,InvalidPNPMVersion}` (config package green)

### Task 2: Add `tar.xz` extraction support to binmanager

- [x] in `internal/binmanager/extract.go` add an xz decompression branch for `contentType == "tar.xz"` (use a pure-Go xz reader, e.g. `github.com/ulikunitz/xz`; add to `go.mod`/`go.sum`) — already present: `BinContentTypeTarXz` dispatch in `extractBinary`/`extractBinaryToDir`/`extractArchiveToPath`, backed by `extractTarXz`/`extractTarXzToDir` using `github.com/ulikunitz/xz v0.5.15` (already in `go.mod`)
- [x] keep existing `tar.gz`/`zip`/`binary` paths unchanged; preserve current path-traversal guards (`validateRelativePath`) and size limits — unchanged; xz path reuses the same `validateArchivePath`/`matchPath` guards and `MaxBinarySize` limits
- [x] write tests: extract a small fixture `.tar.xz` containing `node-vX-.../bin/node` → file present, perms preserved, traversal entries rejected (success + error) — `TestExtractTarXz_NodeArchive` (extractDir preserves exec perms on `node-v26.2.0-linux-x64/bin/node` + single-file binaryPath resolve), `TestExtractTarXzToDir_RejectsPathTraversal`
- [x] write tests: malformed/truncated xz stream errors cleanly — `TestExtractTarXz_MalformedStream` (garbage + truncated), `TestExtractTarXzToDir_MalformedStream`
- [x] run `go test ./internal/binmanager/...` — must pass before next task — passes

### Task 3: Implement node archive install — `internal/runtimemanager/node.go`

- [x] create `node.go`: node archive acquisition via `installNode(runtimeName)` → `rm.GetRuntimePath` (the same generic managed-runtime path jvm/go use): selects `binaries[os][arch][libc]` (reuse `resolveLibcKey`), downloads, streams + SHA-256-verifies, extracts (`extractDir: true`), resolves `bin/node` (`node.exe` on windows) via `binaryPath`. (A self-contained jvm-`downloadAndVerifyJAR`-style downloader is not feasible — the binmanager extract helpers are package-private and a JAR needs no extraction, whereas node needs full-tree `extractDir`; `GetRuntimePath`/binmanager is the correct mirror and reuses tested infra. `AppConfigNode` added to binmanager; node arm added to `GetAppPath`.)
- [x] reuse pnpm install + npm-app-install: `node.go` adds the full node app flow (`InstallNodeApp`/`installNodeAppOnce`/`GetNodeCommandInfo`/`resolveNodeAppEnvPath`) reusing `installPNPM`/`downloadPNPMFromRegistry` + `buildPackageJSON`/`buildPNPMInstallArgs` + the workspace helpers from fnm.go (same package; the physical move/rename happens in Task 7 when fnm.go is deleted); keeps pnpm pinned-SHA-256 + npm SHA-512 verification
- [x] node binary path resolved via the generic `GetRuntimeBinaryPath("node", configHash)/<binaryPath>` extracted-archive layout (jvm/go-style, returned by `GetRuntimePath`); the fnm-specific `env.GetNodeBinaryPath` is left untouched (still used by fnm.go) and removed with fnm in Task 7 — no separate node env helper needed
- [x] **no** 30s total timeout, **no** mirror/`FNM_ARCH` logic: reuses binmanager's 5-minute download client (30s is only time-to-first-byte / dial, not a total cap), single source of truth is the git-pinned `{url, hash}`
- [x] write tests: `TestInstallNode_DownloadVerifyExtract` (download+verify+extract success, perms, content), `TestInstallNode_SHA256Mismatch` (**hash mismatch → error**), `TestInstallNode_CacheHitNoRefetch` (already-installed → cache hit, server hit count unchanged), `TestNodeLibcSelection` (musl entry selected / glibc fallback via `resolveLibcKey` — host-independent), `TestInstallNode_GlibcFallbackWhenNoMusl` (end-to-end glibc fallback download on musl host), plus `TestGetNodeEnvVars`, `TestInstallNodeApp_{InvalidRuntime,AlreadyInstalled}`, `TestGetNodeCommandInfo_{InvalidRuntime,MissingNodeConfig}`
- [x] run `go test ./internal/runtimemanager/...` — passes (also `go test ./...` green, `golangci-lint` 0 issues)

### Task 4: Route runtimemanager + hash to the `node` kind

- [x] in `internal/runtimemanager/runtimemanager.go` add `node`-kind arms to app-path (`ComputeAppPath`), command info (`GetCommandInfo`), required-runtime collection (`CollectRequiredRuntimes`), and `systemCommandForKind` (returns `node`) — additive, alongside existing fnm arms. Also added `Node *AppConfigNode` to `binmanager.App`, the `app.Node != nil` dispatch in `GetCommandInfo`/`ComputeInstallPath`, and the `"node"` type in `GetAppsList`.
- [x] in `internal/runtimemanager/hash.go` add node config parts to runtime/system hashes and a `calculateNodeAppHash` (mirror `calculateFNMAppHash`) — runtime/system hash node parts were already added in Task 3; `calculateNodeAppHash` added now and wired into `GetAppPath` for `RuntimeKindNode`
- [x] add `node` to `cmd/exec.go:71` `typeOrder` (`{"binary", "uv", "fnm", "node", "jvm", "go", "shell"}`)
- [x] write tests: `GetCommandInfo`/`ComputeAppPath` resolve correctly for a `node` runtime; node hash is stable + distinct from jvm/uv — `TestComputeAppPathNode`, `TestGetCommandInfoNode`, `TestCollectRequiredRuntimesNode`, `TestSystemCommandForKindNode`, `TestCalculateNodeAppHash`, `TestCalculateRuntimeHashNodeDistinct`, `binmanager.TestGetAppsList_NodeApp`. Updated `TestInstallNode_GlibcFallbackWhenNoMusl` to inject a no-system-node lookPath (node now has a system fallback command).
- [x] run `go test ./...` — passes (`go build ./...` clean, `golangci-lint` 0 issues on changed packages)

### Task 5: devtools — `pull-node` registry generator

- [x] in `cmd/devtools_pull_runtimes.go` add `pullNodeRuntime()`: builds the os/arch/libc tuples (`nodeArchiveSpecs`) and resolves node archive URLs + sha256 via `detectNodeBinaries`/`buildNodeBinaries`:
  - glibc/darwin/windows → `https://nodejs.org/dist/v<ver>/` (`node-v<ver>-linux-{x64,arm64}.tar.xz`, `darwin-{x64,arm64}.tar.xz`, `win-{x64,arm64}.zip`), hashes parsed from the GPG-verified `SHASUMS256.txt.asc` (`parseSHASUMS`)
  - musl → `https://unofficial-builds.nodejs.org/download/release/v<ver>/` (`linux-{x64,arm64}-musl.tar.xz`), hashes from its unsigned `SHASUMS256.txt`
- [x] **provenance at pull time:** new `internal/nodekeys` package embeds the 28 official Node.js release public keys (from `github.com/nodejs/release-keys`) and exposes `ReleaseKeyring()` + `VerifyClearsigned()`; `fetchVerifiedShasums` downloads `SHASUMS256.txt.asc`, verifies the clearsign signature against the keyring, and parses the **verified plaintext** (validated end-to-end against the real `nodejs.org` v26.2.0 signature). musl has no Node signature → recorded from unofficial-builds SHASUMS with a logged "unsigned (matches node:alpine)" notice (`detectNodeBinaries`). `ProtonMail/go-crypto/openpgp` added to `go.mod`
- [x] emit a `node` entry (`kind: "node"`, `contentType: "tar.xz"`/`"zip"`, `extractDir: true`, computed `binaryPath`, node config `{nodeVersion, pnpmVersion, pnpmHash}`) via `buildNodeRuntimeJSON`/`NodeConfigJSON`; wired `pull-node` into the `pull-runtimes` switch, `validRuntimeNames`, `runtimeVersion`, and the `--runtime node` flag/help. Hashes are mandatory: missing or non-64-hex sha256 → hard error (`buildNodeBinaries`)
- [x] write tests (mirror `devtools_pull_runtimes_test.go`): `TestDetectNodeBinaries_MockServers` (two httptest servers serving a clearsigned dist `SHASUMS256.txt.asc` + plain musl `SHASUMS256.txt`, test keyring → correct URLs/hashes/binaryPaths for every tuple), plus pure `TestBuildNodeBinaries_AllTuples`, `TestNodeArchiveSpecs_FilenamesAndPaths`, `TestNodeBinaryPath`, `TestParseSHASUMS`, `TestBuildNodeRuntimeJSON`, `TestRuntimeVersion_Node`, and `internal/nodekeys` tests (`TestReleaseKeyring_LoadsEmbeddedKeys`, `TestVerifyClearsigned_{ValidSignature,WrongKey,TamperedContent,NotClearsigned,NilKeyring}`)
- [x] write tests: missing musl asset (`TestBuildNodeBinaries_MissingMuslAsset`, `TestDetectNodeBinaries_MissingMuslAssetFromServer`) / bad signature (`TestDetectNodeBinaries_BadSignature`, signed by an untrusted key → error) handled; `go test ./cmd/...` + `./internal/nodekeys/...` pass; `golangci-lint`/cspell/gitleaks green

### Task 6: Generate the `node` registry entry + point apps at `node`

- [x] run `pull-node` to regenerate `config/src/runtimes.json` with the real `node` entry (node `26.2.0` + pnpm `11.5.0`, all 8 os/arch/libc tuples incl. musl) alongside the existing `fnm` entry — ran `devtools pull-runtimes --update --runtime node`; glibc/darwin/windows hashes GPG-verified, musl from unofficial-builds; spot-checked linux-x64 glibc+musl sha256 against upstream SHASUMS (exact match)
- [x] rename `config/src/fnmApps.json` → `config/src/nodeApps.json` (`git mv`) and switch `jscowsay`'s runtime reference from `fnm:` → `node:` in `config/src/apps.ts`; `runtimes.ts` embed unchanged (uses `as unknown as` cast). Added `node?: AppConfigNode` + `interface AppConfigNode`, `node?: RuntimeConfigNode` + `interface RuntimeConfigNode`, and `"node"` to `RuntimeKind` in `config/config.d.ts`; regenerated `internal/config/config.js`. Added `Node` branches to `internal/config/validate.go` (binPath/lockFile/runtime-ref) and `cmd/config_lockfile.go` (list/print/clear/readLockFile → pnpm-lock.yaml). Updated `package.json` `pull:fnm*` → `pull:node*`
- [x] rename `cmd/devtools_fnm.go` → `cmd/devtools_node.go`: `pull-fnm` → `pull-node` (npm package versions) targeting `nodeApps.json`; all symbols renamed (`pullNodeCmd`/`runPullNode`/`readNodeAppsJSON`/… ) — no collision with the Task 5 `pullNodeRuntime` registry generator (distinct command `pull-node` vs `pull-runtimes`)
- [x] write/update tests: `TestLoadConfigRuntimes` now asserts the embedded registry loads the `node` entry (kind node, managed mode, extractDir+hash on linux/amd64/glibc, musl entry present, darwin present, node config populated); `TestLoadConfigRuntimeApps` asserts `jscowsay` resolves to `.Node`; `cmd/devtools_node_test.go` (renamed) covers `pull-node` (apps) updating `nodeApps.json`; updated the 5 `validate_test.go` message assertions to "uv, fnm, and node apps"
- [x] run `go test ./...` — passes (36 packages ok); `go build ./...` clean; `golangci-lint` 0 issues; `datamitsu check` green (cspell, eslint, prettier, tsc, oxlint, gitleaks, syncpack, sort-package-json, editorconfig-checker all pass)

### Task 7: Remove fnm entirely (clean break)

- [x] delete `internal/runtimemanager/fnm.go` and all fnm-only code (mirror hack, `muslFNMArch`, `FNM_NODE_DIST_MIRROR`/`FNM_ARCH`, 30s timeout, fnm-binary download, `fnm install` shell-out + the move/rename step) — `fnm.go` deleted; the shared pnpm/npm-install helpers it held (`installPNPM`/`downloadPNPMFromRegistry`/`extractFullTgz`/`verifyPNPM*`/workspace+package.json builders) moved to new `internal/runtimemanager/pnpm.go` (`fnmHTTPClient`→`pnpmHTTPClient`, `writeFNMAppWorkspaceFile`→`writeAppWorkspaceFile`); all fnm-only funcs (`getFNMEnvVars`, `installNodeVersion*`, `muslFNMArch`, `buildFNMInstallEnv`, `fnmInstallEnv`, `resolveFNMAppEnvPath`, `InstallFNMApp`, `installFNMAppOnce`, `GetFNMCommandInfo`) removed. Also removed `env.GetNodeBinaryPath` (fnm-only) and renamed the pnpm cache path segment `fnm-pnpm`→`pnpm`
- [x] remove `RuntimeKindFNM`, `RuntimeConfigFNM`, the `RuntimeConfig.FNM` field, and every fnm switch/case/hash branch left in `runtimemanager.go`/`hash.go`/`cmd/exec.go` — done; also removed `binmanager.AppConfigFNM`/`App.Fnm`, `calculateFNMAppHash`, `FNMAppPathExtra`→`NodeAppPathExtra`, the `nodeInstall` singleflight, and the fnm arms in `validate.go`/`config_lockfile.go`/`devtools_verify.go`/`devtools_pull_runtimes.go`/`tooling/executor.go`
- [x] remove the `fnm` entry from `config/src/runtimes.json` and any `fnm` leftovers in `config/src/*` and devtools — fnm runtime entry deleted from `runtimes.json`; `AppConfigFNM`/`RuntimeConfigFNM`/`fnm?` fields + `"fnm"` `RuntimeKind` removed from `config/config.d.ts`; embedded `internal/config/config.js`/`config.d.ts` (git-ignored build artifacts) regenerated via `tsdown` (0 fnm refs); `pull-fnm`-runtime generator (`pullFNMRuntime`/`FNMRuntimeData`/`FNMConfigJSON`/`buildFNMRuntimeJSON`/`RuntimeJSON.FNM`) removed and `validRuntimeNames` → `{uv, jvm, node}`
- [x] write/adjust tests: a config with kind `"fnm"` is now rejected/unknown (clean-break behavior) — `TestValidateRuntimes_FNMKindUnrecognized` (config) asserts a raw `Kind:"fnm"` runtime gets no node validation; `cmd` `TestIsValidRuntime`/`TestValidRuntimeNames` assert `"fnm"`/`"FNM"` are no longer valid. fnm test suites converted/pruned across config/cmd/binmanager/env/runtimemanager (fnm_test.go → pnpm_test.go, fnm-only tests dropped, generic samples converted to node)
- [x] run `go build ./...` + `go test ./...` — must pass before next task — `go build ./...` clean, `go test ./...` all packages ok, `golangci-lint run ./...` 0 issues

### Task 8: Migrate and clean the test suite

- [x] rename `internal/runtimemanager/fnm_test.go` → `node_test.go`; convert fnm-install tests to node-archive tests (fake `.tar.xz` served over httptest); **delete obsolete musl-mirror-wiring tests** (`fnm_test.go:118-325`) — done in Task 7: `fnm_test.go` is gone, `node_test.go` exercises the full node-archive flow via `httptest` servers streaming synthetic `.tar.xz` (`TestInstallNode_DownloadVerifyExtract`/`_SHA256Mismatch`/`_CacheHitNoRefetch`); no `muslFNMArch`/`FNM_NODE_DIST_MIRROR`/`FNM_ARCH`/`buildFNMInstallEnv` references remain in any `*_test.go`
- [x] update `runtimemanager_test.go`, `hash_test.go`, `multiversion_test.go`, `cmd/devtools_pull_runtimes_test.go`, `cmd/devtools_fnm_test.go` for the `node` kind / `nodeApps.json` — all carry node coverage: `runtimemanager_test.go` (`RuntimeKindNode` fixtures, `NodeAppPathExtra`, `TestGetAppPathNode`, mixed uv+node CollectRequiredRuntimes), `hash_test.go` (`RuntimeConfigNode` runtime-hash subtests + `TestCalculateNodeAppHash`), `multiversion_test.go` (`binmanager.AppConfigNode` multi-version eslint, `/node/` path assertions), `cmd/devtools_pull_runtimes_test.go` (`buildNodeRuntimeJSON`/`NodeRuntimeData`, node entry in registry assertions); `cmd/devtools_fnm_test.go` → `cmd/devtools_node_test.go` (renamed in Task 6). Fixed a stale `fnm/uv/jvm/go arms` comment in `node_test.go`
- [x] add coverage for the new edge cases: xz extract, musl-entry selection, sha256 mismatch, pull-node signature handling — all present and passing: xz extract (`TestExtractTarXz`/`_NodeArchive`/`_RejectsPathTraversal`/`_MalformedStream` in `internal/binmanager/extract_test.go`), musl-entry selection + glibc fallback (`TestNodeLibcSelection`, `TestInstallNode_GlibcFallbackWhenNoMusl`), sha256 mismatch (`TestInstallNode_SHA256Mismatch`), pull-node signature handling (`TestDetectNodeBinaries_MockServers`/`_BadSignature`, `internal/nodekeys` `TestVerifyClearsigned_*`)
- [x] run `go test ./...` — passes (all packages ok; `golangci-lint run ./...` 0 issues; `go build ./...` clean)

### Task 9: Verify acceptance criteria

- [ ] verify all Overview requirements implemented (direct archive, pinned hash, musl static entries, no fnm, no 30s timeout)
- [ ] run full `go test ./...`; run the linter (`golangci-lint` / project lint task) — all issues fixed
- [ ] verify test coverage meets project standard (check `coverage.out` / project threshold)
- [ ] grep the repo for residual `fnm` references (code, config, docs) — none remain except intentional changelog/history
- [ ] run `go build ./...` clean

### Task 10: [Final] Update documentation

- [ ] update `README.md` / `AGENTS.md` / `CLAUDE.md` and any runtime docs describing the Node runtime: now a direct archive (url+hash) like jvm; remove fnm mentions; note `fnmApps.json` → `nodeApps.json`
- [ ] add a CHANGELOG / migration note: `fnm` runtime kind removed (clean break) — consumers must re-init; node now pinned by sha256
- [ ] document the `devtools pull-node` workflow (how to bump the node version + refresh hashes, incl. glibc `.asc` provenance check)

_Note: ralphex automatically moves completed plans to `docs/plans/completed/`._

## Technical Details

**Target node `runtimes.json` entry (mirrors `jvm`):**

```json
{
  "node": {
    "kind": "node",
    "mode": "managed",
    "managed": {
      "binaries": {
        "linux": {
          "amd64": {
            "glibc": {
              "url": "https://nodejs.org/dist/v26.2.0/node-v26.2.0-linux-x64.tar.xz",
              "hash": "<sha256>",
              "contentType": "tar.xz",
              "binaryPath": "node-v26.2.0-linux-x64/bin/node",
              "extractDir": true
            },
            "musl": {
              "url": "https://unofficial-builds.nodejs.org/download/release/v26.2.0/node-v26.2.0-linux-x64-musl.tar.xz",
              "hash": "<sha256>",
              "contentType": "tar.xz",
              "binaryPath": "node-v26.2.0-linux-x64-musl/bin/node",
              "extractDir": true
            }
          },
          "arm64": {
            "glibc": { "...": "linux-arm64.tar.xz" },
            "musl": { "...": "linux-arm64-musl.tar.xz" }
          }
        },
        "darwin": {
          "amd64": { "unknown": { "...": "darwin-x64.tar.xz" } },
          "arm64": { "unknown": { "...": "darwin-arm64.tar.xz" } }
        },
        "windows": {
          "amd64": {
            "unknown": { "...": "win-x64.zip", "binaryPath": "node-v26.2.0-win-x64/node.exe" }
          }
        }
      }
    },
    "node": { "nodeVersion": "26.2.0", "pnpmVersion": "11.x", "pnpmHash": "<sha256>" }
  }
}
```

**Archive naming (devtools `pull-node` must generate):**

| os      | arch        | libc    | filename                            | host              |
| ------- | ----------- | ------- | ----------------------------------- | ----------------- |
| linux   | amd64       | glibc   | `node-vX-linux-x64.tar.xz`          | nodejs.org/dist   |
| linux   | amd64       | musl    | `node-vX-linux-x64-musl.tar.xz`     | unofficial-builds |
| linux   | arm64       | glibc   | `node-vX-linux-arm64.tar.xz`        | nodejs.org/dist   |
| linux   | arm64       | musl    | `node-vX-linux-arm64-musl.tar.xz`   | unofficial-builds |
| darwin  | amd64/arm64 | unknown | `node-vX-darwin-{x64,arm64}.tar.xz` | nodejs.org/dist   |
| windows | amd64/arm64 | unknown | `node-vX-win-{x64,arm64}.zip`       | nodejs.org/dist   |

**Removed fnm concepts (file:line):** mirror hack `fnm.go:294-340`; `muslFNMArch` `fnm.go:302-310`; 30s timeout `fnm.go:34`; fnm install + move `fnm.go:362-411`; `RuntimeKindFNM` `config.go:130`; `RuntimeConfigFNM` `config.go:143-147`.

**Kept/renamed (node still needs them):** `installPNPM`/`downloadPNPMFromRegistry` (pnpm direct from npm registry, pinned hash + SHA-512); npm-app-install (`node <pnpm.cjs> install`, package.json/lockfile build) `fnm.go:474-612`; os/arch/libc detection `internal/target/*` + `resolveLibcKey` `runtimemanager.go:233-243`.

**Provenance:** at `pull-node` time, glibc hashes verified against `nodejs.org` `SHASUMS256.txt.asc` (GPG, Node release keys) → strong supply-chain anchor; musl from unofficial-builds `SHASUMS256.txt` (unsigned) → recorded as pinned hash in git (same trust model as `node:alpine`). At runtime: hash-only check against the git-pinned `sha256` (no signature needed — anchor is the repo).

## Post-Completion

_Items requiring manual intervention or external systems — no checkboxes, informational only._

**Manual verification:**

- Build the Docker images for **both** arches and variants, confirming `init --all` installs node without fnm and without timing out on the slow musl mirror:
  - `docker buildx build --platform linux/amd64,linux/arm64 -f docker/Dockerfile.alpine .` (musl)
  - `docker buildx build --platform linux/amd64,linux/arm64 -f docker/Dockerfile .` (glibc)
- Run `datamitsu init --all` on a real Alpine/musl host and a glibc host; confirm node + pnpm + npm tools install.
- Spot-check macOS (darwin-arm64) and Windows (win-x64) node resolution if those targets are supported.

**External system updates:**

- **Consuming projects (e.g. `datamitsu-config`)** must re-init after upgrading: the `fnm` runtime kind is removed (clean break). Any external config/lockfile referencing kind `"fnm"` will no longer load.
- Bump the datamitsu release version; consumers pin the new version and re-run `datamitsu init`.
- The base Docker images (`ghcr.io/datamitsu/datamitsu*`) rebuild with the new node-archive flow — verify the published images install node correctly before announcing.
