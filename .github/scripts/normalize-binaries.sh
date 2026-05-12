#!/usr/bin/env bash
set -euo pipefail

# Normalize binaries from GoReleaser output
# Usage: normalize-binaries.sh <is_unstable>
#
# GoReleaser creates: dist/datamitsu_{version}_{os}_{arch}_v{goamd}/datamitsu
# This script creates: dist/binaries/datamitsu-{os}_{arch}[.exe]
# Used for packaging (npm, PyPI, RubyGems, Docker, etc.)

IS_UNSTABLE="${1:-false}"

echo "📦 Normalizing binaries (unstable=$IS_UNSTABLE)"
mkdir -p dist/binaries

# Extract binaries from GoReleaser directories
for dir in dist/datamitsu_*_*/; do
  [[ -d "$dir" ]] || continue

  binary=$(find "$dir" -maxdepth 1 -type f \( -name "datamitsu" -o -name "datamitsu.exe" \) | head -1)
  [[ -f "$binary" ]] || continue

  dirname=$(basename "$dir")

  # Strip prefix and microarch: datamitsu_1.0.0_linux_amd64_v1 → 1.0.0_linux_amd64
  target=$(echo "$dirname" | sed 's/^datamitsu_//' | sed 's/_v[0-9.]*$//')

  # Remove version: 1.0.0_linux_amd64 → linux_amd64
  if [[ "$IS_UNSTABLE" != "true" ]]; then
    # shellcheck disable=SC2001  # sed is required for regex pattern matching
    target=$(echo "$target" | sed 's/^[0-9][^_]*_//')
  fi

  # Add file extension
  if [[ "$binary" == *.exe ]]; then
    newname="datamitsu-${target}.exe"
  else
    newname="datamitsu-${target}"
  fi

  cp -p "$binary" "dist/binaries/$newname"
  echo "✓ $binary → dist/binaries/$newname"
done

echo ""
echo "📋 Normalized binaries:"
ls -lh dist/binaries/

verify_permissions() {
  echo ""
  echo "🔍 Verifying executable permissions..."

  local FAILED=0

  for binary in dist/binaries/datamitsu*; do
    [[ -f "$binary" ]] || continue

    # Skip Windows executables (permission checks don't apply)
    if [[ "$binary" == *.exe ]]; then
      echo "⊙ Skipped: $binary (Windows binary)"
      continue
    fi

    if [[ ! -x "$binary" ]]; then
      echo "❌ Not executable: $binary"
      FAILED=1
    else
      echo "✓ Executable: $binary"
    fi
  done

  if [[ $FAILED -eq 1 ]]; then
    echo "❌ Some binaries are not executable"
    return 1
  fi

  echo "✓ All binaries have correct permissions"
  return 0
}

# Call verification
verify_permissions
