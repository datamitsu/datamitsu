import argparse
import base64
import hashlib
import json
import os
import re
import subprocess
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

# cspell:ignore livecheck postflight

VERSION_RE = re.compile(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
SHA256_RE = re.compile(r"[0-9a-f]{64}\Z")
ARCHES = {
    "darwin_amd64": "darwin/amd64",
    "darwin_arm64": "darwin/arm64",
    "linux_amd64": "linux/amd64",
    "linux_arm64": "linux/arm64",
    "windows_amd64": "windows/amd64",
    "windows_arm64": "windows/arm64",
}
GH_API = "https://api.github.com"


def require(condition, message):
    if not condition:
        raise ValueError(message)


def command(*args, input=None):
    return subprocess.run(args, check=True, input=input, capture_output=True).stdout


def gh_api(method, path, payload=None, expected=(200,)):
    token = os.environ.get("GH_TOKEN")
    require(token, "GH_TOKEN is required for destination API operations")
    data = (
        None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    )
    request = urllib.request.Request(
        GH_API + path,
        data=data,
        method=method,
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
            **({"Content-Type": "application/json"} if data else {}),
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            require(
                response.status in expected,
                f"unexpected GitHub API status {response.status} for {path}",
            )
            body = response.read()
            return json.loads(body) if body else None
    except urllib.error.HTTPError as error:
        if error.code in expected:
            body = error.read()
            return json.loads(body) if body else None
        raise ValueError(
            f"GitHub API {method} {path} failed with HTTP {error.code}"
        ) from error


def digest(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def validate_dist(dist, version, source_sha):
    require(
        re.fullmatch(r"[0-9a-f]{40}", source_sha) is not None,
        "SOURCE_SHA must be a full lowercase Git SHA",
    )
    root = Path(dist).resolve(strict=True)
    require(root.is_dir(), "DIST must be a directory")
    checksums_path = root / "checksums.txt"
    require(checksums_path.is_file(), "DIST lacks GoReleaser checksums.txt")
    checksums = {}
    for line in checksums_path.read_text(encoding="ascii").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([^\s]+)", line)
        if match is None or match.group(2) in checksums:
            raise ValueError("invalid checksums.txt")
        checksums[match.group(2)] = match.group(1)
    assets = {}
    for suffix in ARCHES:
        ext = ".zip" if suffix.startswith("windows") else ".tar.gz"
        filename = f"datamitsu_{version}_{suffix}{ext}"
        path = root / filename
        require(
            path.is_file() and path.stat().st_size > 0,
            f"missing staged asset: {filename}",
        )
        require(
            checksums.get(filename) == digest(path),
            f"GoReleaser checksum mismatch for {filename}",
        )
        assets[filename] = path
    return assets


def url(repository, version, filename):
    return f"https://github.com/{repository}/releases/download/v{version}/{filename}"


def read_current(repository, branch, path):
    encoded = urllib.parse.quote(path, safe="/")
    response = gh_api(
        "GET",
        f"/repos/{repository}/contents/{encoded}?ref={urllib.parse.quote(branch)}",
        expected=(200, 404),
    )
    if response is None:
        return None, None
    require(response.get("type") == "file", f"unexpected object at {repository}:{path}")
    content = base64.b64decode(response["content"], validate=False)
    require(
        base64.b64encode(content).decode().rstrip("=")
        == response["content"].replace("\n", "").rstrip("="),
        "invalid GitHub contents encoding",
    )
    return content, response["sha"]


def atomic_commit(repository, branch, path, content, message, expected_sha):
    ref = gh_api(
        "GET",
        f"/repos/{repository}/git/ref/heads/{urllib.parse.quote(branch, safe='')}",
    )
    parent = ref["object"]["sha"]
    blob = gh_api(
        "POST",
        f"/repos/{repository}/git/blobs",
        {"content": base64.b64encode(content).decode(), "encoding": "base64"},
        expected=(201,),
    )
    commit_tree = gh_api("GET", f"/repos/{repository}/git/commits/{parent}")["tree"][
        "sha"
    ]
    tree = gh_api(
        "POST",
        f"/repos/{repository}/git/trees",
        {
            "base_tree": commit_tree,
            "tree": [
                {"path": path, "mode": "100644", "type": "blob", "sha": blob["sha"]}
            ],
        },
        expected=(201,),
    )
    commit = gh_api(
        "POST",
        f"/repos/{repository}/git/commits",
        {"message": message, "tree": tree["sha"], "parents": [parent]},
        expected=(201,),
    )
    _, current_sha = read_current(repository, branch, path)
    require(current_sha == expected_sha, "destination manifest changed during update")
    result = gh_api(
        "PATCH",
        f"/repos/{repository}/git/refs/heads/{urllib.parse.quote(branch, safe='')}",
        {"sha": commit["sha"], "force": False},
        expected=(200,),
    )
    require(
        result["object"]["sha"] == commit["sha"],
        "destination ref did not advance to proposed commit",
    )


def compare_upgrade(target, current, version):
    text = current.decode("utf-8")
    if target == "homebrew":
        match = re.search(
            r'^\s*version "([0-9]+\.[0-9]+\.[0-9]+)"\s*$', text, re.MULTILINE
        )
    elif target == "scoop":
        try:
            old = json.loads(text)["version"]
            require(
                VERSION_RE.fullmatch(old) is not None, "invalid current Scoop version"
            )
            require(
                tuple(map(int, version.split("."))) > tuple(map(int, old.split("."))),
                "refusing Scoop downgrade or duplicate-version update",
            )
            return
        except (KeyError, json.JSONDecodeError) as error:
            raise ValueError("cannot determine current Scoop version") from error
    else:
        raise ValueError("unknown mutable manifest target")
    if match is None:
        raise ValueError(f"cannot determine current {target} version")
    require(
        tuple(map(int, version.split(".")))
        > tuple(map(int, match.group(1).split("."))),
        f"refusing {target} downgrade or duplicate-version update",
    )


def homebrew(version, repository, assets):
    def block(suffix, platform):
        ext = ".zip" if suffix.startswith("windows") else ".tar.gz"
        filename = f"datamitsu_{version}_{suffix}{ext}"
        return f'      sha256 "{digest(assets[filename])}"\n      url "https://github.com/{repository}/releases/download/v#{{version}}/datamitsu_#{{version}}_{suffix}{ext}"'

    return (
        f'''# This file was generated by GoReleaser. DO NOT EDIT.\ncask "datamitsu" do\n  version "{version}"\n\n  on_macos do\n    on_arm do\n{block("darwin_arm64", "arm64")}\n    end\n    on_intel do\n{block("darwin_amd64", "amd64")}\n    end\n  end\n  on_linux do\n    on_arm do\n{block("linux_arm64", "arm64")}\n    end\n    on_intel do\n{block("linux_amd64", "amd64")}\n    end\n  end\n\n  name "datamitsu"\n  desc "Configuration management and binary distribution tool."\n  homepage "https://datamitsu.com/"\n\n  livecheck do\n    skip "Auto-generated on release."\n  end\n\n  binary "datamitsu"\n  bash_completion "completions/datamitsu.bash"\n  zsh_completion "completions/_datamitsu"\n  fish_completion "completions/datamitsu.fish"\n\n  postflight_steps do\n    on_macos do\n      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{{{staged_path}}}}/datamitsu"]\n    end\n  end\n\n  zap :not_required\nend\n'''
    ).encode()


def scoop(version, repository, assets):
    architecture = {}
    for key, suffix in (("64bit", "windows_amd64"), ("arm64", "windows_arm64")):
        filename = f"datamitsu_{version}_{suffix}.zip"
        architecture[key] = {
            "url": f"https://github.com/{repository}/releases/download/v#{{version}}/datamitsu_#{{version}}_{suffix}.zip",
            "bin": ["datamitsu.exe"],
            "hash": digest(assets[filename]),
        }
    return (
        json.dumps(
            {
                "version": version,
                "architecture": architecture,
                "homepage": "https://datamitsu.com/",
                "license": "MIT",
                "description": "Configuration management and binary distribution tool.\nJavaScript-configurable tool orchestration for linting and development workflows.",
            },
            indent=4,
        )
        + "\n"
    ).encode()


def winget_files(version, repository, assets, release_date):
    installers = []
    for arch, suffix in (("x64", "windows_amd64"), ("arm64", "windows_arm64")):
        filename = f"datamitsu_{version}_{suffix}.zip"
        installers.append(
            f"  - Architecture: {arch}\n    NestedInstallerType: portable\n    NestedInstallerFiles:\n      - RelativeFilePath: datamitsu.exe\n        PortableCommandAlias: datamitsu\n    InstallerUrl: {url(repository, version, filename)}\n    InstallerSha256: {digest(assets[filename])}\n    UpgradeBehavior: uninstallPrevious"
        )
    base = f"""# This file was generated by GoReleaser. DO NOT EDIT.\nPackageIdentifier: datamitsu.datamitsu\nPackageVersion: {version}\n"""
    files = {
        f"manifests/d/datamitsu/datamitsu/{version}/datamitsu.datamitsu.yaml": base
        + "DefaultLocale: en-US\nManifestType: version\nManifestVersion: 1.28.0\n",
        f"manifests/d/datamitsu/datamitsu/{version}/datamitsu.datamitsu.installer.yaml": base
        + f'InstallerLocale: en-US\nInstallerType: zip\nReleaseDate: "{release_date}"\nInstallers:\n'
        + "\n".join(installers)
        + "\nManifestType: installer\nManifestVersion: 1.28.0\n",
        f"manifests/d/datamitsu/datamitsu/{version}/datamitsu.datamitsu.locale.en-US.yaml": base
        + "PackageLocale: en-US\nPublisher: datamitsu\nPublisherUrl: https://datamitsu.com/\nPublisherSupportUrl: https://github.com/datamitsu/datamitsu/issues/new\nPackageName: datamitsu\nPackageUrl: https://datamitsu.com/\nLicense: mit\nLicenseUrl: https://github.com/datamitsu/datamitsu/blob/main/LICENSE\nCopyright: datamitsu contributors\nCopyrightUrl: https://github.com/datamitsu/datamitsu/blob/main/LICENSE\nShortDescription: Configuration management and binary distribution tool.\nDescription: |-\n  Configuration management and binary distribution tool.\n  JavaScript-configurable tool orchestration for linting and development workflows.\nMoniker: datamitsu\nTags:\n  - golang\n  - cli\n  - devtools\nReleaseNotesUrl: "
        + f"https://github.com/{repository}/releases/tag/v{version}\nManifestType: defaultLocale\nManifestVersion: 1.28.0\n",
    }
    return {name: content.encode() for name, content in files.items()}


def publish_mutable(target, args, assets):
    repositories = {
        "homebrew": ("datamitsu/homebrew-tap", "main", "Casks/datamitsu.rb"),
        "scoop": ("datamitsu/scoop-bucket", "main", "datamitsu.json"),
    }
    repo, branch, path = repositories[target]
    current, current_sha = read_current(repo, branch, path)
    content = (
        homebrew(args.version, args.source, assets)
        if target == "homebrew"
        else scoop(args.version, args.source, assets)
    )
    if current == content:
        return
    if current is not None:
        compare_upgrade(target, current, args.version)
    atomic_commit(
        repo,
        branch,
        path,
        content,
        f"chore: update datamitsu {target} to {args.version}",
        current_sha,
    )


def publish_winget(args, assets, release_date):
    fork = "datamitsu/winget-pkgs"
    upstream = "microsoft/winget-pkgs"
    branch = f"datamitsu-v{args.version}"
    files = winget_files(args.version, args.source, assets, release_date)
    present_upstream = 0
    for path, expected in files.items():
        current, _ = read_current(upstream, "master", path)
        if current is not None:
            require(
                current == expected,
                f"WinGet version exists with different bytes: {path}",
            )
            present_upstream += 1
    if present_upstream == len(files):
        return
    require(present_upstream == 0, "WinGet already has an incomplete version directory")
    ref_path = f"/repos/{fork}/git/ref/heads/{urllib.parse.quote(branch, safe='')}"
    existing_ref = gh_api("GET", ref_path, expected=(200, 404))
    if existing_ref is not None:
        for path, content in files.items():
            current, _ = read_current(fork, existing_ref["object"]["sha"], path)
            require(
                current == content,
                f"existing WinGet version branch differs or is incomplete: {path}",
            )
    else:
        parent = gh_api("GET", "/repos/microsoft/winget-pkgs/git/ref/heads/master")[
            "object"
        ]["sha"]
        parent_tree = gh_api(
            "GET", f"/repos/microsoft/winget-pkgs/git/commits/{parent}"
        )["tree"]["sha"]
        entries = []
        for path, content in files.items():
            blob = gh_api(
                "POST",
                f"/repos/{fork}/git/blobs",
                {"content": base64.b64encode(content).decode(), "encoding": "base64"},
                expected=(201,),
            )
            entries.append(
                {"path": path, "mode": "100644", "type": "blob", "sha": blob["sha"]}
            )
        tree = gh_api(
            "POST",
            f"/repos/{fork}/git/trees",
            {"base_tree": parent_tree, "tree": entries},
            expected=(201,),
        )
        commit = gh_api(
            "POST",
            f"/repos/{fork}/git/commits",
            {
                "message": f"datamitsu.datamitsu: {args.version}",
                "tree": tree["sha"],
                "parents": [parent],
            },
            expected=(201,),
        )
        gh_api(
            "POST",
            f"/repos/{fork}/git/refs",
            {"ref": f"refs/heads/{branch}", "sha": commit["sha"]},
            expected=(201,),
        )
    prs = gh_api(
        "GET", f"/repos/{upstream}/pulls?head=datamitsu:{branch}&base=master&state=open"
    )
    if prs:
        return
    gh_api(
        "POST",
        f"/repos/{upstream}/pulls",
        {
            "title": f"datamitsu.datamitsu: {args.version}",
            "head": f"datamitsu:{branch}",
            "base": "master",
            "body": f"Update datamitsu to {args.version}.",
        },
        expected=(201,),
    )


def run(args):
    require(
        VERSION_RE.fullmatch(args.version) is not None,
        "version must be stable numeric SemVer",
    )
    require(
        re.fullmatch(r"[0-9a-f]{40}", args.source_sha) is not None,
        "SOURCE_SHA must be a full lowercase Git SHA",
    )
    args.source = "datamitsu/datamitsu"
    assets = validate_dist(args.dist, args.version, args.source_sha)
    if args.target == "winget":
        release = gh_api("GET", f"/repos/{args.source}/releases/tags/v{args.version}")
        require(
            release.get("draft") is False and release.get("prerelease") is False,
            "tag is not a published stable release",
        )
        require(
            release.get("tag_name") == f"v{args.version}",
            "release tag/version mismatch",
        )
        release_date = (release.get("published_at") or "")[:10]
        require(
            re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}", release_date) is not None,
            "release has no valid publication date",
        )
        publish_winget(args, assets, release_date)
    else:
        publish_mutable(args.target, args, assets)


def main():
    parser = argparse.ArgumentParser(
        description="Publish one stable package-manager manifest from staged GoReleaser artifacts"
    )
    parser.add_argument("target", choices=("homebrew", "scoop", "winget"))
    parser.add_argument(
        "dist", help="directory containing the checked staged release payload"
    )
    parser.add_argument("version")
    parser.add_argument(
        "source_sha", help="full source commit SHA represented by the staged release"
    )
    args = parser.parse_args()
    run(args)


if __name__ == "__main__":
    try:
        main()
    except (
        OSError,
        ValueError,
        subprocess.CalledProcessError,
        json.JSONDecodeError,
        urllib.error.URLError,
    ) as error:
        raise SystemExit(f"publish-release-manifests: {error}")
