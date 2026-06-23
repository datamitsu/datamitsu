# Contributing

## Testing

Standard Go testing, stdlib `testing` only (no testify) — table-driven, `t.TempDir`/`t.Setenv`.

```bash
go test ./...                 # all unit + offline blackbox tests
go test ./test/cli/ -count=2  # offline CLI golden suite (must be byte-stable)
```

### CLI blackbox suite

[`test/cli`](test/cli) runs the compiled binary in subprocesses inside isolated
temp git repos and asserts stdout/stderr/exit codes against golden files,
freezing the CLI contract. The reusable harness is in
[`internal/clitest`](internal/clitest). Regenerate goldens with `-update`
(`go test ./test/cli/ -update`) and review the diff. See
[test/cli/README.md](test/cli/README.md) for full details, including the
contract-completeness gate that requires every leaf command to have a test.

### Combined coverage merge

Blackbox tests run a `go build -cover` binary, so their counters flow through
`GOCOVERDIR`; unit tests are instrumented in-process and emit covdata via
`-test.gocoverdir`. [`scripts/coverage-all.sh`](scripts/coverage-all.sh) routes
both into known dirs, `go tool covdata merge`s them, and `textfmt`s the union
into `coverage.out` so the blackbox runs count toward the real number:

```bash
pnpm test:coverage:all   # -> coverage.out (merged), prints total + lowest pkgs
```

CI's `test` job runs this (not plain `pnpm test`). `-covermode=atomic` is
required so the in-process and blackbox-binary counter modes match (else covdata
merge rejects the union with a "counter mode clash").

**Measurement caveat — read the merged number, not the unit-only one.** A plain
`go test ./... -coverprofile` only instruments in-process code, so it **cannot
see** statements executed inside the blackbox subprocess binary and therefore
**under-reports** any package the blackbox suite drives. `cmd/` is the headline
example: unit-only reports ~54%, but the merged profile shows ~68% because the
CLI golden suite exercises those paths through a real subprocess. Always judge
`cmd/` (and any package covered by `test/cli`) from the merged `coverage.out`
produced by `pnpm test:coverage:all` — never from a bare `-coverprofile` run,
which silently misses the blackbox contribution.

### Gated OCI e2e tier (`e2e_oci`)

[`test/e2e`](test/e2e) exercises the real seed/install/exec/init/check/fix/lint
pipelines against the released, digest-pinned config. It needs network + registry
access and is **double-gated** — the `//go:build e2e_oci` build tag plus a
`DATAMITSU_TEST_OCI=1` env check — so it never runs in default CI:

```bash
DATAMITSU_TEST_OCI=1 go test -tags e2e_oci ./test/e2e/...
```

CI runs this tier only via the manual/nightly
[oci-e2e.yml](.github/workflows/oci-e2e.yml) workflow. The vendored config it
inherits is the single source of truth in
[test/e2e/source.go](test/e2e/source.go) — bump and re-download it when a new
`datamitsu-config` release is cut.

## Releases

Releases are automated via GitHub Actions ([release.yml](.github/workflows/release.yml)).

### Stable release

```bash
git tag v1.0.0
git push origin v1.0.0
```

Publishes: GitHub Release, npm, Docker (GHCR + Docker Hub), Homebrew cask.

### Release candidate

```bash
git tag v1.0.0-rc.1
git push origin v1.0.0-rc.1
```

Same as stable, but Homebrew cask update is skipped (prerelease detected automatically).

### Unstable release

Actions > Release > Run workflow > release_type: `unstable`.

Publishes: npm (`unstable` tag), Docker (GHCR only). No GitHub Release or Homebrew.

### Distribution channels

| Channel        | Stable          | RC               | Unstable   |
| -------------- | --------------- | ---------------- | ---------- |
| GitHub Release | yes             | yes (prerelease) | opt-in     |
| npm            | `latest` / `rc` | `rc`             | `unstable` |
| Docker Hub     | yes             | yes              | no         |
| GHCR           | yes             | yes              | yes        |
| Homebrew       | yes             | no               | no         |
