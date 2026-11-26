import argparse
import base64
import hashlib
import json
import os
import re
import stat
import subprocess
import sys
import tempfile
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import quote
from urllib.request import HTTPRedirectHandler, build_opener

# cspell:ignore DSSE dsse newurl

REGISTRY = "https://registry.npmjs.org"
APPROVED_REPOSITORY = "datamitsu/datamitsu"
APPROVED_WORKFLOW = "datamitsu/datamitsu/.github/workflows/release.yml"
TAGGED_VERSION = re.compile(
    r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-rc\.(?:0|[1-9][0-9]*))?"
)


class RejectRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("unexpected redirect from npm registry")


def archive_names():
    return [
        f"datamitsu-{platform}-{arch}.tgz"
        for platform in ("darwin", "freebsd", "linux", "win32")
        for arch in ("arm64", "x64")
    ] + ["datamitsu.tgz"]


def reconcile(directory, version, fetch=None):
    if fetch is None:
        fetch = build_opener(RejectRedirect()).open
    names = archive_names()
    if {path.name for path in directory.glob("*.tgz")} != set(names):
        raise ValueError("unexpected npm archive inventory")
    result = {"present": [], "missing": []}
    for archive in (directory / name for name in names):
        name = f"@datamitsu/{archive.stem}"
        coordinate = f"{name}@{version}"
        with archive.open("rb") as file:
            integrity = "sha512-" + base64.b64encode(
                hashlib.file_digest(file, "sha512").digest()
            ).decode("ascii")
        try:
            response = fetch(
                f"{REGISTRY}/{quote(name, safe='')}/{quote(version, safe='')}",
                timeout=30,
            )
        except HTTPError as error:
            if error.code != 404:
                raise
            result["missing"].append(archive.name)
            continue
        with response:
            metadata = json.load(response)
        if metadata.get("name") != name or metadata.get("version") != version:
            raise ValueError(f"registry did not return exact coordinate {coordinate}")
        if metadata.get("dist", {}).get("integrity") != integrity:
            raise ValueError(f"published {coordinate} differs from prepared bytes")
        result["present"].append(archive.name)
    return result


def _private_directory(path):
    path = Path(path)
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o077:
        raise ValueError("provenance directory must be private and not a symlink")
    resolved = path.resolve(strict=True)
    if resolved != path.absolute():
        raise ValueError("provenance directory must not resolve through symlinks")
    return resolved


def verify_provenance(directory, version, options, *, run=subprocess.run):
    if not TAGGED_VERSION.fullmatch(version):
        raise ValueError(
            "provenance verification is only supported for approved tagged versions"
        )
    if options.repository != APPROVED_REPOSITORY:
        raise ValueError("unapproved source repository")
    if not re.fullmatch(r"[0-9a-f]{40}", options.source_sha):
        raise ValueError("invalid approved source SHA")
    if options.source_ref != f"refs/tags/v{version}":
        raise ValueError("approved source ref does not match npm version")
    for value, label in (
        (options.source_run, "source run"),
        (options.producing_attempt, "producing attempt"),
        (options.index_id, "index ID"),
    ):
        if not re.fullmatch(r"[1-9][0-9]*", value):
            raise ValueError(f"invalid approved {label}")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", options.index_archive_digest):
        raise ValueError("invalid approved index archive digest")
    provenance_dir = _private_directory(options.provenance_dir)
    expected_files = {f"{Path(name).stem}.sigstore.json" for name in archive_names()}
    expected_files.add("signing-attempts.json")
    if {entry.name for entry in provenance_dir.iterdir()} != expected_files:
        raise ValueError(
            "provenance directory must contain exactly one bundle for each archive"
        )
    attempts_path = provenance_dir / "signing-attempts.json"
    if not stat.S_ISREG(attempts_path.lstat().st_mode):
        raise ValueError("npm signing attempt inventory must be a regular file")
    signing_attempts = json.loads(attempts_path.read_text())
    if not isinstance(signing_attempts, dict) or signing_attempts.keys() != set(
        archive_names()
    ):
        raise ValueError("invalid npm signing attempt inventory")
    for filename in archive_names():
        signing_attempt = signing_attempts[filename]
        if (
            not isinstance(signing_attempt, str)
            or not re.fullmatch(r"[1-9][0-9]*", signing_attempt)
            or int(signing_attempt) < int(options.producing_attempt)
        ):
            raise ValueError(f"invalid npm signing attempt for {filename}")
        archive = Path(directory) / filename
        bundle_path = provenance_dir / f"{archive.stem}.sigstore.json"
        if not stat.S_ISREG(bundle_path.lstat().st_mode):
            raise ValueError(f"provenance bundle must be a regular file: {filename}")
        bundle = json.loads(bundle_path.read_text())
        envelope = bundle.get("dsseEnvelope")
        if (
            not isinstance(envelope, dict)
            or envelope.get("payloadType") != "application/vnd.in-toto+json"
        ):
            raise ValueError(f"invalid DSSE envelope for {filename}")
        try:
            statement = json.loads(base64.b64decode(envelope["payload"], validate=True))
        except (KeyError, ValueError, TypeError) as error:
            raise ValueError(f"invalid DSSE payload for {filename}") from error
        with archive.open("rb") as file:
            sha512 = hashlib.file_digest(file, "sha512").hexdigest()
        purl = f"pkg:npm/%40datamitsu/{archive.stem}@{version}"
        subjects = statement.get("subject")
        if (
            statement.get("_type") != "https://in-toto.io/Statement/v1"
            or statement.get("predicateType") != "https://slsa.dev/provenance/v1"
            or not isinstance(subjects, list)
            or len(subjects) != 1
            or not isinstance(subjects[0], dict)
            or subjects[0].get("name") != purl
            or subjects[0].get("digest", {}).get("sha512") != sha512
        ):
            raise ValueError(f"provenance subject does not match {filename}")
        predicate = statement.get("predicate", {})
        definition = predicate.get("buildDefinition", {})
        params = definition.get("externalParameters", {})
        workflow = params.get("workflow", {})
        if params.get("preparation") != {
            "index_id": options.index_id,
            "index_archive_digest": options.index_archive_digest,
        }:
            raise ValueError(f"provenance preparation index does not match {filename}")
        repo_url = f"https://github.com/{APPROVED_REPOSITORY}"
        dependencies = definition.get("resolvedDependencies", [])
        if (
            definition.get("buildType")
            != "https://slsa-framework.github.io/github-actions-buildtypes/workflow/v1"
            or workflow
            != {
                "ref": options.source_ref,
                "repository": repo_url,
                "path": ".github/workflows/release.yml",
            }
            or not isinstance(dependencies, list)
            or not any(
                isinstance(item, dict)
                and item.get("uri") == f"git+{repo_url}@{options.source_ref}"
                and item.get("digest", {}).get("gitCommit") == options.source_sha
                for item in dependencies
            )
        ):
            raise ValueError(f"provenance workflow or source does not match {filename}")
        invocation = (
            predicate.get("runDetails", {}).get("metadata", {}).get("invocationId", "")
        )
        expected_invocation = (
            f"https://github.com/{APPROVED_REPOSITORY}/actions/runs/"
            f"{options.source_run}/attempts/{signing_attempt}"
        )
        if invocation != expected_invocation:
            raise ValueError(f"provenance run or attempt does not match {filename}")
        run(
            [
                "gh",
                "attestation",
                "verify",
                str(archive),
                "--repo",
                APPROVED_REPOSITORY,
                "--bundle",
                str(bundle_path),
                "--digest-alg",
                "sha512",
                "--signer-workflow",
                APPROVED_WORKFLOW,
                "--signer-digest",
                options.source_sha,
                "--source-ref",
                options.source_ref,
                "--source-digest",
                options.source_sha,
                "--format",
                "json",
            ],
            check=True,
            stdout=subprocess.DEVNULL,
        )
    return archive_names()


def upload_missing(
    directory, version, fetch=None, run=subprocess.run, *, provenance=None
):
    unstable = re.fullmatch(
        r"0\.0\.0-(?:unstable\.(?:([1-9][0-9]{11})\.)?|preview\.([1-9][0-9]{11})\.)"
        r"([1-9][0-9]*)\.([1-9][0-9]*)",
        version,
    )
    tagged = TAGGED_VERSION.fullmatch(version)
    if unstable is None and tagged is None:
        raise ValueError("invalid approved npm version")
    directory = Path(directory)
    before = reconcile(directory, version, fetch)
    if tagged and before["missing"]:
        if provenance is None:
            raise ValueError(
                "tagged npm uploads require approved cryptographic provenance inputs"
            )
        verify_provenance(directory, version, provenance)
        clean_env = {
            key: value
            for key, value in os.environ.items()
            if not key.startswith(("GITHUB_", "ACTIONS_ID_TOKEN_"))
        }
        with tempfile.TemporaryDirectory() as cwd:
            for filename in before["missing"]:
                bundle = (
                    Path(provenance.provenance_dir)
                    / f"{Path(filename).stem}.sigstore.json"
                )
                staged_tag = "staged-" + re.sub(r"[^A-Za-z0-9._-]", "-", version)
                run(
                    [
                        "npm",
                        "publish",
                        str((directory / filename).resolve()),
                        "--registry",
                        REGISTRY,
                        "--provenance=false",
                        "--tag",
                        staged_tag,
                        "--access",
                        "public",
                        "--ignore-scripts",
                        "--provenance-file",
                        str(bundle),
                    ],
                    cwd=cwd,
                    check=True,
                    env=clean_env,
                )
    elif unstable:
        tag = "staged-" + "-".join(filter(None, unstable.groups()))
        with tempfile.TemporaryDirectory() as cwd:
            for filename in before["missing"]:
                try:
                    run(
                        [
                            "npm",
                            "publish",
                            str((directory / filename).resolve()),
                            "--registry",
                            REGISTRY,
                            "--tag",
                            tag,
                            "--access",
                            "public",
                            "--ignore-scripts",
                        ],
                        cwd=cwd,
                        check=True,
                    )
                except subprocess.CalledProcessError:
                    if filename not in reconcile(directory, version, fetch)["present"]:
                        raise
    after = reconcile(directory, version, fetch)
    if after["missing"]:
        raise ValueError(
            f"npm has not confirmed all prepared archives: {', '.join(after['missing'])}"
        )
    return {
        "reused": before["present"],
        "uploaded": before["missing"],
        "verified": after["present"],
    }


def _parser():
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=Path)
    parser.add_argument("version")
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--upload-missing", action="store_true")
    modes.add_argument("--verify-provenance", action="store_true")
    parser.add_argument("--source-repository", dest="repository")
    parser.add_argument("--source-sha")
    parser.add_argument("--source-ref")
    parser.add_argument("--source-run")
    parser.add_argument("--producing-attempt")
    parser.add_argument("--index-id")
    parser.add_argument("--index-archive-digest")
    parser.add_argument("--provenance-dir", type=Path)
    return parser


if __name__ == "__main__":
    args = _parser().parse_args()
    identity = (
        args.repository,
        args.source_sha,
        args.source_ref,
        args.source_run,
        args.producing_attempt,
        args.index_id,
        args.index_archive_digest,
        args.provenance_dir,
    )
    try:
        if args.verify_provenance or args.upload_missing:
            provided = any(value is not None for value in identity)
            if (args.verify_provenance or provided) and any(
                value is None for value in identity
            ):
                sys.exit("all approved provenance inputs are required")
            if args.verify_provenance:
                print(
                    json.dumps(
                        {
                            "verified": verify_provenance(
                                args.directory, args.version, args
                            )
                        }
                    )
                )
            else:
                print(
                    json.dumps(
                        upload_missing(
                            args.directory,
                            args.version,
                            provenance=args if provided else None,
                        )
                    )
                )
        else:
            if any(value is not None for value in identity):
                sys.exit(
                    "approved provenance inputs require --verify-provenance or --upload-missing"
                )
            print(json.dumps(reconcile(args.directory, args.version)))
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
