import argparse
import hashlib
import json
import re
from pathlib import Path
from zipfile import ZipFile


def require(condition, message):
    if not condition:
        raise ValueError(message)


def main():
    parser = argparse.ArgumentParser(
        description="Validate release preparation evidence without publishing"
    )
    for name in ("run-id", "index-id", "archive-digest"):
        parser.add_argument(f"--{name}", required=True)
    parser.add_argument(
        "--trigger-tag",
        help="Tag from independently verified push evidence; required for tag runs",
    )
    parser.add_argument(
        "--publish-mode",
        choices=("prerelease", "unstable"),
        help="Independently authorized nonstable publication mode",
    )
    parser.add_argument(
        "--trusted-dispatch-sha",
        help="Automatic handoff workflow revision verified against the protected default branch",
    )
    parser.add_argument(
        "--run-metadata",
        type=Path,
        required=True,
        help="GitHub GET /actions/runs/{id}/attempts/{n} response for the producing attempt",
    )
    for name in ("archive", "artifact-metadata", "job-metadata"):
        parser.add_argument(f"--{name}", type=Path, required=True)
    args = parser.parse_args()
    for value in (args.run_id, args.index_id):
        require(re.fullmatch(r"[1-9][0-9]*", value), "invalid run or artifact ID")
    require(
        re.fullmatch(r"sha256:[0-9a-f]{64}", args.archive_digest),
        "invalid archive digest",
    )
    run = json.loads(args.run_metadata.read_text())
    require(str(run["id"]) == args.run_id, "wrong preparation run")
    require(
        run["path"] == ".github/workflows/release.yml",
        "unexpected preparation workflow",
    )
    artifacts = {}
    for artifact in json.loads(args.artifact_metadata.read_text())["artifacts"]:
        artifact_id = str(artifact["id"])
        require(artifact_id not in artifacts, "duplicate artifact ID in metadata")
        artifacts[artifact_id] = artifact

    def check_artifact(artifact_id, digest, name):
        artifact = artifacts.get(artifact_id)
        if artifact is None:
            raise ValueError(f"{name} (artifact {artifact_id}) is absent from the run")
        require(artifact["name"] == name, f"artifact {artifact_id} is not {name}")
        require(
            artifact["expired"] is False, f"{name} (artifact {artifact_id}) is expired"
        )
        workflow_run = artifact.get("workflow_run")
        if workflow_run is not None:
            require(
                str(workflow_run["id"]) == args.run_id,
                "artifact belongs to another run",
            )
        require(
            artifact["digest"] == digest,
            f"{name} (artifact {artifact_id}) digest mismatch",
        )

    with args.archive.open("rb") as archive:
        digest = "sha256:" + hashlib.file_digest(archive, "sha256").hexdigest()
        require(digest == args.archive_digest, "index ZIP digest mismatch")
        archive.seek(0)
        with ZipFile(archive) as bundle:
            entries = bundle.infolist()
            require(
                len(entries) == 1
                and entries[0].filename == "release-artifact-index.json",
                "unexpected index ZIP entries",
            )
            require(entries[0].file_size <= 65536, "index JSON is too large")
            index = json.loads(bundle.read(entries[0]))
    require(
        index["run_id"] == args.run_id,
        "index does not match run identity",
    )
    mode = index["mode"]
    require(
        (mode, run["event"])
        in {
            ("tag", "push"),
            ("prerelease", "workflow_dispatch"),
            ("unstable", "workflow_dispatch"),
        },
        "index is not a publishing preparation",
    )
    producing = index["producing_attempt"]
    preparation = index["preparation_attempt"]
    for value in (producing, preparation):
        require(re.fullmatch(r"[1-9][0-9]*", value), "invalid preparation attempt")
    require(
        int(preparation) <= int(producing),
        "preparation attempt follows producing attempt",
    )
    require(
        run["run_attempt"] == int(producing),
        "run metadata is not for the producing attempt",
    )
    require(run["status"] == "completed", "producing attempt is not complete")
    source_sha = index["source_sha"]
    require(re.fullmatch(r"[0-9a-f]{40}", source_sha), "invalid product SHA")
    tag = index["release_tag"]
    github_release = index["github_release"]
    require(
        type(github_release) is bool
        and github_release == (mode in {"tag", "prerelease"}),
        "GitHub destination does not match the preparation mode",
    )
    build_name = "Prepare release artifacts"
    if mode == "unstable" or args.trusted_dispatch_sha is not None:
        require(mode != "tag", "tag preparation cannot use a dispatch workflow")
        require(
            args.trusted_dispatch_sha is not None
            and index.get("workflow_sha")
            == run["head_sha"]
            == args.trusted_dispatch_sha,
            "index does not match the trusted dispatch SHA",
        )
        require(
            preparation == producing == "1" and run["conclusion"] == "success",
            "automatic preparation requires the first successful preparation attempt",
        )
        if mode == "unstable":
            # GitHub assigns this job name before starting product-source steps.
            build_name = f"Prepare unstable release artifacts [{source_sha}]"
        else:
            build_name = "Prepare prerelease for publication"
    if mode != "unstable":
        require(source_sha == run["head_sha"], "index does not match run identity")
    builds = [
        job
        for page in json.loads(args.job_metadata.read_text())
        for job in page["jobs"]
        if job["name"]
        in {"Prepare release artifacts", "Prepare prerelease for publication"}
        or job["name"].startswith("Prepare unstable release artifacts [")
    ]
    require(
        len(builds) == 1 and builds[0]["name"] == build_name,
        "missing, ambiguous, or mismatched release build in the producing attempt",
    )
    build = builds[0]
    require(
        str(build["run_id"]) == args.run_id
        and build["head_sha"] == run["head_sha"]
        and build.get("run_attempt", int(producing)) == int(producing)
        and build["status"] == "completed"
        and build["conclusion"] == "success",
        "release build did not succeed for this index's producing attempt",
    )
    if mode == "tag":
        require(
            args.publish_mode is None,
            "unexpected manual authorization for a tag run",
        )
        number = r"(?:0|[1-9][0-9]*)"
        require(
            re.fullmatch(rf"v{number}\.{number}\.{number}(?:-rc\.{number})?", tag),
            "invalid release tag",
        )
        require(
            tag == args.trigger_tag,
            "release tag does not match verified trigger evidence",
        )
        require(
            index["version"] == tag[1:],
            "inconsistent tagged release route",
        )
    else:
        require(args.trigger_tag is None, "unexpected trigger tag for a manual run")
        require(
            args.publish_mode == mode,
            "nonstable publication route does not match the index mode",
        )
        suffix = f"{args.run_id}-{preparation}"
        kind = "unstable" if mode == "unstable" else "preview"
        require(
            tag == f"unstable-{suffix}"
            or re.fullmatch(rf"{kind}-[1-9][0-9]{{11}}-{suffix}", tag),
            "incorrect manual release tag for mode",
        )
        prefix, identity = tag.split("-", 1)
        require(
            index["version"] == f"0.0.0-{prefix}." + identity.replace("-", "."),
            "incorrect manual release version",
        )
    check_artifact(args.index_id, digest, f"release-artifact-index-attempt-{producing}")
    require(
        set(index["artifacts"]) == {"release", "vsix"},
        "unexpected preparation artifact set",
    )
    for kind, prefix in (("release", "release-artifacts"), ("vsix", "vsix")):
        entry = index["artifacts"][kind]
        require(
            re.fullmatch(r"[1-9][0-9]*", entry["id"]), "invalid preparation artifact ID"
        )
        require(
            re.fullmatch(r"sha256:[0-9a-f]{64}", entry["archive_digest"]),
            "invalid preparation artifact digest",
        )
        check_artifact(
            entry["id"], entry["archive_digest"], f"{prefix}-attempt-{producing}"
        )
    print(json.dumps(index, sort_keys=True))


if __name__ == "__main__":
    main()
