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


# ── Live progress ────────────────────────────────────────────────────
#
# Added 2026-08-07: an accurate denominator (every top-level test name,
# summed across every package via `go test -list`) was measured
# directly before deciding against it -- 76.7s for 3,587 tests across
# 46 packages, against a ~116s total run: counting first would nearly
# double `make test`'s own wall time just to know a percentage. The
# previous run's own test-output.json is read instead -- free (the
# file already exists on disk from last time), and test counts don't
# swing wildly run to run, so it's a genuinely useful estimate, not a
# guess pretending to be exact. Labelled "~" in the UI so it reads as
# an estimate, not a promise. Subtests are never counted toward this
# denominator or shown in the live bar at all -- t.Run() subtests are
# generated at runtime and cannot be enumerated in advance by any
# method (not just the expensive one just ruled out), so a "percent of
# subtests complete" figure would be fabricated, not merely
# approximate. The final summary's own subtest totals (see
# build_report's own run_all/skip_all counts) are exact, computed
# after the fact from the real, complete stream -- only the live,
# in-flight estimate is approximate.
def estimate_total_from_previous(json_path: Path) -> int | None:
    """Top-level test count (pass+fail+skip) from the PREVIOUS run's
    own json_path, read before this run overwrites it. None if no
    previous run exists or it's unparseable -- callers fall back to a
    count-only display (no percentage, no bar) rather than a fabricated
    total."""
    if not json_path.is_file():
        return None
    n = 0
    try:
        with open(json_path) as f:
            for line in f:
                try:
                    ev = json.loads(line)
                except json.JSONDecodeError:
                    continue
                t = ev.get("Test")
                a = ev.get("Action")
                if t and "/" not in t and a in ("pass", "fail", "skip"):
                    n += 1
    except OSError:
        return None
    return n or None


# Added 2026-08-07, same day as the estimator above -- Horacio's own
# direct challenge to the first-run message ("no previous run found")
# was fair: only the expensive, compile-based go test -list approach
# had actually been measured and ruled out (76.7s). A plain source scan
# for top-level Test function declarations was never tried, and once
# measured is dramatically cheaper -- 0.02-0.14s, effectively free,
# since it never invokes the Go toolchain at all, just greps *_test.go
# files directly. Slightly over-counts against the real, build-tag-
# respecting total (3627 vs 3597 measured here, ~0.8% high, since a
# plain grep doesn't know about build constraints the way `go test`
# does) -- close enough for a live estimate, labelled "~" the same as
# the previous-run figure, and used only as the fallback when no
# previous run exists at all; the previous run's own real count is
# preferred whenever it's available, since it reflects an actual past
# execution rather than a naive source count.
def estimate_total_via_static_scan(root: Path) -> int | None:
    """Counts `func TestXxx(t *testing.T)` declarations across every
    *_test.go file in the tree -- no compilation, no go toolchain
    invocation, just grep. The exact signature match already excludes
    Benchmark/Example/Fuzz functions (different parameter types), so
    this counts the same population -list would, just less precisely.
    None if grep itself is unavailable or the scan fails for any
    reason -- callers fall back to a bare running count in that case."""
    try:
        p = subprocess.run(
            ["grep", "-rEc", r"^func Test[A-Za-z0-9_]+\(t \*testing\.T\)",
             "--include=*_test.go", str(root)],
            capture_output=True, text=True, timeout=10,
        )
    except (OSError, subprocess.TimeoutExpired):
        return None
    total = 0
    for line in p.stdout.splitlines():
        if ":" not in line:
            continue
        try:
            n = int(line.rsplit(":", 1)[1])
        except ValueError:
            continue
        total += n
    return total or None


class ProgressBar:
    """Renders one live, in-place-redrawn status line via \\r + clear-
    to-end-of-line -- no scrollback spam, one line updated in place.
    `total` is an estimate (see estimate_total_from_previous's own doc)
    or None, in which case this shows a running count with no bar or
    percentage rather than a fabricated one. Redraws are throttled to
    avoid flooding the terminal on a fast run -- state still updates on
    every event, only the actual screen write is rate-limited."""

    BAR_WIDTH = 32
    MIN_REDRAW_INTERVAL = 0.08  # seconds

    def __init__(self, total: int | None, c: "Colour"):
        self.total = total
        self.c = c
        self.done = self.fail = self.skip = 0
        self.t0 = time.time()
        self._last_draw = 0.0
        self._last_len = 0

    def event(self, action: str) -> None:
        if action == "pass":
            self.done += 1
        elif action == "fail":
            self.done += 1
            self.fail += 1
        elif action == "skip":
            self.done += 1
            self.skip += 1
        self._maybe_draw()

    def _maybe_draw(self, force: bool = False) -> None:
        now = time.time()
        if not force and (now - self._last_draw) < self.MIN_REDRAW_INTERVAL:
            return
        self._last_draw = now
        elapsed = now - self.t0

        if self.total:
            pct = min(100, int(100 * self.done / self.total))
            filled = min(self.BAR_WIDTH, int(self.BAR_WIDTH * self.done / self.total))
            bar = "█" * filled + "░" * (self.BAR_WIDTH - filled)
            counts = f"~{self.done}/{self.total}"
            line = f"  [{bar}] {pct:3d}% {counts}"
        else:
            line = f"  running... {self.done} done"

        if self.fail:
            line += f"  {self.c.red(f'{self.fail} fail')}"
        if self.skip:
            line += f"  {self.c.yellow(f'{self.skip} skip')}"
        line += f"  {elapsed:.0f}s"

        pad = max(0, self._last_len - len(line))
        sys.stdout.write("\r" + line + (" " * pad))
        sys.stdout.flush()
        self._last_len = len(line)

    def finish(self) -> None:
        self._maybe_draw(force=True)
        sys.stdout.write("\n")
        sys.stdout.flush()


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


def run_unsharded_live(short: bool, race: bool, cover_path: Path, json_path: Path,
                       timeout: int, c: "Colour") -> int:
    """Same invocation and same on-disk output as run_unsharded --
    every line still lands in json_path exactly as before, so
    build_report() downstream is completely unaffected -- but streamed
    through Popen instead of run() so a live progress line can update
    as events arrive, instead of the process running silently to
    completion first. TTY-gated by the caller; never called otherwise,
    since the ANSI redraw sequences below would corrupt a log file or
    piped output.

    Real perf regression found and fixed 2026-08-07, same day as the
    live bar itself: TestRebuildFrom_CorrectAndBounded (pkg/cal) started
    failing under this path specifically -- a genuine wall-clock
    performance ceiling test, sensitive to whatever else is consuming
    CPU while it runs. The original version called json.loads() on
    EVERY line of go test's own -json stream, including every "output"
    and "run" event never actually used for progress tracking -- real,
    ongoing CPU work in this process, competing directly against the
    test binary it's supposed to be quietly observing. Fixed with a
    cheap substring pre-check (go test's own JSON is Go's compact
    marshaling, no spaces after colons, confirmed directly against a
    real sample before relying on it) that skips full JSON parsing
    entirely for the large majority of lines that can't be a top-level
    pass/fail/skip event, plus lowering this process's own scheduling
    priority via os.nice() so what CPU work does remain yields to the
    actual test run rather than contending with it on equal footing.
    """
    cmd = ["go", "test", "-json"]
    if short:
        cmd.append("-short")
    if race:
        cmd.append("-race")
    cmd += ["-count=1", f"-coverprofile={cover_path}", "./..."]

    total = estimate_total_from_previous(json_path)
    if total is None:
        total = estimate_total_via_static_scan(ROOT)
    bar = ProgressBar(total, c)

    try:
        os.nice(10)  # yield CPU to the test binary this process is observing
    except (AttributeError, OSError):
        pass  # not available on this platform, or already niced -- harmless either way

    needle = ('"Action":"pass"', '"Action":"fail"', '"Action":"skip"')
    with open(json_path, "w") as jf:
        proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                text=True, cwd=ROOT)
        assert proc.stdout is not None
        for line in proc.stdout:
            jf.write(line)
            if not any(n in line for n in needle):
                continue  # cheap skip -- avoids json.loads() on the vast majority of lines
            try:
                ev = json.loads(line)
            except json.JSONDecodeError:
                continue
            t = ev.get("Test")
            a = ev.get("Action")
            if t and "/" not in t and a in ("pass", "fail", "skip"):
                bar.event(a)
        proc.wait(timeout=timeout)

    bar.finish()
    return proc.returncode


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
    the failure list, per-package results (status, elapsed, coverage%)
    parsed from package-level pass/fail/output events, and -- new,
    2026-08-07 -- the SUBTEST-inclusive run/skip totals (every `t.Run`
    subtest counted, not just its own top-level parent) plus the full
    name of every skipped test, top-level or subtest. The data for both
    was always present in the -json stream; only the top-level-only
    filter meant it was never surfaced before now."""
    cats = {c: {"pass": 0, "fail": 0, "skip": 0} for c in ("system", "cache", "stress", "bench")}
    failures = []
    skip_names = []  # every skip, top-level and subtest, "pkg: name"
    run_all = skip_all = 0  # subtest-inclusive totals, all categories combined
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

            if test:
                if action == "run":
                    run_all += 1
                elif action == "skip":
                    skip_all += 1
                    skip_names.append(f"{pkg}: {test}")

            if test and "/" not in test:  # top-level only, existing categorised counts
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
    return {"cats": cats, "failures": failures, "pkgs": pkgs, "total_fail": total_fail,
            "run_all": run_all, "skip_all": skip_all, "skip_names": skip_names}


def print_report(report: dict, c: Colour, quiet: bool, aggregate_cover: float | None):
    cats, pkgs, failures = report["cats"], report["pkgs"], report["failures"]
    run_all, skip_all, skip_names = report["run_all"], report["skip_all"], report["skip_names"]

    if failures:
        print(c.red("FAILURES"))
        print(c.red("--------"))
        for f in failures:
            print(f"  {c.red(f)}")
        print()

    if skip_names:
        print(c.yellow("SKIPPED"))
        print(c.yellow("-------"))
        for s in skip_names:
            print(f"  {c.yellow(s)}")
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
    subtest_note = f" ({skip_all} skipped)" if skip_all else ""
    print(f"  {'Subtests run:':<22} {run_all}{subtest_note}")
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

    print(c.bold("xolu test suite"))
    print("===============")
    if short:
        print(c.yellow("(short mode — stress tests and benchmarks skipped; use --full to include them)"))
    print()

    t0 = time.time()
    if args.shard_size:
        rc = run_sharded(short, args.race, args.shard_size, args.tag,
                         cover_path, json_path, args.timeout)
    elif c.on:
        # Live progress only when colour_enabled's own TTY check passed
        # -- the same interactive-terminal test already governing every
        # other ANSI sequence this script emits, reused rather than a
        # second, possibly-divergent check.
        rc = run_unsharded_live(short, args.race, cover_path, json_path, args.timeout, c)
    else:
        rc = run_unsharded(short, args.race, cover_path, json_path, args.timeout)
    elapsed = time.time() - t0

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
