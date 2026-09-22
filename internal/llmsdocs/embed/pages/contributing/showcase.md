# List your configuration

> How to add a datamitsu configuration to the showcase, and what is checked before it is merged

The [showcase](/showcase) lists configurations other people can inherit. Adding yours is one entry
in [`website/src/data/showcases.json`](https://github.com/datamitsu/datamitsu/edit/main/website/src/data/showcases.json)
and a pull request.

## What an entry needs

```json
{
  "added": "2026-09-21",
  "author": { "github": "your-handle", "name": "Your Name" },
  "consume": [{ "kind": "npm", "package": "@you/datamitsu-config" }],
  "description": "One sentence: what kinds of repositories this config is built for.",
  "id": "you-datamitsu-config",
  "links": { "repository": "https://github.com/you/datamitsu-config" },
  "name": "@you/datamitsu-config",
  "tags": ["go", "typescript"]
}
```

| Field         | Required | Notes                                                                        |
| ------------- | -------- | ---------------------------------------------------------------------------- |
| `id`          | yes      | Lowercase, unique, stable — it keys the derived data                         |
| `name`        | yes      | The listing title, usually the package name                                  |
| `author`      | yes      | Name and GitHub handle                                                       |
| `description` | yes      | One sentence, up to 200 characters                                           |
| `tags`        | yes      | From the schema's list, so stacks do not fragment into `ts` and `typescript` |
| `consume`     | yes      | How a reader actually uses it: `npm`, `pypi`, `gem`, `remote` or `oci`       |
| `links`       | yes      | `repository`, plus optional `site` and `inspector`                           |
| `dataset`     | no       | URL of your published resolved-config dataset (see below)                    |
| `added`       | yes      | The date of the pull request                                                 |

Fill only what is true. Leaving a field out is always better than guessing at it.

## A `remote` entry carries a hash

A reader executes what a `remote` entry points at, so its SHA-256 is mandatory and the pull request
downloads the file and verifies it:

```json
{
  "kind": "remote",
  "url": "https://github.com/you/datamitsu-config/releases/download/v1.2.0/datamitsu.config.js",
  "hash": "6fcd9a1edfaf713683380caa7fee64b9d5d40a2adec41db153ede44e230c74f7"
}
```

## Publish a dataset, and get a fingerprint

The showcase does not screenshot configurations — it draws each one's composition across runtimes as
a thin bar. That comes from a **dataset**: the resolved-config JSON the inspector's **Dataset**
button downloads, and which you can produce in CI:

```bash
datamitsu inspect --output - | grep -o '<script type="application/json" id="inspector-manifest">.*' # or
datamitsu inspect --output atlas.html
```

Publish it as a release asset next to your config and point `dataset` at the URL that follows your
latest release:

```
https://github.com/you/datamitsu-config/releases/latest/download/datamitsu-inspector-manifest.json
```

The dataset is **display data**: it is parsed and rendered as numbers and text, never executed, so it
is not pinned by hash — a weekly refresh would be pointless if the file could never change. What the
refresh job saw is recorded instead, in `showcases.generated.json`, where a changed composition shows
up in a diff. No dataset means no bar: an entry is never drawn with made-up proportions.

## What happens to your pull request

Automation checks the shape: the schema, the tag list, a valid GitHub handle, unique ids, every URL
reachable, every `remote` hash verified by download, and a dataset that parses as an inspector manifest.
It does not check intent — **listing is a maintainer's decision**, and an entry can be removed later.

A refresh job then keeps the derived half current: the latest release date, the last commit, and the
composition from your dataset. It runs weekly, opens a pull request with the regenerated file, and
never evaluates or runs a third-party configuration. An entry whose fetch fails keeps its previous
values and records the error rather than disappearing.

Star counts are collected but never shown and never sorted on: this is a directory of
configurations you can inherit, not a ranking. Freshness is the signal that protects a reader from
inheriting an abandoned configuration: an entry with no release in twelve months is marked, not
hidden. Search, sorting and the tag chips appear once there are six or more entries; below that the
page is short enough to read without them.
