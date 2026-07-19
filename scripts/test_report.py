#!/usr/bin/env python3
# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0

"""Summarise `go test -json` output into partial totals per binary plus a
general total.

Each binary's partial total is the set of top-level tests in its own
`cmd/<name>` package. The shared `pkg/...` tree is reported once as a separate
line (no single binary owns it). The general total is every top-level test in
the run.

For a full release (all three binaries in scope) this prints three partial
lines, the shared line, and the general total. For a single-binary build, pass
just that binary via --binaries; one partial line plus the general total is
shown (and the general total then equals that binary's own tests plus shared).
"""

import argparse
import json
import sys

MODULE = "github.com/ha1tch/xolu"


def classify(package):
    """Map a full package path to a report bucket.

    Returns ("cmd", name) for a binary's own command package,
    ("pkg", None) for the shared library tree, or ("other", None).
    """
    if not package.startswith(MODULE + "/"):
        return ("other", None)
    rel = package[len(MODULE) + 1:]
    if rel.startswith("cmd/"):
        # cmd/<name> or cmd/<name>/<sub> -> attribute to <name>
        name = rel.split("/", 2)[1]
        return ("cmd", name)
    if rel.startswith("pkg/"):
        return ("pkg", None)
    return ("other", None)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", required=True, help="path to go test -json output")
    ap.add_argument("--binaries", default="xolu iolu otogen",
                    help="space-separated binaries in scope for this build")
    args = ap.parse_args()

    binaries = args.binaries.split()
    single = len(binaries) == 1

    # Per-bucket counters: bucket key -> {pass,fail,skip}
    def newc():
        return {"pass": 0, "fail": 0, "skip": 0}

    cmd_counts = {}   # binary name -> counts
    pkg_counts = newc()
    general = newc()
    pkg_failures = []  # package-level failures (compile/setup)

    try:
        with open(args.json) as f:
            lines = f.readlines()
    except OSError as e:
        print(f"  test_report: cannot read {args.json}: {e}", file=sys.stderr)
        return 1

    for line in lines:
        try:
            ev = json.loads(line)
        except ValueError:
            continue
        action = ev.get("Action", "")
        test = ev.get("Test", "")
        package = ev.get("Package", "")

        # Package-level failure (no Test field): surface compile/setup failures.
        if action == "fail" and not test:
            kind, _ = classify(package)
            if kind != "other":
                pkg_failures.append(package.replace(MODULE + "/", ""))
            continue

        # Only count top-level tests (no subtest separator).
        if not test or "/" in test:
            continue
        if action not in ("pass", "fail", "skip"):
            continue

        kind, name = classify(package)
        if kind == "cmd":
            cmd_counts.setdefault(name, newc())[action] += 1
        elif kind == "pkg":
            pkg_counts[action] += 1
        else:
            continue
        general[action] += 1

    def fmt(label, c):
        return (f"  {label:<18} pass={c['pass']} "
                f"skip={c['skip']} fail={c['fail']}")

    if single:
        name = binaries[0]
        c = cmd_counts.get(name, newc())
        print(fmt(f"{name}:", c))
        # For a single binary, the meaningful total is that binary's own tests
        # plus the shared pkg tree that backs it -- not other binaries' cmd
        # tests that happened to run in the same ./... pass.
        combined = {k: c[k] + pkg_counts[k] for k in ("pass", "skip", "fail")}
        print(fmt("total:", combined))
    else:
        for name in binaries:
            print(fmt(f"{name}:", cmd_counts.get(name, newc())))
        print(fmt("shared (pkg):", pkg_counts))
        print(fmt("total:", general))

    for p in pkg_failures:
        print(f"  FAIL {p}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
