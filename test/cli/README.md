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
- `shell.go` — `sh` scripts as tools (`ShellTool`, `ShellConfig`) and the
  marker files they record their runs in (`MarkerDir`, `Project.Marker`).
- `jsonl.go` — the `--log-format jsonl` stream: `ParseJSONL`, causal chain
  checks (`AssertChains`) and golden normalization (`NormalizeJSONL`).
- `parsers.go` — `SeedParserModule`, which places a WASM parser module in a
  run's store so an offline run loads it without a fetch.

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
`DATAMITSU_*`, `CI`, `TERM`, CI-system markers such as `GITHUB_ACTIONS`,
agent-session markers such as `CLAUDECODE` or `CODEX_*`, `FORCE_COLOR` or
`CLICOLOR_FORCE`), an isolated `DATAMITSU_CACHE_DIR`, and a `git init`-ed temp
CWD. No network is required. A golden recorded in a CI job or an agent session
is therefore the same as one recorded in a plain shell; a test that needs one
of those variables sets it through `RunOptions.Env`.

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

## Execution characterization

[`execution_test.go`](execution_test.go) freezes what `check`, `fix` and `lint`
do when tools actually run — where fail-fast stops a run, what a failure
prints, the JSON-L stream, the cache footer and the exit codes — so a change to
the executor or the runner shows up as a reviewable golden diff. Its goldens are
`testdata/golden/execution_*.txt`, each holding the exit code, stdout and
stderr of one run.

- **Tools are `sh` scripts.** `clitest.ShellTool(name, script, spec)` declares
  a tool whose app is `sh -c <script> <name>`: the script sees its tool name as
  `$0`, the operation's arguments from `$1`, and the marker directory as
  `$MARKERS`. `clitest.RecordRun` appends `<tool> <first argument>` to
  `.markers/<tool>`, so a test asserts whether a process ran with
  `Project.Marker` instead of reading logs. The marker directory ignores its
  own content, so markers never enter a later run's file set or cache keys.
  Every scenario skips when no `sh` is on `PATH` (Windows).
- **A scenario that records a known defect says so.** Its comment names the
  plan of `docs/plans/2026-09-26-unified-results.md` that changes the
  behaviour; that plan flips the assertion and regenerates the golden in the
  same change.
- **Event streams are asserted causally.** `clitest.AssertChains` checks that
  every `tool_run` start has a terminal event (except for the tools a scenario
  names as orphaned), that an operation's `phase` precedes its `tool_run`
  events, and that `done.runs` counts the terminal `tool_run` events. Parallel
  scenarios never assert line order: `NormalizeJSONL` sets `ts` to `0` and a
  present `duration_ms` to `1`, and the golden's lines are sorted.
- **What the goldens leave out.** Progress lines (`→ …`) are dropped: they are
  throttled display that carries whichever label the last parallel callback
  set. Duration text is masked including the padding after it. Every script
  sleeps 10ms first, because a process faster than a millisecond reports a
  duration of 0, which `omitempty` drops from its JSON-L event.
- **Parser modules are seeded, not fetched.** `clitest.SeedParserModule` copies
  a module into the run's store at its content-addressed path and returns the
  `parsers` declaration carrying its real SHA-256; the offline run loads it
  from there.

## Contract completeness gate

`TestContractCompletenessGate` walks the binary's live `--help` tree and asserts
the set of leaf commands equals exactly the tested ∪ builtin leaf sets. Adding a
new leaf command without a blackbox test (or removing one) fails this gate — so
every command stays covered. When you add a command, add at least one blackbox
test and register it in the gate's tested-leaf set.

## Real-shell tier (`test/shell`)

`test/shell` sits one level above this suite. Where `test/cli` golden-tests what
the binary _prints_, the shell tier tests what a shell _does_ with it: it evaluates
the activation code in real `bash`, `zsh` and `fish` and then runs tools by bare
name. That is the only way to prove the three properties source mode exists for —
pinned versions beat a same-named system binary, a branch switch lands on the
same command line, and activation downloads nothing.

It is not build-tagged and runs under `go test ./...`; cases skip with a message
naming the unverified property when a shell is missing. It writes into the same
`clitest.CoverDir()` this suite uses, which is why that path is not overridable.

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
