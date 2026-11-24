import hashlib
import json
import re
import sys
import time
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import HTTPRedirectHandler, build_opener

# cspell:ignore newurl


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("unexpected PyPI redirect")


def reconcile(directory, version, fetch):
    wheels = {}
    for path in directory.glob("*.whl"):
        with path.open("rb") as file:
            wheels[path.name] = hashlib.file_digest(file, "sha256").hexdigest()
    if len(wheels) != 6:
        raise ValueError("expected six validated wheels")
    try:
        response = fetch(f"https://pypi.org/pypi/datamitsu/{version}/json", timeout=20)
    except HTTPError as error:
        if error.code == 404:
            return sorted(wheels)
        raise
    with response:
        record = json.load(response)
    if record["info"]["name"] != "datamitsu" or record["info"]["version"] != version:
        raise ValueError("PyPI returned a different release")
    if not isinstance(record.get("urls"), list):
        raise TypeError("invalid PyPI artifact inventory")
    seen = set()
    for asset in record["urls"]:
        name = asset["filename"]
        if (
            name in seen
            or name not in wheels
            or asset.get("yanked") is not False
            or asset["digests"]["sha256"] != wheels[name]
        ):
            raise ValueError(f"conflicting PyPI artifact: {name!r}")
        seen.add(name)
    return sorted(wheels.keys() - seen)


if __name__ == "__main__":
    directory, version = Path(sys.argv[1]), sys.argv[2].replace("-rc.", "rc", 1)
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:rc[0-9]+)?", version):
        sys.exit("invalid tagged PyPI version")
    fetch = build_opener(NoRedirect()).open
    if sys.argv[3:] == ["--require-complete"]:
        for attempt in range(5):
            if not reconcile(directory, version, fetch):
                break
            if attempt != 4:
                time.sleep(3)
        else:
            sys.exit("PyPI has not confirmed the complete approved wheel set")
    elif not sys.argv[3:]:
        for name in reconcile(directory, version, fetch):
            print(name)
    else:
        sys.exit("expected no option or --require-complete")
