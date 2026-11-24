import hashlib
import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

# cspell:ignore Ftags

SCRIPT = Path(__file__).with_name("publish-prerelease.py")
SPEC = importlib.util.spec_from_file_location("publish_prerelease", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise ImportError(f"cannot load {SCRIPT.name}")
PUBLISH = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PUBLISH)


class Result:
    def __init__(self, stdout=""):
        self.stdout = stdout


class PublishTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.version = "0.0.0-unstable.12345678901.2"
        self.tag = "unstable-12345678901-2"
        self.sha = "a" * 40
        names = [
            f"datamitsu_{self.version}_{os}_{arch}.tar.gz"
            for arch in ("amd64", "arm64")
            for os in ("linux", "darwin", "freebsd")
        ]
        names += [
            f"datamitsu_{self.version}_windows_{arch}.zip"
            for arch in ("amd64", "arm64")
        ]
        names += [
            "checksums.txt",
            "checksums.txt.sigstore.json",
            f"datamitsu_parsers_{self.version}.wasm",
            "parsers-oci.json",
        ]
        for name in names:
            (self.directory / name).write_bytes(("contents:" + name).encode())
        (self.directory / "checksums.txt").write_text(
            "".join(
                f"{hashlib.sha256((self.directory / name).read_bytes()).hexdigest()}  {name}\n"
                for name in names
                if name.endswith((".tar.gz", ".zip", ".wasm"))
            )
        )
        self.assets = PUBLISH.inventory(self.directory, self.version)
        self.record = {
            "id": 42,
            "tag_name": self.tag,
            "target_commitish": self.sha,
            "draft": True,
            "prerelease": True,
            "assets": [],
        }
        self.exists = True
        self.uploaded = set()
        self.calls = []

    def fake_cli(self, args):
        self.calls.append(args)
        if args[:4] == ["gh", "api", "-X", "POST"]:
            name = args[4].split("?name=", 1)[1]
            self.assertEqual(
                args[4],
                f"https://uploads.github.com/repos/{PUBLISH.REPOSITORY}/releases/42/assets?name={name}",
            )
            self.assertNotIn(name, self.uploaded)
            self.assertEqual(
                hashlib.sha256(Path(args[-1]).read_bytes()).hexdigest(),
                self.assets[name],
            )
            self.uploaded.add(name)
            return Result()
        if args[:2] == ["gh", "api"]:
            if "/commits/refs%2Ftags%2F" in args[2]:
                self.assertEqual(
                    args[2],
                    f"repos/{PUBLISH.REPOSITORY}/commits/refs%2Ftags%2F{self.tag}",
                )
                return Result(json.dumps({"sha": getattr(self, "tag_sha", self.sha)}))
            if "/git/matching-refs/" in args[2]:
                refs = (
                    []
                    if self.record["draft"]
                    else [
                        {
                            "ref": f"refs/tags/{self.tag}",
                            "object": {"type": "commit", "sha": self.sha},
                        }
                    ]
                )
                return Result(json.dumps(refs))
            self.record["assets"] = [
                {"name": n, "digest": "sha256:" + d}
                for n, d in self.assets.items()
                if n in self.uploaded
            ]
            if args[2] == "--paginate":
                return Result(json.dumps([[self.record] if self.exists else []]))
            endpoint = f"repos/{PUBLISH.REPOSITORY}/releases/42"
            if args[2:5] == ["-X", "PATCH", endpoint]:
                self.assertIn("make_latest=false", args)
                self.assertIn(
                    f"prerelease={str(self.record['prerelease']).lower()}", args
                )
                self.record["draft"] = False
            else:
                self.assertEqual(args[2], endpoint)
            return Result(json.dumps(self.record))
        if args[:3] == ["gh", "release", "create"]:
            self.assertFalse(self.exists)
            self.assertIn("--draft", args)
            self.assertEqual("--prerelease" in args, self.record["prerelease"])
            self.assertEqual("--verify-tag" in args, self.tag.startswith("v"))
            self.assertEqual(args[args.index("--target") + 1], self.sha)
            self.exists = True
            return Result()
        raise AssertionError("unexpected API/CLI call: " + repr(args))

    def test_uploads_missing_then_verifies_and_publishes(self):
        result = PUBLISH.publish(
            self.directory,
            self.version,
            self.tag,
            self.sha,
            repository=PUBLISH.REPOSITORY,
            run=self.fake_cli,
        )
        self.assertEqual(set(result["assets"]), set(self.assets))
        uploads = [
            call for call in self.calls if call[:4] == ["gh", "api", "-X", "POST"]
        ]
        self.assertEqual(len(uploads), len(self.assets))
        self.assertTrue(all("--clobber" not in call for call in uploads))
        self.assertFalse(self.record["draft"])

    def test_retry_skips_matching_existing_assets(self):
        self.uploaded = set(sorted(self.assets)[:3])
        PUBLISH.publish(
            self.directory,
            self.version,
            self.tag,
            self.sha,
            repository=PUBLISH.REPOSITORY,
            run=self.fake_cli,
        )
        uploads = [
            call for call in self.calls if call[:4] == ["gh", "api", "-X", "POST"]
        ]
        self.assertEqual(len(uploads), len(self.assets) - 3)

    def test_creates_missing_draft_and_reuses_published_release(self):
        self.exists = False
        PUBLISH.publish(
            self.directory,
            self.version,
            self.tag,
            self.sha,
            repository=PUBLISH.REPOSITORY,
            run=self.fake_cli,
        )
        self.assertTrue(self.exists)
        self.assertFalse(self.record["draft"])
        self.calls.clear()
        PUBLISH.publish(
            self.directory,
            self.version,
            self.tag,
            self.sha,
            repository=PUBLISH.REPOSITORY,
            run=self.fake_cli,
        )
        self.assertTrue(
            all(call[:2] == ["gh", "api"] and "-X" not in call for call in self.calls)
        )

    def test_preflight_rejects_missing_windows_archive(self):
        names = [
            name for name in self.assets if name.endswith((".tar.gz", ".zip", ".wasm"))
        ]
        (self.directory / "checksums.txt").write_text(
            "".join(f"{self.assets[name]}  {name}\n" for name in names)
        )
        (self.directory / "checksums.txt.sigstore.json").unlink()
        PUBLISH.inventory(self.directory, self.version, unsigned=True)
        (self.directory / f"datamitsu_{self.version}_windows_arm64.zip").unlink()
        with self.assertRaisesRegex(ValueError, "missing one or more"):
            PUBLISH.inventory(self.directory, self.version, unsigned=True)

    def test_tagged_inventory_verifies_standalone_executables(self):
        for version in ("1.2.3", "1.2.3-rc.2"):
            with self.subTest(version=version):
                binaries = self.prepare_tagged_assets(version)
                (binaries / "datamitsu-windows_arm64.exe").write_bytes(b"changed")
                with self.assertRaisesRegex(ValueError, "windows_arm64\\.exe"):
                    PUBLISH.inventory(self.directory, version, unsigned=True)

    def test_tagged_preflight_requires_vsix_without_a_signature(self):
        version = "1.2.3-rc.2"
        self.prepare_tagged_assets(version)
        (self.directory / "checksums.txt.sigstore.json").unlink()
        command = [
            sys.executable,
            "-B",
            str(SCRIPT),
            "--check-only",
            str(self.directory),
            version,
        ]
        subprocess.run(command, check=True, capture_output=True)
        (self.directory / f"datamitsu-{version}.vsix").unlink()
        result = subprocess.run(command, check=False, capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn("missing one or more required release assets", result.stderr)

    def prepare_tagged_assets(self, version):
        binaries = self.directory / "binaries"
        binaries.mkdir(exist_ok=True)
        rows = []
        expected = {}
        for platform in ("linux", "darwin", "freebsd", "windows"):
            for arch in ("amd64", "arm64"):
                stem = f"datamitsu_{version}_{platform}_{arch}"
                suffix = ".exe" if platform == "windows" else ""
                files = [
                    (stem + suffix, binaries / f"datamitsu-{platform}_{arch}{suffix}")
                ]
                expected[stem + suffix] = hashlib.sha256(
                    (stem + suffix).encode()
                ).hexdigest()
                extensions = [".zip" if suffix else ".tar.gz", ".sbom"]
                if platform == "linux":
                    extensions += [".deb", ".rpm", ".apk"]
                files += [
                    (stem + ext, self.directory / (stem + ext)) for ext in extensions
                ]
                for name, path in files:
                    path.write_bytes(name.encode())
                    rows.append(
                        f"{hashlib.sha256(name.encode()).hexdigest()}  {name}\n"
                    )
        wasm = f"datamitsu_parsers_{version}.wasm"
        (self.directory / wasm).write_bytes(b"wasm")
        rows.append(f"{hashlib.sha256(b'wasm').hexdigest()}  {wasm}\n")
        (self.directory / f"datamitsu-{version}.vsix").write_bytes(b"extension")
        (self.directory / "checksums.txt").write_text("".join(rows))
        actual = PUBLISH.inventory(self.directory, version, unsigned=True)
        self.assertEqual({name: actual.get(name) for name in expected}, expected)
        upload_name = f"datamitsu_{version}_windows_arm64.exe"
        source, digest = PUBLISH.asset_plan(self.directory, version, unsigned=True)[
            upload_name
        ]
        self.assertEqual(digest, expected[upload_name])
        calls = []
        PUBLISH.upload_asset(PUBLISH.REPOSITORY, 42, upload_name, source, calls.append)
        self.assertEqual(
            calls,
            [
                [
                    "gh",
                    "api",
                    "-X",
                    "POST",
                    f"https://uploads.github.com/repos/{PUBLISH.REPOSITORY}/releases/42/assets?name={upload_name}",
                    "-H",
                    "Content-Type: application/octet-stream",
                    "--input",
                    str(binaries / "datamitsu-windows_arm64.exe"),
                ]
            ],
        )
        return binaries

    def test_tagged_recovery(self):
        for version in ("1.2.3", "1.2.3-rc.2"):
            with self.subTest(version=version):
                self.prepare_tagged_assets(version)
                self.tag = f"v{version}"
                self.assets = PUBLISH.inventory(self.directory, version)
                self.record.update(
                    tag_name=self.tag, prerelease="-rc." in version, draft=True
                )
                self.exists = False
                self.uploaded.clear()

                def interrupt(args):
                    if args[:4] == ["gh", "api", "-X", "POST"] and self.uploaded:
                        raise RuntimeError("interrupted")
                    return self.fake_cli(args)

                with self.assertRaisesRegex(RuntimeError, "interrupted"):
                    PUBLISH.publish(
                        self.directory,
                        version,
                        self.tag,
                        self.sha,
                        PUBLISH.REPOSITORY,
                        interrupt,
                    )
                self.assertEqual(len(self.uploaded), 1)
                PUBLISH.publish(
                    self.directory,
                    version,
                    self.tag,
                    self.sha,
                    PUBLISH.REPOSITORY,
                    self.fake_cli,
                )
                self.assertEqual(self.uploaded, self.assets.keys())
                self.assertFalse(self.record["draft"])
                self.calls.clear()
                PUBLISH.publish(
                    self.directory,
                    version,
                    self.tag,
                    self.sha,
                    PUBLISH.REPOSITORY,
                    self.fake_cli,
                )
                self.assertTrue(
                    all(
                        call[:2] == ["gh", "api"] and "-X" not in call
                        for call in self.calls
                    )
                )

    def test_tagged_rejects_moved_tag_and_conflicting_digest(self):
        version = "1.2.3"
        self.prepare_tagged_assets(version)
        self.tag = f"v{version}"
        self.assets = PUBLISH.inventory(self.directory, version)
        self.record.update(tag_name=self.tag, prerelease=False)
        self.tag_sha = "b" * 40
        with self.assertRaisesRegex(ValueError, "approved source commit"):
            PUBLISH.publish(
                self.directory,
                version,
                self.tag,
                self.sha,
                PUBLISH.REPOSITORY,
                self.fake_cli,
            )
        self.tag_sha = self.sha
        name = f"datamitsu-{version}.vsix"
        self.uploaded.add(name)
        (self.directory / name).write_bytes(b"changed")
        with self.assertRaisesRegex(ValueError, "existing GitHub asset differs"):
            PUBLISH.publish(
                self.directory,
                version,
                self.tag,
                self.sha,
                PUBLISH.REPOSITORY,
                self.fake_cli,
            )
        self.assertTrue(
            all(call[:2] == ["gh", "api"] and "-X" not in call for call in self.calls)
        )

    def test_wrong_target_fails_before_writes(self):
        self.record["target_commitish"] = "b" * 40
        with self.assertRaises(ValueError):
            PUBLISH.publish(
                self.directory,
                self.version,
                self.tag,
                self.sha,
                repository=PUBLISH.REPOSITORY,
                run=self.fake_cli,
            )
        self.assertTrue(
            all(call[:2] == ["gh", "api"] and "-X" not in call for call in self.calls)
        )

    def test_stops_uploading_if_draft_becomes_public(self):
        def publish_after_first_upload(args):
            result = self.fake_cli(args)
            if args[:4] == ["gh", "api", "-X", "POST"]:
                self.record["draft"] = False
            return result

        with self.assertRaisesRegex(ValueError, "draft identity changed"):
            PUBLISH.publish(
                self.directory,
                self.version,
                self.tag,
                self.sha,
                repository=PUBLISH.REPOSITORY,
                run=publish_after_first_upload,
            )
        writes = [call for call in self.calls if "-X" in call]
        self.assertEqual(len(writes), 1)
        self.assertEqual(writes[0][:4], ["gh", "api", "-X", "POST"])

    def test_refuses_stale_checksums_before_api_calls(self):
        (self.directory / "checksums.txt").write_text("stale\n")
        with self.assertRaisesRegex(ValueError, "checksum"):
            PUBLISH.publish(
                self.directory,
                self.version,
                self.tag,
                self.sha,
                repository=PUBLISH.REPOSITORY,
                run=self.fake_cli,
            )
        self.assertEqual(self.calls, [])

    def test_unknown_api_error_fails_closed(self):
        def fail(args):
            raise RuntimeError("unexpected API failure")

        with self.assertRaises(RuntimeError):
            PUBLISH.publish(
                self.directory,
                self.version,
                self.tag,
                self.sha,
                repository=PUBLISH.REPOSITORY,
                run=fail,
            )

    def test_wrong_tag_and_repository_are_rejected(self):
        with self.assertRaises(ValueError):
            PUBLISH.publish(
                self.directory,
                self.version,
                "unstable-12345678901-3",
                self.sha,
                repository=PUBLISH.REPOSITORY,
                run=self.fake_cli,
            )
        with self.assertRaises(ValueError):
            PUBLISH.publish(
                self.directory,
                self.version,
                self.tag,
                self.sha,
                repository="attacker/repo",
                run=self.fake_cli,
            )


if __name__ == "__main__":
    unittest.main()
