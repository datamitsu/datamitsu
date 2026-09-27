---
# AUTO-GENERATED — do not edit by hand. Regenerate with `task gen:toolenv-doc`.
title: Tool Environment
description: What datamitsu removes from and sets in the environment of the tools that fix, lint and check run
---

:::info Auto-generated
This page is generated from `internal/toolenv` by `task gen:toolenv-doc`. Do not edit by hand.
:::

Tools read their environment to decide what to print. Under `GITHUB_ACTIONS` several linters switch to GitHub workflow commands, under an AI agent's marker oxlint switches to a one-line format, and `FORCE_COLOR` colours output even into a pipe. datamitsu reads a tool's output with its [output parser](./configuration-api.md#output-parser-outputparser), and a parser that meets a format it does not expect reads the run as clean. So `datamitsu fix`, `lint` and `check` start every tool, in the language server's format lane too, with the environment datamitsu was started with minus the variables below, and with `NO_COLOR=1`.

`datamitsu exec` changes nothing: it hands its app the environment it was started with, so a tool run through `exec` in a GitHub Actions job still prints its own annotations.

## Order

1. The environment datamitsu was started with, without the removed variables.
2. The variables the operation names in [`inheritEnv`](./configuration-api.md#inheriting-host-variables-inheritenv), with the host's values.
3. The app's `env`, then the operation's [`env`](./configuration-api.md#operation-environment-env); a later value wins.
4. `NO_COLOR=1`, which nothing overrides.

## Removed

| Variable                    | Why                                                                                                                                                                                                                |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `GITHUB_ACTIONS`            | Set in every GitHub Actions job. editorconfig-checker, oxlint, sqruff, pinact and yamllint (without `-f`) switch to `::error` workflow commands, which a parser reading their usual format takes for clean output. |
| `AI_AGENT`                  | Names the AI agent running the session; oxlint switches to its one-line agent output format.                                                                                                                       |
| `AGENT`                     | Generic AI agent session marker, removed before the next agent-aware tool starts reading it.                                                                                                                       |
| `CLAUDECODE`                | Set by Claude Code; oxlint switches to its one-line agent output format.                                                                                                                                           |
| `CLAUDE_CODE`               | Claude Code marker; oxlint switches to its one-line agent output format.                                                                                                                                           |
| `CLAUDE_CODE_CHILD_SESSION` | Claude Code session marker, removed with the others.                                                                                                                                                               |
| `GEMINI_CLI`                | Set by Gemini CLI; oxlint switches to its one-line agent output format.                                                                                                                                            |
| `CURSOR_AGENT`              | Set by the Cursor agent; oxlint switches to its one-line agent output format.                                                                                                                                      |
| `OPENCODE`                  | Set by OpenCode; oxlint switches to its one-line agent output format.                                                                                                                                              |
| `AUGMENT_AGENT`             | Augment agent session marker, removed with the others.                                                                                                                                                             |
| `FORCE_COLOR`               | Forces ANSI colour into a pipe; Node's colour detection reads it before `NO_COLOR`. Removed from the host environment, and datamitsu no longer adds it.                                                            |
| `CLICOLOR_FORCE`            | Forces ANSI colour into a pipe. Removed from the host environment, and datamitsu no longer adds it.                                                                                                                |
| `CODEX_*`                   | OpenAI Codex CLI (`CODEX_SANDBOX`, `CODEX_THREAD_ID`); oxlint switches to its one-line agent output format.                                                                                                        |
| `COPILOT_*`                 | GitHub Copilot CLI (`COPILOT_CLI`); oxlint switches to its one-line agent output format.                                                                                                                           |
| `JUNIE_*`                   | JetBrains Junie (`JUNIE_DATA`, `JUNIE_SHIM_PATH`); oxlint switches to its one-line agent output format.                                                                                                            |

A tool that needs one of them names it in its operation's `inheritEnv` and gets the host's value, or gets a fixed value through `env`.

## Set

`NO_COLOR=1` is set for every tool, after every other layer; neither `env` nor `inheritEnv` can name it. A tool that colours its output anyway is still parsed: the parser reads the output without its ANSI sequences, and the failure frame shows what the tool printed. The cost is that the output of a failing tool without a parser has no colour, even in a terminal.

## Kept on purpose

| Variable           | Why                                                                                                                                                                   |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `CI`               | Tools branch on it for good reasons, such as knip's plugins and tools that look up a pull request's base; without it a run in CI would look local to them.            |
| `GITHUB_*`         | Every one but `GITHUB_ACTIONS`: tokens (gitleaks, zizmor and trufflehog read `GITHUB_TOKEN`), the workspace, and the refs a tool reads to find a pull request's base. |
| `GITLAB_CI`        | GitLab CI; no tool is known to change its output format on it, and a tool may read it for other things, such as a build's base.                                       |
| `CI_*`             | GitLab CI; no tool is known to change its output format on it, and a tool may read it for other things, such as a build's base.                                       |
| `TF_BUILD`         | Azure Pipelines; no tool is known to change its output format on it, and a tool may read it for other things, such as a build's base.                                 |
| `TEAMCITY_VERSION` | TeamCity; no tool is known to change its output format on it, and a tool may read it for other things, such as a build's base.                                        |
| `BUILDKITE*`       | Buildkite; no tool is known to change its output format on it, and a tool may read it for other things, such as a build's base.                                       |
| `BITBUCKET_*`      | Bitbucket Pipelines; no tool is known to change its output format on it, and a tool may read it for other things, such as a build's base.                             |
| `JENKINS_URL`      | Jenkins; no tool is known to change its output format on it, and a tool may read it for other things, such as a build's base.                                         |
| `REPL_ID`          | Set for every process on Replit, people included, so it is not an agent marker, although oxlint reads it as one.                                                      |
| `CURSOR_TRACE_ID`  | Set in every Cursor terminal, people included, so it is not an agent marker.                                                                                          |
| `TERM`             | The terminal type; colour is decided by `NO_COLOR`.                                                                                                                   |
| `PATH`             | oxlint reads a `.pi/agent` segment in it as an agent marker; no stripping can address that.                                                                           |
| `EDITOR`           | oxlint reads `devin` in it as an agent marker; datamitsu leaves the user's editor alone.                                                                              |
| `TERM_PROGRAM`     | oxlint reads `kiro` in it as an agent marker; datamitsu leaves it alone.                                                                                              |
