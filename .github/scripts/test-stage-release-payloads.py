import hashlib
import runpy
import tempfile
import unittest
from pathlib import Path
from zipfile import ZipFile

VERIFY = runpy.run_path(str(Path(__file__).with_name("stage-release-payloads.py")))[
    "verify_tagged_wheels"
]


class TaggedWheelTest(unittest.TestCase):
    def test_rejects_wrong_platform_binary_with_updated_archive_checksum(self):
        targets = {
            "manylinux2014_x86_64": ("linux", "x86_64", "amd64"),
            "manylinux2014_aarch64": ("linux", "arm64", "arm64"),
            "macosx_13_0_x86_64": ("darwin", "x86_64", "amd64"),
            "macosx_13_0_arm64": ("darwin", "arm64", "arm64"),
            "win_amd64": ("windows", "x86_64", "amd64"),
            "win_arm64": ("windows", "arm64", "arm64"),
        }
        for version, native_version in (("1.2.3", "1.2.3"), ("1.2.3-rc.2", "1.2.3rc2")):
            with (
                self.subTest(version=version),
                tempfile.TemporaryDirectory() as directory,
            ):
                dist = Path(directory)
                wheels = dist / "python"
                wheels.mkdir()
                binaries = dist / "binaries"
                binaries.mkdir()
                for tag, (platform, python_arch, arch) in targets.items():
                    suffix = ".exe" if platform == "windows" else ""
                    content = f"{platform}/{arch}".encode()
                    (binaries / f"datamitsu-{platform}_{arch}{suffix}").write_bytes(
                        content
                    )
                    wheel = wheels / f"datamitsu-{native_version}-py3-none-{tag}.whl"
                    with ZipFile(wheel, "w") as package:
                        package.writestr(
                            f"datamitsu-{native_version}.dist-info/METADATA",
                            f"Name: datamitsu\nVersion: {native_version}\n",
                        )
                        package.writestr(
                            f"datamitsu/bin/datamitsu-{platform}-{python_arch}/datamitsu{suffix}",
                            content,
                        )

                def checksum_wheels(directory):
                    rows = [
                        f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
                        for path in directory.glob("*.whl")
                    ]
                    (directory / "SHA256SUMS").write_text("".join(rows))

                checksum_wheels(wheels)
                VERIFY(dist, version)
                wheel = wheels / f"datamitsu-{native_version}-py3-none-win_arm64.whl"
                with ZipFile(wheel, "w") as package:
                    package.writestr(
                        f"datamitsu-{native_version}.dist-info/METADATA",
                        f"Name: datamitsu\nVersion: {native_version}\n",
                    )
                    package.writestr(
                        "datamitsu/bin/datamitsu-windows-arm64/datamitsu.exe",
                        b"windows/amd64",
                    )
                checksum_wheels(wheels)
                with self.assertRaisesRegex(
                    ValueError, "wheel executable differs.*win_arm64"
                ):
                    VERIFY(dist, version)


if __name__ == "__main__":
    unittest.main()
