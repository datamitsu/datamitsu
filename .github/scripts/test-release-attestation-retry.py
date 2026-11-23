import base64
import hashlib
import io
import json
import runpy
import subprocess
import sys
import tempfile
import unittest
from contextlib import nullcontext
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch
from zipfile import ZipFile

# cspell:ignore dsse

SCRIPTS = Path(__file__).resolve().parent
FETCH = runpy.run_path(str(SCRIPTS / "fetch-npm-provenance.py"))["fetch"]
NPM = runpy.run_path(str(SCRIPTS / "check-npm-release.py"))
REPO = "datamitsu/datamitsu"
REPO_URL = f"https://github.com/{REPO}"
TAG = "v1.2.3-rc.1"
REF = f"refs/tags/{TAG}"
SHA = "a" * 40
RUN = "100"
INDEX = "42"


def archive(name, value):
    output = io.BytesIO()
    with ZipFile(output, "w") as bundle:
        bundle.writestr(name, json.dumps(value))
    return output.getvalue()


def digest(raw):
    return "sha256:" + hashlib.sha256(raw).hexdigest()


class AttestationRetryTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.npm = self.root / "npm"
        self.npm.mkdir()
        self.proofs = self.root / "proofs"
        self.payloads = {
            "release": {"id": "1", "archive_digest": "sha256:" + "b" * 64},
            "vsix": {"id": "2", "archive_digest": "sha256:" + "c" * 64},
        }
        self.index = archive(
            "release-artifact-index.json",
            {
                "run_id": RUN,
                "mode": "tag",
                "release_tag": TAG,
                "version": TAG[1:],
                "github_release": True,
                "source_sha": SHA,
                "preparation_attempt": "1",
                "producing_attempt": "1",
                "artifacts": self.payloads,
            },
        )
        self.metadata = []
        self.bundles = {}
        for id_, name in enumerate(NPM["archive_names"](), start=100):
            package = self.npm / name
            data = name.encode()
            package.write_bytes(data)
            attempt = "2" if name == "datamitsu.tgz" else "1"
            statement = {
                "_type": "https://in-toto.io/Statement/v1",
                "predicateType": "https://slsa.dev/provenance/v1",
                "subject": [
                    {
                        "name": f"pkg:npm/%40datamitsu/{package.stem}@{TAG[1:]}",
                        "digest": {"sha512": hashlib.sha512(data).hexdigest()},
                    }
                ],
                "predicate": {
                    "buildDefinition": {
                        "buildType": "https://slsa-framework.github.io/github-actions-buildtypes/workflow/v1",
                        "externalParameters": {
                            "workflow": {
                                "repository": REPO_URL,
                                "ref": REF,
                                "path": ".github/workflows/release.yml",
                            },
                            "preparation": {
                                "index_id": INDEX,
                                "index_archive_digest": digest(self.index),
                            },
                        },
                        "resolvedDependencies": [
                            {
                                "uri": f"git+{REPO_URL}@{REF}",
                                "digest": {"gitCommit": SHA},
                            }
                        ],
                    },
                    "runDetails": {
                        "metadata": {
                            "invocationId": f"{REPO_URL}/actions/runs/{RUN}/attempts/{attempt}"
                        }
                    },
                },
            }
            raw = archive(
                f"{package.stem}.sigstore.json",
                {
                    "dsseEnvelope": {
                        "payloadType": "application/vnd.in-toto+json",
                        "payload": base64.b64encode(
                            json.dumps(statement).encode()
                        ).decode(),
                    }
                },
            )
            self.bundles[id_] = raw
            self.metadata.append(
                {
                    "id": id_,
                    "name": f"npm-provenance-{INDEX}-{attempt}-{package.stem}",
                    "expired": False,
                    "digest": digest(raw),
                    "size_in_bytes": len(raw),
                }
            )

    def validate_index(self, build_conclusion):
        def write(name, data):
            path = self.root / name
            path.write_text(json.dumps(data))
            return str(path)

        index = self.root / "index.zip"
        index.write_bytes(self.index)
        run = write(
            "run.json",
            {
                "id": int(RUN),
                "path": ".github/workflows/release.yml",
                "head_sha": SHA,
                "event": "push",
                "run_attempt": 1,
                "status": "completed",
                "conclusion": "failure",
            },
        )
        jobs = write(
            "jobs.json",
            [
                {
                    "jobs": [
                        {
                            "name": "Prepare release artifacts",
                            "run_id": int(RUN),
                            "head_sha": SHA,
                            "run_attempt": 1,
                            "status": "completed",
                            "conclusion": build_conclusion,
                        }
                    ]
                }
            ],
        )
        artifacts = write(
            "artifacts.json",
            {
                "artifacts": [
                    {
                        "name": "release-artifact-index-attempt-1",
                        "id": int(INDEX),
                        "digest": digest(self.index),
                        "expired": False,
                    },
                    *[
                        {
                            "name": f"{prefix}-attempt-1",
                            "id": int(self.payloads[kind]["id"]),
                            "digest": self.payloads[kind]["archive_digest"],
                            "expired": False,
                        }
                        for kind, prefix in (
                            ("release", "release-artifacts"),
                            ("vsix", "vsix"),
                        )
                    ],
                ]
            },
        )
        return subprocess.run(
            [
                sys.executable,
                str(SCRIPTS / "verify-release-index.py"),
                "--run-id",
                RUN,
                "--index-id",
                INDEX,
                "--archive-digest",
                digest(self.index),
                "--trigger-tag",
                TAG,
                "--archive",
                str(index),
                "--run-metadata",
                run,
                "--job-metadata",
                jobs,
                "--artifact-metadata",
                artifacts,
            ],
            check=False,
            capture_output=True,
            text=True,
        )

    def fetch_proofs(self):
        def open_bundle(args, **_kwargs):
            id_ = int(args[-1].split("/")[-2])
            process = SimpleNamespace(
                stdout=io.BytesIO(self.bundles[id_]), wait=lambda: 0
            )
            return nullcontext(process)

        with (
            patch.object(
                subprocess,
                "check_output",
                return_value=json.dumps([{"artifacts": self.metadata}]).encode(),
            ),
            patch.object(subprocess, "Popen", side_effect=open_bundle),
        ):
            FETCH(self.npm, self.proofs, RUN, "1", INDEX)

    def validate_proofs(self, *, index_id=INDEX):
        options = SimpleNamespace(
            repository=REPO,
            source_sha=SHA,
            source_ref=REF,
            source_run=RUN,
            producing_attempt="1",
            index_id=index_id,
            index_archive_digest=digest(self.index),
            provenance_dir=self.proofs,
        )
        return NPM["verify_provenance"](
            self.npm, TAG[1:], options, run=lambda *_args, **_kwargs: None
        )

    def test_failed_signing_attempt_reuses_successful_build_and_eight_proofs(self):
        result = self.validate_index("success")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["artifacts"], self.payloads)
        self.fetch_proofs()
        self.assertEqual(self.validate_proofs(), NPM["archive_names"]())

    def test_failed_build_cannot_use_its_uploaded_index(self):
        result = self.validate_index("failure")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("release build did not succeed", result.stderr)

    def test_same_bytes_on_another_index_are_not_accepted(self):
        self.fetch_proofs()
        with self.assertRaisesRegex(ValueError, "preparation index does not match"):
            self.validate_proofs(index_id="43")

    def test_duplicate_bundle_is_not_resolved_by_picking_the_latest(self):
        self.metadata.append(
            {**self.metadata[-1], "name": f"npm-provenance-{INDEX}-3-datamitsu"}
        )
        with self.assertRaisesRegex(ValueError, "ambiguous npm provenance"):
            self.fetch_proofs()

    def test_signing_attempt_must_match_artifact_label(self):
        self.metadata[-1]["name"] = f"npm-provenance-{INDEX}-1-datamitsu"
        self.fetch_proofs()
        with self.assertRaisesRegex(
            ValueError, "provenance run or attempt does not match"
        ):
            self.validate_proofs()


if __name__ == "__main__":
    unittest.main()
