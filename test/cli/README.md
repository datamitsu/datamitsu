# CLI blackbox test suite

This directory holds the **offline golden contract tests** for datamitsu's
command-line surface. They run the real, compiled binary in subprocesses inside
isolated temp git repos and assert stdout/stderr/exit codes against golden
files. The goal is to freeze the externally-observable CLI behavior so the
internals can be rewritten with confidence — the CLI surface must not change
during that rewrite.

The reusable harness lives in [`internal/clitest`](../../internal/clitest):

- `binary.go` — builds a `go build -cover` instrumented binary once per run.
- `run.go` — runs the binary with isolated env (`BaseEnv`) + workdir, captures
  streams separately, returns exit codes.
- `project.go` — temp git repo + config writers (`NewProject`,
  `WriteMinimalConfig`, `WriteOverlayConfig`, `WriteDatamitsuIgnore`).
- `golden.go` — output normalization + golden compare (`AssertGolden`).

A second, **gated** OCI-seeded tier lives in [`test/e2e`](../e2e) — see below.

## Running the offline suite

```bash
# Run the whole blackbox suite (builds the instrumented binary once).
go test ./test/cli/

# Determinism check — goldens must be byte-stable across two runs.
go test ./test/cli/ -count=2

# Run a single test.
go test ./test/cli/ -run TestVersionGolden
```

The suite is fully offline and hermetic: each run gets a clean env
(`DATAMITSU_OFFLINE=1`, `DATAMITSU_NO_OCI=1`, `NO_COLOR=1`, no inherited
`DATAMITSU_*`/`CI`/`TERM`), an isolated `DATAMITSU_CACHE_DIR`, and a `git init`-ed
temp CWD. No network is required.

> The embedded `internal/config/config.js` is checked in, so `go build` (and
> therefore the harness's instrumented build) works without a prior `pnpm build`.

## Updating goldens

Golden files live in `test/cli/testdata/golden/*.txt`. When a CLI change
intentionally alters output, regenerate the affected goldens with the
package-level `-update` flag:

```bash
# Regenerate every golden the suite touches.
go test ./test/cli/ -update

# Regenerate only the goldens for one test.
go test ./test/cli/ -run TestConfigShow -update
```

Always review the resulting `git diff` of the golden files — `-update` accepts
whatever the binary currently prints, so an unintended behavior change will show
up as a golden diff to inspect, not a silent pass.

## Contract completeness gate

`TestContractCompletenessGate` walks the binary's live `--help` tree and asserts
the set of leaf commands equals exactly the tested ∪ builtin leaf sets. Adding a
new leaf command without a blackbox test (or removing one) fails this gate — so
every command stays covered. When you add a command, add at least one blackbox
test and register it in the gate's tested-leaf set.

## Gated OCI e2e tier (`test/e2e`)

The OCI tier exercises the **real** seed/install/exec/init/check/fix/lint
pipelines against the user's released, digest-pinned config. It needs network +
registry access and is therefore double-gated and never runs in default CI:

1. Build tag `//go:build e2e_oci` — keeps the files out of the default build.
2. `RequireOCIE2E(t)` — skips unless `DATAMITSU_TEST_OCI=1`.

```bash
# Run the OCI tier (network required).
DATAMITSU_TEST_OCI=1 go test -tags e2e_oci ./test/e2e/...

# Point at a warm cache for dedup speed (avoids re-pulling the bundle).
DATAMITSU_TEST_OCI=1 DATAMITSU_TEST_CACHE=$HOME/.cache/datamitsu \
  go test -tags e2e_oci ./test/e2e/...
```

Without the tag, `go test ./test/e2e/...` reports "no test files". With the tag
but without `DATAMITSU_TEST_OCI=1`, the tests SKIP. CI runs this tier only via
the manual/nightly [`oci-e2e.yml`](../../.github/workflows/oci-e2e.yml) workflow.

### Bumping the vendored OCI config (single source of truth)

The tier inherits a vendored, digest-pinned config at
`test/e2e/testdata/datamitsu.config.oci-ghcr.js`. Its canonical upstream URL is
the `OCIConfigSource` const in [`test/e2e/source.go`](../e2e/source.go) — the
single source of truth. When a new `datamitsu-config` release is cut, bump the
version in that URL and re-download into testdata:

```bash
curl -sSL -o test/e2e/testdata/datamitsu.config.oci-ghcr.js \
  https://github.com/shibanet0/datamitsu-config/releases/download/v0.1.6/datamitsu.config.oci-ghcr.js
```

The vendored file carries the bundle's `oci.ref` + `oci.digest`, so it pins the
whole OCI bundle by content — no separate hash field is needed.

## Combined coverage

The blackbox subprocess runs collect coverage via `GOCOVERDIR` (Go 1.26), merged
with in-process unit-test covdata into a single profile by
[`scripts/coverage-all.sh`](../../scripts/coverage-all.sh):

```bash
pnpm test:coverage:all   # -> coverage.out (merged), prints total + lowest pkgs
```

This is what CI's `test` job runs so the blackbox runs count toward the real
coverage number. See [`CONTRIBUTING.md`](../../CONTRIBUTING.md#testing) for the
merge mechanics.
