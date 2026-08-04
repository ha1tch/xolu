#!/usr/bin/env python3
"""scripts/regrun.py — mid-session regression runner.

Answers a narrower question than either of this repo's other two test
runners: NOT "does this checkpoint reproduce its own release record"
(baseline.py — compares against MANIFEST.sha256 and TESTING.md's
already-recorded counts) and NOT "is this tree ready to ship"
(release.py — the full guarded pipeline: build, tests, lint,
c04dcheck, generators, gate, zip). regrun.py exists for the gap
between those two: after editing code mid-session, BEFORE the next
release regenerates TESTING.md's own numbers, is the tree still green?
baseline.py cannot answer this — its own TESTING.md comparison would
report a false divergence for any legitimate test-count change (a new
test added, a renamed one) that hasn't been through a release yet.

Reuses testrun.py's sharding/resume machinery exactly like
baseline.py: any package's test run over SPLIT_THRESHOLD is
transparently split, and a killed run resumes from its own journal
without repeating completed shards. Journal and log files are named
`.regrun-<tag>-*` / `regrun-<tag>.log` at the repo root — a distinct
prefix from `.baseline-*`/`.release-*` so the three tools' journals can
never collide, and (like those two) outside every path release.py's
own ZIP_SOURCES allowlist walks, so nothing here can leak into a
checkpoint by accident.

Usage:

    python3 scripts/regrun.py                         # full suite, no -race
    python3 scripts/regrun.py --race                  # full suite, -race
    python3 scripts/regrun.py --race --tag t138-probe  # named journal
    python3 scripts/regrun.py --resume --tag t138-probe

Exit 0: run=N fail=0. Exit 1: at least one failure, or a shard was
left incomplete (re-invoke with the same --tag to resume).

When to reach for this rather than a bare `go test ./...`: any time
the run is long enough that a session-environment kill would lose
uncommitted progress (T-111's own lesson — a single large package is
one OS process regardless of shard grouping, and this environment has
no guaranteed process lifetime across a kill). A `go test` a person
would happily wait out at a terminal doesn't need this; a multi-minute
full-tree run does.
"""
import argparse
import json
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import testrun  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent


def say(msg):
    print(msg, flush=True)


def main() -> int:
    ap = argparse.ArgumentParser(
        description="Mid-session regression: is the tree green after "
                     "an edit, before the next release regenerates "
                     "TESTING.md's own counts?")
    ap.add_argument("--tag", default="default",
                     help="journal/log name suffix; use a distinct tag "
                          "per concurrent investigation so journals "
                          "never collide (default: 'default')")
    ap.add_argument("--race", action="store_true",
                     help="pass -race to every shard")
    ap.add_argument("--shard-size", type=int, default=8,
                     help="packages per shard (default 8, matches "
                          "baseline.py/release.py)")
    ap.add_argument("--resume", action="store_true",
                     help="no functional difference from a bare "
                          "re-invoke with the same --tag — the journal "
                          "always resumes; --resume exists so the "
                          "intent is explicit at the call site, "
                          "matching baseline.py/release.py's own flag")
    args = ap.parse_args()

    state_path = ROOT / f".regrun-{args.tag}-state.json"
    state = json.loads(state_path.read_text()) if state_path.exists() else {}

    say(f"regrun: tag={args.tag} race={args.race} at {ROOT}")
    pkgs = testrun.list_packages(ROOT)
    shards = testrun.chunk(pkgs, args.shard_size)
    log = open(ROOT / f"regrun-{args.tag}.log", "a")
    jfiles, cfiles = [], []
    for i, group in enumerate(shards):
        key = f"shard-{i:02d}"
        jfile = ROOT / f".regrun-{args.tag}-{i:02d}.json"
        cfile = ROOT / f".regrun-{args.tag}-{i:02d}.cover.out"
        jfiles.append(jfile)
        cfiles.append(cfile)
        if state.get(key) == "ok" and jfile.is_file():
            say(f"   .. {key} (journaled ok, skipped)")
            continue
        t = time.time()
        if args.race:
            cmd = ["go", "test", "-race", "-json", "-count=1",
                   f"-coverprofile={cfile}", *group]
            with open(jfile, "w") as jf:
                p = subprocess.run(cmd, stdout=jf, stderr=log, cwd=ROOT, timeout=900)
            rc = p.returncode
        else:
            rc = testrun.run_shard(ROOT, group, jfile, cfile, log, timeout=600)
        state[key] = "ok" if rc == 0 else "fail"
        state_path.write_text(json.dumps(state))
        say(f"   {'FAIL' if rc != 0 else 'ok  '} {key} ({len(group)} pkgs, {time.time()-t:.0f}s)")
        if rc != 0:
            say(f"   halted; re-invoke with --tag {args.tag} to resume after diagnosis")
            return 1

    merged = ROOT / f".regrun-{args.tag}-tests.json"
    testrun.merge(jfiles, cfiles, merged, ROOT / f".regrun-{args.tag}-cover.out")
    counts = testrun.count(merged)
    say(f"DONE tag={args.tag}: run={counts['run']} fail={counts['fail']} "
        f"skip={counts['skip']} pass_top={counts['pass_top']} skip_top={counts['skip_top']}")
    return 1 if counts["fail"] else 0


if __name__ == "__main__":
    sys.exit(main())
