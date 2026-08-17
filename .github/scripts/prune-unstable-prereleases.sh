#!/usr/bin/env bash
# Delete all but the newest KEEP unstable prereleases, newest first.
#
# Unstable builds are dispatched by hand for downstream testing and are never
# consumed by version resolution, so they accumulate as clutter on the releases
# page and bury the stable ones. Nothing resolves them by tag, so deleting old
# ones breaks no consumer.
#
# Selection requires BOTH that the release is marked prerelease AND that its tag
# starts with "unstable-". Either check alone is unsafe: a stable release
# candidate (v1.2.0-rc.1) is also a prerelease, and a tag prefix alone would
# match a full release that happened to use the name. Anything that is not both
# is left strictly alone — this script must never be able to delete a stable
# release.
set -euo pipefail

KEEP="${KEEP:-5}"
DRY_RUN="${DRY_RUN:-false}"

if ! [[ "$KEEP" =~ ^[0-9]+$ ]]; then
  echo "error: KEEP must be a non-negative integer, got ${KEEP@Q}" >&2
  exit 1
fi

# --limit is well above any plausible backlog; the default of 30 would silently
# hide older releases from the prune and let them live forever.
ALL=$(gh release list --limit 500 --json tagName,isPrerelease,createdAt)

CANDIDATES=$(printf '%s' "$ALL" | jq -r '
  map(select(.isPrerelease and (.tagName | startswith("unstable-"))))
  | sort_by(.createdAt) | reverse
  | .[].tagName
')

TOTAL=$(printf '%s' "$CANDIDATES" | grep -c . || true)
echo "unstable prereleases: ${TOTAL}, keeping the newest ${KEEP}"

if [[ "$TOTAL" -le "$KEEP" ]]; then
  echo "nothing to prune"
  exit 0
fi

DOOMED=$(printf '%s\n' "$CANDIDATES" | tail -n +$((KEEP + 1)))

while IFS= read -r tag; do
  [[ -n "$tag" ]] || continue
  if [[ "$DRY_RUN" == "true" ]]; then
    echo "would delete ${tag}"
    continue
  fi
  echo "deleting ${tag}"
  # The tag is removed separately rather than with --cleanup-tag so a repository
  # with protected or immutable tags still gets the release cleared: the release
  # is the clutter, the dangling tag is a lesser one, and failing the whole run
  # over it would leave the actual problem in place.
  gh release delete "$tag" --yes --repo "$GITHUB_REPOSITORY"
  if ! gh api -X DELETE "repos/${GITHUB_REPOSITORY}/git/refs/tags/${tag}" > /dev/null 2>&1; then
    echo "::notice title=Tag retained::deleted the ${tag} release but could not delete its tag; it is likely protected or immutable."
  fi
done <<< "$DOOMED"

echo "✓ pruned $(printf '%s\n' "$DOOMED" | grep -c .) unstable prerelease(s)"
