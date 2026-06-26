#!/usr/bin/env bash
# Assert that the WASM parser module's SHA-256 is recorded in goreleaser's
# checksums.txt — the file the existing cosign `sign-blob` step signs. If the
# WASM were missing from checksums.txt it would ship unsigned and unverifiable
# by the core, so fail the release build loudly rather than publish it silently.
set -euo pipefail

CHECKSUMS="${1:-dist/checksums.txt}"
# The asset is versioned by goreleaser (datamitsu_parsers_<version>.wasm), so match
# by pattern rather than a fixed name — accepts both versioned and plain. An
# explicit second arg overrides with a literal/regex name.
WASM_PATTERN="${2:-datamitsu_parsers(_[^[:space:]]+)?\.wasm}"

if [[ ! -f "$CHECKSUMS" ]]; then
  echo "error: $CHECKSUMS not found (did goreleaser run?)" >&2
  exit 1
fi

# goreleaser sha256 lines are "<hash>  <filename>"; match the filename at EOL.
if ! grep -qE "[[:space:]]${WASM_PATTERN}\$" "$CHECKSUMS"; then
  echo "error: no WASM parser module (${WASM_PATTERN}) listed in ${CHECKSUMS}" >&2
  echo "--- ${CHECKSUMS} ---" >&2
  cat "$CHECKSUMS" >&2
  exit 1
fi

echo "✓ WASM parser module is listed in ${CHECKSUMS}:"
grep -E "[[:space:]]${WASM_PATTERN}\$" "$CHECKSUMS"
