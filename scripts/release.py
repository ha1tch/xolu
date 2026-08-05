#!/usr/bin/env python3
"""
scripts/release.py — xolu release orchestration.

Python replacement for release.sh, written after two sessions of the
shell original's structural failures: display pipes eating guard exit
codes, monolithic runs killed by execution ceilings with no record of
what completed, and PIPESTATUS-class shell fragility (the shell script
itself documents one such bug it once shipped).

Design principles:

  1. DURABLE JOURNAL. Every step records its result in
     .release-state.json atomically on completion. A killed run leaves
     an exact record; `--resume` continues it.
  2. RESUMABILITY WITH TEETH. Expensive verifications (test shards,
     lint) skip on resume when journaled green for the same version.
     Cheap generators always re-run (they are idempotent). The gate and
     the archive checks ALWAYS run — no journal entry excuses them.
  3. SHARDED TESTS. The suite runs as journaled package groups, so no
     single step outlives an execution ceiling and a resume continues
     mid-suite. JSON output concatenates; coverprofiles merge.
  4. NO DISPLAY PIPES. Full command output streams to the run log
     (release-<version>.log); stdout carries a bounded per-step
     summary. The process exit code is the only success signal, and
     nothing the caller does for terseness can disarm it.
  5. GUARDS OWNED HERE. The four-guard chain — tests, lint, c04dcheck,
     release gate — is this script's job, in this order, every exit
     checked. c04dcheck is treated as failed on ANY diagnostic output,
     because its singlechecker exits 0 on "analysis skipped".

Usage:
    python3 scripts/release.py <version> [--resume] [--short]
        [--no-zip] [--no-lint] [--with-integration] [--shard-size N]

release.sh remains as a shim invoking this script.
"""

import argparse
import fnmatch
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import time
import zipfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import syncver  # noqa: E402
import testrun  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
STATE_FILE = ROOT / ".release-state.json"
TEST_JSON = ROOT / "test-output.json"
COVER_OUT = ROOT / "cover.out"
COVER_SUMMARY = ROOT / "cover-summary.txt"

# ── Archive policy as data ──────────────────────────────────────────
# Explicit source list — never '.' or a parent. tools/ is included
# (release.sh omitted it; checkpoints have always carried it, and
# future sessions need c04dcheck to travel).
ZIP_SOURCES = [
    "README.md", "CHANGELOG.md", "MANUAL.md", "TESTING.md", "VERSION",
    "LICENSE", "Makefile", "Dockerfile", "docker-compose.yml",
    ".golangci.yml", ".repoman.json", "run_tests.sh", "syncver.sh", "release.sh",
    "go.mod", "go.sum",
    "cmd", "pkg", "docs", "scripts", "tests", ".github", "tools", "examples",
]
ZIP_OPTIONAL = ["TS_PROGRESS.md"]

ZIP_EXCLUDE = [
    "*.bak", "*.db", "*.db-wal", "*.db-shm", "*.db-journal", "*.db-tmp",
    "*-wal", "*-shm", "*-journal", "graph.data", "graph.index",
    "*.golden", "*.pprof", "*.prof", "*.test", "*.tmp",
    "test-output.json", "test-errors.txt",
    "cover.out", "cover-summary.txt", "coverage.out", "cover.*.out",
    ".DS_Store", "._*", "__MACOSX", "Thumbs.db", "ehthumbs.db",
    "*.so", "*.dylib", "*.dll", "*.exe", "*.a", "*.o",
    ".release-state.json", "release-*.log", ".ed-journal.json",
    "*.pyc", "*.pyo", "__pycache__/*",
    "examples/crm/xolu-crm-data/*",
]

CONTAMINATION_RE = re.compile(
    r"\.bak$|\.db$|\.db-wal$|\.db-shm$|\.db-journal$|\.db-tmp$"
    r"|-wal$|-shm$|-journal$|graph\.data$|graph\.index$"
    r"|\.golden$|\.pprof$|\.prof$|\.test$|\.DS_Store$|/\._|__MACOSX|Thumbs\.db$"
    r"|\.pyc$|\.pyo$|__pycache__/"
)

MAGICS = [
    (b"\x7fELF", "ELF"),
    (b"\xca\xfe\xba\xbe", "Mach-O"),
    (b"\xfe\xed\xfa\xce", "Mach-O"),
    (b"\xfe\xed\xfa\xcf", "Mach-O"),
    (b"MZ", "PE"),
]
ZIP_SIZE_WARN = 3 * 1024 * 1024  # 3 MB ceiling for a source-only archive

CLEAN_PATTERNS = [
    "*.bak", "*.db", "*.db-wal", "*.db-shm", "*.db-journal", "*.db-tmp",
    "*-wal", "*-shm", "*-journal", "graph.data", "graph.index",
    "*.golden", "*.pprof", "*.prof", "*.test",
    ".DS_Store", "._*", "Thumbs.db", "ehthumbs.db",
    "*.pyc", "*.pyo",
]


# ── Infrastructure ──────────────────────────────────────────────────

class StepFailed(Exception):
    pass


class Journal:
    """Per-version step results, written atomically after every step."""

    def __init__(self, version: str):
        self.version = version
        self.data = {}
        if STATE_FILE.is_file():
            try:
                loaded = json.loads(STATE_FILE.read_text())
                if loaded.get("version") == version:
                    self.data = loaded.get("steps", {})
            except (json.JSONDecodeError, OSError):
                pass  # corrupt/stale journal: start fresh

    def record(self, step: str, status: str, **meta):
        self.data[step] = {"status": status, "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), **meta}
        tmp = STATE_FILE.with_suffix(".tmp")
        tmp.write_text(json.dumps({"version": self.version, "steps": self.data}, indent=1))
        os.replace(tmp, STATE_FILE)

    def green(self, step: str) -> bool:
        return self.data.get(step, {}).get("status") == "ok"


class Ctx:
    def __init__(self, args):
        self.args = args
        self.version = args.version
        self.journal = Journal(self.version)
        self.log_path = ROOT / f"release-{self.version}.log"
        self.log = open(self.log_path, "a")

    def run(self, cmd, timeout=600, cwd=None, env=None) -> int:
        """Run a command, full output to the run log, return exit code.
        The caller sees nothing to pipe; the log sees everything."""
        self.log.write(f"\n$ {' '.join(cmd)}\n")
        self.log.flush()
        e = dict(os.environ)
        if env:
            e.update(env)
        try:
            p = subprocess.run(cmd, stdout=self.log, stderr=subprocess.STDOUT,
                               timeout=timeout, cwd=cwd or ROOT, env=e)
            return p.returncode
        except subprocess.TimeoutExpired:
            self.log.write(f"\n[release.py] TIMEOUT after {timeout}s\n")
            self.log.flush()
            return 124

    def capture(self, cmd, timeout=600, cwd=None) -> tuple[int, str]:
        """Run a command capturing output (also copied to the log)."""
        p = subprocess.run(cmd, capture_output=True, text=True,
                           timeout=timeout, cwd=cwd or ROOT)
        out = (p.stdout or "") + (p.stderr or "")
        self.log.write(f"\n$ {' '.join(cmd)}\n{out}")
        self.log.flush()
        return p.returncode, out


def say(msg):
    print(msg, flush=True)


# ── Steps ───────────────────────────────────────────────────────────

def step_validate(ctx: Ctx):
    if not syncver.VERSION_RE.match(ctx.version):
        raise StepFailed("invalid version format (expected X.Y.Z or X.Y.Z-suffix)")
    changelog = (ROOT / "CHANGELOG.md").read_text()
    if f"## [{ctx.version}]" not in changelog:
        say(f"   !! no CHANGELOG entry for [{ctx.version}] — continuing anyway")
    return {}


def step_syncver(ctx: Ctx):
    syncver.set_version(ctx.version)
    return {"version": ctx.version}


def step_build(ctx: Ctx):
    rc = ctx.run(["make", "build-xolu", "build-iolu", "build-xotogen"], timeout=300)
    if rc != 0:
        raise StepFailed(f"build failed (rc={rc}); see {ctx.log_path.name}")
    return {}


def step_test(ctx: Ctx):
    """Sharded suite via testrun: per-shard JSON + coverprofile,
    journaled per shard, merged at the end. A resume re-runs only
    non-green shards."""
    pkgs = testrun.list_packages(ROOT)
    shards = testrun.chunk(pkgs, ctx.args.shard_size)
    failed = []
    jfiles, cfiles = [], []
    for i, group in enumerate(shards):
        key = f"test-shard-{i:02d}-of-{len(shards)}"
        jfile = ROOT / f".test-shard-{i:02d}.json"
        cfile = ROOT / f"cover.{i:02d}.out"
        jfiles.append(jfile)
        cfiles.append(cfile)
        if ctx.args.resume and ctx.journal.green(key) and jfile.is_file() and cfile.is_file():
            say(f"   .. {key} (journaled green, skipped)")
            continue
        rc = testrun.run_shard(ROOT, group, jfile, cfile, ctx.log,
                               short=ctx.args.short)
        ctx.journal.record(key, "fail" if rc != 0 else "ok",
                           packages=len(group), rc=rc)
        say(f"   {'FAIL' if rc != 0 else 'ok  '} {key} ({len(group)} pkgs)")
        if rc != 0:
            failed.append(key)
    if failed:
        raise StepFailed(f"test shards failed: {', '.join(failed)}")

    testrun.merge(jfiles, cfiles, TEST_JSON, COVER_OUT)
    with open(COVER_SUMMARY, "w") as cs:
        subprocess.run(["go", "tool", "cover", f"-func={COVER_OUT}"],
                       stdout=cs, stderr=ctx.log, cwd=ROOT, timeout=120)
    if COVER_OUT.stat().st_size == 0:
        raise StepFailed("cover.out is empty — coverage was not collected")

    # Fail-count belt over the merged stream, independent of shard rcs.
    c = testrun.count(TEST_JSON)
    if c["fail"]:
        raise StepFailed(f"{c['fail']} test failure(s) in merged JSON")
    if ctx.args.with_integration:
        rc = ctx.run(["go", "test", "-tags", "integration", "-count=1",
                      "./pkg/client/", "-run", "TestIntegration"], timeout=600)
        if rc != 0:
            raise StepFailed("integration suite failed")
    ctx.run(["python3", "scripts/test_report.py", "--json", str(TEST_JSON),
             "--binaries", os.environ.get("RELEASE_BINARIES", "xolu iolu xotogen")])
    return {"tests_run": c["run"], "shards": len(shards)}


def step_lint(ctx: Ctx):
    if ctx.args.no_lint:
        say("   !! lint skipped (--no-lint)")
        return {"skipped": True}
    if shutil.which("golangci-lint") is None:
        raise StepFailed("golangci-lint not found — install it or pass --no-lint explicitly")
    rc = ctx.run(["golangci-lint", "run", "--timeout=5m"], timeout=420)
    if rc != 0:
        raise StepFailed(f"lint failed (rc={rc}); see {ctx.log_path.name}")
    return {}


def step_c04dcheck(ctx: Ctx):
    """Doctrine analyzer. Its singlechecker exits 0 when analysis is
    skipped, so ANY output is treated as failure — the guard must not
    be disarmable by a loading error."""
    tool = Path("/tmp/c04dcheck")
    if not tool.is_file():
        rc = ctx.run(["go", "build", "-o", str(tool), "."],
                     timeout=300, cwd=ROOT / "tools" / "c04dcheck")
        if rc != 0:
            raise StepFailed("c04dcheck build failed")
    rc, out = ctx.capture([str(tool), "./pkg/...", "./cmd/..."], timeout=300)
    if rc != 0 or out.strip():
        raise StepFailed(f"c04dcheck not clean (rc={rc}): {out.strip()[:300]}")
    return {}


def step_generate(ctx: Ctx):
    meta = ctx.journal.data.get("test", {})
    total = meta.get("tests_run", 0)
    rc = ctx.run(["python3", "scripts/gen_testing_md.py",
                  "--json", str(TEST_JSON), "--cover", str(COVER_SUMMARY),
                  "--version", ctx.version,
                  "--narrative", "docs/TESTING_STRATEGY.md",
                  "--output", "TESTING.md"])
    if rc != 0:
        raise StepFailed("gen_testing_md.py failed")
    rc = ctx.run(["python3", "scripts/update_badges.py", ctx.version, str(total)])
    if rc != 0:
        raise StepFailed("update_badges.py failed")
    rc = ctx.run(["python3", "scripts/wave_progress.py"])
    if rc != 0:
        raise StepFailed("wave_progress.py failed")
    return {"tests_run": total}


def step_consistency(ctx: Ctx):
    if not syncver.check():
        raise StepFailed("VERSION and version.go disagree")
    top = ""
    for line in (ROOT / "CHANGELOG.md").read_text().splitlines():
        m = re.match(r"^## \[(.+)\]", line)
        if m:
            top = m.group(1)
            break
    if top != ctx.version:
        say(f"   !! CHANGELOG top entry [{top}] does not match [{ctx.version}]")
    return {}


def step_gate(ctx: Ctx):
    rc, out = ctx.capture(["python3", "scripts/release_gate.py"], timeout=120)
    for line in out.splitlines():
        if line.startswith(("WARN", "ERR", "GATE")):
            say(f"   {line}")
    if rc != 0:
        raise StepFailed("release gate failed")
    return {}


def step_clean(ctx: Ctx):
    removed = 0
    for p in ROOT.rglob("*"):
        if ".git" in p.parts or not p.is_file():
            continue
        if any(fnmatch.fnmatch(p.name, pat) for pat in CLEAN_PATTERNS):
            p.unlink()
            removed += 1
    return {"removed": removed}


def _excluded(rel: str) -> bool:
    name = rel.rsplit("/", 1)[-1]
    return any(fnmatch.fnmatch(name, pat) or fnmatch.fnmatch(rel, pat)
               for pat in ZIP_EXCLUDE)


def step_zip(ctx: Ctx):
    if ctx.args.no_zip:
        say("   !! zip skipped (--no-zip)")
        return {"skipped": True}
    zipname = ROOT / f"xolu-v{ctx.version}-checkpoint.zip"
    zipname.unlink(missing_ok=True)
    sources = list(ZIP_SOURCES) + [s for s in ZIP_OPTIONAL if (ROOT / s).exists()]
    count = 0
    manifest_lines = []
    with zipfile.ZipFile(zipname, "w", zipfile.ZIP_DEFLATED) as z:
        for src in sources:
            path = ROOT / src
            if not path.exists():
                raise StepFailed(f"zip source missing: {src}")
            files = [path] if path.is_file() else sorted(
                p for p in path.rglob("*") if p.is_file())
            for f in files:
                rel = str(f.relative_to(ROOT))
                if _excluded(rel):
                    continue
                z.write(f, rel)
                digest = hashlib.sha256(f.read_bytes()).hexdigest()
                manifest_lines.append(f"{digest}  {rel}")
                count += 1
        # MANIFEST.sha256 last: every archived file, hash + path, so an
        # intake ritual (scripts/baseline.py) can verify the checkpoint
        # byte-for-byte before trusting it as a session baseline.
        z.writestr("MANIFEST.sha256", "\n".join(manifest_lines) + "\n")

    # Sanity 1: contamination scan over the archive listing.
    with zipfile.ZipFile(zipname) as z:
        names = z.namelist()
        dirty = [n for n in names if CONTAMINATION_RE.search(n)]
        if dirty:
            raise StepFailed(f"checkpoint contains {len(dirty)} artifact file(s): {dirty[:5]}")
        # Sanity 2: magic-byte binary sniff.
        bad = []
        for n in names:
            if n.endswith("/"):
                continue
            head = z.open(n).read(4)
            for magic, kind in MAGICS:
                if head.startswith(magic):
                    bad.append((kind, n))
        if bad:
            raise StepFailed(f"checkpoint contains binaries: {bad[:5]}")
    # Sanity 3: size ceiling.
    size = zipname.stat().st_size
    if size > ZIP_SIZE_WARN:
        say(f"   !! checkpoint is {size/1048576:.1f} MB — exceeds 3 MB source-only ceiling")
    return {"files": count, "bytes": size, "zip": zipname.name}


# name, fn, resumable (skip when journaled green under --resume),
# always (runs even when journaled green — gates and generators).
STEPS = [
    ("validate",    step_validate,    False, True),
    ("syncver",     step_syncver,     False, True),
    ("build",       step_build,       True,  False),
    ("test",        step_test,        True,  False),
    ("lint",        step_lint,        True,  False),
    ("c04dcheck",   step_c04dcheck,   True,  False),
    ("generate",    step_generate,    False, True),
    ("consistency", step_consistency, False, True),
    ("gate",        step_gate,        False, True),
    ("clean",       step_clean,       False, True),
    ("zip",         step_zip,         False, True),
]


def main() -> int:
    ap = argparse.ArgumentParser(description="xolu release orchestration")
    ap.add_argument("version")
    ap.add_argument("--resume", action="store_true",
                    help="skip expensive steps journaled green for this version")
    ap.add_argument("--short", action="store_true", help="pass -short to go test")
    ap.add_argument("--no-zip", action="store_true")
    ap.add_argument("--no-lint", action="store_true")
    ap.add_argument("--with-integration", action="store_true")
    ap.add_argument("--shard-size", type=int, default=8,
                    help="packages per test shard (default 8)")
    args = ap.parse_args()

    ctx = Ctx(args)
    say(f"release.py {args.version}  (log: {ctx.log_path.name}"
        f"{', resuming' if args.resume else ''})")
    t0 = time.time()
    for name, fn, resumable, always in STEPS:
        if args.resume and resumable and not always and ctx.journal.green(name):
            say(f"-- {name}: journaled green, skipped")
            continue
        say(f"-- {name}")
        t = time.time()
        try:
            meta = fn(ctx) or {}
        except StepFailed as e:
            ctx.journal.record(name, "fail", error=str(e))
            say(f"   FAIL {name}: {e}")
            say(f"   run halted; fix and re-run with --resume")
            return 1
        ctx.journal.record(name, "ok", seconds=round(time.time() - t, 1), **meta)
        say(f"   ok {name} ({time.time() - t:.0f}s)")

    tests = ctx.journal.data.get("test", {}).get("tests_run", "?")
    zmeta = ctx.journal.data.get("zip", {})
    say("")
    say("======================================")
    say(f"  Release v{args.version} prepared")
    say(f"  Tests run: {tests}")
    if zmeta.get("zip"):
        say(f"  Zip: {zmeta['zip']} ({zmeta.get('bytes', 0)/1048576:.1f} MB)")
    say(f"  Total: {time.time() - t0:.0f}s")
    say("======================================")
    return 0


if __name__ == "__main__":
    sys.exit(main())
