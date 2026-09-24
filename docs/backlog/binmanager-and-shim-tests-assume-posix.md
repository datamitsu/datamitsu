---
worth: yes
where: internal/binmanager/extract.go:977
added: 2026-09-24
---

# binmanager and shim tests fail on Windows, so CI runs only a handful there

The `Test (Windows)` job runs only the tests that start a downloaded binary app
(`internal/binmanager/store_path_test.go`). The rest of `internal/binmanager` and `internal/shim`
was never run on Windows, and it does not pass there: cross-compiling both test binaries at
`e312cf1` and running them from a copy of the package directories (so `testdata` is present) on a
Windows 11 host gives 17 failing top-level tests in `binmanager` and 8 in `shim`.

Most are assumptions of the tests, not of the code:

- executable bits: `TestMoveFile` and `TestCopyFileAtomic` expect mode `0755` and get `0666`,
  which is all Windows reports;
- POSIX fixtures: served binaries and farm targets are `#!/bin/sh` scripts, which Windows cannot
  start (`TestExecEntryResolvesABareInterpreterOutsideTheFarm`, the `TestDispatch*` family);
- separators: `TestGetCommandInfo_MergesAppEnv_*` expect `store\cfg` and get `store/cfg` from a
  `${STORE}/cfg` placeholder, which Windows accepts;
- archive extraction (`TestExtractTarGzToDir`, `TestExtractTarXz_NodeArchive`,
  `TestWriteAppFiles_*`) and download limits/timeouts were not examined one by one.

One is a policy question rather than a test fixture: `validateArchivePath("/etc/passwd")` and
`matchPath("/usr/bin/myapp", …)` accept a rooted, drive-less path on Windows, because
`filepath.IsAbs` is false there. The later `filepath.Join` with the destination keeps such an entry
inside it, so this does not look like an escape, but the rule the tests state ("absolute archive
paths are rejected") does not hold on Windows, and a drive-relative entry (`C:foo`) has not been
checked at all. Decide whether a rooted entry is rejected everywhere before widening the Windows
job to the whole package.
