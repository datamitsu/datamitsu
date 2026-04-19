#!/usr/bin/env bash
set -euo pipefail

# Prepare release assets from GoReleaser output
# Usage: prepare-release-assets.sh <is_unstable>
#
# This script normalizes GoReleaser output structure:
# - Stable:   dist/datamitsu_{version}_{os}_{arch}/   → dist/release/datamitsu-{os}_{arch}[.exe]
# - Unstable: dist/datamitsu_{os}_{arch}_v{goamd}/    → dist/release/datamitsu-{os}_{arch}[.exe]

IS_UNSTABLE="${1:-false}"

echo "📦 Preparing release assets (unstable=$IS_UNSTABLE)"
mkdir -p dist/release

# Copy archives (.tar.gz and .zip)
if [[ "$IS_UNSTABLE" == "true" ]]; then
  # Unstable: datamitsu_*-SNAPSHOT-*.tar.gz → datamitsu_*_{os}_{arch}.tar.gz
  for archive in dist/datamitsu_*-SNAPSHOT-*.tar.gz dist/datamitsu_*-SNAPSHOT-*.zip; do
    [[ -f "$archive" ]] || continue
    filename=$(basename "$archive")
    # shellcheck disable=SC2001  # sed is required for regex pattern matching
    newname=$(echo "$filename" | sed 's/-SNAPSHOT-[a-f0-9]*_/_/')
    cp "$archive" "dist/release/$newname"
    echo "✓ Archive: $archive → dist/release/$newname"
  done
else
  # Stable: already clean filenames
  for archive in dist/datamitsu_*.tar.gz dist/datamitsu_*.zip; do
    [[ -f "$archive" ]] || continue
    cp "$archive" dist/release/
    echo "✓ Archive: $archive"
  done
fi

# Copy raw binaries from GoReleaser directories
# Parse directory names to extract os/arch, handling both stable and unstable formats
for dir in dist/datamitsu_*_*/; do
  [[ -d "$dir" ]] || continue

  binary=$(find "$dir" -maxdepth 1 -type f \( -name "datamitsu" -o -name "datamitsu.exe" \) | head -1)
  [[ -f "$binary" ]] || continue

  dirname=$(basename "$dir")

  # Strip "datamitsu_" prefix and microarchitecture suffix (_v1, _v2, etc.)
  # Unstable: datamitsu_linux_amd64_v1  → linux_amd64
  # Stable:   datamitsu_1.0.0_linux_amd64 → 1.0.0_linux_amd64
  target=$(echo "$dirname" | sed 's/^datamitsu_//' | sed 's/_v[0-9.]*$//')

  # For stable releases, remove version prefix: 1.0.0_linux_amd64 → linux_amd64
  if [[ "$IS_UNSTABLE" != "true" ]]; then
    # shellcheck disable=SC2001  # sed is required for regex pattern matching
    target=$(echo "$target" | sed 's/^[0-9][^_]*_//')
  fi

  # Determine file extension
  if [[ "$binary" == *.exe ]]; then
    newname="datamitsu-${target}.exe"
  else
    newname="datamitsu-${target}"
  fi

  cp -p "$binary" "dist/release/$newname"
  echo "✓ Binary: $binary → dist/release/$newname"
done

echo ""
echo "📋 Release assets prepared:"
ls -lh dist/release/
