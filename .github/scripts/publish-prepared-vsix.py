#!/usr/bin/env python3
"""Publish an exact staged stable VSIX to Open VSX or VS Code Marketplace.

The staged archive is inspected but never installed or executed. Recovery
requires downloading the exact registry VSIX and matching its SHA-256.
"""

# cspell:ignore extensionquery gallerycdn readback vsassets vsextensions vspackage

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from pathlib import Path

STABLE = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\Z")
PUBLISHER = "datamitsu"
EXTENSION = "datamitsu-toolkit"
OPEN_VSX_API = "https://open-vsx.org/api"
OPEN_VSX_ORIGIN = "https://open-vsx.org"
MARKETPLACE = "https://marketplace.visualstudio.com/_apis/public/gallery"


def require(ok, message):
    if not ok:
        raise ValueError(message)


def run(args, env):
    return subprocess.run(args, check=True, capture_output=True, text=True, env=env)


def fetch(url, *, headers=None, data=None, missing_ok=False, expected=None):
    request = urllib.request.Request(url, headers=headers or {}, data=data)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            parsed = urllib.parse.urlsplit(response.geturl())
            host = parsed.hostname or ""
            require(
                parsed.scheme == "https"
                and (
                    host in {"open-vsx.org", "marketplace.visualstudio.com"}
                    or (
                        expected is not None
                        and host.endswith(".gallerycdn.vsassets.io")
                    )
                ),
                "registry download redirected outside its approved origin",
            )
            content = response.read((32 << 20) + 1)
            require(
                len(content) <= 32 << 20, "registry response exceeds verification limit"
            )
            if expected is not None:
                require(
                    hashlib.sha256(content).hexdigest() == expected,
                    "registry VSIX does not match the prepared SHA-256",
                )
            return content
    except urllib.error.HTTPError as error:
        if missing_ok and error.code == 404:
            return None
        raise ValueError(f"registry request returned HTTP {error.code}") from error
    except (urllib.error.URLError, TimeoutError) as error:
        raise ValueError(f"registry request failed: {error}") from error


def get_json(url, *, headers=None, data=None, missing_ok=False):
    content = fetch(url, headers=headers, data=data, missing_ok=missing_ok)
    if content is None:
        return None
    try:
        return json.loads(content)
    except json.JSONDecodeError as error:
        raise ValueError(f"registry returned invalid JSON: {error}") from error


def digest_file(path):
    with Path(path).open("rb") as file:
        return hashlib.file_digest(file, "sha256").hexdigest()


def inspect(path, version):
    path = Path(path)
    require(
        path.is_file() and path.stat().st_size > 0, f"missing or empty VSIX: {path}"
    )
    try:
        with zipfile.ZipFile(path) as archive:
            require(archive.testzip() is None, "VSIX contains a corrupt ZIP member")
            require(
                archive.namelist().count("extension/package.json") == 1,
                "VSIX must contain exactly one extension/package.json",
            )
            package = json.loads(archive.read("extension/package.json"))
    except (OSError, zipfile.BadZipFile, json.JSONDecodeError) as error:
        raise ValueError(f"cannot inspect staged VSIX: {error}") from error
    require(
        package.get("publisher") == PUBLISHER and package.get("name") == EXTENSION,
        "VSIX identity does not match editors/vscode/package.json",
    )
    require(
        package.get("version") == version,
        f"VSIX manifest version {package.get('version')!r} does not match approved stable version {version}",
    )
    return digest_file(path)


def open_vsx_bytes(version, expected):
    record = get_json(
        f"{OPEN_VSX_API}/{PUBLISHER}/{EXTENSION}/{version}", missing_ok=True
    )
    if record is None:
        return None
    require(
        isinstance(record, dict) and isinstance(record.get("files"), dict),
        "Open VSX returned invalid version metadata",
    )
    url = record["files"].get("download")
    require(
        isinstance(url, str) and url.startswith(OPEN_VSX_ORIGIN + "/"),
        "Open VSX did not provide a fixed-origin exact VSIX URL",
    )
    content = fetch(url, missing_ok=True, expected=expected)
    if content is None:
        raise ValueError(
            "Open VSX version exists but its exact VSIX download returned 404"
        )
    return content


def marketplace_bytes(version, expected):
    payload = json.dumps(
        {
            "filters": [
                {
                    "criteria": [
                        {"filterType": 7, "value": f"{PUBLISHER}.{EXTENSION}"}
                    ],
                    "pageNumber": 1,
                    "pageSize": 1,
                    "sortBy": 0,
                    "sortOrder": 0,
                }
            ],
            "assetTypes": [],
            "flags": 1,
        }
    ).encode()
    body = get_json(
        f"{MARKETPLACE}/extensionquery",
        headers={
            "Content-Type": "application/json",
            "Accept": "application/json;api-version=7.2-preview.1",
        },
        data=payload,
    )
    require(
        isinstance(body, dict)
        and isinstance(body.get("results"), list)
        and body["results"],
        "Marketplace returned an invalid query response",
    )
    entries = body["results"][0].get("extensions")
    require(isinstance(entries, list), "Marketplace returned no extension inventory")
    matches = [
        item
        for item in entries
        if item.get("publisher", {}).get("publisherName", "").lower() == PUBLISHER
        and item.get("extensionName", "").lower() == EXTENSION
    ]
    require(len(matches) <= 1, "Marketplace returned ambiguous extension records")
    if not matches:
        return None
    inventory = matches[0].get("versions")
    require(
        isinstance(inventory, list) and inventory,
        "Marketplace returned incomplete version metadata",
    )
    versions = [item for item in inventory if item.get("version") == version]
    require(len(versions) <= 1, "Marketplace returned duplicate versions")
    if not versions:
        return None
    return fetch(
        f"{MARKETPLACE}/publishers/{PUBLISHER}/vsextensions/{EXTENSION}/{version}/vspackage",
        headers={"Accept": "application/octet-stream"},
        expected=expected,
    )


def current_bytes(registry, version, expected):
    if not re.fullmatch(r"[0-9a-f]{64}", expected):
        raise ValueError("missing approved VSIX SHA-256")
    return (
        open_vsx_bytes(version, expected)
        if registry == "open-vsx"
        else marketplace_bytes(version, expected)
    )


def assert_equal(registry, content, expected):
    require(content is not None, f"{registry} version is not visible")
    actual = hashlib.sha256(content).hexdigest()
    require(
        actual == expected,
        f"{registry} VSIX bytes differ: staged sha256:{expected}, downloaded sha256:{actual}",
    )


def readback(registry, version, expected):
    last_error = None
    for attempt in range(6):
        try:
            assert_equal(registry, current_bytes(registry, version, expected), expected)
            return
        except ValueError as error:
            last_error = str(error)
        if attempt < 5:
            time.sleep(min(2**attempt, 16))
    raise ValueError(f"{registry} readback failed after bounded retries: {last_error}")


def publish(registry, path, version, run=run):
    require(
        bool(STABLE.fullmatch(version)),
        "approved store version must be stable x.y.z SemVer",
    )
    require(registry in ("open-vsx", "marketplace"), "unsupported registry")
    expected = inspect(path, version)
    current = current_bytes(registry, version, expected)
    if current is not None:
        assert_equal(registry, current, expected)
        return f"{registry} {PUBLISHER}.{EXTENSION}@{version}: existing VSIX matches sha256:{expected}"

    env = os.environ.copy()
    if registry == "open-vsx":
        executable = shutil.which("ovsx")
        require(
            executable,
            "ovsx must be installed by trusted lockfile-backed setup and available on PATH",
        )
        run([executable, "publish", str(Path(path).resolve())], env)
    else:
        executable = shutil.which("vsce")
        require(
            executable,
            "vsce must be installed by trusted lockfile-backed setup and available on PATH",
        )
        # The caller must establish the Azure CLI OIDC session consumed by vsce.
        run(
            [
                executable,
                "publish",
                "--packagePath",
                str(Path(path).resolve()),
                "--azure-credential",
            ],
            env,
        )
    readback(registry, version, expected)
    return f"{registry} {PUBLISHER}.{EXTENSION}@{version}: published and downloaded equal sha256:{expected}"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("registry", choices=("open-vsx", "marketplace"))
    parser.add_argument("path", type=Path, help="exact staged VSIX; no repackaging")
    parser.add_argument("version", help="approved stable version without leading v")
    args = parser.parse_args()
    print(publish(args.registry, args.path, args.version))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, subprocess.CalledProcessError, OSError) as error:
        print(f"VSIX publication failed: {error}", file=sys.stderr)
        sys.exit(1)
