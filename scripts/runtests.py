#!/usr/bin/env python3
"""scripts/runtests.py — the canonical xolu test runner.

Supersedes two things that used to diverge from this repo's own
Python test machinery (testrun.py, used by baseline.py/release.py/
regrun.py): run_tests.sh's bash implementation, and the Makefile's
bare `go test -short ./...`. Both are now thin wrappers around this
module (see run_tests.sh and the Makefile's own `test` target) --
one orchestration path, used identically whether it's a local
`make test` or a session in this sandbox, so a test result means the
same thing regardless of who ran it or how. That equivalence is the
actual point: T-139 (an intermittent "too many open files" failure)
was invisible in this sandbox's own sharded tooling and only
reproduced under the traditional unsharded invocation -- not because
sharding fixed anything, but because it changed the odds of a
leaked-resource bug crossing a per-process ceiling. Divergent
orchestration meant a result on one side couldn't be trusted to mean
the same thing on the other.

DEFAULT BEHAVIOUR IS UNSHARDED: every package in one `go test`
invocation, matching run_tests.sh's and make test's historical
behaviour, and preserving the property that actually caught T-139.
Sharding (--shard-size) is opt-in, for environments with a
genuine per-process resource ceiling this sandbox has (see
testrun.py's own SPLIT_THRESHOLD doc) -- reach for it there, not by
default here.

Usage:
    python3 scripts/runtests.py                   # standard (short mode)
    python3 scripts/runtests.py --full             # include stress tests
    python3 scripts/runtests.py --race
    python3 scripts/runtests.py --threshold 75
    python3 scripts/runtests.py --html
    python3 scripts/runtests.py --quiet
    python3 scripts/runtests.py --charts
    python3 scripts/runtests.py --shard-size 8 --tag ci   # constrained env

Exit code matches `go test`'s own: 0 on pass, nonzero on any failure.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import testrun  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
MODULE = "github.com/ha1tch/xolu"

CACHE_PAT = re.compile(r"TestRedisCache_|TestSlabbis_")
STRESS_PAT = re.compile(r"TestStress_|TestTSStress_")
BENCH_PAT = re.compile(r"_Benchmark|BlobVsAdapted")


# ── Colour ───────────────────────────────────────────────────────────

class Colour:
    def __init__(self, enabled: bool):
        self.on = enabled

    def _wrap(self, code, s):
        return f"\033[{code}m{s}\033[0m" if self.on else str(s)

    def bold(self, s):    return self._wrap("1", s)
    def green(self, s):   return self._wrap("32", s)
    def red(self, s):     return self._wrap("1;31", s)
    def yellow(self, s):  return self._wrap("33", s)
    def dim(self, s):     return self._wrap("2", s)

    def cover(self, pct):
        if pct is None:
            return "n/a"
        s = f"{pct:.1f}%"
        if pct >= 80:
            return self.green(s)
        if pct >= 60:
            return self.yellow(s)
        return self.red(s)


def colour_enabled(no_colour: bool) -> bool:
    if no_colour or os.environ.get("NO_COLOR"):
        return False
    if not sys.stdout.isatty():
        return False
    term = os.environ.get("TERM", "")
    return any(term.startswith(p) for p in
               ("xterm", "rxvt", "screen", "tmux", "vte", "alacritty", "foot", "linux", "ansi"))


# ── Run ──────────────────────────────────────────────────────────────

def run_unsharded(short: bool, race: bool, cover_path: Path, json_path: Path, timeout: int) -> int:
    cmd = ["go", "test", "-json"]
    if short:
        cmd.append("-short")
    if race:
        cmd.append("-race")
    cmd += ["-count=1", f"-coverprofile={cover_path}", "./..."]
    with open(json_path, "w") as jf:
        p = subprocess.run(cmd, stdout=jf, stderr=subprocess.DEVNULL, cwd=ROOT, timeout=timeout)
    return p.returncode


def run_sharded(short: bool, race: bool, shard_size: int, tag: str,
                 cover_path: Path, json_path: Path, timeout: int) -> int:
    """Shares testrun.py's journal-per-shard resumability with
    baseline.py/regrun.py, under its own `.runtests-<tag>-*` prefix so
    it can never collide with either. -race isn't threaded through
    testrun.run_shard's own signature (it doesn't take one), so shard
    with a raw invocation mirroring run_shard's own shape when -race
    is requested, matching regrun.py's own precedent for this."""
    state_path = ROOT / f".runtests-{tag}-state.json"
    state = json.loads(state_path.read_text()) if state_path.exists() else {}
    pkgs = testrun.list_packages(ROOT)
    shards = testrun.chunk(pkgs, shard_size)
    log = open(ROOT / f"runtests-{tag}.log", "a")
    jfiles, cfiles, max_rc = [], [], 0
    for i, group in enumerate(shards):
        key = f"shard-{i:02d}"
        jfile = ROOT / f".runtests-{tag}-{i:02d}.json"
        cfile = ROOT / f".runtests-{tag}-{i:02d}.cover.out"
        jfiles.append(jfile)
        cfiles.append(cfile)
        if state.get(key) == "ok" and jfile.is_file():
            continue
        if race:
            cmd = ["go", "test", "-race", "-json", "-count=1", f"-coverprofile={cfile}", *group]
            if short:
                cmd.insert(3, "-short")
            with open(jfile, "w") as jf:
                p = subprocess.run(cmd, stdout=jf, stderr=log, cwd=ROOT, timeout=timeout)
            rc = p.returncode
        else:
            rc = testrun.run_shard(ROOT, group, jfile, cfile, log, short=short, timeout=timeout)
        max_rc = max(max_rc, rc)
        state[key] = "ok" if rc == 0 else "fail"
        state_path.write_text(json.dumps(state))
    testrun.merge(jfiles, cfiles, json_path, cover_path)
    return max_rc


# ── Report ───────────────────────────────────────────────────────────

def classify(name: str) -> str:
    if CACHE_PAT.search(name):
        return "cache"
    if STRESS_PAT.search(name):
        return "stress"
    if BENCH_PAT.search(name):
        return "bench"
    return "system"


def build_report(json_path: Path) -> dict:
    """Walks the merged -json stream once, building everything the
    report needs: per-category PASS/FAIL/SKIP counts (top-level tests
    only, matching run_tests.sh's own prior counting convention),
    the failure list, and per-package results (status, elapsed,
    coverage%) parsed from package-level pass/fail/output events."""
    cats = {c: {"pass": 0, "fail": 0, "skip": 0} for c in ("system", "cache", "stress", "bench")}
    failures = []
    pkgs = {}  # pkg -> {"status": "ok"|"FAIL"|"?", "elapsed": float, "cover": float|None}

    with open(json_path) as f:
        for line in f:
            try:
                ev = json.loads(line)
            except json.JSONDecodeError:
                continue
            pkg = ev.get("Package")
            test = ev.get("Test")
            action = ev.get("Action")

            if test and "/" not in test:  # top-level only
                cat = classify(test)
                if action == "pass":
                    cats[cat]["pass"] += 1
                elif action == "fail":
                    cats[cat]["fail"] += 1
                    failures.append(f"{pkg}: {test}")
                elif action == "skip":
                    cats[cat]["skip"] += 1

            if pkg and not test:
                if action in ("pass", "fail"):
                    pkgs.setdefault(pkg, {})["status"] = "ok" if action == "pass" else "FAIL"
                    pkgs[pkg]["elapsed"] = ev.get("Elapsed", 0.0)
                elif action == "output":
                    out = ev.get("Output", "")
                    m = re.search(r"coverage:\s*([\d.]+)% of statements", out)
                    if m:
                        pkgs.setdefault(pkg, {})["cover"] = float(m.group(1))
                    elif re.match(r"^\?\s", out):
                        pkgs.setdefault(pkg, {})["status"] = "no-tests"

    total_fail = sum(c["fail"] for c in cats.values())
    return {"cats": cats, "failures": failures, "pkgs": pkgs, "total_fail": total_fail}


def print_report(report: dict, c: Colour, quiet: bool, aggregate_cover: float | None):
    cats, pkgs, failures = report["cats"], report["pkgs"], report["failures"]

    if failures:
        print(c.red("FAILURES"))
        print(c.red("--------"))
        for f in failures:
            print(f"  {c.red(f)}")
        print()

    if not quiet:
        print(c.bold("Package results:"))
        print()
        print(f"  {c.bold('Package'):<40} {c.bold('Time'):>7} {c.bold('Cover'):>8} {c.bold('Status'):>7}")
        print(f"  {'-------':<40} {'----':>7} {'-----':>8} {'------':>7}")
        for pkg in sorted(pkgs):
            info = pkgs[pkg]
            short = pkg[len(MODULE) + 1:] if pkg.startswith(MODULE) else pkg
            status = info.get("status", "?")
            elapsed = info.get("elapsed")
            time_s = f"{elapsed:.2f}s" if elapsed else "-"
            cover_s = c.cover(info.get("cover"))
            if status == "ok":
                status_s = c.green("ok")
            elif status == "FAIL":
                status_s = c.red("FAIL")
                short = c.red(short)
            else:
                status_s = c.dim("-")
                short = c.dim(short)
            print(f"  {short:<40} {time_s:>7} {cover_s:>8} {status_s:>7}")
        print()

    print(c.bold("Summary"))
    print(c.bold("-------"))
    labels = {"system": "SYSTEM TESTS:", "cache": "CACHE INTEGRATION:",
              "stress": "STRESS TESTS:", "bench": "BENCHMARKS:"}
    for key in ("system", "cache", "stress", "bench"):
        n = cats[key]
        label = labels[key]
        if n["fail"] > 0:
            fail_s = c.red(f"{n['fail']} FAIL")
            print(f"  {label:<22} {fail_s}  ({n['pass']} pass, {n['skip']} skip)")
        elif n["skip"] > 0 and n["pass"] == 0:
            skip_s = c.yellow(f"{n['skip']} SKIPPED")
            print(f"  {label:<22} {skip_s}")
        else:
            detail = f"  ({n['skip']} skipped)" if n["skip"] > 0 else ""
            pass_s = c.green(f"{n['pass']} PASS")
            print(f"  {label:<22} {pass_s}{detail}")

    total_fail = report["total_fail"]
    print(f"  {'FAIL:':<22} {c.green('0') if total_fail == 0 else c.red(str(total_fail))}")
    print()
    print(f"  {'Coverage:':<22} {c.cover(aggregate_cover)}")

    ok_pkgs = sum(1 for p in pkgs.values() if p.get("status") == "ok")
    fail_pkgs = sum(1 for p in pkgs.values() if p.get("status") == "FAIL")
    notest_pkgs = sum(1 for p in pkgs.values() if p.get("status") == "no-tests")
    print(f"  {'Packages:':<22} {c.green(str(ok_pkgs))} ok, "
          f"{c.red(str(fail_pkgs)) if fail_pkgs else c.green('0')} fail, "
          f"{c.dim(str(notest_pkgs))} no-tests")
    print()


def aggregate_coverage(cover_path: Path) -> float | None:
    if not cover_path.is_file():
        return None
    p = subprocess.run(["go", "tool", "cover", f"-func={cover_path}"],
                       capture_output=True, text=True, cwd=ROOT)
    if p.returncode != 0:
        return None
    lines = [l for l in p.stdout.splitlines() if l.strip()]
    if not lines:
        return None
    last = lines[-1].split()[-1]
    try:
        return float(last.rstrip("%"))
    except ValueError:
        return None


def run_charts(report: dict):
    charts_py = Path(__file__).parent / "charts.py"
    if not charts_py.is_file():
        print("Charts skipped: charts.py not found alongside runtests.py", file=sys.stderr)
        return
    lines = []
    for pkg, info in report["pkgs"].items():
        tests = sum(1 for f in report["failures"] if f.startswith(pkg + ":"))  # placeholder count
        cover = info.get("cover", "")
        lines.append(f"{pkg}|{tests}|{cover}")
    subprocess.run([sys.executable, str(charts_py), "both"],
                   input="\n".join(lines), text=True, cwd=ROOT)


# ── Main ─────────────────────────────────────────────────────────────

def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--full", action="store_true", help="include stress tests (drop -short)")
    ap.add_argument("--race", action="store_true", help="enable the race detector")
    ap.add_argument("--threshold", type=float, default=None,
                    help="fail if aggregate coverage is below this percentage")
    ap.add_argument("--html", action="store_true", help="generate coverage.html")
    ap.add_argument("--quiet", action="store_true", help="skip the per-package table")
    ap.add_argument("--no-colour", "--no-color", action="store_true", dest="no_colour")
    ap.add_argument("--charts", action="store_true", help="show coverage/test-count charts")
    ap.add_argument("--shard-size", type=int, default=None,
                    help="split into shards of N packages, journaled and resumable "
                         "(for constrained environments; default is unsharded)")
    ap.add_argument("--tag", default="default",
                    help="journal/log tag when --shard-size is used")
    ap.add_argument("--timeout", type=int, default=600, help="per-invocation timeout, seconds")
    args = ap.parse_args()

    c = Colour(colour_enabled(args.no_colour))
    cover_path = ROOT / "coverage.out"
    json_path = ROOT / "test-output.json"
    short = not args.full

    t0 = time.time()
    if args.shard_size:
        rc = run_sharded(short, args.race, args.shard_size, args.tag,
                         cover_path, json_path, args.timeout)
    else:
        rc = run_unsharded(short, args.race, cover_path, json_path, args.timeout)
    elapsed = time.time() - t0

    print(c.bold("xolu test suite"))
    print("===============")
    if short:
        print(c.yellow("(short mode — stress tests and benchmarks skipped; use --full to include them)"))
    print()

    report = build_report(json_path)
    agg_cover = aggregate_coverage(cover_path)
    print_report(report, c, args.quiet, agg_cover)
    print(f"  {'Elapsed:':<22} {elapsed:.1f}s")
    print()

    if args.html and cover_path.is_file():
        subprocess.run(["go", "tool", "cover", f"-html={cover_path}",
                        "-o", str(ROOT / "coverage.html")], cwd=ROOT)
        print("Reports: coverage.out, coverage.html")
        print()

    if args.threshold is not None:
        if agg_cover is None:
            print(c.red(f"Threshold: coverage unavailable, cannot check against {args.threshold}%"))
            rc = rc or 1
        elif agg_cover >= args.threshold:
            print(c.green(f"Threshold: {agg_cover:.1f}% >= {args.threshold}% (ok)"))
        else:
            print(c.red(f"Threshold: {agg_cover:.1f}% < {args.threshold}% (FAIL)"))
            rc = rc or 1

    if args.charts:
        run_charts(report)

    return rc


if __name__ == "__main__":
    sys.exit(main())
