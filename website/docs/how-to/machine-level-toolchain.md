---
title: Machine-Level Toolchain
description: Activate a hash-verified, pinned toolchain from a config outside any repository, so the tools you want on your machine are on PATH in every shell
---

# Machine-Level Toolchain

[Source mode](../guides/source-mode.md) puts a **project's** declared toolchain on `PATH`. This
page covers the other half: a toolchain that belongs to **your machine** rather than to one
repository — the tier a dotfiles setup usually installs with `curl | sh`, `eget`, `go install`
or an npm global.

```bash
# in ~/.config/fish/config.fish
datamitsu source fish --config ~/.config/datamitsu/datamitsu.config.ts | source
```

Nothing about it is a new discovery rule: `--config` is the ordinary global flag, and naming
the file is the whole mechanism. The tools that config declares are then on `PATH` in every
shell, pinned and hash-verified, downloaded on first use.

## Write the config

The config is an ordinary datamitsu config file. It does not need to be inside a repository,
and datamitsu will never find it on its own — you name it.

```javascript title="~/.config/datamitsu/datamitsu.config.js"
globalThis.getMinVersion = () => "0.0.1";

globalThis.getConfig = (config) => ({
  ...config,
  apps: {
    ...config.apps,
    jq: {
      binary: {
        binaries: {
          darwin: {
            arm64: {
              unknown: {
                url: "https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-macos-arm64",
                hash: "<sha256 of that file>",
                contentType: "binary",
              },
            },
          },
          linux: {
            amd64: {
              glibc: {
                url: "https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-linux-amd64",
                hash: "<sha256 of that file>",
                contentType: "binary",
              },
            },
          },
        },
      },
    },
  },
});
```

Every platform you actually use needs an entry, and every entry needs a `hash` — a real 64-character
lowercase SHA-256 hex string, not the placeholder above. That is not a lint rule you can turn off:
a config with a missing or malformed hash fails to load. See
[Hash verification is the point](#hash-verification-is-the-point) below.

Writing those hashes by hand is not the intended workflow.
[`datamitsu devtools pull-github`](./maintain-wrapper.md#binary-apps-devtools-pull-github)
resolves the latest release, downloads every platform tuple and computes the SHA-256 hashes for
you; the same `devtools pull-node` / `pull-uv` commands do it for npm and PyPI tools, which are
declared as [runtime-managed apps](../guides/runtime-management.md) with a lock file rather than
a URL.

## Activate it from a shell rc file

```bash title="~/.bashrc / ~/.zshrc"
eval "$(datamitsu source bash --config ~/.config/datamitsu/datamitsu.config.js)"
```

```fish title="~/.config/fish/config.fish"
datamitsu source fish --config ~/.config/datamitsu/datamitsu.config.js | source
```

Activation downloads nothing, and costs a manifest read when the farm is already current — the
same contract project activation has, for the same reason: an rc file runs in every shell and
every tmux pane. A tool is fetched the first time it is actually run.

The emitted code exports the farm directory and puts it first on `PATH`:

```console
$ datamitsu source fish --config ~/.config/datamitsu/datamitsu.config.js
set -e -g DATAMITSU_ROOT
set -gx DATAMITSU_FARM '/home/you/.cache/datamitsu/cache/configs/8d1f…/bin'
set -gx DATAMITSU_FARM_CONFIG '/home/you/.config/datamitsu/datamitsu.config.js'
fish_add_path --global --move --path '/home/you/.cache/datamitsu/cache/configs/8d1f…/bin'
```

`DATAMITSU_ROOT` is deliberately **unset** rather than set to an empty value: this farm has no
git root, and an empty one would read as "the repository could not be determined". Unsetting it
also clears whatever a project activation left behind in the same shell.
`DATAMITSU_FARM_CONFIG` records the config chain it was baked from, joined like `PATH`. Both are
informational — no tool resolves through them.

The farm itself lives in its own namespace, keyed by the resolved config chain rather than by a
root:

```text
~/.cache/datamitsu/cache/projects/{XXH3-128(gitRoot)}/     ← project farms
~/.cache/datamitsu/cache/configs/{XXH3-128(config chain)}/ ← machine-level farms
```

The chain is resolved to absolute paths with symlinks followed before it is hashed, so
`--config ./datamitsu.config.js` and `--config ~/.config/datamitsu/datamitsu.config.js` from that
directory are one farm, not two. Order matters, because the chain is a merge order: naming two
configs in the other order is a different toolchain and therefore a different farm.

An explicit-config activation never merges a discovered config, even when you run it from inside
a repository. The chain is exactly what you named.

## How it layers with project activation

Both can be active at once. The mechanism is `PATH` order and nothing else:

```text
{cache}/cache/projects/{hash}/bin   ← added by `datamitsu source` inside a repository
{cache}/cache/configs/{hash}/bin    ← added by `datamitsu source --config …`
…the rest of your PATH…
```

If a repository pins `jq` and your machine-level config also declares it, the project's pin wins,
because the project farm sits ahead on `PATH`. There is no merge, no precedence table and no
resolution logic to get wrong — `echo $PATH` shows you the whole answer.

A tool that only the machine-level config declares stays reachable from inside the project: the
project farm does not answer for that name, and `PATH` continues to the config farm.

Order follows activation order in your rc file, so activate the machine-level config first and
let per-project activation prepend itself later:

```bash title="~/.bashrc"
eval "$(datamitsu source bash --config ~/.config/datamitsu/datamitsu.config.js)"
# then, in a project shell, deliberately:
#   eval "$(datamitsu source bash)"
```

## The trust boundary

**A machine-level farm never evaluates a project's config.** A shell activated against
`~/.config/datamitsu/datamitsu.config.js` that `cd`s into a clone you have not read keeps running
the machine-level tools. It does not start resolving that repository's toolchain, and it does not
run that repository's JavaScript.

This is a security property, not a missing feature. A config is evaluated JavaScript. Making a
tool name enough to trigger that evaluation would mean any repository you happen to `cd` into
runs code the moment you type a common command. Getting into a repository's toolchain stays a
deliberate act:

```bash
cd ~/src/some-clone
eval "$(datamitsu source bash)"    # you decided to evaluate this repository's config
```

The two farm origins differ in exactly this, and in nothing else:

```text
origin: git-root           shim re-resolves from cwd's git root   (branch switching)
                           watch = config chain + .git/HEAD

origin: explicit-config    shim revalidates its own recorded chain, never walks up for .git
                           watch = config chain
```

So branch switching still works normally inside a project you _have_ activated, with the
machine-level farm sitting underneath, and a machine-level tool run from inside any repository
is still the version your own config pins.

The same [exclusions](../guides/source-mode.md#what-is-deliberately-excluded) apply here — `sudo`,
the shells, `git`, `ssh`, `datamitsu` itself and every `shell`-kind app are refused, with the
reason listed in `source status`. Shadow reporting matters more for a machine-level farm than for
a project one, because this farm is first on `PATH` in every shell: the list of system binaries it
takes over is the thing you most need to be able to read.

## Inspect it

`status` and `refresh` take the same `--config` flag, and report which origin the farm has:

```console
$ datamitsu source status --config ~/.config/datamitsu/datamitsu.config.js
origin:   explicit-config
config:   /home/you/.config/datamitsu/datamitsu.config.js
farm:     /home/you/.cache/datamitsu/cache/configs/8d1f…/bin
manifest: /home/you/.cache/datamitsu/cache/configs/8d1f…/manifest.json (fresh)

entries (2):
  jq          shim  installed
  shellcheck  shim  not installed

excluded (1):
  sudo  name is on the source-mode deny-list

shadowed (1):
  jq  /opt/homebrew/bin/jq
```

`--json` emits the same information as one document, with `origin` and `configPaths` fields.
`status` never downloads and never re-bakes, so it can describe a broken farm instead of quietly
repairing it.

Editing the config is enough: the next tool invocation notices the file changed and re-bakes
itself, exactly as a branch switch does for a project farm. `source refresh --config <path>` is
the repair command for the case the watch set cannot see — a config that branches on an
environment variable — and `--force` re-bakes unconditionally.

A name your config declares but that cannot be resolved **exits 127** naming the farm and the
`--config` invocation to repair it. It never falls through to whatever the system has under that
name, because exiting 0 with the wrong binary is worse than not running at all.

## Hash verification is the point

Every artifact datamitsu downloads carries a mandatory SHA-256, verified before it is used. A
missing hash is a configuration error, not a warning, and there is no hash-less fallback mode.
Lock files are likewise mandatory for npm- and PyPI-sourced tools.

That is the actual difference this makes. `curl https://example.com/install.sh | sh` verifies
nothing, pins nothing, and re-downloads whatever is at that URL today; `go install tool@latest`
resolves whatever `latest` means at that moment. Moving a tool into a machine-level datamitsu
config replaces that with a pinned version and a hash you can review in a diff — and, because
the config is a file, with a record of what your machine actually has on it.

See [Supply Chain Security](../guides/supply-chain-security.md) for what is verified where, and
for the minimum-release-age filter that keeps `pull-*` from adopting a release published minutes
ago.

## Migrating from install scripts

The honest scope: datamitsu can take over the tier of **user-space CLI binaries** — the things
that end up in `~/.local/bin` or `/usr/local/bin` by unverified download. That tier is exactly
where hash verification is usually absent.

| Move it to datamitsu                            | Leave it where it is                                    |
| ----------------------------------------------- | ------------------------------------------------------- |
| Single-binary CLIs from GitHub releases         | System packages needing a package manager or `sudo`     |
| npm globals and `uv`/`pipx` tools               | The package managers and language installers themselves |
| `go install`-ed tools                           | GUI application installers                              |
| `curl \| sh` installers for user-space binaries | Fonts, system libraries, kernel-adjacent things         |

Your system package manager does not go away. It stops being the thing that downloads unverified
binaries.

A migration that will not ruin your week:

1. Move **a few tools first**, not the whole list.
2. **Leave the previously installed binaries in place.** They stay on `PATH` behind the farm, so
   if activation is not working you get the old behavior rather than a missing command.
3. Run `datamitsu source status --config <path>` and read the `shadowed` section. That is the
   authoritative list of what the farm has taken over, with the absolute path each system binary
   was found at.
4. Delete the old copies only once that list says what you expect.
5. Run with the activation line in your rc file for a week before recommending it to anyone.
   Shell-startup regressions are noticed immediately and forgiven slowly.

`which jq` will report the farm path rather than the store, because every farm entry is a shim
pointing at the datamitsu binary. `datamitsu source status` prints the real target for every
entry — that is the answer to "which binary is this actually going to run".

## What this is not

There is no `~/.config/datamitsu` auto-discovery. datamitsu will not read a config you did not
name, in any location, for any command — `lint`, `fix` and `check` in a project are byte-for-byte
unaffected by whatever machine-level config exists on your disk, including their cache keys.

Naming the file is the trust boundary, and it is the cheapest one available for something that
executes JavaScript.

## See also

- [Source Mode](../guides/source-mode.md) — the project-scoped half, farms, shims and branch switching
- [`source` command reference](../reference/cli-commands.md#source) — every flag, in detail
- [Supply Chain Security](../guides/supply-chain-security.md) — what is hashed, verified and pinned
- [Maintain a Wrapper](./maintain-wrapper.md) — the `devtools pull-*` commands that compute hashes for you
