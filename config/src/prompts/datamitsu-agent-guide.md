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

## Common commands

- `datamitsu check` - run fix then lint
- `datamitsu exec <app>` - run a managed tool
  - List apps: `datamitsu exec`
  - Pass args to the app: `datamitsu exec <app> -- [app-args]`
