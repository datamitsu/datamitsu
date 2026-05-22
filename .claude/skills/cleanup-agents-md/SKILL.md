---
name: cleanup-agents-md
description: Cleanup the project AGENTS.md by removing chunk duplicates, extracting non-rule content (architecture docs, templates, release notes) to separate files, and stripping migration cruft. Use this skill whenever the user asks to compress, clean up, deduplicate, slim down, sync, or compact AGENTS.md against the datamitsu config — even if they describe it informally as "AGENTS.md got too big", "remove duplicates from AGENTS.md", "fix my agents file", or "extract architecture from AGENTS.md".
---

# Cleanup AGENTS.md

Read `.datamitsu/ai/skills/cleanup-agents-md/instructions.md` from the project root and follow it precisely.

The instructions file is the source of truth for this skill — it is regenerated automatically by `datamitsu` from the central `datamitsu-config` repository, so it is always up to date with the current chunks.
