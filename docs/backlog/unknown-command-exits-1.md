---
worth: yes
where: cmd/root.go:266
added: 2026-09-27
---

# An unknown command and some commands' own argument checks exit 1, not the usage code 2

Usage errors exit 2 (`internal/exitcode`): cobra flag errors through `SetFlagErrorFunc`,
positional-argument checks through `usageArgs`, required flags and flag groups through the
root's `PersistentPreRunE`, and the argument checks of `fix`, `lint`, `check`, `install`,
`inspect`, `source`, `store seed`, `devtools dockerfile` and `devtools pull-runtimes`. Two kinds
still exit 1:

- **An unknown command.** `datamitsu bogus` fails inside cobra's command lookup, before any hook
  or flag parsing, with a plain `unknown command "bogus" for "datamitsu"` error. Telling it apart
  in `Execute` means matching cobra's message or resolving the command with `rootCmd.Find` first.
  An unknown subcommand of a group (`datamitsu config bogus`) prints the group's help and exits 0.
- **Argument checks commands make themselves** in their `RunE`, returning a plain error
  (`errors.New`/`fmt.Errorf`) for a value they refuse. Each is a one-line change to
  `exitcode.UsageError`; nobody has listed them all.

Scripts that tell "you called it wrong" from "the code is bad" by the exit code get the wrong
answer for these. Found while implementing the execution-control plan's exit-code change.
