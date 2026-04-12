#!/bin/bash
# Creates a test inline archive for embedding in datamitsu config.
# Usage: scripts/create-test-archive.sh [directory]
#   directory: path to directory to archive (default: creates example files in /tmp)
# Output: "tar.br:..." string ready to paste into config

set -euo pipefail

if [ $# -ge 1 ]; then
  src_dir="$1"
  if [ ! -d "$src_dir" ]; then
    echo "Error: $src_dir is not a directory" >&2
    exit 1
  fi
  tmp_tar=$(mktemp /tmp/archive-XXXXXX.tar)
  trap 'rm -f "$tmp_tar" "${tmp_tar}.br"' EXIT
  tar -cf "$tmp_tar" -C "$src_dir" .
else
  tmp_dir=$(mktemp -d /tmp/test-archive-XXXXXX)
  tmp_tar=$(mktemp /tmp/archive-XXXXXX.tar)
  trap 'rm -rf "$tmp_dir" "$tmp_tar" "${tmp_tar}.br"' EXIT

  mkdir -p "$tmp_dir/config"
  echo "key: value" > "$tmp_dir/config/base.yml"
  echo "# Example config" > "$tmp_dir/README.md"

  tar -cf "$tmp_tar" -C "$tmp_dir" .
fi

if ! command -v brotli &> /dev/null; then
  echo "Error: brotli command not found. Install with: apt install brotli" >&2
  exit 1
fi

brotli -q 11 -o "${tmp_tar}.br" "$tmp_tar"
echo "tar.br:$(base64 -w0 "${tmp_tar}.br")"
