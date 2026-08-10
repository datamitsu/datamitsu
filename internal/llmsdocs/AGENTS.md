# AGENTS.md — do not edit the generated `embed/` snapshot

**Everything under `embed/` in this package is auto-generated. Do not edit it.**

That means all of:

- `embed/pages/**` — the per-page documentation
- `embed/index.txt` — the rewritten root index
- `embed/manifest.json` — the provenance manifest

A manual edit here will be silently overwritten on the next regeneration, and it
will fail CI in the meantime: the page bytes are hashed into `manifest.json`, and
the `llms-docs-drift` check re-harvests on every pull request and diffs the
result against what is committed.

## To change this documentation, edit the source

The real source lives in the website, under `website/docs/**`. Edit the page
there, then regenerate the snapshot:

```bash
task gen:llms-docs
git add internal/llmsdocs/embed
```

That rebuilds the Docusaurus site and re-harvests the cleaned markdown that
`docusaurus-plugin-llms` emits. The harvester is `packaging/llms-harvest`.

## Not covered by this rule

The Go source in this package (`*.go`) is normal, editable code. This warning is
only about the generated `embed/` snapshot.
