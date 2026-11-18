# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""Tests of seed_system that need no server: the file format, the placeholders and the plan.

    python3 -m unittest discover -s scripts/seed_tests

What a load does to a real xolu is covered by scripts/seed_conformance/run.py.
"""
import datetime
import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from seed_system import Context, SeedError, build_plan, read_seed, write_record  # noqa: E402
from seed_system.cli import main  # noqa: E402

SEEDS = os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))), "seeds")
AS_OF = datetime.datetime(2026, 1, 31, 10, 0, 0)


class Files(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.dir.cleanup()

    def seed(self, text, name="t.seed", raw=False):
        path = os.path.join(self.dir.name, name)
        with open(path, "wb") as fh:
            fh.write(text if raw else text.encode("utf-8"))
        return path

    def plan(self, text, **kw):
        path = self.seed(text)
        header, records, _ = read_seed(path)
        return build_plan([(path, header, records)], Context(as_of=AS_OF, variables=kw.pop("variables", None)), **kw)

    def refused(self, text, says, **kw):
        with self.assertRaises(SeedError) as caught:
            self.plan(text, **kw)
        self.assertIn(says, str(caught.exception))


class Format(Files):
    def test_one_record_per_line_and_over_several(self):
        path = self.seed('# a comment\n{"seed": 2,\n  "name": "x"\n  }\n\n{"kind": "entity", "entity": "a", "id": 1, "data": {\n\t"n": 1\n  }}\n{"kind": "entity", "entity": "a", "id": 2, "data": {}}\n')
        header, records, digest = read_seed(path)
        self.assertEqual(header["name"], "x")
        self.assertEqual([r["id"] for r in records], [1, 2])
        self.assertEqual([r["_line"] for r in records], [6, 9])
        self.assertEqual(len(digest), 64)

    def test_byte_order_mark_and_crlf(self):
        _, records, _ = read_seed(self.seed(b'\xef\xbb\xbf{"seed": 1}\r\n{"kind": "entity", "entity": "a", "id": 1, "data": {}}\r\n', raw=True))
        self.assertEqual(len(records), 1)

    def test_what_is_refused(self):
        for text, says in (('{"kind": "entity"}\n', "first record must be the header"),
                           ('{"seed": 3}\n', "unsupported seed format version"),
                           ('{"seed": true}\n', "unsupported seed format version"),
                           ('{"seed": 2, "tennant": "x"}\n', 'unknown header key "tennant"'),
                           ('{"seed": 1, "description": "x"}\n', 'declare "seed": 2'),
                           ('{"seed": 1}\n{"kind": "entity", "entity": "a",\n  "id": 1, "data": {}}\n', 'need "seed": 2'),
                           ('{"seed": 2}\n{"entity": "a"}\n', 'has no "kind"'),
                           ('{"seed": 2}\n[1]\n', "must be a JSON object"),
                           ('{"seed": 2}\n{"kind": \n', "invalid JSON"),
                           ('  {"seed": 2}\n', "continuation line with no record"),
                           ('{"seed": 2, "entities": "a"}\n', "must be a list of names"),
                           ('', "empty seed file")):
            with self.assertRaises(SeedError, msg=text) as caught:
                read_seed(self.seed(text))
            self.assertIn(says, str(caught.exception))

    def test_a_comment_ends_a_record(self):
        with self.assertRaises(SeedError):
            read_seed(self.seed('{"seed": 2}\n{"kind": "entity", "entity": "a", "id": 1, "data": {\n# no comments inside a record\n  "n": 1}}\n'))

    def test_lenient_warns(self):
        seen = []
        header, _, _ = read_seed(self.seed('{"seed": 2, "tennant": "x"}\n'), lenient=True, warn=seen.append)
        self.assertEqual(len(seen), 1)

    def test_written_records_read_back(self):
        rec = {"kind": "entity", "entity": "assets", "id": 1, "data": {"name": "x" * 90, "list": [{"a": "y" * 70, "b": [1, 2, 3]}] * 3, "due": {"$date": "+1d"}, "none": None}}
        text = write_record({"seed": 2, "name": "w", "description": "d" * 120, "entities": ["assets"]}) + "\n" + write_record(rec) + "\n"
        self.assertTrue(all(len(line) < 400 for line in text.splitlines()))
        header, records, _ = read_seed(self.seed(text))
        self.assertEqual({k: v for k, v in records[0].items() if not k.startswith("_")}, rec)
        self.assertEqual(header["entities"], ["assets"])


class Placeholders(Files):
    def test_dates_and_times(self):
        ctx = Context(as_of=AS_OF)
        for arg, want in (("", "2026-01-31"), ("+1d", "2026-02-01"), ("-1w", "2026-01-24"), ("+1mo", "2026-02-28"), ("+13mo", "2027-02-28"),
                          ("-2y", "2024-01-31"), ("+ 90 d", "2026-05-01"), ("-2dT08:30:00Z", "2026-01-29T08:30:00Z"), ("T06:00:00Z", "2026-01-31T06:00:00Z")):
            self.assertEqual(ctx.resolve({"$date": arg}, "t"), want, arg)
        for arg, want in (("", "2026-01-31T10:00:00Z"), ("-2h", "2026-01-31T08:00:00Z"), ("+30m", "2026-01-31T10:30:00Z"), ("-45s", "2026-01-31T09:59:15Z"), ("+1d", "2026-02-01T10:00:00Z")):
            self.assertEqual(ctx.resolve({"$now": arg}, "t"), want, arg)
        leap = Context(as_of=datetime.datetime(2024, 2, 29))
        self.assertEqual(leap.resolve({"$date": "+1y"}, "t"), "2025-02-28")

    def test_bad_offsets(self):
        for arg in ("tomorrow", "+1", "1d", "+1.5d", "+1q", 5, None, "+99999y"):
            with self.assertRaises(SeedError, msg=repr(arg)):
                Context(as_of=AS_OF).resolve({"$date": arg}, "t")

    def test_text_variables_and_nesting(self):
        ctx = Context(as_of=AS_OF, variables={"tenant_id": 7})
        got = ctx.resolve({"a": [{"m": {"$text": "due ${date:+30d}, tenant ${var:tenant_id}, at ${now:}"}}], "t": {"$var": "tenant_id"}, "keep": {"$seed": "x"}, "two": {"$date": "", "x": 1}}, "t")
        self.assertEqual(got, {"a": [{"m": "due 2026-03-02, tenant 7, at 2026-01-31T10:00:00Z"}], "t": 7, "keep": {"$seed": "x"}, "two": {"$date": "", "x": 1}})
        with self.assertRaises(SeedError):
            ctx.resolve({"$var": "missing"}, "t")

    def test_as_of(self):
        self.assertEqual(Context.parse_as_of("2026-11-17"), datetime.datetime(2026, 11, 17))
        self.assertEqual(Context.parse_as_of("2026-11-17T08:30:00Z"), datetime.datetime(2026, 11, 17, 8, 30))
        with self.assertRaises(SeedError):
            Context.parse_as_of("yesterday")

    def test_header_defaults_and_set(self):
        text = '{"seed": 2, "vars": {"t": 1, "r": "north"}}\n{"kind": "entity", "entity": "a", "id": 1, "data": {"t": {"$var": "t"}, "r": {"$var": "r"}}}\n'
        self.assertEqual(self.plan(text)[0]["body"], {"t": 1, "r": "north"})
        self.assertEqual(self.plan(text, variables={"t": 9})[0]["body"], {"t": 9, "r": "north"})


class Plan(Files):
    H = '{"seed": 2}\n'

    def test_phases_and_file_order(self):
        plan = self.plan(self.H + '{"kind": "entity", "entity": "b", "id": 1, "data": {}}\n{"kind": "schema", "entity": "b", "schema": {}}\n'
                         '{"kind": "entity", "entity": "a", "as": "x", "data": {}}\n{"kind": "call", "method": "GET", "path": "/api/v1/a"}\n')
        self.assertEqual([(s["kind"], s["op"]) for s in plan], [("schema", "call"), ("entity", "create"), ("entity", "save"), ("call", "call")])
        self.assertEqual(plan[2]["path"], "/api/v1/b/save/1")

    def test_tenant_scope(self):
        text = '{"seed": 2, "tenant": "acme"}\n{"kind": "entity", "entity": "a", "id": 1, "data": {}}\n{"kind": "schema", "entity": "a", "schema": {}}\n'
        plan = self.plan(text)
        self.assertEqual(plan[0]["path_template"], "/api/v1/schema/a")          # schemas are not scoped
        self.assertEqual(plan[1]["path"], "/api/v1/tenant/acme/a/save/1")
        self.assertEqual(self.plan(text, tenant="other")[1]["path"], "/api/v1/tenant/other/a/save/1")

    def test_match_and_find(self):
        plan = self.plan(self.H + '{"kind": "entity", "entity": "notes", "match": ["code"], "data": {"code": "N-1", "asset_id": {"$seed": "pump"}}}\n'
                         '{"kind": "entity", "entity": "assets", "find": {"tag": "A-1"}, "as": "pump"}\n'
                         '{"kind": "fsm_def", "match": ["name"], "as": "d", "body": {"name": "life"}}\n')
        self.assertEqual([(s["kind"], s["op"], s["tracked"]) for s in plan], [("entity", "find", False), ("fsm_def", "call", True), ("entity", "match", True)])
        self.assertEqual(plan[0]["body"], {"tag": "A-1"})        # a lookup runs before everything, so what it binds can be used anywhere
        for text, says in (('{"kind": "entity", "entity": "a", "find": {"tag": "x"}}\n', 'needs "as"'),
                           ('{"kind": "entity", "entity": "a", "find": {"tag": "x"}, "as": "n", "data": {}}\n', "only looked up"),
                           ('{"kind": "entity", "entity": "a", "find": {}, "as": "n"}\n', "must be an object"),
                           ('{"kind": "entity", "entity": "a", "id": 1, "match": ["c"], "data": {"c": 1}}\n', "not both"),
                           ('{"kind": "entity", "entity": "a", "match": ["c"], "data": {"d": 1}}\n', "does not have"),
                           ('{"kind": "fsm_def", "match": ["description"], "body": {"description": "x"}}\n', "by name only")):
            self.refused(self.H + text, says)
        # a lookup is not something the seed writes, so its entity type need not be in the header's list
        self.plan('{"seed": 2, "entities": ["notes"]}\n{"kind": "entity", "entity": "assets", "find": {"tag": "A-1"}, "as": "p"}\n')

    def test_an_addition_runs_after_what_it_adds_to(self):
        base = self.seed('{"seed": 2, "name": "base"}\n{"kind": "entity", "entity": "assets", "id": 1, "data": {"tag": "A-1"}}\n', "base.seed")
        add = self.seed('{"seed": 2, "name": "add", "requires": ["base"]}\n{"kind": "entity", "entity": "assets", "find": {"tag": "A-1"}, "as": "p"}\n'
                        '{"kind": "config", "namespace": "n", "key": "k", "body": {"v": {"$seed": "p"}}}\n', "add.seed")
        for order in ((add, base), (base, add)):
            files = [(f,) + read_seed(f)[:2] for f in order]
            plan = build_plan(files, Context(as_of=AS_OF))
            # the lookup has the lowest phase of all, and still runs after the record it looks for
            self.assertEqual([(os.path.basename(s["file"]), s["op"]) for s in plan], [("base.seed", "save"), ("add.seed", "find"), ("add.seed", "call")])

    def test_the_same_record_twice_is_two_records(self):
        walk = '{"kind": "fsm_walk", "machine": 7, "input": "hold"}\n'
        plan = self.plan(self.H + walk + '{"kind": "fsm_walk", "machine": 7, "input": "resume"}\n' + walk)
        keys = [s["key"] for s in plan]
        self.assertEqual(len(set(keys)), 3)
        self.assertEqual(keys[2], keys[0] + "#2")

    def test_refs_order_and_cycles(self):
        ref = lambda e, i: json.dumps({"type": "REF", "entity": e, "id": i})
        plan = self.plan(self.H + '{"kind": "entity", "entity": "p", "id": 2, "data": {"partner": %s, "skill": %s}}\n'
                         '{"kind": "entity", "entity": "p", "id": 1, "data": {"partner": %s}}\n{"kind": "entity", "entity": "s", "id": 1, "data": {}}\n' % (ref("p", 1), ref("s", 1), ref("p", 2)))
        self.assertEqual([(s["entity"], s["id"], s["op"]) for s in plan], [("s", 1, "save"), ("p", 2, "save"), ("p", 1, "save"), ("p", 2, "patch"), ("p", 1, "patch")])
        self.assertEqual(plan[1]["body"], {"skill": {"type": "REF", "entity": "s", "id": 1}})

    def test_what_is_refused(self):
        e = '{"kind": "entity", "entity": "a", "id": 1, "data": {}}\n'
        for text, says in ((self.H + '{"kind": "entty", "entity": "a"}\n', "unknown kind 'entty'"),
                           (self.H + '{"kind": "entity", "entity": "a", "id": 1, "data": {}, "expct": []}\n', 'unknown key "expct"'),
                           (self.H + e + e, "duplicate entity a/1"),
                           (self.H + '{"kind": "entity", "entity": "a", "id": -1, "data": {}}\n', "non-negative integer"),
                           (self.H + '{"kind": "entity", "entity": "a", "id": 1}\n', 'needs a "data" object'),
                           ('{"seed": 2, "entities": ["a"]}\n{"kind": "entity", "entity": "b", "id": 1, "data": {}}\n', 'not in the header\'s "entities" list'),
                           (self.H + '{"kind": "entity", "entity": "a", "data": {"x": {"$seed": "later"}}}\n{"kind": "entity", "entity": "b", "as": "later", "data": {}}\n', '"later" is used here before'),
                           (self.H + '{"kind": "call", "method": "GET", "path": "/api/v1/a/${nobody}"}\n', '"nobody" is used here before'),
                           (self.H + '{"kind": "call", "method": "GET", "path": "/elsewhere"}\n', "must start with /api/v1 or /api/v2"),
                           (self.H + '{"kind": "meta", "entity": "a", "id": 1, "value": true}\n', 'needs the field "key"'),
                           (self.H + '{"kind": "entity", "entity": "a", "id": 1, "data": {"v": {"$var": "nope"}}}\n', 'variable "nope" has no value'),
                           (self.H + '{"kind": "call", "method": "POST", "path": "/api/v1/a", "body_file": "../x"}\n', "outside the seed's directory")):
            self.refused(text, says)
        self.refused(self.H + '{"kind": "config", "namespace": "n", "key": "k", "body": {}}\n', "not allowed with --safe", safe=True)
        self.refused(self.H + e, "unknown kind 'nothing'", only=["nothing"])

    def test_only_and_skip(self):
        text = self.H + '{"kind": "schema", "entity": "a", "schema": {}}\n{"kind": "entity", "entity": "a", "id": 1, "data": {}}\n'
        self.assertEqual([s["kind"] for s in self.plan(text, skip=["schema"])], ["entity"])
        self.assertEqual([s["kind"] for s in self.plan(text, only=["schema"])], ["schema"])


class Repository(unittest.TestCase):
    """The seeds this repository ships and the command that loads them."""

    def test_the_example_reads_and_plans(self):
        path = os.path.join(SEEDS, "example.seed")
        header, records, _ = read_seed(path)
        self.assertEqual(header["seed"], 1)                       # a version 1 file: still valid, and means the same
        plan = build_plan([(path, header, records)], Context(as_of=AS_OF))
        # one step for each record, and the two patches that close the cycle of references between persons 1 and 2
        self.assertEqual(len(plan), len(records) + 2)
        self.assertEqual([s["op"] for s in plan if s["kind"] == "entity"], ["save", "save", "save", "patch", "patch"])

    def test_command_line_without_a_server(self):
        null = open(os.devnull, "w")
        old = sys.stdout, sys.stderr
        sys.stdout = sys.stderr = null
        try:
            self.assertEqual(main(["list", "--seeds-dir", SEEDS]), 0)
            self.assertEqual(main(["info", "example", "--seeds-dir", SEEDS, "--json"]), 0)
            self.assertEqual(main(["load", "example", "--seeds-dir", SEEDS, "--dry-run"]), 0)
            self.assertEqual(main(["load", "--seeds-dir", SEEDS]), 2)                                   # no seed named
            self.assertEqual(main(["load", "nothing-like-this", "--seeds-dir", SEEDS, "--dry-run"]), 2)
        finally:
            sys.stdout, sys.stderr = old
            null.close()


if __name__ == "__main__":
    unittest.main()
