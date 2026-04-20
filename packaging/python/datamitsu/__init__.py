"""datamitsu - Configuration management and binary distribution tool."""

__version__ = "0.0.0"  # Replaced during build

from datamitsu._find_binary import get_exe_path

__all__ = ["get_exe_path", "__version__"]
