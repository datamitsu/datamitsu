# Plan: WASM output-parser modules in OCI bundles

**Status:** in progress (core implementation on branch `parsers-in-oci-bundles`)
**Date:** 2026-06-26
**Related:** [2026-06-09-oci-bundles.md](./2026-06-09-oci-bundles.md)

## Problem

The WASM output-parser module ships only as a GitHub Release asset. The core
downloads it by url+hash via `internal/parsermanager` and stores it at
`{store}/.parsers/{module}/{xxh3(url,hash)}/module.wasm`. The OCI bundle system —
which seeds tool binaries / runtimes / runtime-apps into the store as OCI layers
for airgapped, zero-network deployments — knows nothing about parsers. So in an
OCI-bundle deployment a tool that has an `outputParser` still reaches out to the
internet for its parser module, defeating the airgap.

This adds the parser module to the OCI-bundle pipeline so it ships as its own
layer, gets seeded into the store, is re-verified against its published SHA-256,
and is then found on disk by the existing `parsermanager` resolution.

## Key facts that shape the design

- **No runtime-resolution change.** `parsermanager.ensureModule` stats the
  content-addressed path before downloading. If the bundle seeds the module to
  exactly `{store}/.parsers/{module}/{xxh3}/module.wasm`, resolution finds it with
  zero code change. The missing capability is _materializing_ the module into the
  store during the Docker build (a builder stage needs a command to fetch it).
- **Deterministic store key.** The parser store key is `xxh3(url, hash)` — an
  internal cache key (correctly XXH3 per the hashing policy; NOT changed to
  SHA-256). Unlike binaries (whose per-config-hash directory is host/arch/libc
  dependent), the parser key is a pure function of config (url+hash), so the
  bundle producer and the runtime compute the same path.
- **Single source for the store path.** `parsermanager` owns the path math; it
  exports a helper so the dockerfile generator and ocibundle don't re-derive it.
- **`Tool` ≠ `App`.** `OutputParser` lives on `config.Tool`, not on the binary
  `App`. Demand-driven seed must collect needed parser modules by walking
  `cfg.Tools[*].OutputParser.Module`, not the app set.

## Design

The parser module flows through the bundle exactly like a downloaded binary,
with one wrinkle: there is no `install <parser>` verb, so a new prefetch command
materializes the module in a builder stage.

1. **`parsermanager`**
   - `Prefetch(ctx, names) error` — download + verify the named modules into the
     store without compiling (empty = all declared). Reuses `ensureModule`.
   - `ModuleStorePath(name, Parser) string` — exported wrapper over `moduleDir`,
     the single source of the `{store}/.parsers/{name}/{xxh3}` path.

2. **CLI:** `datamitsu devtools parsers prefetch [module...]` — loads config,
   builds a `Manager` over `cfg.Parsers`, calls `Prefetch`. No args = every
   declared parser. This is what a parser builder stage runs.

3. **`internal/dockerfile`**
   - `parserSubtree(module)` = `.parsers/{module}` (hash-less parent COPY, mirrors
     `.bin/{app}`; the per-config-hash child is the only entry and the post-process
     resolves it).
   - `PlanOptions.Parsers` (config.MapOfParsers) feeds `Plan.ParserStages
[]ParserStage{Module}` (one per declared module, sorted). Keeps `BuildPlan`'s
     signature stable; only the dockerfile/split-config callers set it.
   - `BuildSlices` emits one slice per parser stage containing only that
     `parsers` entry, so a re-pin busts only that parser's layer.
   - `writeFinalStage` / `writeParserStages` render a `FROM <builderBase> AS
parser-<module>` stage that COPYs its slice and runs the prefetch command,
     then the final stage `COPY .parsers/{module}`. Emitted after binaries.
   - `BuildOCIMap` appends `{subtree: ".parsers/{module}", kind: "parser", app:
module}` per parser stage.

4. **`internal/ocibundle`**
   - Add `.parsers/` to `subtreeRoots` (extract/seed allowlist). Without this the
     extractor rejects parser layers. This alone makes a **full** seed pull them.
   - `expectedSubtrees` adds the hash-level subtree for every parser module
     referenced by any `cfg.Tools[*].OutputParser`, so **demand-driven** seeds
     (`seed --apps X`) also cover parsers. (Simplification: any referenced parser
     is included whenever a demand seed runs — parsers are tiny and shared; per-
     tool precision is a possible later refinement. Documented, not silent.)
   - `buildReVerifyIndex` adds parsers: single-file `module.wasm` stored verbatim,
     published SHA-256 = `cfg.Parsers[module].Hash` — the ideal re-verify case.

5. **datamitsu-config (NOT in this PR — handed off)**
   - `scripts/oci-bundle-postprocess.ts`: add `"parser"` to the `kind` union and
     keep parser subtrees on the hash-segment-scanning path (same as `.bin/...`,
     NOT the exact `uv-python` path). The generated Dockerfile already emits the
     parser stages + oci-map entries, so the CI push needs only this annotation
     handling. Per the standing rule, the config repo is changed by the maintainer,
     not autonomously.

## Risks / notes

- **Demand-seed over-pull:** a `seed --apps prettier` (no parser) will still pull
  the shared parser layer. Tiny, acceptable; logged via the expected-subtree owner
  label.
- **Annotation `app`:** parsers have no owning app; the `com.datamitsu.app`
  annotation carries the `parsers` map key (the module name) as the identifier.
- **oci-map is app-level (version 1):** a re-pinned parser regenerates the oci-map
  with the image, so they stay in sync (same as binaries).
- **Hashing policy:** the parser store key stays XXH3 (internal). The published
  SHA-256 (`Parser.Hash`) gates download and re-verify. No crypto/internal mixing.

## Checklist

- [ ] `parsermanager.Prefetch` + `ModuleStorePath` (+ tests)
- [ ] `devtools parsers prefetch` command (+ golden test)
- [ ] dockerfile: `parserSubtree`, `PlanOptions.Parsers`, `Plan.ParserStages`,
      `BuildSlices` parser slices, render parser stages + final COPY, oci-map (+ tests)
- [ ] dockerfile/split-config cmd wiring threads `cfg.Parsers`
- [ ] ocibundle: `.parsers/` subtreeRoot, `expectedSubtrees`, `buildReVerifyIndex` (+ tests)
- [ ] docs: oci-bundles guide mentions parser layers
- [ ] hand-off note for datamitsu-config `oci-bundle-postprocess.ts`
