---
worth: yes
where: cmd/store.go:109
added: 2026-08-27
---

# The store keeps every superseded install forever

An installed app lives at a content-addressed path, `{store}/.apps/<kind>/<app>/<hash>/`, where the
hash covers everything that identifies the installation: version, dependency map, lockfile hash,
runtime hash (node version + os/arch/libc) and the hashed files and archives
(`internal/runtimemanager/runtimemanager.go:254`). Binaries follow the same shape under
`{store}/.bin/<app>/<hash>`.

That is correct, and it is why two projects on different versions do not fight over one directory.
The missing half is that **nothing ever removes the previous copy after a successful install**.
`os.RemoveAll` appears only to roll back a failed install, to clear an extraction temp directory,
and to overwrite a JVM app — never to collect a superseded one. `datamitsu store` offers `path`,
`clear`, `seed`, `status` and `import`: all or nothing.

So every change that moves a hash leaves the old tree behind permanently. A dependency bump, a
regenerated lockfile, a node version bump, or a rebuild of the shared config is enough.

## Measured

One developer machine, 2026-08-27:

| Path                       | Size   |
| -------------------------- | ------ |
| `~/.cache/datamitsu` total | 11 GB  |
| `{store}/.apps`            | 3.6 GB |
| `{store}/.runtimes`        | 1.8 GB |
| `{store}/.bin`             | 977 MB |
| `{cache}/projects`         | 981 MB |

Copies of one app under `{store}/.apps/node/`: eslint 10 (706 MB), cspell 8, prettier 7 (139 MB),
knip 7, oxlint 6, commitlint 6. Under `{store}/.bin/`: tombi 4, lefthook 4, hadolint 2 (106 MB).

The eslint directories carry their own story — ten copies at ~345 MB each, two of them written
sixteen minutes apart, three more within ninety minutes on another day. Those are not ten versions
of eslint; they are successive rebuilds of the shared config. **One is live.** The other ~635 MB is
residue.

## Why it has not been fixed

Deleting is not obviously safe. A copy that this repository no longer references may still be the
live one for a checkout elsewhere on the machine, and the store is deliberately global. There is no
inventory of who references what, so "unreferenced" cannot be computed from inside one repository.

The one thing that makes this less frightening than it sounds: **a wrong deletion costs a
re-download, not data.** The store is a cache, every artifact in it is fetched under a mandatory
hash check, and nothing in it is authored locally. That is a materially different risk profile from
the config-evaluation cache, where a wrong answer is silent.

## Shape of a fix

Start with `datamitsu store gc --dry-run` that only reports: walk `{cache}/projects/*`, compute the
live app paths per project (`ComputeAppPath` already answers this without installing), subtract from
what is on disk, and print the candidates with sizes. That alone is useful — it turns "the store is
big" into a number and a list a human can act on.

Beyond the dry run, two things it needs and one it must not skip:

- **An age threshold.** Do not remove a directory touched within N days even when no reference is
  found, so an incomplete enumeration of projects degrades into keeping too much rather than
  deleting something live.
- **The installer's lock.** Removal must take the same lock an install takes, or a concurrent run
  in another terminal can read a tree while it is being removed.
- **`{cache}/projects` too**, which is 981 MB here and has the same no-GC problem, with the easier
  invalidation story: a project directory whose repository no longer exists is dead outright.

A per-machine inventory of the user's repositories would make the reference set precise rather than
approximate; that is described separately in `find-repositories-with-stale-datamitsu-pins.md`, and
this item does not depend on it — the age threshold is enough for a first version.

## Found

Recorded three separate times before this entry existed: in the follow-up sections of the plans
dated 2026-08-19 (as "14 GB with no GC"), 2026-08-26 (planner) and 2026-08-26 (verdict). Each was
written by someone who had just measured it, in a document nobody searches, and each time the next
person measured it again. That is the cost this file exists to stop.
