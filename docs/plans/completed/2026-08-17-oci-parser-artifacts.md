# Plan: OCI parser artifacts — closing the last github.com hole in the single-registry promise

**Status:** IMPLEMENTED — phases 0-5 and 7 shipped in the core; phase 6 (the wrapper repo) is not
part of this repository and remains open. See "What shipped" below for the deviations.
**Date:** 2026-08-17 (design), 2026-08-17 (implementation).
**Related:** `docs/plans/2026-06-09-oci-bundles.md` (the bundle format contract this reuses),
`internal/ocidigest`, `internal/ocibundle`, `internal/parsermanager`, `.github/workflows/release.yml`.

> This document supersedes the three competing drafts (minimal-diff `oci://`-in-url,
> enterprise-suite, supply-chain). The supply-chain shape is the base; the scope discipline of the
> minimal draft and two items from the enterprise draft (`store refs`, the shared ref parser) are
> grafted in. Six reviewer blockers survived verification and are folded in as **Phase 1
> prerequisites** — three of them are pre-existing security/correctness defects the feature would
> otherwise inherit and amplify. Two draft claims were **factually wrong** and are corrected here
> (§14).

---

## What shipped, and where it deviates from this plan

Recorded at implementation time so the next reader does not have to diff the plan against the tree.

**Shipped as designed:** phase 0 (PRs #247, #251, #252), phase 1 (#248), and phases 2, 3, 4, 5 and 7
in one arc. The locked decisions in §2 all held; nothing in §11 was reopened.

**Deviations, each with its reason:**

1. **`dist/parsers-oci.json` is NOT added to `.goreleaser.yml` `release.extra_files` (§7.7).** It
   cannot be: goreleaser runs inside `build`, and the manifest digest the file records does not
   exist until `publish-parsers-oci` has pushed. The record reaches consumers by two routes that do
   work — the launcher npm package (`packaging/npm/datamitsu/`, listed in its `files`) and the
   unstable prerelease, which downloads it as a separate job artifact.
2. **`ValidateOCI`'s signer rejection replaced the identity/issuer checks rather than joining
   them** (§2.6). Any shape of `signer` is now rejected, complete or not: the rejection is about the
   field being present at all. `ocibundle/seed.go` and `cmd/store.go` keep their own signer handling
   as defense in depth for non-config callers of the exported `SeedFromLayout`.
3. **The parsermanager dispatch goes through a `fetchOCIModule` package variable**, not a direct
   call (§5 step 4). `ociartifact`'s own `newClient` seam is unexported and unreachable from
   parsermanager, so without this the dispatch, the post-fetch verification and the store publish
   would have had no hermetic test at all.
4. **The gated e2e cases skip rather than fail** when the vendored config declares no
   registry-sourced parser (§12). The fixture is the wrapper's _published_ config, so until phase 6
   ships a pin there is nothing real to exercise; a hardcoded ref would have tested a pin nobody
   uses.
5. **Docker Hub mirroring is deferred** (§7.5, option 2), as the plan recommended. GHCR is the only
   parser channel today, matching unstable images, which already never reach Docker Hub.
6. **`internal/dockerfile` gained `Plan.RegistrySourcedParsers`** so `devtools dockerfile` warns
   when a generated parser stage would need registry egress from inside buildkit (§10, phase 3).

**Still open (not this repository):** phase 6 — the wrapper's `parsers.ts` placeholder,
`inject-oci-pin.ts`, `sync-datamitsu-version.ts`, the `test-fresh-init.ts` matrix leg, and the
`docs/get-started/oci-bundle.md` update, all under `shibanet0/datamitsu-config`.

**Human prerequisite, unchanged:** an org owner has to create `datamitsu-parsers` and
`datamitsu-parsers-unstable` as **public** GHCR packages. The first release will otherwise fail at
the anonymous-pull check — which is exactly what that check exists for (§7.4, §13.3).

---

## 0. The idea in one paragraph

datamitsu turns a tool's raw output into structured diagnostics with a Rust→WASM parser module.
That module ships exactly one way today: as an asset on a GitHub Release, referenced from config as
`url` + `hash`. It is therefore the **one artifact that always comes from github.com over HTTPS**,
which breaks the OCI bundle story's whole point — "mirror one registry and everything works". We add
a **second, digest-pinned source**: `parsers.<name>.oci = { ref, digest }`, pulled through the
existing `internal/ocidigest` client as a small OCI 1.1 artifact whose single `application/wasm`
layer digest **is** the mandatory config SHA-256. The config hash stays mandatory and unconditional;
the registry digest chain is added integrity, never a substitute. A second, free consequence: unstable
core builds run `goreleaser --snapshot`, so no release and no asset exist for them — but unstable
builds already push images to ghcr.io, so an OCI channel is the first way the wrapper's CI can ever
exercise an unreleased parser module.

**Out of scope, explicitly:** `oci` on any entity other than `parsers` (not apps, bundles, archives,
JARs, runtimes or remote configs); private-registry authentication (docker `config.json`, credential
helpers, custom CA — §13.2, the real ceiling); sigstore verification inside the binary (still
unimplemented, still rejected at validation); tag resolution on the parser path (structurally
impossible, §2.5).

---

## 1. Grounding (verified this session — do not re-research)

**The consumer half already exists and needs no changes.**
`ocidigest.PullBlob(ctx, repo, dgst string, size, maxBytes int64, displayName, destDir string) (string, error)`
(`pull.go:106`) materializes one digest-pinned blob into a temp file under `destDir`: declared-size
refused before the request (`pull.go:110`), stream through `io.MultiWriter(tmpFile, sha256)` wrapped
in `io.LimitReader(body, maxBytes+1)`, then three separate `httpretry.Permanent` failures —
`written > maxBytes`, `size >= 0 && written != size`, `hex(sha256) != wantHex` (`pull.go:220-229`).
Four attempts, a progress-based stall guard, the caller owns the temp path. That output contract is
byte-identical to `binmanager.downloadFileInternal`'s, which is what `parsermanager.ensureModule`
already consumes.

`PullManifest` (`pull.go:64-97`) re-hashes the response **body** (`sha256.Sum256(body)`) against the
pinned digest and never trusts `Docker-Content-Digest` — only `Resolve` reads that header
(`client.go:136`), and the parser path never calls `Resolve`. `acceptManifests` (`client.go:41-47`)
already lists `application/vnd.oci.image.manifest.v1+json`, so an OCI 1.1 artifact manifest is
retrievable with zero client changes. Manifests are capped at 4 MiB; a 404 yields
`ErrManifestNotFound` with bundle-specific hint text that must be re-wrapped on this path.

**No new dependency.** `go.mod:16-17` pins `opencontainers/go-digest v1.0.0` and
`opencontainers/image-spec v1.1.1`. In the module cache, `ocispec.Manifest` carries `ArtifactType`
(`manifest.go:27`) and `Subject` (`manifest.go:37`); `MediaTypeEmptyJSON` is `mediatype.go:34`.
There is no go-containerregistry, no ORAS, no regclient in the tree — everything is hand-rolled over
`internal/httpx`.

**The import graph is clean.** `go list -deps ./internal/ocidigest` yields only
env/hashutil/httpretry/httpx/logger/ui/utils/color/term/uievent/ldflags — no `config`, no
`binmanager`. `internal/config/validate.go` imports `internal/binmanager` (so binmanager can never
import config); `internal/ocibundle/paths.go` imports `internal/parsermanager` (so parsermanager can
never import ocibundle).

**`ensureModule` is already the right shape** (`parsermanager.go:292-347`): url check (`:297`,
message `parser %q has no url`), hash check (`:300`, "an empty hash is a configuration error, never
a silent hash-less download"), `moduleDir` (`:306`), bare `os.Stat` fast path (`:308`), singleflight
keyed on the content dir (`:315`), one fetch call (`:327`), atomic `os.Rename` (`:333`). Adding a
source branch is a ~6-line switch at one call site.

**The store key is the problem.** `cacheKey` (`parsermanager.go:354`) is
`hashutil.XXH3Multi([]byte(p.URL), []byte(p.Hash))`. Re-pointing a parser at a mirror silently
**moves** `.parsers/<name>/<key>`, so a bundle layer built from one config variant is never found by
a consumer running another. `ModuleStorePath` has exactly two non-test consumers, both in
`ocibundle/paths.go` (`:140` `expectedSubtrees`, `:246-250` `buildReVerifyIndex`).
`internal/dockerfile/storepaths.go:38-44` `parserSubtree` is hash-**LESS** (`.parsers/<module>`), so
the wrapper's committed `docker/Dockerfile*` and `oci-map*.json` do **not** move when the key
changes — only the bundle image must be rebuilt.

**The bundle half is done.** `ocibundle.go:50` `subtreeRoots` already whitelists `.parsers/`;
`expectedSubtrees` unconditionally injects every tool-referenced parser's `ModuleStorePath` even
under `--apps`; `buildReVerifyIndex` re-checks `module.wasm` against `p.Hash` after layout ("WASM
parser modules are the ideal re-verify case", `paths.go:236`).

**Failures are silent.** `internal/tooling/executor.go:663-676` logs a `Warn` and returns raw output
on any parser error; `runner.go:322-329` logs `Debug` on a Prewarm failure. A broken pin, a private
package, a GC'd manifest and a malformed artifact all present as "diagnostics look worse than
usual". Any CI meant to validate a pin must call a command that _forces_ the fetch and assert its
exit code.

**Publishing today.** `.goreleaser.yml` `checksum.extra_files` (`:85-87`) puts the module's SHA-256
into `checksums.txt`, which the `signs` block (`:184`) signs with cosign `sign-blob` (keyless);
`release.extra_files` (`:150-152`) uploads it to a real release. Unstable runs
`goreleaser release --snapshot --clean` (**without** `--skip=sign`), so `checksums.txt.sigstore.json`
**is** produced — confirmed from real unstable run 31676105843 (2026-08-13), whose goreleaser log
shows `signing cmd=cosign artifact=checksums.txt signature=dist/checksums.txt.sigstore.json`. The
same run confirms the snapshot-mangled asset name:
`datamitsu_parsers_0.0.0-unstable.20260813.604c8af-SNAPSHOT-604c8af.wasm`. `gh release list` returns
only `v0.1.1`…`v0.1.15` — **no unstable prerelease has ever been created**, so `publish-github` is
untested in production.

**No OCI tooling in CORE.** `crane`/`gcrane`/`cosign` are datamitsu-managed apps declared by the
WRAPPER (`src/datamitsu-config/apps.ts:44-47`), reachable in CORE CI only through
`pnpm dm exec crane` after `./.github/actions/setup-environment`. `oras` and `regctl` appear nowhere
in either repo. CORE's `Taskfile.yaml`, `package.json`, workflows and `.github/scripts/` contain zero
references to any of them.

**The wrapper skew is live.** `src/datamitsu-config/parsers.ts:18` pins `DATAMITSU_VERSION = "0.1.9"`
while `src/datamitsu-config/datamitsu.config.ts:102` `getMinVersion()` returns `"0.1.15"`. Nothing
detects it: `scripts/sync-datamitsu-version.ts` only rewrites `getMinVersion()` and never touches
`parsers.ts`. The wrapper's `package.json` depends on `@datamitsu/datamitsu` (currently an unstable
version), and the sync script reads that version from `dependencies`.

**Sizing.** The built module is **385,667 bytes** (~377 KiB). `binmanager.MaxBinarySize` is 500 MiB
and `ocibundle`'s compressed-blob cap is 2 GiB; neither is a sane ceiling for one wasm file.

---

## 2. Locked decisions

1. **Scope: `oci` is accepted on `config.Parser` and nowhere else.** Not apps, bundles, archives,
   JARs, runtimes or remote configs. Those already have a working https+hash path and a bundle path;
   widening the surface here buys nothing and multiplies the validation/test matrix.

2. **Schema: an optional pointer sub-object, not a URL scheme.** `Parser.OCI *ParserOCI`, modelled
   on the existing `Config.OCI *OCIRef`. Exactly one of `url`/`oci`, mutually exclusive, **no
   fallback chain** — an airgapped organization must be able to _prove_ there is no github egress.
   Rejected alternative and why: §11.1.

3. **Digest only, mandatory, structurally enforced.** `oci.digest` is required and is the artifact
   _manifest_ digest. `ociartifact` never calls `Resolve`/`ResolveCached`; `ociRefPattern`'s charset
   excludes `@` entirely and `:` outside the port position, so a `:tag`/`@digest` suffix cannot even
   parse (the comment at `validate.go:335-342` states exactly this intent). Beyond the obvious
   reason: `ocidigest/cache.go:17-19` says digest-cache entries never expire, and `runStoreClear`
   removes `{base}/store` while that cache lives under `{base}/cache` — one floating tag resolved
   once would be poisoned forever with no supported recovery. Tags are published for humans and
   mirroring tools and are never consulted by the core.

4. **The pivot: `manifest.layers[0].digest == "sha256:" + parser.hash`.** This makes the OCI digest
   chain and the policy-mandated config SHA-256 literally the same number. A registry serving a
   correctly-digested manifest that points at different content is rejected **before one payload
   byte is requested**. The hash is then checked twice more (streamed, then on the file on disk).

5. **Store key becomes content-only.** `cacheKey(p) = XXH3Multi([]byte("parser-v2"), []byte(p.Hash))`.
   The same module reached over `https://`, `oci://`, `file://` or a bundle layer lands in **one**
   directory. This is what lets a bundle producer, a registry-sourced consumer and a mirrored
   registry agree on `.parsers/<name>/<key>` without republishing anything. Still XXH3 per the
   hashing policy: an internal key never compared against anything external; `p.Hash` (SHA-256)
   still gates the content. The `"parser-v2"` prefix is domain separation so the next migration
   costs one character.

6. **Signatures: signed at publish, rejected in config.** CI cosign-signs the pushed manifest by
   digest (real, keyless, transparency-logged, verifiable out-of-band). `parsers.<n>.oci.signer` is
   **rejected at validation** with the same wording `ocibundle/seed.go:164` uses. No sigstore
   dependency is added. Accepting-and-ignoring a `signer` would let a config assert a guarantee this
   binary does not deliver — strictly worse than a loud error. **Correction to the existing code:**
   `ValidateOCI` (`validate.go:376-382`) currently _accepts_ `signer` at load and defers the
   rejection to seed time. That asymmetry is fixed in the same arc — both surfaces reject at load,
   before any network (§4, §12 note).

7. **`--no-oci` keeps its current meaning: "skip the bundle accelerator".** It does **not** block an
   explicitly declared parser `oci` source. Grounded in three facts: `env.NoOCI()` has exactly two
   non-test call sites (`ocibundle/seed.go:74`, `runtimeconfig.go:66`); the flag's help text is
   "Disable OCI bundle store seeding"; the bundle degrades gracefully (`AutoSeed` warns and falls
   through) whereas a declared source is the only route to the bytes. `DATAMITSU_OFFLINE` remains
   the single hard network gate, already enforced at the transport in `authedGet`
   (`client.go:150`). The flag's `--help` string is deliberately **not** touched, so none of the
   goldens embedding the "Global Flags:" block move.

8. **Publisher is a hand-rolled Node script, not `oras`.** Changed from the draft on review. Reasons:
   node is present on `ubuntu-latest` and needs only `GITHUB_TOKEN`; it avoids adding a new
   third-party action to a job holding `packages: write` + `id-token: write`; it keeps the scorecard
   pinned-dependencies posture unchanged; and it gives exact control over `artifactType`, the layer
   media type and the annotations, with no dependence on an undocumented `oras push --format json`
   field layout. Model: the wrapper's `scripts/oci-bundle-postprocess.ts` `Registry` class (POST
   `/v2/<repo>/blobs/uploads/`, PUT manifest, read `Docker-Content-Digest`). `oras` remains a
   documented fallback; `crane` is retained for **mirroring only**. Rejected producers: §11.4.

9. **The wrapper's DEFAULT published config keeps the https url.** Only
   `datamitsu.config.oci-ghcr.js` / `.oci-dockerhub.js` carry the `oci` pin, exactly as the bundle
   pin works today. `npm i && datamitsu lint` needs no registry.

10. **The GitHub prerelease for unstable builds stays OPTIONAL.** `publish-github`'s gate on
    `github.event.inputs.create_github_prerelease == 'true'` is unchanged and the input keeps
    `default: false`. After Phase 4 it is not even on the critical path — the OCI channel publishes
    unconditionally on every unstable run — so the prerelease becomes a human-debugging convenience
    that happens to carry the module too.

11. **`.goreleaser.yml` `snapshot.version_template` is NOT changed.** The OCI channel makes it moot
    and the blast radius (archives, nfpms, the vsix signature path,
    `packaging/action/src/checksums.ts`, `editors/vscode/scripts/gen-binaries.ts`,
    `normalize-binaries.sh`) is not worth it. `stage-wasm-artifact.sh` already reads the asset name
    from `checksums.txt` instead of re-templating it.

12. **`devtools parsers path` is NOT added.** `datamitsu config show | jq .parsers` already prints
    the resolved declaration and `store refs` (Phase 5) covers enumeration. Deferred.

---

## 3. Prerequisites: three pre-existing defects this feature would otherwise inherit

These are **not** nice-to-haves. Each was found by an adversarial review, each was re-verified in the
tree this session, and each is either a security hole the new field widens or a migration trap the
new store key springs. They are Phase 1, before any schema work.

### 3.1 A full bundle seed places `.parsers/` content that nothing verifies

**Verified.** `cmd/store.go:156-158` sets `opts.Needed` only when `--apps` is non-empty, so
`datamitsu store seed <ref>@<digest>` — the documented airgap recipe — leaves `Needed` nil,
`Options.demandDriven()` is false, and `seedFrom` (`seed.go:151-155`) leaves `expected == nil`.
`classifyLayers` (`seed.go:258-289`) then skips its `if expected != nil` filter and queues **every**
annotated layer. `seedLayer` (`seed.go:326-336`) re-verifies only `if spec, ok := reVerify[job.subtree]`,
and `buildReVerifyIndex` (`paths.go:236-251`) indexes exclusively the subtrees of the **consumer's
currently-pinned** parsers. So a bundle layer annotated
`com.datamitsu.subtree=.parsers/core/<any-other-key>` passes `validateSubtree` (`.parsers/` is in
`subtreeRoots`) and is `placeSubtree`d into the store **with no SHA-256 check at all**.

It is inert only until the consumer's config pins the hash whose key equals that directory — at which
point `ensureModule`'s bare `os.Stat` fast path returns it and `compiledFor` executes it, with the
mandatory config SHA-256 never having been compared. Content-only keying (§2.5) makes the target
directory deterministically derivable from the publicly published hash and identical across every
config variant, so one pre-planted subtree would hit every consumer.

**Calibration, both ways.** This is **not** RCE: `runtime.go:56-62` uses `wazero.NewRuntime(ctx)`
with no host modules and no WASI ("no host imports, no WASI — the parser is pure computation over
byte buffers"). But parsers produce the diagnostics that drive `check`/`lint` exit codes, and
executor failures only warn — so a module that returns zero diagnostics **silently disables the
entire lint/security gate with a green CI**.

**Fix (Phase 1):**

- In `classifyLayers`/`seedLayer`, a layer whose subtree is under `.parsers/` and which has **no**
  `reVerify` entry is **skipped with a Warn**, never placed. Chosen over fatal: a shared org bundle
  legitimately carries modules for several config variants, and "a partial bundle is a valid partial
  seed" is an existing, deliberate property. Skipping costs nothing — the consumer cannot load a
  module its config does not declare.
- Re-verify `module.wasm` against `p.Hash` on `ensureModule`'s `os.Stat` fast path. ~1 ms for
  385,667 bytes, once per `Prewarm`/first `Acquire` (`compiledFor` caches the compiled module by key,
  so this is not per-parse). This is the structural fix: the store becomes self-healing regardless of
  how the bytes arrived. A mismatch removes the directory and falls through to a normal fetch.
- Regression test: pre-create `{parsersPath}/<name>/<key>/module.wasm` with wrong bytes and assert
  `LoadWASMBytes` fails. **That test fails today** — which is precisely why it is the right guard.

### 3.2 GITHUB_TOKEN can be relayed to any registry a `ref` names

**Verified.** `NewResolverForHost` (`client.go:85-95`) sets `token: os.Getenv("GITHUB_TOKEN")`
**unconditionally, for every host**. The only guard, `shouldAttachGitHubToken(u.Host)`
(`client.go:220-222`), is evaluated inside `fetchToken` against `u.Host` parsed from
`params["realm"]` — i.e. from the `WWW-Authenticate` header, which is **registry-controlled**.

Attack: a config points a `ref` at `evil.example/x/y`; `authedGet` gets a 401 whose challenge is
`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:victim-org/private:pull"`;
`u.Host == "ghcr.io"` so `req.SetBasicAuth("x-access-token", r.token)` (`client.go:252`) mints a real
GHCR token at the attacker-chosen scope; `storeBearerToken(repo, token)` then `return do(token)`
(`client.go:194-196`) sends `Authorization: Bearer <that GHCR token>` **to evil.example**.

This is pre-existing on the bundle path, but this feature adds a second, per-parser config field
whose stated purpose is to be re-pointed at third-party hosts ("one host edit per key"), and the
existing code comment describes the gate as if it were host-scoped — propagating the wrong mental
model into the deferred auth work.

**Fix (Phase 1, ~2 lines):** gate on the resolver's **own configured registry** in addition to the
challenge realm:

```go
if r.token != "" && r.registry == "ghcr.io" && shouldAttachGitHubToken(u.Host) {
	req.SetBasicAuth("x-access-token", r.token)
}
```

Tests: a resolver for `evil.example` answering 401 with `realm="https://ghcr.io/token"` must send no
Basic auth and must forward no bearer token to `evil.example`. Update the
`shouldAttachGitHubToken` doc comment to say the realm check is necessary but **not sufficient**.

### 3.3 The full-pull marker makes the store-key migration a silent no-op

**Verified.** `markerPath(storeRoot, digest)` (`ocibundle.go:118-121`) keys the marker on the bundle
digest **alone**; `seedFrom` short-circuits on bare existence (`seed.go:159-161`). Sequence: a user
runs a full `datamitsu store seed` → marker written for digest D. They upgrade to the new core →
`.parsers/<name>/<key>` relocates. They re-run `store seed` → the marker for D still exists → the
function returns having laid down **nothing**. Later, `lint` under `DATAMITSU_OFFLINE=1`: `AutoSeed`
short-circuits on `env.Offline()`, `ensureModule`'s stat misses, the fetch is refused, and the
executor warns and returns raw output. The airgap case the feature exists for degrades silently.

**Fix (Phase 1):** the marker already records `Subtrees` (`ocibundle.go:115`). Compare the recorded
list against `expectedSubtrees(cfg, storeRoot, nil, nil)` and re-pull on a mismatch, instead of the
bare existence check. A store-layout change then self-heals. `datamitsu store clear` also works (the
marker lives inside `{store}/.oci-seeded`) and is the documented manual escape hatch — but it must
not be the _only_ one.

**Related, cheap, same phase:** make `runStoreClear` also remove the digest cache
(`{cache}/.oci-digests`). Today `store clear` wipes `{base}/store` while `digestCachePath` writes
under `{base}/cache`, and cache entries never expire — so a mis-resolved floating tag has no
supported recovery path.

---

## 4. Config declaration

### 4.1 Go — `internal/config/config.go`

Replaces `Parser` at `config.go:359-372`; `ParserOCI` goes immediately after it.

```go
// ParserOCI declares a parser module published as an OCI artifact: the registry
// repository plus the mandatory sha256 digest pinning the artifact manifest.
// Deliberately mirrors OCIRef so there is one reference vocabulary, one
// validator and one set of error strings.
type ParserOCI struct {
	// Ref is the full repository reference including the registry host
	// (e.g. "ghcr.io/datamitsu/datamitsu-parsers"). Tags and digests are not
	// allowed inside the ref — content is pinned exclusively by Digest.
	Ref string `json:"ref"`
	// Digest pins the artifact manifest in "sha256:<64 hex>" form. The single
	// layer inside that manifest must have digest "sha256:" + Parser.Hash, so
	// the pin is checked before the payload is requested.
	Digest string `json:"digest"`
	// Signer is REJECTED at validation while sigstore verification is
	// unimplemented (mirrors ocibundle/seed.go:164). The field exists so the
	// eventual implementation reuses the one signer vocabulary rather than
	// inventing a second, and so a config that sets it fails loudly instead of
	// silently getting no verification.
	Signer *OCISigner `json:"signer,omitempty"`
}

// Parser declares a WASM output-parser module as a hash-pinned data artifact.
// The module is fetched, SHA-256 verified, then loaded into the sandboxed WASM
// runtime. Exactly one source (URL or OCI) must be declared.
type Parser struct {
	// URL is the https:// source (file:// additionally, in a dev-link build).
	// omitempty so an oci-sourced entry does not marshal `"url":""`; a
	// url-sourced entry marshals byte-identically to today, so no existing
	// config's execution-cache key changes on upgrade.
	URL string `json:"url,omitempty"`
	// Hash is the mandatory SHA-256 (64 lowercase hex) of the .wasm module, for
	// EVERY source. For an OCI source it is simultaneously the expected layer
	// blob digest.
	Hash string `json:"hash"`
	// OCI sources the module from a registry. omitempty is load-bearing: the
	// whole Config is marshaled into the execution-cache invalidation key.
	OCI *ParserOCI `json:"oci,omitempty"`
	// NOTE: there is intentionally no `version` field. A module reports its own
	// build-injected version via its WASM `describe` export; declaring it here
	// too would only let the declared and actual versions drift.
}
```

### 4.2 TypeScript — `config/config.d.ts`

Replaces `interface Parser` at `config.d.ts:743`; `ParserOCI` goes before it. Fields alphabetized to
match the file's existing style. `internal/config/config.d.ts` is the byte-identical embedded copy
(verified identical today), synced by `Taskfile.yaml` (`cp ./config/config.d.ts
./internal/config/config.d.ts`) and guarded by `TestConfigDTSCopiesAreByteIdentical`
(`internal/config/oci_test.go:185`).

```ts
/**
 * A parser module published as an OCI artifact and pulled from a registry. Lets an air-gapped
 * organization mirror one registry and get everything, including diagnostics parsing.
 */
interface ParserOCI {
  /**
   * Artifact manifest digest, "sha256:" followed by 64 lowercase hex characters. Mandatory —
   * a tag never pins content, and the module hash changes on every build anyway.
   */
  digest: string;

  /**
   * Full repository reference including the registry host (e.g.
   * "ghcr.io/datamitsu/datamitsu-parsers"). No default host; tags and digests are not allowed
   * inside the ref.
   */
  ref: string;

  /**
   * RESERVED. Setting it is currently a config error: this build cannot verify sigstore
   * signatures. The mandatory `hash` verifies the module bytes.
   */
  signer?: OCISigner;
}

interface Parser {
  /**
   * SHA-256 hash (64 lowercase hex characters) of the .wasm module. Mandatory per the security
   * policy for every source — an empty or malformed hash is a config error. For an `oci`
   * source it must also equal the artifact's single layer blob digest, so a mismatch is
   * rejected before the module is downloaded.
   */
  hash: string;

  /**
   * Registry-sourced module (OCI artifact). Exactly one of `oci` or `url` must be set.
   */
  oci?: ParserOCI;

  /**
   * URL of the .wasm module. Exactly one of `oci` or `url` must be set.
   */
  url?: string;

  // NOTE: no `version` field by design. The module reports its own
  // build-injected version through its WASM `describe` export (see
  // `datamitsu devtools parsers list`); declaring it here too would only let
  // the declared and actual versions drift.
}
```

### 4.3 What the wrapper's OCI variant emits

```js
parsers: {
  core: {
    hash: "612a5c2da01d74a35fc0a27ac01ac9ae92442cbdc8bc6ddddee4a32642a9d73f",
    oci: {
      digest: "sha256:<64 hex — the ARTIFACT MANIFEST digest>",
      ref: "ghcr.io/datamitsu/datamitsu-parsers",
    },
  },
},
oci: { digest: "sha256:<bundle>", ref: "ghcr.io/shibanet0/datamitsu-config" },
```

Inside a firewall — one host edit per key, digests untouched:

```js
parsers: { core: { hash: "612a…d73f", oci: { digest: "sha256:<same>", ref: "harbor.corp/dm/datamitsu-parsers" } } },
oci: { digest: "sha256:<same>", ref: "harbor.corp/dm/datamitsu-config" },
```

**Two digests, because they pin two different things.** `oci.digest` pins the _packaging_ (which
layer descriptor, what artifactType, which annotations) and lets the pull abort before spending
bandwidth. `hash` pins the _payload_: it is what makes the store content-addressed and what
`buildReVerifyIndex` re-checks after a bundle seed. Neither subsumes the other; the publish job emits
both from one source so they cannot drift.

### 4.4 Validation — `internal/config/validate.go`

Extends `ValidateParsers` (`validate.go:394-427`). Errors keep aggregating into the existing
`config validation failed:\n  ` block, sorted by parser name.

- `parser %q: exactly one of url or oci is required`
- `parser %q: url and oci are mutually exclusive (declare one source; use a config layer to override)`
- `parser %q: hash is required (SHA-256)` — **existing string, verbatim, still unconditional**
- `parser %q: hash must be a valid SHA-256 hex string (64 lowercase hex characters)` — existing
- `parser %q: oci.ref %q is not a valid repository reference (expected host[:port]/path, lowercase, no tag and no digest)`
- `parser %q: oci.ref %q must include the registry host as its first segment (e.g. ghcr.io/owner/repo)`
- `parser %q: oci.digest is required (sha256:<64 hex>)`
- `parser %q: oci.digest %q must be "sha256:" followed by 64 lowercase hex characters`
- `parser %q: oci.signer is set but signature verification is not implemented in this build; remove signer (the mandatory hash still verifies the module bytes)`

Ref/host rules come from `ociref.Parse`; `isValidSHA256Hex` (already used by `ValidateOCI`) is reused
for the digest tail. Per §2.6, `ValidateOCI` gains the symmetric load-time `signer` rejection.

**Breaking test change, must be planned:** the message `parser %q: url is required` disappears. Two
sites assert it — `internal/config/parsers_test.go:86` and `:151` (inside
`TestValidateParsers_AggregatesAllErrors`) — and both must be rewritten. (`coverage_paths_test.go:67`
also matches `"url is required"` but is a `BinaryOsArchInfo` case for a different entity and is
unaffected — do not chase it.) `ensureModule`'s own runtime message is `parser %q has no url`
(`parsermanager.go:298`), a different string, and becomes an exactly-one-source check.

---

## 5. Resolution: declaration → verified bytes in the store

**Step 0 — load time.** `cmd/config_loader.go` already calls `config.ValidateParsers(cfg.Parsers)`.
Nothing touches the network until it passes, so a typo is a load-time config error rather than a
four-attempt retry storm.

**Step 1 — demand.** `internal/runner/runner.go` constructs `parsermanager.New(sc.cfg.Parsers)` when
`len(cfg.Parsers) > 0 && !parsingDisabled()`; `runner.go:324` calls `Prewarm` → `Acquire` →
`ensureModule`.

**Step 2 — store fast path** (`parsermanager.go:306-311`), now with the §3.1 re-verify:
`dir := moduleDir(name, p)`; `os.Stat(dir/module.wasm)`; on a hit, re-hash against `p.Hash` and
return. A bundle-seeded module short-circuits with zero registry traffic regardless of declared
source. This is where the store-key change earns its keep.

**Step 3 — the keystone** (`parsermanager.go:349-357`):

```go
// parserKeyVersion namespaces the store key so a future change to its inputs is
// a one-character edit instead of another silent relocation.
const parserKeyVersion = "parser-v2"

// cacheKey is the XXH3-128 content-addressed key for a parser module. It keys on
// the SHA-256 ALONE, not on the transport: the hash fully identifies the single
// module.wasm the directory holds, so the same module reached over https://,
// oci://, file:// or an OCI bundle layer lands in ONE directory. That is what
// lets a bundle producer, a registry-sourced consumer and a mirrored registry
// agree on .parsers/<name>/<key> without republishing anything. XXH3 (not a
// crypto hash) is correct per the hashing policy: this value is never compared
// against anything external — p.Hash still gates the content.
func cacheKey(p config.Parser) string {
	return hashutil.XXH3Multi([]byte(parserKeyVersion), []byte(p.Hash))
}
```

`moduleDir` / `ModuleStorePath` / `WASMFileName` are otherwise untouched, so `ocibundle/paths.go`
follows automatically and `dockerfile/storepaths.go`'s hash-less `parserSubtree` does not move.

**Step 4 — singleflight + dispatch** (`parsermanager.go:315-345`). Unchanged shape; replace the
single call at `:327`:

```go
var tmpPath string
var err error
if p.OCI != nil {
	// The registry digest chain verifies transport integrity; p.Hash remains
	// the authoritative content anchor and is checked twice more below.
	tmpPath, err = ociartifact.FetchParserModule(ctx, p.OCI.Ref, p.OCI.Digest, p.Hash, dir, name)
	if err == nil {
		// Post-hoc re-check on the materialized file. Logically redundant given
		// the manifest pivot and PullBlob's stream hash, and deliberately kept:
		// it is the only check that mentions p.Hash AFTER the bytes exist on
		// disk, so "the config hash is verified on every transport" stays true
		// by grep rather than by reasoning. ~1 ms for 377 KiB.
		if vErr := binmanager.VerifyFileHashPublic(tmpPath, p.Hash, binmanager.BinHashTypeSHA256); vErr != nil {
			_ = os.Remove(tmpPath)
			err = fmt.Errorf("hash verification failed: %w", vErr)
		}
	}
} else {
	// allowLocalFile: unchanged. file:// stays double-locked by
	// ldflags.LocalArtifactsEnabled() + this per-call bool.
	tmpPath, err = binmanager.DownloadAndVerifySHA256(ctx, p.URL, p.Hash, dir, name, true)
}
if err != nil {
	return nil, fmt.Errorf("download+verify: %w", err)
}
```

**`internal/binmanager` is NOT modified.** `DownloadAndVerifySHA256`'s exported signature, the
`allowLocalFile` bool and the whole `file://` two-lock suite in `download_test.go` stay exactly as
they are. That is a deliberate property: binmanager's defining invariant is "`downloadAndVerifyInternal`
is the one function that takes `expectedHash`, `verifyFileHash` is the one call site", and threading
a hash in as an _address_ would quietly change that function's meaning.

**Step 5 — `internal/ociref` (new, zero-dependency leaf).** Both reviewers independently objected to
putting the shared reference parser inside `ociartifact`: `ociartifact` imports `ocidigest`, so
`internal/config` would transitively depend on the OCI registry client, `internal/ui` and the mpb
progress stack just to run a regex. `internal/ociref` imports nothing but stdlib.

```go
var ErrRefSyntax = errors.New("not a valid repository reference (expected host[:port]/path, lowercase, no tag and no digest)")
var ErrRefNoHost = errors.New("must include the registry host as its first segment (e.g. ghcr.io/owner/repo)")

// Parse splits a repository reference into its registry host and repository
// path. Single definition of datamitsu's reference grammar, replacing the three
// copy-pasted strings.Cut(ref, "/") calls at ocibundle/seed.go:124,
// ocibundle/status.go:58 and cmd/store.go:134. Callers wrap the sentinels with
// their own prefix so existing messages stay byte-identical.
func Parse(ref string) (host, repo string, err error)
```

**Ordering is load-bearing:** the pattern check runs first and the host check only after it matches,
because `TestValidateOCI_MalformedRef` (`internal/config/oci_test.go:55-67`) discriminates the two —
`"ghcr.io"` (no path) must fail the _syntax_ branch while `"owner/repo"` matches `ociRefPattern` and
must fail the _host_ branch. That is what `ValidateOCI`'s `switch`/`default` encodes today.

**One call site changes behaviour:** `cmd/store.go:134`'s ref comes straight from an unchecked CLI
arg, and today only produces `reference %q has no repository path`. `ociref.Parse` adds full pattern
validation (lowercase-only, digits-only port, restricted charset). That is an improvement, but it IS
a CLI behaviour change and belongs in the release notes. **Pre-existing bug it exposes:**
`store.go:127` cuts at the _first_ colon, so `localhost:5000/o/r:latest` yields `ref="localhost"`,
`tag="5000/o/r:latest"` → rejected by the `strings.Contains(tag, "/")` check — `--resolve-tag` can
never work with a ported host. Fix it in the same commit (cut at the last colon after the last `/`).

**Step 6 — `internal/ociartifact` (new).** Imports only `ociref`, `ocidigest`, `httpx`, `image-spec`,
stdlib. Takes plain strings, never `config`.

```go
const (
	// ArtifactTypeParsers is the manifest-level artifactType of a datamitsu
	// parser module artifact — a vendor-tree media type, which is exactly what
	// OCI image-spec v1.1 expects for artifactType.
	ArtifactTypeParsers = "application/vnd.datamitsu.parsers.v1+wasm"
	// MediaTypeWasm is the only accepted layer media type. Closed allowlist: we
	// are the sole publisher of this artifact.
	MediaTypeWasm = "application/wasm"
	// MaxParserModuleBytes caps a parser module blob. The real module is
	// 385,667 bytes; 64 MiB is generous headroom and far below
	// binmanager.MaxBinarySize (500 MiB) or ocibundle's 2 GiB blob cap, neither
	// of which is a sane ceiling for one wasm file.
	MaxParserModuleBytes int64 = 64 << 20
)

var errIntegrity = errors.New("oci artifact integrity")

// IsIntegrityError reports whether err is an artifact-policy violation. These
// are fatal: never retried, never degraded to a different source.
func IsIntegrityError(err error) bool { return errors.Is(err, errIntegrity) }

// registryClient is the narrow slice of *ocidigest.Resolver this package needs.
// It exists as a test seam: NewResolverForHost hardcodes scheme "https" on an
// unexported field (ocidigest/client.go:90), so out-of-package code cannot be
// pointed at an httptest server. Deliberately NOT solved with an
// insecure-registry env var, which would ship a production TLS downgrade switch.
type registryClient interface {
	PullManifest(ctx context.Context, repo, digest string) ([]byte, error)
	PullBlob(ctx context.Context, repo, digest string, size, maxBytes int64, displayName, destDir string) (string, error)
}

var newClient = func(host string) registryClient { return ocidigest.NewResolverForHost(host) }

// SelectWasmLayer validates an already digest-verified artifact manifest against
// the parser artifact contract and returns its single wasm layer descriptor.
// Pure: no I/O, no network — the whole policy is table-testable.
func SelectWasmLayer(manifestBytes []byte, wantSHA256 string) (ocispec.Descriptor, error)

// FetchParserModule materializes the parser module pinned by ref+digest into a
// temp file under destDir, verified against wantSHA256. Returns the temp path;
// the caller owns (and removes) it — the same contract as
// binmanager.DownloadAndVerifySHA256 and ocidigest.PullBlob.
func FetchParserModule(ctx context.Context, ref, digest, wantSHA256, destDir, displayName string) (string, error)
```

`FetchParserModule`, in order:

1. `httpx.GuardOffline("oci parser module pull of " + ref)` — early and explicit. `authedGet` already
   guards, but it does not wrap the error `httpretry.Permanent` (unlike `binmanager/download.go:158`),
   so an offline pull reaching `PullBlob`'s loop burns four attempts, ~7 s of backoff and four
   aborted progress bars. Phase 1 fixes that at the source too.
2. `host, repo, err := ociref.Parse(ref)`.
3. `c := newClient(host)`.
4. `raw, err := c.PullManifest(ctx, repo, digest)` — the strong link (body re-hashed, header never
   trusted, 4 MiB cap). Re-wrap `ErrManifestNotFound` so the message names the parser instead of
   inheriting the bundle-specific "was the bundle garbage-collected?" hint.
5. `desc, err := SelectWasmLayer(raw, wantSHA256)`.
6. `return c.PullBlob(ctx, repo, desc.Digest.String(), desc.Size, MaxParserModuleBytes, displayName, destDir)`.

**`SelectWasmLayer` policy**, in order, every failure wrapping `errIntegrity` and naming both digests:

1. Probe `{mediaType, manifests}` first (same technique as `ocibundle.resolvePlatformManifest`): an
   OCI index, a docker manifest list, or any non-empty `manifests` array → **reject**. A wasm module
   is platform-independent; accepting an index would drag in `selectDescriptor`'s
   linux-requires-`com.datamitsu.libc` contract, which is meaningless here and unexported anyway.
2. `m.MediaType == ocispec.MediaTypeImageManifest`.
3. `m.ArtifactType == ArtifactTypeParsers`.
4. `m.Subject == nil` — a manifest with a subject is a referrer (signature/SBOM attached to something
   else), not the module.
5. `len(m.Layers) == 1`.
6. `m.Layers[0].MediaType == MediaTypeWasm`.
7. **`m.Layers[0].Digest.String() == "sha256:" + wantSHA256`** — the pivot.
8. `0 < m.Layers[0].Size <= MaxParserModuleBytes`.

**Deliberately NOT a rule: the config blob's media type.** The draft required
`m.Config.MediaType == ocispec.MediaTypeEmptyJSON` as a hard reject. That turns a producer-tooling
detail into a consumer integrity rule and couples every already-published pin to whatever the
publisher emits; any future producer swap that writes a typed config blob would break every pinned
config with a fatal, non-degradable error. The trust anchor is rule 7. The producer still emits
`application/vnd.oci.empty.v1+json` (§7) — it is a _publishing_ invariant, asserted in CI, not a
consumer gate. The consumer never fetches the config blob at all.

**Step 7 — publish.** `os.Rename(tmpPath, wasmPath)` — same directory, same filesystem, atomic
(`parsermanager.go:333`, unchanged).

**Step 8 — load.** `LoadWASMBytes` / `compiledFor` / `Acquire` / `Prewarm` / `Prefetch` /
`ListCapabilities` need no signature change; they are already source-agnostic. `capabilities.go`'s
dedup by `cacheKey` now means "dedup by module content", which is strictly more correct.

**Bundle vs artifact precedence — no new rule needed.** `AutoSeed` → `SeedBundle` →
`expectedSubtrees` (which unconditionally injects every tool-referenced parser's `ModuleStorePath`,
even under `--apps`) → `seedLayer` lays it out **and** re-verifies against `p.Hash`; then
`ensureModule`'s stat hits and nothing is fetched. With the old `XXH3(url,hash)` key this silently
failed whenever producer and consumer pinned different sources. The standalone artifact is a fallback
for what the bundle did not carry, not a competitor. Declaring both is legal, expected and the
recommended enterprise config.

**Step 9 — the ordering fix.** `sc.parserMgr.Prewarm` runs at `runner.go:324` while
`ocibundle.AutoSeed` runs at `runner.go:542`. On a cold store every `lint`/`check`/`fix` therefore
fetches the parser over the network **before** the seed step gets a chance. Move the `AutoSeed` call
ahead of `Prewarm`, preserving its `if sc.binMgr != nil` guard (`runner.go:538`). No signature
change: `expectedSubtrees` already covers parsers regardless of the `Needed` list.

Two mechanics the draft got wrong or omitted, corrected here:

- Prewarm failure is a **`log.Debug`**, swallowed and invisible without `-v` — **not** a hard failure
  under `DATAMITSU_OFFLINE=1`. The real symptom is the silent degradation to raw output later, in the
  executor. The reorder is still worth doing (it avoids a network fetch on a cold store), but the
  justification is "wasted network + a broken airgap path", not "hard failure".
- Moving `AutoSeed` above line 322 puts seed progress bars **before** the phase header printed at
  `runner.go:339-352`, changing interactive output ordering. Acceptable, but check it by eye.

---

## 6. Verification: the trust chain, end to end

| Link                          | Enforced by                                                                     | What it proves                                                                 |
| ----------------------------- | ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| Config is the only trust root | `ValidateParsers` at load + `ensureModule:299-303`                              | `hash` is a 64-lowercase-hex SHA-256, mandatory, never optional                |
| Manifest body                 | `PullManifest` (`pull.go:92-96`), `sha256.Sum256(body)`                         | The bytes are the pinned manifest; `Docker-Content-Digest` is never read       |
| Manifest policy               | `SelectWasmLayer`, 8 rules, culminating in `layers[0].digest == "sha256:"+hash` | A substituted payload is rejected **before one blob byte is requested**        |
| Blob stream                   | `pullBlobOnce` (`pull.go:220-229`)                                              | Three independent permanent failures: over-limit, size mismatch, hash mismatch |
| File on disk                  | `binmanager.VerifyFileHashPublic`                                               | "The config hash is verified on every transport" is true by grep               |
| Store fast path               | §3.1 re-verify on `os.Stat` hit                                                 | The store is self-healing regardless of how the bytes arrived                  |
| Bundle path                   | `buildReVerifyIndex` + §3.1 skip rule                                           | Seeded parser content is either config-verified or not placed                  |
| Atomic publish                | `os.Rename` inside the content-addressed dir                                    | No torn module is ever observable                                              |

**Trusted:** only bytes whose SHA-256 equals a value written in the config; `crypto/sha256`; the OS
TLS trust store (transport only).

**Not trusted:** the `Docker-Content-Digest` header on this path; the registry's TLS identity as a
content guarantee; the blob redirect target (GHCR 307s to a CDN — `net/http` strips `Authorization`
cross-host and `httpx.hardenedRedirect` caps at 10 hops and refuses any HTTPS→HTTP downgrade); the
registry host itself (mirror-swappable); any tag; layer/manifest annotations as authorization — they
are read only from digest-verified bytes and only to **reject**, never to widen.

**Signatures, stated honestly.** `go.mod` has no sigstore/cosign dependency; `ocibundle/seed.go:164`
hard-errors when `oci.signer` is set; `cmd/store.go` prints "Signer: pinned (verification not yet
supported by this build)". **The binary verifies no signatures at all.** Therefore: (a) CI
cosign-signs the pushed manifest by digest — real, keyless, transparency-logged, verifiable
out-of-band with `cosign verify --certificate-identity … --certificate-oidc-issuer
https://token.actions.githubusercontent.com`; (b) `parsers.<n>.oci.signer` is rejected at validation;
(c) the module's SHA-256 remains inside the cosign-signed `checksums.txt`; (d) at publish time the
job asserts `layers[0].digest == "sha256:" + <the checksums.txt entry>`, binding the OCI publication
to the already-signed checksums file. **Docs must not imply datamitsu checks a signature.**

**Introspection** (CLAUDE.md "Introspectable by Design"). No new env var, no new runtime parameter,
so `runtimeconfig.Effective` is unchanged and `test/cli/config_test.go TestConfigRuntime` does not
move. The effective parser source is already mechanically queryable:

```bash
datamitsu config show | jq '.parsers.core'
# -> {"hash":"612a…","oci":{"digest":"sha256:…","ref":"ghcr.io/datamitsu/datamitsu-parsers"}}
datamitsu config runtime | jq '{offline, noOci, ociRegistry}'   # all three fields already exist
datamitsu store refs --oci-only                                  # Phase 5: what must I mirror?
```

---

## 7. Publishing pipeline

### 7.1 Artifact contract (pinned by a Go test over a golden manifest, not by prose)

- manifest `application/vnd.oci.image.manifest.v1+json` — already in `ocidigest.acceptManifests`
- `artifactType: application/vnd.datamitsu.parsers.v1+wasm`
- config `application/vnd.oci.empty.v1+json` (digest
  `sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a`, 2 bytes) — a publishing
  invariant asserted in CI, not a consumer gate (§5 step 6)
- exactly **one** layer, `application/wasm`, **uncompressed**, annotated
  `org.opencontainers.image.title=datamitsu_parsers_<version>.wasm`, plus `com.datamitsu.kind=parser`
  (reusing `ocibundle.AnnotationKind`) and `com.datamitsu.parsers-version=<version>` for humans,
  mirrors and policy engines. The consumer reads none of these for authorization.
- no index, no `subject`

Deliberately **not** bundle-consumable: `ocibundle.layerCompressionFor` accepts only tar+gzip and
tar+zstd. That is correct — the bundle carries parsers as `.parsers/` tar subtrees, the standalone
artifact carries the raw wasm, and both resolve to the same store path.

### 7.2 New job `publish-parsers-oci`

Inserted alphabetically between `publish-npm` (release.yml:467) and `publish-open-vsx` (:529).
**Not** an extension of `build`: `build`'s permissions block (`:12-17`) is `actions: read` /
`attestations: write` / `contents: write` / `id-token: write` — no `packages:` scope — and it already
runs goreleaser with four write-scoped tokens. **Not** an extension of `publish-docker`'s 2×2 matrix,
whose `type=raw,value=unstable,enable=<is_unstable && dockerhub>` tag rule is already dead code (the
push step at `:396` is gated to ghcr for unstable).

```yaml
publish-parsers-oci:
  if: |
    always() &&
    !cancelled() &&
    needs.build.result == 'success'
  name: Publish WASM Parser Module (OCI)
  needs:
    - build
    - determine-release-type
    - validate-stable
    - generate-unstable-version
  outputs:
    digest: ${{ steps.push.outputs.digest }}
    ref: ${{ steps.push.outputs.ref }}
    sha256: ${{ steps.push.outputs.sha256 }}
    tag: ${{ steps.push.outputs.tag }}
  permissions:
    attestations: write
    contents: read
    id-token: write
    packages: write
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
      with:
        ref: ${{ needs.determine-release-type.outputs.checkout_ref }}
    - if: ${{ !env.ACT }}
      name: Download build artifacts
      uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
      with:
        name: release-artifacts
        path: .
    - name: Install cosign
      uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2
    - env:
        GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        IS_STABLE: ${{ needs.determine-release-type.outputs.is_stable }}
        UNSTABLE_TAG: ${{ needs.generate-unstable-version.outputs.tag_name }}
        VERSION: ${{ needs.validate-stable.outputs.version }}
      id: push
      if: ${{ !env.ACT }}
      name: Push parser module as an OCI artifact
      run: node .github/scripts/push-parsers-oci.mjs
    - if: ${{ !env.ACT }}
      name: Verify the artifact pulls anonymously
      run: .github/scripts/verify-parsers-anonymous.sh
    - if: ${{ !env.ACT }}
      name: Sign parser artifact with cosign
      run: cosign sign --yes "${{ steps.push.outputs.ref }}@${{ steps.push.outputs.digest }}"
    - if: ${{ !env.ACT }}
      name: Attest parser artifact
      uses: actions/attest@f7c74d28b9d84cb8768d0b8ca14a4bac6ef463e6 # v4.2.0
      with:
        subject-digest: ${{ steps.push.outputs.digest }}
        subject-name: ${{ steps.push.outputs.ref }}
```

`./.github/actions/setup-environment` is deliberately **not** used: node is preinstalled on
`ubuntu-latest` and the push script needs only `GITHUB_TOKEN`, so the self-hosting bootstrap (running
the _published_ core to publish the _new_ core's artifacts) stays off the critical path.

**No `docker/login-action` in this job.** That is not an oversight — it is what makes the next step
honest.

### 7.3 `.github/scripts/push-parsers-oci.mjs` (~180 lines, node stdlib + fetch only)

1. Resolve the asset name without hardcoding a version, from `dist/checksums.txt` — same discipline as
   `assert-wasm-checksum.sh`, because `--snapshot` with no `snapshot:` block yields
   `datamitsu_parsers_0.0.0-unstable.<date>.<sha>-SNAPSHOT-<short-sha>.wasm`. **Exactly one match
   required**; two is a loud failure.
2. Read the recorded SHA-256, re-hash `dist/<name>`, assert equality.
3. `REF = ghcr.io/datamitsu/datamitsu-parsers` (stable) or `…-parsers-unstable`;
   `TAG = ${VERSION#v}` or `$UNSTABLE_TAG`.
4. Registry handshake (model: the wrapper's `scripts/oci-bundle-postprocess.ts` `Registry` class):
   `POST /v2/<repo>/blobs/uploads/` → PUT the empty-JSON config blob and the wasm blob →
   `PUT /v2/<repo>/manifests/<tag>` with the §7.1 manifest → read `Docker-Content-Digest`.
5. **Load-bearing assertion — the design breaks silently without it.** Re-fetch the manifest by
   digest and assert `layers[0].digest === "sha256:" + SHA256`. This is also the cross-check binding
   the OCI publication to the cosign-signed `checksums.txt`. If a future producer swap ever
   compresses or tars the layer, this fails the release instead of every consumer's pull.
6. Write `dist/parsers-oci.json` and emit `ref`/`tag`/`digest`/`sha256` to `$GITHUB_OUTPUT` plus a
   human line to `$GITHUB_STEP_SUMMARY`.

```json
{
  "module": "core",
  "version": "0.1.16",
  "ref": "ghcr.io/datamitsu/datamitsu-parsers",
  "tag": "0.1.16",
  "digest": "sha256:…",
  "sha256": "612a…"
}
```

### 7.4 `.github/scripts/verify-parsers-anonymous.sh` — a genuinely credential-free pull

The draft ran `env -u GITHUB_TOKEN … oras blob fetch`. That is a **guaranteed false pass**: any
`docker/login-action` step writes GHCR credentials into `~/.docker/config.json`, which `oras` reads
by default — unsetting env vars does not unset a credential store, so the fetch succeeds even on a
private package, the release goes green, and every anonymous consumer 404s. This matters because a
**GHCR package created by a workflow is private by default and is not auto-linked to the repository**,
and users have no GHCR credential of their own (`shouldAttachGitHubToken` exists precisely so
datamitsu does not send them one).

The replacement is a bare `curl` with no `Authorization` header and no credential store in play:

```bash
curl -fsSL -o /dev/null -w '%{http_code}\n' \
  -H "Accept: application/vnd.oci.image.manifest.v1+json" \
  "https://ghcr.io/v2/${REPO}/manifests/${DIGEST}"
curl -fsSL "https://ghcr.io/v2/${REPO}/blobs/sha256:${SHA256}" | sha256sum -c <<<"${SHA256}  -"
```

(GHCR issues an anonymous pull token for public packages; a 401 here is the intended failure.) This
converts the private-by-default trap from a silent user-facing 404 into a loud release failure.

### 7.5 Docker Hub mirror — a separate job, or defer

`crane` is **not** available in `publish-parsers-oci` (§1: no crane/oras/regctl anywhere in CORE),
and crane reads `~/.docker/config.json`, not `DOCKERHUB_USERNAME`/`DOCKERHUB_TOKEN` env vars. The
draft's inline `run:` step could not have worked. Two acceptable shapes:

- **Separate job `mirror-parsers-dockerhub`** modelled line-for-line on the wrapper's proven mirror
  step (`WRAPPER/.github/workflows/release.yml:235-283`): `./.github/actions/setup-environment` +
  `docker/login-action` for `ghcr.io` **and** `docker.io`, a warm-up `pnpm dm exec crane -- version`
  so install logs never pollute command substitutions, then `crane digest`-compare-then-`crane copy`.
  The digest compare is **mandatory, not an optimization**: the wrapper's own comment records that
  its Docker Hub repo has tag immutability enabled, which denies **any** PUT to an existing tag even
  with an identical digest.
- **Defer Docker Hub entirely to a follow-up.** Defensible: mirroring the bundle but not the parser
  leaves Docker Hub orgs half-broken, but Docker Hub also imposes a 100-pulls/6h/IP anonymous limit
  that GHCR does not, so the two mirrors are not equivalent anyway.

Specifics if it is done: the destination is `index.docker.io/datamitsu/datamitsu-parsers` — the host
**must** be `index.docker.io`, because `ocidigest` has no `docker.io` → `registry-1.docker.io`
normalization (which is why the wrapper's existing bundle pin already uses `index.docker.io`).
CORE's Docker Hub namespace is `datamitsu/datamitsu`, so `datamitsu-parsers` is a brand-new repo — a
second one-time human prerequisite.

### 7.6 Tagging and retention

Stable: `ghcr.io/datamitsu/datamitsu-parsers:<version>` (+ `:latest` only for non-rc). Unstable:
`ghcr.io/datamitsu/datamitsu-parsers-unstable:unstable-<date>-<sha>`. **Never** a floating
`:unstable` tag. Tags exist for humans, for mirroring tools, and to keep the blob referenced
(registries GC unreferenced manifests) — the core never resolves one.

Because there is no floating tag, every unstable dispatch leaves one permanent tag. Decide a prune
rule (keep last N) that can never prune a tag a pinned wrapper config still references.

### 7.7 `parsers-oci.json` delivery to the wrapper

The draft had `packaging/pack.ts` write this file during `build`. That cannot work: the `digest`
field only exists after the push, which runs strictly after `build`. And `packaging/npm/datamitsu/package.json`
declares `"files": ["bin", "lib", "get-exe.js"]`, so a root-level `parsers-oci.json` would be
stripped by `npm publish` and never reach `node_modules/`.

**Resolution:** add `needs: publish-parsers-oci` to `publish-npm`, have it write `parsers-oci.json`
into `packaging/npm/datamitsu/` from the job outputs before `pack:prepare`, and add the entry to that
package's `files`. `publish-npm` already runs on both channels
(`if: always() && !cancelled() && needs.build.result == 'success'`), and the wrapper already depends
on `@datamitsu/datamitsu` and already reads its version out of `package.json` in
`sync-datamitsu-version.ts` — so the npm launcher the wrapper installs **is** the transport, with no
`gh release download`, no registry query and no `oras` in the wrapper. Also add
`dist/parsers-oci.json` to `.goreleaser.yml` `release.extra_files` and to `publish-github`'s `files:`
glob so it is visible to humans on both release kinds.

Fix the now-stale header comment in `packaging/pack.ts` (~lines 27-30) claiming the module ships only
as a release asset.

---

## 8. Variant A: the optional-prerelease fix (Phase 0)

**Already written in the working tree.** `git status` shows ` M .github/workflows/release.yml` and
`?? .github/scripts/stage-wasm-artifact.sh`; both were read in full and are correct as-is. The
prerelease **stays opt-in**: `publish-github`'s gate on
`github.event.inputs.create_github_prerelease == 'true'` is unchanged, the input keeps
`default: false`, and nothing in this design makes it mandatory or makes any other job depend on it.

The diff already present:

1. A new `build` step between "Assert WASM is in checksums.txt" and "Attest build artifacts",
   running `.github/scripts/stage-wasm-artifact.sh` — because goreleaser uploads the module straight
   from `parsers/target` on a real release and `--clean` wipes `dist/` before the build, so the copy
   must happen after goreleaser, under the exact name `checksums.txt` records.
2. Two globs added to `publish-github`'s `files:` — `dist/checksums.txt.sigstore.json` and
   `dist/datamitsu_parsers_*.wasm`.
3. The `workflow_dispatch` input _description_ updated (the gate untouched).
4. `stage-wasm-artifact.sh`: greps the name out of `checksums.txt`, copies, re-hashes the copy and
   fails on disagreement.

**Three changes before committing:**

- Replace `| head -1` with an explicit exactly-one-match check, so a future second parser module is a
  loud failure rather than a silently dropped artifact.
- Add `target_commitish: ${{ needs.determine-release-type.outputs.checkout_ref }}` to
  `softprops/action-gh-release`. Without it the created `unstable-<date>-<sha>` tag resolves against
  the default branch head rather than the built ref — and since **no unstable prerelease has ever
  been created**, all five globs are first-time-exercised and `softprops` silently tolerates
  unmatched ones.
- **Delete the draft's hedge about the sigstore bundle.** It is produced under `--snapshot` — real
  unstable run 31676105843 logs
  `signing cmd=cosign artifact=checksums.txt signature=dist/checksums.txt.sigstore.json` (unstable
  runs pass `--snapshot --clean` **without** `--skip=sign`, unlike `pr-checks.yml`). Keep the glob;
  no validation dispatch is needed for this question.

**What Phase 0 is and is not worth.** It is **not** "immediately useful for automated downstream
testing", as the draft claimed: the asset is named
`datamitsu_parsers_0.0.0-unstable.<date>.<sha>-SNAPSHOT-<short-sha>.wasm` while the npm/launcher
version is `0.0.0-unstable.<date>.<sha>` (no SNAPSHOT suffix), nothing machine-readable records the
asset name until Phase 4, and §2.11 declines to add `snapshot.version_template`. Its real value is
"a human can hand-copy the name+hash from the prerelease", plus the side effect Phase 4 depends on:
the staged copy lives in `dist/`, so the module now rides inside the existing `release-artifacts`
upload and `publish-parsers-oci` needs only `download-artifact` — no cargo rebuild, no rustup.
(`release-artifacts` has `retention-days: 1`, so a manual re-run a day later must rebuild.)

---

## 9. Wrapper changes

All paths relative to `WRAPPER = /Users/shibanet0/ghq/github.com/shibanet0/datamitsu-config`.

1. **`src/datamitsu-config/parsers.ts`** — add a placeholder + parser mirroring the proven bundle-pin
   mechanism in `src/datamitsu-config/oci.ts` (`const OCI_BUNDLE_PIN = "__DATAMITSU_OCI_BUNDLE_PIN__"`
   at `:18`, `parsePin()` at `:20` returning `undefined` on failure). The default published config
   keeps the https url — the placeholder parses to `undefined` and the url branch is used:

   ```ts
   const PARSER_OCI_PIN = "__DATAMITSU_PARSER_OCI_PIN__";

   function parseParserPin(raw: string): undefined | { digest: string; ref: string } {
     /* JSON.parse, undefined on failure */
   }

   const coreParserOci = parseParserPin(PARSER_OCI_PIN);

   export const parsers: config.MapOfParsers = {
     core: {
       hash: CORE_PARSER_HASH,
       ...(coreParserOci
         ? { oci: coreParserOci }
         : {
             url: `https://github.com/datamitsu/datamitsu/releases/download/v${DATAMITSU_VERSION}/datamitsu_parsers_${DATAMITSU_VERSION}.wasm`,
           }),
     },
   };
   ```

   Also fix the live skew: `parsers.ts:18` pins `"0.1.9"` while `datamitsu.config.ts:102` returns
   `"0.1.15"`.

2. **`scripts/inject-oci-pin.ts`** — inject the parser pin alongside the bundle pin into the same
   `datamitsu.config.oci-ghcr.js` / `.oci-dockerhub.js` variants the release already produces. Keep
   the script's discipline verbatim: exactly-one-placeholder guard, regex validation of ref/digest
   matching the Go rules, double `JSON.stringify` into a JS string literal. Zero new distribution
   channels, zero new `package.json` `files` entries.

3. **`WRAPPER/.github/workflows/release.yml`** — `publish-github` and `publish-npm` pass
   `--parser-ref`/`--parser-digest` to the existing `inject-oci-pin.ts` invocation, sourced from
   `node_modules/@datamitsu/datamitsu/parsers-oci.json`. No new job.

4. **`scripts/sync-datamitsu-version.ts`** — teach it about `parsers.ts`. It currently only rewrites
   `getMinVersion()` via `MIN_VERSION_REGEX` and early-returns on `^0\.0\.0-unstable\.` versions,
   which is exactly why the two pins drifted. **Keep** the unstable early-return for `getMinVersion`
   (an unstable version must not become the floor); **add** a parser-pin branch that runs for **both**
   channels, reading `node_modules/@datamitsu/datamitsu/parsers-oci.json` and rewriting
   `DATAMITSU_VERSION` + `CORE_PARSER_HASH` (and the ref/digest for the variant build). Fully
   offline, deterministic, no registry query.

5. **`scripts/__tests__/parsers.test.ts` (new)** — there is no parsers test today. Assert: the hash
   is 64 lowercase hex; for a stable pin the version embedded in the url equals `getMinVersion()`; a
   digest, when present, matches `^sha256:[0-9a-f]{64}$`. This is the guard that would have caught
   0.1.9-vs-0.1.15, which is structurally undetectable today.

6. **`scripts/test-fresh-init.ts` — parameterize it, and add a real assertion.** Two separate fixes:

   - **The OCI parser path currently gets zero CI coverage in either repo**, and the draft's claim
     that the existing job exercises it "for free" is false. `test-fresh-init.ts:35` sets
     `BASE_CONFIG = datamitsu.config.base.js`, produced by `Taskfile.yaml:31` and **never**
     pin-injected — the release only injects into `datamitsu.config.oci-ghcr.js`
     (`release.yml:327`). This is structural, not an oversight: §2.9 keeps the default config on the
     https url. Fix: add a `--config <path>` flag to the script and a second `fresh-init` matrix leg
     that first runs `cp datamitsu.config.js /tmp/oci.js && node scripts/inject-oci-pin.ts --file
/tmp/oci.js --parser-ref … --parser-digest …` with a **static, already-published** test pin.
   - Add one assertion: `datamitsu devtools parsers list` exits 0. Not polish — `executor.go` warns
     and returns raw output on any parser error, so a broken pin, a private package or a bad digest
     degrades silently and the current smoke test still passes. `devtools parsers list` forces
     download + SHA-256 verify + wazero compile + `describe`. Harness constraint already respected by
     this file: it must use bare `pnpm exec datamitsu` inside the script (the `dm` wrapper passes
     `--before-config`, which makes datamitsu ignore `getBeforeConfigs()`), and
     `scripts/__tests__/workflow-ci-guard.test.ts` forbids bare `pnpm exec datamitsu` in workflow
     YAML.

7. **`Taskfile.yaml`** — no structural change. `build` already calls `sync:datamitsu-version`, and
   `build:lib:datamitsu-types` already regenerates `src/datamitsu-config/datamitsu.config.d.ts` from
   `pnpm --silent dm config types`, so `ParserOCI` propagates automatically on a core bump.

8. **`docs/get-started/oci-bundle.md`** — extend "Air-gapped hosts" and "Overriding the registry" to
   cover the parser artifact: mirror **two** refs, override **two** keys, digests unchanged. Note the
   `index.docker.io` host requirement and the anonymous-pull limitation (§13.2).

9. **NOT changed, deliberately:** `docker/Dockerfile`, `docker/Dockerfile.alpine`,
   `docker/oci-map*.json`. `internal/dockerfile/storepaths.go:38-44` `parserSubtree` is hash-**less**,
   so the content-only cacheKey moves no COPY path and no map entry — only the bundle image must be
   rebuilt so its layer tar carries the new xxh3 child. And the wrapper's build config
   (`datamitsu.config.base.js`, which feeds `devtools dockerfile`) keeps the `url` source, so the
   buildkit `parser-core` stage needs no registry credentials. `scripts/__tests__/dockerfile-consistency.test.ts`
   should be **unchanged** — verify it, since it is the canary for an accidental subtree move.

---

## 10. Phasing

| Phase | Title                                | Effort | Deliverable                                                                   |
| ----- | ------------------------------------ | ------ | ----------------------------------------------------------------------------- |
| 0     | Optional-prerelease fix (variant A)  | 0.5 d  | The unstable prerelease carries the module; ships alone                       |
| 1     | Prerequisites + foundations          | 1.5 d  | Three security/correctness fixes, `ociref`, content-only key, runner ordering |
| 2     | Config schema (Go + TS + validation) | 0.5 d  | `parsers.<n>.oci` parses, validates, round-trips                              |
| 3     | `internal/ociartifact` + dispatch    | 1.5 d  | A registry-sourced parser actually loads                                      |
| 4     | Publish pipeline                     | 2 d    | Every release publishes a signed artifact anyone can pull                     |
| 5     | `datamitsu store refs`               | 0.5 d  | "What must I mirror?" is a command                                            |
| 6     | Wrapper plumbing + CI coverage       | 1.5 d  | Pins propagate; the OCI path is actually tested                               |
| 7     | Documentation                        | 1 d    | Seven website pages, CONTRIBUTING, embedded llms-docs                         |

**Total ≈ 9 engineer-days**, plus 2-3 throwaway unstable dispatches and one org-owner action.

### Phase 0 — Optional-prerelease fix (0.5 d)

Files: `.github/workflows/release.yml`, `.github/scripts/stage-wasm-artifact.sh`.

1. Replace `| head -1` with an exactly-one-match check.
2. Add `target_commitish` to `softprops/action-gh-release`.
3. Drop the sigstore-glob hedge (§8 — resolved as present).
4. Verify the gate still reads `github.event.inputs.create_github_prerelease == 'true'` and the input
   still defaults to `false`.
5. One real unstable `workflow_dispatch` with `create_github_prerelease=true`; record the full `dist/`
   listing (all five globs are first-time-exercised).
6. Commit. Ships independently.

### Phase 1 — Prerequisites and foundations (1.5 d)

Files: `internal/ociref/` (new), `internal/config/validate.go`, `internal/ocibundle/{seed.go,status.go,paths.go,ocibundle.go}`,
`cmd/store.go`, `internal/parsermanager/parsermanager.go`, `internal/ocidigest/{client.go,pull.go}`,
`internal/runner/runner.go`.

1. **§3.2 — the token relay.** Gate `SetBasicAuth` on `r.registry == "ghcr.io"` as well as the
   challenge realm. Tests: an `evil.example` resolver answering 401 with `realm="https://ghcr.io/token"`
   sends no Basic auth and forwards no bearer token. Update the doc comment.
2. **§3.1 — unverified parser seeding.** Skip-with-Warn any `.parsers/` layer with no `reVerify`
   entry. Test: a bundle carrying `.parsers/core/<unknown-key>` completes the seed without writing
   it.
3. **§3.1 — self-healing store.** Re-verify `module.wasm` against `p.Hash` on `ensureModule`'s
   `os.Stat` hit; on mismatch remove and re-fetch. Test: a pre-planted wrong-bytes `module.wasm`
   makes `LoadWASMBytes` fail (this test fails today).
4. **§3.3 — marker validation.** Compare the marker's recorded `Subtrees` against
   `expectedSubtrees(cfg, storeRoot, nil, nil)` instead of a bare existence check. Also make
   `runStoreClear` remove `{cache}/.oci-digests`.
5. **`internal/ociref`** — `Parse` + `ErrRefSyntax`/`ErrRefNoHost`, pattern-then-host ordering.
   Move `ociRefPattern` (and its comment) here. Swap the three duplicated
   `strings.Cut(ref, "/")` calls (`ocibundle/seed.go:124`, `ocibundle/status.go:58`,
   `cmd/store.go:134`) and point `ValidateOCI` at it, keeping its two error strings verbatim so
   `TestValidateOCI_MalformedRef` passes unchanged. Note `cmd/store.go` gains stricter validation
   (release note) and fix the first-colon `--resolve-tag` cut bug.
6. **The keystone** — `cacheKey` becomes `XXH3Multi([]byte("parser-v2"), []byte(p.Hash))` with the
   §5 doc comment. Add `TestModuleStorePath_SameHashDifferentSources`.
7. **`ocidigest` hardening** — wrap `authedGet`'s `GuardOffline` error in `httpretry.Permanent`
   (matching `binmanager/download.go:158`), and give `PullManifest` the same retry loop `PullBlob`
   has (today one transient 5xx fails the whole operation).
8. **Runner ordering** — move `ocibundle.AutoSeed` (`runner.go:542`) ahead of `Prewarm`
   (`runner.go:324`), preserving the `sc.binMgr != nil` guard; eyeball the progress-bar ordering.
9. **Prose sweep, same commit** (all currently say "url+hash"): `parsermanager.go:191, 290, 313,
352-353`, `capabilities.go:12, 100`, `dockerfile/storepaths.go:40`, `config.go:359-360, 371,
396-397`, `cmd/devtools_parsers.go:68`, `capabilities_test.go:52`.
10. `go test ./...` — expect `parsermanager`/`ocibundle` store-path tests and
    `test/cli/testdata/golden/devtools_parsers_prefetch_help.txt` to need regeneration (that golden
    embeds "matching url+hash" — the draft wrongly asserted no parsers golden would move).

**This phase is worth shipping alone.** It fixes a credential-relay hole, an unverified-write hole
and a silent airgap bug in the bundle path, and stops every cold-store `lint` from fetching a bundled
parser over the network. Landing it separately de-risks the store relocation from the feature.

### Phase 2 — Config schema (0.5 d)

Files: `internal/config/{config.go,validate.go}`, `config/config.d.ts`, `internal/config/config.d.ts`,
`internal/config/{parsers_test.go,oci_test.go}`.

1. Add `ParserOCI` and rewrite `Parser` per §4.1.
2. Extend `ValidateParsers` per §4.4; add the symmetric load-time `signer` rejection to `ValidateOCI`
   (§2.6) and list it as a behaviour change.
3. Add `interface ParserOCI` and rewrite `interface Parser` in `config/config.d.ts` (fields
   alphabetized: hash, oci?, url?), retaining the no-version NOTE verbatim.
4. `task build:lib` regenerates the embedded copy; `TestConfigDTSCopiesAreByteIdentical` must pass.
5. **Rewrite `parsers_test.go:86` and `:151`** for the new exactly-one-of message.
6. New validation cases: both sources, neither source, uppercase ref, `:tag` in ref, `@digest` in
   ref, ref with no host, missing digest, non-`sha256:` digest, uppercase digest hex, signer set.
7. Confirm `TestEmptyConfigMarshalUnchanged` still yields `{}` and add a case pinning that a url-only
   Parser marshals byte-identically to today — that is what protects every user's execution-cache
   invalidation key on upgrade.

### Phase 3 — `internal/ociartifact` + dispatch (1.5 d)

Files: `internal/ociartifact/{parsers.go,parsers_test.go,testdata/}` (new),
`internal/parsermanager/{parsermanager.go,parsermanager_test.go,prefetch_test.go}`,
`internal/ocidigest/pull_test.go`.

1. Implement the constants, `errIntegrity`/`IsIntegrityError`, the `registryClient` seam, the
   overridable `newClient`, `SelectWasmLayer` and `FetchParserModule` exactly as §5 step 6.
2. `SelectWasmLayer` is pure and enforces the 8 rules in order. **Do not** add the config-media-type
   rule.
3. Wire the dispatch at `parsermanager.go:327` per §5 step 4, including the post-hoc
   `VerifyFileHashPublic` on the OCI branch. Turn the `p.URL == ""` check into exactly-one-source;
   leave the hash check alone.
4. **Do not touch `internal/binmanager`.**
5. Table-test `SelectWasmLayer` (§12).
6. Test `FetchParserModule` against a fake `registryClient` (§12).
7. **Add an httptest-backed test _inside_ package `ocidigest`** driving a real `Resolver` through
   manifest→blob for an artifact-shaped manifest. Without it the `ociartifact`↔`ocidigest` wiring
   (URL shape, Accept header, auth handshake, the `desc.Size`→`PullBlob` handoff) is exercised
   **nowhere** in default CI: `newTestResolver` is unexported and `NewResolverForHost` hardcodes
   `scheme: "https"`, so out-of-package code cannot reach an httptest server. The draft's claim that
   the existing suite covers this half is wrong.
8. Regenerate `prefetch_test.go TestModuleStorePath_MatchesInternalLayout` and
   `ocibundle/parsers_test.go`. Confirm `internal/dockerfile/parsers_test.go` is unaffected.
9. `go build ./... && go test ./... && go vet ./...`.

**Also in this phase:** `internal/dockerfile/slice.go:70-77` builds `&config.Config{Parsers: {module:
parsers[module]}}` and `RenderSlice` marshals it, so an oci-sourced parser is serialized straight into
the buildkit stage config whose `RUN datamitsu devtools parsers prefetch` would then need registry
egress and credentials from inside the build. Add a `devtools dockerfile` **warning** when a planned
parser stage has an OCI source, and an oci-parser case in
`slice_test.go TestRenderSlice_LoadableModuleRoundTrips`.

### Phase 4 — Publish pipeline (2 d, was 1 in the draft)

Files: `.github/workflows/release.yml`, `.github/scripts/push-parsers-oci.mjs` (new),
`.github/scripts/verify-parsers-anonymous.sh` (new), `.goreleaser.yml`,
`packaging/npm/datamitsu/package.json`, `packaging/pack.ts`, `.github/workflows/oci-e2e.yml`.

1. **Prerequisite:** an org owner creates `datamitsu-parsers` and `datamitsu-parsers-unstable` as
   **public** GHCR packages linked to the repo.
2. Add `publish-parsers-oci` per §7.2. Reuse the already-pinned cosign-installer, download-artifact
   and attest SHAs. Guard every network step with `if: ${{ !env.ACT }}`.
3. Write `push-parsers-oci.mjs` per §7.3, including the load-bearing layer-digest assertion.
4. Write `verify-parsers-anonymous.sh` per §7.4 (bare curl, no credential store).
5. Add `needs: publish-parsers-oci` to `publish-npm`; write `parsers-oci.json` into
   `packaging/npm/datamitsu/` from the job outputs; add it to that package's `files`; fix the stale
   `pack.ts` header comment.
6. Add `dist/parsers-oci.json` to `.goreleaser.yml` `release.extra_files` and `publish-github`'s
   `files:`. Do **not** add a `snapshot:` block (§2.11).
7. Optionally add `mirror-parsers-dockerhub` per §7.5, or record the deferral.
8. Extend `oci-e2e.yml`. **Be honest about what it proves:** it runs on a nightly cron against the
   vendored released wrapper config (`test/e2e/testdata/datamitsu.config.oci-ghcr.js`, which today has
   **no** `parsers` key at all), so it validates a _previously published_ pin, never the current
   run's. Same-run validation lives only in step 4.
9. Validate with 2-3 throwaway unstable dispatches. There is no `act`-testable path for a registry
   push (`if: ${{ !env.ACT }}` guards cover the whole job).

### Phase 5 — `datamitsu store refs` (0.5 d)

Files: `cmd/store.go`, `cmd/store_refs.go` (new), `test/cli/store_test.go`,
`test/cli/completeness_test.go`, `test/cli/testdata/golden/{store_refs_help.txt,store_help.txt}`.

1. A `store refs [--json] [--oci-only]` leaf: pure traversal of the effective config, **no network**,
   so it runs inside a firewall and inside the hermetic clitest harness. Plain output = one
   `<ref>@<digest>` per line; `--json` adds every hashed https artifact with its mandatory hash, so a
   platform team knows exactly what remains outside the registry.
2. Sources: `cfg.OCI`, every `cfg.Parsers[*].OCI`, plus the https URLs when `--oci-only` is absent.
   Deterministic sort.
3. Register the leaf in `testedLeafCommands` — `TestContractCompletenessGate` fails the build
   otherwise.
4. Blackbox test + `store_refs_help.txt`; regenerate `store_help.txt` with `-update`.
5. **No new persistent root flag** — that would rewrite ~30 of the 61 goldens embedding the shared
   "Global Flags:" block.

### Phase 6 — Wrapper (1.5 d, was 1)

Per §9, all eight items. The extra half-day is the `test-fresh-init.ts` parameterization and the
second matrix leg — the only thing that gives the OCI path real CI coverage.

### Phase 7 — Documentation (1 d)

Per §15.

**Sequencing:** Phase 0 and Phase 1 each ship independently. Phases 2+3 must land together (the
schema is unusable without the fetch path). Phase 4 must land before Phase 6 (the wrapper reads
`parsers-oci.json`). The GHCR visibility prerequisite must be done before the first Phase 4 run.

---

## 11. Rejected alternatives

### 11.1 `oci://…` inside the existing `url` field

Superficially the smallest diff (~120 LOC, 3 files) and mechanically sound —
`GET /v2/<name>/blobs/<digest>` is a first-class endpoint and `PullBlob` genuinely skips its size
check when passed `size = -1`. Rejected for four reasons, in descending weight:

1. **Pull-through proxies.** A bare blob GET with no preceding manifest pull is exactly what
   pull-through cache topologies (Harbor proxy projects, Artifactory remote repos, Nexus proxies)
   handle worst — several populate a blob only after seeing it referenced by a manifest pull.
   `crane copy` into a mirror still works, but "point at our proxy" is the single most common
   enterprise ask, and the failure mode is a 404 on a hash that still looks valid. The manifest-first
   shape exercises the same request sequence as an ordinary image pull.
2. **Silent failure on older cores.** `ValidateParsers` performs **zero** scheme checks on `url` —
   any non-empty string passes. So `url: "oci://…"` against an older core validates fine and then
   dies inside `downloadFileInternal` with `unsupported protocol scheme "oci"`, an error **not**
   wrapped `permanent()`, so it is retried four times with ~7 s of backoff first. A structured key
   makes an older core fail at load with the existing, clear `parser %q: url is required`.
3. **`binmanager` would stop meaning what it means.** Threading `expectedHash` into
   `downloadFileInternal` to use it as an _address_ quietly changes the defining invariant of the one
   package whose job is "one function takes `expectedHash`, one function verifies it".
4. **No room for `signer`, `artifactType`, or annotations** — and no way to reject a `signer` that
   this build cannot honour.

It also matches every existing discriminated-union precedent in the tree: `Config.OCI *OCIRef`,
`binmanager.App.Binary/Uv/Node/Jvm/Go/Shell`, `RuntimeConfig.Managed/System/…`.

### 11.2 Reusing `internal/ocibundle` for the artifact

`layerCompressionFor` (`ocibundle.go:95-104`) accepts only tar+gzip and tar+zstd, and
`classifyLayers` requires the `com.datamitsu.subtree` annotation — so a raw `application/wasm` layer
is a hard `errIntegrity`. `selectDescriptor` enforces a linux-requires-`com.datamitsu.libc` contract
that is meaningless for a platform-independent wasm. The artifact gets its own extract-free path; the
bundle keeps carrying parsers as `.parsers/` tar subtrees. Both write the same store path — that is
the point of the cacheKey change.

### 11.3 The full enterprise suite (`internal/ociauth`, `DATAMITSU_CA_BUNDLE`, `DATAMITSU_OCI_MIRROR`)

~1200 LOC, three new packages, two new CLI leaves, three new env vars, new `runtimeconfig` fields.
Every individual claim in that draft checks out, but it bundles four separable projects into one arc,
and its own honest finding is that the ceiling is **auth**, not transport — and auth is independent
of parsers entirely. Deferred, named, costed (§13.2).

### 11.4 `oras` / `crane` / `buildx` / `regctl` as the publisher

- **crane**: `crane append` gzips, so the layer digest would not equal the wasm SHA-256 — fatal for
  this design. Retained for mirroring only, where it is already proven.
- **buildx**: image shape plus provenance manifests a consumer must filter (the wrapper's
  `oci-bundle-postprocess.ts` had to do exactly that).
- **regctl**: no precedent in either repo.
- **oras**: the closest fit and a reasonable fallback, but adding `oras-project/setup-oras` to a job
  holding `packages: write` + `id-token: write` costs a new pinned third-party action, and
  `oras push --format json`'s field layout would have to be verified before hard-coding a `jq`
  expression against it. §2.8 chooses the hand-rolled Node script instead.

### 11.5 Making unverifiable `.parsers/` bundle layers fatal

The stricter reading of §3.1. Rejected because a shared org bundle legitimately carries modules for
several config variants, and "a partial bundle is a valid partial seed" is a deliberate existing
property. Skip-with-Warn achieves the same security outcome — the consumer cannot load a module its
config does not declare — without breaking legitimate multi-variant bundles.

### 11.6 `--no-oci` blocking a declared parser source

Would silently change behaviour under the _entire_ offline golden suite, since `internal/clitest/run.go:113-116`
forces both `DATAMITSU_OFFLINE=1` and `DATAMITSU_NO_OCI=1`. §2.7 keeps the flag's meaning. Because
the harness forces both, the chosen semantics are invisible to the golden tier — so Phase 3 must add
an explicit unit test pinning that a declared parser `oci` source **is** attempted under
`DATAMITSU_NO_OCI=1` and **is** refused under `DATAMITSU_OFFLINE=1`.

---

## 12. Test plan

### Unit — `internal/ociartifact` (the bulk of the value; no HTTP, no filesystem)

`SelectWasmLayer` table test over golden manifest bytes in `testdata/`:

- valid single-layer artifact → returns the descriptor
- `application/vnd.oci.image.index.v1+json` → rejected
- `application/vnd.docker.distribution.manifest.list.v2+json` → rejected
- image manifest with a non-empty `manifests` array → rejected
- `artifactType` empty / wrong → rejected
- `subject` present → rejected
- 0 / 2 / 3 layers → rejected
- layer media type `application/vnd.oci.image.layer.v1.tar+gzip` → rejected
- **`layers[0].digest != "sha256:"+wantSHA256` → rejected** (the pivot)
- layer size 0 and size > `MaxParserModuleBytes` → rejected
- **a non-empty config media type → ACCEPTED** (pins §5's deliberate non-rule)
- every rejection asserts `IsIntegrityError(err) == true` and that the message names both digests

`FetchParserModule` against a fake `registryClient` injected via `newClient`:

- happy path returns a temp file under `destDir`
- **layer-digest mismatch issues ZERO blob calls** — assert the fake's blob counter is 0; this is the
  pre-flight-rejection property and the enterprise-security claim
- malformed ref never constructs a client
- `t.Setenv("DATAMITSU_OFFLINE", "1")` → refused immediately with `httpx.ErrOffline`, zero calls
- `ocidigest.ErrManifestNotFound` is re-wrapped to name the parser, not the bundle

`ociref.Parse` table: valid `ghcr.io/a/b`, `localhost:5000/a`, `harbor.corp.example/dm/parsers`;
rejected `Ghcr.io/a`, `ghcr.io/a:tag`, `ghcr.io/a@sha256:…`, `datamitsu/parsers` (no host → host
sentinel), `ghcr.io` (no path → syntax sentinel).

### Unit — `internal/parsermanager`

- an OCI-source case via a fake fetch seam; every existing case (ValidHashStoresAndLoads,
  WrongHashFails, MissingHashIsError, ConcurrentLoadsDeduplicate, LocalFileSource) passes untouched
- **`TestModuleStorePath_SameHashDifferentSources`** — a url-sourced and an oci-sourced Parser with
  the same Hash resolve to the SAME `ModuleStorePath`. The cross-source bundle-interop invariant the
  airgap story rests on.
- **`TestEnsureModule_RejectsPoisonedStoreFile`** — pre-planted wrong-bytes `module.wasm` makes
  `LoadWASMBytes` fail (§3.1; fails today)
- a declared `oci` source is attempted under `DATAMITSU_NO_OCI=1`, refused under `DATAMITSU_OFFLINE=1`
- regenerate `TestModuleStorePath_MatchesInternalLayout`

### Unit — `internal/config`

- JSON round-trip with `oci`; `TestConfig_ParsersOmitEmpty` still holds; a url-only Parser marshals
  byte-identically to today; `TestConfig_ParsersIgnoresLegacyVersion` unchanged
- the full validation table from §4.4, each asserting the exact message
- **rewritten** `TestValidateParsers_MissingURL` and `TestValidateParsers_AggregatesAllErrors`
- `TestConfigDTSCopiesAreByteIdentical` and `TestEmptyConfigMarshalUnchanged` still pass
- `TestValidateOCI_MalformedRef` unchanged after the `ociref.Parse` swap; a new case for the
  load-time `signer` rejection

### Unit — `internal/ocidigest`

- `PullManifest` retry loop: a 500-then-200 sequence succeeds (reuse `setFastBlobRetries`)
- the offline error is now `httpretry.IsPermanent`: `PullBlob` makes exactly **one** attempt under
  `DATAMITSU_OFFLINE=1` (today it makes four)
- **new httptest-backed artifact test** driving a real `Resolver` manifest→blob (Phase 3 item 7)
- **§3.2**: an `evil.example` resolver answering 401 with `realm="https://ghcr.io/token"` sends no
  Basic auth and forwards no bearer to `evil.example`

### Unit — `internal/ocibundle`

- `parsers_test.go` regenerated for the new store key; `TestValidateSubtree_AcceptsParsers`,
  `TestExpectedSubtrees_ParserReferencedByTool`, `TestBuildReVerifyIndex_IndexesParser` still pass
- **new**: a full seed of a bundle carrying `.parsers/core/<unknown-key>` completes without writing
  it (§3.1)
- **new**: a marker whose recorded `Subtrees` no longer matches `expectedSubtrees` triggers a re-pull
  (§3.3)
- the three `ociref.Parse` call-site swaps change no existing error string — run seed/status tests
  unmodified

### CLI golden (`test/cli`, byte-stable under `go test ./test/cli/ -count=2`)

- `store_refs_help.txt` NEW; `store_help.txt` regenerated
- **`devtools_parsers_prefetch_help.txt` regenerated** (it embeds "matching url+hash")
- register `store refs` in `testedLeafCommands`
- blackbox `store refs` in a project with neither `oci` nor `parsers` (empty output, exit 0) and one
  with both (deterministic sorted output)
- `config_show.txt` is `{}` today and does not move; `root_help.txt` and the ~30 goldens embedding
  "Global Flags:" are untouched (no new root flag, `--no-oci` help text unchanged)
- `TestDevtoolsParsersCommandSetDrift` unchanged (no new parsers leaf); `TestConfigRuntime` unchanged
  (no new `Effective` field)

### Gated `e2e_oci` (`//go:build e2e_oci` + `DATAMITSU_TEST_OCI=1`, nightly, never in default CI)

- refresh `test/e2e/testdata/datamitsu.config.oci-ghcr.js` from `test/e2e/source.go OCIConfigSource`
  — it has **no** `parsers` key today, so the gated tier tests zero parser behaviour
- `TestOCIParserModulePull`: pin `parsers.core` to a published `oci` ref+digest, run
  `devtools parsers list`, assert exit 0
- `TestOCIParserOfflineAfterSeed`: `store seed` the bundle, re-run the parse under
  `DATAMITSU_OFFLINE=1`, assert zero network — the only true end-to-end proof of the airgap claim

### CI self-tests (fail the release, not the users)

- `layers[0].digest == "sha256:" + <checksums.txt entry>` — without it the design breaks silently
- the credential-free anonymous pull (§7.4)

### Wrapper (vitest)

- `scripts/__tests__/parsers.test.ts` NEW (§9.5)
- extend `sync-datamitsu-version.test.ts` for the parser-pin branch, including that it runs for
  unstable versions while `getMinVersion` still early-returns
- `test-fresh-init.ts` gains the `devtools parsers list` assertion **and** a second matrix leg driven
  by an injected pin (§9.6)
- `dockerfile-consistency.test.ts` should be UNCHANGED — verify it

**Coverage:** `pnpm test:coverage:all` should show `internal/ociartifact` near-fully covered by the
pure table tests.

---

## 13. Risks

1. **Store-key relocation is deliberate and is the biggest operational risk.** Changing `cacheKey`
   orphans every existing `.parsers/<name>/<key>` (a one-time ~377 KiB re-download) **and** makes
   every already-published bundle's parser layer unmatched until the bundle is rebuilt. The stale-
   bundle symptom is silent: the subtree is not found, the parser falls back to the network, and under
   `DATAMITSU_OFFLINE=1` the parse degrades to raw output with only a Warn. Mitigations: the §3.3
   marker fix makes a re-run of `store seed` actually re-pull; republish the wrapper bundle in the
   same arc; release-note it as a breaking change (alpha policy permits it);
   `TestModuleStorePath_SameHashDifferentSources` pins the invariant going forward. Good news:
   `parserSubtree` is hash-less, so the wrapper's `docker/Dockerfile*` and `oci-map*.json` need no
   regeneration.

2. **Private-registry auth is the actual ceiling and this arc does not lift it.** The only credential
   in the tree is `GITHUB_TOKEN`, attached as Basic `x-access-token` and (after §3.2) only when the
   resolver's own registry is `ghcr.io`. No docker `config.json`, no credential helpers, no
   `DOCKER_CONFIG`, no user/password, no static bearer, no mTLS, no custom CA
   (`httpx.NewHardenedClient` sets no `TLSClientConfig`), no ECR/GAR/ACR. This works today for a
   **public or anonymous-pull** mirror — the common Harbor/Artifactory/Nexus proxy-project setup —
   and **not** for an authenticated one. Identical for the existing bundle path, so not a regression,
   but the docs must say it plainly rather than promising "mirror one registry and everything works"
   unconditionally.

3. **GHCR packages created by a workflow are private by default** and are not auto-linked to the
   repository, so a brand-new `datamitsu-parsers` package 401s for every anonymous consumer. The
   credential-free smoke test (§7.4) converts this into a loud release failure, but the one-time
   visibility flip is a human prerequisite.

4. **The entire design rests on `layers[0].digest == sha256(file).`** Any tooling change that gzips
   or tars the layer diverges the digest from the config hash and fails **every** consumer pull. The
   post-push assertion is load-bearing, not belt-and-braces.

5. **Silent degradation masks every parser failure.** `executor.go` warns and returns raw output on
   any parser error. Any CI meant to validate a pin must force the fetch and assert an exit code.

6. **Registry garbage collection.** The artifact must stay tagged; an untagged manifest can be GC'd,
   after which every pinned config fails with `ErrManifestNotFound`. CI must never prune
   `parsers-*` tags.

7. **Pull-through proxy behaviour could not be verified from this repo.** Choosing the manifest-first
   shape specifically to be safe here is a judgement call about how proxy caches populate, not a
   measurement. Validate against at least one real enterprise registry (a Harbor proxy project or an
   Artifactory remote repo) before promising mirror transparency in the docs. Some proxying
   registries have historically **normalized** manifests, which would change the digest and fail
   closed — a loud failure, but worth knowing in advance.

8. **Old cores + new config.** `vm.ExportTo` drops unknown JS keys (pinned by
   `TestConfig_ParsersIgnoresLegacyVersion`), so an older core reading an oci-only parser entry sees
   no url and fails with `parser "core": url is required` — clear, but obscure unless
   `getMinVersion()` is bumped in lockstep. Mitigated because only the opt-in `oci-*` variants carry
   the pin, and those already require a newer core for the bundle.

9. **`--no-oci` not disabling a declared parser source is defensible but surprising.** Grounded and
   documented (§2.7), and now explicitly unit-tested — but a reviewer reading only the flag name will
   object.

10. **Cosign signing can create a false sense of verification.** The binary checks nothing. Rejecting
    `parsers.oci.signer` keeps this honest; the docs must not imply otherwise.

11. **No rollback protection.** Nothing stops a config pinning an older, still-valid, still-signed
    module forever; `MinimumReleaseAgeMinutes` enforces "not too new", never "not too old". The live
    0.1.9-vs-0.1.15 wrapper skew **is** this hazard already realized. The Phase 6 guard test is the
    only mitigation offered here, and it is client-side and advisory.

12. **The bundle producer still needs github egress by default,** because the wrapper's build config
    keeps the `url` source for the buildkit parser-prefetch stage (no registry credentials inside
    buildkit). An org wanting to rebuild its own bundle fully offline overrides the parser to `oci`
    in its own build config, which then needs registry egress from buildkit. Document it.

13. **Redirect-driven SSRF primitive (pre-existing, shared with the bundle path).** An
    attacker-controlled 307 chain can make datamitsu GET up to 10 arbitrary https URLs.
    `Authorization` is stripped cross-host and the response is only hashed and discarded on mismatch,
    so nothing leaks and no wrong bytes are accepted. Not new, not worsened.

14. **A new leaf command trips three gates at once** (`store refs`): `testedLeafCommands`,
    `TestContractCompletenessGate` and the `store_help.txt` golden. The build fails until all three
    are done — intended, but it costs a cycle.

15. **`DATAMITSU_PARSERS_DIR` pointing outside the store** makes bundle seeding of parsers a silent
    no-op: `subtreeRel` fails and `ocibundle/paths.go` drops the parser from both `expectedSubtrees`
    (debug log only) and `buildReVerifyIndex` (a bare `continue`). Pre-existing, unchanged by this
    design, worth a follow-up Warn.

16. **The unstable→wrapper loop is not automatic.** An unstable core release is `workflow_dispatch`-
    only (`on.push.tags` matches `v[0-9]+.[0-9]+.[0-9]+` only), so testing an unreleased parser still
    requires a human dispatch, then a wrapper dependency bump + `sync:datamitsu-version` + a wrapper
    PR. The OCI channel removes the _missing artifact_, not the manual hop. Say so in CONTRIBUTING.

---

## 14. Draft claims corrected during adjudication

Recorded so they are not re-introduced:

- **"Prewarm hard-fails under `DATAMITSU_OFFLINE=1`"** — false. `runner.go:322-329` logs
  `log.Debug("parser prewarm failed; compiling lazily")` and swallows the error. The real symptom is
  the executor's later silent degradation to raw output.
- **"`dist/checksums.txt.sigstore.json` may not exist under `--snapshot`"** — false. Real unstable run
  31676105843 shows goreleaser signing `checksums.txt` under `--snapshot`; only the _release_ pipe is
  skipped. The hedge in the Phase 0 comment should be deleted, not committed.
- **"No parsers golden moves"** — false. `test/cli/testdata/golden/devtools_parsers_prefetch_help.txt`
  embeds "A module already on disk (matching url+hash) is a no-op".
- **"The existing `ocidigest` httptest suite covers the network half"** — false. `newTestResolver` is
  unexported and `NewResolverForHost` hardcodes `scheme: "https"` on an unexported field, so
  out-of-package code cannot reach an httptest server.
- **"The wrapper's `fresh-init` job exercises the whole chain for free"** — false. It runs against
  `datamitsu.config.base.js`, which is never pin-injected.
- **"`parsers-oci.json` can be written in `build`"** — false. The manifest digest does not exist until
  after the push; and `packaging/npm/datamitsu/package.json`'s `files` would strip it.
- **"`crane`/`oras` are available in the release job"** — false. Neither exists anywhere in CORE;
  `crane` is a wrapper-declared managed app reachable only after `setup-environment`.
- **"`env -u GITHUB_TOKEN oras blob fetch` proves the artifact pulls anonymously"** — false. A prior
  `docker/login-action` writes `~/.docker/config.json`, which oras reads.
- **Reviewer claim that `internal/config/coverage_paths_test.go:67` asserts the parser url message** —
  wrong. That case covers `BinaryOsArchInfo`, a different entity, and is unaffected. Exactly two
  sites need rewriting (`parsers_test.go:86` and `:151`).
- **"Store poisoning is a local-write problem and the bundle path is stronger"** — inverted. The
  vector is a remote bundle publisher and the bundle path is _weaker_ for any `.parsers/` subtree the
  consumer's config does not currently name (§3.1).

---

## 15. Documentation plan

All docs land in the **same PR** as the code (binding per `.datamitsu/ai/agents/agents-docs-website.md`),
followed by `task gen:llms-docs` and a commit of `internal/llmsdocs/embed` — the `pr-checks.yml`
`llms-docs-drift` job rebuilds the site, re-harvests and requires a clean git diff.

1. **`website/docs/guides/architecture/parsers.md`** — the canonical page and the most wrong one
   today. Re-draw the six-stage Mermaid so **Deliver** branches into two channels (GitHub Release
   asset | OCI artifact) instead of hard-coding "GitHub Release asset". Rewrite **Deliver** (which
   currently says "The built module ships one way") and **Sign** (distinguish the cosign-signed
   `checksums.txt` from the cosign-signed artifact manifest, and state plainly that datamitsu verifies
   **neither**). Update **Store key** for content-only addressing and explain why that is what makes
   a bundle-seeded and a registry-pulled module interchangeable. Rewrite the **Trust Model Summary**
   with a row per channel. Conceptual + Mermaid only, no Go snippets; config examples in JS, CLI in
   bash.
2. **`website/docs/reference/configuration-api.md`** — in "Output Parsers (`parsers`)": document
   `url` XOR `oci`, add the `ParserOCI` field table (ref, digest, signer-is-rejected), state that
   `hash` stays mandatory for every source and doubles as the layer blob digest, and replace the
   "ships only as a versioned asset on the GitHub Release" sentence. **Delete the stale
   `version: "1.2.3"` line at `:970`** — it contradicts the same section's own no-version note and is
   a pre-existing bug. Add `oci` to "Security Requirements". Use the repo's BAD/GOOD block convention
   for the "don't declare both sources" guidance.
3. **`website/docs/guides/oci-bundles.md`** — the single-registry claim is now literally true for
   parsers. Add a "Mirroring everything" section: two `crane copy` commands, the two-key config
   override, digests survive mirroring. Add an explicit **Limitations** note (§13.2): datamitsu can
   authenticate only to GHCR; an authenticated Harbor/Artifactory/Nexus mirror is not yet supported,
   so the mirror must allow anonymous pull.
4. **`website/docs/reference/cli-commands.md`** — add `store refs`; state that `--no-oci` /
   `DATAMITSU_NO_OCI` disables OCI **bundle store seeding** and does **not** disable a declared parser
   `oci` source, and that `DATAMITSU_OFFLINE` is the hard gate. Fix `:711` ("module already on disk
   with a matching url and hash") and its embedded twin at
   `internal/llmsdocs/embed/pages/reference/cli-commands.md:708`.
5. **`website/docs/getting-started/installation/github-releases.md`** — the "only channel" line
   becomes "one of two channels"; mention the optional unstable prerelease now carries the module.
6. **`website/docs/guides/architecture/index.md`** — update the one-line parsers row summary.
7. **`website/docs/guides/supply-chain-security.md`** — under "Hash Verification (All Downloads)",
   add: a registry-sourced parser module is verified by the registry digest chain **and**,
   independently, by the mandatory config SHA-256; the layer-digest requirement makes the two the same
   value, so it is checked three times. Also state that a parser/bundle `ref` is a **trusted-host
   setting** — datamitsu holds a `GITHUB_TOKEN` when talking to registries, so pointing a ref at an
   untrusted registry is a credential-exposure decision, not merely a mirroring decision (true even
   after §3.2, and increasingly so once an auth surface exists).
8. **`CONTRIBUTING.md`** — extend "Local WASM parsers" (`file://`, double-locked by
   `ldflags.LocalArtifacts` + the per-call flag, stays the local dev loop; `oci` is how an unreleased
   module reaches downstream CI). Update the unstable-release section: the module is published to
   `ghcr.io/datamitsu/datamitsu-parsers-unstable` on every unstable run, the GitHub prerelease remains
   optional, and the loop still requires a human `workflow_dispatch` (§13.16). Update "Gated OCI e2e
   tier" for the two new cases.
9. **`internal/llmsdocs/embed/`** — regenerate with `task gen:llms-docs` and commit.
10. **Do NOT touch `website/docs/reference/parser-catalog.md`** — generated from the module's
    `describe` output by `packaging/parsers-catalog.ts` via `task build:parsers`.
11. **WRAPPER `docs/get-started/oci-bundle.md`** — per §9.8.

---

## 16. Deferred, named and costed

| Item                                                                                                                                    | Cost                                   | Why deferred                                                                                                 |
| --------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| `internal/ociauth` (docker `config.json` + per-registry credentials, host-scoped like §3.2, surfaced in `Effective` as host names only) | ~3 d + its own security review         | The real ceiling on the enterprise story; should be the very next arc                                        |
| `DATAMITSU_CA_BUNDLE` (additive `RootCAs` on `x509.SystemCertPool`, never `InsecureSkipVerify`)                                         | ~0.5 d                                 | Naturally bundles with `ociauth`                                                                             |
| `DATAMITSU_OCI_MIRROR` host rewrite                                                                                                     | ~1 d                                   | A config-layer override keeps the effective pin statically visible in `config show`; an env rewrite hides it |
| sigstore/cosign verification in Go                                                                                                      | separate project (new dependency tree) | Until it exists, `signer` stays rejected on both bundles and parsers                                         |
| `devtools parsers path`                                                                                                                 | ~0.5 d incl. three CLI gates           | `config show` + `store refs` cover the need                                                                  |
| Docker Hub parser mirror                                                                                                                | ~0.5 d                                 | §7.5 — optional; GHCR is the primary channel                                                                 |
