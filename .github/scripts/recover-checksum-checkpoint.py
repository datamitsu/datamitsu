import hashlib
import io
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from zipfile import ZipFile


def restore(destination, coordinates, index_id, index_digest):
    match = re.fullmatch(
        r"([1-9][0-9]*) ([1-9][0-9]*) (sha256:[0-9a-f]{64}) ([0-9a-f]{40})", coordinates
    )
    if match is None:
        raise ValueError("invalid checksum checkpoint coordinates")
    run_id, artifact_id, digest, signer = match.groups()
    if signer != os.environ["GITHUB_WORKFLOW_SHA"]:
        raise ValueError(
            "incomplete finalization requires the original signer revision"
        )
    api = f"repos/{os.environ['GITHUB_REPOSITORY']}/actions"
    run = json.loads(subprocess.check_output(["gh", "api", f"{api}/runs/{run_id}"]))
    expected_path, expected_ref = os.environ["GITHUB_WORKFLOW_REF"].split("@", 1)
    expected_path = expected_path.removeprefix(os.environ["GITHUB_REPOSITORY"] + "/")
    if (
        run["path"].split("@", 1)[0] != expected_path
        or run["head_sha"] != signer
        or expected_ref != f"refs/heads/{run['head_branch']}"
    ):
        raise ValueError("checksum checkpoint did not come from the approved finalizer")
    pages = json.loads(
        subprocess.check_output(
            [
                "gh",
                "api",
                "--paginate",
                "--slurp",
                f"{api}/runs/{run_id}/artifacts?per_page=100",
            ]
        )
    )
    matches = [
        item
        for page in pages
        for item in page["artifacts"]
        if str(item["id"]) == artifact_id
    ]
    if len(matches) != 1:
        raise ValueError("missing or ambiguous checksum checkpoint")
    item = matches[0]
    if (
        item["expired"] is not False
        or item["digest"] != digest
        or not re.fullmatch(
            rf"checksum-checkpoint-{re.escape(index_id)}-[1-9][0-9]*", item["name"]
        )
        or not 0 < item["size_in_bytes"] <= 131072
    ):
        raise ValueError("invalid or expired checksum checkpoint")
    with subprocess.Popen(
        ["gh", "api", f"{api}/artifacts/{artifact_id}/zip"], stdout=subprocess.PIPE
    ) as proc:
        if proc.stdout is None:
            raise RuntimeError("missing checkpoint download stream")
        raw = proc.stdout.read(131073)
        if len(raw) > 131072:
            proc.kill()
            raise ValueError("checkpoint ZIP exceeds limit")
        if proc.wait() != 0:
            raise ValueError("cannot download checksum checkpoint")
    if digest != "sha256:" + hashlib.sha256(raw).hexdigest():
        raise ValueError("checksum checkpoint ZIP digest mismatch")
    with ZipFile(io.BytesIO(raw)) as archive:
        entries = archive.infolist()
        names = {"checksums.txt.sigstore.json", "checksum-finalization.txt"}
        if (
            len(entries) != 2
            or {entry.filename for entry in entries} != names
            or any(not 0 < entry.file_size <= 65536 for entry in entries)
        ):
            raise ValueError("unexpected checksum checkpoint ZIP contents")
        bundle = archive.read("checksums.txt.sigstore.json")
        record = archive.read("checksum-finalization.txt")
    expected = (
        f"datamitsu-checksum-finalization/1\nindex_id={index_id}\nindex_digest={index_digest}\n"
        f"bundle_digest=sha256:{hashlib.sha256(bundle).hexdigest()}\n"
    ).encode()
    if record != expected:
        raise ValueError(
            "checksum checkpoint does not identify the approved index and bundle"
        )
    for name, data in (
        ("checksums.txt.sigstore.json", bundle),
        ("checksum-finalization.txt", record),
    ):
        with (destination / name).open("xb") as output:
            output.write(data)


if __name__ == "__main__":
    restore(Path(sys.argv[1]), *sys.argv[2:])
