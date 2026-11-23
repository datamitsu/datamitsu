import importlib.util
import json
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import quote

REGISTRY = "https://registry.npmjs.org"
LEGACY = re.compile(r"0\.0\.0-unstable\.[0-9]{8}\.[0-9a-f]{7,40}")
SCRIPT_DIR = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location(
    "check_npm_release", SCRIPT_DIR / "check-npm-release.py"
)
if SPEC is None or SPEC.loader is None:
    raise ImportError("cannot load check-npm-release.py")
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)


def stable_tag(name, fetch):
    url = f"{REGISTRY}/-/package/{quote(name, safe='')}/dist-tags"
    with fetch(url, timeout=30) as response:
        tags = json.load(response)
    if not isinstance(tags, dict) or any(
        not isinstance(value, str) for value in tags.values()
    ):
        raise ValueError(f"invalid dist-tags response for {name}")
    return tags.get("latest"), tags.get("next")


def _stable_version(value):
    match = re.fullmatch(
        r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc\.(0|[1-9][0-9]*))?",
        value or "",
    )
    if not match:
        return None
    return (
        tuple(map(int, match.groups()[:3])),
        match.group(4) is not None,
        int(match.group(4) or 0),
    )


def promote_tagged(
    directory, version, fetch=None, run=subprocess.run, *, baseline=None
):
    parsed = _stable_version(version)
    if parsed is None:
        raise ValueError("invalid approved tagged npm version")
    is_rc = parsed[1]
    channel = "next" if is_rc else "latest"
    if fetch is None:
        fetch = CHECK.build_opener(CHECK.RejectRedirect()).open
    directory = Path(directory)
    reconciled = CHECK.reconcile(directory, version, fetch)
    if reconciled["missing"]:
        raise ValueError(
            f"all target npm bytes must be present before promotion: {', '.join(reconciled['missing'])}"
        )
    names = [
        f"@datamitsu/{archive.stem}" for archive in sorted(directory.glob("*.tgz"))
    ]
    if baseline is not None and (
        not isinstance(baseline, dict) or set(baseline) != set(names)
    ):
        raise ValueError("tagged promotion baseline must pin every package")
    if baseline is not None and any(
        value is not None
        and (not isinstance(value, str) or _stable_version(value) is None)
        for value in baseline.values()
    ):
        raise ValueError(
            "tagged promotion baseline values must be numeric SemVer or null"
        )
    states = {}
    for name in names:
        latest, next_version = stable_tag(name, fetch)
        current = next_version if is_rc else latest
        other = latest if is_rc else next_version
        if other is not None:
            other_parsed = _stable_version(other)
            if (
                other_parsed is None
                or (is_rc and other_parsed[1])
                or (not is_rc and not other_parsed[1])
            ):
                raise ValueError(
                    f"unrecognized npm channel pointer for {name}: {other!r}"
                )
        if current is None:
            if baseline is None or baseline.get(name) is not None:
                raise ValueError(
                    f"unknown {channel} pointer for {name}; explicit null baseline required"
                )
            states[name] = None
            continue
        current_parsed = _stable_version(current)
        if current_parsed is None or current_parsed[1] != is_rc:
            raise ValueError(f"unrecognized {channel} pointer for {name}: {current!r}")
        if (
            baseline is not None
            and baseline.get(name) is not None
            and baseline[name] != current
            and current != version
        ):
            pinned = _stable_version(baseline[name])
            if (
                pinned is None
                or pinned[1] != is_rc
                or (current_parsed[0], current_parsed[2]) < (pinned[0], pinned[2])
            ):
                raise ValueError(
                    f"{channel} pointer predates the approved cutover baseline for {name}"
                )
        if current_parsed[0] > parsed[0] or (
            is_rc and current_parsed[0] == parsed[0] and current_parsed[2] > parsed[2]
        ):
            raise ValueError(f"refusing to regress {name} {channel} from {current}")
        states[name] = current
    for name, current in states.items():
        if current != version:
            run(
                [
                    "npm",
                    "dist-tag",
                    "add",
                    f"{name}@{version}",
                    channel,
                    "--registry",
                    REGISTRY,
                ],
                check=True,
            )
    for name in names:
        observed = stable_tag(name, fetch)[1 if is_rc else 0]
        if observed != version:
            raise ValueError(
                f"npm {channel} tag verification failed for {name}: {observed!r}"
            )
    return {"version": version, "channel": channel, "promoted": names}


def unstable_tag(name, fetch):
    url = f"{REGISTRY}/-/package/{quote(name, safe='')}/dist-tags"
    with fetch(url, timeout=30) as response:
        tags = json.load(response)
    if not isinstance(tags, dict) or any(
        not isinstance(value, str) for value in tags.values()
    ):
        raise ValueError(f"invalid dist-tags response for {name}")
    return tags.get("unstable")


def promote(directory, version, fetch=None, run=subprocess.run, *, baseline=None):
    if LEGACY.fullmatch(version) or not re.fullmatch(
        r"0\.0\.0-unstable\.[1-9][0-9]*\.[1-9][0-9]*", version
    ):
        raise ValueError("invalid approved npm prerelease version")
    if fetch is None:
        fetch = CHECK.build_opener(CHECK.RejectRedirect()).open
    directory = Path(directory)
    # Prove every target version is present and byte-identical before querying or writing tags.
    reconciled = CHECK.reconcile(directory, version, fetch)
    if reconciled["missing"]:
        raise ValueError(
            f"npm has not confirmed all prepared archives: {', '.join(reconciled['missing'])}"
        )
    names = [
        f"@datamitsu/{archive.stem}" for archive in sorted(directory.glob("*.tgz"))
    ]
    if baseline is not None and (
        not isinstance(baseline, dict)
        or set(baseline) != set(names)
        or any(
            value is not None
            and (not isinstance(value, str) or not LEGACY.fullmatch(value))
            for value in baseline.values()
        )
    ):
        raise ValueError(
            "cutover baseline must pin every package to a legacy version or null"
        )
    target_order = tuple(map(int, version.removeprefix("0.0.0-unstable.").split(".")))
    states = {}
    for name in names:
        current = unstable_tag(name, fetch)
        if current is None:
            if baseline is not None and baseline[name] is not None:
                raise ValueError(f"pinned unstable tag disappeared for {name}")
            states[name] = None
            continue
        if LEGACY.fullmatch(current):
            if baseline is None or baseline[name] != current:
                raise ValueError(
                    f"legacy unstable tag does not match a pinned cutover baseline for {name}"
                )
            states[name] = current
            continue
        match = re.fullmatch(r"0\.0\.0-unstable\.([1-9][0-9]*)\.([1-9][0-9]*)", current)
        if not match:
            raise ValueError(
                f"unrecognized unstable tag version for {name}: {current!r}"
            )
        existing = tuple(map(int, match.groups()))
        if existing > target_order:
            raise ValueError(
                f"refusing to move {name} unstable tag backward from {current}"
            )
        states[name] = current
    # The complete scan precedes all writes. Equal target is retry-safe for split promotions.
    for name, current in states.items():
        if current != version:
            run(
                [
                    "npm",
                    "dist-tag",
                    "add",
                    f"{name}@{version}",
                    "unstable",
                    "--registry",
                    REGISTRY,
                ],
                check=True,
            )
    for name in names:
        observed = unstable_tag(name, fetch)
        if observed != version:
            raise ValueError(
                f"npm unstable tag verification failed for {name}: {observed!r}"
            )
    return {"version": version, "promoted": names}


if __name__ == "__main__":
    if len(sys.argv) not in (3, 4):
        sys.exit(
            "usage: promote-npm-unstable.py STAGED_NPM_DIRECTORY VERSION [BASELINE_JSON]"
        )
    baseline = json.loads(Path(sys.argv[3]).read_text()) if len(sys.argv) == 4 else None
    if len(sys.argv) == 4 and not isinstance(baseline, dict):
        sys.exit("promotion baseline must be a JSON object")
    target = Path(sys.argv[1])
    version = sys.argv[2]
    if _stable_version(version) is not None:
        print(json.dumps(promote_tagged(target, version, baseline=baseline)))
    else:
        print(json.dumps(promote(target, version, baseline=baseline)))
