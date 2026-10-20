# Plan: unified digest representation — `alg:value` everywhere a hash faces a person

**Status:** decisions closed by the owner on 2026-10-09 after an independent high-effort review
(23 findings; all accepted findings folded in — see §7).
**Date:** 2026-10-09.
**Related:** `internal/hashutil` (replaced), `internal/binmanager`, `internal/runtimemanager`,
`internal/config`, `internal/configcache`, `internal/cache`, `internal/releaseprovider`,
`internal/remotecfg`, `internal/ocidigest`, `internal/ocibundle`, `internal/ociartifact`,
`internal/parsermanager`, `internal/appstate`, `internal/report`, `internal/sourcefarm`,
`internal/llmsmanifest`, `internal/verifycache`, `internal/tooling`, `internal/clitest`,
`packaging/llms-harvest`, `cmd` (devtools), `config/` (d.ts, committed JSON manifests),
`parsers/embedded.lock`, `.github/scripts`, website docs, AGENTS.md.

---

## 0. Why this exists

The repository uses two hash families, correctly split by policy — XXH3-128 for internal keys
and SHA-256 for everything that touches the internet — but represents them eight different
ways:

| Surface today                                                                                          | Form                                                                                                                                                          |
| ------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| config pins (`binaries.<os>.<arch>.<libc>.hash`, `jvm.jarHash`, `archives[].hash`, `parsers.<n>.hash`) | bare 64-hex, prefix **rejected** (`internal/config/validate.go:705`)                                                                                          |
| `oci.digest` (config + parser)                                                                         | `sha256:<64-hex>` **required** (validate.go:526,637)                                                                                                          |
| `getRemoteConfigs().hash`                                                                              | bare or `sha256:`-prefixed, lowercase only (remotecfg/fetch.go:72)                                                                                            |
| `expectChainHash`                                                                                      | canonical `xxh3:<32-hex>`, bare accepted, hex lowercased but prefix matched case-sensitively; **no syntax validation at load** (managed_config_verify.go:100) |
| `binaryApps.json`/`runtimes.json` generated `hash` fields                                              | bare 64-hex (devtools strips the `sha256:` the API gave it — devtools.go:847)                                                                                 |
| `binaryApps.json` hand pin maps (`hashes`, `checksums`)                                                | bare or prefixed, uppercase tolerated (releaseprovider/integrity.go:18)                                                                                       |
| internal XXH3 keys (toolstate, configcache, staleness, verify, manifests)                              | bare 32-hex                                                                                                                                                   |
| `parsers/embedded.lock` L2                                                                             | `sha256:<hex>` (the target format, already)                                                                                                                   |

Eight independent validators implement "is this a sha256 string" with disagreeing rules
(validate.go:705, remotecfg/fetch.go:72, releaseprovider/integrity.go:18,
ocidigest/pull.go:44, ociartifact/parsers.go:219/231/241, ocibundle/source.go:91/174,
cmd/devtools.go:847, cmd/devtools_pull_runtimes.go:1133). The algorithm lives in a separate
field (`hashType`) that the download policy already pins to sha256
(validate.go:448), so the md5/sha1/sha384/sha512 arms of `verifyFileHash`
(binmanager/hash.go:60-97) are unreachable from validated configs — though
`VerifyFileHashPublic` still dispatches on the type and tests exercise the weak arms
(verify.go:158, hash_test.go:446). The same value crosses encodings inside one config:
`ociartifact.SelectWasmLayer` bridges `parsers.<n>.hash` (bare) to an OCI layer digest
(prefixed) by string concatenation (parsers.go:198).

Goal: **one child package owning every hash; one string format `alg:value` for every digest
a person writes, reads, or copies; typed parsing so a bare `string` cannot pose as a
validated hash.**

## 1. Decision registry

| ID  | Decision                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1  | New package `internal/digest` is the single owner of hashing. It wraps XXH3-128 (replacing `internal/hashutil`) and SHA-256. `internal/hashutil` is deleted in the same change; every caller migrates — including `packaging/llms-harvest` (main.go:276), `internal/clitest` (parsers.go:29), `internal/tooling` (verdict.go:58, executor.go:886) and every test fixture builder. The depguard rule in `.golangci.yaml` is retargeted (`zeebo/xxh3` denied outside `internal/digest`).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| D2  | Canonical string: `<alg>:<hex>`, algorithm token lowercase from a closed set, value lowercase hex of exact length (`sha256` → 64, `xxh3` → 32; `xxh3` names XXH3-128, as `expectChainHash` already prints). No aliases, no alternative tokens.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| D3  | A typed value `digest.Digest` (immutable, unexported fields) exists **only at integrity boundaries** — parsing a declared pin, verifying bytes, comparing digests. It is never a field of a struct that crosses msgpack (config-eval cache marshals directly, store.go:194/256), goja (reflective export, config_loader.go:874), or any persisted Go struct: those keep `string` fields holding canonical or bare forms as decided below. No `MarshalJSON` on the type.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| D4  | Strict where datamitsu authors both ends: `datamitsu.config` fields (`binaries…hash`, `jarHash`, `archives[].hash`, `parsers.<n>.hash`, `oci.digest`, `expectChainHash`) accept **only** the canonical form, validated **at load** — `expectChainHash` gains the load-time syntax check it lacks today (validate.go:716 checks scope only). Errors teach the format (`expected sha256:<64 lowercase hex>`). Breaking for pre-change configs — acceptable per Product Stage. A bare 64-hex pin stops loading; that is the point of one form.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| D5  | Lenient where a human copies from a release page or a feed: `binaryApps.json` hand pin maps (`hashes`, `checksums`), `getRemoteConfigs().hash`, and upstream ingestion (GitHub release digests, GitLab `file_sha256`, SHASUMS lines, goreleaser checksums) accept `alg:value` in any case or bare hex whose length selects the algorithm (64 → sha256, 32 → xxh3), normalized to canonical **in memory** at load — the on-disk source bytes stay untouched, and `appstate.Save` writes the normalized values so retained entries migrate on the next pull (a pull can save a manifest whose entries it never regenerated — devtools.go:125/251/283; normalization must not depend on a successful discovery).                                                                                                                                                                                                                                                                                                                                       |
| D6  | Writers emit canonical: `devtools pull-releases` and `devtools pull-runtimes` (`pull-node`/`pull-uv` update versions and descriptions, not digest pins), `appstate` pin maps, error messages printing an expected value, `config chain-hash` (already canonical), committed JSON manifests, goldens. `remotecfg` is two things and stays two things: the file cache is addressed by URL alone and verified by SHA-256 (cache.go:15, resolver.go:27) — its address does not change; the _declared_ hash string that `configcache.Key` folds (key.go:206; production leaves `Inputs.RemoteConfigs` unset today — cmd/config_cache.go:161 — the parent chain bytes already carry the pin) is normalized to canonical where it is read.                                                                                                                                                                                                                                                                                                                 |
| D7  | `hashType` dies. `BinaryOsArchInfo.HashType`, `BinHashType` constants, `IsAllowedDownloadHashType`, the validate.go:448/1256 arms, and the md5/sha1/sha384/sha512 branches of `verifyFileHash` are deleted; `crypto/md5` and `crypto/sha1` imports and their gosec nolints go with them. This removes behavior `VerifyFileHashPublic` still exposed (verify.go:158) — a policy-mandated removal, not dead-code cleanup. `appstate.Load` uses `DisallowUnknownFields` (appstate.go:62): a manifest that still carries `hashType` fails to load after the field is dropped — the committed manifests are regenerated in the same change, and the removal is a documented breaking change (only hand-authored `hashType` ever existed; generated entries never wrote it).                                                                                                                                                                                                                                                                              |
| D8  | Identity inputs and filesystem addresses use the bare value (`Digest.Hex()`), never the canonical string — **no exceptions, no algorithm-qualified new namespaces**: `calculateConfigHash`, `calculateRuntimeHash`, `calculateAppHash`/`calculatePackageAppHash` (whose inputs nest other hashes — runtime, pnpm, lock, files, JAR via `lockHash`, runtimemanager/hash.go:109/138, jvm.go:173), `parsermanager.cacheKey`, `HashFilesAndArchives` inputs, verify-cache fingerprint fields, `configcache.Key` parts (incl. `ChainFile.ContentHash`, which is a key _input_, not payload), `ComputePageSetHash` inputs, and every content-addressed directory or cache filename. Consequences: install identity is byte-stable across the migration (nothing re-downloads, no store re-provisions); no `:` enters a path component (Windows). The **existing** algorithm-qualified layouts stay as they are: OCI image-layout blobs under `blobs/sha256/` and the manifest cache `sha256-<hex>.json` (ocibundle/source.go:44/90) keep their addresses. |
| D9  | Persisted digest _fields_ stay **bare** unless a person reads or writes them: `FileEntry.ContentHash`, `File.InvalidationKey`, `VerdictEntry.InputHash`, verify-cache `Fingerprint`, farm `StalenessKey`, `appstate.configHash`, `configcache` artifact `PayloadHash`, `llmsmanifest.pageSetHash` — all compared only against values the same code computed; churning them buys nothing (and `appstate.configHash` staying bare keeps the `currentHash[:8]` display at devtools.go:253 byte-identical). Canonical goes only to person-facing committed values: `expectChainHash` pins, `fallback.wasm.sources` (prints its algorithm), generated pin fields in `binaryApps.json`/`runtimes.json` and `apps.ts` (`jarHash`), e2e/shell fixture pins, d.ts examples, docs.                                                                                                                                                                                                                                                                            |
| D10 | Version bumps: `cache.cacheSemantics` `d8v1` → `d9v1` (config shape changed — hashType gone, hash strings canonical); `configcache.FormatVersion` 7 → 8. **No `sourcefarm.ManifestFormatVersion` bump**: the manifest persists resolved commands, env and paths — not binary config fields (plan.go:122) — and a bump would disable `UsableStale` offline fallback for every existing farm (manifest.go:500/530); nothing the farm records changes shape. Verify-cache fingerprints miss once through changed inputs (url/hash strings, hashType removal) — no `extractionCheck` bump. `llmsmanifest` manifest.json is regenerated with canonical page hashes (schema stays 1).                                                                                                                                                                                                                                                                                                                                                                     |
| D11 | Exceptions that keep their own format, deliberately: `report.Fingerprint` stays bare 64-hex — an opaque external identity pinned by `FingerprintVersion` (`dmfp1`) and golden vectors (fingerprint.go:37, fingerprint_test.go:18); baselines, SARIF uploads and `report/diff` must not churn. Likewise `LineHash`, `RowHash` (`row:<n>`), the TeamCity truncated-name suffix (teamcity.go:157 — the external service retains that identity), `gitutil` snapshot sentinels, composite in-memory keys (`<tool>@<hex>`, `<name>+env:<hex>`, `path\x1fhash` tuples, `link:<hex>`), OCI `godigest` strings at the protocol boundary. Package-manager lock internals (`pnpm-lock.yaml` integrity lines, uv `RECORD`, `go.sum`) are external formats datamitsu never interprets — untouched.                                                                                                                                                                                                                                                               |
| D12 | `oci.digest` fields and `parsers/embedded.lock` are already canonical; their bespoke validators are replaced by the shared parser. `ociartifact.SelectWasmLayer` compares the parsed parser-hash digest with the layer digest directly — the `"sha256:"+hash` pivot disappears. `embedded.lock` has no Go validator to migrate (its consumer is the Taskfile shell check, Taskfile.yaml:47); that container-pin contract stays as is.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| D13 | The JS config layer stays validation-free (Go validates); `config.d.ts` + embedded copy document every hash field as `sha256:<64 hex>` / `xxh3:<32 hex>` with examples. No new JS helper, no Goja-visible change.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| D14 | AGENTS.md keeps the XXH3-internal / SHA-256-external split (it is correct) and is rewritten to name `internal/digest` and the canonical form; the Security Policy section gains the format. `.golangci.yaml` depguard denies `zeebo/xxh3` outside `internal/digest` and `crypto/sha256` outside `internal/digest` (the report fingerprint and TeamCity suffix move onto `digest` helpers, so no exceptions remain).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| D15 | One-time migrations inside this repo: committed `config/src/binaryApps.json`, `runtimes.json`, `apps.ts` (`jarHash`), e2e/shell fixtures gain prefixes (values unchanged); `internal/config/config.js` regenerated (`task build:lib`); `fallback.wasm.sources` regenerated as `xxh3:<hex>` via the sourcehash command; `test/cli` goldens with `-update`; `.github/scripts/push-parsers-oci.mjs` prints canonical config pins and compares digests without concatenation, **paired** with `.github/scripts/verify-parsers-anonymous.sh`, which today expects bare hex and prepends `sha256:` itself (line 20) — both sides of the publication contract move together.                                                                                                                                                                                                                                                                                                                                                                               |
| D16 | Scope guard: no new algorithms (sha512 etc.), no length-prefixed `Multi` re-keying, no package-manager lock handling changes, no inspector changes (it shows no hashes — manifest.go:25), no farm/store layout changes (D8). The XXH3/SHA-256 policy split itself is not up for revision.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |

## 2. The package

`internal/digest` — small, closed, no generic framework:

```go
// Parsing at boundaries (each names the algorithm it expects — a wrong
// algorithm is an error, not a type-state):
func ParseSHA256(s string) (Digest, error) // canonical "sha256:<64 lc hex>", strict
func ParseXXH3(s string) (Digest, error)   // canonical "xxh3:<32 lc hex>", strict
func ParseSHA256Loose(s string) (Digest, error) // any case; optional prefix; bare 64-hex accepted
func ParseXXH3Loose(s string) (Digest, error)   // any case; optional prefix; bare 32-hex accepted

// Computing:
func SHA256Of(data []byte) Digest
func XXH3Of(data []byte) Digest
func XXH3Multi(parts ...[]byte) Digest   // NUL-joined; byte-identical to hashutil.XXH3Multi
func SHA256Reader(r io.Reader) ([]byte, error) // raw sum — for tee/counting streaming verify
func XXH3Reader(r io.Reader) (Digest, error)
func SHA256File(path string) (Digest, error)

type Digest struct{ alg algorithm; value string } // value: lowercase hex, fixed length
func (d Digest) String() string // "alg:value" — pins, display, person-facing persistence
func (d Digest) Hex() string    // bare value — hash inputs, cache/store addresses (D8)
func (d Digest) Algorithm() string
func (d Digest) VerifyFile(path string) error // streams, constant-time value compare
```

No exported `Algorithm` type, no `HashBytes(alg, …)`, no generic registry: an unsupported
algorithm cannot be expressed, and the compiler, not a switch default, enforces the set.
Tests: parse matrix (strict/loose × case × length × wrong-alg prefix × unknown token),
golden vectors for both algorithms, `XXH3Multi` byte-compatibility goldens frozen from
`hashutil` output, `VerifyFile` hit/miss, `Hex()`/`String()` invariants, path-safety of
`Hex()`.

## 3. Surface-by-surface migration

| Surface                                                                                                                                                                                                                                | Change                                                                                                                                                                                                                                              |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/config/validate.go`                                                                                                                                                                                                          | pin fields via `digest.ParseSHA256` (canonical-only, D4); `oci.digest` via shared parser; **new** `expectChainHash` syntax check at load; delete `hashType` arms (D7)                                                                               |
| `internal/config/managed_config_verify.go`                                                                                                                                                                                             | `chainHashPrefix` dies; `ChainHashes` prints `digest.String()`; `VerifyChainHashes` strict-canonical (D4) — removes the case-sensitive prefix strip at :100                                                                                         |
| `internal/binmanager` (hash.go, download.go, verify.go, binmanager.go:719, hashtype.go)                                                                                                                                                | `verifyFileHash` → `digest.VerifyFile` (SHA-256 only, D7); identity hashes keep `Hex()` inputs (D8); `hashtype.go` shrinks to content types; `DownloadAndVerifySHA256`/`VerifyFileHashPublic` take `Digest`                                         |
| `internal/runtimemanager` (hash.go, jvm.go)                                                                                                                                                                                            | identity hashes on `Hex()` (D8); JAR streaming verify keeps its MultiWriter/stall-guard/cap/retry shape and compares `digest.SHA256Of`'s bytes — no second pass (D8)                                                                                |
| `internal/releaseprovider/integrity.go`                                                                                                                                                                                                | `SHA256()` validator → `digest.ParseSHA256Loose`; `Asset.Digest` canonical; checksum-asset self-pin via shared verify; `ValidateHashPins` behavior unchanged                                                                                        |
| `cmd/devtools.go`, `devtools_pull_runtimes.go`                                                                                                                                                                                         | `extractHashFromDigest`/`isSHA256Hex`/SHASUMS parsing → `digest.ParseSHA256Loose`; generated `hash` fields written canonical (D6); `currentHash[:8]` display byte-identical (D9)                                                                    |
| `internal/remotecfg`                                                                                                                                                                                                                   | one `ParseSHA256Loose` at the boundary replaces both hand-rolled prefix strips (fetch.go:73/92); URL-addressed file cache untouched (D6)                                                                                                            |
| `internal/ocidigest`, `internal/ocibundle`, `internal/ociartifact`                                                                                                                                                                     | local validators → `digest.ParseSHA256`; digest pivot in `SelectWasmLayer` becomes digest equality; blob/manifest streaming keeps counters and `httpretry.Permanent`/`errDigestMismatch` semantics; `sha256-<hex>.json` cache layout preserved (D8) |
| `internal/parsermanager`                                                                                                                                                                                                               | `cacheKey` over `Hex()`; stored-module re-verify via `digest.VerifyFile`; `parsers.<n>.hash` canonical                                                                                                                                              |
| `internal/appstate`                                                                                                                                                                                                                    | pins normalized at load and written canonical by `Save` (D5) — retained entries included; `configHash` bare (D9); `hashType` field dropped (D7)                                                                                                     |
| `internal/configcache`, `internal/cache`, `internal/sourcefarm`, `internal/verifycache`, `internal/llmsmanifest`, `internal/gitutil`, `internal/lsp`, `internal/env`, `internal/tooling`, `internal/clitest`, `packaging/llms-harvest` | mechanical migration to `digest.XXH3Of`/`XXH3Multi`/`XXH3Reader`; persisted identity fields bare (D9); path components `Hex()` (D8); version bumps per D10                                                                                          |
| `internal/report`                                                                                                                                                                                                                      | fingerprint construction moves onto `digest` helpers; output byte-identical (D11); golden vectors unchanged                                                                                                                                         |
| `internal/parsermanager/embedded/sourcehash`                                                                                                                                                                                           | writes `xxh3:<hex>`; `embedded_test` compares canonical                                                                                                                                                                                             |
| committed manifests + fixtures                                                                                                                                                                                                         | per D15                                                                                                                                                                                                                                             |
| `config/config.d.ts` + embedded copy, website docs, guides, AGENTS.md, `.golangci.yaml`                                                                                                                                                | per D13/D14 and §5                                                                                                                                                                                                                                  |

## 4. Compatibility (what breaks, once)

- Configs/config-authored pins in bare form fail load with a format-teaching error (D4).
- A JS config carrying a leftover `hashType` key no longer errors: the field is gone from the
  Go struct, so goja's reflective export drops it silently (amendment to D7 — the promised
  removal error applies only to `binaryApps.json`, whose loader rejects unknown fields;
  a stale `hashType` beside a canonical pin is inert, never an integrity bypass).
- A `binaryApps.json` still carrying `hashType` fails to load (DisallowUnknownFields) —
  regenerate with the updated devtools in the same change (D7).
- Config-eval cache and toolstate go cold once (D10); `verify-all` re-verifies once (its
  fingerprints fold the pin string, which changed spelling — a deliberate one-time miss);
  `pull-releases` re-runs discovery once. Farms do **not** rebake (no format bump, D10), the
  store does not move, nothing re-downloads (D8).
- Baselines, SARIF fingerprints, history: unchanged (D11). `store refs` output changes where
  it prints a declared pin — a person-facing value, canonical by D6.
- The wrapper config package (`@shibanet0/datamitsu-config`) re-spells its pins on its next
  release — author-guide "Recent changes" entry, not this repo. Until then a repository whose
  chain pulls the published package fails load on its bare pins; that is the clean break (D4).

## 5. Documentation

- AGENTS.md: Hashing Policy rewritten around `internal/digest` + the canonical form and the
  bare-for-identity rule (D8/D9); Security Policy gains the format; Release Providers and
  Output Parsers sections updated where they name pins or `fallback.wasm.sources`.
- `config/src/prompts/datamitsu-config-author-guide.md`: hash fields documented canonical;
  entry under "Recent changes to review" naming the first release carrying it.
- Website: `configuration-api.md` (every hash field + examples), `binary-management.md`
  ("plain SHA-256 hex strings" → canonical), `supply-chain-security.md`,
  `use-remote-configs.md`, `core-concepts.md`, `managed-configs.md`.
- `parsers/README.md`: no change expected (embedded.lock already canonical).

## 6. Implementation order

1. `internal/digest` + tests (hashutil still present; both green).
2. Mechanical XXH3-only caller migration (~45 sites incl. harvester, clitest, tooling),
   delete `internal/hashutil`, retarget depguard. Byte streams unchanged (D8) — no behavior
   shift; `go test ./...` green.
3. Config validation: canonical pins, `expectChainHash` load check, delete `hashType`/weak
   verify arms, JAR/OCI streaming verify through `digest` (semantics preserved), lenient
   ingestion (remotecfg/releaseprovider/devtools), OCI validator swap, `SelectWasmLayer`
   digest equality. Bumps per D10.
4. Person-facing canonical fields (D9): generated manifests, sourcehash file, chain-hash
   strictness, appstate normalization-on-save.
5. d.ts/docs/AGENTS/.golangci/push-parsers-oci.mjs + verify-parsers-anonymous.sh (paired);
   `task build:lib`; regenerate `fallback.wasm.sources`.
6. `go build`, `go vet`, `go test ./...`; `test/cli` goldens `-update`.

## 7. Review provenance and resolved findings

The plan was reviewed independently (Codex, high reasoning effort, read-only, 2026-10-09):
23 findings. Accepted and folded in: algorithm-typed parse gates at every boundary (was: a
generic `Parse`/`ParseLoose` that would have admitted XXH3 where SHA-256 is required);
msgpack/goja codec boundary (D3); round-trip test blind to `ManagedConfigRender.Hash`
(added to §8); transitive identity stability tests (§8); **no farm format bump** (D10 was
wrong — manifest holds resolved commands, not config fields, and a bump would break
`UsableStale` offline fallback); remotecfg cache-vs-key split (D6); bare-for-key-inputs
classification of `ChainFile.ContentHash` and page hashes (D8/D9); the omitted persisted
fields now decided individually (D9); inventory gaps (harvester, clitest, verdicts,
`currentHash[:8]` display) added to D1/D9; retained-entry normalization in `appstate.Save`
(D5); `hashType` × `DisallowUnknownFields` migration note (D7); paired publication-script
migration (D15); streaming-verify semantics preserved (D3 API: raw `SHA256Reader` bytes);
`expectChainHash` load-time validation (D4); command-name corrections (D6); `embedded.lock`
contract untouched (D12); preserved `sha256-<hex>.json` layout (D8); narrowed "dead code"
claim (D7); smaller closed API (§2). Rejected: accepting legacy bare pins in
`datamitsu.config` (D4 stays strict — alpha doctrine; lenient only where the source is
external); canonicalizing `report.Fingerprint` (D11 stands).

The implementation was reviewed a second time before commit (Codex `gpt-6.1-sol`,
reasoning effort xhigh, read-only, 2026-10-09): 2 blockers, 5 majors, 4 minors, 6 verified
correct. Fixed: external archive pins folded verbatim in `HashFilesAndArchives` (now `Hex()`
— the one identity the first pass missed); 29 test files still importing `crypto/sha256`
(migrated to `digest.SHA256Of`, so the depguard rule holds with no test exemption);
`parsers-oci.json` + workflow `sha256` output now canonical (the record a wrapper copies
into `parsers.core.hash`); `appstate.Load` no longer panics on null entries; the chain-hash
gate fails closed on an unparsable pin instead of manufacturing a match; `downloadAndVerify`
rejects an unparsable pin before any network activity; spelling-stability vectors added for
binary/archive/parser identities; remotecfg mismatch errors print canonical digests and the
config-eval key folds the remote pin by `Hex()`; a no-op golden test and a stray smoke
artifact removed. Declined as design changes, recorded as amendments: JS-config `hashType`
is silently dropped rather than rejected (§4); verify-cache fingerprints intentionally fold
the canonical spelling (one-time miss, D10).

## 8. Verification

- Unit: digest parse matrix, goldens, `XXH3Multi` compatibility vectors.
- Transitive stability: a golden test pins `calculateConfigHash`, `calculateRuntimeHash`,
  `calculateAppHash` outputs across the migration (fixtures before/after byte-equal) — the
  `task` smoke alone is insufficient because identities nest other hashes.
- Round-trip: a `configcache` Save→Load test asserting rendered managed-config content and
  its hash survive (the JSON-comparison test cannot see `Render`).
- `go test ./...` (unit + offline blackbox + real-shell tier) green with regenerated goldens.
- Smoke: pinned app resolves and `exec`s against a pre-migration store without
  re-downloading (D8 proof); a bare-hash config fails with the teaching error; an
  `expectChainHash` pin round-trips through `config chain-hash`; `pull-releases` on the
  committed `binaryApps.json` normalizes retained entries.
- Windows: no canonical string in any path component (grep audit; CI).
