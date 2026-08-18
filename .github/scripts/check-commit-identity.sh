#!/usr/bin/env bash
# Reject commits attributed to a reserved test identity.
#
# internal/clitest gives every throwaway fixture repo a fixed identity
# (datamitsu-clitest <clitest@datamitsu.invalid>) so `git init` fixtures need no
# real author. That identity is for temp directories only. When it ends up in
# this repository's own .git/config — usually because someone set a local
# user.name/user.email to commit non-interactively — every commit on the branch
# is authored by it, and GitHub's squash merge then copies it onto main as a
# `Co-authored-by:` trailer. The result is permanent, world-readable history
# crediting an account that does not exist.
#
# The .invalid TLD is reserved by RFC 2606 and can never be a real address, so
# matching on it is exact: a hit is always a misconfiguration, never a
# contributor.
set -euo pipefail

BASE="${1:-${BASE_SHA:-}}"
HEAD="${2:-${HEAD_SHA:-HEAD}}"

if [[ -z "$BASE" ]]; then
  echo "error: no base revision given (pass as \$1 or set BASE_SHA)" >&2
  exit 1
fi

RANGE="${BASE}..${HEAD}"

# One record per commit: author email, committer email, and every
# Co-authored-by value. %x00 separates records so a multi-line trailer block
# cannot be mistaken for the next commit.
offenders=$(
  git log --no-merges --format='%H%x09%ae%x09%ce%x09%(trailers:key=Co-authored-by,valueonly,separator=%x2C)%x00' "$RANGE" |
    tr '\0' '\n' |
    grep -F 'datamitsu.invalid' || true
)

if [[ -n "$offenders" ]]; then
  echo "error: commits in ${RANGE} are attributed to the reserved test identity" >&2
  echo >&2
  printf '%s\n' "$offenders" >&2
  echo >&2
  echo "This identity belongs to internal/clitest fixture repositories only." >&2
  echo "It usually means this repository has a local override:" >&2
  echo >&2
  echo "  git config --local --get-regexp 'user\\.'" >&2
  echo >&2
  echo "Remove it so your global identity applies, then rewrite the commits:" >&2
  echo >&2
  echo "  git config --local --unset user.name" >&2
  echo "  git config --local --unset user.email" >&2
  echo "  git config --local --unset commit.gpgSign   # if signing was disabled too" >&2
  echo "  git rebase --reset-author-date --exec 'git commit --amend --no-edit --reset-author' ${BASE}" >&2
  exit 1
fi

echo "commit identity ok: no reserved test identity in ${RANGE}"
