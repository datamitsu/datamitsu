---
worth: yes
where: internal/tooling/planner.go:408
added: 2026-09-20
---

# A file selection at the repository root still plans a whole-repository verdict

From this repository's root:

```bash
datamitsu lint package.json --tools syncpack --widen-to=target --explain
```

The plan includes syncpack at repository scope with one matched file, even though
its command accepts no file list and the requested widening limit is `target`.
Running the same command from `website/` instead reports
`whole-repository verdict — cannot narrow`.

The repository-scope guard tests `cwdPath != rootPath`; it does not test whether
an explicit file selection narrowed the input. The CLI reference describes both
naming files and entering a subdirectory as narrowed requests. At the root, the
plan can therefore promise a limited operation while the tool reads the entire
repository. The homepage recording uses the working subdirectory case and names
that context explicitly.

Apply the operation's granularity to the selection as well as to the current
working directory. Cover explicit root file arguments, staged-file selection,
subdirectories and an unrestricted root run; retain `--widen-to=repo` as an
explicit override. This needs planner regression tests independently of the
inspector, which displays config declarations and does not execute a plan.
