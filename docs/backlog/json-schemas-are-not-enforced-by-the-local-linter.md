---
worth: yes
where: website/scripts/check-showcases.ts:29
added: 2026-09-21
---

# The published JSON Schemas are checked in CI, not by `dm check`

Two schemas are published under `website/static/schemas/`: the inspector theme
(`internal/inspectortheme.Schema`, verified by `go test ./internal/inspectortheme`) and the showcase
(`website/src/data/showcase-schema.ts`, verified by `node website/scripts/check-showcases.ts`). Both
are generated from the code that enforces them, so they cannot drift — but nothing runs either check
during `pnpm dm check`. A contributor editing `showcases.json` locally sees the mistake only after
pushing, and a stale published schema only fails in the `Showcase Entries` job.

Wiring both into the toolchain is the remaining half: a datamitsu tool entry that runs the showcase
check (offline, so a pull request without network access still gets the shape checked) and the theme
schema agreement test, or a JSON-schema linter pointed at the `$schema` each data file already
declares. The blocker is a choice rather than a difficulty: whether to run repository scripts as
tools from the config, or to add a schema-validating tool to the wrapper and let the `$schema` keys
drive it.

A third schema is missing entirely: the **inspector manifest** — the dataset the Dataset button
downloads, which a showcase entry publishes and the refresh job reads. Today both readers check it
by hand (`Array.isArray(manifest.apps) && typeof manifest.schemaVersion === "number"`), which
accepts almost anything. It should be generated from `inspector.Manifest` the way the theme schema
is generated from its token table, published next to the others, and used by both the refresh job
and the pull-request check. It has grown since: apps now carry `officialUrl` and
`officialUrlDerived`, which a generated schema would describe for free and a hand-written check
will keep missing.

Found while building the showcase: the shape checks were written twice, in two languages, against a
document that has a Go struct as its single source.
