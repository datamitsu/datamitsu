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

- [ ] compute the farm root identity for an explicit-config activation from the **resolved,
      absolute, ordered** config file paths rather than a git root — XXH3-128 via
      `internal/hashutil`, an internal fingerprint never compared against an external value
- [ ] place it under the cache root alongside project farms but in a distinct namespace, so a
      project farm and a config farm can never collide and both are visible to a future GC
- [ ] resolve symlinks and relative paths before hashing, so
      `--config ./cfg.ts` and `--config /abs/cfg.ts` from the same directory produce one farm
      rather than two
- [ ] set `origin: explicit-config` in the manifest and record the config paths in it
- [ ] the watch set is the resolved config chain, with `{path, mtime_ns, size, exists}` compared
      with `!=`. There is no `.git/HEAD` to watch, and nothing should pretend there is
- [ ] write a test asserting two different config paths produce different farm identities
- [ ] write a test asserting the same config named relatively, absolutely, and through a
      symlink produces one identity
- [ ] write a test asserting the identity is stable across calls and contains no `..` after
      `filepath.Clean`
- [ ] write a test asserting a config farm and a project farm for the same directory do not
      collide
- [ ] write a test asserting the manifest records `origin: explicit-config` and the config paths
- [ ] run tests — must pass before Task 3

### Task 3: Shim resolution for explicit-config farms

The trust boundary lives here, in one branch.

- [ ] teach the shim to branch on the manifest's `origin`: a `git-root` farm resolves from
      cwd's git root as the source-mode plan specifies; an `explicit-config` farm **never
      performs git discovery** and revalidates only its recorded config chain
- [ ] the shim locates an explicit-config manifest through the farm path exported by
      activation, since there is no git root to derive it from. Read it from the environment,
      and fail loudly with an actionable message if it is absent or points nowhere
- [ ] precedence when both are active: a project farm sits ahead of a config farm on `PATH`, so
      the project's pin wins by ordinary `PATH` order. Do not add resolution logic for this —
      `PATH` order is the mechanism, and it is the one users can inspect
- [ ] a stale explicit-config farm re-bakes exactly as a project farm does, using the recorded
      config paths
- [ ] write a test asserting an explicit-config shim performs no git-root discovery even when
      cwd is deep inside a repository that has its own config — assert on the absence of the
      call, not merely on the outcome
- [ ] write a test asserting an explicit-config shim re-bakes when its config file changes
- [ ] write a test asserting a missing or dangling farm environment variable produces an
      actionable error rather than a silent fallback
- [ ] write a test asserting a name absent from an explicit-config manifest exits 127 and does
      not search the rest of `PATH`
- [ ] run `go test ./... -race` — must pass before Task 4

### Task 4: Wire `source` to accept the explicit path

- [ ] `datamitsu source <shell> --config <path>` bakes and activates the config farm; the
      error added in the source-mode plan's Task 9 now only fires when neither a discovered
      config nor `--config` is available
- [ ] `source status` and `source refresh` work against a config farm exactly as they do
      against a project farm, and `status` reports which origin the farm has
- [ ] activation exports the farm path and the config paths, not a git root that does not exist
- [ ] the deny-list, shell-app exclusion and shadow reporting from the source-mode plan apply
      unchanged. Shadow reporting matters **more** here: a machine-level farm is first on
      `PATH` in every shell, so the list of system binaries it takes over is the thing the user
      most needs to be able to read
- [ ] write a test asserting `source <shell> --config <path>` outside a repository emits valid
      shell code and exits 0
- [ ] write a test asserting it downloads nothing, under `DATAMITSU_OFFLINE=1`
- [ ] write a test asserting `source status` reports `origin: explicit-config` and the config
      paths
- [ ] write a test asserting a config declaring a shell app or a deny-listed name still
      excludes it, with a reason
- [ ] run tests — must pass before Task 5

### Task 5: Blackbox tests and goldens

- [ ] add goldens for `source <shell> --config <path>` with the normalizer masking the temp
      config directory and the cache root
- [ ] pin `PATH` explicitly via `RunOptions.Env` in every case, as the source-mode plan
      requires — shadow detection reads `PATH` and goldens are machine-dependent otherwise
- [ ] add cases to `detCases` so `TestGoldenSuiteDeterministic` covers them
- [ ] no new leaf command should be needed: `--config` is an existing persistent flag, so
      `testedLeafCommands` (`test/cli/completeness_test.go:22`) needs no new entry. Confirm
      this rather than assuming it, and regenerate the `source` help goldens if the help text
      changed
- [ ] write a test asserting a config whose `getConfig()` calls `console.log` produces stdout
      with zero extra bytes — the config-injection guard, now applied to a config the user
      names explicitly
- [ ] write a test asserting an unreadable or malformed `--config` path exits non-zero with
      nothing on stdout
- [ ] `go test ./test/cli/ -count=2` passes byte-identically
- [ ] run `go test ./... -race` — must pass before Task 6

### Task 6: Real-shell tests

- [ ] fixture: a config outside any repository declaring a stub tool at version A, and a
      project repository declaring the same tool at version B
- [ ] assert activating the config farm outside any repository runs version A
- [ ] assert `cd`ing into the project **without** activating it still runs version A, and that
      the project's config is never evaluated — the trust boundary, proven rather than assumed
- [ ] assert activating the project as well runs version B, and that removing the project farm
      from `PATH` returns to version A
- [ ] assert branch switching inside the project still swaps versions on a single command line
      with the config farm active underneath
- [ ] assert a tool declared only in the machine-level config stays reachable from inside the
      project
- [ ] assert both bash and fish, skipping cleanly when a shell is unavailable
- [ ] run the tier — must pass before Task 7

### Task 7: Verify acceptance criteria

- [ ] `datamitsu source fish --config <path>` in a shell rc file activates a working farm from
      a directory that is not a git repository, and downloads nothing
- [ ] `datamitsu source` with no discovered config and no `--config` still fails loudly with a
      message naming `--config`
- [ ] an explicit-config shim never evaluates a project's config
- [ ] `lint`, `fix` and `check` are entirely unaffected: the execution-cache invalidation key is
      byte-identical with and without a machine-level config present on disk
- [ ] no new implicit discovery path was added anywhere
- [ ] `go test ./... -race` passes
- [ ] `go test ./test/cli/ -count=2` passes
- [ ] `pnpm dm check` passes
- [ ] coverage meets the project standard via `pnpm test:coverage:all`

### Task 8: [Final] Documentation

- [ ] a `website/docs/how-to/` page for the machine-level toolchain: writing a config outside
      any project, activating it from a shell rc file, and how it layers with project
      activation
- [ ] document the trust boundary explicitly — an explicit-config farm never evaluates project
      configs, and project activation is deliberate. State that this is a security property,
      not an omission
- [ ] document the migration path from unverified download scripts honestly, including what
      stays with the system package manager and why. Keep it generic: the guide belongs to
      whoever reads it, and it should not describe any particular person's machine
- [ ] document that hash verification is the point — every tool datamitsu takes over gains a
      mandatory SHA-256, which `curl | sh` installers generally do not have
- [ ] document `--config` in the `## source` section of
      `website/docs/reference/cli-commands.md`, including the outside-a-repository invocation
- [ ] register new pages in `website/sidebars.ts`
- [ ] run `task gen:llms-docs` and commit `internal/llmsdocs/embed` — the `llms-docs-drift` job
      fails on any diff
- [ ] `pnpm dm check --file-scoped` passes

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
`--no-user-config`, no `internal/clitest` `BaseEnv` change, and no new environment variable.
An earlier draft of this plan had all six. They existed only to make an implicit discovery
layer safe; naming the config removes the need for every one of them.

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
