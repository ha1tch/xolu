#!/usr/bin/env python3
# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""run.py -- the .seed conformance suite: what a loader must do, checked on a real xolu.

Each directory under cases/ is one case: seed files, a case.json that lists
the steps, and the state the tenant must be in afterwards. For each case a
fresh xolu is started, the steps are run, and what the instance holds is
compared with expected.json. The suite judges any loader that takes the same
command line, so two implementations can be held to the same behaviour:

    XOLU_BIN=/path/to/xolu python3 scripts/seed_conformance/run.py
    python3 scripts/seed_conformance/run.py --xolu /path/to/xolu --loader "xoseed"
    python3 scripts/seed_conformance/run.py --only 04 -v

case.json:
    {"about": "one line",
     "entities": ["brands", "models"],          the entity types to compare
     "ignore": ["users.password_hash"],         fields left out of the comparison
     "steps": [
        {"run": ["load", "a.seed"], "exit": 0, "says": "text the output must contain"},
        {"http": ["PATCH", "/api/v1/brands/1", {"name": "edited"}]}
     ]}

"--url" is added to every "run" step. Exit status: 0 all cases passed, 1 a
case failed, 2 the suite could not run. Python standard library only; needs a
xolu binary (0.30.44 or later), which is why "make test" does not run it: "make test-seeds-live".
"""

import argparse
import http.client
import json
import os
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import time

HERE = os.path.dirname(os.path.abspath(__file__))
SYSTEM_FIELDS = ("id", "_version")


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def call(port, method, path, body=None):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=30)
    try:
        data = json.dumps(body).encode("utf-8") if body is not None else None
        conn.request(method, path, body=data, headers={"Content-Type": "application/json"} if data else {})
        resp = conn.getresponse()
        raw = resp.read()
        try:
            return resp.status, json.loads(raw.decode("utf-8")) if raw else None
        except ValueError:
            return resp.status, raw.decode("utf-8", "replace")
    finally:
        conn.close()


class Xolu:
    """A xolu started on a free port over an empty directory, stopped and removed on exit."""

    def __init__(self, binary):
        self.binary, self.dir, self.proc, self.port = binary, None, None, None

    def __enter__(self):
        self.dir = tempfile.mkdtemp(prefix="seed-conformance-")
        self.port = free_port()
        env = dict(os.environ, XOLU_PORT=str(self.port), XOLU_BASE_DIR=self.dir, XOLU_STORAGE_TYPE="sqlite", XOLU_TENANT_MODE="path",
                   XOLU_TENANT_AUTO_REGISTER="true", XOLU_GRAPH_MODE="flat", XOLU_FULLTEXT_ENABLED="true", XOLU_STRICT_COMMIT="true",
                   XOLU_CASCADING_DELETE="true", XOLU_API_V2_ENABLED="true", XOLU_LOG_LEVEL="error", XOLU_NO_ASCII="true")
        self.log = open(os.path.join(self.dir, "xolu.log"), "w")
        self.proc = subprocess.Popen([self.binary], env=env, cwd=self.dir, stdout=self.log, stderr=subprocess.STDOUT)
        deadline = time.time() + 20
        while time.time() < deadline:
            if self.proc.poll() is not None:
                break
            try:
                if call(self.port, "GET", "/health")[0] < 500:
                    return self
            except OSError:
                time.sleep(0.1)
        self.__exit__(None, None, None)
        raise RuntimeError("xolu did not start (%s)" % self.binary)

    def __exit__(self, *exc):
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()
        if self.log:
            self.log.close()
        if self.dir:
            shutil.rmtree(self.dir, ignore_errors=True)

    @property
    def url(self):
        return "http://127.0.0.1:%d" % self.port


def state(port, entities, ignore, prefix=""):
    """What the instance holds for the given entity types: {entity: {id: data}}."""
    out = {}
    for entity in entities:
        status, body = call(port, "POST", prefix + "/oql/query", {"query": "SELECT id FROM %s" % entity})
        ids = sorted(r["id"] for r in body.get("data", [])) if status == 200 and isinstance(body, dict) else []
        rows = {}
        for i in ids:
            _, row = call(port, "GET", "%s/%s/%d?embed=false" % (prefix, entity, i))
            rows[str(i)] = {k: v for k, v in row.items() if k not in SYSTEM_FIELDS and "%s.%s" % (entity, k) not in ignore}
        out[entity] = rows
    return out


def differences(want, have):
    out = []
    for entity in sorted(set(want) | set(have)):
        w, h = want.get(entity, {}), have.get(entity, {})
        for i in sorted(set(w) | set(h), key=lambda x: int(x)):
            if i not in h:
                out.append("%s/%s is missing" % (entity, i))
            elif i not in w:
                out.append("%s/%s is not expected" % (entity, i))
            else:
                for k in sorted(set(w[i]) | set(h[i])):
                    a, b = w[i].get(k, "<absent>"), h[i].get(k, "<absent>")
                    if a != b or type(a) is not type(b):
                        out.append("%s/%s.%s: expected %s, found %s" % (entity, i, k, json.dumps(a, ensure_ascii=False)[:80], json.dumps(b, ensure_ascii=False)[:80]))
    return out


def run_case(path, loader, binary, update, verbose):
    case = json.load(open(os.path.join(path, "case.json"), encoding="utf-8"))
    problems = []
    with Xolu(binary) as xolu:
        for n, step in enumerate(case["steps"], 1):
            if "http" in step:
                method, target = step["http"][0], step["http"][1]
                status, _ = call(xolu.port, method, target, step["http"][2] if len(step["http"]) > 2 else None)
                if not 200 <= status < 300:
                    problems.append("step %d: %s %s -> HTTP %d" % (n, method, target, status))
                continue
            cmd = loader + step["run"] + ["--url", xolu.url]
            p = subprocess.run(cmd, cwd=path, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, universal_newlines=True)
            if verbose:
                print("    $ %s  -> %d\n%s" % (" ".join(step["run"]), p.returncode, "\n".join("      " + l for l in p.stdout.strip().splitlines()[-12:])))
            if p.returncode != step.get("exit", 0):
                problems.append("step %d (%s): exit %d, expected %d: %s" % (n, " ".join(step["run"]), p.returncode, step.get("exit", 0), p.stdout.strip()[-300:]))
            if step.get("says") and step["says"].lower() not in p.stdout.lower():
                problems.append("step %d (%s): the output does not say \"%s\": %s" % (n, " ".join(step["run"]), step["says"], p.stdout.strip()[-300:]))
        prefix = "/api/v1" + ("/tenant/" + case["tenant"] if case.get("tenant") else "")
        have = state(xolu.port, case.get("entities", []), set(case.get("ignore", [])), prefix)
    expected = os.path.join(path, case.get("expect", "expected.json"))
    if update:
        with open(expected, "w", encoding="utf-8") as fh:
            json.dump(have, fh, ensure_ascii=False, indent=1, sort_keys=True)
            fh.write("\n")
    else:
        want = json.load(open(expected, encoding="utf-8"))
        ignore = set(case.get("ignore", []))
        want = {e: {i: {k: v for k, v in r.items() if "%s.%s" % (e, k) not in ignore} for i, r in rows.items()} for e, rows in want.items()}
        diffs = differences(want, have)
        problems.extend(diffs[:8])
        if len(diffs) > 8:
            problems.append("... and %d more differences" % (len(diffs) - 8))
    return case.get("about", ""), problems


def main(argv=None):
    ap = argparse.ArgumentParser(description="The .seed conformance suite.")
    ap.add_argument("--xolu", default=os.environ.get("XOLU_BIN"), help="the xolu binary (default: XOLU_BIN)")
    ap.add_argument("--loader", default=None, help="the loader's command (default: this repository's scripts/seed.py)")
    ap.add_argument("--only", help="run the cases whose name contains this text")
    ap.add_argument("--update", action="store_true", help="write expected.json from what the loader does (review the change)")
    ap.add_argument("-v", "--verbose", action="store_true", help="show each step's output")
    args = ap.parse_args(argv)
    if not args.xolu or not os.path.isfile(args.xolu):
        print("error: no xolu binary: set XOLU_BIN or pass --xolu (a xolu 0.30.44 or later, built from github.com/ha1tch/xolu)", file=sys.stderr)
        return 2
    loader = shlex.split(args.loader) if args.loader else [sys.executable, os.path.join(os.path.dirname(HERE), "seed.py")]
    cases = sorted(d for d in os.listdir(os.path.join(HERE, "cases")) if os.path.isfile(os.path.join(HERE, "cases", d, "case.json")))
    if args.only:
        cases = [c for c in cases if args.only in c]
    if not cases:
        print("error: no cases", file=sys.stderr)
        return 2
    red = "\033[31m" if sys.stdout.isatty() else ""
    off = "\033[0m" if red else ""
    failed = 0
    for name in cases:
        try:
            about, problems = run_case(os.path.join(HERE, "cases", name), loader, args.xolu, args.update, args.verbose)
        except Exception as exc:        # a case that cannot run is a failed case, and the rest still run
            about, problems = "", ["could not run: %s" % exc]
        if problems:
            failed += 1
            print("%sFAIL%s  %-28s %s" % (red, off, name, about))
            for p in problems:
                print("        " + p)
        else:
            print("%s  %-28s %s" % ("wrote" if args.update else "ok   ", name, about))
    summary = "%d case(s), %d passed, " % (len(cases), len(cases) - failed)
    print(summary + ("%s%d failed%s" % (red, failed, off) if failed else "0 failed"))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
