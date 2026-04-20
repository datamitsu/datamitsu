"""Platform detection and binary path resolution."""

import platform
from pathlib import Path


def get_platform():
    """Get normalized platform name."""
    system = platform.system().lower()
    if system == "linux":
        return "linux"
    elif system == "darwin":
        return "darwin"
    elif system == "windows":
        return "windows"
    else:
        raise RuntimeError(f"Unsupported platform: {system}")


def get_arch():
    """Get normalized architecture name."""
    machine = platform.machine().lower()
    if machine in ("x86_64", "amd64", "x64"):
        return "x86_64"
    elif machine in ("arm64", "aarch64"):
        return "arm64"
    else:
        raise RuntimeError(f"Unsupported architecture: {machine}")


def get_exe_path():
    """Find the datamitsu binary for the current platform."""
    plat = get_platform()
    arch = get_arch()

    package_name = f"datamitsu_{plat}_{arch}"
    exe_name = "datamitsu.exe" if plat == "windows" else "datamitsu"

    # Try to import the platform-specific package
    try:
        mod = __import__(package_name)
        package_dir = Path(mod.__file__).parent
        exe_path = package_dir / "bin" / exe_name

        if exe_path.exists():
            return str(exe_path)
    except ImportError:
        pass

    raise RuntimeError(
        f"datamitsu binary not found for {plat}-{arch}.\n"
        f"Please install the platform-specific package: pip install datamitsu-{plat}-{arch}"
    )
