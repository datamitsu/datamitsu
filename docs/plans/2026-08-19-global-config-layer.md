# Plan: Activation outside a git repository — machine-level toolchains via explicit `--config`

**Status:** ready for implementation.
**Date:** 2026-08-19.
**Depends on:** `docs/plans/2026-08-19-cli-startup-cost.md` and
`docs/plans/2026-08-19-source-mode.md`. This plan is decision **D3** of the source-mode plan.
**Related:** `cmd/root.go`, `cmd/config_loader.go`, `internal/env`, `internal/sourcefarm`.

**Branch:** `global-config-layer`, branched from `source-mode` (**not** from `main` — this plan
reuses `internal/sourcefarm` and the `source` command built there). Third of three stacked
plans.
Review base ref: `source-mode` — pass `--base-ref=source-mode`, or the review pass re-reviews
both previous plans' diffs as if they were new work.

## Overview

Source mode is project-scoped: it needs a discovered config, and config discovery is
git-root-only. `facts.GetGitRoot()` resolves the repository root, then `discoverAutoConfig()`
stats exactly three filenames — `datamitsu.config.{js,mjs,ts}` — at that root and nowhere
else. There is no upward walk from cwd, no `.datamitsu/` config lookup and no `$HOME`
fallback. After the source-mode plan, running `datamitsu source` outside a repository exits
with an error pointing at `--config`.

This plan makes that pointer true: activating a **machine-level toolchain** from a config file
the user names explicitly.

```bash
# in ~/.config/fish/config.fish
datamitsu source fish --config ~/.config/datamitsu/datamitsu.config.ts | source
```

The tools that config declares are then on `PATH` in every shell, hash-verified and pinned,
materialized on first use — the same guarantees the project farm gives, applied to the tools a
person wants on their machine rather than in one repository.

**Why explicit `--config` and not an implicit `~/.config/datamitsu` layer.** The implicit
version is worse on three counts, and the flag it needs already exists
(`cmd/root.go:110`, `--config strings`, "Additional configuration file(s) to load and merge"):

1. **It changes config loading for every command.** A discovery layer folded into the chain is
   read by `lint`, `fix` and `check` too. `cache.calculateInvalidationKey`
   (`internal/cache/cache.go:479`) marshals the whole `Config` into the execution-cache key, so
   every project's lint cache would depend on the developer's personal config, and a CI run and
   a local run of the same commit would disagree. Explicit `--config` on `source` touches
   nothing else.
2. **It makes the test suite machine-dependent.** `internal/clitest`'s `BaseEnv`
   (`internal/clitest/run.go:100-131`) strips `DATAMITSU_*`, `CI`, `TERM` and `NO_COLOR`, but
   not `HOME` and not `XDG_CONFIG_HOME`. There are 65 golden files across 103 test functions.
   An implicit layer means every one of them starts reading whatever is in the developer's home
   directory, and needs an opt-out flag pinned in `BaseEnv` before it can merge at all.
   Explicit `--config` needs none of that.
3. **Implicit is the wrong default for something that executes JavaScript.** A config is
   evaluated goja code. Requiring the user to name the file is the cheapest possible trust
   boundary, and it is the same reasoning that keeps project activation explicit in the
   source-mode plan.

**Scope.** datamitsu can take over the tier of user-space CLI binaries that a dotfiles setup
typically installs by unverified download — `curl | sh` scripts, `eget`-style fetchers,
`go install`, npm globals. That tier is exactly where hash verification is usually absent, and
datamitsu's mandatory-hash policy closes it. It cannot take over system packages that need a
package manager or sudo (apt/brew formulae), GUI application installers, the package managers
and language toolchain installers themselves, or fonts. A system package manager does not go
away; it stops being the thing that downloads unverified binaries.

## What already exists

Verified first-hand at `20c75ba`. Most of the machinery this plan needs is already there,
which is why it is the smallest of the three.

- `cmd/root.go:110` — `--config strings` is already a persistent flag on every command, and
  `--no-auto-config` (`:108`) already suppresses git-root discovery.
- `cmd/config_loader.go` — explicitly named config paths already flow through the same
  `configSource` chain and the same `getConfig(input)` fold as discovered ones. **No new
  config source, no new discovery rule and no new env var is required by this plan.** Verify
  this before writing code; if it turns out an explicitly named config still requires a git
  root somewhere on the path, that is the one real code change and it should be recorded here.
- `internal/sourcefarm` (built in the source-mode plan) — the plan/manifest/materialize
  pipeline is root-agnostic apart from how the root identity is computed.
- The manifest already carries an `origin` field, added in the source-mode plan's Task 6 for
  exactly this purpose, so no format migration is needed.

## The trust boundary

The source-mode plan established: a shim exits 127 rather than baking a manifest for a
repository that was never explicitly activated, because baking means evaluating that
repository's JavaScript merely because a tool name was typed. This plan must not weaken it.

A farm created from an explicit `--config` is `origin: explicit-config`. Its shims revalidate
**their own recorded config chain** and never perform git-root discovery. So a shell activated
against a machine-level config that `cd`s into an untrusted clone keeps running the
machine-level tools; it does not start evaluating the clone's config. Getting into a
repository's toolchain still requires typing `datamitsu source` there.

## Development Approach

- **Testing approach**: Regular (code first, then tests).
- **CRITICAL: every task MUST include new/updated tests**, listed as separate checklist items,
  covering both success and error scenarios.
- **CRITICAL: all tests must pass before the next task.** Run `go test ./... -race` and
  `go test ./test/cli/ -count=2`.
- **CRITICAL: update this plan file when scope changes.**
- No new `DATAMITSU_*` variable should be needed. If one turns out to be, it goes through
  `internal/env` (envVar in `e.go`, getter in `env.go`, tests in `env_test.go`) and must be
  checked against `internal/clitest`'s `BaseEnv` stripping.
- Internal fingerprints use XXH3-128 via `internal/hashutil`.

## Testing Strategy

- **Unit tests**: required for every task, stdlib `testing` only.
- **Blackbox CLI tests**: the existing goldens must stay byte-identical. Any golden that shifts
  without a deliberate reason in this plan is a bug in the change.
- **Real-shell tests**: extend the tier from the source-mode plan's Task 13 with the
  outside-a-repository cases. This is the only tier that proves the actual use case.

## Progress Tracking

- Mark completed items `[x]` immediately. New tasks get ➕, blockers get ⚠️.

## Implementation Steps

### Task 1: Confirm the config path works without a git root

This task is investigation with a test as its deliverable. Do it first: its outcome decides
whether Task 2 is trivial or has real work in it.

- [x] establish, by reading `cmd/config_loader.go` and by running the built binary from a
      directory that is not inside any git repository, whether
      `datamitsu --config <path> exec` already resolves and lists that config's apps
- [x] record the answer in this file, with the code path that decides it
- [x] if a git root is required somewhere on that path, identify precisely where and make the
      minimum change so an explicitly named config does not need one — do not add a synthetic
      or fabricated root — **not required, see the finding below**
- [x] confirm `--config` composes with `--no-auto-config` as expected inside a repository
- [x] write a test asserting an explicitly named config resolves its apps with no git root
      present (build the fixture in `t.TempDir`, outside any repository)
- [x] write a test asserting a nonexistent `--config` path produces a clear error naming the
      path
- [x] write a test asserting `--config` inside a repository merges with the discovered config,
      and that `--no-auto-config` suppresses the discovered half
- [x] run `go test ./... -race` — must pass before Task 2

#### Finding: no git root is required, and no code change was needed

`datamitsu --config <path> exec` already resolves and lists that config's apps from a
directory outside every repository. Verified by running the built binary in a `mktemp -d`
directory where `git rev-parse --show-toplevel` fails: `config show` printed the config's
apps and exited 0. Task 2 is therefore the trivial branch of this task's outcome.

The code path that decides it is `loadConfigImpl` (`cmd/config_loader.go:159`):

1. The git-root lookup at `:184` is entered **only** to find the auto-discovered config, and
   only when `--no-auto-config` was not given.
2. When `facts.GetGitRoot` fails, `:187` makes the failure fatal **only** if
   `traverser.HasGitDir(cwdPath)` reports a `.git` directory — i.e. a repository that exists
   but whose git command is broken. Outside a repository there is no `.git`, so the failure is
   swallowed, `rootPath` stays at cwd and `autoConfigPath` stays empty.
3. `buildConfigSources` (`:380`) appends the `--config` paths at `:422` unconditionally, with
   no reference to `autoConfigPath`.
4. `facts.Collect` (`internal/facts/facts.go:109-132`) treats a missing git root the same way:
   `IsInGitRepo=false`, `gitRoot=""`, non-fatal without a `.git` directory. So `engine.New`
   builds a VM outside a repository too.

Two consequences recorded for later tasks: an explicit config's apps are appended **after**
the auto-discovered config when both are present (so the explicit half wins on key collision),
and a nonexistent `--config` path is a hard error naming the path
(`failed to load config from <path>: failed to read config file: …`), not a silent skip.

Tests live in `test/cli/config_explicit_test.go`. The outside-a-repository fixture needs a
directory git knows nothing about, which `clitest.NewProject` cannot provide — hence
`clitest.NewBareDir` (`internal/clitest/project.go`). It resolves symlinks like `NewProject`
and asserts no repository encloses the temp dir, skipping with the unverified property named
if `TMPDIR` happens to sit inside a checkout.

### Task 2: Farm identity without a git root

- [x] compute the farm root identity for an explicit-config activation from the **resolved,
      absolute, ordered** config file paths rather than a git root — XXH3-128 via
      `internal/hashutil`, an internal fingerprint never compared against an external value
- [x] place it under the cache root alongside project farms but in a distinct namespace, so a
      project farm and a config farm can never collide and both are visible to a future GC
- [x] resolve symlinks and relative paths before hashing, so
      `--config ./cfg.ts` and `--config /abs/cfg.ts` from the same directory produce one farm
      rather than two
- [x] set `origin: explicit-config` in the manifest and record the config paths in it
- [x] the watch set is the resolved config chain, with `{path, mtime_ns, size, exists}` compared
      with `!=`. There is no `.git/HEAD` to watch, and nothing should pretend there is
- [x] write a test asserting two different config paths produce different farm identities
- [x] write a test asserting the same config named relatively, absolutely, and through a
      symlink produces one identity
- [x] write a test asserting the identity is stable across calls and contains no `..` after
      `filepath.Clean`
- [x] write a test asserting a config farm and a project farm for the same directory do not
      collide
- [x] write a test asserting the manifest records `origin: explicit-config` and the config paths
- [x] run tests — must pass before Task 3

#### Implementation notes

- `internal/env/runtime.go` gained `ResolveConfigChain`, `ConfigFarmIdentity`,
  `GetConfigFarmBinPath` and `GetConfigFarmManifestPath`. The namespace is
  `{cache}/configs/{XXH3-128(resolved chain)}/`, a sibling of `{cache}/projects/`, so a
  collision between the two kinds of identity is impossible by construction rather than by
  hash luck. The lock file needs no new accessor: `materialize.go` derives it from the farm
  directory's parent.
- Chain order is significant and duplicates are preserved — the chain is a merge order, so two
  orderings of the same files resolve to different configs and are different farms.
- A path that cannot be `EvalSymlinks`'d (typically: it does not exist) falls back to its
  cleaned absolute form. Identity must be computable before the caller decides whether a
  missing config is an error.
- `internal/sourcefarm`: `OriginExplicitConfig`, `Manifest.ConfigPaths`, `ConfigWatchPaths`
  (the chain and nothing else) and `BuildConfigManifest`. `Manifest.Root` stays **empty** for
  such a farm rather than carrying a synthetic root — a fabricated root would be
  indistinguishable from a real one to Task 3's origin branch. `ConfigPaths` is deliberately
  outside the staleness key: every path in it is already a watch-set entry, and the key is
  recomputed from the manifest's own fields, so folding it in would compare it against itself.
- Existing `BuildManifest` callers are untouched, and `configPaths` is `omitempty`, so no
  git-root manifest changes byte-for-byte. `go test ./test/cli/ -count=2` is unchanged.

### Task 3: Shim resolution for explicit-config farms

The trust boundary lives here, in one branch.

- [x] teach the shim to branch on the manifest's `origin`: a `git-root` farm resolves from
      cwd's git root as the source-mode plan specifies; an `explicit-config` farm **never
      performs git discovery** and revalidates only its recorded config chain
- [x] the shim locates an explicit-config manifest through the farm path exported by
      activation, since there is no git root to derive it from. Read it from the environment,
      and fail loudly with an actionable message if it is absent or points nowhere
- [x] precedence when both are active: a project farm sits ahead of a config farm on `PATH`, so
      the project's pin wins by ordinary `PATH` order. Do not add resolution logic for this —
      `PATH` order is the mechanism, and it is the one users can inspect
- [x] a stale explicit-config farm re-bakes exactly as a project farm does, using the recorded
      config paths
- [x] write a test asserting an explicit-config shim performs no git-root discovery even when
      cwd is deep inside a repository that has its own config — assert on the absence of the
      call, not merely on the outcome
- [x] write a test asserting an explicit-config shim re-bakes when its config file changes
- [x] write a test asserting a missing or dangling farm environment variable produces an
      actionable error rather than a silent fallback
- [x] write a test asserting a name absent from an explicit-config manifest exits 127 and does
      not search the rest of `PATH`
- [x] run `go test ./... -race` — must pass before Task 4

#### Implementation notes

- `Dispatch` now takes the origin branch before it touches the working directory:
  `explicitConfigFarm` runs first, and only when it declines does the git-root path run at all.
  The shared tail — freshness check, rebake, lazy install, exec — moved into `runManifest`, so
  the two origins differ only in _which manifest is found_, never in what happens afterwards.
- The manifest is located from the farm directory the invocation arrived through, which
  `computeThroughFarm` now records in `d.invokedFarmDir`. That directory is the answer PATH
  order already gave, which is exactly the precedence rule the plan asks for — a project farm
  ahead of a config farm on `PATH` means the invocation arrives through the project farm and
  the config farm is never consulted. `DATAMITSU_FARM` is the second source, covering the case
  where a `DATAMITSU_CACHE_DIR`/`HOME` that resolves differently in this process makes the farm
  directory recognizable only through the exported variable. No new env var was needed.
- A directory textually inside `{cache}/configs/*/bin` with no readable manifest is exit 127
  naming the farm and `--config`, never a fall-through: falling through would send a
  machine-level tool name into git discovery and, on a rebake, evaluate the config of whatever
  repository the shell happened to be in.
- `isFarmDir` now recognizes both namespaces (`env.ProjectFarmsDirName`,
  `env.ConfigFarmsDirName`, exported so the two packages cannot spell them differently), so a
  config farm is stripped from a spawned child's `PATH` and refused as a spawn target exactly
  like a project farm.
- The rebake spawn for an explicit-config farm always carries `--no-auto-config` on top of the
  recorded `ConfigArgs` (or `--config <path>` synthesized from `ConfigPaths` when none were
  recorded), and runs in the first config path's own directory rather than inheriting the
  tool's cwd. Without the flag a rebake fired from inside a repository would merge that
  repository's config into a machine-level farm; without the fixed directory the farm's
  contents would depend on where the user was standing.
- User-facing messages go through `farmLabel`/`refreshHint`, so a rootless farm is named by its
  config chain and told to run `source refresh --config <path> --force` instead of "in that
  repository". A git-root farm's messages are byte-identical to before — `go test ./test/cli/
-count=2` is unchanged.

### Task 4: Wire `source` to accept the explicit path

- [x] `datamitsu source <shell> --config <path>` bakes and activates the config farm; the
      error added in the source-mode plan's Task 9 now only fires when neither a discovered
      config nor `--config` is available
- [x] `source status` and `source refresh` work against a config farm exactly as they do
      against a project farm, and `status` reports which origin the farm has
- [x] activation exports the farm path and the config paths, not a git root that does not exist
- [x] the deny-list, shell-app exclusion and shadow reporting from the source-mode plan apply
      unchanged. Shadow reporting matters **more** here: a machine-level farm is first on
      `PATH` in every shell, so the list of system binaries it takes over is the thing the user
      most needs to be able to read
- [x] write a test asserting `source <shell> --config <path>` outside a repository emits valid
      shell code and exits 0
- [x] write a test asserting it downloads nothing, under `DATAMITSU_OFFLINE=1`
- [x] write a test asserting `source status` reports `origin: explicit-config` and the config
      paths
- [x] write a test asserting a config declaring a shell app or a deny-listed name still
      excludes it, with a reason
- [x] run tests — must pass before Task 5

#### Implementation notes

- The branch is taken once, in `resolveSourceTarget` (`cmd/source.go`), which every `source`
  leaf now calls first. A `sourceTarget` carries the origin, the identity (a git root **or** a
  resolved chain) and the two paths derived from it; everything downstream — freshness, bake,
  activation, status, refresh — is shared. `--config` present means an explicit-config target,
  in a repository or out of one.
- **An explicit-config bake never discovers.** `resolveSourcePlanFor` forces `noAutoConfig`,
  and `sourceConfigArgs` records `--no-auto-config` in the manifest, so the bake and every
  rebake the shim spawns resolve the same chain. Without it, `source fish --config ~/tools.ts`
  run from inside a repository would merge that repository's config into a machine-level farm —
  and the first rebake (which always carries the flag, per Task 3) would silently drop
  everything it contributed. That merge is what the pre-Task-4 `--config` handling did; it is
  now a farm of its own, keyed by the chain rather than by the root it happened to run in.
- `--before-config` files are part of the identity, not a modifier of it: the chain is
  before-configs then configs, hashed as one. Two invocations differing only in them are two
  farms rather than one farm baked twice from different inputs.
- Recorded `ConfigArgs` are rebuilt from the _resolved_ chain, so the three spellings that
  already collapse to one farm also record one fragment. Recording the typed spelling would
  make an absolute and a relative invocation rebake each other forever through
  `manifestChainMatches`.
- **The manifest fast path is now open to config farms** (`sourceManifestDecides`). It is safe
  by construction rather than by permission: such a farm's manifest does not live at a git
  root's path but at the path the chain itself hashes to, so the only manifest an invocation
  can find is one baked from those files. Activation lives in a shell rc file, so the
  alternative is a full config resolution in every shell and every tmux pane. `loadSourcePlan`
  and `manifestStatus` additionally compare `Origin`, so neither kind of farm can ever be
  served to the other.
- Activation exports `DATAMITSU_FARM` + a new `DATAMITSU_FARM_CONFIG` for a rootless farm, and
  deliberately no `DATAMITSU_ROOT` — an empty one reads as "the repository could not be
  determined". ➕ This is the one new `DATAMITSU_*` variable the plan said it did not expect to
  need; it goes through `internal/env` (`e.go`, `env.go`, `env_test.go`) and is in
  `environExcluded`, like the other two activation markers, so it cannot make every command in
  an activated shell report the manifest stale. `clitest.BaseEnv` already strips every
  `DATAMITSU_*`, so no harness change was needed.
- `source status` gained `origin` (never omitted) and `configPaths` (`omitempty`); the human
  report prints an `origin:` line and, for a rootless farm, `config:` lines in place of `root:`.
  There is no golden for the human report, so nothing shifted. Every other user-facing message
  goes through `sourceTarget.label()`, so a rootless farm is named by its chain.
- Only one golden changed: `source_help`, which now documents `--config`. A git-root
  activation's stdout, stderr and manifest bytes are unchanged — `go test ./test/cli/ -count=2`
  and `go test ./test/shell/` both pass.
- Tests: `cmd/source_config_test.go` (target resolution, recorded args, watch set, activation
  vars, origin-crossed manifests, status shape, labels) and `test/cli/source_config_test.go`
  (activation outside a repository in bash and fish, downloads-nothing offline, status origin
  and chain, exclusions with reasons, no merge of a surrounding repository, refresh no-op and
  rebake, one identity per chain).

### Task 5: Blackbox tests and goldens

- [x] add goldens for `source <shell> --config <path>` with the normalizer masking the temp
      config directory and the cache root
- [x] pin `PATH` explicitly via `RunOptions.Env` in every case, as the source-mode plan
      requires — shadow detection reads `PATH` and goldens are machine-dependent otherwise
- [x] add cases to `detCases` so `TestGoldenSuiteDeterministic` covers them
- [x] no new leaf command should be needed: `--config` is an existing persistent flag, so
      `testedLeafCommands` (`test/cli/completeness_test.go:22`) needs no new entry. Confirm
      this rather than assuming it, and regenerate the `source` help goldens if the help text
      changed
- [x] write a test asserting a config whose `getConfig()` calls `console.log` produces stdout
      with zero extra bytes — the config-injection guard, now applied to a config the user
      names explicitly
- [x] write a test asserting an unreadable or malformed `--config` path exits non-zero with
      nothing on stdout
- [x] `go test ./test/cli/ -count=2` passes byte-identically
- [x] run `go test ./... -race` — must pass before Task 6

#### Implementation notes

- Five new goldens: `source_config_{bash,zsh,fish}` and `source_config_status{,_json}`. They
  use `emptyMachineConfigJS` — the machine-level counterpart of `sourceAutoConfigJS`, declaring
  no apps — so the frozen bytes depend on nothing but the farm path and the chain that names it.
  `machineConfigJS`'s exclusions belong to `TestSourceConfigExclusionsStillApply`, which asserts
  on their reasons rather than on their formatting.
- `sourceConfigNormalizer` is `sourceNormalizer`'s counterpart: the bare directory holding the
  config → `<TMP>`, the isolated cache → `<CACHE>`, and `configFarmHashRE`
  (`configs/[0-9a-f]{32}`) → `configs/<CHAIN>`. Masking the cache path alone is not enough —
  the chain fingerprint hashes a temp directory that differs on every run.
- `detSourceConfig` joins `detCases` for bash, fish and both status forms. It is the case that
  would catch a chain resolution that is not stable run-to-run, which a per-git-root case cannot.
- **`testedLeafCommands` needed no entry, confirmed rather than assumed:** `TestContractCompletenessGate`
  discovers leaves from the `--help` tree, and `--config` is a persistent flag, not a command,
  so the discovered leaf set is unchanged. The `source` help goldens were already regenerated in
  Task 4 (`source_help` documents `--config` and the outside-a-repository example) and did not
  shift again here.
- The injection guard is `TestSourceExplicitConfigCannotInjectIntoStdout` — named apart from
  `TestSourceConfigCannotInjectIntoStdout`, the discovered-config case in `source_test.go`.
  Naming a config is the trust boundary for _evaluating_ it, not permission for it to write to
  stdout: the output is `eval`'d in a shell rc file.
- `TestSourceConfigUnusableChainFails` covers both ways a chain is unusable — the path is absent,
  and the file is present but not valid JavaScript. It asserts empty stdout in both, since a
  diagnostic that leaked there would be run as shell code by the shell that failed to activate.
  Unreadability is exercised as a missing path rather than `chmod 0000`, which is a no-op for root.
- No existing golden shifted: `go test ./test/cli/ -count=2` and `go test ./... -race` both pass.

### Task 6: Real-shell tests

- [x] fixture: a config outside any repository declaring a stub tool at version A, and a
      project repository declaring the same tool at version B
- [x] assert activating the config farm outside any repository runs version A
- [x] assert `cd`ing into the project **without** activating it still runs version A, and that
      the project's config is never evaluated — the trust boundary, proven rather than assumed
- [x] assert activating the project as well runs version B, and that removing the project farm
      from `PATH` returns to version A
- [x] assert branch switching inside the project still swaps versions on a single command line
      with the config farm active underneath
- [x] assert a tool declared only in the machine-level config stays reachable from inside the
      project
- [x] assert both bash and fish, skipping cleanly when a shell is unavailable
- [x] run the tier — must pass before Task 7

#### Implementation notes

- The fixture grew a machine-level tier alongside the two-branch repository:
  `fixture.machineConfig()` writes a config into a `clitest.NewBareDir` (a temp directory with
  no repository above it, which skips cleanly when `TMPDIR` sits inside a checkout) declaring
  `stub-tool` at `9.9.9` — a version no branch uses, so which farm answered is readable
  straight off the output — plus `stub-only`, which exists in no repository at all. Both are
  served from the same loopback host as the branch stubs, hash-verified for real.
- `stubScript`, `configJS` and `assertRan` were generalized to take a name (and a list of apps)
  rather than closing over `toolName` and one branch. `runRawIn`/`datamitsuIn` add cwd control,
  which the machine-level cases need in both directions: a cwd outside every repository, and a
  cwd inside one that was never activated.
- **The trust boundary is proved by a poisoned config, not by an absence.** The repository's
  config is replaced with one that throws on evaluation, and the test first asserts that
  `datamitsu source bash` in that repository does fail with the marker — otherwise the case
  would pass vacuously against a config that happens to be harmless. Only then does it run the
  tool from the repository root under a machine-level activation and require version `9.9.9`,
  no marker on either stream, and no farm under `{cache}/cache/projects`.
- `TestProjectFarmWinsOverMachineFarm` asserts the mechanism, not just the outcome: it dumps
  `PATH`, requires the project farm to appear ahead of the config farm, then drops that one
  directory from `PATH` in the shell — no datamitsu command, no re-activation — and requires the
  machine-level version back. A precedence table would not show up in that dump at all.
- `configShells` is bash and fish. zsh shares bash's renderer and is already covered by the
  project cases in `source_test.go`; repeating every machine-level case in it would triple the
  tier's runtime for one already-proved property.
- `go test ./test/shell/`, `go test ./... -race` and `go test ./test/cli/ -count=2` all pass,
  and `golangci-lint run ./test/shell/` is clean.

### Task 7: Verify acceptance criteria

- [x] `datamitsu source fish --config <path>` in a shell rc file activates a working farm from
      a directory that is not a git repository, and downloads nothing
- [x] `datamitsu source` with no discovered config and no `--config` still fails loudly with a
      message naming `--config`
- [x] an explicit-config shim never evaluates a project's config
- [x] `lint`, `fix` and `check` are entirely unaffected: the execution-cache invalidation key is
      byte-identical with and without a machine-level config present on disk
- [x] no new implicit discovery path was added anywhere
- [x] `go test ./... -race` passes
- [x] `go test ./test/cli/ -count=2` passes
- [x] `pnpm dm check` passes
- [x] coverage meets the project standard via `pnpm test:coverage:all`

#### Verification notes

- **Activation outside a repository, downloading nothing** — `test/cli/source_config_test.go`
  (`TestSourceConfigFishActivatesOutsideARepository`, `TestSourceConfigActivatesOutsideARepository`,
  `TestSourceConfigDownloadsNothing`, all under `DATAMITSU_OFFLINE=1`) and the real-shell tier
  (`test/shell/source_config_test.go`), which activates in a live bash and fish and runs the
  resulting tool.
- **The loud failure still names `--config`** — `TestSourceOutsideAGitRepository` and
  `TestSourceWithoutAConfig` (`test/cli/source_test.go`) assert non-zero exit, empty stdout and
  `--config` in stderr. Task 4 widened when the error fires, not what it says.
- **The shim never evaluates a project's config** — `internal/shim/config_farm_test.go` asserts on
  the absence of the git-discovery call, and the real-shell tier proves it end to end against a
  repository whose config throws on evaluation, requiring the machine-level version and no marker
  on either stream.
- **`lint`/`fix`/`check` unaffected** — new file `test/cli/machine_config_isolation_test.go`. It
  plants the same config in every location an implicit layer would plausibly read
  (`$XDG_CONFIG_HOME/datamitsu/datamitsu.config.{js,ts,mjs}`, `$HOME/.datamitsu/`, `$HOME` itself,
  and the directory above the project), with `$HOME`/`$XDG_CONFIG_HOME` pinned at the fixture —
  `clitest.BaseEnv` strips every `DATAMITSU_*` var but deliberately not those two, so a test that
  cares must pin them itself. `config show` is `json.MarshalIndent` of the same `config.Config`
  that `cache.calculateInvalidationKey` marshals, so byte-identical output there is a
  byte-identical key; the `--explain=json` plans for all three commands are compared as well.
  `TestMachineConfigReachesTheChainOnlyWhenNamed` is the positive control — naming one of the
  planted files with `--config` does add its app, so the identical-bytes assertions are not
  passing against an inert fixture.
- **No new implicit discovery** — `git diff source-mode..HEAD -- cmd/config_loader.go` is empty;
  `discoverAutoConfig` is still called from exactly one place with a git root as its argument.
- **Results:** `go test ./... -race`, `go test ./test/cli/ -count=2` and `pnpm dm check` all pass.
  `pnpm test:coverage:all` reports 81.9% merged (`cmd` 75.0% read from the merged profile,
  `internal/shim` 88.9%, `internal/sourcefarm` 85.7%, `internal/env` 93.7%). Its first run failed
  on a transient `coverage meta-data emit failed: … no such file or directory` while writing into
  the shared `GOCOVERDIR`, which desynchronized a determinism case's two runs; it passed on a
  clean rerun and is a covdata-emit flake, not a test defect.

### Task 8: [Final] Documentation

- [x] a `website/docs/how-to/` page for the machine-level toolchain: writing a config outside
      any project, activating it from a shell rc file, and how it layers with project
      activation
- [x] document the trust boundary explicitly — an explicit-config farm never evaluates project
      configs, and project activation is deliberate. State that this is a security property,
      not an omission
- [x] document the migration path from unverified download scripts honestly, including what
      stays with the system package manager and why. Keep it generic: the guide belongs to
      whoever reads it, and it should not describe any particular person's machine
- [x] document that hash verification is the point — every tool datamitsu takes over gains a
      mandatory SHA-256, which `curl | sh` installers generally do not have
- [x] document `--config` in the `## source` section of
      `website/docs/reference/cli-commands.md`, including the outside-a-repository invocation
- [x] register new pages in `website/sidebars.ts`
- [x] run `task gen:llms-docs` and commit `internal/llmsdocs/embed` — the `llms-docs-drift` job
      fails on any diff
- [x] `pnpm dm check --file-scoped` passes

#### Implementation notes

- `website/docs/how-to/machine-level-toolchain.md` is the new page: writing the config,
  activating it from bash/zsh/fish rc files, the two farm namespaces
  (`{cache}/cache/configs/{hash}` vs `.../projects/{hash}`), the `PATH`-order layering rule, the
  trust boundary, `status`/`refresh --config`, the hash argument, and the migration table.
- The migration section is deliberately a table of _kinds_ of tool rather than a list of tools:
  single-binary CLIs, npm/uv globals and `go install`-ed tools move; package managers, GUI
  installers, fonts and anything needing `sudo` stay. It describes no particular machine.
- The example config's `hash` fields are `<sha256 of that file>` placeholders with the format
  requirement stated next to them, and the page points at `devtools pull-github` as the way to
  fill them in. Inventing plausible-looking SHA-256 values for a real release URL would be a
  worse example than an obviously-unfinished one.
- The `## source` reference gained a `### source --config: a machine-level toolchain` subsection
  with the origin comparison table, and its pre-existing claim that "any of `--config`,
  `--before-config` or `--no-auto-config` bypasses the manifest fast path" was corrected —
  Task 4 opened the fast path to config farms, so only the other two flags still bypass it.
  `source status`/`refresh` document their `--config` form, and `DATAMITSU_FARM_CONFIG` joins the
  environment-variable table.
- `task gen:llms-docs` had to run **twice**: the first harvest ran before `dm check`'s prettier
  reformatted the new pages, so its snapshot was already stale (`pageSetHash` changed on the
  second run). Format first, then harvest.
- `go test ./test/cli/` and `go test ./internal/llmsdocs/` pass; `pnpm dm check --file-scoped` is
  clean with the changes staged (it finds nothing when they are not).

## Technical Details

### The two farm origins

```text
origin: git-root           root = <git root>
                           watch = config chain + .git/HEAD
                           shim  = re-resolves from cwd's git root  (branch switching)

origin: explicit-config    root = XXH3-128(resolved absolute config paths)
                           watch = config chain
                           shim  = revalidates its own chain, never walks up for .git
```

### The resulting PATH

```text
{cache}/<project farm>/bin     ← added by `datamitsu source` inside a repository
{cache}/<config farm>/bin      ← added by `datamitsu source --config <path>`
…the rest of the user's PATH…
```

Layering is `PATH` order and nothing else. There is no merge, no precedence table and no
resolution logic to get wrong — and the user can see the whole thing with `echo $PATH`.

### What this plan deliberately does not add

No `~/.config/datamitsu` auto-discovery, no `GetUserConfigPath()`, no new config source, no
`--no-user-config` and no `internal/clitest` `BaseEnv` change. An earlier draft of this plan
had all five. They existed only to make an implicit discovery layer safe; naming the config
removes the need for every one of them.

➕ One item on that list did not survive Task 4: `DATAMITSU_FARM_CONFIG`, the activation
marker naming the chain a rootless farm was baked from. It is the counterpart of the existing
`DATAMITSU_ROOT`, which such a farm has nothing truthful to put in. It is informational — no
tool resolves through it — and it is in `environExcluded`, so it does not enter any
fingerprint. It changes no discovery rule, which is what the list is actually about.

## Post-Completion

**Manual verification:**

- Run for a week with the activation line in a real shell rc file before recommending it.
  Shell-startup regressions are noticed immediately and forgiven slowly.
- Verify on Linux as well as macOS.
- Migrate a few tools first, leave the previously installed binaries in place, and use
  `datamitsu source status` to confirm what the farm actually shadows before deleting anything.

**Deliberately deferred:**

- **An approval gate** (`datamitsu allow` / `deny`, SHA-256 over config-chain file contents —
  a cryptographic hash, because it is compared against content from an untrusted source, plus
  a per-user approval store). This is the prerequisite for ever letting an activated shell
  resolve a repository's config automatically on `cd`. Until it exists, explicit activation is
  the boundary.
- **A convention path** such as a documented default location that `--config` could shorten to.
  Tempting, and it is implicit discovery wearing a smaller hat. Revisit only with the gate
  above in place.
- **A machine-wide farm shared across projects**, holding the union of every name any project
  ever declared and sitting first on `PATH` in every shell. One repository visited once would
  permanently change what a common command does in the home directory. The per-root farms
  avoid this by construction.
- **Store GC**, which becomes more pressing once a second farm holds references. It must be
  farm-aware or it will delete binaries out from under open shells.
