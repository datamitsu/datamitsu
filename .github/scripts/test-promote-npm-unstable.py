import importlib.util
import io
import tempfile
import unittest
from pathlib import Path
from urllib.parse import unquote

SCRIPT = Path(__file__).with_name("promote-npm-unstable.py")
SPEC = importlib.util.spec_from_file_location("promote_npm_unstable", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise ImportError(f"cannot load {SCRIPT.name}")
PROMOTE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROMOTE)


class Response(io.BytesIO):
    pass


class PromotionTest(unittest.TestCase):
    def test_cutover_requires_exact_baseline_and_never_rolls_back(self):
        for scenario in (
            "newer",
            "unpinned",
            "pinned",
            "drifted",
            "interrupted",
            "after-cutover",
        ):
            with self.subTest(scenario=scenario):
                self.check_promotion(scenario)

    def check_promotion(self, scenario):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            names = [
                f"datamitsu-{os}-{arch}.tgz"
                for os in ("darwin", "freebsd", "linux", "win32")
                for arch in ("arm64", "x64")
            ] + ["datamitsu.tgz"]
            import base64
            import hashlib

            for filename in names:
                (directory / filename).write_bytes(filename.encode())
            requests = []
            target = "0.0.0-unstable.12345678901.1"
            legacy = "0.0.0-unstable.20261121.3f08339"
            launcher = "@datamitsu/datamitsu"
            tags = {f"@datamitsu/{Path(filename).stem}": target for filename in names}
            tags[launcher] = (
                "0.0.0-unstable.12345678901.2" if scenario == "newer" else legacy
            )
            platform = max(tags)
            if scenario == "drifted":
                tags[launcher], tags[platform] = target, legacy
                tags[sorted(tags)[1]] = "0.0.0-unstable.12345678900.1"
            elif scenario == "interrupted":
                tags[platform] = legacy
            elif scenario == "after-cutover":
                tags[launcher] = "0.0.0-unstable.12345678900.2"
            baseline = {name: None for name in tags}
            for name, current in tags.items():
                if current == legacy:
                    baseline[name] = (
                        legacy
                        if scenario != "drifted"
                        else "0.0.0-unstable.20261121.aaaaaaa"
                    )
            if scenario == "after-cutover":
                baseline[launcher] = legacy

            def fetch(url, timeout):
                package, version = url.rsplit("/", 2)[-2:]
                if version == "dist-tags":
                    return Response(
                        f'{{"unstable":"{tags[unquote(package)]}"}}'.encode()
                    )
                name = unquote(package).rsplit("/", 1)[-1]
                archive = directory / f"{name}.tgz"
                integrity = (
                    "sha512-"
                    + base64.b64encode(
                        hashlib.sha512(archive.read_bytes()).digest()
                    ).decode()
                )
                return Response(
                    (
                        f'{{"name":"@datamitsu/{name}","version":"{version}","dist":{{"integrity":"{integrity}"}}}}'
                    ).encode()
                )

            def run(args, **kwargs):
                requests.append(args)
                name, version = args[3].rsplit("@", 1)
                tags[name] = version
                if scenario == "interrupted" and len(requests) == 1:
                    raise RuntimeError("response lost")

            selected = (
                baseline
                if scenario in ("pinned", "drifted", "interrupted", "after-cutover")
                else None
            )
            if scenario == "interrupted":
                with self.assertRaisesRegex(RuntimeError, "response lost"):
                    PROMOTE.promote(directory, target, fetch, run, baseline=selected)
                PROMOTE.promote(directory, target, fetch, run, baseline=selected)
                self.assertTrue(all(current == target for current in tags.values()))
                self.assertEqual(
                    [command[3] for command in requests],
                    [f"{platform}@{target}", f"{launcher}@{target}"],
                )
                return
            if scenario in ("pinned", "after-cutover"):
                PROMOTE.promote(directory, target, fetch, run, baseline=selected)
                self.assertEqual(tags[launcher], target)
            else:
                with self.assertRaisesRegex(
                    ValueError,
                    "backward" if scenario == "newer" else "pinned cutover baseline",
                ):
                    PROMOTE.promote(directory, target, fetch, run, baseline=selected)
            self.assertEqual(
                sum(command[1] == "dist-tag" for command in requests),
                int(scenario in ("pinned", "after-cutover")),
            )


if __name__ == "__main__":
    unittest.main()
