#!/usr/bin/env python3
"""Publish staged OCI indexes only when destination digest equality is proven."""
# cspell:ignore readback

import argparse
import hashlib
import json
import re
import subprocess
import sys
import tempfile
from pathlib import Path

UNSTABLE = ("ghcr.io/datamitsu/datamitsu-unstable",)
TAGGED = ("ghcr.io/datamitsu/datamitsu", "docker.io/datamitsu/datamitsu")
ARCHES = {"amd64", "arm64"}
DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
NUMBER = r"(?:0|[1-9][0-9]*)"
SEMVER = re.compile(rf"v?({NUMBER})\.({NUMBER})\.({NUMBER})(?:-rc\.({NUMBER}))?\Z")


def run(*args):
    return subprocess.run(args, check=True, capture_output=True).stdout


def require(condition, message):
    if not condition:
        raise ValueError(message)


def index_digest(ref):
    raw = run("skopeo", "inspect", "--raw", ref)
    manifest = json.loads(raw)
    require(isinstance(manifest.get("manifests"), list), f"{ref} is not an image index")
    images, attestations = {}, []
    for entry in manifest["manifests"]:
        platform = entry.get("platform", {})
        os_name, arch = platform.get("os"), platform.get("architecture")
        digest = entry.get("digest", "")
        require(DIGEST.fullmatch(digest), f"{ref} has an invalid descriptor digest")
        annotations = entry.get("annotations", {})
        if (os_name, arch) == ("unknown", "unknown"):
            require(
                annotations.get("vnd.docker.reference.type") == "attestation-manifest",
                f"{ref} has an unexpected unknown-platform descriptor",
            )
            attestations.append(annotations.get("vnd.docker.reference.digest"))
            continue
        require(
            os_name == "linux" and arch in ARCHES and arch not in images,
            f"{ref} has a duplicate or unexpected platform: {os_name}/{arch}",
        )
        images[arch] = digest
    require(
        images.keys() == ARCHES and len(set(images.values())) == len(ARCHES),
        f"{ref} must contain exactly one distinct linux/amd64 and linux/arm64 image",
    )
    require(
        all(digest in images.values() for digest in attestations),
        f"{ref} has an attestation that does not reference a runnable image",
    )
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def destination_digest(repository, tag):
    """Accept only registry responses that identify a missing manifest."""
    args = ("skopeo", "inspect", "--raw", f"docker://{repository}:{tag}")
    result = subprocess.run(args, check=False, capture_output=True)
    if result.returncode == 0:
        return "sha256:" + hashlib.sha256(result.stdout).hexdigest()
    error = result.stderr.decode(errors="replace")
    if re.search(
        r"(?:StatusCode\s*[:=]\s*404|status code[: ]+404|HTTP[^\n]*\b404\b|:\s*manifest unknown(?:: manifest unknown)?\s*$)",
        error,
        re.IGNORECASE,
    ):
        return None
    raise subprocess.CalledProcessError(
        result.returncode, args, result.stdout, result.stderr
    )


def stable_version(version):
    match = SEMVER.fullmatch(version)
    if match is None or match.group(4):
        raise ValueError(f"not a stable SemVer version: {version}")
    return tuple(map(int, match.groups()[:3]))


def tags(repository):
    data = json.loads(run("skopeo", "list-tags", f"docker://{repository}"))
    require(isinstance(data.get("Tags"), list), f"invalid tag listing for {repository}")
    return data["Tags"]


def current_alias_order(repository, alias, alpine):
    current = destination_digest(repository, alias)
    if current is None:
        return None
    suffix = "-alpine" if alpine else ""
    evidence = set()
    for candidate in tags(repository):
        match = re.fullmatch(
            rf"({NUMBER}\.{NUMBER}\.{NUMBER}){re.escape(suffix)}", candidate
        )
        if match and destination_digest(repository, candidate) == current:
            evidence.add(stable_version(match.group(1)))
    require(
        len(evidence) == 1,
        f"cannot establish unique stable version provenance for {repository}:{alias} digest {current}",
    )
    return next(iter(evidence))


def metadata_version(source):
    versions = set()
    for arch in sorted(ARCHES):
        raw = run(
            "skopeo",
            "--override-os",
            "linux",
            "--override-arch",
            arch,
            "inspect",
            "--config",
            source,
        )
        labels = json.loads(raw).get("config", {}).get("Labels", {})
        version = labels.get("org.opencontainers.image.version")
        require(
            isinstance(version, str), f"missing image version label for linux/{arch}"
        )
        versions.add(version)
    require(len(versions) == 1, "Debian and Alpine platform image versions disagree")
    return versions.pop()


def main():
    parser = argparse.ArgumentParser(
        description=(
            "Publish checked image indexes by digest. Stable aliases require a current "
            "immutable-version match and monotonic numeric SemVer order; recovery never writes."
        )
    )
    parser.add_argument("dist", type=Path)
    parser.add_argument("release_tag")
    parser.add_argument("--check-only", action="store_true")
    parser.add_argument("--registry", choices=("ghcr", "dockerhub"))
    parser.add_argument(
        "--promote", action="store_true", help="promote existing signed stable indexes"
    )
    parser.add_argument(
        "--require-present",
        action="store_true",
        help="recovery: require all immutable tags to exist and perform no registry writes",
    )
    args = parser.parse_args()

    unstable = re.fullmatch(r"unstable-[1-9][0-9]*-[1-9][0-9]*", args.release_tag)
    if unstable:
        is_stable = False
        version = "0.0.0-unstable." + args.release_tag.removeprefix(
            "unstable-"
        ).replace("-", ".")
    else:
        parsed = SEMVER.fullmatch(args.release_tag)
        if parsed is None:
            raise ValueError("invalid release tag")
        rc = parsed.group(4)
        is_stable = rc is None
        version = ".".join(parsed.groups()[:3]) + (
            f"-rc.{rc}" if rc is not None else ""
        )
    if unstable:
        repositories = UNSTABLE
        sources = (
            (args.dist / "datamitsu-debian.oci.tar", args.release_tag),
            (args.dist / "datamitsu-alpine.oci.tar", args.release_tag + "-alpine"),
        )
    else:
        repositories = TAGGED
        sources = (
            (args.dist / "datamitsu-debian.oci.tar", version),
            (args.dist / "datamitsu-alpine.oci.tar", version + "-alpine"),
        )
    if args.registry:
        if unstable and args.registry != "ghcr":
            raise ValueError("unstable releases use GHCR only")
        repositories = tuple(
            repo
            for repo in repositories
            if repo.startswith("ghcr.io/" if args.registry == "ghcr" else "docker.io/")
        )
    if args.promote and (not is_stable or args.check_only or args.require_present):
        raise ValueError("promotion is a separate stable-only operation")
    for path, _ in sources:
        require(path.is_file(), f"missing prepared image archive: {path}")

    with tempfile.TemporaryDirectory() as stage:
        plans = []
        for index, (path, tag) in enumerate(sources):
            source = f"oci-archive:{path}"
            expected = index_digest(source)
            local = f"oci:{Path(stage) / str(index)}:checked"
            actual_version = metadata_version(source)
            require(
                actual_version == version,
                f"{tag} image version metadata {actual_version} does not match release version {version}",
            )
            existing = (
                []
                if args.check_only
                else [destination_digest(repo, tag) for repo in repositories]
            )
            require(
                all(digest is None or digest == expected for digest in existing),
                f"refusing to overwrite {tag}: an existing index has different content",
            )
            require(
                not (args.require_present or args.promote)
                or all(digest == expected for digest in existing),
                f"cannot recover missing image {tag}",
            )
            plans.append((tag, source, local, expected, existing))
        require(
            len({plan[3] for plan in plans}) == len(plans),
            "Debian and Alpine archives have the same index digest",
        )

        for tag, source, local, expected, existing in plans:
            copy_source = source
            if existing and all(digest == expected for digest in existing):
                copy_source = f"docker://{repositories[0]}@{expected}"
            run("skopeo", "copy", "--all", "--preserve-digests", copy_source, local)
            require(
                index_digest(local) == expected,
                f"local OCI copy changed the index digest for {tag}",
            )

        if args.check_only:
            return

        for tag, _, local, expected, existing in plans:
            for repository, current in zip(
                repositories, existing or [None] * len(repositories)
            ):
                now = destination_digest(repository, tag)
                require(
                    now == current,
                    f"destination changed after preflight for {repository}:{tag}",
                )
                if now is None:
                    run(
                        "skopeo",
                        "copy",
                        "--all",
                        "--preserve-digests",
                        local,
                        f"docker://{repository}:{tag}",
                    )
                raw = run(
                    "skopeo",
                    "inspect",
                    "--no-creds",
                    "--raw",
                    f"docker://{repository}:{tag}",
                )
                require(
                    "sha256:" + hashlib.sha256(raw).hexdigest() == expected,
                    f"anonymous tag digest mismatch for {repository}:{tag}",
                )
                with tempfile.TemporaryDirectory() as readback:
                    local_readback = f"oci:{readback}:checked"
                    run(
                        "skopeo",
                        "copy",
                        "--src-no-creds",
                        "--all",
                        "--preserve-digests",
                        f"docker://{repository}@{expected}",
                        local_readback,
                    )
                    require(
                        index_digest(local_readback) == expected,
                        f"anonymous index digest mismatch for {repository}:{tag}",
                    )

        if args.promote:
            candidate = stable_version(version)
            promotions = []
            for repository in repositories:
                for alpine, suffix in ((False, ""), (True, "-alpine")):
                    digest = next(
                        plan[3] for plan in plans if plan[0] == version + suffix
                    )
                    for alias in ("major", "minor", "stable", "latest"):
                        target = (
                            str(candidate[0])
                            if alias == "major"
                            else f"{candidate[0]}.{candidate[1]}"
                            if alias == "minor"
                            else alias
                        ) + suffix
                        current_order = current_alias_order(repository, target, alpine)
                        require(
                            current_order is None or candidate >= current_order,
                            f"refusing stable alias downgrade {repository}:{target}: {candidate} < {current_order}",
                        )
                        promotions.append(
                            (
                                repository,
                                target,
                                digest,
                                destination_digest(repository, target),
                            )
                        )
            for repository, target, digest, current in promotions:
                require(
                    destination_digest(repository, target) == current,
                    f"stable alias changed after preflight for {repository}:{target}",
                )
                if current != digest:
                    run(
                        "skopeo",
                        "copy",
                        "--all",
                        "--preserve-digests",
                        f"docker://{repository}@{digest}",
                        f"docker://{repository}:{target}",
                    )
                require(
                    destination_digest(repository, target) == digest,
                    f"alias digest mismatch for {repository}:{target}",
                )

        for tag, _, _, expected, _ in plans:
            for repository in repositories:
                print(f"{repository}:{tag} {expected}")


if __name__ == "__main__":
    try:
        main()
    except (
        OSError,
        subprocess.CalledProcessError,
        ValueError,
        json.JSONDecodeError,
    ) as error:
        print(f"publish-prepared-images: {error}", file=sys.stderr)
        sys.exit(1)
