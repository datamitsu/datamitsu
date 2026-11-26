# Contributing

## Development toolchains

Go builds require Go 1.27.4 or newer, as declared in `go.mod`. Parser builds use
Rust 1.100.0 from `parsers/rust-toolchain.toml`; keep its embedded container pins
aligned and regenerate both parser WASM artifacts when changing that compiler.

## Testing

Standard Go testing, stdlib `testing` only (no testify) — table-driven, `t.TempDir`/`t.Setenv`.

```bash
go test ./...                 # all unit + offline blackbox tests
go test ./test/cli/ -count=2  # offline CLI golden suite (must be byte-stable)
```

### Python packaging checks

Before running `pnpm dm check` in a fresh checkout, install uv and sync the Python
packaging development dependencies from the committed lock file:

```bash
uv sync --project packaging/python --locked --only-group dev --no-install-project
```

This supplies Hatchling to the type checker without building or installing the
platform-specific package. Build-system requirements alone are installed only in
the isolated build environment.

CI's lint job runs the same sync before linting so fresh runners use the locked
development environment too.

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

### Real-shell tier (`test/shell`)

[`test/shell`](test/shell) proves the source-mode properties that only a real
shell can show: that a project's pinned version wins over a system binary of the
same name, that a branch switch takes effect on the same command line (no prompt
hook involved), and that activation downloads nothing until a tool is first run.
It drives real `bash`, `zsh` and `fish` against a two-branch fixture repository
served by a loopback `httptest` release host.

It is deliberately **not** build-tagged — it runs under plain `go test ./...`:

```bash
go test ./test/shell/
```

Where a shell (or `make`) is not installed, the affected cases `t.Skip` and name
the property left unverified rather than passing silently. The tier shares
`clitest.CoverDir()` with `test/cli`, so its coverage output path is fixed on
purpose: making it overridable would let the two tiers clobber each other's
profile.

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

[test/e2e/parsers_oci_test.go](test/e2e/parsers_oci_test.go) adds the two cases
for a registry-sourced parser module: one pulls it and calls the module's own
`describe` export, the other prefetches it and then repeats the run under
`DATAMITSU_OFFLINE=1`, which is the only end-to-end proof of the airgap claim.
Both drive `devtools parsers list` rather than `lint`, because a parser failure
degrades to raw output and would leave `lint` green. Both skip — with the reason
— while the vendored config still pins its parser by `url`: the fixture is the
wrapper's published config, so this tier can only exercise a pin that already
shipped.

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

A `parsers` entry declares exactly one source — a `url` or an `oci` pin — plus a
mandatory SHA-256. To run a locally built module, `task dev:link` prints a ready
entry pointing at the copy inside the linked package:

```js
const parsers = {
  core: {
    hash: "<printed sha256 digest>",
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

### An unreleased module in downstream CI

`file://` never leaves the machine that built the module, so a downstream
project's CI cannot use it. That is what the `oci` source is for: the entry pins
a registry artifact instead of a URL, and the same mandatory hash verifies the
bytes.

```js
const parsers = {
  core: {
    hash: "sha256:0123…cdef",
    oci: {
      ref: "ghcr.io/datamitsu/datamitsu-parsers-unstable",
      digest: "sha256:89ab…4567",
    },
  },
};
```

The `ref`, the `digest` and the matching `sha256`-prefixed hash all come from
`parsers-oci.json`, which preparation computes locally and embeds in
`@datamitsu/datamitsu`, so a wrapper reads the pin out of `node_modules`
instead of querying a registry.

The two sources are mutually exclusive rather than a fallback chain — declaring
both, or neither, fails at config load:

```js
// BAD: two sources in one entry — refused at load
parsers: {
  core: {
    url: "file:///abs/path/to/datamitsu_parsers.wasm",
    oci: { ref: "ghcr.io/datamitsu/datamitsu-parsers-unstable", digest: "sha256:89ab…4567" },
    hash: "sha256:0123…cdef",
  },
}

// GOOD: one source; the hash is the same value either way
parsers: {
  core: {
    oci: { ref: "ghcr.io/datamitsu/datamitsu-parsers-unstable", digest: "sha256:89ab…4567" },
    hash: "sha256:0123…cdef",
  },
}
```

Switching transports does not move the module: the parser store key is derived
from the SHA-256 alone, so a local build, a release download, a registry pull
and a bundle layer all land in one directory — and whatever is already there is
re-hashed against the declared SHA-256 on every use and discarded when it does
not match. A locally built module and a registry-pulled one are therefore
interchangeable.

Three things that surprise people when the fetch does not go the way they
expect:

- `--no-oci` / `DATAMITSU_NO_OCI` disables OCI bundle store **seeding**. It does
  not disable a declared parser `oci` source, which is the only route to those
  bytes. `DATAMITSU_OFFLINE=1` is the hard network gate.
- `oci.signer` is rejected at config load, on parsers and on bundles alike. CI
  cosign-signs the pushed manifest and that signature is verifiable out of band,
  but this build carries no sigstore dependency and checks no signature itself.
- A parser failure degrades silently to raw tool output, so CI that means to
  validate a pin has to force the fetch and assert an exit code — for example
  `datamitsu devtools parsers prefetch`.

## Committing

Do not set a repository-local `user.name` / `user.email` here. The CLI blackbox
suite gives its throwaway fixture repositories a fixed identity,
`datamitsu-clitest <clitest@datamitsu.invalid>`, and that identity has a way of
migrating into this repository's own `.git/config` — usually alongside
`commit.gpgSign = false`, when someone wants to commit non-interactively.

The consequence is not local. Every commit on the branch is authored by an
account that does not exist, and GitHub's squash merge copies each distinct
branch author onto `main` as a `Co-authored-by:` trailer — so a local
convenience becomes permanent, world-readable history, with the commits
unsigned into the bargain.

Two guards catch it: a lefthook `pre-commit` check before the commit exists, and
the `Commit Identity` CI job, which walks `base..head` and fails the PR. To
check by hand:

```bash
git config --local --get-regexp 'user\.|commit\.'   # expect no output
```

If commits were already made under the wrong identity, drop the override and
rewrite them:

```bash
git config --local --unset user.name
git config --local --unset user.email
git config --local --unset commit.gpgSign
git rebase --reset-author-date --exec 'git commit --amend --no-edit --reset-author' main
```

## Releases

The [Release workflow](.github/workflows/release.yml) prepares checked artifacts.

- `verify` (default): build from the selected branch without signing or publishing.
  The run summary reports that verification passed and no publication was requested.
- `prerelease`: a new run on the protected default branch automatically hands the
  prepared index to `Release Preflight`. After verifying the preparation, it publishes
  to npm and GHCR and creates a GitHub Pre-release without setting it as Latest.
  A run from another branch prepares artifacts only; manual publication still
  requires the source commit to be in the default branch history.
- `unstable`: select `main` under **Use workflow from**, then enter the unmerged
  branch name in `product_ref`. Release resolves that branch in this repository
  once and publishes the checked commit to npm and GHCR. The publisher runs
  from the protected default branch, not from the product branch. `verify`
  accepts the same input without publishing. Leave it empty to use the dispatch
  commit. `prerelease` does not accept a product override.

Manual builds use versions such as `0.0.0-preview.202611251830.123.1` for
`prerelease` and `verify`, or `0.0.0-unstable.202611251830.123.1` for `unstable`.
The date and time are in UTC. Run and attempt distinguish same-minute builds.
Publication recovery uses the version recorded in the original index.

A protected `vX.Y.Z` or `vX.Y.Z-rc.N` tag push starts a tagged release.
The workflow must exist on the default branch before GitHub allows dispatch.

The npm launcher is outside the default workspace globs. The release workflow
packs it from the workspace root with an explicit package selection so `pnpm`
resolves `catalog:` from the root configuration. Generated platform packages
are packed independently so root ignore rules do not omit their executables.
The smoke test installs the packed launcher and matching host archive offline.
The preparation job can restore GitHub Actions caches but cannot save them.

[Release Preflight](.github/workflows/release-preflight.yml) starts automatically
after a successful tagged preparation or an eligible first `unstable` or
`prerelease` preparation. It runs on the protected default branch, checks the
original index and GitHub's producing job metadata, and validates every
referenced payload. It records the product SHA separately
from the trusted workflow revision. A moved tag, ambiguous index, expired
artifact, or digest mismatch blocks publication.
Publisher jobs consume the prepared bytes without rebuilding them or running
product-source scripts.
If a prerelease finalization claim already exists, automatic Preflight stops
and directs the operator to manual recovery.
Automatic and manual Preflight runs for the same preparation run ID share
one queue. A recovery waits for the active run instead of canceling it.

For a manual-only `prerelease` or publication recovery, use **Release Preflight**
on the default branch. Copy the preparation run ID, producing attempt, index ID,
and index archive SHA-256 from the original preparation summary. The verified
preparation mode determines the destinations; a recovery cannot change them.
Use `unstable` for an unmerged product SHA. To rebuild `unstable`, start a new
dispatch. For publication recovery, reuse the original index, not a rebuild.
Enter `release_tag` for a tagged recovery. To recover all stable destinations,
also select `publish_pypi` and `publish_rubygems`. Do not choose newer artifact
IDs or create a second tag to recover a partial release.

### Activation

Common publishing jobs run automatically on the protected default branch after
Preflight verifies the source, preparation and artifacts. They do not use a
shared approval environment. Destination-specific environments retain their
required reviewers and deployment restrictions. `verify` never publishes.
Tagged npm releases also require `RELEASE_BUILD_ATTESTATION_ENABLED=true`
before preparation so Preflight can verify their provenance.

Before enabling publishing workflows on the default branch, create the
environments below with required reviewers and restrict deployments to the
protected default branch. For tagged npm attestations, restrict
`release-build-attestation` to protected release tags and require approval.
Protect the default branch and release tags. Restrict writes to the
`release-finalization/**` refs to the release automation and prevent changes or
deletion after creation. Retire any old writers for the same channels before
cutover. Do not activate until the repository and registry owners have confirmed
the protections and bindings.

| Environment                   | Required registry configuration                                                                                                          |
| ----------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| Common jobs (no environment)  | Existing npm and Docker Hub credentials available to trusted automation, and GHCR package write access for the publisher                 |
| `pypi`, `rubygems`            | Trusted publisher for `datamitsu/datamitsu`, workflow `release-preflight.yml`, and the matching environment; no API token fallback       |
| `setup-action`                | `SETUP_DATAMITSU_TOKEN` for `datamitsu/setup-datamitsu`                                                                                  |
| `open-vsx`                    | Existing publisher namespace and `OVSX_PAT`                                                                                              |
| `vscode-marketplace`          | Azure federated identity scoped to this environment, `AZURE_CLIENT_ID` and `AZURE_TENANT_ID`, and permission for the extension publisher |
| `homebrew`, `scoop`, `winget` | `HOMEBREW_TAP_TOKEN`, `SCOOP_TOKEN`, and `WINGET_TOKEN`, respectively; WinGet also needs permission to open an upstream pull request     |

Before promoting npm aliases, record any prior channel values in
`NPM_UNSTABLE_CUTOVER_BASELINE`, `NPM_NEXT_CUTOVER_BASELINE`, or
`NPM_LATEST_CUTOVER_BASELINE` as applicable. A baseline is a JSON object with
one entry per package, using `null` for an absent tag. Tagged uploads use
verified provenance from the protected producer job: each of the nine bundles
binds the exact tarball, source SHA, tag, and preparation index. Each bundle
records its own signing attempt, which can be newer than the attempt that
produced the index. The publisher uses `--provenance-file` instead of generating
provenance on its own branch. It uploads immutable versions before moving
channel tags.

### Recovery

Start a new manual `Release Preflight` run on the protected default branch
with the original preparation index and signed evidence. Do not rerun publisher
jobs: their Preflight approval checks apply only to the attempt that ran them.
A matching coordinate is not proof of matching content.
Publishers reconcile remote contents first and stop on different bytes or
when the destination cannot confirm equality. Successful writes to earlier
destinations are not rolled back.

- If an npm attestation job fails before reserving its signing claim, rerun
  only the failed jobs. The successful build and the other bundles are reused.
  Preflight accepts one verified bundle per package for that index, even when
  they come from different attempts of the same run. It rejects duplicate
  bundles rather than picking the newest one. A claim with no corresponding
  bundle requires reconciliation; do not remove the claim or rerun the build.
- `reuse_parser_finalization` verifies the existing parser signature and the
  signed record for the original index.
- Choose the image recovery mode separately for each registry. Supply
  `ghcr_image_signer_sha` or `dockerhub_image_signer_sha` to verify that
  registry's existing Debian and Alpine images, signatures, and attestation
  without writing to it. Set `first_ghcr_image_finalization` or
  `first_dockerhub_image_finalization` only if the corresponding registry
  never reserved its signing claim. Do not set both modes for one registry.
  For example, if GHCR completed and Docker Hub failed at login, dispatch
  with the original index, `ghcr_image_signer_sha`, and
  `first_dockerhub_image_finalization`.
- `signature_recovery` restores both checksum bundles without any new signing.
  It pins the original artifact ZIP and the checksum bundle. The second
  signature binds that bundle to the exact preparation index.
- `checksum_checkpoint` restores the first verified signature and its index
  record after interruption. It requires the original finalizer revision and
  a successful new reservation for **only** the index signature. If that step
  was already reserved or its state cannot be determined, stop and reconcile
  with Sigstore; never remove a claim to retry.
- For a manually approved first finalization, select the corresponding
  `first_*_finalization` inputs. A claim is not proof that an external write
  completed. A missing checkpoint, unconfirmed signature, or ambiguous
  response requires reconciliation, not re-signing.

### Distribution channels

The binary matrix is Linux, macOS, Windows, and FreeBSD on amd64 and arm64.
FreeBSD is included in npm and RubyGems, but not PyPI. Tagged GitHub releases
also include the native Linux packages, SBOMs, parser Wasm, and checked VSIX.
APK filenames keep the release version; native metadata uses `X.Y.Z_rcN`
and `0.0.0_preRUN_preATTEMPT`. PyPI wheels target macOS 13 or later. Manual
preparation builds diagnostic wheels and gems, but does not publish them.

| Channel                                              | Stable                                           | RC                             | Manual prerelease / unstable                                               |
| ---------------------------------------------------- | ------------------------------------------------ | ------------------------------ | -------------------------------------------------------------------------- |
| GitHub Release                                       | Version and Latest promotion                     | Versioned prerelease           | `prerelease` only, never Latest                                            |
| npm                                                  | `latest`                                         | `next`                         | `next` for preview; `unstable` for unstable                                |
| Docker Hub and GHCR                                  | Debian and Alpine on amd64/arm64; stable aliases | Versioned images and `preview` | GHCR only; `preview` in the ordinary repository, `unstable` in its sibling |
| Parser OCI                                           | Version and `latest`                             | Version and `preview`          | `preview` in the ordinary repository, `unstable` in its sibling            |
| PyPI and RubyGems                                    | Versioned                                        | Native RC version              | No                                                                         |
| Setup action, editor stores, Homebrew, Scoop, WinGet | Yes                                              | No                             | No                                                                         |

A tagged RC takes precedence over manual previews on npm `next`. If any
package already points to a tagged RC, manual preview publishes its immutable
version without moving `next`. Install it by its exact version in that case.

New manual preparations embed the selected OCI repository in the CLI and
package the matching parser repository and digest in the npm launcher.
Recover older preparations from their original index and pins; do not rewrite
them to use the new coordinates.

Stable OCI promotion checks the current digest against the immutable tag named
by its version metadata. A lower SemVer keeps the current alias; equal versions
must have equal digests.

The editor stores receive only the stable VSIX. An RC GitHub release carries
its own VSIX, but that file embeds the same base extension version as stable.
Publishing it to a store would occupy the stable coordinate. The cleanup
workflow only previews old unstable releases; downstream pins may still use
their assets.

Use `pnpm test:release` and the `.github/scripts/test-*.py` entry points for
offline routing and publisher checks. Verify that protected tagged preflight
rejects replaced archives, RC cannot advance stable aliases, and a partial
publish can recover with the original index but rejects another one. Test
registry acceptance separately; a successful Verify is not publication proof.
