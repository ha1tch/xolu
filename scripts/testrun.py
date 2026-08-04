#!/usr/bin/env python3
"""
scripts/testrun.py — sharded `go test` machinery shared by release.py
and baseline.py.

Not a CLI. Both consumers run the suite as package shards so no single
subprocess outlives an execution ceiling, then merge shard outputs into
one test-output.json and one cover.out. Keeping the machinery in one
module means the release and the intake ritual cannot drift in how they
count a test.

Package-count sharding (chunk()) does not protect a package whose own
test count has grown large enough to trip cumulative per-process
resource pressure -- `go test pkgA pkgB` still runs pkgA and pkgB as
two separate OS processes regardless of sharing one command line, so a
single large package is still one process no matter how it's grouped
(T-111: pkg/server crossed this line at ~870 tests). run_shard()
detects any package over SPLIT_THRESHOLD and splits its own test run
across several processes automatically, transparently to both
callers -- no manual per-release workaround needed.
"""

import json
import subprocess
from pathlib import Path


def list_packages(root: Path) -> list[str]:
    p = subprocess.run(["go", "list", "./..."], capture_output=True,
                       text=True, cwd=root, timeout=120)
    if p.returncode != 0:
        raise RuntimeError(f"go list failed: {p.stderr.strip()[:200]}")
    return [l for l in p.stdout.splitlines() if l.strip()]


def chunk(pkgs: list[str], size: int) -> list[list[str]]:
    return [pkgs[i:i + size] for i in range(0, len(pkgs), size)]


def list_test_names(root: Path, pkg: str) -> list[str]:
    """Top-level Test function names for one package, via `go test
    -list` -- the Go-native enumeration, not a source grep (respects
    build tags and generated files, which a grep would not). Excludes
    Benchmark/Example/Fuzz entries (matched separately by `-list`'s own
    regex, not filtered client-side) and `-list`'s trailing summary
    line."""
    p = subprocess.run(["go", "test", "-list", "^Test", pkg],
                       capture_output=True, text=True, cwd=root, timeout=60)
    if p.returncode != 0:
        raise RuntimeError(f"go test -list failed for {pkg}: {p.stderr.strip()[:200]}")
    return [l for l in p.stdout.splitlines() if l.startswith("Test")]


# A single package's own test binary is one OS process regardless of
# how many packages share one `go test` command line -- chunk()'s
# package-count sharding does nothing to protect a package whose own
# test count has grown large enough to trip cumulative per-process
# resource pressure in a constrained sandbox (T-111: pkg/server
# crossed this line at ~870 tests, first hit during the 0.20.2
# release, worked around by hand each time since). Any package over
# SPLIT_THRESHOLD gets its own run split into several separate `go
# test` processes instead of one, transparently to every caller of
# run_shard.
#
# T-141 (2026-08-03): the original 150 was calibrated against a real
# capacity ceiling that has since moved. Measured directly, not
# inferred: pkg/server at 957 tests -- well over the old threshold --
# now runs cleanly UNSPLIT in 68.8s, versus 91.8s split into 11
# segments under the old threshold (34% pure per-invocation overhead:
# process startup + test-binary link, paid once per segment instead
# of once total, for identical work). Whatever combination of fixes
# across intervening sessions raised the effective ceiling (T-139,
# this session's own bal-rollup/cal-manager file-descriptor leak fix,
# is the most likely single contributor, though not confirmed as the
# sole cause), it has moved past pkg/server's current size. Raised
# with headroom rather than re-derived to a precise new ceiling --
# that's its own capacity investigation, matching how T-98 originally
# established evidence before picking a number, and not owed again
# until this is actually tripped.
SPLIT_THRESHOLD = 1200
SPLIT_SEGMENT_SIZE = 90


def run_package_split(root: Path, pkg: str, tmp_dir: Path, log,
                      short: bool = False,
                      timeout: int = 600) -> tuple[int, list[Path], list[Path]]:
    """Runs one package's tests as several separate `go test`
    processes instead of one, each covering a disjoint slice of its
    own top-level Test functions via `-run`. Returns (max_rc,
    json_paths, cover_paths) for the caller to fold into merge().
    Exists because a test binary is one OS process regardless of
    sharding by package (T-111) -- splitting has to happen inside one
    package's own test run, not just across packages."""
    names = list_test_names(root, pkg)
    n_segments = max(1, -(-len(names) // SPLIT_SEGMENT_SIZE))
    seg_size = -(-len(names) // n_segments)
    segments = [names[i:i + seg_size] for i in range(0, len(names), seg_size)]
    safe = pkg.strip("./").replace("/", "_").replace(".", "_")
    json_paths, cover_paths = [], []
    max_rc = 0
    for i, seg in enumerate(segments):
        pattern = "^(" + "|".join(seg) + ")$"
        jp = tmp_dir / f"split.{safe}.{i:02d}.json"
        cp = tmp_dir / f"split.{safe}.{i:02d}.cover.out"
        cmd = ["go", "test", "-json"]
        if short:
            cmd.append("-short")
        cmd += ["-count=1", f"-coverprofile={cp}", "-run", pattern, pkg]
        with open(jp, "w") as jf:
            p = subprocess.run(cmd, stdout=jf, stderr=log, cwd=root, timeout=timeout)
        max_rc = max(max_rc, p.returncode)
        json_paths.append(jp)
        cover_paths.append(cp)
    return max_rc, json_paths, cover_paths


def run_shard(root: Path, pkgs: list[str], json_path: Path, cover_path: Path,
              log, short: bool = False, timeout: int = 600) -> int:
    """One shard: -json stream to json_path, coverage to cover_path,
    stderr to the caller's log. Returns the go test exit code.

    Any package in the group whose own top-level test count exceeds
    SPLIT_THRESHOLD is pulled out and run via run_package_split
    instead of joining the shard's combined invocation -- see that
    function's own doc for why. Fully transparent to the caller:
    json_path/cover_path end up holding everything, merged, exactly as
    if the whole shard had run in one invocation with no package large
    enough to need splitting.
    """
    split_pkgs, normal_pkgs = [], []
    for pkg in pkgs:
        try:
            names = list_test_names(root, pkg)
        except RuntimeError:
            # -list itself failed (e.g. a build-tag-gated package with
            # no default test binary) -- let the normal combined
            # invocation below surface the real error rather than
            # masking it here.
            normal_pkgs.append(pkg)
            continue
        (split_pkgs if len(names) > SPLIT_THRESHOLD else normal_pkgs).append(pkg)

    tmp_dir = json_path.parent
    all_json: list[Path] = []
    all_cover: list[Path] = []
    max_rc = 0

    if normal_pkgs:
        cmd = ["go", "test", "-json"]
        if short:
            cmd.append("-short")
        normal_json = tmp_dir / f"{json_path.stem}.normal.json"
        normal_cover = tmp_dir / f"{cover_path.stem}.normal.out"
        cmd += ["-count=1", f"-coverprofile={normal_cover}", *normal_pkgs]
        with open(normal_json, "w") as jf:
            p = subprocess.run(cmd, stdout=jf, stderr=log, cwd=root, timeout=timeout)
        max_rc = max(max_rc, p.returncode)
        all_json.append(normal_json)
        all_cover.append(normal_cover)

    for pkg in split_pkgs:
        rc, jps, cps = run_package_split(root, pkg, tmp_dir, log,
                                         short=short, timeout=timeout)
        max_rc = max(max_rc, rc)
        all_json.extend(jps)
        all_cover.extend(cps)

    merge(all_json, all_cover, json_path, cover_path)
    return max_rc


def merge(json_paths: list[Path], cover_paths: list[Path],
          test_json: Path, cover_out: Path) -> None:
    """Concatenate shard JSON streams; merge coverprofiles keeping a
    single mode: line."""
    with open(test_json, "w") as out:
        for p in json_paths:
            out.write(p.read_text())
    with open(cover_out, "w") as out:
        wrote_mode = False
        for p in cover_paths:
            for line in p.read_text().splitlines(keepends=True):
                if line.startswith("mode:"):
                    if wrote_mode:
                        continue
                    wrote_mode = True
                out.write(line)


def count(test_json: Path) -> dict:
    """Counts over a merged -json stream. `run`/`fail`/`skip` count
    individual tests (subtests included); `pass_top`/`skip_top` count
    top-level tests (no '/' in the name), matching TESTING.md's table."""
    c = {"run": 0, "fail": 0, "skip": 0, "pass_top": 0, "skip_top": 0}
    with open(test_json) as f:
        for line in f:
            try:
                ev = json.loads(line)
            except json.JSONDecodeError:
                continue
            t = ev.get("Test")
            a = ev.get("Action")
            if not t:
                continue
            if a == "run":
                c["run"] += 1
            elif a == "fail":
                c["fail"] += 1
            elif a == "skip":
                c["skip"] += 1
                if "/" not in t:
                    c["skip_top"] += 1
            elif a == "pass" and "/" not in t:
                c["pass_top"] += 1
    return c
