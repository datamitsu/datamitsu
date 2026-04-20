"""Entry point for python -m datamitsu."""

import sys
import subprocess
from datamitsu._find_binary import get_exe_path


def main():
    """Execute datamitsu binary with all arguments."""
    try:
        exe_path = get_exe_path()
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)

    # Add --binary-command datamitsu if not present
    args = sys.argv[1:]
    if "--binary-command" not in args:
        args = ["--binary-command", "datamitsu"] + args

    # Execute binary
    result = subprocess.run([exe_path] + args)
    sys.exit(result.returncode)


if __name__ == "__main__":
    main()
