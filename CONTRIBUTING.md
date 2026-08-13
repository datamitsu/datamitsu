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

## Testing an unreleased core in a real project (dev-link)

Some behaviour only shows up through the full chain — core → wrapper config
package → consuming project — with real tools, a real monorepo and real plugin
noise. Publishing an unstable build to npm just to see it is a slow loop, so
`task dev:link` produces the same package pair locally:

```bash
task build:parsers   # only if you changed a WASM parser
task dev:link        # -> dist/dev-link/ (gitignored)
```

It writes two throwaway packages mirroring the published ones:

| Package                                  | Contents                                                                             |
| ---------------------------------------- | ------------------------------------------------------------------------------------ |
| `@datamitsu/datamitsu-<platform>-<arch>` | the freshly built binary                                                             |
| `@datamitsu/datamitsu`                   | the launcher (`bin/`, `get-exe.js`), plus the locally built `.wasm` under `parsers/` |

The launcher already symlinks the platform package into its own `node_modules`,
so it runs as-is and needs no install step. Link it down the chain with pnpm:

```bash
# in the wrapper config repo (the package your projects depend on)
pnpm link /path/to/datamitsu/dist/dev-link/datamitsu

# in the consuming project
pnpm link /path/to/your/wrapper-config-repo
```

`pnpm dm <command>` in that project now runs your local core. Rebuild with
`task dev:link` after each change; the links keep pointing at the same directory.

### Local WASM parsers

A parser module is normally fetched from the release by `url` + SHA-256. To run
a locally built one, `task dev:link` prints a ready `parsers` entry pointing at
the copy inside the linked package:

```js
const parsers = {
  core: {
    hash: "<printed sha256>",
    url: "file:///abs/path/to/dist/dev-link/datamitsu/parsers/datamitsu_parsers.wasm",
  },
};
```

Paste it into the wrapper config in place of the release pin, and re-run
`task dev:link` (which reprints the hash) whenever you rebuild the parser.

**Why this is not a hole in the hash policy.** A `file://` source is copied and
then SHA-256 verified on exactly the same code path as a download — the hash
stays mandatory, and a mismatch still fails. Two independent locks keep the
capability out of anything you ship:

1. **Build.** The local-read path requires `-X …/internal/ldflags.LocalArtifacts=1`,
   which only `task dev:link` injects. Released binaries — and a plain
   `go build` — refuse `file://` outright.
2. **Call site.** Even in a dev-link build, only the parser store passes
   `allowLocalFile`. An app, archive or JAR declaration can never reach it, so a
   config cannot turn a `file://` URL into an installed binary.

Reads are further restricted to absolute paths pointing at regular files (no
FIFOs or devices, which would hang or stream forever) and bounded by the same
size ceiling as a download.

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
