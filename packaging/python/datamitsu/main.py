import os
import platform
import sys

ISSUE_URL = "https://github.com/datamitsu/datamitsu/issues/new"
ARCH_MAPPING = {
    "amd64": "x86_64",
    "x86_64": "x86_64",
    "aarch64": "arm64",
    "arm64": "arm64",
}


def main():
    os_name = platform.system().lower()
    arch = platform.machine().lower()
    arch = ARCH_MAPPING.get(arch, arch)
    ext = ".exe" if os_name == "windows" else ""
    subfolder = f"datamitsu-{os_name}-{arch}"
    executable = os.path.join(
        os.path.dirname(__file__), "bin", subfolder, "datamitsu" + ext
    )
    if not os.path.isfile(executable):
        print(
            f"Couldn't find binary {executable}. "
            f"Please create an issue: {ISSUE_URL}",
            file=sys.stderr,
        )
        return 1

    if os_name == "windows":
        import subprocess

        result = subprocess.run([executable] + sys.argv[1:])
        sys.exit(result.returncode)
    else:
        try:
            os.execvp(executable, [executable] + sys.argv[1:])
        except OSError as e:
            print(f"Failed to execute {executable}: {e}", file=sys.stderr)
            return 1
