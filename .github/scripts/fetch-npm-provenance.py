import hashlib
import io
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from zipfile import ZipFile


def fetch(directory, output, run_id, producing_attempt, index_id):
    if any(
        not re.fullmatch(r"[1-9][0-9]*", value)
        for value in (run_id, producing_attempt, index_id)
    ):
        raise ValueError("invalid provenance run, attempt, or index ID")
    api = "repos/datamitsu/datamitsu/actions"
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
    output.mkdir(mode=0o700)
    archives = sorted(directory.glob("*.tgz"))
    if len(archives) != 9:
        raise ValueError("expected nine npm archives")
    signing_attempts = {}
    for archive in archives:
        name = f"{archive.stem}.sigstore.json"
        pattern = re.compile(
            rf"npm-provenance-{index_id}-([1-9][0-9]*)-{re.escape(archive.stem)}"
        )
        matches = [
            (artifact, match[1])
            for page in pages
            for artifact in page["artifacts"]
            if (match := pattern.fullmatch(artifact["name"])) is not None
        ]
        if len(matches) != 1:
            raise ValueError(
                f"missing or ambiguous npm provenance for {archive.stem} on index {index_id}"
            )
        artifact, signing_attempt = matches[0]
        label = artifact["name"]
        if int(signing_attempt) < int(producing_attempt):
            raise ValueError(f"npm provenance predates the index for {archive.stem}")
        digest = artifact.get("digest", "")
        if (
            artifact.get("expired") is not False
            or type(artifact.get("id")) is not int
            or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest)
            or not 0 < artifact.get("size_in_bytes", 0) <= 131072
        ):
            raise ValueError(f"invalid or expired npm provenance: {label}")
        with subprocess.Popen(
            ["gh", "api", f"{api}/artifacts/{artifact['id']}/zip"],
            stdout=subprocess.PIPE,
        ) as proc:
            if proc.stdout is None:
                raise RuntimeError("missing artifact download stream")
            raw = proc.stdout.read(131073)
            if len(raw) > 131072:
                proc.kill()
                raise ValueError(f"provenance ZIP exceeds limit: {label}")
            if proc.wait() != 0:
                raise ValueError(f"cannot download provenance: {label}")
        if "sha256:" + hashlib.sha256(raw).hexdigest() != digest:
            raise ValueError(f"provenance ZIP digest mismatch: {label}")
        with ZipFile(io.BytesIO(raw)) as bundle:
            entries = bundle.infolist()
            if (
                len(entries) != 1
                or entries[0].filename != name
                or not 0 < entries[0].file_size <= 65536
            ):
                raise ValueError(f"unexpected npm provenance ZIP: {label}")
            (output / name).write_bytes(bundle.read(entries[0]))
        signing_attempts[archive.name] = signing_attempt
    (output / "signing-attempts.json").write_text(
        json.dumps(signing_attempts, sort_keys=True)
    )


if __name__ == "__main__":
    if os.environ.get("GITHUB_REPOSITORY") != "datamitsu/datamitsu":
        sys.exit("unexpected repository for npm provenance")
    fetch(Path(sys.argv[1]), Path(sys.argv[2]), *sys.argv[3:])
