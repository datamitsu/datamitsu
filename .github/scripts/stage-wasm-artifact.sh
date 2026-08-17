#!/usr/bin/env bash
# Copy the built WASM parser module into dist/ under the name goreleaser
# recorded in checksums.txt.
#
# On a stable release goreleaser uploads the module straight from its build
# directory (release.extra_files), so dist/ never holds it — which also means it
# is absent from the release-artifacts upload every publish job downloads. An
# unstable build goes further: `--snapshot` skips the release pipe entirely, so
# nothing is uploaded anywhere and the optional prerelease job has no module to
# attach.
#
# Staging it here — after goreleaser, since `--clean` wipes dist/ first — fixes
# both. The name comes from checksums.txt rather than being re-templated, so a
# published asset always matches the entry in the file cosign signs.
set -euo pipefail

CHECKSUMS="${1:-dist/checksums.txt}"
SRC="${2:-parsers/target/wasm32-unknown-unknown/release/datamitsu_parsers.wasm}"

if [[ ! -f "$CHECKSUMS" ]]; then
  echo "error: $CHECKSUMS not found (did goreleaser run?)" >&2
  exit 1
fi
if [[ ! -f "$SRC" ]]; then
  echo "error: $SRC not found (did the cargo wasm32 build run?)" >&2
  exit 1
fi

# goreleaser sha256 lines are "<hash>  <filename>"; take the module's filename.
# Exactly one match is required: two entries would mean the release carries two
# modules, and silently picking either would publish an asset whose name does
# not describe the bytes a consumer resolves by that name.
NAMES=$(grep -oE 'datamitsu_parsers[^[:space:]]*\.wasm$' "$CHECKSUMS" || true)
COUNT=$(printf '%s' "$NAMES" | grep -c . || true)
if [[ "$COUNT" -ne 1 ]]; then
  echo "error: expected exactly one WASM parser module in ${CHECKSUMS}, found ${COUNT}" >&2
  [[ -n "$NAMES" ]] && printf '%s\n' "$NAMES" >&2
  exit 1
fi
NAME="$NAMES"

mkdir -p dist
cp -p "$SRC" "dist/${NAME}"

# Re-verify the staged copy against the signed entry: a mismatch means dist/ and
# checksums.txt disagree, and shipping that would hand consumers a hash that
# does not describe the bytes they downloaded.
EXPECTED=$(grep -E "[[:space:]]${NAME}\$" "$CHECKSUMS" | awk '{print $1}')
if command -v sha256sum > /dev/null 2>&1; then
  ACTUAL=$(sha256sum "dist/${NAME}" | awk '{print $1}')
else
  ACTUAL=$(shasum -a 256 "dist/${NAME}" | awk '{print $1}')
fi
if [[ "$EXPECTED" != "$ACTUAL" ]]; then
  echo "error: staged ${NAME} hashes ${ACTUAL}, but ${CHECKSUMS} records ${EXPECTED}" >&2
  exit 1
fi

echo "✓ staged dist/${NAME} (sha256 ${ACTUAL})"
