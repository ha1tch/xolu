#!/usr/bin/env python3
"""
scripts/baseline.py — checkpoint intake ritual.

Answers one question mechanically: IS THIS TREE WHAT IT CLAIMS TO BE?
Run it at the start of any session that begins from a checkpoint zip,
before trusting the tree as a baseline for new work.

What it does, in order:

  1. UNPACK (zip mode) the checkpoint into a working directory.
  2. MANIFEST: verify every file against MANIFEST.sha256 inside the
     archive — corruption anywhere in the zip's round-trip is caught at
     the door. Checkpoints older than v0.16.22 carry no manifest; that
     is reported as a warning, not a failure.
  3. BUILD: `go build ./...` must be clean.
  4. TEST: the full suite, sharded (same machinery as release.py), so
     no single step outlives an execution ceiling. Interrupted runs
     resume: re-invoke with the same arguments and completed shards
     are skipped (journal: .baseline-state.json in the tree).
  5. COMPARE: pass/skip/fail counts against the numbers TESTING.md
     records for this version. A divergence means the tree does not
     reproduce its own release record and must not be trusted silently.

Exit 0: baseline clean. Exit 1: divergent or failed — the output names
what differs; file it in the register before proceeding (a witnessed
divergence is evidence, not noise).

Typical use:

    # from a fresh session, checkpoint uploaded
    python3 xolu/scripts/baseline.py --zip /mnt/user-data/uploads/xolu-v0.16.22-checkpoint.zip \
        --dir /home/claude/xolu
    # tree already unpacked
    python3 scripts/baseline.py --tree /home/claude/xolu

Prerequisites: Go toolchain on PATH and the module cache populated
(`go mod download`); baseline verifies the tree, it does not install
toolchains.
"""

import argparse
import hashlib
import json
import re
import sys
import subprocess
import time
import zipfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import testrun  # noqa: E402


def say(msg):
    print(msg, flush=True)


def unpack(zip_path: Path, dest: Path) -> None:
    dest.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(zip_path) as z:
        z.extractall(dest)


def verify_manifest(tree: Path) -> tuple[str, list[str]]:
    """Returns (status, problems). status: ok | absent | fail."""
    mf = tree / "MANIFEST.sha256"
    if not mf.is_file():
        return "absent", []
    problems = []
    listed = set()
    for line in mf.read_text().splitlines():
        if not line.strip():
            continue
        digest, rel = line.split(None, 1)
        listed.add(rel)
        f = tree / rel
        if not f.is_file():
            problems.append(f"missing: {rel}")
        elif hashlib.sha256(f.read_bytes()).hexdigest() != digest:
            problems.append(f"hash mismatch: {rel}")
    return ("fail" if problems else "ok"), problems


def testing_md_counts(tree: Path) -> dict:
    """Parse TESTING.md's statistics table: the release's own record."""
    text = (tree / "TESTING.md").read_text()
    out = {}
    for key, label in [("run", "Tests (including subtests)"),
                       ("pass_top", "Passed"), ("skip_top", "Skipped"),
                       ("fail", "Failed")]:
        m = re.search(rf"\|\s*{re.escape(label)}\s*\|\s*(\d+)\s*\|", text)
        if m:
            out[key] = int(m.group(1))
    m = re.search(r"Version \*\*([^*]+)\*\*", text)
    out["version"] = m.group(1) if m else "?"
    return out


class Journal:
    def __init__(self, tree: Path, version: str):
        self.path = tree / ".baseline-state.json"
        self.version = version
        self.data = {}
        if self.path.is_file():
            try:
                loaded = json.loads(self.path.read_text())
                if loaded.get("version") == version:
                    self.data = loaded.get("steps", {})
            except (json.JSONDecodeError, OSError):
                pass

    def record(self, step, status, **meta):
        self.data[step] = {"status": status, **meta}
        self.path.write_text(json.dumps(
            {"version": self.version, "steps": self.data}, indent=1))

    def green(self, step):
        return self.data.get(step, {}).get("status") == "ok"


def main() -> int:
    ap = argparse.ArgumentParser(
        description="Checkpoint intake: verify a tree is what it claims to be")
    src = ap.add_mutually_exclusive_group(required=True)
    src.add_argument("--zip", type=Path, help="checkpoint zip to unpack and verify")
    src.add_argument("--tree", type=Path, help="already-unpacked tree to verify")
    ap.add_argument("--dir", type=Path,
                    help="unpack destination (zip mode; default /tmp/baseline)")
    ap.add_argument("--shard-size", type=int, default=8)
    ap.add_argument("--short", action="store_true", help="pass -short to go test")
    args = ap.parse_args()

    if args.zip:
        tree = args.dir or Path("/tmp/baseline")
        say(f"-- unpack {args.zip.name} -> {tree}")
        unpack(args.zip, tree)
    else:
        tree = args.tree
    tree = tree.resolve()
    version = (tree / "VERSION").read_text().strip()
    journal = Journal(tree, version)
    log = open(tree / f"baseline-{version}.log", "a")
    say(f"baseline: v{version} at {tree}")

    # 1. Manifest
    say("-- manifest")
    status, problems = verify_manifest(tree)
    if status == "absent":
        say("   !! no MANIFEST.sha256 (checkpoint predates v0.16.22) — file-integrity check skipped")
    elif status == "fail":
        for p in problems[:10]:
            say(f"   {p}")
        say(f"   FAIL manifest: {len(problems)} problem(s)")
        return 1
    else:
        say("   ok manifest")
    journal.record("manifest", status if status != "absent" else "ok", detail=status)

    # 2. Build
    say("-- build")
    if journal.green("build"):
        say("   .. journaled green, skipped")
    else:
        p = subprocess.run(["go", "build", "./..."], stdout=log,
                           stderr=subprocess.STDOUT, cwd=tree, timeout=600)
        if p.returncode != 0:
            journal.record("build", "fail", rc=p.returncode)
            say(f"   FAIL build (rc={p.returncode}); see baseline-{version}.log")
            return 1
        journal.record("build", "ok")
        say("   ok build")

    # 3. Test, sharded and resumable
    say("-- test")
    pkgs = testrun.list_packages(tree)
    shards = testrun.chunk(pkgs, args.shard_size)
    jfiles, cfiles = [], []
    for i, group in enumerate(shards):
        key = f"shard-{i:02d}"
        jfile = tree / f".baseline-shard-{i:02d}.json"
        cfile = tree / f".baseline-cover-{i:02d}.out"
        jfiles.append(jfile)
        cfiles.append(cfile)
        if journal.green(key) and jfile.is_file():
            say(f"   .. {key} (journaled green, skipped)")
            continue
        t = time.time()
        rc = testrun.run_shard(tree, group, jfile, cfile, log, short=args.short)
        journal.record(key, "fail" if rc != 0 else "ok", rc=rc)
        say(f"   {'FAIL' if rc != 0 else 'ok  '} {key} ({len(group)} pkgs, {time.time()-t:.0f}s)")
        if rc != 0:
            say(f"   halted; failures are in .baseline-shard-{i:02d}.json — "
                "re-invoke to resume remaining shards after diagnosis")
            return 1
    merged = tree / ".baseline-tests.json"
    testrun.merge(jfiles, cfiles, merged, tree / ".baseline-cover.out")
    got = testrun.count(merged)

    # 4. Compare against the release's own record
    say("-- compare against TESTING.md")
    want = testing_md_counts(tree)
    if want.get("version") != version:
        say(f"   !! TESTING.md records v{want.get('version')}, VERSION says {version}")
    divergent = []
    for key, label in [("run", "tests run"), ("pass_top", "top-level passed"),
                       ("skip_top", "skipped"), ("fail", "failed")]:
        w, g = want.get(key), got.get(key)
        mark = "ok  " if w == g else "DIFF"
        if w != g:
            divergent.append(label)
        say(f"   {mark} {label}: recorded={w} observed={g}")
    journal.record("compare", "fail" if divergent else "ok",
                   recorded=want, observed=got)

    if divergent:
        say(f"BASELINE DIVERGENT: {', '.join(divergent)} — do not trust this "
            "tree silently; file the divergence in docs/TRACKING.md")
        return 1
    say(f"BASELINE CLEAN: v{version} reproduces its release record")
    return 0


if __name__ == "__main__":
    sys.exit(main())
