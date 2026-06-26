#!/usr/bin/env bash
# Assert that the WASM parser module's SHA-256 is recorded in goreleaser's
# checksums.txt — the file the existing cosign `sign-blob` step signs. If the
# WASM were missing from checksums.txt it would ship unsigned and unverifiable
# by the core, so fail the release build loudly rather than publish it silently.
set -euo pipefail

CHECKSUMS="${1:-dist/checksums.txt}"
WASM_NAME="${2:-datamitsu_parsers.wasm}"

if [[ ! -f "$CHECKSUMS" ]]; then
  echo "error: $CHECKSUMS not found (did goreleaser run?)" >&2
  exit 1
fi

# goreleaser sha256 lines are "<hash>  <filename>"; match the filename at EOL.
if ! grep -qE "[[:space:]]${WASM_NAME}\$" "$CHECKSUMS"; then
  echo "error: ${WASM_NAME} not listed in ${CHECKSUMS}" >&2
  echo "--- ${CHECKSUMS} ---" >&2
  cat "$CHECKSUMS" >&2
  exit 1
fi

echo "✓ ${WASM_NAME} is listed in ${CHECKSUMS}:"
grep -E "[[:space:]]${WASM_NAME}\$" "$CHECKSUMS"
