# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""The command line: load, check, dump, list, info.

Exit status: 0 done; 1 a record failed, or check found differences; 2 bad
input or usage; 3 a guard refused the load before anything was written.
"""

import argparse
import json
import os
import sys

from . import __version__
from .client import Client, default_base_url
from .format import Context, SeedError, SeedRefused, read_seed
from .inspect import check_seed, dump_instance
from .load import OUTCOMES, Loader, seed_name
from .plan import ALL_KINDS, build_plan


def _seeds_dir(args):
    return args.seeds_dir or os.environ.get("XOLU_SEEDS_DIR") or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _all_seeds(directory):
    if not os.path.isdir(directory):
        raise SeedError("seeds directory not found: %s" % directory)
    return sorted(os.path.join(directory, n) for n in os.listdir(directory) if n.endswith(".seed"))


def _subdirs(directory):
    skip = ("seed_system", "conformance", "tests")
    return sorted(os.path.join(directory, n) for n in os.listdir(directory)
                  if os.path.isdir(os.path.join(directory, n)) and n not in skip and not n.startswith((".", "_")))


def _resolve(args):
    directory = _seeds_dir(args)
    paths = []
    for item in args.seeds:
        cands = [item, os.path.join(directory, item), os.path.join(directory, item + ".seed"), os.path.join(directory, "seed-" + item + ".seed")]
        # additions are kept by module in directories of their own: seeds/issues/seed-NAME.seed
        cands += [os.path.join(sub, "seed-" + item + ".seed") for sub in _subdirs(directory)]
        for cand in cands:
            if os.path.isfile(cand):
                paths.append(cand)
                break
        else:
            raise SeedError("seed '%s' not found (looked for it as a path and in %s)" % (item, directory))
    return paths


def _warn(message):
    print("warning: " + message, file=sys.stderr)


def _read(paths, args):
    files = []
    for path in paths:
        header, records, digest = read_seed(path, lenient=args.lenient, warn=_warn)
        files.append((path, header, records, digest))
    whole = [seed_name(p, h) for p, h, _r, _d in files if h.get("exclusive")]
    if len(whole) > 1:
        raise SeedError("seeds \"%s\" and \"%s\" are each a whole dataset (\"exclusive\": true) and cannot be loaded together" % (whole[0], whole[1]))
    return files


def _variables(args):
    out = {}
    for item in args.set or []:
        name, sep, value = item.partition("=")
        if not sep or not name:
            raise SeedError("--set: \"%s\" is not NAME=VALUE" % item)
        try:
            out[name] = json.loads(value)
        except ValueError:
            out[name] = value
    return out


def _client(args):
    return Client(args.url or default_base_url(), api_key=args.api_key, token=args.token, timeout=args.timeout)


def _kinds(text):
    return [k for k in (text or "").split(",") if k]


def _plan(files, ctx, args):
    return build_plan([(p, h, r) for p, h, r, _d in files], ctx, tenant=args.tenant, only=_kinds(args.only), skip=_kinds(args.skip),
                      lenient=args.lenient, safe=args.safe, outside_files=args.allow_outside_files, warn=_warn)


def _counts(stats):
    lines = []
    for kind in sorted(stats):
        parts = ["%s=%d" % (o, stats[kind][o]) for o in OUTCOMES if stats[kind][o]]
        lines.append("  %-14s %s" % (kind, " ".join(parts) or "nothing"))
    return lines


def cmd_list(args):
    rows = []
    directory = _seeds_dir(args)
    for path in _all_seeds(directory) + [p for sub in _subdirs(directory) for p in _all_seeds(sub)]:
        try:
            header, records, _ = read_seed(path, lenient=True)
            rows.append({"file": os.path.relpath(path, directory), "name": seed_name(path, header), "format": header["seed"],
                         "records": len(records), "description": header.get("description", "")})
        except SeedError as exc:
            rows.append({"file": os.path.relpath(path, directory), "error": str(exc)})
    if args.json:
        print(json.dumps(rows, ensure_ascii=False, indent=1))
        return 0
    if not rows:
        print("no .seed files in %s" % _seeds_dir(args))
    for r in rows:
        print("%-46s %s" % (r["file"], r.get("error") or "%5d records  %s" % (r["records"], r["description"])))
    return 0


def cmd_info(args):
    out = []
    for path, header, records, digest in _read(_resolve(args), args):
        kinds = {}
        for rec in records:
            label = rec["kind"] + (":" + rec["entity"] if rec["kind"] in ("entity", "schema") and isinstance(rec.get("entity"), str) else "")
            kinds[label] = kinds.get(label, 0) + 1
        out.append({"file": path, "name": seed_name(path, header), "digest": digest, "records": len(records), "counts": kinds,
                    "header": {k: v for k, v in header.items() if not k.startswith("_")}})
    if args.json:
        print(json.dumps(out if len(out) > 1 else out[0], ensure_ascii=False, indent=1))
        return 0
    for item in out:
        print("%s  (%s)" % (item["name"], item["file"]))
        for key in ("description", "version", "requires_xolu", "tenant", "exclusive"):
            if key in item["header"]:
                print("  %-14s %s" % (key, item["header"][key]))
        print("  %-14s %d" % ("records", item["records"]))
        for label in sorted(item["counts"]):
            print("    %-28s %d" % (label, item["counts"][label]))
    return 0


def cmd_load(args):
    if not args.seeds:
        if not args.all:
            print("Name the seed to load, or pass --all to load every file in the directory. Seeds here:", file=sys.stderr)
            cmd_list(args)
            return 2
        args.seeds = _all_seeds(_seeds_dir(args))
    files = _read(_resolve(args), args)
    given = Context.parse_as_of(args.as_of) if args.as_of else None
    variables = _variables(args)

    if args.dry_run:
        ctx = Context(as_of=given, variables=variables)
        plan = _plan(files, ctx, args)
        for step in plan:
            path = step.get("path_template") or step["path"]
            print("%-6s %s%s" % (step["method"], path, "  " + json.dumps(step["body"], ensure_ascii=False)[:200] if step["body"] is not None else ""))
        print("%d requests would be made (reference time %s); nothing was contacted" % (len(plan), ctx.as_of_text()))
        return 0

    client = _client(args)
    try:
        if args.wait > 0:
            client.wait_ready(args.wait)
        loader = Loader(client, [(p, h, d) for p, h, _r, d in files], tenant=args.tenant, mode="replace" if args.replace else "create",
                        allow_existing=args.allow_existing, undo_on_failure=args.undo_on_failure, keep_going=args.keep_going,
                        force_schemas=args.force_schemas, verbose=args.verbose, partial=bool(args.only or args.skip),
                        say=lambda m: print(m, file=sys.stderr)).inspect()
        ctx = Context(as_of=loader.as_of(given), variables=variables)
        plan = _plan(files, ctx, args)
        result = loader.run(plan, ctx)
    finally:
        client.close()

    if args.json:
        print(json.dumps(result, ensure_ascii=False, indent=1))
    else:
        names = ", ".join(result["seeds"])
        print("%s: %s" % (names, "loaded" if result["ok"] else "NOT fully loaded"))
        print("\n".join(_counts(result["stats"])))
        print("  reference time %s, mode %s, %d requests" % (result["as_of"], result["mode"], result["requests"]))
        if result["failures"]:
            print("\n%d failure(s):" % len(result["failures"]), file=sys.stderr)
            for f in result["failures"]:
                print("  " + f, file=sys.stderr)
            if result["undone"]:
                u = result["undone"]
                print("undo: %d record(s) deleted. %s" % (u["deleted"], u["why"]), file=sys.stderr)
            else:
                print("What was written stays. Fix the cause and run the same command again: records that exist are left "
                      "alone and the rest are loaded.", file=sys.stderr)
        else:
            for _p, header, _r, _d in files:
                if header.get("notes"):
                    print("\n" + header["notes"])
    return 0 if result["ok"] else 1


def cmd_check(args):
    files = _read(_resolve(args), args)
    client = _client(args)
    try:
        if args.wait > 0:
            client.wait_ready(args.wait)
        loader = Loader(client, [(p, h, d) for p, h, _r, d in files], tenant=args.tenant).inspect()
        given = Context.parse_as_of(args.as_of) if args.as_of else None
        as_of = loader.as_of(given)
        ctx = Context(as_of=as_of, variables=_variables(args))
        result = check_seed(client, _plan(files, ctx, args))
    finally:
        client.close()
    result["as_of"] = ctx.as_of_text()
    result["as_of_from"] = "the command line" if given else "the recorded load" if as_of else "now (no load of this seed is recorded)"
    if args.json:
        print(json.dumps(result, ensure_ascii=False, indent=1))
    else:
        print("%d record(s) compared, reference time %s (from %s)" % (result["compared"], result["as_of"], result["as_of_from"]))
        if result["unchecked"]:
            print("%d record(s) without an id were not compared" % result["unchecked"])
        for label in result["missing"][:args.show]:
            print("  missing    %s" % label)
        for item in result["different"][:args.show]:
            print("  different  %s: %s" % (item["record"], ", ".join(item["fields"])))
        for label in result["extra"][:args.show]:
            print("  extra      %s" % label)
        print("same" if result["same"] else "%d missing, %d different, %d extra" % (len(result["missing"]), len(result["different"]), len(result["extra"])))
    return 0 if result["same"] else 1


def cmd_dump(args):
    client = _client(args)
    try:
        if args.wait > 0:
            client.wait_ready(args.wait)
        text, total = dump_instance(client, args.name, tenant=args.tenant, entities=_kinds(args.entities) or None, description=args.description)
    finally:
        client.close()
    if args.output and args.output != "-":
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(text)
        print("%d record(s) written to %s" % (total, args.output), file=sys.stderr)
    else:
        sys.stdout.write(text)
    return 0


def build_parser():
    ap = argparse.ArgumentParser(prog="seed.py", description="Load, check and dump .seed files for xolu (format in docs/SEED_FORMAT.md).")
    ap.add_argument("--version", action="version", version="seed_system " + __version__)
    sub = ap.add_subparsers(dest="command", metavar="COMMAND")

    def common(p, server=True, seeds=True):
        p.add_argument("--seeds-dir", help="where seeds are looked for by name (default: XOLU_SEEDS_DIR, else this directory)")
        p.add_argument("--json", action="store_true", help="print the result as JSON")
        p.add_argument("--lenient", action="store_true", help="warn about unknown kinds and keys instead of stopping")
        if seeds:
            p.add_argument("--tenant", help="override the tenant named in each seed header")
            p.add_argument("--as-of", metavar="WHEN", help="reference day or time for {\"$date\"} and {\"$now\"} (default: the recorded load, else now)")
            p.add_argument("--set", action="append", metavar="NAME=VALUE", help="give a variable its value; repeat for more")
            p.add_argument("--only", help="comma list of kinds to use (" + ", ".join(ALL_KINDS[:4]) + ", ...)")
            p.add_argument("--skip", help="comma list of kinds to leave out")
            p.add_argument("--safe", action="store_true", help="refuse 'call' and 'config' records (for a file from a source that is not trusted)")
            p.add_argument("--allow-outside-files", action="store_true", help="let a body_file point outside the seed's directory")
        if server:
            p.add_argument("--url", "--base-url", dest="url", help="xolu's address (default: XOLU_BASE_URL, else XOLU_HOST/XOLU_PORT/XOLU_ADDR, else http://127.0.0.1:9090)")
            p.add_argument("--api-key", default=os.environ.get("XOLU_API_KEY"), help="sent as X-API-Key (default: XOLU_API_KEY)")
            p.add_argument("--token", default=os.environ.get("XOLU_TOKEN"), help="sent as a Bearer token (default: XOLU_TOKEN)")
            p.add_argument("--wait", type=float, default=15.0, metavar="SECS", help="wait up to SECS for /health first (default 15; 0 to skip)")
            p.add_argument("--timeout", type=float, default=30.0, metavar="SECS", help="per-request timeout (default 30)")

    p = sub.add_parser("load", help="load seeds into a tenant")
    p.add_argument("seeds", nargs="*", metavar="SEED", help="a path, or a name in the seeds directory")
    common(p)
    p.add_argument("--all", action="store_true", help="load every .seed file in the seeds directory")
    p.add_argument("--replace", action="store_true", help="write again the records that already exist (default: leave them alone)")
    p.add_argument("--allow-existing", action="store_true", help="load into a tenant that holds other data")
    p.add_argument("--undo-on-failure", action="store_true", help="if a record fails and the tenant was empty, delete what this run created")
    p.add_argument("--keep-going", action="store_true", help="continue after a failed record")
    p.add_argument("--force-schemas", action="store_true", help="register schemas without checking for a registered one or for rows")
    p.add_argument("--dry-run", action="store_true", help="print the requests that would be made; contact nothing")
    p.add_argument("-v", "--verbose", action="store_true", help="print every request")
    p.set_defaults(func=cmd_load)

    p = sub.add_parser("check", help="compare a tenant with seeds; write nothing")
    p.add_argument("seeds", nargs="+", metavar="SEED")
    common(p)
    p.add_argument("--show", type=int, default=20, metavar="N", help="how many of each kind of difference to print (default 20)")
    p.set_defaults(func=cmd_check)

    p = sub.add_parser("dump", help="write what a tenant holds as a .seed file")
    common(p, seeds=False)
    p.add_argument("--tenant", help="the tenant to read")
    p.add_argument("--name", default="dump", help="the name for the header (default: dump)")
    p.add_argument("--description", help="a description for the header")
    p.add_argument("--entities", help="comma list of entity types (default: all of them)")
    p.add_argument("-o", "--output", help="file to write (default: standard output)")
    p.set_defaults(func=cmd_dump)

    p = sub.add_parser("list", help="list the seeds in the seeds directory")
    common(p, server=False, seeds=False)
    p.set_defaults(func=cmd_list)

    p = sub.add_parser("info", help="show a seed's header and what it holds")
    p.add_argument("seeds", nargs="+", metavar="SEED")
    common(p, server=False, seeds=False)
    p.set_defaults(func=cmd_info)
    return ap


def main(argv=None):
    ap = build_parser()
    args = ap.parse_args(argv)
    if not args.command:
        ap.print_help()
        return 2
    try:
        return args.func(args)
    except SeedRefused as exc:
        print("refused: %s" % exc, file=sys.stderr)
        return 3
    except SeedError as exc:
        print("error: %s" % exc, file=sys.stderr)
        return 2
    except KeyboardInterrupt:
        print("interrupted", file=sys.stderr)
        return 130
