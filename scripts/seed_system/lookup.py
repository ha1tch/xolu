# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""Finding the record a "match" names: by fields, for an entity; in the list, for a definition."""

from .format import SeedError


V2_HINT = " (xolu answers 404 on every /api/v2 route when it runs without XOLU_API_V2_ENABLED=true)"


def _literal(value, where):
    if isinstance(value, bool) or value is None or isinstance(value, (dict, list)):
        raise SeedError("%s: a record is matched by text or whole numbers; %r is neither" % (where, value))
    if isinstance(value, (int, float)):
        return repr(value)
    return "'" + str(value).replace("'", "''") + "'"


def find(client, step, body):
    """The id of the record the step's "match" finds, or None. `body` is the record with its bindings resolved."""
    where = step["where"]
    if step["kind"] == "entity":
        conds = " AND ".join("%s = %s" % (f, _literal(body[f], where)) for f in step["match"])
        status, res = client.request("POST", step["query_path"], body={"query": "SELECT id FROM %s WHERE %s ORDER BY id" % (step["entity"], conds)})
        if status != 200 or not isinstance(res, dict):
            return None         # an entity type with no rows does not exist yet
        rows = [r for r in (res.get("data") or []) if isinstance(r, dict)]
        if step["op"] == "find" and len(rows) > 1:
            raise SeedError("%s: \"find\" on %s matches %d records (ids %s): it must name one" % (
                where, step["entity"], len(rows), ", ".join(str(r.get("id")) for r in rows[:5])))
        return rows[0].get("id") if rows else None
    status, res = client.request("GET", step["list_path"])
    if status != 200:
        hint = V2_HINT if status == 404 and "/api/v2/" in step["list_path"] else ""
        raise SeedError("%s: cannot list what exists to match against: GET %s -> HTTP %d%s" % (where, step["list_path"], status, hint))
    rows = res
    if isinstance(res, dict):
        rows = next((v for v in res.values() if isinstance(v, list)), [])
    for row in rows if isinstance(rows, list) else []:
        if isinstance(row, dict) and all(row.get(f) == body[f] for f in step["match"]):
            return row.get("id")
    return None
