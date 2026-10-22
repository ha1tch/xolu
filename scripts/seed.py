#!/usr/bin/env python3
"""
seed.py -- load .seed files into a running xolu instance.

Standard library only (no curl, no third-party packages). Normally run via
scripts/seed.sh, which points it at <repo>/seeds.

  seed.py [options] [SEED ...]

  SEED is a path, or a name resolved inside --seeds-dir ("demo" -> demo.seed).
  With no SEED, every *.seed file in --seeds-dir is loaded, in name order.

THE .seed FORMAT (version 1)
----------------------------
A .seed file is UTF-8 text, one JSON object per line. Blank lines and lines
starting with '#' are ignored. The first record is the header; every other
record has a "kind". Bodies are the JSON that the xolu API itself accepts,
passed through verbatim, so a record can be produced from any GET response.

  {"seed": 1, "name": "demo", "tenant": "acme"}        header ("tenant" optional)
  {"kind": "schema", "entity": "person", "schema": {...JSON schema...}}
  {"kind": "entity", "entity": "person", "id": 7, "data": {...}}
  {"kind": "edge", "from": "person:7", "to": "person:9", "rel": "knows", "props": {}}
  {"kind": "meta", "entity": "person", "id": 7, "key": "flag", "value": true}
  {"kind": "seq", "body": {"name": "invoice_no", "start": 1000}}

Optional fields on any record:
  "as": "name"      bind a value from the response (default field: "id")
  "bind": "a.b"     dotted path to bind instead of "id" (used with "as")
  "phase": N        override the ordering phase (see below)
  "expect": [404]   extra HTTP statuses to treat as success

Placeholders: any JSON value {"$seed": "name"} is replaced by the value bound
as "name"; in a "call" path, ${name} is replaced. This is how records refer to
objects whose IDs the server assigns (FSM definitions, events, machines, ...).

Kinds and the order they run in (lower phase first; file order within a phase,
across all files, so a file need not list things in dependency order):

  10 config        namespace, key, body                  PUT admin/config
  20 schema        entity, schema                        (guarded, see Schemas)
  30 seq gen bal_account loc_pattern fsm_def dxp_def ts_provision   body
  35 loc ts_timeline                                      body
  39/40/41 entity  entity, [id], data   (see below)
  45 cal_calendar                                         body
  46 fsm_machine                                          body
  47 fsm_walk      machine, input, [payload]
  48 ts_event ts_batch                                    body
  50 edge          from, to, rel, [props]
  55 event_def                                            body
  60 meta          entity, id, key, value, [expires_at]
  60 blob          key, [content_type], body_file | body_b64 | body_text
  70 dxp_txn obj loc_fence                                body
  75 call          method, path, ... (generic, see below)
  80 bal_transfer cal_propose                             body
  81 cal_confirm                                          body
  90 bal_close                                            body

Event defs run after the data on purpose. They fire on entity changes and FSM
outputs, so creating them late stops a seed from triggering webhooks or OQL
actions while it loads. For the same reason machines are created and walked,
and timeseries events appended, before event defs exist: replaying a machine's
history to restore its state is silent. fsm_walk replays one transition:
{"kind":"fsm_walk","machine":{"$seed":"m1"},"input":"go","payload":{}}.

Entities. With an "id", the record is saved under that id (POST /{entity}/save
/{id}), so IDs survive a round trip. Without one, it is created and the new id
can be bound with "as". REF values ({"type":"REF","entity":..,"id":..}) are
written in dependency order, so a record's targets exist first. A reference
cycle (including self-reference) is broken by saving the cycle members without
their REF fields and PATCHing the REF fields in afterwards. Records without an
"id" are created first, in file order. Server-managed fields cannot be set
through the public API and are dropped from "data": id and _version.

Generic call, for anything without a typed kind:
  {"kind": "call", "method": "POST", "path": "/api/v1/blob", "scoped": true,
   "query": {"key": "logo"}, "content_type": "image/png", "body_file": "logo.png"}
Body options: "body" (JSON), "body_file" (path relative to the .seed file),
"body_b64", "body_text". "scoped": true inserts /tenant/<tenant> after the
/api/vN segment. Paths must start with /api/v1 or /api/v2.

Tenants. "tenant" in the header (or --tenant) selects the tenant for every
tenant-scoped kind. Schemas and config are global in xolu and are never scoped.

Re-running. Entities and meta are upserts; schemas are guarded (next paragraph). For definition kinds (seq,
gen, bal_account, loc_pattern, loc, cal_calendar) HTTP 409 counts as "already
exists" and is skipped. Anything else that fails stops the run, unless
--keep-going is given.

Schemas. Registering a schema over an entity type that already holds rows hides
those rows (docs/TRACKING.md, XOT244). So before each schema the seed asks the
server: a schema that is already registered is skipped; a type with no schema
but with rows (in the seed's tenant) is refused with an error; a fresh type is
registered. --force-schemas turns the guard off. Rows in other tenants cannot be
seen from here, so the guard is a safeguard, not a proof.

Exit status: 0 success, 1 one or more records failed, 2 bad input/usage.
"""

import argparse
import base64
import heapq
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

FORMAT_VERSION = 1
DEFAULT_PORT = 9090
SYSTEM_FIELDS = ("id", "_version")

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
CALL_PHASE = 75


class SeedError(Exception):
    """Bad input or an unrecoverable problem; reported without a traceback."""


# ---------------------------------------------------------------------------
# Loading
# ---------------------------------------------------------------------------

def load_seed_file(path):
    """Return (header, records) for one .seed file."""
    header = None
    records = []
    try:
        fh = open(path, encoding="utf-8")
    except OSError as exc:
        raise SeedError(f"cannot read {path}: {exc}")
    with fh:
        for lineno, raw in enumerate(fh, 1):
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            try:
                obj = json.loads(line)
            except ValueError as exc:
                raise SeedError(f"{path}:{lineno}: invalid JSON: {exc}")
            if not isinstance(obj, dict):
                raise SeedError(f"{path}:{lineno}: each record must be a JSON object")
            if header is None:
                if "seed" not in obj:
                    raise SeedError(f"{path}:{lineno}: the first record must be the header, e.g. {{\"seed\": 1}}")
                if obj["seed"] != FORMAT_VERSION:
                    raise SeedError(f"{path}:{lineno}: unsupported seed format version {obj['seed']!r} (this seed.py reads version {FORMAT_VERSION})")
                header = obj
                continue
            if "kind" not in obj:
                raise SeedError(f"{path}:{lineno}: record has no \"kind\"")
            obj["_file"] = path
            obj["_line"] = lineno
            records.append(obj)
    if header is None:
        raise SeedError(f"{path}: empty seed file (no header record)")
    return header, records


def discover(args):
    """Resolve the list of seed file paths from the arguments."""
    seeds_dir = args.seeds_dir
    if args.seeds:
        paths = []
        for item in args.seeds:
            candidates = [item, os.path.join(seeds_dir, item), os.path.join(seeds_dir, item + ".seed")]
            for cand in candidates:
                if os.path.isfile(cand):
                    paths.append(cand)
                    break
            else:
                raise SeedError(f"seed '{item}' not found (looked for it as a path and in {seeds_dir})")
        return paths
    if not os.path.isdir(seeds_dir):
        raise SeedError(f"seeds directory not found: {seeds_dir}")
    paths = sorted(
        os.path.join(seeds_dir, n) for n in os.listdir(seeds_dir) if n.endswith(".seed")
    )
    if not paths:
        raise SeedError(f"no .seed files in {seeds_dir}")
    return paths


# ---------------------------------------------------------------------------
# Placeholders and path helpers
# ---------------------------------------------------------------------------

def resolve(value, bindings, where):
    """Replace {"$seed": name} anywhere inside a JSON value."""
    if isinstance(value, dict):
        if set(value) == {"$seed"}:
            name = value["$seed"]
            if name not in bindings:
                raise SeedError(f"{where}: placeholder {{\"$seed\": \"{name}\"}} has no binding yet (bind it with \"as\" in an earlier record)")
            return bindings[name]
        return {k: resolve(v, bindings, where) for k, v in value.items()}
    if isinstance(value, list):
        return [resolve(v, bindings, where) for v in value]
    return value


def interpolate(text, bindings, where):
    def sub(match):
        name = match.group(1)
        if name not in bindings:
            raise SeedError(f"{where}: ${{{name}}} has no binding yet")
        return str(bindings[name])
    return re.sub(r"\$\{(\w+)\}", sub, text)


def fill_path(template, rec, where):
    def sub(match):
        name = match.group(1)
        if name not in rec:
            raise SeedError(f"{where}: kind '{rec['kind']}' needs the field \"{name}\"")
        value = rec[name]
        if isinstance(value, dict) and set(value) == {"$seed"}:
            return "${" + str(value["$seed"]) + "}"   # resolved when the step runs
        return urllib.parse.quote(str(value), safe="")
    return re.sub(r"\{(\w+)\}", sub, template)


def full_path(api, suffix, scoped, tenant):
    prefix = f"/tenant/{urllib.parse.quote(tenant, safe='')}" if (scoped and tenant) else ""
    return f"{api}{prefix}{suffix}"


def dig(obj, dotted):
    cur = obj
    for part in dotted.split("."):
        if isinstance(cur, dict) and part in cur:
            cur = cur[part]
        else:
            raise KeyError(dotted)
    return cur


# ---------------------------------------------------------------------------
# Planning: turn records into ordered steps
# ---------------------------------------------------------------------------

def where_of(rec):
    return f"{rec['_file']}:{rec['_line']}"


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
        "phase": rec.get("phase", phase),
        "label": label,
        "kind": rec["kind"],
        "where": where_of(rec),
        "tenant": tenant,
        "as": rec.get("as"),
        "bind": rec.get("bind", "id"),
        "expect": set(rec.get("expect", [])),
        "exists_ok": False,
        "headers": {},
        "query": None,
        "body": None,
        "raw": None,
    }


def plan_typed(rec, tenant):
    kind = rec["kind"]
    spec = KINDS[kind]
    where = where_of(rec)
    step = base_step(rec, spec["phase"], f"{kind}", tenant)
    step["method"] = spec["method"]
    step["path_template"] = full_path(spec["api"], fill_path(spec["path"], rec, where), spec["scoped"], tenant)
    step["exists_ok"] = spec.get("exists_ok", False)
    body_spec = spec["body"]
    if isinstance(body_spec, tuple):
        body = {k: rec[k] for k in body_spec if k in rec}
    else:
        if body_spec not in rec:
            raise SeedError(f"{where}: kind '{kind}' needs the field \"{body_spec}\"")
        body = rec[body_spec]
    step["body"] = body
    ident = rec.get("entity") or rec.get("namespace") or rec.get("type") or ""
    step["label"] = f"{kind} {ident}".strip()
    if kind == "schema":
        step["guard_schema"] = rec["entity"]
    return step


def plan_call(rec, tenant):
    where = where_of(rec)
    method = str(rec.get("method", "")).upper()
    path = rec.get("path", "")
    if method not in ("GET", "POST", "PUT", "PATCH", "DELETE"):
        raise SeedError(f"{where}: call needs a method (GET, POST, PUT, PATCH or DELETE)")
    m = re.match(r"^(/api/v[12])(/.*)$", path)
    if not m:
        raise SeedError(f"{where}: call path must start with /api/v1 or /api/v2, got {path!r}")
    step = base_step(rec, CALL_PHASE, f"call {method} {path}", tenant)
    step["method"] = method
    step["path_template"] = full_path(m.group(1), m.group(2), bool(rec.get("scoped")), tenant)
    step["query"] = rec.get("query")
    if "content_type" in rec:
        step["headers"]["Content-Type"] = rec["content_type"]
    if rec.get("headers"):
        step["headers"].update(rec["headers"])
    given = [k for k in ("body", "body_file", "body_b64", "body_text") if k in rec]
    if len(given) > 1:
        raise SeedError(f"{where}: call may have only one of body, body_file, body_b64, body_text")
    if given == ["body"]:
        step["body"] = rec["body"]
    elif given == ["body_file"]:
        fpath = os.path.join(os.path.dirname(rec["_file"]), rec["body_file"])
        try:
            with open(fpath, "rb") as fh:
                step["raw"] = fh.read()
        except OSError as exc:
            raise SeedError(f"{where}: cannot read body_file: {exc}")
    elif given == ["body_b64"]:
        try:
            step["raw"] = base64.b64decode(rec["body_b64"], validate=True)
        except (ValueError, TypeError) as exc:
            raise SeedError(f"{where}: body_b64 is not valid base64: {exc}")
    elif given == ["body_text"]:
        step["raw"] = str(rec["body_text"]).encode("utf-8")
    return step


def plan_blob(rec, tenant):
    """A blob is a raw-body POST with its key in the X-Blob-Key header."""
    where = where_of(rec)
    key = rec.get("key")
    if not isinstance(key, str) or not key:
        raise SeedError(f"{where}: blob needs a \"key\"")
    options = [k for k in ("body_file", "body_b64", "body_text") if k in rec]
    if len(options) != 1:
        raise SeedError(f"{where}: blob needs exactly one of body_file, body_b64, body_text")
    call = {k: rec[k] for k in options + ["_file", "_line", "as", "bind", "expect"] if k in rec}
    call.update(kind="blob", method="POST", path="/api/v1/blob", scoped=True,
                headers={"X-Blob-Key": key},
                content_type=rec.get("content_type", "application/octet-stream"))
    step = plan_call(call, tenant)
    step["label"] = f"blob {key}"
    step["phase"] = rec.get("phase", 60)
    return step


def clean_data(rec, where):
    data = rec.get("data")
    if not isinstance(data, dict):
        raise SeedError(f"{where}: entity needs a \"data\" object")
    return {k: v for k, v in data.items() if k not in SYSTEM_FIELDS}


def plan_entities(entity_recs, tenants):
    """Order entity records so REF targets exist first; break cycles with PATCH."""
    steps = []
    idless = []
    ided = []
    for rec in entity_recs:
        where = where_of(rec)
        if not isinstance(rec.get("entity"), str) or not rec["entity"]:
            raise SeedError(f"{where}: entity record needs an \"entity\" name")
        if "id" in rec and (not isinstance(rec["id"], int) or isinstance(rec["id"], bool) or rec["id"] < 0):
            raise SeedError(f"{where}: entity \"id\" must be a non-negative integer")
        (ided if "id" in rec else idless).append(rec)

    def entity_step(rec, data, patch_fields=None):
        tenant = tenants[rec["_file"]]
        ent = rec["entity"]
        if patch_fields is not None:
            step = base_step(rec, 41, f"entity {ent}/{rec['id']} (refs)", tenant)
            step["method"] = "PATCH"
            step["path"] = full_path("/api/v1", f"/{urllib.parse.quote(ent, safe='')}/{rec['id']}", True, tenant)
            step["body"] = patch_fields
            step["as"] = None
            return step
        if "id" in rec:
            step = base_step(rec, 40, f"entity {ent}/{rec['id']}", tenant)
            step["method"] = "POST"
            step["path"] = full_path("/api/v1", f"/{urllib.parse.quote(ent, safe='')}/save/{rec['id']}", True, tenant)
        else:
            step = base_step(rec, 39, f"entity {ent} (new)", tenant)
            step["method"] = "POST"
            step["path"] = full_path("/api/v1", f"/{urllib.parse.quote(ent, safe='')}", True, tenant)
        step["body"] = data
        return step

    for rec in idless:
        steps.append(entity_step(rec, clean_data(rec, where_of(rec))))

    keys = {}
    for rec in ided:
        key = (tenants[rec["_file"]], rec["entity"], rec["id"])
        if key in keys:
            raise SeedError(f"{where_of(rec)}: duplicate entity {rec['entity']}/{rec['id']} (first at {where_of(keys[key])})")
        keys[key] = rec

    deps = {}
    for key, rec in keys.items():
        found = set()
        for value in clean_data(rec, where_of(rec)).values():
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
        rec = keys[key]
        steps.append(entity_step(rec, clean_data(rec, where_of(rec))))
        for nxt in dependents[key]:
            indegree[nxt] -= 1
            if indegree[nxt] == 0:
                heapq.heappush(heap, (order[nxt], nxt))

    remaining = [k for k in keys if k not in done]
    remaining_set = set(remaining)
    patches = []
    for key in remaining:
        rec = keys[key]
        data = clean_data(rec, where_of(rec))
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


def build_plan(files, args):
    """Return the ordered list of steps for every loaded file."""
    tenants = {}
    plan = []
    entity_recs = []
    for path, header, records in files:
        tenants[path] = args.tenant or header.get("tenant")
        for rec in records:
            kind = rec["kind"]
            if kind == "entity":
                entity_recs.append(rec)
            elif kind == "call":
                plan.append(plan_call(rec, tenants[path]))
            elif kind == "blob":
                plan.append(plan_blob(rec, tenants[path]))
            elif kind in KINDS:
                plan.append(plan_typed(rec, tenants[path]))
            elif args.lenient:
                print(f"warning: {where_of(rec)}: unknown kind '{kind}' skipped", file=sys.stderr)
            else:
                known = ", ".join(sorted(list(KINDS) + list(SPECIAL_KINDS)))
                raise SeedError(f"{where_of(rec)}: unknown kind '{kind}'. Known kinds: {known}")
    plan.extend(plan_entities(entity_recs, tenants))

    only = set(filter(None, (args.only or "").split(",")))
    skip = set(filter(None, (args.skip or "").split(",")))
    for name in only | skip:
        if name not in KINDS and name not in SPECIAL_KINDS:
            raise SeedError(f"--only/--skip: unknown kind '{name}'")
    plan = [s for s in plan if (not only or s["kind"] in only) and s["kind"] not in skip]
    plan.sort(key=lambda s: s["phase"])
    return plan


# ---------------------------------------------------------------------------
# HTTP
# ---------------------------------------------------------------------------

def default_base_url():
    explicit = os.environ.get("XOLU_BASE_URL")
    if explicit:
        return explicit.rstrip("/")
    addr = os.environ.get("XOLU_ADDR", "")
    addr_host, addr_port = "", ""
    if ":" in addr:
        addr_host, _, addr_port = addr.rpartition(":")
    host = os.environ.get("XOLU_HOST") or addr_host or "127.0.0.1"
    port = os.environ.get("XOLU_PORT") or addr_port or str(DEFAULT_PORT)
    return f"http://{host}:{port}"


class Client:
    def __init__(self, base, headers, timeout):
        self.base = base
        self.headers = headers
        self.timeout = timeout

    def request(self, method, path, body=None, raw=None, headers=None, query=None):
        url = self.base + path
        if query:
            url += "?" + urllib.parse.urlencode(query, doseq=True)
        data = None
        hdrs = dict(self.headers)
        if raw is not None:
            data = raw
        elif body is not None:
            data = json.dumps(body).encode("utf-8")
            hdrs["Content-Type"] = "application/json"
        if headers:
            hdrs.update(headers)
        req = urllib.request.Request(url, data=data, method=method, headers=hdrs)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                status, payload = resp.status, resp.read()
        except urllib.error.HTTPError as exc:
            status, payload = exc.code, exc.read()
        except (urllib.error.URLError, OSError) as exc:
            raise SeedError(f"{method} {url}: {exc}")
        parsed = None
        if payload:
            text = payload.decode("utf-8", "replace")
            try:
                parsed = json.loads(text)
            except ValueError:
                parsed = text
        return status, parsed

    def wait_ready(self, seconds):
        deadline = time.time() + seconds
        last = None
        while True:
            try:
                status, _ = self.request("GET", "/health")
                if 200 <= status < 300:
                    return
                last = f"HTTP {status}"
            except SeedError as exc:
                last = str(exc)
            if time.time() >= deadline:
                raise SeedError(f"xolu not ready at {self.base} after {seconds}s ({last})")
            time.sleep(0.5)


# ---------------------------------------------------------------------------
# Execution
# ---------------------------------------------------------------------------

def short(value, limit=200):
    text = json.dumps(value, ensure_ascii=False) if not isinstance(value, str) else value
    return text if len(text) <= limit else text[:limit] + "..."


def has_rows(client, entity, tenant):
    """True if the entity type already holds rows in this tenant's scope."""
    path = full_path("/api/v1", f"/{urllib.parse.quote(entity, safe='')}", True, tenant)
    status, body = client.request("GET", path, query={"per_page": 1})
    if status == 404:
        return False
    if status >= 300:
        raise SeedError(f"cannot check whether '{entity}' already has rows: GET {path} -> HTTP {status}")
    if isinstance(body, dict):
        data = body.get("data")
        if isinstance(data, list) and data:
            return True
        return bool((body.get("pagination") or {}).get("total_items"))
    return isinstance(body, list) and bool(body)


def schema_guard(client, step):
    """XOT244: registering a schema over existing rows hides them.

    Returns None to go ahead, or (outcome, message) to skip or refuse.
    """
    entity = step["guard_schema"]
    status, _ = client.request("GET", f"/api/v1/schema/{urllib.parse.quote(entity, safe='')}")
    if status == 200:
        return ("skipped", f"schema '{entity}' is already registered")
    if status != 404:
        raise SeedError(f"cannot check whether a schema for '{entity}' exists: HTTP {status}")
    if has_rows(client, entity, step["tenant"]):
        return ("failed", f"{step['where']}: refusing to register a schema for '{entity}': the type already has "
                          f"rows and registering would hide them (XOT244). Promote it with "
                          f"POST /entities/promote/..., or pass --force-schemas")
    return None


def execute(plan, client, args):
    bindings = {}
    stats = {}
    failures = []

    def tally(kind, outcome):
        stats.setdefault(kind, {"ok": 0, "skipped": 0, "failed": 0})[outcome] += 1

    for step in plan:
        where = step["where"]
        try:
            path = interpolate(step.get("path_template") or step["path"], bindings, where)
            body = resolve(step["body"], bindings, where) if step["body"] is not None else None
            query = resolve(step["query"], bindings, where) if step["query"] else None
        except SeedError as exc:
            tally(step["kind"], "failed")
            failures.append(str(exc))
            if args.keep_going:
                continue
            break

        if step.get("guard_schema") and not args.force_schemas and not args.dry_run:
            verdict = schema_guard(client, step)
            if verdict:
                outcome, message = verdict
                tally(step["kind"], outcome)
                if outcome == "failed":
                    print(f"  [failed] {message}", file=sys.stderr)
                    failures.append(message)
                    if not args.keep_going:
                        break
                elif args.verbose:
                    print(f"  [skipped] {message}")
                continue

        if args.dry_run:
            print(f"{step['method']:6} {path}" + (f"  {short(body)}" if body is not None else ""))
            if step["as"]:
                bindings[step["as"]] = f"<{step['as']}>"
            tally(step["kind"], "ok")
            continue

        status, parsed = client.request(step["method"], path, body=body, raw=step["raw"],
                                        headers=step["headers"], query=query)
        if 200 <= status < 300 or status in step["expect"]:
            outcome = "ok"
        elif step["exists_ok"] and status == 409:
            outcome = "skipped"
        else:
            outcome = "failed"

        if outcome == "ok" and step["as"]:
            try:
                bindings[step["as"]] = dig(parsed, step["bind"])
            except (KeyError, TypeError):
                outcome = "failed"
                failures.append(f"{where}: {step['label']}: response has no \"{step['bind']}\" to bind as \"{step['as']}\" (got {short(parsed)})")
                tally(step["kind"], outcome)
                if args.keep_going:
                    continue
                break

        tally(step["kind"], outcome)
        if args.verbose or outcome == "failed":
            print(f"  [{outcome}] {step['method']} {path} -> {status}", file=sys.stderr if outcome == "failed" else sys.stdout)
        if outcome == "failed":
            failures.append(f"{where}: {step['label']}: HTTP {status}: {short(parsed)}")
            if not args.keep_going:
                break
    return stats, failures


# ---------------------------------------------------------------------------
# Command line
# ---------------------------------------------------------------------------

def parse_args(argv):
    here = os.path.dirname(os.path.abspath(__file__))
    ap = argparse.ArgumentParser(
        prog="seed.py",
        description="Load .seed files into a running xolu instance. "
                    "Run with --format-help for the file format.")
    ap.add_argument("seeds", nargs="*", metavar="SEED", help="seed path or name (default: every *.seed in --seeds-dir)")
    ap.add_argument("--seeds-dir", default=os.environ.get("XOLU_SEEDS_DIR") or os.path.join(os.path.dirname(here), "seeds"))
    ap.add_argument("--base-url", default=None, help="default: XOLU_BASE_URL, else built from XOLU_HOST/XOLU_PORT/XOLU_ADDR, else http://127.0.0.1:9090")
    ap.add_argument("--tenant", default=None, help="override the tenant named in each seed header")
    ap.add_argument("--api-key", default=os.environ.get("XOLU_API_KEY"), help="sent as X-API-Key (default: XOLU_API_KEY)")
    ap.add_argument("--token", default=os.environ.get("XOLU_TOKEN"), help="sent as a Bearer token (default: XOLU_TOKEN)")
    ap.add_argument("--wait", type=float, default=15.0, metavar="SECS", help="wait up to SECS for /health first (default 15; 0 to skip)")
    ap.add_argument("--timeout", type=float, default=30.0, metavar="SECS", help="per-request timeout (default 30)")
    ap.add_argument("--only", help="comma list of kinds to load, e.g. schema,entity")
    ap.add_argument("--skip", help="comma list of kinds to leave out")
    ap.add_argument("--keep-going", action="store_true", help="continue after a failed record")
    ap.add_argument("--force-schemas", action="store_true", help="register schemas without the already-registered / existing-rows check (see XOT244)")
    ap.add_argument("--lenient", action="store_true", help="skip unknown kinds with a warning instead of failing")
    ap.add_argument("--dry-run", action="store_true", help="print the requests that would be made; contact nothing")
    ap.add_argument("--list", action="store_true", help="list the seed files and their record counts, then exit")
    ap.add_argument("-v", "--verbose", action="store_true", help="print every request")
    ap.add_argument("--format-help", action="store_true", help="print the .seed format description and exit")
    return ap.parse_args(argv)


def main(argv=None):
    args = parse_args(sys.argv[1:] if argv is None else argv)
    if args.format_help:
        print(__doc__)
        return 0
    try:
        paths = discover(args)
        files = [(p,) + load_seed_file(p) for p in paths]
        if args.list:
            for path, header, records in files:
                counts = {}
                for rec in records:
                    counts[rec["kind"]] = counts.get(rec["kind"], 0) + 1
                detail = ", ".join(f"{k}={v}" for k, v in sorted(counts.items())) or "empty"
                print(f"{os.path.basename(path)}: name={header.get('name', '-')} tenant={header.get('tenant', '-')} records={len(records)} ({detail})")
            return 0
        plan = build_plan(files, args)
    except SeedError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    headers = {"Accept": "application/json"}
    if args.api_key:
        headers["X-API-Key"] = args.api_key
    if args.token:
        headers["Authorization"] = f"Bearer {args.token}"
    base = (args.base_url or default_base_url()).rstrip("/")
    client = Client(base, headers, args.timeout)

    print(f"seeding {base}: {len(plan)} step(s) from {len(files)} file(s)" + (" [dry run]" if args.dry_run else ""))
    try:
        if not args.dry_run and args.wait > 0:
            client.wait_ready(args.wait)
        stats, failures = execute(plan, client, args)
    except SeedError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    print()
    for kind in sorted(stats):
        s = stats[kind]
        print(f"  {kind:14} ok={s['ok']} skipped={s['skipped']} failed={s['failed']}")
    if failures:
        print(f"\n{len(failures)} failure(s):", file=sys.stderr)
        for line in failures:
            print(f"  {line}", file=sys.stderr)
        return 1
    print("done")
    return 0


if __name__ == "__main__":
    sys.exit(main())
