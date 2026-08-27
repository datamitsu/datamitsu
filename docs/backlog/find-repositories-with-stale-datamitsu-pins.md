---
worth: later
added: 2026-08-27
---

# No way to find which repositories are still on an old datamitsu or config pin

Someone maintaining a shared config across many repositories has no way to ask "where is it out of
date?" other than opening each repository and looking. The pin appears in a different place in each
ecosystem — a dependency in `package.json`, a catalog entry in `pnpm-workspace.yaml`, a
`--before-config` URL, an OCI reference in a Dockerfile, a version in a CI workflow — so a single
grep does not answer it either.

The same gap blocks a precise store GC. Deciding whether a store directory is still referenced needs
to know which repositories exist and what they pin; from inside one repository that is unanswerable.
See `store-keeps-every-superseded-install-forever.md`, which works around it with an age threshold.

## Shape of an idea

The idea as it was described, kept in its own terms because the specifics are the part worth
preserving.

### A user-level config, not a project one

It lives in the home directory, in dotfiles, outside any repository — because the question it
answers ("where are my repositories, and what do they pin?") is about a machine and a person, not
about a project. datamitsu is installed globally; the config sits beside it; the maintainer runs the
command when it suits them. Nothing is scheduled and nothing watches.

### Part one: where the repositories are

A plain list of roots, absolute or relative, under which git repositories live. The walk is
recursive but **bounded to those roots** — not the whole filesystem — so it covers exactly the
repositories the user actually works in, and finishes.

That list is the piece both features need. For this one it bounds the search; for the store cleanup
it bounds what counts as "referenced".

### Part two: what the artifact is called, per channel

The same config declares the artifact's identity under **every channel it is published to**, because
a pin looks different in each one. For a shared config that means at least:

- its npm package name,
- its GitHub repository,
- its OCI reference on each registry it is pushed to — GHCR and Docker Hub are separate entries,
  not one,
- its uv / Python identity, where it has one.

The declaration is per artifact, listing its names: _this is what my config is called on npm, this
is what it is called on GHCR, this is what it is called on Docker Hub._ Everything the scan does is
then derived from that block rather than hardcoded, so adding a channel is a config edit.

**The structure of this block is the open design question**, and it is what makes this `later`
rather than `yes` — see below.

### Part three: where to look for those names

Given the names, the scan looks for their occurrence — plain substring matching is enough to start
— in the places a pin actually appears:

- `package.json` dependencies and `pnpm-workspace.yaml` catalogs,
- Python and Ruby project manifests,
- `--before-config` arguments and config URLs, including artifacts pulled from GitHub releases,
- Dockerfiles and OCI references,
- CI workflow files — GitHub Actions and GitLab CI both,
- the app registry, the verify cache and bundle declarations, where a reference to the artifact can
  also sit.

### What comes out

A report: which repositories reference the artifact, through which channel, at which version, and
which are behind. The maintainer sees the fleet in one place instead of opening repositories one at
a time.

The same walk yields the reference set the store cleanup wants, which is the other half of why this
is worth building.

Deliberately **not** part of this: changing anything. Reporting is the whole scope — a maintainer
decides what to bump and does it themselves.

## What is unresolved

This is `later` rather than `yes` because the design has a soft centre: **the shape of the
per-channel declaration**. Saying "this artifact is called X on npm, Y on GHCR, Z on Docker Hub"
sounds simple until it has to cover a version range in a `package.json`, a catalog indirection in
`pnpm-workspace.yaml`, a digest-pinned OCI reference, and a URL with the version embedded in the
path. Making that expressive enough to be useful without turning into a small pattern language
nobody wants to write is the whole risk. Nobody has tried to write that block by hand yet.

Two cheap things would settle it, and neither needs code:

1. **Write the declaration for one real config, by hand, covering every channel it publishes to.**
   If it reads like configuration, build it. If it reads like a program, the idea needs a different
   shape.
2. **Count how often a fleet actually drifts.** If dependency bots already bump the pin everywhere,
   the report has no reader and the store-cleanup half is the only real motivation left.

## Found

Raised while looking at why the store grows without bound, as the mechanism that would make the
cleanup precise. It outgrew that question immediately, which is why it is filed on its own.
