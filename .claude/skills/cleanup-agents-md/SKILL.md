---
name: cleanup-agents-md
description: Cleanup and compact the project AGENTS.md — remove duplicates of the shared datamitsu chunks and of other rules in the same file, extract non-rule content (architecture docs, templates, release notes) to separate files, strip migration cruft, move deferred work to docs/backlog/, and shrink the rules that remain without losing a single rule, condition, exception, obligation or exact command. Use this skill whenever the user asks to compress, clean up, deduplicate, slim down, sync, compact, or reduce the token cost of AGENTS.md against the datamitsu config — even if they describe it informally as "AGENTS.md got too big", "remove duplicates from AGENTS.md", "fix my agents file", or "extract architecture from AGENTS.md". Also use for explicitly requested recommendations on splitting AGENTS.md (advice only).
---

# Cleanup AGENTS.md

From the project root, read `.datamitsu/ai/skills/cleanup-agents-md/instructions.md` and follow it exactly.
`datamitsu` automatically regenerates this source of truth from the central `datamitsu-config` repository to match current chunks.
