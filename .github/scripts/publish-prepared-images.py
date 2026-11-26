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


def current_alias_order(repository, digest, suffix):
    version = metadata_version(f"docker://{repository}@{digest}")
    order = stable_version(version)
    require(
        destination_digest(repository, version + suffix) == digest,
        f"stable image metadata does not match the versioned digest in {repository}",
    )
    return order


def metadata(source):
    result = {}
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
        result[arch] = (
            version,
            labels.get("com.datamitsu.preparation-run"),
            labels.get("com.datamitsu.preparation-attempt"),
        )
    require(
        len(set(result.values())) == 1,
        "Debian and Alpine platform image metadata disagree",
    )
    return result["amd64"]


def metadata_version(source):
    return metadata(source)[0]


def preparation_order(source):
    _, run_id, attempt = metadata(source)
    require(
        isinstance(run_id, str) and run_id.isdecimal() and int(run_id) > 0,
        "missing or invalid preparation run label",
    )
    require(
        isinstance(attempt, str) and attempt.isdecimal() and int(attempt) > 0,
        "missing or invalid preparation attempt label",
    )
    return (int(run_id), int(attempt))


def verified_nonstable_order(repository, digest, suffix):
    immutable = f"docker://{repository}@{digest}"
    candidate_version, run_id, attempt = metadata(immutable)
    require(
        isinstance(run_id, str) and run_id.isdecimal() and int(run_id) > 0,
        "current alias has invalid preparation run label",
    )
    require(
        isinstance(attempt, str) and attempt.isdecimal() and int(attempt) > 0,
        "current alias has invalid preparation attempt label",
    )
    parsed = SEMVER.fullmatch(candidate_version)
    manual = re.fullmatch(
        r"0\.0\.0-(preview|unstable)\.((?:[1-9][0-9]{11}\.)?([1-9][0-9]*)\.([1-9][0-9]*))",
        candidate_version,
    )
    if manual:
        require(
            (run_id, attempt) == manual.groups()[2:],
            "version and preparation labels disagree",
        )
        version_tag = manual.group(1) + "-" + manual.group(2).replace(".", "-") + suffix
    else:
        require(
            parsed is not None and parsed.group(4) is not None,
            "invalid current nonstable image version",
        )
        version_tag = candidate_version + suffix
    require(
        destination_digest(repository, version_tag) == digest,
        f"current nonstable metadata does not match immutable tag {repository}:{version_tag}",
    )
    return int(run_id), int(attempt)


def nonstable_promotions(repository, plans, candidate, alias):
    promotions = []
    for suffix in ("", "-alpine"):
        immutable_tag = next(
            plan[0]
            for plan in plans
            if plan[0].endswith(suffix) and (suffix or not plan[0].endswith("-alpine"))
        )
        digest = next(plan[3] for plan in plans if plan[0] == immutable_tag)
        current = destination_digest(repository, alias + suffix)
        if current is not None:
            current_order = verified_nonstable_order(repository, current, suffix)
            if candidate < current_order:
                continue
            require(
                candidate != current_order or current == digest,
                f"same preparation identity has different bytes at {repository}:{alias + suffix}",
            )
        promotions.append((repository, alias + suffix, digest, current))
    return promotions


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
    parser.add_argument("--alias-policy", choices=("legacy", "ordered-v1"))
    parser.add_argument("--registry", choices=("ghcr", "dockerhub"))
    parser.add_argument(
        "--promote", action="store_true", help="promote existing signed stable indexes"
    )
    parser.add_argument(
        "--promote-nonstable",
        action="store_true",
        help="promote the guarded preview or unstable alias",
    )
    parser.add_argument(
        "--require-present",
        action="store_true",
        help="recovery: require all immutable tags to exist and perform no registry writes",
    )
    args = parser.parse_args()
    if args.alias_policy and not args.check_only:
        parser.error("--alias-policy requires --check-only")

    unstable = re.fullmatch(
        r"(unstable|preview)-(?:[1-9][0-9]{11}-)?[1-9][0-9]*-[1-9][0-9]*",
        args.release_tag,
    )
    if unstable:
        is_stable = False
        kind, identity = args.release_tag.split("-", 1)
        version = f"0.0.0-{kind}." + identity.replace("-", ".")
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
        repositories = TAGGED[:1] if kind == "preview" else UNSTABLE
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
    if args.promote_nonstable and (
        is_stable or args.check_only or args.require_present or not args.registry
    ):
        raise ValueError(
            "nonstable promotion requires a nonstable release and exactly one selected registry"
        )
    if args.promote_nonstable and len(repositories) != 1:
        raise ValueError("nonstable promotion requires exactly one selected registry")
    if args.promote and (not is_stable or args.check_only or args.require_present):
        raise ValueError("promotion is a separate stable-only operation")
    for path, _ in sources:
        require(path.is_file(), f"missing prepared image archive: {path}")

    with tempfile.TemporaryDirectory() as stage:
        plans = []
        candidate_orders = set()
        for index, (path, tag) in enumerate(sources):
            source = f"oci-archive:{path}"
            if args.alias_policy == "legacy":
                require(
                    metadata(source)[1:] == (None, None),
                    "legacy index conflicts with prepared image labels",
                )
            elif args.alias_policy == "ordered-v1":
                candidate_orders.add(preparation_order(source))
            candidate_order = (
                preparation_order(source) if args.promote_nonstable else None
            )
            if args.promote_nonstable:
                candidate_orders.add(candidate_order)
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
                not (args.require_present or args.promote or args.promote_nonstable)
                or all(digest == expected for digest in existing),
                f"cannot recover missing image {tag}",
            )
            plans.append((tag, source, local, expected, existing))
        if args.promote_nonstable or args.alias_policy == "ordered-v1":
            require(
                len(candidate_orders) == 1,
                "prepared images have different preparation identities",
            )
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
                for suffix in ("", "-alpine"):
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
                        current = destination_digest(repository, target)
                        if current is not None:
                            current_order = current_alias_order(
                                repository, current, suffix
                            )
                            if candidate < current_order:
                                continue
                            require(
                                candidate != current_order or current == digest,
                                f"same stable version has different bytes at {repository}:{target}",
                            )
                        promotions.append(
                            (
                                repository,
                                target,
                                digest,
                                current,
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

        if args.promote_nonstable:
            candidate = next(iter(candidate_orders))
            alias = "unstable" if unstable and kind == "unstable" else "preview"
            promotions = nonstable_promotions(repositories[0], plans, candidate, alias)
            for repository, target, digest, current in promotions:
                require(
                    destination_digest(repository, target) == current,
                    f"nonstable alias changed after preflight for {repository}:{target}",
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
