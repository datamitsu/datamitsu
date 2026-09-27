---
worth: yes
where: internal/clitest/binary.go:118
added: 2026-09-27
---

# `go test ./...` serves the CLI blackbox suite from the test cache after the binary changed

The blackbox suite in `test/cli` runs a binary that `clitest` builds with a `go build` subprocess.
Go's test cache keys a result on the test binary and on the files and environment variables the
test process itself reads; neither the `go build` child nor the sources it compiles are among
them. The test binary of `test/cli` does not import `cmd`, `internal/runner` or
`internal/tooling` (`go list -test -deps ./test/cli/`), so a change to any of them leaves it
unchanged, and `go test ./...` — and the pre-commit hook's `go test -coverprofile=coverage.out
./...` — reports `test/cli` as `(cached)` from a run against the previous binary. A regression in
the CLI can then pass the hook.

The documented `go test ./test/cli/ -count=2` bypasses the cache, which is why the suite is still
trustworthy when run that way. The fix has to make the build an input the cache sees: for
example, `clitest` stats or hashes the module's Go sources (the cache records files a test opens),
or the suite's `TestMain` fails fast unless `-count` is set, or the hook passes `-count=1` for
`test/cli`.

Found while implementing the execution-control plan: after a change to `internal/runner` alone,
the hook printed `ok .../test/cli (cached)`.
