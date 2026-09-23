# Plan: Keep tool configs out of the repository until a project ejects them

**Status:** implemented in the core; the wrapper's adoption is prepared on its own branch and waits
for an unstable core release. See "Implementation notes" for where the build departs from the first
design.
**Date:** 2026-09-23.
**Related:** `internal/install`, `internal/managedconfig`, `internal/config` (`managed_config_*`,
`validate.go`), `cmd/config_loader.go`, `cmd/config_reconcile.go`, `cmd/init.go`,
`internal/tooling` (executor, verdict), `internal/cache`, `internal/configcache`, `internal/lsp`,
`internal/inspector`. Downstream: the `@shibanet0/datamitsu-config` wrapper.

## Problem

Every `managedConfigs` entry is written into the repository by `datamitsu config reconcile`. A
consumer of the shared wrapper config ends up with about forty files at its root. Most of them exist
only because a tool can read them, and nobody in the project ever opens them: `.gitleaks.toml`,
`.yamlfmt.yaml`, `hadolint.yaml`, `.trufflehog-exclude-paths.txt` and so on.

A file belongs in the repository when something other than datamitsu needs it there (an editor, a
platform, another tool that discovers it by name) or when the project wants to change it. Neither
is true by default for a large share of them.

## Model

- A config author declares that an entry **may** live outside the repository:
  `ManagedConfig.ejectable: true`. Without the flag nothing changes: the file is always written to
  the repository. ESLint, oxlint, `.editorconfig` and `lefthook.yaml` stay that way.
- A project asks for a tool's configs in the repository by naming the tool:
  `ejectConfigs: ["gitleaks"]`.
- A non-ejected ejectable entry is rendered into `.datamitsu/configs/<key>`. `datamitsu init` owns
  that directory: it writes the desired files and removes stale ones. Bundles and app links in
  `.datamitsu/` are unchanged.
- An ejected entry is written to the repository by `datamitsu config reconcile` only, exactly as
  today. Removing the tool from `ejectConfigs` makes reconcile delete the repository copy and init
  render the internal one again.
- A tool refers to its config through `{managedConfig:<key>}`. The core resolves it after the whole
  config chain is known, so a base layer never has to know what a later layer ejects.

A tool or wrapper upgrade that changes a non-ejected config needs `init`, never `reconcile`.

## Contract

| Surface                                   | Rule                                                                                                                                                                                                                                                                                                         |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `ManagedConfig.ejectable?: boolean`       | Requires a `content` function, no `linkTarget`, no `deleteOnly`, a non-empty `tools`, and `scope: "git-root"` (v1).                                                                                                                                                                                          |
| `Config.ejectConfigs?: string[]`          | Tool names. Each must be a configured tool and must own at least one ejectable entry. Duplicates are an error.                                                                                                                                                                                               |
| `ManagedConfig.tools`                     | An unknown tool name is a load error (it was a warning). `skip: true` replaces conditionally deleting a tool.                                                                                                                                                                                                |
| `{managedConfig:<key>}` in `args` / `env` | Resolved in the config loader after the chain, before validation and before the config-evaluation cache is written. Becomes `{root}/<key>` (repository; `{cwd}/<key>` for a project-scoped entry) or `{root}/.datamitsu/configs/<key>`. The key must exist, and the operation's tool must be in its `tools`. |
| `content()` context                       | Gains `placement` (`"repo"` / `"internal"`), `outputPath` and `outputDir` (absolute, like `rootPath`), and `datamitsuDirFromOutput`. `datamitsuDir` keeps its meaning (relative to `cwdPath`).                                                                                                               |

Two path conventions exist in the wild and a context has to serve both. JavaScript configs import
relative to the file (`datamitsuDirFromOutput`). gitleaks 8.30.1 resolves `extend.path` relative to
the process working directory, not the config file — verified: `../gitleaks-managed.toml` from
`.datamitsu/configs/` fails, `.datamitsu/gitleaks-managed.toml` works. The author picks.

An ejectable entry is rendered with an empty project context (`projectTypes`, `projectLocations`)
in both placements, so the internal copy is by construction what an eject would write from scratch.

## Design decisions

### Rendering happens in the loader; init writes

The loader keeps each layer's managed configs and the engine that owns their callables. Once the
chain is evaluated and the eject set is known, it replays the ordered layers for every non-ejected
ejectable entry with the internal context, threading `existingContent` exactly like the eager pass.
The rendered text and its XXH3 are stored in the resolved config (`ManagedConfig.Render`, hidden
from the config JSON but kept by the config-evaluation cache).

- A cache hit needs no JavaScript to know what init writes or whether the file on disk is current.
- `lint` checks freshness by hashing the file against `Render.Hash`. No stamp file is needed, and
  the config-evaluation key could not have served as one: it includes `.git/HEAD` and the whole
  environment, so it moves on every commit.
- A failing render of an ejectable entry is fatal. The eager pass swallows errors; that is not
  acceptable when a missing render could lead to deleting a file.

### `.datamitsu/configs/` survives the `.datamitsu/` rebuild

`CreateDatamitsuLinks` rebuilds `.datamitsu/` in a temporary directory and swaps it in, from `init`
and from `exec` of a lazy link-app. `exec` has no renders to restore, so the rebuild copies the
`configs/` subtree into the tree it swaps in, and the writer replaces files one by one rather than
the directory, so a concurrent reader never finds it missing. Writers of `.datamitsu/` share a
lock. The name `configs` is reserved for
links, and keys are validated against traversal and collisions.

### Placement is part of the cache identity

`{managedConfig:}` is resolved before anything hashes the args, so switching placement changes
`verdictIdentity` and the per-file invalidation key. Each operation also records the managed configs
it reads (`ToolOperation.ManagedConfigRefs`, key and path). Their paths join the verdict guards,
which otherwise only see a standalone absolute argument, never `--config=PATH` or an env value. For
per-file entries the tool is recorded as `tool@<digest of its configs>` rather than folding the
digest into the cache-wide key, so a config edit re-runs that tool on every file and leaves every
other tool's entries alone.

### Deletion only when nothing is lost

When an entry stops being ejected, reconcile deletes the repository copy only if it holds nothing
the configuration would not render again: it is byte-for-byte the pristine repository render (the
whole chain, no `originalContent`), or re-rendering it from its own content — what reconcile would
write for it — gives that pristine render. Anything else fails with two ways out: add the tool to
`ejectConfigs`, or delete the file by hand. An ejectable entry's `otherFileNameList` files must match
byte for byte. The whole deletion plan is validated before the first write. `expectChainHash` is
verified for repository placement only.

### Preflight before any cache hit

`lint`, `fix`, `check` and the language server check, for every planned task that reads a managed
config:

- internal file missing or stale → run `datamitsu init`;
- ejected but absent from the repository → run `datamitsu config reconcile --tools <tool>`;
- not ejected but present in the repository → error: the tool would silently ignore it. This is the
  migration case of a consumer whose customized `.gitleaks.toml` predates the wrapper marking
  gitleaks ejectable.

Only planned tasks are checked, so an entry that is inapplicable for the project's types reports a
configuration error rather than a request to run init.

### Boundaries

Only tool operations receive the resolved path. `datamitsu exec <app>`, source-mode shims, CI
actions and editor extensions that run a tool directly do not, which is why every config an editor
reads by name stays repository-resident. Docker app slices are unaffected: they carry apps and
runtimes, not workspace configs.

## Implementation notes

What running the wrapper through the core changed:

- **Formatters defeat byte equality.** Reconcile's post-fix runs tombi and yamlfmt over the files it
  just wrote, so the wrapper's own untouched `.gitleaks.toml` and `.dclint.yaml` differed from their
  pristine renders in quote style and indentation. A byte-only check reported both as customized;
  the re-render test above does not, and still flags the files that carry real rules.
- **Applicability must not strand a file.** `droast.toml` is git-root scoped with
  `projectTypes: ["docker-project"]`; a repository whose Docker project sits in `docker/` never
  reconciles it at the root, yet init renders the internal copy and the preflight rejects the
  repository copy. An internal entry's repository copy is therefore removed whatever the project
  types.
- **The check earned its keep on the first real run.** The wrapper's own `droast.toml` carries
  `[[overrides]]` for its Dockerfiles that no generator produces. Reconcile refused; the fix was to
  eject droast in the wrapper's root config, alongside alint, ls-lint and yamlfmt.
- **Path conventions differ per tool** (measured on the pinned binaries): alint resolves a local
  `extends` against the config's directory, so its generator builds the path from
  `datamitsuDirFromOutput`; gitleaks resolves `extend.path` against the working directory and keeps
  `.datamitsu/…`; vale's `StylesPath` follows the config file, which the generated config does not
  set.

## Phases

1. Schema and validation: `ejectable`, `ejectConfigs`, fatal unknown tool references, placeholder
   parsing and resolution. Placement is always "repo" until phase 2, so behaviour is unchanged and
   the wrapper can switch its args early.
2. Rendering in the loader: layer replay, context fields, fatal render errors, cache format bump.
3. Materialization: `.datamitsu/configs/`, stamp, lock, carry-over through link rebuilds, init.
4. Reconcile (write, pristine deletion, migration refusal), preflight, cache inputs, language server.
5. Introspection (`config show`, `--explain`, inspector manifest, init/reconcile output), CLI
   goldens, documentation.

## Wrapper rollout (`@shibanet0/datamitsu-config`)

- **Wave 0, independent of the core.** Stop deleting `.sqruffignore` (it is sqruff's ignore file,
  not an alternate config name); give editorconfig-checker's `-config` a `{root}` prefix; pass an
  explicit config path to alint, zizmor, pinact, sqruff and mdsf; align oxlint/prettier managed
  config `projectTypes` with their tools.
- **Wave 1 (done).** Every config path in `tools.ts` goes through `{managedConfig:}`. Ejectable:
  gitleaks, hadolint, dclint, droast, yamllint, yamlfmt, `.editorconfig-checker.json`,
  `.trufflehog-exclude-paths.txt`, ls-lint, vale, mdsf, sqruff, alint. `getMinVersion()` stays at
  `0.3.1` until an unstable core carrying this ships; raise it then, since an older core passes
  `{managedConfig:…}` to the tool verbatim.
- **Deferred.** syncpack, pinact and zizmor: people run them by hand through `datamitsu exec`, which
  does not receive the path, and each silently falls back to defaults without a config.
- **Not ejectable.** tombi (1.5.0 has no config flag or variable), oxfmt, ESLint, oxlint, Prettier,
  Stylelint, cspell, knip, commitlint, golangci-lint, rustfmt, ty, cargo-deny, and every file an
  editor, platform or package manager reads by name.

## Verification

Through `task dev:link` against the wrapper, on scratch copies of a real repository:

- fresh clone → init → lint uses the internal configs;
- eject: lint fails with the reconcile hint, reconcile writes the file, init drops the internal copy;
- an ejected file's customization survives reconcile;
- un-eject a customized file (refused) and a pristine one (deleted, internal copy restored by init);
- consumer migration with a customized `.gitleaks.toml`;
- wrapper upgrade without init reports stale internal configs;
- `datamitsu exec` of a lazy link-app keeps `.datamitsu/configs/`;
- every validation error; `reconcile --tools`; cache hit and miss resolve identically.
