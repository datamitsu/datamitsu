#!/usr/bin/env bash
# Build the VS Code extension .vsix inside GoReleaser's sign phase (see the
# vsix entry under `signs` in .goreleaser.yml). The sign phase runs after the
# checksum step — dist/checksums.txt exists, so codegen can bake each binary
# archive's SHA-256 — and before the release step, so GoReleaser uploads the
# .vsix inside its own draft->publish flow. The release is therefore complete
# the moment it goes live, which the homebrew/scoop/winget manifests
# (published by GoReleaser afterwards) depend on: their download URLs must
# resolve as soon as the registries' CIs validate them.
set -euo pipefail

CHECKSUMS="$1" # goreleaser ${artifact}: dist/checksums.txt
OUTPUT="$2"    # goreleaser ${signature}: dist/datamitsu-<version>.vsix
VERSION="$3"   # goreleaser {{ .Version }}: no leading v; may carry -rc./-unstable suffixes

# vsce rejects prerelease suffixes in package.json versions, so the manifest is
# pinned to the base version; rc/snapshot builds keep the full version in the
# .vsix filename via OUTPUT. codegen matches archive filenames in checksums.txt
# by the FULL version.
BASE_VERSION="${VERSION%%-*}"

# pnpm --filter runs with CWD=editors/vscode, so both paths must be absolute.
export VERSION
CHECKSUMS_FILE="$(pwd)/$CHECKSUMS"
export CHECKSUMS_FILE

pnpm --filter "./editors/vscode" codegen
(cd editors/vscode && npm pkg set version="$BASE_VERSION")
pnpm --filter "./editors/vscode" build
# --allow-package-all-secrets/--allow-package-env-file skip vsce's built-in
# secret scanner: under pnpm's isolated node_modules it cannot resolve its own
# @secretlint/* rule modules and crashes on load. The .vsix ships only the
# bundled extension.cjs + static metadata, and the repo already scans secrets
# via `datamitsu check` (secretlint + gitleaks).
pnpm --filter "./editors/vscode" exec vsce package \
  --allow-package-all-secrets --allow-package-env-file \
  --no-dependencies -o "$(pwd)/$OUTPUT"
