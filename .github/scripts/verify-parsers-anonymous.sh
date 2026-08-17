#!/usr/bin/env bash
# Prove the just-published parser artifact can be pulled with NO credentials.
#
# A GHCR package created by a workflow is private by default and is not
# auto-linked to its repository, so a brand-new package 401s for every user —
# and users have no GHCR credential of their own (datamitsu deliberately never
# sends them one). That failure would otherwise surface as "diagnostics look
# worse than usual" for every consumer, long after the release went green.
#
# The check must be genuinely credential-free. Unsetting GITHUB_TOKEN is not
# enough on its own: any earlier docker/login-action writes credentials into
# ~/.docker/config.json, and any tool that reads a credential store would then
# succeed against a private package and report a false pass. Hence bare curl
# with no Authorization header and no credential store in play.
set -euo pipefail

REGISTRY="${PARSERS_REGISTRY:-ghcr.io}"
REPO="${PARSERS_REPO:?PARSERS_REPO is required (e.g. datamitsu/datamitsu-parsers)}"
DIGEST="${PARSERS_DIGEST:?PARSERS_DIGEST is required (sha256:<64 hex>)}"
SHA256="${PARSERS_SHA256:?PARSERS_SHA256 is required (the module sha256, bare hex)}"

# GHCR issues an anonymous pull token for public packages; for a private one the
# token request succeeds but the pull that follows does not, which is exactly
# the failure this step exists to catch.
TOKEN=$(curl -fsSL "https://${REGISTRY}/token?scope=repository:${REPO}:pull&service=${REGISTRY}" |
  sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [[ -z "$TOKEN" ]]; then
  echo "error: ${REGISTRY} issued no anonymous pull token for ${REPO}" >&2
  exit 1
fi

echo "→ anonymous manifest pull of ${REPO}@${DIGEST}"
if ! curl -fsSL -o /dev/null \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Accept: application/vnd.oci.image.manifest.v1+json" \
  "https://${REGISTRY}/v2/${REPO}/manifests/${DIGEST}"; then
  echo "error: the artifact cannot be pulled anonymously — the ${REPO} package is almost certainly still PRIVATE." >&2
  echo "       A workflow-created GHCR package defaults to private; an org owner has to make it public once." >&2
  exit 1
fi

echo "→ anonymous blob pull, verifying it hashes to ${SHA256}"
ACTUAL=$(curl -fsSL \
  -H "Authorization: Bearer ${TOKEN}" \
  "https://${REGISTRY}/v2/${REPO}/blobs/sha256:${SHA256}" | sha256sum | awk '{print $1}')
if [[ "$ACTUAL" != "$SHA256" ]]; then
  echo "error: the anonymously pulled blob hashes ${ACTUAL}, expected ${SHA256}" >&2
  exit 1
fi

echo "✓ ${REGISTRY}/${REPO}@${DIGEST} pulls anonymously and its layer is the published module"
