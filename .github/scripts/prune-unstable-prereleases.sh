#!/usr/bin/env bash
# cspell:ignore argjson
# Preview only: downstream URL-and-hash pins can still need these release assets.
set -euo pipefail

KEEP="${KEEP:-5}"

if ! [[ "$KEEP" =~ ^(0|[1-9][0-9]{0,8})$ ]]; then
  echo "error: KEEP must be a canonical decimal integer from 0 to 999999999" >&2
  exit 1
fi

CANDIDATES=$(gh api --paginate --slurp "repos/${GITHUB_REPOSITORY}/releases?per_page=100" | jq -r --argjson keep "$KEEP" '
  add // []
  | map(select(.prerelease and (.draft | not) and (.tag_name | startswith("unstable-"))))
  | sort_by(.published_at, .id) | reverse | .[$keep:] | .[].tag_name
')

if [[ -z "$CANDIDATES" ]]; then
  echo "No published unstable prereleases are beyond the retention count."
  exit 0
fi
printf 'Cleanup candidates (preview only):\n%s\n' "$CANDIDATES"
