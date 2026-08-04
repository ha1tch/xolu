#!/usr/bin/env python3
"""
scripts/release_gate.py — the canonical release-readiness gate.

Consolidates every check the release process performs by convention
into one script that either exits 0 (green) or lists what is wrong.
This is the canonical generator for the release gate; ad-hoc inline
gate checks (in commit messages, chat, or elsewhere) are non-canonical
and must not be trusted.

Checks performed:

  A. Register consistency
     A1. No item in docs/TRACKING.md's status table carries ✓
         (closed items belong in RESOLVED.md, not the register).
     A2. Every T-nn row in the status table has a matching detail
         section, and vice versa (id sets equal).
     A3. Each item's status table row agrees with its detail block's
         Theme/Priority/Status field lines (values match exactly).

  B. Header discipline
     B1. Release-coupled documents (docs/TRACKING.md, docs/KNOWN_ISSUES.md)
         carry `Version:` matching the VERSION file.

  C. Changelog hygiene
     C1. No `[Unreleased]` section (its contents should be folded into
         the most recent version's entry before release).

  D. Resolution record hygiene
     D1. RESOLVED.md exists and has a T-item closure at its top for
         at least the current version (soft check; warns rather than
         fails, since not every release closes items).

  E. Toolchain pin coherence
     E1. go.mod's `go` directive, `.github/workflows/ci.yml`'s
         golangci-lint-action version, and Dockerfile's `golang:`
         base image should track together.
     Coherence rule: golangci-lint version's built-with-Go >= go.mod's
     Go >= Dockerfile's Alpine Go base (majors match; base image
     may be one minor behind if intentional). Mismatches are named,
     not auto-fixed.

  F. Dormant-guards discipline
     F1. docs/KNOWN_ISSUES.md carries a "Dormant guards" section
         with at least the guards this project has (soft: presence,
         not currency of last-exercised dates — that is a per-release
         decision recorded in release notes, not this gate).

Exit 0 on green. Exit 1 on any error. Warnings do not fail the gate.

Usage:
    python3 scripts/release_gate.py            # from repo root
    python3 scripts/release_gate.py --strict   # promote warnings to errors
"""

import argparse
import re
import sys
from pathlib import Path


# ---------------------------------------------------------------------------
# Small utilities.

def read(path):
    return Path(path).read_text()


def slurp_lines(path):
    return Path(path).read_text().splitlines()


class Report:
    def __init__(self, strict=False):
        self.errors = []
        self.warnings = []
        self.strict = strict

    def err(self, section, msg):
        self.errors.append(f"[{section}] {msg}")

    def warn(self, section, msg):
        if self.strict:
            self.errors.append(f"[{section}] (strict) {msg}")
        else:
            self.warnings.append(f"[{section}] {msg}")

    def print_and_exit(self):
        for w in self.warnings:
            print(f"WARN: {w}")
        for e in self.errors:
            print(f"ERROR: {e}")
        if self.errors:
            print(f"\nGATE FAIL: {len(self.errors)} error(s), {len(self.warnings)} warning(s)")
            sys.exit(1)
        print(f"GATE PASS ({len(self.warnings)} warning(s))")


# ---------------------------------------------------------------------------
# A. Register consistency — delegated to scripts/register.py so the gate
# and the register editor share one parser and cannot disagree. (The
# gate's former inline regexes silently skipped items using ** emphasis
# on the priority cell — T-51 was invisible to A2/A3 for two releases.)

def check_register(r: Report):
    sys.path.insert(0, str(Path(__file__).resolve().parent))
    import register
    register.load().check(r)


# ---------------------------------------------------------------------------
# B. Header discipline.

def check_headers(r: Report):
    version = read("VERSION").strip()
    for path in ("docs/TRACKING.md", "docs/KNOWN_ISSUES.md"):
        m = re.search(r"^Version:\s*(\S+)", read(path), re.M)
        if not m:
            r.err("B1", f"{path}: no Version: header")
        elif m.group(1) != version:
            r.err("B1", f"{path}: Version: {m.group(1)} != VERSION {version}")


# ---------------------------------------------------------------------------
# C. Changelog hygiene.

def check_changelog(r: Report):
    c = read("CHANGELOG.md")
    if re.search(r"^## \[Unreleased\]", c, re.M):
        r.err("C1", "CHANGELOG.md has an [Unreleased] section — fold into the release entry")


# ---------------------------------------------------------------------------
# D. Resolution record.

def check_resolved(r: Report):
    version = read("VERSION").strip()
    resolved = Path("docs/RESOLVED.md")
    if not resolved.exists():
        r.warn("D1", "docs/RESOLVED.md not found")
        return
    top = resolved.read_text().split("\n\n", 3)  # skip title/preamble
    body = "\n\n".join(top[:4])
    if not re.search(rf"closed v?{re.escape(version)}", body):
        r.warn("D1", f"no closure record for v{version} near the top of RESOLVED.md")


# ---------------------------------------------------------------------------
# E. Toolchain pin coherence.

def _extract_go_directive():
    m = re.search(r"^go (\d+\.\d+(?:\.\d+)?)", read("go.mod"), re.M)
    return m.group(1) if m else None


def _extract_ci_linter_version():
    ci = read(".github/workflows/ci.yml")
    m = re.search(r"version:\s*v(\d+\.\d+\.\d+)", ci)
    return m.group(1) if m else None


def _extract_dockerfile_go():
    """Return the (base image line, Go version string) or (None, None)."""
    lines = read("Dockerfile").splitlines()
    for line in lines:
        m = re.search(r"golang:(\d+(?:\.\d+)?)", line)
        if m:
            return line.strip(), m.group(1)
    return None, None


def check_pins(r: Report):
    go_directive = _extract_go_directive()
    linter_version = _extract_ci_linter_version()
    docker_line, docker_go = _extract_dockerfile_go()

    if not go_directive:
        r.err("E1", "go.mod: `go` directive not found")
        return
    if not linter_version:
        r.err("E1", "ci.yml: golangci-lint-action version pin not found")
    if not docker_go:
        r.err("E1", "Dockerfile: no golang:X image found")

    # Coherence: majors match. Minor drift is allowed one step for the
    # Dockerfile base (Alpine images sometimes lag by a few days).
    def major_minor(s):
        parts = s.split(".")
        return int(parts[0]), int(parts[1]) if len(parts) > 1 else 0

    if docker_go:
        gd_maj, gd_min = major_minor(go_directive)
        dk_maj, dk_min = major_minor(docker_go)
        if (dk_maj, dk_min) < (gd_maj, gd_min - 1) or (dk_maj != gd_maj):
            r.err(
                "E1",
                f"Dockerfile is at Go {docker_go} but go.mod requires {go_directive} "
                f"(bump `{docker_line}` to golang:{gd_maj}.{gd_min}-alpine)"
            )
        elif (dk_maj, dk_min) != (gd_maj, gd_min):
            r.warn(
                "E1",
                f"Dockerfile at Go {docker_go}, go.mod at {go_directive} — "
                f"minor drift; align at next Docker refresh (see register)"
            )

    # The linter pin is verified by CI itself (the action refuses if the
    # linter's built-with-Go < go.mod target); a stale pin surfaces there
    # as a hard failure, so we only warn here to name the versions in play.
    if linter_version:
        r.warn(
            "E1 (info)",
            f"pins in play: go.mod={go_directive}, golangci-lint={linter_version}, "
            f"Dockerfile={docker_go or '?'}"
        )


# ---------------------------------------------------------------------------
# F. Dormant guards discipline.

def check_dormant_guards(r: Report):
    ki = read("docs/KNOWN_ISSUES.md")
    if not re.search(r"^## Dormant guards", ki, re.M):
        r.err("F1", "docs/KNOWN_ISSUES.md: no `Dormant guards` section (Part 3 §8)")


# ---------------------------------------------------------------------------

def main():
    ap = argparse.ArgumentParser(description="xolu release-readiness gate")
    ap.add_argument("--strict", action="store_true",
                    help="promote warnings to errors")
    args = ap.parse_args()

    r = Report(strict=args.strict)
    check_register(r)
    check_headers(r)
    check_changelog(r)
    check_resolved(r)
    check_pins(r)
    check_dormant_guards(r)
    r.print_and_exit()


if __name__ == "__main__":
    main()
