---
worth: later
where: internal/install/installer.go:125
added: 2026-08-27
---

# A managed config file cannot be renamed without discarding its contents

`otherFileNameList` is the only mechanism `dm setup` has for reacting to a config file that carries
the wrong name, and it only deletes. For each alternate filename the installer removes the file
outright:

```go
for _, altFilename := range cfg.OtherFileNameList {
    ...
    if err := os.Remove(altPath); err != nil {
```

The ordering is what makes this irreversible. Alternates are removed at
`internal/install/installer.go:125`, **before** the main file is read at `:150`, so by the time
`content()` runs, `originalContent` holds the main file and nothing else — the alternate's bytes are
already gone. The generator has no way to see them, and no way to ask for them: no field on
`ConfigContext` carries a non-canonical file's content (`cwdPath`, `datamitsuDir`,
`existingContent`, `existingPath`, `isRoot`, `originalContent`, `projectLocations`, `projectTypes`,
`rootPath`), and config code runs in goja with no filesystem access to go looking. There is no
`renameFrom`, and no config-side workaround for its absence.

## Why it has not mattered yet

Every filename currently listed in an `otherFileNameList` belongs to a config datamitsu itself
writes — `.eslintrc.js`, `.golangci.yml`, `rustfmt.toml`, `prettier.config.*` and the rest. Deleting
one is safe because the canonical file is regenerated from the shared config in the same pass. The
delete-only design is a correct fit for that whole class, and the "one config per tool" policy keeps
it that way.

The assumption underneath it is that **datamitsu can write the replacement from scratch**. That
holds for a datamitsu-owned config and fails for a user-authored one.

## What it blocks

Normalizing `Taskfile.yml` to `Taskfile.yaml`. A Taskfile is written entirely by the project — it is
the one file in this family whose contents datamitsu could never reproduce. The naive encoding

```js
{ fileName: "Taskfile.yaml", otherFileNameList: ["Taskfile.yml"], content: … }
```

deletes the project's task definitions and writes an empty replacement. Not a migration: a silent
loss of hand-written work, on a routine `dm setup`.

Worth noting that Taskfile is not managed at all today — no entry under
`src/datamitsu-config/setup/`, and no tool in the config matches it — so nothing is broken right
now. The gap is that the capability needed to start managing it does not exist.

The same wall stands in front of any future config where the file is authored by the project rather
than generated: renaming it is off the table until setup can move a file instead of removing one.

## Shape of a fix

A `renameFrom` alongside `otherFileNameList`, distinguished by intent — `otherFileNameList` means
"this file is stale, delete it", `renameFrom` means "this file is the same file under an old name,
move it".

- Resolve `renameFrom` **before** the alternate-deletion loop, so a name appearing in both lists is
  moved rather than raced.
- Move the file to the canonical name, then continue the normal path: the moved content becomes
  `originalContent`, and `content()` transforms it exactly as it would any pre-existing file. That
  gives a generator the option of a real content migration (a YAML rewrite, say) for free, rather
  than only a rename.
- Refuse to overwrite: if the canonical name already exists with different content, report both
  paths and leave the repository untouched. Two files that disagree is a question for a human, not
  something to resolve by picking one.
- `--dry-run` must name the move, since this is the first setup action that can destroy authored
  content if it is wrong.

## Found

While compacting the shared agent-rule chunks in the config repository on 2026-08-27. The chunk text
spells the file `Taskfile.yml`, real repositories use `Taskfile.yaml`, and settling that
inconsistency in the written rule immediately raised the question of migrating the files that
already carry the old name — at which point the missing primitive surfaced. The rule can be
normalized on its own; the migration cannot follow until this exists.
