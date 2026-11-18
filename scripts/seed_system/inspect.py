# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""Reading an instance: checking it against a seed, and writing it out as one."""

import json
import re
import urllib.parse

from .format import SYSTEM_FIELDS, SeedError, bindings_in, write_record
from .load import PROVENANCE_ENTITY
from .lookup import find
from .plan import full_path

_NAME = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


def _ids(client, tenant, entity):
    """The ids an entity type holds, or None when they cannot be listed."""
    if not _NAME.match(entity):
        return None
    status, body = client.request("POST", full_path("/api/v1", "/oql/query", True, tenant), body={"query": "SELECT id FROM %s" % entity})
    if status != 200 or not isinstance(body, dict) or not isinstance(body.get("data"), list):
        return [] if status in (400, 404) else None      # a type with no rows does not exist yet
    return sorted(r["id"] for r in body["data"] if isinstance(r, dict) and isinstance(r.get("id"), int))


def check_seed(client, plan):
    """Compare what the plan would write with what the instance holds; nothing is written.

    Looks at entity records that have an id. Returns missing, different (with
    the fields), extra (ids of the same entity types that the seed does not
    have), and how many records were compared or could not be.
    """
    missing, different, extra, compared, unchecked = [], [], [], 0, 0
    seen = {}
    later = {}      # fields a record gets in a second request, when its references form a cycle
    for step in plan:
        if step["op"] == "patch":
            later.setdefault((step["tenant"], step["entity"], step["id"]), {}).update(step["body"])
    for step in plan:
        if step["op"] == "find":
            if not bindings_in(step["body"]) and find(client, step, step["body"]) is None:
                missing.append("%s (%s), which the seed expects to find" % (step["entity"], ", ".join("%s=%s" % (f, step["body"][f]) for f in step["match"])))
            continue
        if step["op"] == "match" and not bindings_in(step["body"]):
            rid = find(client, step, step["body"])
            label = "%s (%s)" % (step["entity"], ", ".join("%s=%s" % (f, step["body"][f]) for f in step["match"]))
            if rid is None:
                missing.append(label)
                continue
            status, found = client.request("GET", "%s/%s" % (step["item_path"], rid), query={"embed": "false"})
        elif step["op"] != "save":
            if step["kind"] == "entity" and step["op"] in ("create", "match"):
                unchecked += 1
            continue
        else:
            seen.setdefault((step["tenant"], step["entity"]), set()).add(step["id"])
            status, found = client.request("GET", step["read_path"], query={"embed": "false"})
            label = "%s/%d" % (step["entity"], step["id"])
        if status == 404:
            missing.append(label)
            continue
        if status != 200 or not isinstance(found, dict):
            raise SeedError("cannot read %s: HTTP %d" % (label, status))
        compared += 1
        want = dict(step["body"], **later.get((step["tenant"], step["entity"], step["id"]), {}))
        have = {k: v for k, v in found.items() if k not in SYSTEM_FIELDS}
        fields = sorted(k for k in set(want) | set(have)
                        if not bindings_in(want.get(k)) and (k not in want or k not in have or want[k] != have[k] or type(want[k]) is not type(have[k])))
        if fields:
            different.append({"record": label, "fields": fields})
    for (tenant, entity), ids in sorted(seen.items(), key=lambda kv: (kv[0][0] or "", kv[0][1])):
        have = _ids(client, tenant, entity)
        if have is not None:
            extra.extend("%s/%d" % (entity, i) for i in have if i not in ids)
    return {"same": not (missing or different or extra), "missing": missing, "different": different, "extra": extra,
            "compared": compared, "unchecked": unchecked}


def dump_instance(client, name, tenant=None, entities=None, description=None):
    """What a tenant holds, as the text of a version 2 .seed file with explicit ids."""
    if entities is None:
        status, body = client.request("GET", full_path("/api/v1", "/entities", True, tenant))
        rows = body
        if isinstance(body, dict):
            rows = body.get("entities", body.get("data"))
        if status != 200 or not isinstance(rows, list):
            raise SeedError("cannot list the entity types (GET /entities -> HTTP %d); name them with --entities" % status)
        entities = sorted(r if isinstance(r, str) else str(r.get("entity_type") or r.get("name") or r.get("entity")) for r in rows)
    entities = [e for e in entities if e and e != PROVENANCE_ENTITY]
    header = {"seed": 2, "name": name}
    if description:
        header["description"] = description
    if tenant:
        header["tenant"] = tenant
    header["entities"] = entities
    out = [json.dumps(header, ensure_ascii=False)]
    total = 0
    for entity in entities:
        ids = _ids(client, tenant, entity)
        if ids is None:
            raise SeedError("cannot list the ids of \"%s\"" % entity)
        if not ids:
            continue
        out.append("\n# %s: %d" % (entity, len(ids)))
        for i in ids:
            status, row = client.request("GET", full_path("/api/v1", "/%s/%d" % (urllib.parse.quote(entity, safe=""), i), True, tenant), query={"embed": "false"})
            if status != 200 or not isinstance(row, dict):
                raise SeedError("cannot read %s/%d: HTTP %d" % (entity, i, status))
            out.append(write_record({"kind": "entity", "entity": entity, "id": i, "data": {k: v for k, v in row.items() if k not in SYSTEM_FIELDS}}))
            total += 1
    return "\n".join(out) + "\n", total
