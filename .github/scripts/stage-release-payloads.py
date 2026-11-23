import hashlib
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
from email.parser import BytesParser
from pathlib import Path
from zipfile import ZipFile

# cspell:ignore headersonly


def unpack(archive, output):
    with ZipFile(archive) as bundle:
        seen = set()
        for member in bundle.infolist():
            name = member.filename.rstrip("/")
            parts = name.split("/")
            if (
                any(part in ("", ".", "..") for part in parts)
                or "\\" in name
                or name in seen
            ):
                raise ValueError(f"invalid or duplicate ZIP member: {name!r}")
            seen.add(name)
            kind = stat.S_IFMT(member.external_attr >> 16)
            if kind not in (0, stat.S_IFDIR if member.is_dir() else stat.S_IFREG):
                raise ValueError(f"unexpected ZIP member type: {name!r}")
            target = output.joinpath(*parts)
            if member.is_dir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with bundle.open(member) as source, target.open("xb") as dest:
                    shutil.copyfileobj(source, dest)


def open_tar_member(archive: tarfile.TarFile, member: tarfile.TarInfo):
    stream = archive.extractfile(member)
    if stream is None:
        raise ValueError(f"archive member is not readable: {member.name}")
    return stream


def stream_digest(stream):
    digest = hashlib.sha256()
    for block in iter(lambda: stream.read(65536), b""):
        digest.update(block)
    return digest.digest()


def verify_npm_packages(directory, version):
    number = r"(?:0|[1-9][0-9]*)"
    tagged = rf"{number}\.{number}\.{number}(?:-rc\.{number})?"
    unstable = r"0\.0\.0-unstable\.[1-9][0-9]*\.[1-9][0-9]*"
    if not re.fullmatch(rf"(?:{tagged}|{unstable})", version):
        raise ValueError("invalid approved npm release version")
    names = [
        f"datamitsu-{platform}-{arch}"
        for platform in ("darwin", "freebsd", "linux", "win32")
        for arch in ("arm64", "x64")
    ] + ["datamitsu"]
    if {p.name for p in directory.glob("*.tgz")} != {f"{name}.tgz" for name in names}:
        raise ValueError("unexpected npm package inventory")
    for name in names:
        with tarfile.open(directory / f"{name}.tgz", "r:gz") as bundle:
            metadata = []
            seen = set()
            directories = set()
            files = set()
            for member in bundle:
                path = member.name.removesuffix("/") if member.isdir() else member.name
                if "\\" in path or any(
                    part in ("", ".", "..") for part in path.split("/")
                ):
                    raise ValueError(f"noncanonical npm archive path in {name}.tgz")
                if path == "package" and not member.isdir():
                    raise ValueError(f"invalid package root in {name}.tgz")
                if path != "package" and not path.startswith("package/"):
                    raise ValueError(f"unexpected npm archive root in {name}.tgz")
                if path in seen or not (member.isdir() or member.isfile()):
                    raise ValueError(
                        f"duplicate or unsupported npm archive member in {name}.tgz"
                    )
                seen.add(path)
                parents = {str(parent) for parent in Path(path).parents}
                if parents & files or (member.isfile() and path in directories):
                    raise ValueError(f"conflicting npm archive member in {name}.tgz")
                directories.update(parents)
                if member.isfile():
                    files.add(path)
                if path == "package/package.json":
                    metadata.append(member)
            if (
                len(metadata) != 1
                or not metadata[0].isfile()
                or metadata[0].size > 65536
            ):
                raise ValueError(f"invalid package.json in {name}.tgz")
            with open_tar_member(bundle, metadata[0]) as source:
                package = json.load(source)
                if "tag" in package or "publishConfig" in package:
                    raise ValueError(
                        f"npm publication overrides are forbidden in {name}.tgz"
                    )
            if name == "datamitsu":
                pin = bundle.getmember("package/parsers-oci.json")
                expected_pin = directory.parent / "parsers-oci.json"
                if (
                    not pin.isfile()
                    or pin.size > 65536
                    or pin.size != expected_pin.stat().st_size
                ):
                    raise ValueError("invalid parser pin in launcher tarball")
                with open_tar_member(bundle, pin) as source:
                    if source.read() != expected_pin.read_bytes():
                        raise ValueError(
                            "launcher parser pin differs from prepared parser pin"
                        )
        if (
            package.get("name") != f"@datamitsu/{name}"
            or package.get("version") != version
        ):
            raise ValueError(f"unexpected npm coordinates in {name}.tgz")
        if name == "datamitsu":
            expected = {f"@datamitsu/{platform}": version for platform in names[:-1]}
            if package.get("optionalDependencies") != expected:
                raise ValueError(
                    "launcher platform dependencies do not match the approved version"
                )


def verify_distribution_hashes(directory, names):
    expected = []
    for name in sorted(names):
        with (directory / name).open("rb") as artifact:
            if not (directory / name).stat().st_size:
                raise ValueError(f"empty release artifact: {name}")
            expected.append(
                f"{hashlib.file_digest(artifact, 'sha256').hexdigest()}  {name}"
            )
    if sorted((directory / "SHA256SUMS").read_text().splitlines()) != sorted(expected):
        raise ValueError(f"distribution checksums differ in {directory.name}")


def verify_gem_payload(dist):
    expected = {}
    for platform in ("linux", "darwin", "freebsd", "windows"):
        suffix = ".exe" if platform == "windows" else ""
        for arch, ruby_arch in (("amd64", "x64"), ("arm64", "arm64")):
            expected[f"libexec/datamitsu-{platform}-{ruby_arch}/datamitsu{suffix}"] = (
                dist / "binaries" / f"datamitsu-{platform}_{arch}{suffix}"
            )
    seen = set()
    with tarfile.open(dist / "ruby" / "datamitsu.gem", "r:") as gem:
        data = [member for member in gem if member.name == "data.tar.gz"]
        if len(data) != 1 or not data[0].isfile():
            raise ValueError("missing or ambiguous gem data archive")
        with (
            open_tar_member(gem, data[0]) as compressed,
            tarfile.open(fileobj=compressed, mode="r|gz") as payload,
        ):
            for member in payload:
                if member.name not in expected and member.name != "bin/datamitsu":
                    continue
                if (
                    member.name in seen
                    or not member.isfile()
                    or not member.size
                    or not member.mode & 0o111
                ):
                    raise ValueError(f"invalid gem executable: {member.name}")
                seen.add(member.name)
                if member.name in expected:
                    with (
                        open_tar_member(payload, member) as actual,
                        expected[member.name].open("rb") as source,
                    ):
                        if (
                            stream_digest(actual)
                            != hashlib.file_digest(source, "sha256").digest()
                        ):
                            raise ValueError(
                                f"gem executable differs from prepared binary: {member.name}"
                            )
    if seen != expected.keys() | {"bin/datamitsu"}:
        raise ValueError("incomplete gem executable inventory")


def verify_tagged_wheels(dist, version):
    wheel_version = version.replace("-rc.", "rc", 1)
    targets = {
        "manylinux2014_x86_64": ("linux", "amd64"),
        "manylinux2014_aarch64": ("linux", "arm64"),
        "macosx_13_0_x86_64": ("darwin", "amd64"),
        "macosx_13_0_arm64": ("darwin", "arm64"),
        "win_amd64": ("windows", "amd64"),
        "win_arm64": ("windows", "arm64"),
    }
    wheels = {
        f"datamitsu-{wheel_version}-py3-none-{tag}.whl": target
        for tag, target in targets.items()
    }
    if {wheel.name for wheel in (dist / "python").glob("*.whl")} != wheels.keys():
        raise ValueError("unexpected tagged wheel inventory")
    verify_distribution_hashes(dist / "python", wheels)
    metadata_path = f"datamitsu-{wheel_version}.dist-info/METADATA"
    for name, (platform, arch) in wheels.items():
        suffix = ".exe" if platform == "windows" else ""
        python_arch = "x86_64" if arch == "amd64" else arch
        binary_name = (
            f"datamitsu/bin/datamitsu-{platform}-{python_arch}/datamitsu{suffix}"
        )
        expected = dist / "binaries" / f"datamitsu-{platform}_{arch}{suffix}"
        with ZipFile(dist / "python" / name) as wheel:
            entries = [
                entry for entry in wheel.infolist() if entry.filename == metadata_path
            ]
            if len(entries) != 1 or not 0 < entries[0].file_size <= 65536:
                raise ValueError(
                    f"missing, duplicate, or oversized wheel metadata: {name}"
                )
            metadata = BytesParser().parsebytes(
                wheel.read(entries[0]), headersonly=True
            )
            if metadata.get_all("Name") != ["datamitsu"] or metadata.get_all(
                "Version"
            ) != [wheel_version]:
                raise ValueError(
                    f"wheel metadata does not match approved release: {name}"
                )
            binaries = [
                entry for entry in wheel.infolist() if entry.filename == binary_name
            ]
            if (
                len(binaries) != 1
                or not 0 < binaries[0].file_size == expected.stat().st_size
            ):
                raise ValueError(
                    f"missing, duplicate, or wrong-size wheel executable: {name}"
                )
            with wheel.open(binaries[0]) as actual, expected.open("rb") as source:
                if (
                    stream_digest(actual)
                    != hashlib.file_digest(source, "sha256").digest()
                ):
                    raise ValueError(
                        f"wheel executable differs from prepared binary: {name}"
                    )


def verify_tagged_distributions(dist, version):
    verify_tagged_wheels(dist, version)
    verify_distribution_hashes(dist / "ruby", ["datamitsu.gem"])
    verify_gem_payload(dist)
    if "-rc." not in version:
        verify_distribution_hashes(
            dist / "setup-action", ["action.yml", "dist/index.mjs", "README.md"]
        )


def stage(output, payloads, repository, version):
    if set(payloads) != {"release", "vsix"}:
        raise ValueError("expected release and VSIX payloads")
    for entry in payloads.values():
        if not re.fullmatch(r"[1-9][0-9]*", entry["id"]):
            raise ValueError("invalid artifact ID")
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", entry["archive_digest"]):
            raise ValueError("invalid artifact archive digest")
    if output.exists():
        raise ValueError("payload destination already exists")
    with tempfile.TemporaryDirectory(dir=output.parent) as directory:
        root = Path(directory)
        for kind, entry in payloads.items():
            archive = root / f"{kind}.zip"
            with archive.open("w+b") as file:
                subprocess.run(
                    [
                        "gh",
                        "api",
                        f"repos/{repository}/actions/artifacts/{entry['id']}/zip",
                    ],
                    stdout=file,
                    check=True,
                )
                file.seek(0)
                digest = "sha256:" + hashlib.file_digest(file, "sha256").hexdigest()
            if digest != entry["archive_digest"]:
                raise ValueError(
                    f"{kind} artifact {entry['id']} archive digest mismatch"
                )
        staged = root / "staged"
        for kind in payloads:
            unpack(root / f"{kind}.zip", staged / kind)
        verify_npm_packages(staged / "release" / "dist" / "npm", version)
        dist = staged / "release" / "dist"
        required = [
            dist / name
            for name in (
                "datamitsu-debian.oci.tar",
                "datamitsu-alpine.oci.tar",
                "parsers-oci.json",
                "parsers-oci-manifest.json",
            )
        ]
        required.extend(
            (
                dist / f"datamitsu-{version}.vsix",
                staged / "vsix" / f"datamitsu-{version}.vsix",
            )
        )
        for path in required:
            if not path.is_file() or not path.stat().st_size:
                raise ValueError(
                    f"missing or empty prepared payload: {path.relative_to(staged)}"
                )
        release_vsix, standalone_vsix = required[-2:]
        with (
            release_vsix.open("rb") as release_file,
            standalone_vsix.open("rb") as standalone_file,
        ):
            if (
                hashlib.file_digest(release_file, "sha256").digest()
                != hashlib.file_digest(standalone_file, "sha256").digest()
            ):
                raise ValueError("release and standalone VSIX payloads differ")
        with ZipFile(standalone_vsix) as extension:
            if (
                "extension/package.json" not in extension.namelist()
                or extension.testzip() is not None
            ):
                raise ValueError("invalid prepared VSIX payload")
        if "-unstable." not in version:
            verify_tagged_distributions(dist, version)
        staged.rename(output)


if __name__ == "__main__":
    stage(
        Path(sys.argv[1]),
        json.loads(os.environ["APPROVED_PAYLOADS"]),
        os.environ["GITHUB_REPOSITORY"],
        os.environ["APPROVED_VERSION"],
    )
