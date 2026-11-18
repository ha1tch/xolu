# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""From records to an ordered list of requests.

The kinds, their routes and their phases are those of xolu's own loader
(scripts/seed.py, format version 1), which this module follows record for
record. What is added: keys are checked, placeholders for dates, times and
variables are resolved here, and a name bound with "as" must be bound before
the record that uses it, which is found here and not halfway through a load.
"""

import base64
import hashlib
import heapq
import json
import os
import re
import urllib.parse

from .format import (SYSTEM_FIELDS, SeedError, bindings_in, check_record_keys)

# kind -> route and behaviour. "body" is the record field holding the JSON body,
# or a tuple of record fields that together make up the body.
KINDS = {
    "config":       dict(phase=10, method="PUT",  api="/api/v1", path="/admin/config/{namespace}/{key}", scoped=False, body="body"),
    "schema":       dict(phase=20, method="POST", api="/api/v1", path="/schema/{entity}", scoped=False, body="schema"),
    "seq":          dict(phase=30, method="POST", api="/api/v2", path="/seq", scoped=True, body="body", exists_ok=True),
    "gen":          dict(phase=30, method="POST", api="/api/v2", path="/gen/{type}", scoped=True, body="body", exists_ok=True),
    "bal_account":  dict(phase=30, method="POST", api="/api/v2", path="/bal/def", scoped=True, body="body", exists_ok=True),
    "loc_pattern":  dict(phase=30, method="POST", api="/api/v2", path="/loc/patterns/def", scoped=True, body="body", exists_ok=True),
    "fsm_def":      dict(phase=30, method="POST", api="/api/v2", path="/fsm/def", scoped=True, body="body"),
    "dxp_def":      dict(phase=30, method="POST", api="/api/v2", path="/dxp/def", scoped=True, body="body"),
    "loc":          dict(phase=35, method="POST", api="/api/v2", path="/loc/def", scoped=True, body="body", exists_ok=True),
    "cal_calendar": dict(phase=45, method="POST", api="/api/v2", path="/cal/calendars", scoped=True, body="body", exists_ok=True),
    "edge":         dict(phase=50, method="POST", api="/api/v1", path="/graph/edges", scoped=True, body=("from", "to", "rel", "props")),
    "event_def":    dict(phase=55, method="POST", api="/api/v2", path="/event/def", scoped=True, body="body"),
    "meta":         dict(phase=60, method="PUT",  api="/api/v2", path="/meta/{entity}/{id}/{key}", scoped=True, body=("value", "expires_at")),
    "fsm_machine":  dict(phase=46, method="POST", api="/api/v2", path="/fsm/machine", scoped=True, body="body"),
    "fsm_walk":     dict(phase=47, method="POST", api="/api/v2", path="/fsm/machine/{machine}/walk", scoped=True, body=("input", "payload")),
    "ts_provision": dict(phase=30, method="POST", api="/api/v2", path="/ts/provision", scoped=True, body="body", exists_ok=True),
    "ts_timeline":  dict(phase=35, method="POST", api="/api/v2", path="/ts/tl/def", scoped=True, body="body", exists_ok=True),
    "ts_event":     dict(phase=48, method="POST", api="/api/v2", path="/ts/events", scoped=True, body="body"),
    "ts_batch":     dict(phase=48, method="POST", api="/api/v2", path="/ts/events/batch", scoped=True, body="body"),
    "dxp_txn":      dict(phase=70, method="POST", api="/api/v2", path="/dxp/txn", scoped=True, body="body"),
    "obj":          dict(phase=70, method="POST", api="/api/v2", path="/obj/attach", scoped=True, body="body"),
    "loc_fence":    dict(phase=70, method="POST", api="/api/v2", path="/loc/fences/attach", scoped=True, body="body"),
    "bal_transfer": dict(phase=80, method="POST", api="/api/v2", path="/bal/transfer", scoped=True, body="body"),
    "cal_propose":  dict(phase=80, method="POST", api="/api/v2", path="/cal/propose", scoped=True, body="body"),
    "cal_confirm":  dict(phase=81, method="POST", api="/api/v2", path="/cal/confirm", scoped=True, body="body"),
    "bal_close":    dict(phase=90, method="POST", api="/api/v2", path="/bal/close", scoped=True, body="body"),
}
SPECIAL_KINDS = ("entity", "call", "blob")
ALL_KINDS = tuple(sorted(list(KINDS) + list(SPECIAL_KINDS)))
CALL_PHASE = 75
ENTITY_KEYS = ("entity", "id", "data", "match", "find")
FIND_PHASE = 5
# Kinds, besides entity, whose records may say "match": what the record is found by, and where it is looked for.
MATCHABLE = {"fsm_def": ("name",)}
_NAME = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
CALL_KEYS = ("method", "path", "scoped", "query", "content_type", "headers", "body", "body_file", "body_b64", "body_text")
BLOB_KEYS = ("key", "content_type", "body_file", "body_b64", "body_text")
# Kinds a file from a source that is not trusted may not use (--safe): they reach
# any route, or the server's configuration.
UNSAFE_KINDS = ("call", "config")


def typed_keys(kind):
    spec = KINDS[kind]
    keys = set(re.findall(r"\{(\w+)\}", spec["path"]))
    keys |= set(spec["body"]) if isinstance(spec["body"], tuple) else {spec["body"]}
    return keys


def where_of(rec):
    return "%s:%d" % (rec["_file"], rec["_line"])


def full_path(api, suffix, scoped, tenant):
    prefix = "/tenant/%s" % urllib.parse.quote(tenant, safe="") if (scoped and tenant) else ""
    return "%s%s%s" % (api, prefix, suffix)


def fill_path(template, rec, where):
    def sub(match):
        name = match.group(1)
        if name not in rec:
            raise SeedError("%s: kind '%s' needs the field \"%s\"" % (where, rec["kind"], name))
        value = rec[name]
        if isinstance(value, dict) and set(value) == {"$seed"}:
            return "${" + str(value["$seed"]) + "}"   # resolved when the step runs
        return urllib.parse.quote(str(value), safe="")
    return re.sub(r"\{(\w+)\}", sub, template)


def is_ref(value):
    return isinstance(value, dict) and value.get("type") == "REF"


def refs_in(value):
    if is_ref(value):
        return [value]
    if isinstance(value, list):
        return [v for v in value if is_ref(v)]
    return []


def base_step(rec, phase, label, tenant):
    return {
        "phase": rec.get("phase", phase), "label": label, "kind": rec["kind"], "where": where_of(rec), "tenant": tenant,
        "as": rec.get("as"), "bind": rec.get("bind", "id"), "expect": set(rec.get("expect", [])), "exists_ok": False,
        "headers": {}, "query": None, "body": None, "raw": None, "entity": None, "id": None, "op": "call",
        "file": rec["_file"], "match": None, "key": rec.get("_key") or record_key(rec),
    }


def record_key(rec):
    """What identifies a record across loads of the same file: a digest of its content."""
    content = {k: v for k, v in rec.items() if not k.startswith("_")}
    return hashlib.sha256(json.dumps(content, sort_keys=True, ensure_ascii=False).encode("utf-8")).hexdigest()[:20]


def _match_fields(rec, where, body, allowed=None):
    fields = rec.get("match")
    if not (isinstance(fields, list) and fields and all(isinstance(f, str) and _NAME.match(f) for f in fields)):
        raise SeedError("%s: \"match\" must be a list of field names" % where)
    for f in fields:
        if allowed is not None and f not in allowed:
            raise SeedError("%s: a '%s' record can be matched by %s only" % (where, rec["kind"], ", ".join(allowed)))
        if not isinstance(body, dict) or f not in body:
            raise SeedError("%s: \"match\" names the field \"%s\", which the record does not have" % (where, f))
    return fields


def _read_body_file(rec, where, outside_ok):
    base = os.path.dirname(os.path.abspath(rec["_file"]))
    fpath = os.path.abspath(os.path.join(base, rec["body_file"]))
    if not outside_ok and os.path.commonpath([base, fpath]) != base:
        raise SeedError("%s: body_file \"%s\" is outside the seed's directory (allow it with --allow-outside-files)" % (where, rec["body_file"]))
    try:
        with open(fpath, "rb") as fh:
            return fh.read()
    except OSError as exc:
        raise SeedError("%s: cannot read body_file: %s" % (where, exc))


def plan_typed(rec, tenant):
    kind = rec["kind"]
    spec = KINDS[kind]
    where = where_of(rec)
    step = base_step(rec, spec["phase"], kind, tenant)
    step["method"] = spec["method"]
    step["path_template"] = full_path(spec["api"], fill_path(spec["path"], rec, where), spec["scoped"], tenant)
    step["exists_ok"] = spec.get("exists_ok", False)
    body_spec = spec["body"]
    if isinstance(body_spec, tuple):
        body = {k: rec[k] for k in body_spec if k in rec}
    else:
        if body_spec not in rec:
            raise SeedError("%s: kind '%s' needs the field \"%s\"" % (where, kind, body_spec))
        body = rec[body_spec]
    step["body"] = body
    ident = rec.get("entity") or rec.get("namespace") or rec.get("type") or ""
    step["label"] = ("%s %s" % (kind, ident)).strip()
    if kind == "schema":
        step["guard_schema"] = rec["entity"]
    if "match" in rec and kind in MATCHABLE:
        step["match"] = _match_fields(rec, where, body, MATCHABLE[kind])
        step["list_path"] = step["path_template"]
    return step


def plan_call(rec, tenant, opts):
    where = where_of(rec)
    method = str(rec.get("method", "")).upper()
    path = rec.get("path", "")
    if method not in ("GET", "POST", "PUT", "PATCH", "DELETE"):
        raise SeedError("%s: call needs a method (GET, POST, PUT, PATCH or DELETE)" % where)
    m = re.match(r"^(/api/v[12])(/.*)$", path if isinstance(path, str) else "")
    if not m:
        raise SeedError("%s: call path must start with /api/v1 or /api/v2, got %r" % (where, path))
    step = base_step(rec, CALL_PHASE, "call %s %s" % (method, path), tenant)
    step["method"] = method
    step["path_template"] = full_path(m.group(1), m.group(2), bool(rec.get("scoped")), tenant)
    step["query"] = rec.get("query")
    if "content_type" in rec:
        step["headers"]["Content-Type"] = rec["content_type"]
    if rec.get("headers"):
        step["headers"].update(rec["headers"])
    given = [k for k in ("body", "body_file", "body_b64", "body_text") if k in rec]
    if len(given) > 1:
        raise SeedError("%s: call may have only one of body, body_file, body_b64, body_text" % where)
    if given == ["body"]:
        step["body"] = rec["body"]
    elif given == ["body_file"]:
        step["raw"] = _read_body_file(rec, where, opts.get("outside_files"))
    elif given == ["body_b64"]:
        try:
            step["raw"] = base64.b64decode(rec["body_b64"], validate=True)
        except (ValueError, TypeError) as exc:
            raise SeedError("%s: body_b64 is not valid base64: %s" % (where, exc))
    elif given == ["body_text"]:
        step["raw"] = str(rec["body_text"]).encode("utf-8")
    return step


def plan_blob(rec, tenant, opts):
    """A blob is a raw-body POST with its key in the X-Blob-Key header."""
    where = where_of(rec)
    key = rec.get("key")
    if not isinstance(key, str) or not key:
        raise SeedError("%s: blob needs a \"key\"" % where)
    options = [k for k in ("body_file", "body_b64", "body_text") if k in rec]
    if len(options) != 1:
        raise SeedError("%s: blob needs exactly one of body_file, body_b64, body_text" % where)
    call = {k: rec[k] for k in options + ["_file", "_line", "_key", "as", "bind", "expect"] if k in rec}
    call.update(kind="blob", method="POST", path="/api/v1/blob", scoped=True, headers={"X-Blob-Key": key},
                content_type=rec.get("content_type", "application/octet-stream"))
    step = plan_call(call, tenant, opts)
    step["label"] = "blob %s" % key
    step["phase"] = rec.get("phase", 60)
    return step


def clean_data(rec):
    data = rec.get("data")
    if not isinstance(data, dict):
        raise SeedError("%s: entity needs a \"data\" object" % where_of(rec))
    return {k: v for k, v in data.items() if k not in SYSTEM_FIELDS}


def plan_entities(entity_recs, tenants):
    """Order entity records so REF targets exist first; break cycles with PATCH."""
    steps, idless, ided = [], [], []
    for rec in entity_recs:
        where = where_of(rec)
        if not isinstance(rec.get("entity"), str) or not rec["entity"]:
            raise SeedError("%s: entity record needs an \"entity\" name" % where)
        if "find" in rec:
            # A record that is looked up and never written: what another seed, or the application, put there.
            fields = rec["find"]
            if any(k in rec for k in ("id", "data", "match")):
                raise SeedError("%s: a record with \"find\" is only looked up: it takes no \"id\", \"data\" or \"match\"" % where)
            if not _NAME.match(rec["entity"]):
                raise SeedError("%s: \"find\" needs an entity name made of letters, digits and underscores" % where)
            if not (isinstance(fields, dict) and fields and all(isinstance(f, str) and _NAME.match(f) for f in fields)):
                raise SeedError("%s: \"find\" must be an object of field names and the values they have" % where)
            if not rec.get("as"):
                raise SeedError("%s: a record with \"find\" needs \"as\", the name its id is bound to" % where)
            tenant = tenants[rec["_file"]]
            step = base_step(rec, FIND_PHASE, "entity %s (find)" % rec["entity"], tenant)
            step["method"], step["op"], step["entity"] = "POST", "find", rec["entity"]
            step["path"] = step["query_path"] = full_path("/api/v1", "/oql/query", True, tenant)
            step["body"], step["match"] = dict(fields), sorted(fields)
            step["item_path"] = full_path("/api/v1", "/%s" % urllib.parse.quote(rec["entity"], safe=""), True, tenant)
            steps.append(step)
            continue
        if "id" in rec and (not isinstance(rec["id"], int) or isinstance(rec["id"], bool) or rec["id"] < 0):
            raise SeedError("%s: entity \"id\" must be a non-negative integer" % where)
        if "match" in rec:
            if "id" in rec:
                raise SeedError("%s: an entity has an \"id\" or is found by \"match\", not both" % where)
            if not _NAME.match(rec["entity"]):
                raise SeedError("%s: \"match\" needs an entity name made of letters, digits and underscores" % where)
            _match_fields(rec, where, rec.get("data"))
        (ided if "id" in rec else idless).append(rec)

    def entity_step(rec, data, patch_fields=None):
        tenant = tenants[rec["_file"]]
        ent = rec["entity"]
        quoted = urllib.parse.quote(ent, safe="")
        if patch_fields is not None:
            step = base_step(rec, 41, "entity %s/%d (refs)" % (ent, rec["id"]), tenant)
            step["method"], step["op"] = "PATCH", "patch"
            step["path"] = full_path("/api/v1", "/%s/%d" % (quoted, rec["id"]), True, tenant)
            step["body"], step["as"] = patch_fields, None
        elif "id" in rec:
            step = base_step(rec, 40, "entity %s/%d" % (ent, rec["id"]), tenant)
            step["method"], step["op"] = "POST", "save"
            step["path"] = full_path("/api/v1", "/%s/save/%d" % (quoted, rec["id"]), True, tenant)
            step["read_path"] = full_path("/api/v1", "/%s/%d" % (quoted, rec["id"]), True, tenant)
            step["body"] = data
        else:
            step = base_step(rec, 39, "entity %s (new)" % ent, tenant)
            step["method"], step["op"] = "POST", "match" if "match" in rec else "create"
            step["path"] = full_path("/api/v1", "/%s" % quoted, True, tenant)
            step["body"] = data
            step["match"] = rec.get("match")
            step["query_path"] = full_path("/api/v1", "/oql/query", True, tenant)
        step["entity"], step["id"] = ent, rec.get("id")
        step["item_path"] = full_path("/api/v1", "/%s" % quoted, True, tenant)
        return step

    for rec in idless:
        steps.append(entity_step(rec, clean_data(rec)))

    keys = {}
    for rec in ided:
        key = (tenants[rec["_file"]], rec["entity"], rec["id"])
        if key in keys:
            raise SeedError("%s: duplicate entity %s/%d (first at %s)" % (where_of(rec), rec["entity"], rec["id"], where_of(keys[key])))
        keys[key] = rec

    deps = {}
    for key, rec in keys.items():
        found = set()
        for value in clean_data(rec).values():
            for ref in refs_in(value):
                target = (key[0], ref.get("entity"), ref.get("id"))
                if target in keys:
                    found.add(target)
        deps[key] = found

    order = {key: i for i, key in enumerate(keys)}
    dependents = {key: [] for key in keys}
    indegree = {}
    for key, found in deps.items():
        indegree[key] = len(found)
        for target in found:
            dependents[target].append(key)
    heap = [(order[k], k) for k, n in indegree.items() if n == 0]
    heapq.heapify(heap)
    done = set()
    while heap:
        _, key = heapq.heappop(heap)
        done.add(key)
        steps.append(entity_step(keys[key], clean_data(keys[key])))
        for nxt in dependents[key]:
            indegree[nxt] -= 1
            if indegree[nxt] == 0:
                heapq.heappush(heap, (order[nxt], nxt))

    remaining = [k for k in keys if k not in done]
    remaining_set = set(remaining)
    patches = []
    for key in remaining:
        rec = keys[key]
        data = clean_data(rec)
        deferred = {}
        for field, value in list(data.items()):
            targets = [(key[0], r.get("entity"), r.get("id")) for r in refs_in(value)]
            if any(t in remaining_set for t in targets):
                deferred[field] = value
                del data[field]
        steps.append(entity_step(rec, data))
        if deferred:
            patches.append(entity_step(rec, None, patch_fields=deferred))
    steps.extend(patches)
    return steps


def build_plan(files, ctx, tenant=None, only=None, skip=None, lenient=False, safe=False, outside_files=False, warn=None):
    """Return the ordered list of steps for the files given as (path, header, records).

    Raises SeedError for anything that can be known without the server: an
    unknown kind or key, an entity type the header does not list, a placeholder
    with no value, a binding used before the record that makes it.
    """
    warn = warn or (lambda message: None)
    opts = {"outside_files": outside_files}
    tenants, plan, entity_recs, passes = {}, [], [], {}
    for path, header, records in files:
        tenants[path] = tenant or header.get("tenant")
        declared = header.get("entities")
        variables = dict(header.get("vars") or {})
        variables.update(ctx.variables)
        file_ctx = type(ctx)(as_of=ctx.as_of, variables=variables)
        # A file that adds to others runs after them, as a pass of its own, in the order the files were named:
        # what it looks up with "find" has to be there, also when both are named in one load.
        if header.get("addition") or header.get("requires"):
            passes[path] = 1 + len([p for p in passes.values() if p])
        else:
            passes[path] = 0
        for rec in records:
            kind = rec["kind"]
            where = where_of(rec)
            if kind not in ALL_KINDS:
                if lenient:
                    warn("%s: unknown kind '%s' skipped" % (where, kind))
                    continue
                raise SeedError("%s: unknown kind '%s'. Known kinds: %s" % (where, kind, ", ".join(ALL_KINDS)))
            if safe and kind in UNSAFE_KINDS:
                raise SeedError("%s: a '%s' record is not allowed with --safe" % (where, kind))
            allowed = ENTITY_KEYS if kind == "entity" else CALL_KEYS if kind == "call" else BLOB_KEYS if kind == "blob" else typed_keys(kind)
            if kind in MATCHABLE:
                allowed = set(allowed) | {"match"}
            check_record_keys(rec, allowed, lenient, warn)
            rec["_key"] = record_key(rec)       # taken before the placeholders are resolved, so it does not move with the clock
            for key in list(rec):
                if not key.startswith("_") and key != "kind":
                    rec[key] = file_ctx.resolve(rec[key], where)
            if kind in ("entity", "schema") and declared is not None and "find" not in rec and rec.get("entity") not in declared:
                raise SeedError("%s: entity type \"%s\" is not in the header's \"entities\" list" % (where, rec.get("entity")))
            if kind == "entity":
                entity_recs.append(rec)
            elif kind == "call":
                plan.append(plan_call(rec, tenants[path], opts))
            elif kind == "blob":
                plan.append(plan_blob(rec, tenants[path], opts))
            else:
                plan.append(plan_typed(rec, tenants[path]))
    plan.extend(plan_entities(entity_recs, tenants))

    only, skip = set(only or ()), set(skip or ())
    for name in only | skip:
        if name not in ALL_KINDS:
            raise SeedError("--only/--skip: unknown kind '%s'" % name)
    plan = [s for s in plan if (not only or s["kind"] in only) and s["kind"] not in skip]
    plan.sort(key=lambda s: (passes.get(s["file"], 0), s["phase"]))

    seen = {}
    for step in plan:
        # two identical records in a file are two things: the second gets its own key
        n = seen[(step["file"], step["key"])] = seen.get((step["file"], step["key"]), 0) + 1
        if step["op"] == "patch":
            step["key"] += "/refs"
        elif n > 1:
            step["key"] += "#%d" % n
        # what the load record remembers having run: everything but records that can be read back by their id
        step["tracked"] = step["op"] not in ("save", "patch", "find") and step["kind"] != "schema"

    bound = set()
    for step in plan:
        used = bindings_in(step["body"]) | bindings_in(step["query"]) | set(re.findall(r"\$\{(\w+)\}", step.get("path_template") or step.get("path") or ""))
        for name in sorted(used - bound):
            raise SeedError("%s: \"%s\" is used here before any record binds it with \"as\"; records run by phase, then in "
                            "file order (docs/SEED_FORMAT.md, \"Order\")" % (step["where"], name))
        if step["as"]:
            bound.add(step["as"])
    return plan
