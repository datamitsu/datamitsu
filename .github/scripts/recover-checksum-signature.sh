#!/usr/bin/env bash
set -euo pipefail

mode=$1
checksums=$2
bundle=$3
index_id=$4
index_digest=$5

if ! [[ "$index_id" =~ ^[1-9][0-9]*$ && "$index_digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo 'Invalid preparation index identity' >&2
  exit 1
fi

record() {
  local digest
  digest=$(sha256sum < "$bundle") || return
  printf 'datamitsu-checksum-finalization/1\nindex_id=%s\nindex_digest=%s\nbundle_digest=sha256:%s\n' \
    "$index_id" "$index_digest" "${digest%% *}"
}

verify() (
  signer_sha=$1
  [[ "$signer_sha" =~ ^[0-9a-f]{40}$ ]]
  evidence=$(mktemp)
  trap 'rm -f "$evidence"' EXIT
  record > "$evidence"
  policy=(--certificate-identity "$GITHUB_SERVER_URL/$GITHUB_WORKFLOW_REF"
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
    --certificate-github-workflow-sha "$signer_sha")
  cosign verify-blob --bundle "$bundle.index.sigstore.json" "${policy[@]}" "$evidence" >&2
  cosign verify-blob --bundle "$bundle" "${policy[@]}" "$checksums" >&2
)

case "$mode" in
  record)
    record
    exit
    ;;
  verify)
    verify "$6"
    exit
    ;;
  recover) ;;
  *)
    echo 'Expected record, verify, or recover' >&2
    exit 1
    ;;
esac
if ! [[ "$RECOVERY" =~ ^([1-9][0-9]*)\ ([1-9][0-9]*)\ (sha256:[0-9a-f]{64})\ (sha256:[0-9a-f]{64})\ ([0-9a-f]{40})$ ]]; then
  echo 'Invalid checksum signature recovery coordinates' >&2
  exit 1
fi
run_id=${BASH_REMATCH[1]} artifact_id=${BASH_REMATCH[2]} archive_digest=${BASH_REMATCH[3]} expected=${BASH_REMATCH[4]} signer_sha=${BASH_REMATCH[5]}
api="repos/$GITHUB_REPOSITORY/actions"
gh api --paginate "$api/runs/$run_id/artifacts?per_page=100" --slurp | jq -e --arg id "$artifact_id" --arg digest "$archive_digest" '
  [.[].artifacts[] | select((.id | tostring) == $id and .expired == false and .digest == $digest
    and (.name | test("^prerelease-checksum-signature-[1-9][0-9]*$")))] | length == 1
' > /dev/null
gh api "$api/artifacts/$artifact_id/zip" | python3 -c '
import hashlib, io, pathlib, sys, zipfile
data = sys.stdin.buffer.read(131073)
if len(data) > 131072:
  sys.exit("Checksum signature archive exceeds 128 KiB")
if "sha256:" + hashlib.sha256(data).hexdigest() != sys.argv[1]:
  sys.exit("Checksum signature ZIP digest mismatch")
with zipfile.ZipFile(io.BytesIO(data)) as archive:
  entries = archive.infolist()
  names = {"checksums.txt.sigstore.json", "checksums.txt.sigstore.json.index.sigstore.json"}
  if len(entries) != 2 or {entry.filename for entry in entries} != names or any(
    not 0 < entry.file_size <= 65536 for entry in entries
  ):
    sys.exit("Unexpected checksum signature archive: expected checksum and index bundles")
  bundle = archive.read("checksums.txt.sigstore.json")
  if "sha256:" + hashlib.sha256(bundle).hexdigest() != sys.argv[2]:
    sys.exit("Checksum signature bundle digest mismatch")
  pathlib.Path(sys.argv[3]).write_bytes(bundle)
  pathlib.Path(sys.argv[3] + ".index.sigstore.json").write_bytes(
    archive.read("checksums.txt.sigstore.json.index.sigstore.json")
  )
' "$archive_digest" "$expected" "$bundle"
verify "$signer_sha"
printf '%s\n' "$signer_sha"
