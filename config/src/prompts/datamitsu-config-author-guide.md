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
