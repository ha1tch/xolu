#!/usr/bin/env python3
# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""crosscheck.py -- the two loaders, given the same files, must plan the same requests.

For every .seed file under the directories named (default: the conformance
cases and the repository's seeds), and for the files of each directory taken
together, this builds the plan with the Python loader (scripts/seed_system)
and with xoseed, and compares them step by step: method, path, body, headers,
phase, the key that identifies the record across loads, everything. Where one
refuses a file the other must refuse it with the same message. No server is
contacted.

    python3 scripts/seed_conformance/crosscheck.py --xoseed ./xoseed [DIR ...]

Exit status: 0 the same everywhere; 1 a difference; 2 usage.
"""

import argparse
import base64
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, os.path.dirname(HERE))

from seed_system.format import Context, SeedError, read_seed    # noqa: E402
from seed_system.load import seed_name                           # noqa: E402
from seed_system.plan import build_plan                          # noqa: E402

AS_OF = "2026-03-15T10:20:30Z"
KEYS = ("phase", "label", "kind", "where", "tenant", "as", "bind", "expect", "exists_ok", "headers", "query", "body", "raw",
        "entity", "id", "op", "file", "match", "key", "method", "path", "list_path", "query_path", "item_path", "read_path",
        "guard_schema", "tracked")


def python_plan(paths):
    """("plan", steps) or ("error", message), as the Python loader has it."""
    try:
        files = []
        for path in paths:
            header, records, _digest = read_seed(path)
            files.append((path, header, records))
        whole = [seed_name(p, h) for p, h, _r in files if h.get("exclusive")]
        if len(whole) > 1:      # the command line's own rule, which xoseed's plan command applies as well
            raise SeedError("seeds \"%s\" and \"%s\" are each a whole dataset (\"exclusive\": true) and cannot be loaded together" % (whole[0], whole[1]))
        ctx = Context(as_of=Context.parse_as_of(AS_OF))
        steps = []
        for step in build_plan(files, ctx):
            out = dict(step)
            out["path"] = step.get("path_template") or step.get("path")
            out["raw"] = base64.b64encode(step["raw"]).decode("ascii") if step["raw"] is not None else None
            out["expect"] = sorted(step["expect"])
            out["tenant"] = step["tenant"] or None
            out["as"] = step["as"] or None
            steps.append({k: out.get(k) for k in KEYS})
        return "plan", steps
    except SeedError as exc:
        return "error", str(exc)


def xoseed_plan(xoseed, paths):
    proc = subprocess.run(xoseed + ["plan", "--as-of", AS_OF] + paths, capture_output=True, text=True, encoding="utf-8")
    if proc.returncode == 0:
        steps = json.loads(proc.stdout)["steps"]
        for step in steps:
            step["expect"] = sorted(step["expect"])
        return "plan", [{k: s.get(k) for k in KEYS} for s in steps]
    lines = [ln for ln in proc.stderr.splitlines() if ln.startswith("error: ")]
    return "error", lines[-1][len("error: "):] if lines else "exit %d: %s" % (proc.returncode, proc.stderr.strip())


def compare(xoseed, paths):
    """A list of differences for one set of files loaded together."""
    kind_py, py = python_plan(paths)
    kind_go, go = xoseed_plan(xoseed, paths)
    if kind_py != kind_go:
        return ["python gives a %s, xoseed a %s: %s" % (kind_py, kind_go, py if kind_py == "error" else go)]
    if kind_py == "error":
        # what the system says about a file it cannot open is worded by the language, not by the loader
        if py != go and "cannot read" not in py:
            return ["the refusals differ:\n      python: %s\n      xoseed: %s" % (py, go)]
        return []
    if len(py) != len(go):
        return ["python plans %d steps, xoseed %d" % (len(py), len(go))]
    out = []
    for i, (a, b) in enumerate(zip(py, go)):
        for key in KEYS:
            # the text is compared, not the value: the order of an object's keys is part of what is sent
            ta, tb = json.dumps(a[key], ensure_ascii=False), json.dumps(b[key], ensure_ascii=False)
            if ta != tb:
                out.append("step %d (%s) \"%s\":\n      python: %s\n      xoseed: %s" % (i + 1, a["where"], key, ta[:300], tb[:300]))
    return out


def groups(directories):
    """Each file alone, then the files of each directory together."""
    seen = []
    for top in directories:
        for base, dirs, names in sorted(os.walk(top)):
            dirs.sort()
            seeds = sorted(os.path.join(base, n) for n in names if n.endswith(".seed"))
            for path in seeds:
                seen.append([path])
            if len(seeds) > 1:
                seen.append(seeds)
    return seen


def main():
    ap = argparse.ArgumentParser(description="Compare the plans of the Python loader and xoseed for the same files.")
    ap.add_argument("--xoseed", default=os.environ.get("XOSEED_BIN", os.path.join(ROOT, "xoseed")), help="the xoseed binary (default: XOSEED_BIN, else ./xoseed)")
    ap.add_argument("dirs", nargs="*", metavar="DIR", help="where to look for .seed files (default: the conformance cases and seeds/)")
    args = ap.parse_args()
    if not os.path.isfile(args.xoseed):
        print("error: no xoseed binary at %s: build it with 'make xoseed' or pass --xoseed" % args.xoseed, file=sys.stderr)
        return 2
    xoseed = [os.path.abspath(args.xoseed)]
    directories = args.dirs or [os.path.join(HERE, "cases"), os.path.join(ROOT, "seeds")]
    sets, steps, bad = 0, 0, 0
    for paths in groups(directories):
        sets += 1
        found = compare(xoseed, paths)
        kind, plan = python_plan(paths)
        steps += len(plan) if kind == "plan" else 0
        if found:
            bad += 1
            print("DIFFERENT  %s" % ", ".join(os.path.relpath(p) for p in paths))
            for line in found[:8]:
                print("    " + line)
    print("%d set(s) of files, %d step(s) planned by both, %d set(s) differ" % (sets, steps, bad))
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
