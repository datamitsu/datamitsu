# Plan: OCI bundles — packaging and distributing the tool-store via OCI

**Status:** design-doc v3.6 — **all decisions confirmed by the owner, no open questions,
ready to hand off for implementation.**
**Date:** 2026-06-09.
**Related:** `devtools dockerfile`/`split-config` (already in core), `internal/ocidigest`, `internal/dockerfile`.

> v2 was reworked after a 4-lens adversarial review against the code. Key changes vs v1: fixed
> the relocation misdiagnosis (paths are store-root-INdependent), added libc-pinning, a security
> section (re-verify against published SHA-256), per-subtree layers, a dedicated relocating
> extractor, failure-UX. Contested forks were moved to §11.
>
> v3 — second pass against the code + closing the open questions. Highlights: (a) the **layer
> annotation scheme** (`com.datamitsu.subtree`) — without it granular pull and the write-allowlist
> were unimplementable; (b) a **detailed pull algorithm** with per-subtree stat and a seed marker
> in the store; (c) resolved the §7 (empty config) vs §8 (labels) contradiction — **metadata
> config-blob**; (d) fixed factual errors of v2 (npm/pypi proxy, `install.go` does not call
> `EnsureTools`, added `cmd/init.go` as a seed point); (e) dependency choices:
> `opencontainers/image-spec` (types-only), sigstore — separate phase 1b, signer optional
> (⚠ this softens a v2 "DECIDED" — see §11); (f) all §11 questions closed with recommendations.
>
> v3.1 — **demand-driven pull**: auto-seed downloads from the bundle only the layers of the tools
> needed by the current operation (+their runtime dependencies), not the whole bundle — the
> "bundle of 50 tools / project needs 3" case costs 1 cacheable manifest GET + 3-5 blobs. A full
> pull happens only via an explicit `store seed`. See §3/§5/§11.8.
>
> v3.2 — **libc is a first-class dimension within a SINGLE index** (user decision, replaces
> per-libc ref/digest from v2): child manifests are distinguished by the descriptor annotation
> `com.datamitsu.libc`; annotations live inside the bytes of the index, which is verified against
> the pinned `oci.digest` → selecting by libc is as tamper-proof as selecting by os/arch. One
> ref+digest per bundle. See §2.5/§5/§7/§11.9.
>
> v3.3 — **target OS matrix** (user decision): linux×(amd64,arm64)×(glibc,musl) + darwin/arm64
> fully; darwin/amd64 and windows/amd64 — binary(+JVM)-only entries (built from a single linux
> host, no execution required); Windows runtime-app layers deferred (exe shims with embedded
> paths are not relocatable). Also closed a relocation hole: rewrite not only symlinks but also
> `pyvenv.cfg` + venv script shebangs. See §2.7/§2.14/§7/§11.10/§12.11.
>
> v3.4 — **producer = community toolchain, NOT native Go** (user decision, CANCELS the v2
> "fully native build/push"): linux layers are built by the existing buildx pipeline (the final
> stage ALREADY does per-subtree `COPY --link` — render.go:327, i.e. the bundle layers already
> exist in production images); annotations/bundle-index — post-processing with community CLIs
> (regctl/crane); signing — cosign CLI in CI; darwin/windows entries — oras/crane. The ONLY thing
> that stays native in datamitsu is pull (that IS the product: a consumer without docker).
> §2.12/§2.13 become a **format contract**, producer-agnostic. See §7/§10/§11.11.
>
> v3.5 — **no separate `oci` CLI namespace** (user decision): after v3.4 only two native commands
> remain, both store operations → `store seed` (explicit pull) and `store status` (coverage)
> under the existing `cmd/store.go`. The config key `oci` stays (it's the format, not the CLI).
> See §2.10/§5.
>
> v3.6 — final adversarial pass: (a) a layer WITHOUT a subtree annotation (base rootfs of the
> runnable image) is skipped, NOT fatal (otherwise pulling any buildx bundle would fail);
> (b) binary-only entries from a linux host require a target-override helper — binmanager is
> host-bound (§1); (c) `devtools oci-layer` materializes external hardlinks (pnpm→`.pnpm-store`),
> exactly like docker COPY does; go layers — check GOPATH/GOMODCACHE bloat; (d) the bundle index
> must be TAGGED — GHCR untagged-cleanup wipes index children; (e) Phase 1 split: binary/JVM
> right away, runtime-app relocation as a separate 1c; (f) `DATAMITSU_LIBC` — full
> env/Effective checklist.

---

## 0. The idea in one paragraph

datamitsu already knows how to download/verify/lay out tools into a content-addressed store.
We make it possible to **package the entire store into an OCI artifact**, push it to a registry,
and on the consumer side **pull it without docker/podman** (registry v2 → blob → unpack into the
store). The bundle is a **cache accelerator and a seed for airgap/offline**, not a replacement
for resolution and **not a trust boundary by itself**: whatever is in the bundle is taken from it
(os.Stat hit, no network); whatever isn't is downloaded the usual way (or fails in offline mode).
The integrity of every OCI blob is verified against its sha256 descriptor at pull time;
**additionally**, downloaded binaries/JARs are re-verified against the published SHA-256 from the
config (see §2 Security) — otherwise the digest chain only proves "the bytes are what the
registry declared", not "what the config author declared".

Second-order goals: offline/airgap, P2P distribution in k8s (Spegel/Dragonfly work **for free**
since the bundle is plain OCI), provenance/SBOM, restricted networks (proxy/registry env).

**Out of scope:** source-mode (PATH export) — separate track; cross-arch/cross-OS builds of
runtime apps **from a single host** (we don't take on emulation — per-platform CI runners +
bundle-index assembly with community CLIs, §7); **native build/push in datamitsu** (cancelled in
v3.4 — producer = buildx/regctl/oras/cosign, §7); **Windows** runtime-app layers (§2.14, §12.11 —
binary/JVM for Windows IS in scope).

---

## 1. Grounding (what exists / what doesn't — to avoid re-researching)

**Tool declaration is DATA, not a function.** Binaries live under the `apps` key of the single
`getConfig(input)=>Config`. On the Go side — type-alias maps in `internal/binmanager`
(`MapOfApps`, `App`, `MapOfBinaries = map[OsType]map[ArchType]map[string]BinaryOsArchInfo`),
included in `config.Config` (`internal/config/config.go:226`). The leaf is
`BinaryOsArchInfo{url, hash(SHA-256 mandatory), …}`.

**Layer model** (`cmd/config_loader.go`): sources `default` → `--before-config` →
`getBeforeConfigs()` → `auto`(root) → `--config`; processed sequentially, each `getConfig(input)`
receives the accumulated input and usually does `{...input,...own}`; `getRemoteConfigs()`
resolves depth-first BEFORE its own `getConfig` and is woven into the input. **The effective
config = the output of the last source.** JS↔Go field names come from
`goja.TagFieldNameMapper("json", true)` (`engine.go:50`) — hence `json:"oci,omitempty"` is
visible in JS as the key `oci`. **goja enumerates ALL exported fields as own-enumerable keys
regardless of value** (omitempty does not affect enumeration in the VM, only encoding/json) — an
inherited nil `oci` will flash as `oci: null` in spread results, but on export it correctly
collapses back to Go nil.

**OCI partially exists already:** `internal/ocidigest` is a working registry-v2 client: manifest
GET, 401 bearer handshake (`parseBearerChallenge`/`fetchToken`), GHCR PAT-as-Basic
(`GITHUB_TOKEN` → `SetBasicAuth("x-access-token", token)` against the realm from the challenge,
client.go:166-167), multi-arch Accept (`acceptManifests` — index+manifest×2, client.go:37-42),
XXH3 digest cache (`{cache}/.oci-digests/`, no TTL — only immutable tag→digest resolutions are
cached). **BUT**:

- it only resolves the `Docker-Content-Digest` header — **the body is NOT verified against the
  digest** and is discarded via `io.LimitReader(…, maxBodyBytes=1 MiB)` (client.go:30,134);
- it does NOT pull blobs; there are **no OCI manifest/index structs at all**; `opencontainers/*`,
  `go-containerregistry`, ORAS are **absent** from go.mod (see §5 — dependency decision);
- the bearer token is **NOT cached between `Resolve` calls** — every resolve is
  GET→401→token→GET; `authedGet` from §5 must add a (scope-aware) token cache — this is new
  behavior, not a refactor;
- `httpx.NewHardenedClient`: ≤10 redirects, https→http downgrade forbidden,
  `ProxyFromEnvironment`, per-phase timeouts; Authorization on cross-host redirects is stripped
  by **net/http itself** (standard behavior), hardenedRedirect doesn't touch headers — for the
  GHCR 307 to the CDN nothing needs doing, just don't re-send the header manually;
- production code has **neither a tar writer nor a zstd writer** (klauspost/compress is present
  but only `zstd.NewReader` is used; the `archive/tar` writer appears only in tests) → after v3.4
  these are needed only for the mini-helper `devtools oci-layer` (§7: deterministic subtree tar
  for darwin/windows entries); the full build pipeline is delegated to buildx.

`internal/dockerfile` + `cmd/devtools_dockerfile.go` — a generator of Dockerfile **text**;
layers/zstd/push/multi-arch are done by BuildKit. `storepaths.go` maps the subtrees:
`.bin/<app>`, `.runtimes/<name>`, `.apps/<kind>/<app>`, `.uv/python`; `runtimeCopiedToFinal`
excludes the Go SDK (build-only). `dockerfile/plan.go::BuildPlan` — deterministic traversal.

**Extractor/layout:** `extract.go::ExtractArchiveToDir` digests tar+gzip (`BinContentTypeTarGz`)
and tar+zstd (`BinContentTypeTarZst`) — exactly the two compressed OCI layer variants (it also
handles xz/bz2/plain-tar/zip, irrelevant for OCI). **BUT** (important for §5):

- it **skips absolute symlinks** (warn log, `extract.go:952-954`) and symlinks escaping dest;
- **hardcoded caps: 500 MiB per file (`MaxBinarySize`) + 2 GiB total per extraction**
  (`extract.go:892`, `download.go:20`) — not parameterized;
- it **widens directory modes** to `|0o755`; it **does not restore hardlinks** —
  `isRegularTarEntry` (extract.go:349-357) accepts only `tar.TypeReg`+`tar.TypeGNUSparse`
  (sparse supported since #101), `tar.TypeLink` is **silently dropped**; ownership/mtime are not
  restored, umask is not reset; the escape guard is **relative to the dest directory**.

`download.go::moveFile/moveDir` — atomic content-addressed layout. `verifyFileHash` is
**unexported**; the public wrapper is
`binmanager.VerifyFileHashPublic(path, hash, BinHashTypeSHA256)` (`verify.go:62`). Retry/backoff
(`downloadAndVerifyInternal`, permanent/retryable classifiers) are **unexported and coupled to
ui** → PullBlob cannot call them; a shared helper or re-implementation is needed.

**The store is content-addressed; the path is store-root-INdependent, the content is NOT always:**

- the `<hash>` segment = XXH3 over (URL, SHA256, contentType, binaryPath, extractDir, **OS, Arch,
  Libc**) for binaries (`binmanager/hash.go:33-42`) / (lockfile, packageName, version,
  runtimeHash…) for runtime apps — **the store root is NOT included**. Therefore the buildx layer
  `/dm/store/.bin/<app>/<hash>` and a pull into `~/.cache/datamitsu/store/.bin/<app>/<hash>`
  yield **the same `<hash>` → the paths match by themselves** (confirmed by `storepaths.go:9-16`).
- What is **not** relocatable is the **content**: the venv python is an absolute symlink into
  `{store}/.uv/python` (`uv.go:30,51`; `uvVenvHealthy` catches a dangling one → rebuild); pnpm —
  hardlinks into `{store}/.pnpm-store`; go — GOPATH/GOMODCACHE under the app dir. `.bin/*` and
  JVM jars are position-independent.
- **Libc is invisible to the STANDARD OCI index.** The platform object selects by
  os/arch/variant; there is no libc field. Yet libc IS part of `<hash>` → a glibc bundle on a
  musl host (or a fragile `unknown` detection on distroless, where there is no
  ldd/PT_INTERP/loader — `target/detection.go`) produces a **different** `<hash>` → os.Stat MISS.
  v3.2 resolution: we make libc visible via a descriptor annotation in the index (§2.5) — we
  control both the builder and the client.

**Trust today is purely path-based:** `os.Stat` on the `<hash>` path; **bytes already on disk
are never re-hashed** (`binmanager.go:294,361`; `uv.go:55`). The integrity of an imported layer
rests ONLY on what was verified **at pull time**.

**Environment:** `DATAMITSU_OCI_REGISTRY`=`ghcr.io` exists and is **already in
`runtimeconfig.Effective`** (`ociRegistry`); `DATAMITSU_OFFLINE` — **does not exist** (only the
CLI `--offline` of dockerfile). **Proxy clarification (v2 correction):** `registry/npm.go|pypi.go`
(`&http.Client{Timeout:15s}`) and `github/client.go` (30s) have a nil Transport →
`http.DefaultTransport` → **they DO honor proxy env vars**; what they lack is httpx hardening
(redirect guard, per-phase timeouts). Phase 7 is about hardening uniformity, not about proxies.
**The name `bundle` is taken** (`MapOfBundles` in Config) — but after v3.5 a separate CLI
namespace isn't needed anyway: commands live under `store` (§2.10).

**Tool installation entry points (for the seed seam §3, v2 correction):** `EnsureTools` is
called ONLY from `runner.go:424`. `cmd/install.go` bypasses it: `rm.InstallRuntimes` (`:91`) +
`installSmartInitApps` (`:103`). The third point, missed in v2, is **`cmd/init.go`**:
`InstallRuntimes` (`:256`), `binMgr.InstallWithConcurrency` (`:276`), `installSmartInitApps`
(`:488`). Plus lazy resolution via `GetBinaryPath` (single-flight `downloadGroup.Do`,
binmanager.go:372) on the exec/run paths.

---

## 2. Locked decisions

1. **Declaration — a scalar top-level `oci` on Config** (`*OCIRef{Ref, Digest}`), not a
   function/array.

   ```ts
   // digest = sha256:<64hex> = the mandatory bundle hash; signer — optional identity pin (§2 Security)
   oci?: { ref: string; digest: string; signer?: { identity: string; issuer: string } };
   ```

   The maintainer releases the config in two variants (plain / OCI-pinned); pinned is the same
   source with `oci` set. The consumer attaches one of them via before/remote/`--config`.

2. **"Which bundle" resolution — from the existing chain, NO special pass.** `oci` chains as a
   scalar: the last one wins; a layer that doesn't set it but does `{...input}` inherits it.
   **The effective config has ≤1 `oci`** → "10 extra OCIs" are structurally impossible.
   **Precondition (load-bearing!):** inheritance holds only if every intermediate layer does
   `{...input}`; a layer that reshapes its output **without spread silently drops** the inherited
   `oci` (same as with apps/runtimes, but here the failure is silent — there is no re-inject from
   the default config). Reset — `oci: undefined` OR `oci: null` (both → Go nil).
   _Footgun mitigation:_ a debug log when `input.oci != nil` but `output.oci == nil`; a Phase-0
   test for the drop case. Precise precedence wording: "the last layer that set OR spread `oci`
   wins", not literally "root".

3. **The bundle is a seed, not a closure, and NOT a trust boundary.** After the pull: os.Stat
   hits skip the network, misses are downloaded (online) / fail (offline). A partial bundle is
   valid as a partial seed.

4. **Security — re-verify whatever is verifiable (see the dedicated block below).**

5. **Libc — a first-class selection dimension within a SINGLE index (revised in v3.2; replaces
   per-libc ref/digest from v2).** The standard OCI `platform` object doesn't carry libc, but we
   control both sides → child manifests in the index are distinguished by the **descriptor
   annotation** `com.datamitsu.libc: glibc|musl` (linux; non-linux entries carry no annotation).
   Selection at pull: os/arch via `platform` + libc via the annotation (host detection from
   `target.DetectLibc` or the `DATAMITSU_LIBC` override — **yes**, it closes the fragile
   detection on distroless). Two entries with the same os/arch (glibc+musl) are legal for our
   client; `docker pull` of the bundle is a non-goal (§7), generic tools won't disambiguate such
   an index — accepted. **Security:** annotations live inside the bytes of the index, which is
   verified against the pinned `oci.digest` → selecting by them is as tamper-proof as selecting
   by os/arch; not a single unverified byte enters the chain. **UX:** the maintainer has ONE
   ref+digest per bundle (not a glibc/musl config pair), nothing to mix up; `store status` shows
   all (os/arch/libc) variants. Host libc and the resolved target are exposed in
   `datamitsu config runtime` (Introspectable) for miss diagnostics. The native build (§7) builds
   the glibc and musl sets in separate `BuildPlan` runs with the `TargetLibc` filter (`plan.go`
   already supports it); the CI post-process (regctl, §7) merges ALL (os/arch/libc) manifests
   into one bundle index.
   The store-path XXH3 already includes libc (`hash.go`) — nothing to change there.

6. **Per-subtree layers, not one whole-store layer.** One layer per `.bin/<app>` /
   `.runtimes/<name>` / `.apps/<kind>/<app>` / `.uv/python` (the `storepaths.go` model). Reasons:
   bypassing the 2 GiB cap; dedup of shared subtrees (CPython) across arches via cross-repo
   blob mount; granular invalidation.

7. **Content relocation — a relocating extractor, not the generic one. DECIDED.** The store seed
   is unpacked NOT by `ExtractArchiveToDir` (it drops absolute symlinks and caps out at 2 GiB)
   but by a dedicated extractor that: (a) **rewrites the absolute store-root prefix** in symlink
   targets to the consumer's store root and recreates the link (the builder's prefix comes from
   the manifest annotation `com.datamitsu.store-root`, §2.13);
   (a2, **new in v3.3 — hole closed**) rewrites the same prefix in **known textual carriers of
   absolute paths**: `pyvenv.cfg` (`home = /abs/...` — without it the venv is broken even with a
   live symlink) and the **shebangs** of `venv/bin/*` scripts (`#!/abs/.../python`); only
   whitelisted files/patterns, no length-preserving replacement needed (it's text), binary files
   are NEVER touched; (b) enforces a **per-subtree write-allowlist** (a layer writes only inside
   its own subtree from the §2.12 annotation, no cross-app overwrite); (c) parameterized caps
   (§11.1); (d) restores pnpm hardlinks (`tar.TypeLink`; the target must lie within the same
   subtree — otherwise fatal). The "pnpm `package-import-method=copy` at build time" variant is
   NOT the primary one: pnpm 11 ignores env config (`node.go:31-32`); only the TypeLink roundtrip
   is reliable (§7). JVM jars/binaries are trivial; uv-venv/pnpm-hardlinks are the target hard
   cases. Consequence for Windows: pip/uv exe shims carry the absolute path **inside the
   binary** → not amenable to textual relocation → Windows runtime-app layers deferred (§2.14).

8. **Trust in `oci.digest` = trust in the config source.** The digest comes from the config,
   which itself may arrive via `getRemoteConfigs()` (with its mandatory hash). **v3 refinement
   (important, corrects the v2 wording):** a pinned `oci.digest` is ALREADY a complete
   byte-level guarantee for the entire bundle: the manifest is verified against the digest, every
   blob against its descriptor from the manifest; this is a closed sha256 chain, identical in
   strength to `BinaryOsArchInfo.Hash`. A signature adds NOT bytes but the **publisher's
   identity** (who built it) — it protects against compromise of the digest's source itself
   (the remote-config chain). Therefore: we sign bundles at publish time (as decided in v2), but
   **verification at pull happens when `oci.signer` is set** (see Security below and §11.3).

9. **Multi-arch — via the OCI image index** (os/arch/variant); libc — via a descriptor
   annotation in the same index (item 5, v3.2).

10. **NO separate CLI namespace (revised in v3.5; cancels the v2 "namespace `oci`").**
    After v3.4 only two native operations remain, both about the store → subcommands of the
    existing `datamitsu store` (`cmd/store.go`): `store seed` and `store status`. This also
    removes the old `bundle` name collision. The config key `oci` stays — it describes the
    declaration/format, not the CLI; `--no-oci`/`DATAMITSU_NO_OCI` are named after the config key.

11. **`oci.ref` — a full reference including the host** (`ghcr.io/owner/repo`); a tag in the ref
    is forbidden (the digest is mandatory anyway; `ref` identifies the repository, `digest` the
    content). No docker.io magic and no default host. `DATAMITSU_OCI_REGISTRY` pertains to the
    devtools-dockerfile base-image resolution and does **not** affect `oci.ref`; a mirror
    override for restricted networks is Phase 7. Format validation — Phase 0 (§4).

12. **Layer↔subtree mapping — via OCI annotations on the layer descriptor** (new in v3; without
    it granular pull, the write-allowlist and `store status` are unimplementable):
    - `com.datamitsu.subtree` — the store-relative subtree path (`.bin/golangci-lint/<hash>`,
      `.runtimes/node/<hash>`, `.apps/uv/<app>/<hash>`, `.uv/python`);
    - `com.datamitsu.kind` — `binary|runtime|app|uv-python`;
    - optional `com.datamitsu.app` — the app/runtime name for `store status`.
      Pull: stat by `subtree` → HIT = the blob is not downloaded at all; the extractor writes
      ONLY inside its own `subtree` (allowlist). **A layer WITHOUT the `com.datamitsu.subtree`
      annotation is silently skipped (v3.6 fix):** the bundle manifest = an annotated runnable
      image; its base layers (rootfs, datamitsu, config) carry no annotations and are not store
      content. Fatal — only a layer WITH an annotation that is malformed: a prefix outside the
      known set of subtree roots, `..`, or an absolute path. **How annotations get into the
      manifest is the producer's business** (v3.4): for buildx builds the CI post-process writes
      them based on the generator's `map.json` (§7); for oras entries the push command sets them.
      One contract; the consumer doesn't care. A mapping mistake is NOT a hole: the allowlist
      validates layer content against the declared subtree and fails loudly.

13. **Bundle metadata — in MANIFEST ANNOTATIONS; the config blob is not interpreted (revised in
    v3.4).** Previously: a custom config blob `vnd.datamitsu.bundle.config.v1+json`. Now the
    producer is buildx (§7), whose manifests have config = a plain docker image config; a custom
    config can't be inserted there without a rebuild. The contract: all metadata lives in
    manifest annotations:
    - `com.datamitsu.store-root` — the builder's store root (needed by the relocating extractor
      §2.7 for prefix rewriting; for buildx images this is `/dm/store`);
    - `com.datamitsu.libc`, `com.datamitsu.from-config-hash`, `com.datamitsu.datamitsu-version`.
      A separate tool list is NOT needed — coverage is derived from the §2.12 layer annotations
      (`store status`). Pull **never downloads the config blob** (one fewer GET); its mediaType
      is not validated (a docker config for buildx entries, anything for oras entries).
      Annotations are inside the bytes of the digest-verified manifest → tamper-proof, like
      everything else.

14. **Target OS/arch matrix (v3.3, user decision).**
    - **Full entries (binary + runtime-app layers):** linux/amd64 and linux/arm64 ×
      (glibc, musl); **darwin/arm64** (amd64 Macs are being phased out — we don't build runtime
      layers for them).
    - **Binary(+JVM)-only entries — "free":** darwin/amd64 and **windows/amd64** (+optionally
      windows/arm64). The key fact: laying out a binary = download from the per-platform URL →
      SHA-256 verify → extract → tar; **no execution needed** → such entries can be built from a
      SINGLE linux host for any os/arch — but via the **target-override helper**
      `devtools oci-binary-store --target` (v3.6, §7): binmanager is host-bound (§1) and won't
      download a foreign target by itself. For Windows this covers `.bin/*` (zip is already
      supported by the extractor) and JVM jars.
    - **Windows runtime-app layers — deferred** (not "out of scope forever"): pip/uv `.exe`
      shims with the absolute path embedded (not text-relocatable, §2.7), pnpm junctions,
      symlinks require developer mode. A separate research spike after phase 6; until then a
      Windows host gets binaries/JARs from the bundle and runtime apps via the regular network.
    - In the index these are just `platform{os,arch}` entries (windows/darwin — without the libc
      annotation); "binary-only" is NOT a special type — just a regular manifest that happens to
      have no runtime/app layers; `store status` shows coverage per platform.
    - darwin/arm64 runtime layers are built on macos-14/15 CI runners (native arm64), phase 4:
      native `datamitsu install` + `devtools oci-layer` + oras/crane push (§7, v3.4).

### Security & trust boundary (new, from the review)

The problem: "verify blob against the descriptor digest" only proves "the bytes match what the
**registry** declared", not "what the **config author** declared (URL+SHA256)". Every other
download path checks bytes against the hash from the config (`validate.go:58`,
`binmanager.go:634`); an OCI seed without an extra check would not.

The solution (two-tier, preserves speed):

- **Binaries and JVM jars** — after unpacking a layer we **re-hash the local file against the
  published `BinaryOsArchInfo.Hash` / `JarHash`** from the effective config
  (`VerifyFileHashPublic`, SHA-256). This is cheap (a local re-hash ≪ a network redownload) and
  closes the hole for the most common case.
- **Runtime app directories (uv/node/go)** — there is no published content hash (the directory
  is built, not downloaded) → they are trusted via the **manifest sha256 chain** (the digest pins
  everything) **+ lockfile + the optional signature**. This is an honest, documented boundary.
- **Signature (v3 refinement, implementation):**
  - The config gains `oci.signer?: { identity: string; issuer: string }` (sigstore keyless:
    certificate identity + OIDC issuer, e.g. a GitHub Actions workflow ref +
    `https://token.actions.githubusercontent.com`). `signer` set → pull MUST verify the
    signature BEFORE layout; failure → fatal. Not set → verification is skipped (byte integrity
    is still guaranteed by the digest chain); `store status` shows unsigned/signed.
  - Library: **`sigstore/sigstore-go`** (the official verification library), NOT
    cosign-as-a-library (a huge dependency tree) and NOT shelling out to a cosign binary
    (bootstrap cycle: the seed verifier would itself arrive through the store). ⚠ sigstore-go is
    still heavy (protobuf etc.) — measure the binary size delta; hence verification is split
    into a **separate phase 1b**, and Phase 1 ships without the sigstore dependency.
  - This softens the v2 "DECIDED: pull verifies the signature" — rationale in §2.8 (the digest
    already provides the bytes, the signature provides identity). **Confirmed by the owner
    2026-06-10 (§11.3).**
- **Threat model in the docs:** whoever controls `oci.digest` (or MITMs the remote config
  delivering it) → code execution via swapped runtime directories. `signer` closes this vector
  too; without `signer` the trust boundary = the config source (exactly as today for
  `BinaryOsArchInfo.Hash` — no worse than the status quo).
- **No blob cache (v3, KISS):** a blob is streamed to temp, unpacked, deleted; a subtree in the
  store IS the cache (stat hit → the blob is never re-downloaded). The digest's seed marker
  lives **inside the store** (`{store}/.oci-seeded/<digest>`) → `datamitsu store clear` removes
  it together with the store automatically — no stale trust. The `.oci-digests` cache
  (tag→digest) is untouched — it only stores immutable resolutions anyway.
- **Offline is orthogonal to OCI** (DECIDED): offline is a standalone "don't touch the network"
  flag, NOT an auto-pull of the bundle. Seeding the store is a separate step (an explicit
  `store seed` while online / manual placement / volume mount), while offline simply refuses all
  network, including the OCI registry. See §6.

---

## 3. Pull lifecycle (where and when)

```text
loadConfig… → effective cfg (cfg.OCI?)                 # config eval is pure, no tools needed
   ↓  [cfg.OCI != nil AND not --no-oci AND NOT offline]   # offline does NOT auto-pull — all network is off
maybeSeedOCIBundle: pull ref@digest → (signer? → sigstore-verify) → relocating-extract into {base}/store  # SEED
   ↓  (+ re-verify binaries/JARs against published SHA-256; serialize BEFORE any lazy downloads)
EnsureTools / install / check / lint …                 # os.Stat hits come from the seed, misses are downloaded
```

- **The seam (fixed in v3):** a single helper `ocibundle.Seed(ctx, cfg, needed []string)`,
  invoked at **three** cmd points — exactly where the target tool set is already known:
  1. `runner.runSingleOperation` — BEFORE `EnsureTools` (`runner.go:424`, the only `EnsureTools`
     call site in the codebase), `needed = plan.GetAppNames()` (the planner has already narrowed
     it to what's relevant for the project);
  2. `cmd/install.go` — BEFORE `rm.InstallRuntimes` (`:91`) / `installSmartInitApps` (`:103`)
     (install does NOT go through `EnsureTools` — a v2 error), `needed` = its target list;
  3. `cmd/init.go` — BEFORE `InstallRuntimes`/`InstallWithConcurrency`/`installSmartInitApps`
     (`:256/:276/:488`) — the point missed in v2; `needed` = the set being installed.
     Lazy resolution via `GetBinaryPath` (the exec path) is covered because all three points
     above run before any store access in the respective commands; other commands don't write to
     the store.
- **Demand-driven seed (new in v3.1):** `needed != nil` → ONLY the layers of the needed tools are
  downloaded, **plus their transitive store dependencies** (the runtime layer for a runtime app,
  `.uv/python` for uv). Matching requires no extra metadata: the consumer computes the expected
  store paths of the needed tools with the same hash math as the regular stat check
  (`binmanager/hash.go`, `runtimemanager.GetAppPath`) and compares them with the
  `com.datamitsu.subtree` layer annotations (exact string equality). The "bundle of 50 tools /
  project needs 3" case costs 1 manifest GET (which is cached, see §5) + 3-5 blobs.
  `needed == nil` (explicit `store seed`/airgap seeding) = the whole bundle.
- **Idempotence/races:** the fast no-op is two-stage: (a) demand-driven — all expected subtrees
  of the needed set are already in the store → **zero network** (the same stat binmanager does
  anyway); (b) full pull — `os.Stat({store}/.oci-seeded/<digest>)` (the marker is written ONLY
  after ALL layers are laid out; see §5). The seed **is serialized before** the concurrent
  single-flight resolution (`binmanager.go:372`, EnsureTools `:397`); the inter-process race is
  handled by a per-subtree file lock (two parallel datamitsu invocations must not unpack the
  same subtree simultaneously; in-process — single-flight per subtree, not sync.Once: a repeat
  call with a different `needed` is legal).
- **Kill switches:** `--no-oci` (a persistent flag on root) and `DATAMITSU_NO_OCI` — both
  **introspectable** (§6).

---

## 4. Config declaration (Phase 0)

- `config.go:226`: `OCI *OCIRef \`json:"oci,omitempty"\``. The type:

  ```go
  type OCIRef struct {
      Ref    string     `json:"ref"`              // ghcr.io/owner/repo — host required, tag forbidden
      Digest string     `json:"digest"`           // sha256:<64hex>
      Signer *OCISigner `json:"signer,omitempty"` // sigstore identity pin (verify-if-present, §2)
  }
  type OCISigner struct {
      Identity string `json:"identity"`
      Issuer   string `json:"issuer"`
  }
  ```

  A pointer-typed top-level field in Config is the **first one** (the rest are maps/slices with
  omitempty) — a new pattern, but a safe one. **`omitempty` is mandatory:** `cache.go:447`
  (`calculateInvalidationKey`) marshals the **entire** Config into the execution-cache
  invalidation key — without omitempty a nil field produces `"oci":null` and invalidates the
  cache of all tools on upgrade. (Per-binary store paths are unaffected —
  `binmanager/hash.go::calculateConfigHash` hashes only `BinaryOsArchInfo`.)

- **v3 decision (execution cache):** when `oci` IS set, a digest change invalidates the
  execution cache (the whole Config is in the key) — a spurious but harmless one-time
  invalidation (the bundle doesn't change tool behavior, but a digest bump usually accompanies a
  tool bump anyway). Accepted as is; we do NOT strip `oci` from the key — KISS; document it in a
  comment next to `calculateInvalidationKey`.
- `validate.go`: the gate (pattern at `:58`): `Ref!=""` and matches
  `^[a-z0-9.-]+(:[0-9]+)?(/[a-z0-9._-]+)+$` (a host with a dot/port + a path; no `:tag` and no
  `@digest` inside ref); `Digest` starts with `sha256:` and its tail passes `isValidSHA256Hex`
  (`:337`); if `Signer` is set, both fields must be non-empty. Empty/malformed → error
  (collected into the `errs` slice, as in `doValidateApps`).
- `config/config.d.ts` **and** `internal/config/config.d.ts` (byte-identical): `oci?` + the
  `OCIRef`, `OCISigner` types. **There is currently NO sync test for the two copies** — add a
  trivial byte-compare test (reads both, compares) right in Phase 0 so the implementer can't
  drift them apart.
- Tests: parsing `oci` (incl. `signer`); the gate (malformed ref/digest/one-legged signer);
  chaining (the last one wins; spread inherits; **non-spread drops** ← the drop case + the debug
  log from §2.2; `undefined`/`null` reset); regression: `Marshal(Config{})` is byte-identical to
  today; a test pinning the actual goja behavior of a nil `*OCIRef` under spread (v2 claims "the
  key exists with value null" — verify it, it's a load-bearing detail of §2.2).

---

## 5. Consume: `store seed` (Phase 1 — the main chunk)

Extend `internal/ocidigest` from "digest resolution" to "content pull"; orchestration
(seed/extract/verify) goes into a new package `internal/ocibundle` (so that ocidigest stays a
thin registry client with no dependency on binmanager).

**Dependencies (v3 decision):** add `github.com/opencontainers/image-spec/specs-go/v1`
(types-only: Manifest/Index/Descriptor/Platform, no transitive deps) + if needed
`opencontainers/go-digest`. Do **NOT** pull in `go-containerregistry`/ORAS (heavy, duplicate our
auth/httpx layer). sigstore-go — only in phase 1b (§2 Security).

- **`authedGet`** — a refactor of the single-shot handshake out of `Resolve` (client.go:86-112):
  **every** endpoint (manifest/blob) can get its own 401; scope `repository:<repo>:pull`; **the
  token is cached in process memory** (per registry+repo+scope; today it is NOT cached — see §1,
  this is new behavior). The host comes from `oci.ref` (§2.11), not from `DATAMITSU_OCI_REGISTRY`.
- **`PullManifest(ctx, host, repo, digest)`** — GET `/v2/{repo}/manifests/{digest}` (Accept =
  `acceptManifests`). Read the body (cap **4 MiB** — keep the current `maxBodyBytes`=1 MiB for
  the resolver), **verify sha256(body)==digest** (today Resolve trusts the header — for pull
  that's unacceptable). If it's an index — pick the descriptor by host os/arch
  (`target.HostTarget()`; variant-tolerant matching: arm64 may or may not carry
  `variant:"v8"`) **AND by the `com.datamitsu.libc` annotation** (v3.2, §2.5: host detection or
  `DATAMITSU_LIBC`; a linux host with `LibcUnknown` and no override → warn + fall-through, do
  NOT guess); the chosen child manifest is pulled and verified the same way.
  No (os, arch, libc) match → warn + fall-through to the network (offline → fatal) — decided,
  §11.2. **A bare-tag pull without a digest violates mandatory-hash** → the CLI must first
  `Resolve` (tag→digest), show the digest and pin it, or refuse (a `--resolve-tag` flag for
  explicitness).
- **`PullBlob(ctx, host, repo, descriptor, w)`** — GET `/v2/{repo}/blobs/{digest}` via
  `httpx.NewHardenedClient`, retry/backoff: **extract `retryDelay`/`isPermanent`/`retryableStatus`
  from `binmanager/download.go` into a shared package** (e.g. `internal/httpretry`) — they are
  private and coupled to ui (`ui.Current().Download`, download.go:148); blob progress goes
  through the same ui mechanism (see memory: binmanager is already on the unified mpb). Stream
  into `{store}/.tmp` (same FS → atomic rename), **verify against descriptor.digest AND
  descriptor.size**, parameterized cap (defaults in §11.1). digest/size mismatch = permanent
  (no retry), like a hash mismatch in download.go. **GHCR returns a 307 to
  `pkg-containers.githubusercontent.com`** — net/http itself strips Authorization on cross-host
  (httpx `hardenedRedirect` doesn't touch headers, it only forbids https→http); the redirect
  target is untrusted → security rests on post-download verification.
- **mediaType→format:** `…layer.v1.tar+gzip`→`BinContentTypeTarGz`,
  `…tar+zstd`→`BinContentTypeTarZst` (+the docker variants `…image.rootfs.diff.tar.gzip` —
  buildx builds emit those); an unknown layer mediaType → refuse. The config blob is NOT
  downloaded and NOT interpreted — all metadata lives in manifest annotations (§2.13, v3.4).

**The `store seed` / `Seed(cfg, needed)` algorithm (v3, demand-driven in v3.1 — step by step so
the implementer doesn't have to invent it):**

1. Parse `ref` → host/repo; `digest` from the config/CLI; `needed` — the target tool set
   (nil = everything).
2. Fast no-op: `needed != nil` → compute the expected store paths of the needed tools
   (+transitive runtime/`.uv/python` dependencies) and stat them; all present → **exit, zero
   network**. `needed == nil` → `os.Stat({store}/.oci-seeded/<digest>)` → present → exit.
3. `PullManifest` (by digest, with body verification; index → select the (os, arch, libc)
   descriptor per §2.5 → child manifest). The manifest body **is cached on disk by digest**
   (`{cache}/oci/manifests/`) — the content is immutable → the cache is eternal, like
   `.oci-digests`; repeated partial pulls of the same digest never fetch the manifest again.
4. Phase 1b: `signer` set → sigstore verification of the manifest signature, failure → fatal.
5. Metadata — from the annotations of the already-verified manifest (`com.datamitsu.store-root`
   for relocation §2.7; tool coverage — from the layer annotations); the config blob is not
   downloaded (§2.13, v3.4).
6. For each layer descriptor: NO `com.datamitsu.subtree` annotation → the layer is **silently
   skipped** (a base layer of the runnable image, §2.12, v3.6); an annotation that exists but is
   malformed (outside the subtree roots, `..`, absolute) → fatal; `needed != nil` AND subtree ∉
   the expected paths of the needed set → the layer is **ignored** (the demand-driven filter §3);
   otherwise `os.Stat({store}/{subtree})` → present → the layer is **skipped without
   downloading** (granular cache); absent → queued.
7. Missing layers are downloaded concurrently (`env.GetConcurrency()`, default 3) → temp file →
   relocating extract (§2.7: `storeRoot` prefix rewriting for symlinks, hardlinks,
   write-allowlist = own subtree, parameterized caps) into a **temporary directory** →
   re-verify binaries/JARs against the published SHA-256 (§2 Security) → atomic `moveDir` into
   `{store}/{subtree}` (reuse `download.go::moveDir`; if the subtree appeared concurrently —
   fine, drop our temp).
8. The marker `{store}/.oci-seeded/<digest>` (contents: ref, date, subtree list) is written
   **only on a full pull** (`needed == nil`) when ALL layers are laid out. A demand-driven pull
   writes no marker (its idempotence is the subtree stat, step 2). A partial failure → no marker;
   the subtrees already laid out remain (they are self-consistent and valid as a partial seed,
   §2.3).

**CLI (v3.5 — no separate namespace, §2.10):** subcommands of the existing `cmd/store.go`:
`store seed [<ref>@<digest>]` (no arguments = from the effective config) — a **full** pull by
default (airgap seeding); `store seed --apps slidev,prettier` — selective (the demand-driven
path §3, same code); `store status` — coverage (§6); plus `store import <oci-layout-dir|tar>`
for fully offline transfer (a separate Phase 2 sub-task; format — the standard OCI image layout).

**Tests:** an httptest fake registry (the ocidigest test pattern): index→arch selection (with
and without variant); **an index with glibc+musl entries for the same os/arch → selected by host
libc / `DATAMITSU_LIBC`; `LibcUnknown` without an override → fall-through, glibc is NOT slipped
in**; manifest body digest mismatch→fatal; blob digest/size mismatch→fatal (no retry); an
annotation battle test (foreign subtree/`..` → fatal; NO annotation → silent skip); an absolute
venv symlink survives the pull (relocating extractor); a hardlink layer (pnpm) is restored;
cross-app overwrite is forbidden; re-verification catches a swapped binary; layer skip with a
warm subtree (network request counter = 0 for covered ones); **demand-driven: from a bundle of N
tools with `needed`={3 tools} exactly the layers of those tools + their runtime layers are
downloaded, no other blob GETs happen (fake-registry request counter); a runtime app also pulls
its runtime's layer; a repeated demand-driven pull of the same digest doesn't fetch the manifest
(disk cache by digest)**; a repeated full pull = no-op via the marker; a partial failure writes
no marker; offline+miss→hard-fail.

---

## 6. Auto-seed + offline + introspection (Phase 2)

- `maybeSeedOCIBundle(ctx, cfg, needed)` from the §3 seam — auto-seed is always demand-driven
  (downloads only what the current operation needs); a full pull happens only via an explicit
  `store seed`.
- **`DATAMITSU_OFFLINE`** — the full CLAUDE.md checklist (Runtime Config Policy): an envVar in
  `env/e.go`, a getter `env.Offline()` in `env.go`, a json field in `runtimeconfig.Effective` +
  wiring in `Compute()`, tests in `env_test.go` AND `runtimeconfig_test.go`, **no key-count
  guards**. `DATAMITSU_NO_OCI` — same (also a behavioral network toggle → also in `Effective`,
  otherwise the feature that adds network egress gets a hidden non-introspectable input).
  **`DATAMITSU_LIBC` (§2.5) — the same full checklist** (v3.6: it affects index descriptor
  selection and store paths → must be introspectable; today `target/detection.go` has no env
  override at all — this is a new envVar).
- Offline semantics (DECIDED): **a full "don't touch the network" flag — blocks EVERYTHING,
  including the OCI registry.** Offline and OCI are orthogonal: the store is seeded separately
  (an explicit `store seed` while online / manual placement / a volume mount), while offline
  simply refuses all network. The auto-seed from `cfg.OCI` does **not** run under offline (a
  pre-seeded store is required). A tool miss under offline → hard fail.
- Offline wiring (v3 detail): a single point — a helper in `httpx` (e.g.
  `httpx.GuardOffline(ctx) error` or a RoundTripper wrapper returning a typed `ErrOffline`
  BEFORE dialing), called from all network clients: `binmanager/download.go`,
  `runtimemanager/*` (incl. child pnpm/uv/go processes — their network can't be cut, so offline
  must fail BEFORE the install processes start), `registry/npm.go|pypi.go`, `github/client.go`,
  `remotecfg` **and `store seed` itself**. ⚠ "Not a single syscall to the network" hermeticity is
  not guaranteed until Phase 7 (npm/pypi/github are not yet on httpx) — until then offline checks
  go at the top of their public functions. `ErrOffline` names the tool in its message and points
  to the §13 remediation.
- **A new introspection command** (Introspectable-by-Design): `store status` — what the bundle
  contains and which of the configured apps it covers vs which require the network.
  `config runtime` alone doesn't cover this.

---

## 7. Build/Push: community toolchain; only PULL is native to datamitsu (revised in v3.4)

**CANCELS the v2 decision "fully native build/push".** Motivation (user decision): don't
maintain our own docker — a native build/push is thousands of lines (tar/zstd writer, chunked
upload, blob mount, index assembly) and an eternal maintenance tail, while the MAINTAINER side
has docker/buildx anyway, and the whole custom machinery is only needed on the CONSUMER side —
and exactly that stays native (pull without docker = the product). §2.12/§2.13 thereby become a
**format contract**: the consumer doesn't care who produced the bundle; a native producer can be
added later without touching the pull code.

- **The linux layers already exist.** The final stage of the generated Dockerfile does
  per-subtree `COPY --link --from=<stage> <abs> <abs>` (`render.go:327`) — each COPY = a separate
  OCI layer with exactly one subtree. datamitsu-config CI already builds and pushes these images
  (incl. the zstd pipeline). Inside the docker build, per-app installs run with verification
  against the published SHA-256 — layer content is verified at layout time.
- **Post-process (CI, community CLIs — regctl/crane):** runnable tags are NOT touched
  (docker pull keeps working). **The bundle index must be TAGGED** (`:bundle-<version>`; the
  consumer still pins the digest): untagged manifests/indexes get mowed down by GHCR
  "delete untagged" cleanup actions — a known footgun that wipes the children of multi-arch
  indexes (risk §12.15). The script: takes the manifests of the built per-libc images →
  **writes in the §2.12 layer annotations** (layer→subtree mapping: the generator knows the
  COPY --link order and emits `map.json` — a new lightweight output
  `devtools dockerfile --emit-oci-map`; a mapping error is NOT a hole — the extractor's
  write-allowlist validates content against the declared subtree and fails loudly) and the
  **§2.13 manifest annotations** (storeRoot, libc, from-config-hash) → puts them as NEW
  (untagged) manifests into the same repo — blobs are shared, overhead = kilobytes of JSON →
  assembles the **bundle index** (descriptors: platform{os,arch} + `com.datamitsu.libc`, §2.5;
  filter out the buildx attestation manifests `unknown/unknown`) → **cosign sign (CLI)** of the
  final digest → the digest is pinned into the config. Signing is the standard cosign flow, no
  signing code in datamitsu (verification at pull is separate — sigstore-go, phase 1b: the
  consumer has no cosign).
- **darwin/windows entries — docker CANNOT build them (containers = linux):** on the native
  runner (macos-14/15 for darwin/arm64) a regular `datamitsu install` lays out the store
  natively (with verification); the small helper **`devtools oci-layer`** (deterministic subtree
  tar — sorted traversal, zero mtime/uid/gid; hardlinks WITHIN the subtree — as `tar.TypeLink`;
  hardlinks pointing OUTSIDE the subtree (pnpm→`{store}/.pnpm-store`) — **materialized into
  regular files** (v3.6; exactly what docker COPY does — that's why the buildx layers work;
  `.pnpm-store` and other install caches are NOT bundled)) + `oras push`/`crane append` push the
  layers and a manifest with the same annotations.
  **Binary-only entries for foreign targets (windows/amd64, darwin/amd64) need the
  target-override helper (v3.6):** binmanager is host-bound (`HostTarget()`, §1) — it can't
  "download windows binaries from linux" by itself; the new `devtools oci-binary-store --target
<os>/<arch>[/<libc>] --output <dir>` iterates the config declarations for the given target
  through the same download+SHA-256-verify+extract (no execution) and lays out the subtree tree
  for `oci-layer`. This is small but NOT zero Go code — an explicit part of Phase 4. All these
  manifests are added to the same bundle index. ⚠ Go app layers: GOPATH/GOMODCACHE live INSIDE
  the app subtree (`goBaseEnvVars`) — check the layer size; pruning build caches at packing time
  is likely needed (runtime only needs `bin/`).
- **Cross-platform — DECIDED (§11.4): no QEMU.** Per-platform CI runners + assembling the bundle
  index from ready digests (manifest-only, regctl) on a single runner. Windows runtime layers —
  the deferred research spike (§2.14).
- ⚠ pnpm subtree: pnpm 11 ignores the `npm_config_store_dir` env (node.go:31-32) — do NOT rely
  on `package-import-method`; the contract is a correct TypeLink roundtrip (a parity test
  tar↔relocating-extractor for the oras path; the buildx path is tarred by BuildKit, which
  preserves hardlinks itself).

---

## 8. Provenance / bundle metadata (Phase 5)

- **The basic metadata is already in the §2.13 manifest annotations** (libc, from-config-hash,
  datamitsu-version, storeRoot; tool coverage — from the layer annotations) — available from
  Phase 3.
- **SBOM/provenance — via the community toolchain (v3.4):** for the buildx path —
  `buildx --provenance --sbom` (in-toto attestations are generated and attached by BuildKit
  itself); for oras entries — `cosign attest`/`cosign attach`. The referrers mechanics
  (OCI 1.1 `/referrers/` vs the fallback tag `sha256-<digest>`) are handled by cosign/oras
  themselves — datamitsu has no code here; the consumer never downloads attestations (outside
  the pull path) — they are artifacts for audits/security engineers.
- Signatures (cosign CLI) — §7; for an untrusted registry see §2.8.

---

## 9. Restricted networks (Phase 7, separate)

`DATAMITSU_NPM_REGISTRY`/`DATAMITSU_PYPI_INDEX`/per-registry creds via `internal/env`; migrate
`registry/npm.go|pypi.go`, `github/client.go` to `httpx.NewHardenedClient` (a unified
proxy+TLS guard).

---

## 10. Phasing

| Phase | Contents                                                                                                                                                                                                                                                                                                               | Depends on |
| ----- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| 0     | `Config.oci` (+`signer`) + validate + d.ts(×2) + **d.ts sync test** + tests (incl. the drop case, goja-nil-spread)                                                                                                                                                                                                     | —          |
| 1     | `store seed` core: image-spec types + authedGet (+token cache) + PullManifest/PullBlob + httpretry extraction + §2.12 annotations + re-verify + seed marker + CLI; **extraction of binary/JVM subtrees only** (position-independent, trivial relocation); runtime-app layers are skipped → fall-through to the network | 0          |
| 1b    | sigstore-go verify (`oci.signer`, verify-if-present) — separate because of the dependency weight                                                                                                                                                                                                                       | 1          |
| 1c    | **runtime-app layer relocation** (v3.6, the riskiest chunk isolated): symlinks + `pyvenv.cfg`/shebangs + pnpm hardlinks (§2.7)                                                                                                                                                                                         | 1          |
| 2     | auto-seed (the 3 points of §3, demand-driven) + `DATAMITSU_OFFLINE`/`NO_OCI`/`LIBC` (+ Effective+checklist) + offline wiring (incl. `store seed`) + `store status` + `store import`                                                                                                                                    | 1          |
| 3     | linux bundle on top of the existing buildx pipeline: `devtools dockerfile --emit-oci-map` + CI post-process (regctl: layer/manifest annotations §2.12/§2.13, the bundle index with libc descriptors, attestation-manifest filtering) + cosign sign (CLI)                                                               | 1          |
| 4     | darwin/windows entries: `devtools oci-binary-store --target` (cross-target download, §7/v3.6) + `devtools oci-layer` (deterministic tar, materialization of external hardlinks) + oras/crane push + adding to the bundle index; darwin/arm64 runtime — on macos-14/15 runners                                          | 3, 1c      |
| 5     | provenance/SBOM: `buildx --provenance --sbom` + `cosign attest` for oras entries (community toolchain, no datamitsu code)                                                                                                                                                                                              | 3          |
| 6     | Windows runtime-app layers — research spike (exe shims, junctions; §2.14), may not pay off                                                                                                                                                                                                                             | 4          |
| 7     | proxy/registry env + httpx migration of npm/pypi/github (also closes §6 offline hermeticity)                                                                                                                                                                                                                           | —          |
| —     | **source-mode** — a separate doc                                                                                                                                                                                                                                                                                       | —          |

---

## 11. Decisions and remaining questions

**Closed (baked into v2):**

- **Build** — ~~fully native pull/push/build~~ **revised in v3.4 (§7, §11.11): only pull is
  native**; build/push — the community toolchain (buildx/regctl/oras/cosign).
- **libc** — ~~per-libc ref/digest~~ **revised in v3.2**: one index, a libc descriptor
  annotation (§2.5, §11.9); `DATAMITSU_LIBC` — yes.
- **uv-venv — the relocating extractor** (§2.7).
- **offline — a standalone full "no-network" flag, orthogonal to OCI**; no auto-seed under
  offline; the store is seeded separately (§6).
- **Trust for runtime directories — digest chain + lockfile + signature** (refined in v3:
  verify-if-`signer`, §2 Security / §11.3).
- **`DATAMITSU_OFFLINE` and `DATAMITSU_NO_OCI` — both in `runtimeconfig.Effective`**
  (Introspectable, §6).

**Closed in v3:**

1. **Extractor caps** — parameterized options of the relocating extractor, plain constants
   (NOT env, NOT runtimeconfig): compressed blob ≤ 2 GiB, file ≤ 2 GiB (a Node binary is
   > 100 MiB; large CPython .so files make 500 MiB a tight fit), unpacked layer ≤ 8 GiB.
   > Per-subtree layers keep real sizes far below; the caps are zip-bomb protection only. The
   > existing `ExtractArchiveToDir` caps are untouched.
2. **No os/arch match in the index** — online: warn + fall-through to the network (the bundle is
   an accelerator, not a gate; symmetric to the stale-seed §13); offline: fatal with the list of
   the index's platforms.
3. **Signature** — sigstore keyless (Fulcio/Rekor, OIDC), the sigstore-go library, phase 1b. The
   consumer pins the signer in `oci.signer{identity, issuer}` (cert identity = workflow ref,
   issuer = OIDC). `signer` is optional: set → verification mandatory, failure = fatal; not set →
   bytes are guaranteed by the digest chain (§2.8). **Confirmed by the owner 2026-06-10** (the
   softening of the v2 "DECIDED: pull verifies the signature" accepted).
4. **QEMU — not used.** Per-platform CI runners + manifest-only bundle-index assembly with
   community CLIs (regctl, §7).
5. **Dependencies** — `opencontainers/image-spec` (types-only) + `go-digest`; NO
   go-containerregistry/ORAS; sigstore-go isolated in phase 1b.
6. **No blob cache** — temp→extract→delete; idempotence via subtree stat + the seed marker in
   the store (`store clear` cleans it for free) (§2 Security, §5).
7. **Execution cache** — `oci` stays in the invalidation key; the spurious invalidation on a
   digest bump is accepted deliberately (§4).
8. **Demand-driven pull (v3.1)** — auto-seed downloads only the layers of the current
   operation's tools (+their runtime dependencies), matching `com.datamitsu.subtree` against
   locally computed store paths; a full pull only via an explicit `store seed` (airgap). The
   marker — full pulls only; partial-pull idempotence — the subtree stat; the manifest is cached
   on disk by digest (immutable) (§3, §5).
9. **Libc in the index (v3.2, user decision)** — one ref+digest per bundle; (os, arch, libc) is
   selected inside the verified index: os/arch via `platform`, libc via the
   `com.datamitsu.libc` annotation (§2.5). Replaces the per-libc ref/digest from v2. The cost:
   generic tools (docker pull) won't disambiguate such an index — an accepted non-goal.
   `LibcUnknown` without a `DATAMITSU_LIBC` override → warn + fall-through, no guessing.
10. **OS matrix (v3.3, user decision)** — linux×2arches×2libc + darwin/arm64 fully;
    darwin/amd64 and windows/amd64 — binary(+JVM)-only entries built from a single linux host;
    Windows runtime-app layers — the deferred research spike (exe shims with embedded paths,
    junctions) (§2.14). Also closed the relocation hole: the relocating extractor rewrites the
    store-root prefix not only in symlinks but also in `pyvenv.cfg` + `venv/bin/*` shebangs
    (§2.7.a2) — otherwise a uv venv broke on a different store root even on linux.
11. **Producer = community toolchain (v3.4, user decision; CANCELS native build/push):** linux
    layers — the existing buildx pipeline (per-subtree `COPY --link` already yields the needed
    layers, render.go:327; per-app verification at layout time inside the build);
    annotations/bundle-index — CI post-process (regctl/crane) driven by the generator's
    `map.json`; signing — cosign CLI; darwin/windows — `devtools oci-layer` + oras/crane. Native
    Go code — only pull/verify/extract (+the mini tar helper). §2.12/§2.13 are the format
    contract: a native producer can be added later without touching pull. sigstore-go remains
    only for VERIFY at pull (phase 1b) — the consumer has no cosign.
12. **The v3.6 pass:** a layer without a subtree annotation = a base layer of the runnable
    image → skip, not fatal (§2.12); cross-target download for binary-only entries — via
    `devtools oci-binary-store --target` (binmanager is host-bound, §1/§7); external hardlinks
    are materialized at packing time, `.pnpm-store`/build caches are not bundled (§7, §12.16 —
    check the go bloat); the bundle index is tagged against GHCR GC (§12.15); Phase 1 split into
    the binary/JVM core + the 1c relocation; `DATAMITSU_LIBC` — the full introspectable
    checklist (§6).

**Remaining to decide: nothing — all decisions confirmed by the owner (the last one, §11.3,
2026-06-10).**

---

## 12. Risks

1. **libc miss** (a blocker without §2.5): a glibc bundle on a musl host, or fragile detection →
   the seed never hits. Mitigation (v3.2) — the libc descriptor annotation in a single index +
   `DATAMITSU_LIBC` + libc in introspection; `LibcUnknown` → an honest fall-through, no guessing.
2. **Content relocation** (uv/pnpm/go): the generic extractor drops absolute symlinks → the
   relocating extractor is mandatory; otherwise uv airgap doesn't work.
3. **Trust-by-digest without re-verify** (a blocker): closed by re-hashing binaries/JARs against
   the config SHA-256; runtime directories — digest + signature.
4. **Extractor caps 2 GiB/500 MiB**: closed by per-subtree layers + parameterization.
5. **Symlink escape guard** (a blocker): the per-subtree allowlist + the cross-app overwrite
   ban; the legit venv→`.uv/python` goes through the relocating extractor, not a store-wide dest.
6. **Multi-arch for runtime apps** — cross-building from a single host is impossible without
   emulation; solved via per-platform CI runners + the regctl merge of the bundle index (§7,
   phases 3-4); binary-only entries build cross-platform right away; the config holds a single
   image index (os/arch/libc, §2.5).
7. **GHCR specifics:** the 307 blob redirect to the CDN (net/http strips Authorization itself);
   referrers maturity (the fallback tag); the GHCR UI reaction to a non-image config mediaType
   §2.13 — check on a test repo in Phase 4; the fallback is artifactType+empty-config with the
   metadata moved into manifest annotations.
8. **Config↔bundle drift:** a stale digest → a partial seed; online tops up, offline — a clear
   failure (§13).
9. **sigstore-go weight** (new, v3): a heavy dependency tree → isolated in phase 1b; before
   merging, measure the binary size delta and build time; if unacceptable — consider a build tag.
10. **Tar-writer determinism** (narrowed in v3.4): the buildx path does NOT need deterministic
    layers (a rebuild produces new digests → a new bundle digest, which is a bump anyway);
    mandatory only for `devtools oci-layer` (§7) — the "two runs → one digest" test, otherwise
    darwin/windows entries get re-uploaded for nothing.
11. **Windows — partially in scope (revised in v3.3, §2.14):** Windows binary/JVM layers are
    built from a linux host and pulled (zip extraction exists, `.bin/*` has no symlinks);
    Windows runtime-app layers are deferred — pip/uv exe shims with the embedded absolute path
    are not text-relocatable, pnpm junctions, symlinks require developer mode. A Windows host:
    binaries/JARs from the bundle, runtime apps via the network. Document the boundary in the
    user docs.
12. **Textual venv relocation** (new, v3.3): `pyvenv.cfg`/shebangs carry absolute paths —
    without rewriting (§2.7.a2) a uv venv is broken on a different store root. Overreach risk:
    we rewrite ONLY whitelisted files by the builder's exact prefix (from the §2.13 annotation),
    binary files are never touched; test — the venv works after a pull onto a non-standard
    store root.
13. **buildx layer-map ordering** (new, v3.4): buildx omits empty layers, adds attestation
    manifests (`unknown/unknown`) to the index, the base image carries its own layers → the
    map.json↔manifest correspondence can drift. Mitigation: a count/path check in the
    post-process script + filtering attestation descriptors + the main safety net — the
    extractor's write-allowlist validates content against the declared subtree (a drifted
    mapping = a loud failure at pull, not a hole); an e2e test pulling a real built image.
14. **Trust in buildx layer content** (new, v3.4): a native build would verify the bytes
    itself; the buildx path relies on verification INSIDE the docker build (per-app installs
    with the published SHA-256) + post-download re-verification of binaries/JARs at pull
    (§2 Security) + the optional cosign signature (the CI builder's identity). For runtime
    directories the boundary is the same as in v3: digest chain + lockfile + signature.
15. **GHCR cleanup wipes the bundle** (new, v3.6): an untagged bundle index and its child
    manifests are victims of "delete untagged versions" actions (a known multi-arch footgun).
    Mitigation: the index is tagged `:bundle-<version>` (§7); the maintainer docs warn to use
    cleanup tools that respect index references; the consumer symptom — a manifest 404 with a
    pinned digest → a clear "bundle GC'd, re-publish or bump digest" error.
16. **Go app layer bloat** (new, v3.6): GOPATH/GOMODCACHE inside the app subtree → a layer can
    weigh hundreds of MiB of caches useless at runtime (only `bin/` is needed). Check on a real
    config; the solution — pruning at packing time (buildx: in the Dockerfile generator; oras:
    in `oci-layer`) or accepting the size deliberately.

---

## 13. Failure UX (explicit messages/codes)

- **manifest body digest mismatch / blob digest|size mismatch** → fatal, a dedicated exit code,
  the message `expected <d> got <d>`, no retry.
- **offline + miss** → fatal: `tool X not in seed and DATAMITSU_OFFLINE set; remove offline or
extend bundle digest <d>`.
- **stale/partial seed online** → warn + fall-through to the network, listing the uncovered
  tools.
- **no os/arch in the index** → online: warn + fall-through; offline: fatal with the list of the
  index's platforms (§11.2).
- **signature failed with `oci.signer` set** → fatal, no fall-through (this is an attack, not a
  degradation).
- **binary re-verification failed** → fatal: seed content was swapped relative to the published
  SHA-256.

---

## 14. Verification (end-to-end)

1. `go build && go test ./...` + the managed
   `golangci-lint --max-issues-per-linter=0 --max-same-issues=0`.
2. Phase 0: `oci` parsing; a malformed digest→error; chaining (the last wins; spread inherits;
   **non-spread drops**; `undefined`/`null` reset); `Marshal(Config{})` byte-identical.
3. Phase 1: build a test store image (an httptest fake registry for unit tests; for e2e — a real
   push to a ghcr test repo, per-subtree + §2.12 annotations, glibc+musl entries in one index
   §2.5); `store seed` without docker → binary/JVM layers land in `{base}/store`; **layers
   WITHOUT an annotation (base rootfs) are skipped without error; runtime-app layers before 1c
   are skipped → fall-through to the network**; manifest/blob mismatch→fatal; repeat = no-op via
   the marker (zero network requests); layer skip on a warm subtree; re-verification catches a
   swap; cross-app overwrite/a malformed annotation are forbidden.
   Phase 1c: the venv symlink survives; pnpm hardlinks survive.
4. Phase 1b: a valid signature → ok; an identity/issuer mismatch → fatal; `signer` not set →
   verification skipped, a warn in `store status`.
5. Phase 2: auto-seed before install/init/run (all 3 points of §3); `DATAMITSU_OFFLINE=1`+miss
   without a seed→hard fail; with a seed→exit 0, no network for the tools;
   `config runtime | jq '.offline,.libc'`; `store status` shows coverage.
6. Phase 3: an e2e of the whole pipeline — an existing buildx image → the post-process script
   (annotations from map.json, the bundle index, attestation filtering; a repeated run is
   idempotent) → `store seed` onto a clean store root → uv/go/pnpm apps work; the
   map.json↔manifest count/path check (§12.13). Phase 4: `devtools oci-layer` is deterministic
   (two runs → one digest, §12.10); a darwin entry is built on a macos runner and pulled.
7. Relocation: a pull onto a different store root → uv/go work (the relocating extractor, incl.
   the rewritten `pyvenv.cfg`/shebangs — python actually starts and finds its stdlib) OR a
   documented failure with the `/dm` pin; a dangling venv symlink (sabotage) → `uvVenvHealthy`
   catches it → rebuild (the safety net).
8. OS matrix (§2.14): a binary-only entry for windows/amd64 and darwin/amd64 is built on linux
   CI and pulled on the corresponding host (e2e at least in a GH matrix); a Windows host: a
   binary from the bundle works, a runtime app honestly goes via the network with a clear
   message.
9. Without `oci` — behavior and cache keys are unchanged (`Marshal(Config{})` byte-identical).
