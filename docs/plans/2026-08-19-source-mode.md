# Plan: Source mode — activate the project toolchain in a shell

**Status:** implemented (2026-08-20). All 15 tasks are done; the branch is not merged, per
the branch note below. Two items remain open as **owner decisions**, not as work: the D1
narrowing recorded under Task 13 (every entry is a shim; the symlink fast path is
implemented but never selected), and the measured ~10–12 ms steady-state cost, which the
Task 14 acceptance row flags as not met as originally written.
**Date:** 2026-08-19.
**Depends on:** `docs/plans/completed/2026-08-19-cli-startup-cost.md` — landed. Every latency
number in this plan assumes it.
**Related:** `internal/binmanager`, `internal/runtimemanager`, `internal/env`,
`internal/config/validate.go`, `cmd/exec.go`, `main.go`, `test/cli`.

**Branch:** `source-mode`, branched from `cli-startup-cost` (**not** from `main` — this plan
needs the startup work in its history). Second of three stacked plans. Do all work on this
branch and do not merge as part of this plan.
Review base ref: `cli-startup-cost` — pass `--base-ref=cli-startup-cost`, or the review pass
re-reviews the whole previous plan's diff as if it were new work.

## Overview

Today the only way to run a datamitsu-managed tool is `datamitsu exec <name> -- <args>`.
This plan makes the project's declared toolchain available as ordinary commands on `PATH`
for the current shell session:

```bash
eval "$(datamitsu source bash)"      # bash / zsh
datamitsu source fish | source       # fish
terragrunt plan                      # the version this repo pins, not the system one
```

**The four properties that define the feature.** They are requirements, not aspirations, and
every design decision below is traceable to one of them:

1. **Transparent versions.** After activation, `terragrunt` is the version this repository
   pins. Not the one in `/usr/local/bin`.
2. **Transparent branch switching.** `git checkout v2` inside an already-activated shell must
   silently change which binary runs — including on a single line
   (`git checkout v2 && terragrunt plan`), inside `make`, inside a git hook, and inside
   `tmux new-window 'terragrunt plan'`. No re-activation, no prompt hook.
3. **Lazy materialization.** Activation downloads nothing. A tool arrives on first real use.
4. **Speed.** Activation is cheap enough to sit in a shell rc file; a steady-state tool
   invocation adds ~0 ms for the common case.

Property 2 is what rules out every prompt-hook design (mise `activate`, direnv). A hook fires
when a prompt renders; `git checkout v2 && terragrunt plan` renders no prompt between the two
commands, so the hook never runs and the **old binary executes silently with plausible
output**. Property 3 is what rules out a pure symlink farm: you cannot symlink to a file that
has not been downloaded, and a dangling symlink is `ENOENT`, not a trigger.

## Decisions (closed by the owner, do not re-litigate)

| #   | Decision                                            | Chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| --- | --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1  | Hot-path shape                                      | ~~**Hybrid.** Real symlink into the content-addressed store for _installed_ `binary`/`go` apps (0 ms). Shim for everything else.~~ **Superseded during Task 13: every entry is a shim.** A symlink has nowhere to put the freshness check, so after `git checkout v2` it keeps resolving to the version the previous branch pinned — silently, exiting 0. `StrategySymlink` remains implemented and round-trips through the manifest, but `strategyFor` never selects it. See the ⚠️ note below; awaiting owner confirmation. |
| D2  | Startup optimizations                               | **Separate plan, landed first** (`2026-08-19-cli-startup-cost.md`).                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| D3  | Activation outside a git repository                 | **Deferred to its own plan.** This plan is project-scoped only: `source` requires a discovered config and fails loudly without one.                                                                                                                                                                                                                                                                                                                                                                                           |
| D4  | Name declared but unresolvable for the current root | **Fail loudly, exit 127.** Never fall through to a system binary. Report shadowing explicitly.                                                                                                                                                                                                                                                                                                                                                                                                                                |

D1 is the decision that makes the numbers work: `terragrunt`, `tofu`, `sops` and `kubectl`
are all `binary` apps, so the tools that motivated the feature take the zero-overhead path,
and the ~10 ms shim tax is confined to cases that structurally require a process (argv
prefixes, env overlays, lazy install).

⚠️ **D1 was narrowed during Task 13.** The real-shell tier proved that a symlink entry cannot
satisfy property 2: nothing of datamitsu's runs when the kernel follows a symlink, so an
installed `binary` app keeps running the previous branch's version after a checkout — silently.
Every entry is now a shim; the symlink machinery remains implemented and tested behind a single
branch in `strategyFor`. See the Task 13 notes. The owner should confirm the narrowing.

D4 matters more than it looks. Without it the feature's failure mode is a _silent wrong
binary_: a name that is declared but missing from the farm falls through `PATH` to a stale
`/usr/local/bin/terragrunt`, exits 0, and prints plausible output. That is strictly worse
than not shipping the feature. **Consequence: the farm must contain an entry for every
declared name, not just every installed one.**

## Context (from discovery)

Everything here was verified first-hand at `20c75ba`. It is recorded so it is not re-derived.

### The farm cannot live in `.datamitsu/`

`internal/managedconfig/links.go:236-272` destroys and recreates `.datamitsu/` wholesale:
it stages a temp dir, renames the existing directory to `.datamitsu.bak`, renames temp into
place, then `os.RemoveAll`s the backup. This runs on `datamitsu init` (`cmd/init.go:322-337`)
**and** on `datamitsu exec` of any lazy link-app (`cmd/exec.go:147-155`). A farm there would
be deleted out from under a live `PATH`.

### Shell apps are a fork bomb, not an edge case

A `shell` app resolves a bare command name through the **inherited** `PATH`
(`internal/binmanager/binmanager.go:476-481` → `exec.CommandContext` at `:830`, which uses
`exec.LookPath` against the process `PATH`; `mergeExecEnv` only rewrites `cmd.Env`, which
`LookPath` never consults). The shipped default config declares `echo` as a shell app
(`config/src/apps.ts:13-18`). There is no `syscall.Exec` anywhere in the repo, so every
recursion level is a surviving process. Shell apps must be excluded by a mechanical kind
rule, not by config policy.

### App names are never validated

`doValidateApps` (`internal/config/validate.go:29-260`) validates URLs, SHA-256 hashes, libc
keys, `binPath`, and `go.packageName` (with an explicit `..` check at `:107`) — and never the
app name itself. An app named `../../../.ssh` already escapes the store today via
`filepath.Join` at `binmanager.go:940`. Under source mode the name additionally becomes a
filename and a dispatch key.

### Resolution installs as a side effect

`runtimemanager.GetCommandInfo` (`internal/runtimemanager/runtimemanager.go:289-320`) calls
`InstallUVApp`/`installNodeApp`/`InstallJVMApp`/`InstallGoApp` _before_ returning. So "where
is `jq`?" can hit the network. The install-free halves partly exist already:
`GetUVCommandInfo` and `GetGoCommandInfo` take no `ctx` and perform no install; only
`getNodeCommandInfo` (via `installNode` at `internal/runtimemanager/node.go:299`) and
`GetJVMCommandInfo` still carry one.

### Not every app reduces to an executable

A tool is `CommandInfo{Type, Command, Args, Env}`
(`internal/binmanager/binmanager.go:741-746`). Only `binary` and `go` reduce to a bare
executable. `jvm` apps are `java -jar x.jar` — argv a symlink cannot carry. `node` apps need
`Env` with `PATH` prepended to the managed node bin dir plus `npm_config_*`
(`internal/runtimemanager/node.go:305-313`); `uv` apps need `UV_CACHE_DIR` /
`UV_PYTHON_INSTALL_DIR`. `mergeAppEnv` means _any_ kind can additionally carry `app.Env`.

### Symlinking a pnpm `.bin` wrapper is broken by construction

pnpm's `node_modules/.bin/<tool>` wrapper is `#!/bin/sh` starting with
`basedir=$(dirname "$(echo "$0" | sed -e 's,\\,/,g')")`. The kernel passes the **invoked**
path as `$0` and `sh` does not resolve symlinks, so a farm symlink makes `basedir` the farm
directory and the wrapper fails to find its real entry point. Node apps must go through the
shim, which `exec`s the absolute real path. This is independently why D1 is a hybrid.

### The store is already content-addressed — which is what makes branch switching work

`{store}/.bin/{name}/{XXH3-128(binary config)}`. Two branches pinning different terragrunt
versions produce two coexisting files with no conflict (verified: `lefthook` currently has six
versions side by side). Branch switching is therefore entirely a question of _re-reading the
config cheaply_, not of moving files.

### stdout is a shared, uncoordinated channel

`ui.Display` hard-codes `os.Stdout` for both lines and mpb progress
(`internal/ui/ui.go:86,150`); a process-global `Display` is live for the whole run
(`cmd/root.go:147-148`); deep library code prints via `ui.Current()`; and user config JS
`console.log` writes straight to stdout (`internal/engine/console.go:33`). A repo's config can
therefore inject text into the stream the user pipes into `eval`. The only working mechanism
is the LSP precedent: a `PersistentPreRun` (not `OnInitialize`, so it beats `--log-format`)
forcing `ui.SetEventSink(uievent.NewJSONLSink(os.Stderr), true)` — `cmd/lsp.go:31-33`.

### The CLI contract gate

`test/cli/completeness_test.go:22` maps every leaf command to a blackbox test and fails the
build for an unregistered new leaf. `test/cli/root_test.go:15` pins the exact top-level
command set. Both must be edited for every new command, and `root_help.txt` regenerated.

### `datamitsu exec` cannot pass tool flags today

`datamitsu exec actionlint --version` fails with `unknown flag: --version` — cobra parses the
tool's flags, so `--` is mandatory. The shim bypasses cobra entirely and must pass argv
verbatim; this is a correctness requirement of property 1, independent of performance.

## Development Approach

- **Testing approach**: Regular (code first, then tests). Exception: Tasks 12 and 13 are the
  contract tests and are written against behavior already implemented.
- Complete each task fully before moving to the next. Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
  Tests are listed as separate checklist items, never bundled with implementation. Cover
  both success and error scenarios.
- **CRITICAL: all tests must pass before starting the next task.** Run `go test ./... -race`
  and `go test ./test/cli/ -count=2`.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Project is alpha; breaking changes are acceptable when they improve correctness or safety.
  Task 1 is a deliberate breaking change to config validation.
- Stdlib `testing` only — table-driven, `t.TempDir`/`t.Setenv`, no testify.
- Environment variable access goes through `internal/env` (envVar in `e.go`, getter in
  `env.go`, tests in `env_test.go`). No direct `os.Getenv` for `DATAMITSU_*`.
- Internal cache keys and fingerprints use **XXH3-128** via `internal/hashutil`; SHA-256 is
  reserved for verifying content that came over the network. The staleness key in Task 6 is
  internal and must be XXH3-128.

## Testing Strategy

- **Unit tests**: required for every task.
- **Blackbox CLI tests** (`test/cli`, harness `internal/clitest`): required for every new
  leaf command, with normalized goldens regenerated via `-update`, and a case added to
  `detCases` so `TestGoldenSuiteDeterministic` proves byte-stability.
- **Real-shell integration tests** (Task 13): a dedicated tier driving real `bash` and `fish`
  against a two-branch fixture repository. This is the only tier that can prove property 2,
  and it is the only place the generated shell code is actually parsed by the shell it
  targets. Skip cleanly (`t.Skip`) when the shell is unavailable.
- **No e2e OCI work**: the `test/e2e` tier (`//go:build e2e_oci`) is untouched.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep this plan in sync with the actual work.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, goldens, docs in this repo.
- **Post-Completion** (no checkboxes): manual verification, and the follow-up plans this one
  deliberately defers.

## Implementation Steps

### Task 1: Validate app names

App names become filenames and dispatch keys. This also closes a latent store-escape bug that
exists today, independent of source mode.

- [x] in `doValidateApps` (`internal/config/validate.go`), validate every app name against
      `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` as a hard error, appended to the existing `errs`
      slice in the established message style
- [x] reject `.`, `..`, any path separator, a leading `-`, and Windows reserved device names
      (`CON`, `PRN`, `AUX`, `NUL`, `COM1`-`COM9`, `LPT1`-`LPT9`) — `website/docs` promises
      Windows support eventually and these are unusable as filenames there
- [x] detect case-fold collisions across the app map (`strings.ToLower` over the sorted
      `appNames` slice already built at `validate.go:35-39`) and report both names — macOS
      filesystems are case-insensitive, so `Git` and `git` are one file
- [x] state in the error text that the constraint exists because app names become filesystem
      entries
- [x] **before landing**: enumerate every app name in the wrapper config
      (`node_modules/@shibanet0/datamitsu-config/`) against the regex and record the result
      here. The wrapper ships 92 of this repo's 98 apps and is versioned separately; a
      violation means the wrapper must be fixed and republished first
      — **Result: clean.** `datamitsu devtools apps list` over the merged config
      (`@shibanet0/datamitsu-config@0.0.0-unstable.20260819.598bb47` plus this repo's own
      declarations) yields **111** app names. All 111 match the regex; no case-fold
      collisions; no Windows reserved device names. No wrapper republish needed.
- [x] write a table test of hostile names: `../x`, `a b`, `$(id)`, `a\nb`, `.`, `..`, `-rf`,
      300 chars, empty, `CON`, `com1`
- [x] write a test asserting a case-fold collision (`{"Git": …, "git": …}`) produces exactly
      one error naming both
- [x] write a test asserting every app name in the embedded default config
      (`config/src/apps.ts`) passes — guards against breaking the shipped config
- [x] write a test asserting valid names are accepted: `yq-json`, `terraform-docs`, `mmdc`,
      `git-cliff`, `task`
- [x] run `go test ./... -race` and `go test ./test/cli/ -count=2` — must pass before Task 2

### Task 2: Add the per-root farm paths to internal/env

- [x] add `GetProjectBinPath(gitRoot string) (string, error)` in `internal/env/runtime.go`
      returning `{cache}/projects/{XXH3-128(gitRoot)}/bin`, mirroring `GetProjectCachePath`
      (`internal/env/runtime.go:49`) but without its hardcoded `cache` segment and without the
      `relativeProjectPath`/`toolName` parameters
- [x] add a sibling getter for the manifest file path under the same per-root directory
- [x] reuse the existing `HashProjectPath` (`internal/env/runtime.go:43`, XXH3-128 via
      `internal/hashutil`) — an internal fingerprint never compared against an external value,
      per the hashing policy
- [x] reject empty or relative `gitRoot` the same way `GetProjectCachePath` rejects escapes
- [x] write a table test: distinct git roots produce distinct paths; the same root is stable
      across calls
- [x] write a test asserting the path is under `GetCachePath()` and contains no `..` after
      `filepath.Clean`
- [x] write a test asserting `DATAMITSU_CACHE_DIR` is honored via `t.Setenv`, matching the
      existing `TestGetProjectCachePath` shape
- [x] write a test asserting empty and relative roots error
- [x] run tests — must pass before Task 3

### Task 3: Side-effect-free resolution

`source` must be able to ask "where would this tool be, and is it there?" without touching
the network.

- [x] split the runtime-bin-path lookup out of `installNode` into a pure
      `resolveNodeBinPath(runtimeName) (string, error)` that does not download —
      `getNodeCommandInfo` calls `installNode` (`internal/runtimemanager/node.go:299`) purely
      to obtain that path
- [x] do the same for `GetJVMCommandInfo`'s ctx-carrying path; `GetUVCommandInfo` and
      `GetGoCommandInfo` are already install-free and need no change
- [x] add `RuntimeManager.ResolveCommandInfo(appName string, app binmanager.App) (*binmanager.CommandInfo, error)`
      composing the existing `Get*CommandInfo` halves with no install call
- [x] add `BinManager.ResolveCommandInfo(appName string) (*CommandInfo, installed bool, err error)`:
      for `binary` apps use the non-downloading path computation plus `os.Stat` rather than
      `GetBinaryPath` (which downloads); for runtime kinds delegate to the new resolver and
      `os.Stat` the resolved `Command`; merge `app.Env` exactly as `GetCommandInfo` does via
      `mergeAppEnv`
- [x] leave `GetCommandInfo`'s behavior untouched — this is an addition, not a replacement
- [x] add a test-visible guard that the resolve path constructs no downloader, so a future
      refactor cannot silently reintroduce a network call
- [x] write a test asserting `ResolveCommandInfo` returns `installed=false` with no error for
      an app with no store entry, and performs no network call (assert under
      `DATAMITSU_OFFLINE=1` and with a nil/failing http client)
- [x] write a test asserting resolved `Command`/`Args`/`Env` for a fabricated node app match
      what `getNodeCommandInfo` produces (PATH prepend and `npm_config_*` present)
- [x] write a test asserting a jvm app resolves to `Command=java` with `-jar` in `Args`
- [x] write a test asserting a shell app returns a shell-typed `CommandInfo` without touching
      the filesystem
- [x] write a test asserting an unknown app returns the same error shape as `GetCommandInfo`
- [x] run tests — must pass before Task 4

### Task 4: internal/shellquote — per-shell escaping

Written before any shell text is emitted anywhere.

- [x] `Bash(s string) string` producing ANSI-C `$'…'` quoting with a guaranteed single-line,
      ASCII-only result; handle NUL explicitly rather than silently
- [x] `Fish(s string) string` using single-quote escaping with `\Xnn` for control bytes
- [x] `FishPathList(dirs []string) string` rendering a space-separated fish list — fish `PATH`
      is a list, not a colon-joined string
- [x] no dependency on `internal/ui` or any writer; pure string functions
- [x] write a table test over: newline, `$(`, backtick, single quote, double quote, backslash,
      `!`, tab, leading dash, non-UTF-8 bytes, empty string
- [x] write a round-trip oracle test: for each vector, `sh -c "printf %s " + Bash(v)` returns
      `v` byte-exactly; `t.Skip` if `/bin/sh` is unavailable
- [x] write the same round-trip oracle against real `fish` for `Fish()`, skipping if absent
- [x] write `FuzzBash` asserting the round-trip property over arbitrary byte strings
- [x] write a test asserting `Bash()` output never contains a raw newline
- [x] run tests — must pass before Task 5

### Task 5: internal/sourcefarm — the plan (pure, no filesystem)

- [x] define `Entry{Name, Provider, Kind, Strategy, Command, Args, Env, Installed}`,
      `Excluded{Name, Reason}` and `Plan{Root, FarmDir, Entries, Excluded, Shadowed}`
- [x] `Strategy` is `symlink` or `shim`, decided mechanically: `symlink` only when the kind is
      `binary` or `go`, **and** the app is installed, **and** `Args` is empty, **and** the
      merged `Env` is empty. Everything else is `shim`
- [x] `BuildPlan(apps, resolver, lookPath)` — exclude `shell` kind categorically with the
      reason "shell apps resolve through the inherited PATH"; exclude deny-listed names;
      resolve via the injected side-effect-free resolver from Task 3
- [x] **include every declared app as an Entry, installed or not** — a not-yet-installed app
      gets `Strategy=shim, Installed=false`. This is the D4 consequence: a declared name must
      never be absent from the farm, or `PATH` falls through to a system binary
- [x] hard deny-list constant: `sudo`, `su`, `doas`, `sudoedit`, `sh`, `bash`, `zsh`, `fish`,
      `dash`, `env`, `ssh`, `scp`, `sftp`, `git`, and `datamitsu` itself. aqua-proxy refuses to
      proxy its own name for the same reason: a shimmed `datamitsu` turns the shim's own
      installer spawn into an infinite `exec` loop
- [x] shadow detection: for each surviving entry, call the injected `lookPath` against the
      **pre-activation** `PATH` and record the absolute path found
- [x] record an explicit `Reason` on every exclusion — a name that silently does not appear is
      undebuggable, and this field is what `source status` prints
- [x] deterministic ordering: sort `Entries` and `Excluded` by name
- [x] write a test asserting a shell app named `echo` is always excluded with the shell reason
- [x] write a test asserting a deny-listed name is excluded even when it resolves to a valid
      binary app
- [x] write a test asserting an uninstalled app produces an Entry with `Strategy=shim` and
      `Installed=false` — **not** an exclusion
- [x] write a test asserting `Strategy=symlink` requires all four conditions, with a case
      per condition failing
- [x] write a test asserting shadow detection reports the absolute path from the injected
      `lookPath` stub and leaves the entry included
- [x] write a determinism test: `BuildPlan` output is byte-identical across two calls over a
      map with randomized iteration order
- [x] run tests — must pass before Task 6

### Task 6: The manifest and the staleness key

This is what makes property 2 work at ~0 cost: the shim revalidates per invocation with a
handful of `stat` calls (measured: 2.1 µs each, 14 µs for a full chain check) instead of a
config load (measured: ~226 ms before the startup plan, ~60 ms after).

- [x] define the on-disk manifest as **JSON** with a typed Go struct and `json` tags — not a
      packed binary format. Reading and decoding a 16.6 KB / 100-app table was measured at
      311 µs against a ~10 ms process; a hand-rolled codec buys 0.3 ms and costs a format
      version to migrate and test data that cannot be golden-diffed
- [x] manifest contents: format version, the authoritative git root, the datamitsu version,
      os/arch, the staleness key, the watch set, and one record per entry
      (name, strategy, command, args, env, installed)
- [x] include an `origin` field recording how the farm was created — `git-root` is the only
      value this plan produces. It exists so the follow-up plan for activation outside a
      repository can add a second value without a format migration, and so the shim can decide
      whether cwd-based root discovery applies at all
- [x] record the **authoritative** root (as resolved by the config loader) inside the
      manifest, so the shim's cheap walk is only used to _select_ which manifest to open, not
      to decide the root. The two can disagree inside a submodule, where `facts.GetGitRoot`
      climbs to the topmost superproject
- [x] watch set: every config-chain file path with `{path, mtime_ns, size, exists}`, plus
      `.git/HEAD`. Compare with `!=`, not a `>` watermark — `!=` catches mtime regressions and
      the exists/gone transitions a branch switch actually produces. `.git/HEAD` is
      load-bearing, not belt-and-braces: a branch that _deletes_ `datamitsu.config.ts`
      produces no mtime on a file that no longer exists, and `HEAD` is rewritten by checkout
      but not by commit, so it causes no spurious rebakes
- [x] include `pnpm-lock.yaml` and the resolved before-config file paths in the watch set —
      a branch that bumps the shared config dependency changes those, and without them the
      rebake reads stale `node_modules` and stamps the result fresh
- [x] staleness key: XXH3-128 (`internal/hashutil`) over {format version, datamitsu version,
      authoritative git root, os/arch, the ordered watch-set tuples, and the `DATAMITSU_*`
      environment variables that feed `runtimeconfig`}. Internal fingerprint, never compared
      against an external value — XXH3 is mandatory here per the hashing policy
- [x] **document the known hole in the manifest format's doc comment**: config JS receives the
      whole environment via `facts().env` (`internal/facts/facts.go:51`) and this repo's own
      `datamitsu.config.ts` already branches on `facts().env.DATAMITSU_BENCH`. A config
      branching on a non-`DATAMITSU_` variable will not invalidate the key. `datamitsu source
refresh --force` is the escape hatch. Tracking the variables the VM actually read
      requires goja read-instrumentation and is explicitly deferred
- [x] `Load(path)` / `Validate(manifest) (fresh bool)` API, where `Validate` performs only
      `lstat` calls and the key comparison — no config load, no allocation-heavy work
- [x] write a table test over staleness transitions: file content changed, file deleted, file
      added, mtime regressed, size changed, `.git/HEAD` rewritten, datamitsu version changed,
      `DATAMITSU_*` var changed — each must report stale
- [x] write a test asserting an unchanged tree reports fresh
- [x] write a test asserting `Validate` opens no config and spawns no subprocess
- [x] write a benchmark asserting `Validate` over a 3-file chain stays in the microsecond range
- [x] write a test asserting an unknown/newer format version reports stale rather than erroring
- [x] write a round-trip test through `json.Marshal`/`Unmarshal` with stable key order
- [x] ➕ add `env.Environ()` (`internal/env/environ.go`) returning datamitsu's own
      environment variables sorted, so the staleness key can fingerprint them without any
      package reaching for `os.Getenv` directly — per the environment-variable policy. It is
      deliberately a superset of the variables feeding `runtimeconfig`: over-invalidating is
      correct but slow, missing one is a stale farm
- [x] run tests — must pass before Task 7

### Task 7: Atomic farm materialization

- [x] `Materialize(plan, manifest) error`: build the whole directory in a sibling temp dir on
      the same filesystem, fsync, then `os.Rename` into place; on rename failure `os.Stat` the
      destination and treat its existence as a peer having won (aqua's lock-free model). Never
      mutate a live directory in place
- [x] `symlink` entries become a real symlink to the resolved `Command` in the
      content-addressed store
- [x] `shim` entries become a symlink to the absolute datamitsu executable path
      (`os.Executable`, resolved), so argv[0] dispatch can pick them up
- [x] **never emit a dangling symlink** — an entry whose target does not exist is a `shim`
      entry by construction (Task 5's `Installed` condition), so this is a guarded invariant
      with a test, not a hope
- [x] write the manifest inside the same atomic swap so the farm and its manifest can never
      disagree
- [x] directory `0o700`, entries `0o755`; refuse to materialize if an existing farm directory
      is group- or other-writable or not owned by the current uid
- [x] refuse to materialize if the target path is not under `GetCachePath()` — defense against
      a bad root
- [x] per-root advisory lock around the bake (`flock` on a lock file created
      `O_CREAT|O_RDWR|O_CLOEXEC` and **never unlinked**), non-blocking first with a poll-and-
      timeout fallback: poll for the artifact to appear, and only on timeout block on the
      lock. Ten tmux panes activating at once is the case this exists for
- [x] on a failed bake (offline, broken config, unreachable remote config) **keep the previous
      manifest and farm**, do not advance the stamp, print one line to stderr, and let the next
      invocation retry — nix-direnv's fail-stale-not-empty rule. The alternative default is an
      empty farm and a machine-wide 127 storm
- [x] no `ui.Current()` calls anywhere in this package — it must be silent
- [x] write a test asserting re-materializing over an existing farm removes entries no longer
      in the plan
- [x] write a test asserting modes: directory `0o700`, entries `0o755`
- [x] write a test asserting materializing to a path outside the cache root errors
- [x] write a test asserting two successive `Materialize` calls with the same plan produce
      identical directory listings
- [x] write a test asserting no entry in the farm is a dangling symlink
- [x] write a concurrency test (`-race`) running N concurrent `Materialize` calls for the same
      root and asserting the final farm is complete and consistent, with no partial directory
- [x] write a test asserting a bake that fails midway leaves the previous farm and manifest
      intact
- [x] ➕ add `env.GetProjectLockPath` and `env.ProjectLockFileName`
      (`internal/env/runtime.go`) so the lock file's location is derived from the same
      per-root path helper as the farm and the manifest, rather than being assembled by
      string concatenation in `sourcefarm`
- [x] run `go test ./... -race` — must pass before Task 8

### Task 8: argv[0] shim dispatch

The shim path must not build the cobra tree or construct the UI. `cmd.Execute()`
(`cmd/root.go:138-153`) runs `clr.Init()`, `ui.New(term.DetectMode())` and `ui.Activate` before
cobra; dispatch happens before all of it.

- [x] in `main.go`, before `cmd.Execute()`, inspect `filepath.Base(os.Args[0])`; when it is not
      one of datamitsu's own invocation names, take the shim path
- [x] **fall back to normal CLI execution** when the name is not declared in the resolvable
      manifest and the executable was not invoked through a farm — a user who renames the
      binary must not lose the CLI. Make this rule explicit and test it in both directions
- [x] shim path: `getcwd` → cheap walk up for `.git` → open the per-root manifest →
      `Validate` → look up the name
- [x] fresh manifest and entry installed: `syscall.Exec` the recorded `Command` with `Args`
      prepended to the user's argv and the recorded `Env` merged into the environment. Pass
      user argv **verbatim** — this is what fixes the `unknown flag: --version` behavior
- [x] use `syscall.Exec`, not a child process: it preserves signals, exit codes, stdin/stdout
      wiring and the process table, and it is why the shim tax is one process and not two
- [x] stale manifest: take the lock, re-bake by spawning the full datamitsu resolution path,
      then re-read and exec. This is the one visible hiccup per branch switch
- [x] entry present but not installed: run the install path, then re-resolve and exec. This is
      property 3
- [x] **`stat` the exec target before `execve` rather than relying on `ENOENT`.** For
      interpreter-based targets (`#!/usr/bin/env node`), `ENOENT` means "script missing" _or_
      "interpreter missing", and the two need different handling. A `stat` costs ~2 µs against
      a ~10 ms process
- [x] name not in the manifest: **exit 127** with a message naming the app, the root, and
      `datamitsu source status`. Never search the rest of `PATH` (D4)
- [x] **no manifest for the cwd's root: exit 127 telling the user to run `datamitsu source`
      in that repository.** Do not bake it implicitly. This is the case where an activated
      shell `cd`s into a different, never-activated repository, and baking would mean
      evaluating that repository's JavaScript merely because a tool name was typed —
      direnv's threat model, with no approval gate to answer it. Explicit activation is the
      act of trust
- [x] resolve the datamitsu executable to install/rebake from `os.Executable()`, never through
      a `PATH` the farm itself controls — otherwise a shimmed name can hijack the spawn
- [x] add a benchmark pinning the shim dispatch path's cost so nobody adds work to it
      — `BenchmarkDispatch` over a 101-entry manifest measures **214 µs/op** on an
      Apple M1 Max, ~84% of it in system calls (the manifest read plus four stats)
- [x] write a test asserting argv is passed verbatim, including a leading `--version` and
      arguments containing spaces, quotes and newlines
- [x] write a test asserting the exit code of the target is preserved exactly (0, 1, 42, 127)
- [x] write a test asserting stdout and stderr are the target's, unmodified and unbuffered
- [x] write a test asserting an unknown name exits 127 with the expected message and does
      **not** exec anything from the rest of `PATH`
- [x] write a test asserting a renamed datamitsu binary still runs the normal CLI
- [x] write a test asserting a stale manifest triggers exactly one rebake and then execs the
      new target
- [x] write a test asserting a not-installed entry triggers the install path exactly once
- [x] ➕ farm detection is a pure path comparison (`{cache}/projects/*/bin/<name>`)
      rather than a stat of the manifest beside the farm: the cases that most need
      to fail loudly — no manifest for this tree, or a manifest that will not parse
      — are exactly the ones a presence check would misread as "not a farm" and
      quietly turn into a CLI run holding a tool's argv
- [x] ➕ the exec-path tests run the dispatch in a re-invoked child process, since
      `syscall.Exec` cannot be observed from the process that performs it
- [x] run `go test ./... -race` — must pass before Task 9

### Task 9: The `datamitsu source <shell>` commands

Shells are subcommands, matching how cobra's own `completion bash|fish|zsh` works and this
repo's `store`/`config` group style.

- [x] add `cmd/source.go` with a `source` group command and `bash`, `zsh`, `fish` leaves
- [x] `PersistentPreRun` unconditionally calling
      `ui.SetEventSink(uievent.NewJSONLSink(os.Stderr), true)` — the `cmd/lsp.go:31-33`
      pattern. It beats `--log-format` and silences both `ui.Current()` in library code and
      config-JS `console.log`
- [x] handler: load config → `BuildPlan` → `Materialize` → write shell code with
      `fmt.Fprint(cmd.OutOrStdout(), …)` only, never through any `ui` primitive
- [x] **no config discovered (no git root, or a git root with no `datamitsu.config.*`): exit
      non-zero with an actionable message and write nothing to stdout.** Config discovery is
      git-root-only (`discoverAutoConfig` stats three filenames at the git root and nowhere
      else), so outside a repository datamitsu would otherwise fall back to the embedded
      default config and silently emit an activation for a handful of built-in apps — the
      worst outcome, because it looks like it worked. The message must name the existing
      `--config` persistent flag (`cmd/root.go:110`) and show a concrete invocation. Making
      that path actually produce a working farm is the follow-up plan; failing loudly is this
      plan's job
- [x] bash/zsh renderer: export `DATAMITSU_ROOT` and `DATAMITSU_FARM`, remove any existing
      occurrence of the farm dir from `PATH`, prepend it, then `hash -r`. Target bash 3.2
      (macOS ships it) — no associative arrays, no namerefs
- [x] fish renderer: `set -gx`, then `fish_add_path --global --move --path <dir>` — `--move`
      is required or re-activation silently no-ops
      — ⚠️ **scope change: no `functions -c` line.** `functions -c` takes two arguments (it
      copies a function) and would abort the activation. fish has no `hash -r` equivalent to
      emit: it resolves commands against `PATH` per invocation, verified by the real-fish
      test. ➕ Also discovered: `fish_add_path` silently skips a directory that does not
      exist, so the real-shell tests bake against a farm directory that is actually there
- [x] every emitted path goes through `internal/shellquote`
- [x] activation must **not** install anything and must be safe to run repeatedly
- [x] warnings via direct `fmt.Fprintf(os.Stderr, …)`, bypassing `ui`: not-yet-installed apps,
      shadowed system binaries with their absolute paths, and offline failures surfacing
      `httpx.GuardOffline`'s existing text verbatim rather than new wording (a failed config
      load is returned unwrapped in wording, so the guard's own text is what the user reads)
- [x] write renderer unit tests: bash output contains exactly one `PATH` mutation; fish output
      uses `--move`; rendering twice is byte-identical
- [x] write a test asserting a farm path containing a space and a single quote renders to
      shell that parses without error — ⚠️ **scope change: real `bash` and real `fish`, not
      `/bin/sh`.** The renderer uses `${var//pat/rep}`, which is bash/zsh syntax and not
      POSIX, so `sh -n` would be a false negative under dash. The tests execute the
      activation and read back `PATH`, which is strictly stronger than a parse check, and
      skip cleanly when the shell is absent
- [x] write a test asserting the renderers accept a `Plan` and return a string, with no
      filesystem or config dependency
- [x] write a test asserting stdout contains no warning text when warnings are emitted
- [x] ➕ add `DATAMITSU_ROOT`/`DATAMITSU_FARM` to `internal/env` (`e.go` + getters and
      name accessors in `env.go`) per the environment-variable policy, and **exclude them
      from `env.Environ()`**: they mark which farm a shell activated rather than how
      datamitsu behaves, and including them in the staleness key would make every activated
      shell disagree with the process that baked its farm and rebake on every command
- [x] ➕ record the config chain's file paths during `loadConfigImpl`
      (`cmd.ConfigChainFiles()`), so the manifest's watch set covers the declared
      before-configs — which are only known after the auto config has been evaluated, the
      exact work the watch set exists to avoid repeating
- [x] ➕ `datamitsu source` with no shell, or with an unsupported one, exits non-zero: a
      cobra group with no `Run` prints help and exits 0, which under `eval "$(…)"` would
      evaluate a help screen
- [x] ➕ registered `source bash|zsh|fish` in `test/cli/root_test.go` and
      `test/cli/completeness_test.go` with an initial `test/cli/source_test.go`, and added a
      `## source` section to `website/docs/reference/cli-commands.md` plus a regenerated
      `internal/llmsdocs/embed` — all three are hard build gates that fail the moment a leaf
      command exists, so they cannot wait for Tasks 12 and 15
- [x] run tests — must pass before Task 10

### Task 10: `datamitsu source status` and `--json`

Under D4 this is the primary mitigation for shadowing and the primary debugging tool. It is
not optional polish.

- [x] add a `status` leaf printing: the root, the farm directory, whether the manifest is
      fresh, every entry with its strategy and install state, every exclusion **with its
      reason**, and every shadowed system binary with the absolute path it was found at
- [x] add `--json` emitting one typed struct with `json` tags — no `map[string]any`, and
      nothing derived from `runtimeconfig.Effective` (per the Runtime Config vs Config Inputs
      policy). Serialize with `json.MarshalIndent(v, "", "  ")` via `cmd.OutOrStdout()`,
      matching `cmd/devtools_parsers.go:245-254`
- [x] in `--json` mode emit no human text at all; warnings still go to stderr
- [x] document this struct as the single serialization any future `exec --json` reuses
- [x] write a test asserting marshalled output has stable key order and sorted lists across
      two calls
- [x] write a test asserting required keys are present — assert presence and values, never a
      field count, per the runtime-config policy precedent
- [x] write a test asserting `shadows` is omitted when empty and carries the absolute path
      when set
- [x] write a test asserting every exclusion carries a non-empty reason
- [x] write a test asserting the output round-trips through `json.Unmarshal`
- [x] ➕ `status` resolves and reports but never materializes: a diagnostic that repairs the
      farm it describes cannot be used to observe a broken one. `bakeSourceFarm` was split
      into a read-only `resolveSourcePlan` plus the materialize step, which Task 11's
      `refresh` reuses. A blackbox test asserts no `manifest.json` appears in the cache
- [x] ➕ the manifest's freshness is reported as a four-state word — `fresh`, `stale`,
      `missing`, `unreadable` — rather than a bare boolean. All three non-fresh states mean
      the same rebake, but a user needs to tell a corrupted farm from one that has simply
      aged out, and "never baked here" is the state the shim reports as exit 127
- [x] ➕ registered `source status` in `test/cli/completeness_test.go`, added
      `TestSourceStatus`/`TestSourceStatusJSON`/`TestSourceStatusDoesNotBake` to
      `test/cli/source_test.go`, extended the `## source` section of
      `website/docs/reference/cli-commands.md` and regenerated `internal/llmsdocs/embed` —
      the same three build gates Task 9 had to satisfy the moment a leaf command exists
- [x] run tests — must pass before Task 11

### Task 11: `datamitsu source refresh`

- [x] add a `refresh` leaf that re-bakes the farm for the current root and prints a one-line
      summary to stderr, nothing to stdout
- [x] `--force` bypasses the staleness check entirely — the documented escape hatch for the
      `facts().env` hole recorded in Task 6
- [x] respect offline and strict-OCI policy: refresh resolves and materializes the farm but
      **downloads nothing**; a missing tool stays a `shim` entry
- [x] write a test asserting `refresh` on an unchanged tree is a no-op that still exits 0
- [x] write a test asserting `--force` re-bakes an already-fresh manifest
- [x] write a test asserting `refresh` performs no download under `DATAMITSU_OFFLINE=1`
- [x] write a test asserting stdout is empty
- [x] ➕ the freshness check runs **before** the config is loaded, not after: the no-op case
      is the common one, and deciding it from the on-disk manifest's watch set costs a
      handful of `lstat` calls instead of a full config evaluation. Every non-fresh state —
      missing, unreadable, aged out — answers "re-bake", so a corrupted farm can be repaired
      without deleting the cache
- [x] ➕ registered `source refresh` in `test/cli/completeness_test.go`, added
      `TestSourceRefresh`/`TestSourceRefreshDownloadsNothing` to `test/cli/source_test.go`,
      extended the `## source` section of `website/docs/reference/cli-commands.md` and
      regenerated `internal/llmsdocs/embed` — the same three build gates Tasks 9 and 10 had
      to satisfy the moment a leaf command exists
- [x] run tests — must pass before Task 12

### Task 12: Blackbox CLI tests and contract registration

Both registries are hard build failures if not updated.

- [x] add `source` to `expectedTopLevelCommands` (`test/cli/root_test.go:15-32`)
- [x] register every new leaf in `testedLeafCommands` (`test/cli/completeness_test.go:22`):
      `source bash`, `source zsh`, `source fish`, `source status`, `source refresh`
- [x] add `test/cli/source_test.go`
- [x] regenerate `root_help.txt` and add the per-command help goldens via
      `go test ./test/cli/ -update` — `root_help.txt` already carried `source` from Task 9;
      added `source_help` plus one golden per leaf
- [x] goldens for bash/zsh/fish using
      `clitest.NewNormalizer().MaskPath(p.Dir, "<TMP>").MaskPath(cacheBase, "<CACHE>")`
- [x] pin `PATH` and `SHELL` explicitly via `RunOptions.Env` in every case —
      `internal/clitest` strips `DATAMITSU_*`/`CI`/`TERM`/`NO_COLOR` but **not** `PATH` or
      `SHELL`, and shadow detection reads `PATH`, so goldens are machine-dependent without
      this. Get it wrong and CI is green locally and red on the runner
- [x] add source cases to `detCases` so `TestGoldenSuiteDeterministic` proves byte-stability
- [x] write a test asserting stdout contains exactly the expected shell block and nothing else,
      and stderr is empty for the clean case (mirroring `test/cli/lsp_test.go:23-59`)
- [x] write a test with a fixture config whose `getConfig()` calls `console.log`, asserting
      stdout has zero extra bytes — this is the config-injection guard
- [x] write a test with a fixture config declaring a shell app named `echo`, asserting the farm
      does not contain `echo`
- [x] write a test asserting a fixture config declaring an app named `sudo` produces an
      exclusion with a deny-list reason
- [x] write a test asserting an unknown shell exits non-zero naming the supported shells
- [x] write a test asserting `source` outside a git repository exits non-zero, writes nothing
      to stdout, and names `--config` in the stderr message
- [x] write a test asserting `source` in a git repository with no `datamitsu.config.*` does the
      same, rather than activating the embedded default config
- [x] write a test asserting `--json` parses as JSON and stdout contains no shell code
- [x] `go test ./test/cli/ -count=2` passes byte-identically
- [x] ➕ the per-root farm directory is `{cache}/projects/{XXH3-128(gitRoot)}/`, so masking
      the cache and project paths is not enough to make source output comparable — the
      fingerprint of a fresh temp root differs on every run. A `maskFarmHash` normalizer
      collapses that segment, and a new `detSource` kind carries it into the determinism
      suite, which also has to write the config where auto-discovery finds it: `source`
      refuses an implicit fallback, so `detConfig`'s `--no-auto-config --config` shape
      does not apply
- [x] ➕ moved `test/cli/source_test.go` from `package cli` to `package cli_test`, matching
      every other file in the suite bar `lsp_test.go`, so `produceDet` in
      `completeness_test.go` can share the source fixtures instead of duplicating them
- [x] run `go test ./... -race` — must pass before Task 13

### Task 13: Real-shell integration tests

The only tier that can prove properties 1–3. Everything above tests Go; this tests the thing
the user actually experiences.

- [x] add a test tier driving real `bash` and real `fish` against a fixture git repository with
      two branches pinning different versions of the same fake tool, using a stub "binary"
      that prints its version — `test/shell`, with a loopback `httptest` release host so the
      real download, SHA-256 verification and install path run end to end
- [x] `t.Skip` cleanly when the shell is not installed, and state in the skip message that the
      property is unverified on this machine
- [x] **property 1**: activate, run the tool, assert the pinned version — not a same-named
      binary planted earlier in `PATH`
- [x] **property 2, the load-bearing case**: in one activated shell, run
      `git checkout v2 && <tool> --version` **on a single line** and assert the v2 version. Then
      `git checkout v1 && <tool> --version` on a single line and assert v1. No re-activation
      between them — ⚠️ **this failed on first run, and the cause was D1's symlink half**; see
      the scope change below
- [x] **property 2, non-interactive**: assert the same through `make`, through `bash -c`, and
      through a `sh -c` recipe — contexts where no prompt ever renders
- [x] **property 3**: delete the tool from the store, run it, assert it is materialized and
      executed, and assert the exit code is the tool's and not 127 — split in two: the
      never-installed case (the first invocation after activation, which is the real lazy path)
      and a delete-from-store case repaired by `source refresh --force`
- [x] **D4**: plant a same-named binary in `PATH`, remove the app from the config, re-bake, and
      assert exit 127 rather than the planted binary running — the re-bake is the shim's own,
      triggered by `git checkout none && <tool>` on one line, and the test first asserts the
      planted binary is reachable so it cannot pass vacuously. ⚠️ **this failed on first run
      too**; see the argv[0] scope change below
- [x] assert exit codes propagate for 0, 1 and 42
- [x] assert argv passes verbatim: a `--version` flag, an argument with a space, an argument
      with a single quote, an argument with a newline (and a bare `*`, which a shell would
      glob-expand if the shim reconstructed the command line instead of passing argv through)
- [x] test a farm path containing a space, a `'` and a `[` — glob-sensitive shell code is the
      class of bug only a real shell catches
- [x] assert activating twice does not duplicate the `PATH` entry, in both bash and fish
- [x] run `go test ./... -race` and the new tier — must pass before Task 14
- [x] ⚠️ **scope change, correctness: `Strategy=symlink` is no longer chosen.** The property-2
      test fails for an _installed_ `binary` app, which is exactly what D1 optimizes. A store
      symlink has nowhere to put the freshness check: nothing of datamitsu's runs when the
      kernel follows it, so after `git checkout v2` the entry keeps resolving to the previous
      branch's version — silently, exiting 0, printing plausible output. That is the failure
      D4 exists to prevent, arriving through the other door. `strategyFor` now returns
      `shim` for every entry; `StrategySymlink` and its materialization stay implemented and
      tested, so re-enabling is one branch if a cheaper revalidation hook is ever found.
      **This narrows D1 and the owner should confirm it.** Measured cost of the change:
      `BenchmarkDispatch` is 227 µs/op, against 0 for a symlink.
- [x] ⚠️ **scope change, correctness: farm detection cannot read argv[0].** Task 8 assumed a
      shell execs the absolute path it found on `PATH` and passes it as argv[0]. It does not —
      bash, fish and dash all pass the _word the user typed_ (verified directly). Every loud
      failure therefore degraded into an ordinary CLI run: `stub-tool` after a branch that
      dropped the app printed datamitsu's help and exited 0 instead of exiting 127.
      `invokedThroughFarm` now resolves a bare name against `PATH` itself, the same way the
      shell just did, and memoizes the answer **before** any rebake — the config change that
      makes a manifest stale is usually the one that removed the app, and the rebake deletes
      the farm entry the answer is read from
- [x] ➕ **the shim's install/rebake spawn re-entered itself on macOS.** A farm entry is a
      symlink to the datamitsu binary, and `os.Executable` reports the invoked path rather than
      the file behind it on darwin, so `Spawn(exe, "install", name)` ran the tool's own farm
      entry again: a fork bomb that only ended with the process table. `datamitsuExe` now
      resolves symlinks and refuses outright to spawn anything still inside a farm
- [x] ➕ `BenchmarkDispatch` now benchmarks the invocation shape a shell actually produces —
      a bare argv[0] with the farm first on `PATH` — rather than a pre-resolved absolute path

### Task 14: Verify acceptance criteria

All measurements below are on an Apple M1 Max, `hyperfine -N` with ≥300 warmup runs, against
this repository's own 111-app config unless stated otherwise.

- [x] `datamitsu source bash|zsh|fish` prints valid activation code that the target shell
      parses without error — bash and fish are proven by the Task 13 tier, which **executes**
      the activation rather than parsing it (all 10 tests ran, neither shell skipped). zsh is
      not driven by that tier, so it was verified directly: `zsh -n` on the emitted file parses,
      and a live `eval` leaves the farm at `$path[1]`
- [x] activation is idempotent and adds exactly one `PATH` entry — `TestActivationIsIdempotent`
      covers bash and fish; the zsh check above double-activates and counts exactly 1
      occurrence of the farm directory in `PATH`
- [x] activation downloads nothing (assert under `DATAMITSU_OFFLINE=1`) — a cold-cache
      `source bash` under `DATAMITSU_OFFLINE=1 DATAMITSU_NO_OCI=1` exits 0 and bakes all 110
      entries; `TestSourceRefreshDownloadsNothing` pins it
- [x] a `binary` app's steady-state invocation adds no measurable overhead versus running the
      store binary directly — **⚠️ NOT MET as written, by the deliberate Task 13 narrowing of
      D1.** With `Strategy=symlink` withdrawn, a `binary` app pays the same shim tax as every
      other kind. Measured against `/usr/bin/true` as the exec target, so the number is the
      datamitsu process itself and not the tool's own startup: **+10.1 ms** (12.8 ± 0.7 ms vs
      2.7 ± 2.2 ms) and **+12.3 ms** (14.0 ± 2.5 ms vs 1.7 ± 0.3 ms) across two independent
      runs — call it **~10–12 ms**. This is the cost the owner is being asked to confirm
      alongside the D1 narrowing; the alternative is a silently wrong binary after a checkout
- [x] a `node`/`jvm` app's steady-state invocation adds ~10 ms — **+11.8 ms** (13.5 ± 1.3 ms vs
      1.7 ± 0.3 ms) for an entry carrying both an argv prefix and an `Env` overlay, measured in
      the same run as the `binary` entry above. Indistinguishable from the `binary` case within
      noise, which is the expected consequence of every entry being a shim: the dispatch work is
      identical and only the exec target differs
- [x] the branch-switch hiccup (first invocation after a checkout) is recorded here as a real
      number — the stale path is a full config load plus resolve plus materialize, measured as
      `source refresh --force` over the 111-app config: **132.2 ± 27.7 ms** (20 runs, offline).
      Adding the ~12 ms shim gives a **~145 ms one-off hiccup per config change**. The
      steady-state counterpart, `source refresh` on a fresh manifest, is **12.7 ± 2.5 ms** —
      identical to a bare shim invocation, confirming the watch-set check itself costs nothing
      measurable beyond process startup. Measured by composition rather than end to end: the
      shim's stale path needs an installed entry _and_ a real config, and the two fixtures that
      have each do not overlap. The Task 13 tier proves the behavior end to end
- [x] stdout carries shell code only; all warnings are on stderr — the activation is 444 bytes
      of `export`/`PATH` lines and nothing else, while all 21 shadow warnings went to stderr
- [x] no secrets appear in activation output — the emitted code names exactly two values, the
      git root and the farm directory, both already-known paths. No config content, no
      environment values, no tokens
- [x] every declared app has a farm entry; no farm entry is a dangling symlink — 111 declared
      names resolve to 110 farm entries plus 1 exclusion (`rustfmt`, a shell app, carrying the
      shell reason); the farm directory holds exactly 110 files, every entry name is present as
      a file, and `find -type l ! -exec test -e {} \;` returns nothing
- [x] `go test ./... -race` passes
- [x] `go test ./test/cli/ -count=2` passes — 43.6 s, byte-identical across both runs
- [x] `pnpm dm check` passes — 22 tools, 80 runs, 0 failed
- [x] coverage meets the project standard via `pnpm test:coverage:all` — **81.7%** combined.
      The packages this plan added are above it: `internal/shellquote` 100%,
      `internal/shim` 88.7%, `internal/sourcefarm` 84.6%

### Task 15: [Final] Documentation

The docs tail is non-negotiable and the drift job runs a full Docusaurus build on every PR.
Budget it as real work, not as "write two markdown files".

- [x] `website/docs/guides/source-mode.md` with `title` + `description` frontmatter (the
      harvester rejects pages without them) and a Mermaid diagram of
      config → resolved apps → farm → activation. Examples in bash and fish only, no Go
- [x] document the branch-switch performance contract honestly: one visible hiccup per config
      change, invisible thereafter — a four-row table carrying the Task 14 numbers verbatim
      (~10–12 ms steady state, ~145 ms once per config change), including why the 0 ms symlink
      path was withdrawn
- [x] document the `facts().env` staleness hole from Task 6 and point at
      `datamitsu source refresh --force`
- [x] document that `which <tool>` shows the farm path, never the store path — the asdf
      pathology, now unmitigated since D1's symlink half was withdrawn in Task 13. Point at
      `datamitsu source status`, which prints the real target
- [x] add a hand-written `## source` section to `website/docs/reference/cli-commands.md` in
      house style, modelled on the `## llms` section — written from the generated goldens, not
      from this plan — written incrementally in Tasks 9–11 (each leaf command is a hard build
      gate the moment it exists), reviewed here against the goldens and left as-is
- [x] register both pages in `website/sidebars.ts` — `guides/source-mode` added to the Guides
      category; `reference/cli-commands` and `reference/comparison` were already registered
- [x] rewrite `website/docs/reference/comparison.md:108-112`, which currently tells users to
      pick mise when "shell-integrated workflows are important". Use the "different layers"
      framing — datamitsu activates a pinned, hash-verified, config-distributed toolchain;
      mise activates per-developer runtime versions. No replacement claims — replaced with a
      "Both activate a shell — at different layers" block that composes the two in one example
      and names the concrete mechanical difference (prompt hook vs per-invocation revalidation)
- [x] replace every `terraform` example with `tofu` — the wrapper declares
      tofu/terragrunt/tflint/tfupdate/terraform-docs and there is no `terraform` app
      — **Result: no examples to replace.** A sweep of `website/` finds `terraform` only in the
      generated `reference/parser-catalog.md`, as the upstream parser id `terraform_validate`,
      the `hashicorp/terraform` link and tfsec's own description — real upstream names, not
      datamitsu app names, and the page is regenerated by `task gen:parsers-doc`. The new guide
      uses `tofu` throughout
- [x] run `task gen:llms-docs` and commit `internal/llmsdocs/embed` — the `llms-docs-drift`
      job re-harvests on every PR and fails on any diff — 52 pages, pageSetHash
      `151a8c2242c07f3dc5c2523869d51752`
- [x] do **not** modify `config/src/prompts/datamitsu-agent-guide.md` — it is distributed to
      every consuming repo on their next `dm init`, and source mode is not yet the recommended
      path for agents — untouched
- [x] `pnpm dm check --file-scoped` passes (cspell, prettier, markdownlint) — 9 tools, 9 runs,
      0 failed. `go test ./... -race` and `go test ./test/cli/ -count=2` re-run green after the
      docs regeneration

## Technical Details

### The farm layout

```text
{cache}/projects/{XXH3-128(gitRoot)}/
  bin/
    terragrunt   -> {abs path to datamitsu}                  # shim
    kubectl      -> {abs path to datamitsu}                  # shim
    prettier     -> {abs path to datamitsu}                  # shim, needs Env
    spectral     -> {abs path to datamitsu}                  # shim, jvm Args
    tflint       -> {abs path to datamitsu}                  # shim, not installed yet
  manifest.json
  lock
```

Every entry is a shim after the Task 13 narrowing of D1. A store symlink — the 0 ms path this
layout originally reserved for `terragrunt` and `kubectl` — has nowhere to put the freshness
check, so it cannot switch versions with the branch.

`echo` (shell app) and `git`/`sudo` (deny-listed) appear in `manifest.json` under `excluded`
with a reason, and never as files.

### What happens when the user types `terragrunt plan`

**Steady state.** `PATH[0]` hit → `execve` of the datamitsu binary → argv[0]
dispatch before cobra → `getcwd`, walk up for `.git`, read + validate the manifest
(microseconds) → `stat` the target → `syscall.Exec`. ~10 ms, one process, signals and exit
codes preserved.

**After a branch switch.** The watch-set comparison fails, the shim takes the lock, re-bakes
(one full config load), and execs the new target. One hiccup, then back to steady state.

**Tool not yet downloaded.** The entry is a shim with `Installed=false`. The shim installs,
re-resolves and execs. Subsequent invocations take the steady-state path.

### Why the hot path holds no trust decision

Hashes are verified at download time, and the store is content-addressed by config hash, so
`exec` of an installed app already does exactly one `os.Stat` today — no hashing, no lockfile
read, no network. `internal/verifycache` is referenced only by `cmd/devtools_verify.go` and is
not on this path. The farm inherits this for free **as long as the recorded path is the same
config-hash-addressed store path**. The manifest stores a location, never a verdict. Do not
later "optimize" by caching a verification result.

### Store GC is now load-bearing and does not exist

The manifest holds absolute store paths, and `cmd/store.go:287` `ForceRemoveAll`s the entire
store path — which would take the farm with it. The store is already 14 GB with no GC. This
plan does not build GC; Task 10's `status` must detect and report a farm whose targets have
vanished, and Task 11's `refresh` must repair it. Designing GC without making it farm-aware
would delete binaries out from under open shells.

## Post-Completion

**Manual verification:**

- Drive a real smug/tmux session profile with several windows and confirm every pane resolves
  the pinned versions, including panes whose `commands:` run a tool directly rather than
  typing into an interactive shell.
- Switch branches in a real infrastructure repository with a genuine terragrunt version bump
  and confirm the switch is invisible.
- Confirm on Linux as well as macOS — symlink and `execve` behavior, and the `.git` layouts,
  differ.

**Deliberately deferred, each to its own plan:**

- **Activation outside a git repository (D3).** Machine-level toolchains — the case where a
  user keeps a personal config outside any project and wants those tools on `PATH` in every
  shell. Config discovery is git-root-only today, so `source` has no root to work from. The
  approach is the existing `--config` persistent flag rather than a new implicit discovery
  layer: explicit is both simpler and the trust boundary. See
  `docs/plans/2026-08-19-global-config-layer.md`.
- **Alias apps** (`yq-json` wrapping `yq eval …`). Genuinely valuable and independently
  shippable through `datamitsu exec yq-json` alone — the mechanics already exist, since
  `GetCommandInfo` prepends `cmdInfo.Args` before user args
  (`internal/binmanager/binmanager.go:809-811`), which _is_ alias semantics. The cost is the
  ~14 duplicated kind-enumeration sites across 11 files, both `config.d.ts` copies, and the
  execution-cache invalidation key. Gate it on an app-kind registry mirroring
  `internal/config/runtimekind.go`.
- **Virtual apps.** A code-distribution feature entangled with source mode by one line. Needs a
  new app kind, a new store layout, a per-runtime dependency-install step, and a relaxation of
  `internal/config/validate.go:44-47`. Separate motivation, separate note.
- **Self apps / `{datamitsu}` targets.** A shell app already does this today at the same cost
  with zero new schema.
- **goja env-read instrumentation** to close the `facts().env` staleness hole soundly, with
  the `sawEnvEnumeration` fallback flag for configs that call `Object.keys(facts().env)`.
- **Store GC**, designed farm-aware from the start.
- **Deactivation**, **PowerShell**, and a **script runner / shebang wrapper**.
