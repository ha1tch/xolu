#!/usr/bin/env python3
"""
scripts/syncver.py — keep VERSION and pkg/version/version.go in sync.

Direct port of syncver.sh with an importable surface: release.py imports
get_versions / set_version / check rather than shelling out. The CLI is
command-compatible with the shell original (show / set / check /
bump-patch / bump-minor / bump-major).
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
VERSION_FILE = ROOT / "VERSION"
VERSION_GO = ROOT / "pkg" / "version" / "version.go"

VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$")

VERSION_GO_TEMPLATE = """\
// Package version provides version information for xolu.
//
// IMPORTANT: Keep Version constant in sync with the VERSION file at project root.
// When updating the version, update BOTH files, or use: ./syncver.sh set <version>
package version

// Version is the current version of xolu.
// This MUST match the contents of the VERSION file.
const Version = "{version}"
"""


def get_file_version() -> str:
    if VERSION_FILE.is_file():
        return VERSION_FILE.read_text().strip()
    return ""


def get_go_version() -> str:
    if VERSION_GO.is_file():
        m = re.search(r'const Version = "([^"]*)"', VERSION_GO.read_text())
        if m:
            return m.group(1)
    return ""


def set_version(new_version: str) -> None:
    """Write both files. Raises ValueError on a malformed version string."""
    if not VERSION_RE.match(new_version):
        # Note: suffix is freeform (rc1, alpha, checkpoint, ...) — no
        # canonicality enforced, same as the shell original.
        raise ValueError(
            f"Invalid version format: {new_version!r}. "
            "Expected X.Y.Z or X.Y.Z-suffix (e.g. 0.7.3, 0.9.4-rc1)"
        )
    VERSION_FILE.write_text(new_version + "\n")
    VERSION_GO.write_text(VERSION_GO_TEMPLATE.format(version=new_version))


def check() -> bool:
    """True when the two files agree."""
    return get_file_version() == get_go_version() != ""


def bump(part: str) -> str:
    current = get_file_version()
    if not current:
        raise ValueError("Cannot read current version")
    base = current.split("-")[0]
    major, minor, patch = (int(x) for x in base.split("."))
    if part == "major":
        major, minor, patch = major + 1, 0, 0
    elif part == "minor":
        minor, patch = minor + 1, 0
    elif part == "patch":
        patch += 1
    else:
        raise ValueError(f"Unknown bump part: {part}")
    new = f"{major}.{minor}.{patch}"
    set_version(new)
    return new


def main(argv: list[str]) -> int:
    cmd = argv[0] if argv else "show"
    try:
        if cmd == "show":
            print(f"VERSION file: {get_file_version() or '<not found>'}")
            print(f"version.go:   {get_go_version() or '<not found>'}")
        elif cmd == "set":
            if len(argv) < 2:
                print("Error: Version required", file=sys.stderr)
                return 1
            set_version(argv[1])
            print(f"Version set to {argv[1]}")
        elif cmd == "check":
            if check():
                print(f"OK: Versions in sync ({get_file_version()})")
            else:
                print("MISMATCH:")
                print(f"  VERSION file: {get_file_version()}")
                print(f"  version.go:   {get_go_version()}")
                return 1
        elif cmd in ("bump-patch", "bump-minor", "bump-major"):
            print(f"Version set to {bump(cmd.split('-')[1])}")
        elif cmd in ("help", "--help", "-h"):
            print(__doc__)
        else:
            print(f"Unknown command: {cmd}", file=sys.stderr)
            return 1
    except ValueError as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
