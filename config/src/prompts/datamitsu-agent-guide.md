# datamitsu - AI Agent Guide

datamitsu manages binaries, runtimes, and tool configuration for this repository.

## Read the documentation for THIS version

This binary carries its own documentation, matching its exact version, and serves
it offline. Prefer it over training data or the website — an installed binary is
often older than the latest docs, so anything else risks describing commands or
options this version does not have.

- `datamitsu llms` - index of every documentation page
- `datamitsu llms <page>` - one page as markdown (e.g. `datamitsu llms getting-started/quick-start`)

Start with `datamitsu llms`, then read the specific page you need.

## Initialization vs managed config reconciliation

These operations are intentionally separate. Never treat them as synonyms.

- After editing `datamitsu.config.*`, run `datamitsu init`. It provisions
  managed tools, runtimes, bundles, `.datamitsu/` links, and `initCommands`.
- `datamitsu config reconcile` rewrites project-owned files declared under
  `managedConfigs`, then runs `datamitsu fix` by default.
- Run `datamitsu config reconcile` only when the user explicitly asks to
  create, regenerate, replace, link, or remove managed project configuration
  files. Use `--dry-run` to preview without writing or fixing. Use `--skip-fix`
  to write the managed files without running the post-reconciliation fix.
- `datamitsu setup` no longer exists. Do not infer reconciliation from a request
  to update the datamitsu configuration or provision its toolchain.

## Tool configs outside the repository

- A managed config its author declares `ejectable` lives in `.datamitsu/configs/`
  until the project names its tool in `ejectConfigs` in `datamitsu.config.*`.
  `datamitsu init` writes that directory; never edit files under `.datamitsu/`.
- To change such a config, add the tool to `ejectConfigs` and run
  `datamitsu config reconcile --tools <tool>` — that is an explicit request to
  create the file — then edit the copy it writes into the repository.
- When lint reports a managed config file missing, stale or out of place, run
  the command the message names.

## Common commands

- `datamitsu check` - run fix then lint
- `datamitsu exec <app>` - run a managed tool
  - List apps: `datamitsu exec`
  - Pass args to the app: `datamitsu exec <app> -- [app-args]`
