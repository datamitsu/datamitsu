# datamitsu - Configuration Author Guide

This repository writes a datamitsu configuration: a wrapper package other
repositories load, or a project configuration that defines its own apps, tools
or managed configs. The datamitsu binary that ran `datamitsu init` wrote this
guide, so it describes exactly the version this configuration runs on.

## Read the reference for THIS version

Never add a field, a placeholder or a command from memory. If the type
declarations do not have it, this version does not accept it.

- `.datamitsu/datamitsu.config.d.ts` - the typed API; reference it from every
  config file with `/// <reference path="./.datamitsu/datamitsu.config.d.ts" />`
- `datamitsu llms reference/configuration-api` - every field and its validation
- `datamitsu llms guides/configuration` - load order and the JavaScript runtime
- `datamitsu llms guides/managed-configs` - `.datamitsu/`, links, managed files
- `datamitsu llms how-to/maintain-wrapper` - version bumps and verification
- `datamitsu llms contributing/creating-wrappers` - packaging a wrapper

## Writing a layer

- A config runs in goja, not Node.js: no `require`, no `node:*` modules, no
  file system and no network. The globals are listed in `guides/configuration`.
- `getConfig(input)` receives every earlier layer. Spread what you change
  (`...input`, `...input.apps`, `...input.managedConfigs?.[key]`): returning a
  fresh object drops what those layers declared.
- Keep evaluation deterministic. A config that reads the clock, calls
  `Math.random()` or writes to `console.*` still loads, but is never cached, so
  every command evaluates the whole chain again.
- `getMinVersion()` returns the oldest datamitsu that has every field and
  behavior the config relies on. Raise it when you adopt a newer feature.
- Branch on `facts()` and `datamitsuConfigInputs`, not on guesses about the
  machine.

## Downloads

- Everything fetched from the network - binary, archive, JAR, runtime, remote
  config - carries a SHA-256 hash: `hash`, or `jarHash` for a JVM app's JAR. A
  missing hash fails the load. Never write a placeholder, and never copy a
  version or a hash from memory.
- Bump versions with `datamitsu devtools pull-github`, `pull-node`, `pull-uv` and
  `pull-runtimes`. They read hashes from the published artifacts and apply the
  minimum release age.
- Bun, Node, UV and Go apps need a `lockFile`. Generate it with
  `datamitsu config lockfile <app>` whenever the app's version or dependencies
  change; never edit one by hand.
- After changing apps or runtimes, run `datamitsu devtools verify-all`. It
  downloads and hash-checks binary apps and managed runtimes for every
  configured platform, but installs Bun, Node, UV, JVM and Go apps for this
  machine's platform only: a pass here says nothing about their other platforms.

## Apps, tools and managed configs

- App names match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`, are not Windows reserved
  device names, and do not differ from another app's name only by case.
- `dependsOn` puts other native binary apps on an app's `PATH`.
  `${APP_BIN:<name>}` resolves only in `runtimeEnv`, and only for a direct
  dependency. `PATH` is rejected in `env` and `runtimeEnv`.
- To turn a tool off, set `skip: true` with a `skipReason` instead of deleting
  it or omitting it conditionally. A managed config whose `tools` names a missing
  tool fails the load.
- Pass a managed config's path to its tool with `{managedConfig:<key>}`, never a
  literal path: only the placeholder knows whether a project ejected the file.
  Mark a managed config `ejectable` when nothing but its tool reads it.

## Agent instructions for consumers

A wrapper that ships agent instructions to the repositories using it includes
`sharedStorage["datamitsu-agent-prompt"]` in them. This guide,
`sharedStorage["datamitsu-config-author-prompt"]`, is for the repository that
writes the configuration; never ship it to consumers.

## Checking a change

- Run `datamitsu init` after every config edit. Run `datamitsu config reconcile`
  only when the user asks for managed files to be written.
- `datamitsu config show` prints the resolved configuration;
  `datamitsu inspect` browses it.
- `datamitsu check` must pass afterwards.

## Recent changes to review

Newest first. Each entry names the first version that has it and what a
configuration should do about it.

- **after v0.4.0** - `devtools pull-github` accepts a strict top-level `platforms`
  list in its JSON manifest. The list filters available targets; it does not
  require every selected target in every app. It saves removal of unselected binary records
  before pulling, even if a pull fails. Changing or removing the list changes
  `configHash` and reruns detection at the same tag. See
  `datamitsu llms reference/cli-commands` for supported identifiers.

- **after v0.3.1** - `lint --baseline` and `check --baseline` gate only on the
  findings a baseline does not hold, but never override a tool's exit code
  (`datamitsu llms guides/reports`). Nothing changes for a configuration that
  does not need baselines. One meant for a project that adopts them lets a
  linter exit 0 on its findings — its exit-zero argument in the operation's
  `args` — and gates with the operation's `failOn`, where the tool's parser
  module declares the severity contract.
- **after v0.3.1** - Output no declared parser recognized is read by the
  fallback built into datamitsu, which tries the standard formats
  (`datamitsu llms guides/architecture/parsers`). A tool without an
  `outputParser` that prints SARIF, Checkstyle, compiler lines and the like now
  has findings: they block its cached passes, and an `error` one fails a run the
  tool itself passed. Declare the format key for such a tool
  (`datamitsu devtools parsers sniff <captured output>` names it), or its own
  parser where the module has one; a declared parser that recognizes nothing is
  now `parse-failed` — for a JSON or XML parser, also when the tool exited 0
  but printed no document of it, so make sure the tool prints the format its
  parser reads. Every cache is cold once.
- **after v0.3.1** - The parser module name `embedded` is reserved for the
  fallback built into datamitsu: a `parsers` entry named `embedded`, or an
  `outputParser.module` naming it, fails the load
  (`datamitsu llms reference/configuration-api`). Rename such an entry and the
  tools that name it.
- **after v0.3.1** - `outputParser.parser` accepts a format key — `sarif`,
  `codeclimate`, `eslint-json`, `json`, `checkstyle-xml`, `junit-xml`,
  `github-annotations`, `azure-logissue`, `msvc`, `gcc` — for a tool without a
  parser of its own that prints a standard format on request
  (`datamitsu llms reference/configuration-api`). Wire such a tool by passing
  its format flag and naming the format —
  `ruff check --output-format sarif` as `sarif`,
  `shellcheck -f checkstyle` as `checkstyle-xml`,
  `typos --format brief` as `gcc` — and keep a tool's own parser where the
  module has one, since its format is richer. The keys come
  with the parser module released with this core (descriptor schema 3): bump
  the `parsers` pin and check it with `datamitsu devtools parsers list`, which
  then lists the format keys.
- **after v0.3.1** - `facts().ci` says which CI runs the job: `vendor`
  (`github`, `gitlab`, `azure`, `teamcity`, … or `generic`), `isCI`, `isPR`,
  `sha`, `ref`, `baseRef` and `prNumber`
  (`datamitsu llms reference/configuration-api`). Branch on
  `facts().ci.isCI` instead of `facts().env.CI`, which Azure Pipelines and
  TeamCity do not set. A wrapper that forks `config.d.ts` declares `ci` on
  `Facts` too.
- **after v0.3.1** - A tool operation takes `failOn` (`error` by default,
  `warning`, `info`, `hint`), the lowest level of parsed finding that fails the
  run on top of the tool's exit code; `--fail-on`/`DATAMITSU_FAIL_ON` raise it
  for one run (`datamitsu llms reference/configuration-api`). With the default,
  a tool that exits 0 on a finding its parser reads as an error now fails the
  run (semgrep without `--error`, trivy without `--exit-code`): pass the tool's
  own gate flag, or accept the failure. Set `failOn` only where a stricter gate
  than the tool's own is wanted. A parser module older than descriptor schema 2
  gates nothing, and a run warns once when a threshold was asked for. The
  terminal shows findings at or above `failOn` and counts the rest. Every cache
  is cold once.
- **after v0.3.1** - A finding a tool printed no level for is an error when the
  tool failed and a warning when it passed (it was always a warning). The parser
  module released with this core (descriptor schema 2) sets levels only from
  what a tool printed, reports golangci-lint's linter as the rule, and adds rule
  URLs (`datamitsu llms guides/architecture/parsers`). Bump the `parsers` pin to
  it and check the pin with `datamitsu devtools parsers list`: a module that has
  the contract shows a `levels` line per tool.

- **after v0.3.1** - Tools run by `fix`, `lint` and `check` no longer see
  `GITHUB_ACTIONS`, AI agent markers or `FORCE_COLOR`, and always get
  `NO_COLOR=1`; `CI` still passes through and `exec` changes nothing
  (`datamitsu llms reference/tool-environment`). A tool that needs one of them
  names it in `inheritEnv`; an `env: { GITHUB_ACTIONS: "false" }` kept only to
  stop annotations can go. Every execution cache is cold once.
- **after v0.3.1** - A tool operation can name host variables in `inheritEnv`;
  the tool gets the host's value, and the value is part of the operation's cache
  identity (`datamitsu llms reference/configuration-api`). `NO_COLOR` is
  rejected in an operation's `env` and in `inheritEnv`: drop it from any `env`.
- **after v0.3.1** - A lint pass is cached only where the tool's
  `outputParser` ran and reported nothing, at any level: files with findings of
  a tool that exits 0 on them run again every time. A `parser` key its module
  does not list, or a module that cannot be fetched, now warns once per run and
  leaves the tool's output unparsed and uncached; check keys against
  `datamitsu devtools parsers list`. `--no-parse` only changes what a failure
  shows. Every execution cache is cold once. A bump of a tool whose output is
  parsed records the tool's fixture pair again first
  (`datamitsu llms how-to/maintain-wrapper`).
- **after v0.3.1** - Usage errors exit 2 and `--fail-on-skip` exits 4, both
  once 1; a failed tool still exits 1. `fix`, `lint` and `check` take
  `--fail-fast=false` (`DATAMITSU_FAIL_FAST=false`) to run every tool to the
  end. A CI step or script of the configuration's repository that expects 1
  from a mistaken invocation or from `--fail-on-skip` expects 2 or 4 instead;
  CI runs `datamitsu lint --fail-fast=false`.
- **after v0.3.1** - Every `devtools pull-*` command that writes its file saves
  it after each entry that changed, so a failed or interrupted run keeps what it
  already pulled; `pull-runtimes` no longer discards the runtimes that succeeded
  when one fails, and pulls pnpm first. The JVM runtime takes the previous
  Temurin feature release while the newest has no build past the minimum
  release age, and a GitHub release lookup reads past a first page of
  prereleases. Commit the successful part of a failed pull and rerun the rest.
- **after v0.3.1** - Every `devtools pull-*` command works through its entries in
  alphabetical order with a `[n/N]` counter and writes its file with the keys
  of every object sorted. The first pull after upgrading rewrites an existing
  registry in that order; commit that diff on its own.
- **after v0.3.1** - Every `devtools pull-*` command retries transient failures,
  prints each retry, reports every app or package that still failed and exits
  with status 1 when any did. `pull-github` no longer stops at a brand-new app
  without an old-enough release; it records the failure and goes on. Under
  `--verify-extraction` a platform whose asset cannot be downloaded fails the
  app instead of being dropped or handed to the next asset, and signature,
  certificate, provenance and SBOM files are never candidates. Let a CI job
  fail on the exit code instead of grepping the log, run large pulls with
  `GITHUB_TOKEN` set, and rerun a failed pull: every `pull-*` command keeps a
  failed entry's previous state.
- **after v0.3.1** - `extractDir: true` on a binary app runs `binaryPath` inside
  the extracted directory instead of failing on the directory itself, and
  requires `binaryPath` and a tar or zip `contentType`; `devtools verify-all`
  checks that path is an executable. Set it for a tool that reads files beside
  its binary, such as protoc and its `include/`, after loading the registry,
  together with the exact `binaryPath` (`bin/protoc`): `pull-github` writes
  neither the flag nor a path it could only guess, and a guessed path that
  passes single-file verification fails a directory install. See
  `datamitsu llms guides/binary-management`.
- **after v0.3.1** - `devtools verify-all` and `pull-github --verify-extraction`
  fail when a `binaryPath` extracts something other than an executable, such as
  a completion script. `pull-github` keeps a `binaryPath` fixed by hand when the
  asset's name does not change, derives the next one from the entry of the same
  os/arch/libc, and detects builds named only `alpine`, `win64`, `win` or
  `.exe`; it never records an illumos, Solaris, NetBSD or Android build for
  Linux, skips installers (`*-setup.exe`, `.msix`, `.dmg`) and prefers the
  asset named after the app when a release holds several programs. Point any
  failing `binaryPath` at the real binary; drop hand-added or hand-corrected
  entries that the next `pull-github` now detects.
- **after v0.3.1** - `datamitsu config lockfile` resolves transitive
  dependencies within the minimum release age: uv records the window in the
  lock, and a Go app fails on a module younger than it. Existing locks still
  install; regenerate them to bring their dependencies under the window.
- **after v0.3.1** - Managed configs can stay out of the repository. Mark the
  ones only their tool reads `ejectable`, pass their path with
  `{managedConfig:<key>}`, and leave `ejectConfigs` to the projects.
- **after v0.3.1** - A configuration names itself with the top-level `name`, and
  an app can declare `officialUrl`. Set `name` in a wrapper; set `officialUrl`
  only where the derived link is missing or wrong.
- **after v0.3.1** - Every native binary app in an app's `dependsOn` closure is
  on its `PATH` under its app name. An app that looks its dependency up on
  `PATH` no longer needs a `runtimeEnv` binding for it.
- **v0.3.0** - An app that runs another app declares it in `dependsOn`, instead
  of `required: true`, hand-written `--apps` lists or searching the store for
  the binary. `runtimeEnv` holds execution-only variables.
- **v0.3.0** - pnpm is a runtime of its own. A Node or Bun runtime names it with
  `pnpmRuntime`; regenerate runtimes with `datamitsu devtools pull-runtimes`.
- **v0.3.0** - `setup` is gone; managed files are written by
  `datamitsu config reconcile`.
