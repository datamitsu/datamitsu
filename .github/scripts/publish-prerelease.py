#!/usr/bin/env python3
"""Publish exact checked GitHub release assets without replacing existing files."""
# cspell:ignore Ftags

import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path

REPOSITORY = "datamitsu/datamitsu"
VERSION_RE = re.compile(
    r"0\.0\.0-(unstable|preview)\.(?:([1-9][0-9]{11})\.)?([1-9][0-9]*)\.([1-9][0-9]*)"
)
TAG_RE = re.compile(
    r"(unstable|preview)-(?:([1-9][0-9]{11})-)?([1-9][0-9]*)-([1-9][0-9]*)"
)
TAGGED_VERSION_RE = re.compile(
    r"(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)){2}(?:-rc\.(?:0|[1-9][0-9]*))?"
)


def require(ok, message):
    if not ok:
        raise ValueError(message)


def command(args, *, check=True):
    return subprocess.run(args, check=check, capture_output=True, text=True)


def release(tag, repository, run=command):
    result = run(
        [
            "gh",
            "api",
            "--paginate",
            "--slurp",
            f"repos/{repository}/releases?per_page=100",
        ]
    )
    matches = [
        entry
        for page in json.loads(result.stdout)
        for entry in page
        if entry["tag_name"] == tag
    ]
    require(len(matches) <= 1, "multiple releases claim the approved tag")
    return matches[0] if matches else None


def asset_plan(directory, version, *, unsigned=False):
    directory = Path(directory)
    archives = [
        f"datamitsu_{version}_{os_name}_{arch}.tar.gz"
        for arch in ("amd64", "arm64")
        for os_name in ("linux", "darwin", "freebsd")
    ]
    archives += [
        f"datamitsu_{version}_windows_{arch}.zip" for arch in ("amd64", "arm64")
    ]
    standalone = {}
    if TAGGED_VERSION_RE.fullmatch(version):
        for arch in ("amd64", "arm64"):
            for platform in ("linux", "darwin", "freebsd", "windows"):
                suffix = ".exe" if platform == "windows" else ""
                name = f"datamitsu_{version}_{platform}_{arch}{suffix}"
                standalone[name] = (
                    directory / "binaries" / f"datamitsu-{platform}_{arch}{suffix}"
                )
        archives += list(standalone)
        archives += [
            f"datamitsu_{version}_{platform}_{arch}.sbom"
            for arch in ("amd64", "arm64")
            for platform in ("linux", "darwin", "freebsd", "windows")
        ]
        archives += [
            f"datamitsu_{version}_linux_{arch}.{extension}"
            for arch in ("amd64", "arm64")
            for extension in ("deb", "rpm", "apk")
        ]
    wasm = f"datamitsu_parsers_{version}.wasm"
    names = archives + ["checksums.txt", wasm, "parsers-oci.json"]
    if TAGGED_VERSION_RE.fullmatch(version):
        names.append(f"datamitsu-{version}.vsix")
    if not unsigned:
        names.append("checksums.txt.sigstore.json")
    require(
        all(name in standalone or (directory / name).is_file() for name in names),
        "dist is missing one or more required release assets",
    )
    assets = {}
    for name in names:
        path = standalone.get(name, directory / name)
        require(
            path.is_file() and path.stat().st_size > 0,
            f"missing or empty asset: {name}",
        )
        with path.open("rb") as source:
            assets[name] = (path, hashlib.file_digest(source, "sha256").hexdigest())
    rows = (directory / "checksums.txt").read_text().splitlines()
    for name in (*archives, wasm):
        entries = [row.split() for row in rows if row.split()[1:] == [name]]
        require(
            entries == [[assets[name][1], name]],
            f"missing, duplicate, or incorrect checksum for {name}",
        )
    return assets


def inventory(directory, version, *, unsigned=False):
    return {
        name: digest
        for name, (_, digest) in asset_plan(
            directory, version, unsigned=unsigned
        ).items()
    }


def upload_asset(repository, release_id, upload_name, source_path, run):
    url = f"https://uploads.github.com/repos/{repository}/releases/{release_id}/assets?name={upload_name}"
    run(
        [
            "gh",
            "api",
            "-X",
            "POST",
            url,
            "-H",
            "Content-Type: application/octet-stream",
            "--input",
            str(source_path),
        ]
    )


def publish(
    directory,
    version,
    tag,
    source_sha,
    repository=None,
    run=command,
    *,
    promote_latest=False,
):
    repository = repository or os.environ.get("GITHUB_REPOSITORY", "")
    require(
        repository == REPOSITORY,
        "refusing publication to an unexpected GitHub repository",
    )
    tagged = TAGGED_VERSION_RE.fullmatch(version) is not None
    if tagged:
        require(tag == f"v{version}", "tag does not match the approved version")
    else:
        vm, tm = VERSION_RE.fullmatch(version), TAG_RE.fullmatch(tag)
        require(
            vm and tm and vm.groups() == tm.groups(),
            "version and tag do not identify the same unstable build",
        )
    prerelease = not tagged or "-rc." in version
    require(
        not promote_latest or not prerelease, "only stable releases may become Latest"
    )
    require(re.fullmatch(r"[0-9a-f]{40}", source_sha) is not None, "invalid source SHA")
    plan = asset_plan(directory, version)
    assets = {name: digest for name, (_, digest) in plan.items()}

    def check_tag(*, required=False):
        if tagged:
            commit = json.loads(
                run(
                    ["gh", "api", f"repos/{repository}/commits/refs%2Ftags%2F{tag}"]
                ).stdout
            )
            require(
                commit.get("sha") == source_sha,
                "Git tag does not point to the approved source commit",
            )
            return
        refs = json.loads(
            run(
                ["gh", "api", f"repos/{repository}/git/matching-refs/tags/{tag}"]
            ).stdout
        )
        exact = [ref["object"] for ref in refs if ref["ref"] == f"refs/tags/{tag}"]
        if not exact and not required:
            return
        require(
            len(exact) == 1
            and exact[0]["type"] == "commit"
            and exact[0]["sha"] == source_sha,
            "Git tag does not point directly to the approved source commit",
        )

    check_tag()
    record = release(tag, repository, run)
    if promote_latest:
        require(
            record is not None and record.get("draft") is False,
            "Latest requires a published release",
        )
    if record is None:
        args = [
            "gh",
            "release",
            "create",
            tag,
            "--draft",
            "--latest=false",
            "--target",
            source_sha,
            "--title",
            tag,
            "--notes",
            "",
            "--repo",
            repository,
        ]
        if tagged:
            args.append("--verify-tag")
        if prerelease:
            args.append("--prerelease")
        run(args)
        record = release(tag, repository, run)
        require(
            record is not None and record.get("draft") is True,
            "created draft not found",
        )
    release_id = record.get("id")
    require(type(release_id) is int and release_id > 0, "invalid release ID")
    require(
        tagged or record.get("target_commitish") == source_sha,
        "release points at a different source commit",
    )
    require(
        record.get("prerelease") is prerelease,
        "target release has the wrong prerelease status",
    )

    def listed_assets():
        observed = {}
        entries = record.get("assets")
        require(isinstance(entries, list), "invalid release asset inventory")
        for entry in entries:
            require(
                isinstance(entry, dict) and isinstance(entry.get("name"), str),
                "invalid GitHub asset entry",
            )
            name = entry["name"]
            require(name not in observed, f"duplicate GitHub asset: {name}")
            if entry.get("state") == "starter":
                raise ValueError(
                    f"incomplete GitHub asset upload for {name}; inspect the draft and remove only the empty starter asset before retrying"
                )
            digest = entry.get("digest")
            require(
                isinstance(digest, str)
                and re.fullmatch(r"sha256:[0-9a-f]{64}", digest),
                f"missing or invalid GitHub SHA-256 for {name}",
            )
            observed[name] = digest.removeprefix("sha256:")
        return observed

    observed = listed_assets()
    require(
        observed.keys() <= assets.keys(), "GitHub release contains unexpected assets"
    )
    for name, digest in observed.items():
        require(digest == assets[name], f"existing GitHub asset differs: {name}")
    if record.get("draft") is False:
        check_tag(required=True)
        require(observed == assets, "published release asset inventory differs")
        if promote_latest:
            latest_url = f"repos/{repository}/releases/latest"
            response = run(["gh", "api", latest_url], check=False)
            if response.returncode != 0:
                require(
                    "HTTP 404" in response.stderr,
                    "could not verify the current Latest release",
                )
            else:
                latest = json.loads(response.stdout)
                latest_tag = latest.get("tag_name", "")
                require(
                    latest_tag.startswith("v")
                    and TAGGED_VERSION_RE.fullmatch(latest_tag[1:])
                    and "-rc." not in latest_tag
                    and latest.get("draft") is False
                    and latest.get("prerelease") is False,
                    "current Latest release is not a recognized stable release",
                )
                if tuple(map(int, latest_tag[1:].split("."))) > tuple(
                    map(int, version.split("."))
                ):
                    return {"tag": tag, "assets": sorted(assets), "latest": latest_tag}
                if latest_tag == tag:
                    require(
                        latest.get("id") == release_id,
                        "Latest points to a different release ID",
                    )
                    return {"tag": tag, "assets": sorted(assets), "latest": tag}
            run(
                [
                    "gh",
                    "api",
                    "-X",
                    "PATCH",
                    f"repos/{repository}/releases/{release_id}",
                    "-f",
                    "make_latest=true",
                ]
            )
            current = json.loads(run(["gh", "api", latest_url]).stdout)
            require(
                current.get("id") == release_id and current.get("tag_name") == tag,
                "could not confirm Latest promotion",
            )
            return {"tag": tag, "assets": sorted(assets), "latest": tag}
        return {"tag": tag, "assets": sorted(assets)}
    require(record.get("draft") is True, "invalid GitHub release draft state")

    endpoint = f"repos/{repository}/releases/{release_id}"

    def read_draft():
        if tagged:
            check_tag(required=True)
        current = json.loads(run(["gh", "api", endpoint]).stdout)
        require(
            current.get("id") == release_id
            and current.get("tag_name") == tag
            and (tagged or current.get("target_commitish") == source_sha)
            and current.get("draft") is True
            and current.get("prerelease") is prerelease,
            "draft identity changed during upload",
        )
        return current

    for name in assets.keys() - observed.keys():
        record = read_draft()
        current = listed_assets()
        require(
            all(assets.get(key) == digest for key, digest in current.items()),
            "draft assets changed during upload",
        )
        if name in current:
            continue
        upload_asset(repository, release_id, name, plan[name][0], run)
    record = read_draft()
    observed = listed_assets()
    require(
        set(observed) == set(assets),
        "GitHub release asset inventory differs after upload",
    )
    for name, digest in assets.items():
        require(observed[name] == digest, f"uploaded GitHub asset differs: {name}")
    check_tag()
    record = json.loads(
        run(
            [
                "gh",
                "api",
                "-X",
                "PATCH",
                endpoint,
                "-F",
                "draft=false",
                "-F",
                f"prerelease={str(prerelease).lower()}",
                "-f",
                "make_latest=false",
            ]
        ).stdout
    )
    require(
        record.get("id") == release_id
        and record.get("draft") is False
        and record.get("prerelease") is prerelease
        and (tagged or record.get("target_commitish") == source_sha)
        and record.get("tag_name") == tag
        and listed_assets() == assets,
        "published release did not match approved inventory",
    )
    check_tag(required=True)
    return {"tag": tag, "assets": sorted(assets)}


if __name__ == "__main__":
    try:
        if len(sys.argv) == 4 and sys.argv[1] == "--check-only":
            require(
                VERSION_RE.fullmatch(sys.argv[3])
                or TAGGED_VERSION_RE.fullmatch(sys.argv[3]),
                "invalid release version",
            )
            inventory(sys.argv[2], sys.argv[3], unsigned=True)
            sys.exit(0)
        promote_latest = len(sys.argv) == 6 and sys.argv[1] == "--promote-latest"
        args = sys.argv[2:] if promote_latest else sys.argv[1:]
        if len(args) != 4:
            raise ValueError(
                "usage: publish-prerelease.py [--promote-latest] DIST VERSION TAG SOURCE_SHA"
            )
        print(
            json.dumps(
                publish(
                    *args,
                    repository=os.environ.get("GITHUB_REPOSITORY"),
                    promote_latest=promote_latest,
                )
            )
        )
    except (
        ValueError,
        OSError,
        subprocess.CalledProcessError,
        json.JSONDecodeError,
    ) as error:
        print(f"error: {error}", file=sys.stderr)
        sys.exit(1)
