# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""Loading a plan into a tenant, with the guards around it.

Before the first write the loader finds out what the tenant holds. An empty
tenant is loaded. A tenant that holds this same seed is continued: records
that exist are left alone (mode "create") or written again (mode "replace").
Anything else is refused unless the caller allows it. What was loaded is
recorded in the tenant, in the entity type PROVENANCE_ENTITY, which is how a
later run tells those cases apart and how an interrupted load is continued
with the same reference time.
"""

import datetime
import os
import re
import urllib.parse

from .format import SeedError, SeedRefused
from .lookup import V2_HINT, find
from .plan import full_path

PROVENANCE_ENTITY = "seed_loads"
OUTCOMES = ("created", "present", "replaced", "skipped", "failed")
LOADER = "seed_system 1.0.0"


def _now():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _short(value, limit=200):
    import json
    text = json.dumps(value, ensure_ascii=False) if not isinstance(value, str) else value
    return text if len(text) <= limit else text[:limit] + "..."


def _dig(obj, dotted):
    cur = obj
    for part in dotted.split("."):
        if isinstance(cur, dict) and part in cur:
            cur = cur[part]
        else:
            raise KeyError(dotted)
    return cur


def seed_name(path, header):
    return header.get("name") or os.path.splitext(os.path.basename(path))[0]


class Loader:
    """One load of one or more files into the tenants they name.

    Use: inspect() (asks the server what is there), then as_of() to settle the
    reference time, build the plan with it, then run(plan).
    """

    def __init__(self, client, files, tenant=None, mode="create", allow_existing=False, undo_on_failure=False,
                 keep_going=False, force_schemas=False, verbose=False, partial=False, say=None):
        if mode not in ("create", "replace"):
            raise SeedError("mode must be \"create\" or \"replace\"")
        self.client, self.mode = client, mode
        self.allow_existing, self.undo_on_failure, self.keep_going = allow_existing, undo_on_failure, keep_going
        self.force_schemas, self.verbose, self.partial = force_schemas, verbose, partial
        self.say = say or (lambda message: None)
        # file -> what is known about it
        self.files = [dict(path=p, header=h, digest=d, name=seed_name(p, h), tenant=tenant or h.get("tenant")) for p, h, d in files]
        self.tenants = {}    # tenant -> {"empty": bool or None, "loads": [rows], "fresh": bool}

    # -- what is there ------------------------------------------------------

    def _summary(self, tenant):
        status, body = self.client.request("GET", full_path("/api/v1", "/tenant-summary", True, tenant))
        if status == 200 and isinstance(body, dict) and isinstance(body.get("empty"), bool):
            return body["empty"]
        return None

    def _loads(self, tenant):
        status, body = self.client.request("GET", full_path("/api/v1", "/" + PROVENANCE_ENTITY, True, tenant), query={"per_page": 500})
        rows = body.get("data") if isinstance(body, dict) else body
        return [r for r in rows if isinstance(r, dict)] if status == 200 and isinstance(rows, list) else []

    def inspect(self):
        for f in self.files:
            t = f["tenant"]
            if t not in self.tenants:
                empty = self._summary(t)
                self.tenants[t] = {"empty": empty, "loads": [] if empty else self._loads(t), "fresh": bool(empty)}
            f["prior"] = next((r for r in reversed(self.tenants[t]["loads"]) if r.get("seed") == f["name"]), None)
            done = (f["prior"] or {}).get("done")
            f["done"] = dict(done) if isinstance(done, dict) else {}
        return self

    def as_of(self, given=None):
        """The reference time: the one given; else the one this seed was first loaded with; else now."""
        if given is not None:
            return given
        for f in self.files:
            prior = f.get("prior")
            if prior and isinstance(prior.get("as_of"), str):
                try:
                    return datetime.datetime.strptime(prior["as_of"], "%Y-%m-%dT%H:%M:%SZ")
                except ValueError:
                    pass
        return None

    def _guard(self, plan):
        together = {f["name"] for f in self.files}
        for f in self.files:
            state = self.tenants[f["tenant"]]
            scope = "tenant \"%s\"" % f["tenant"] if f["tenant"] else "the instance"
            requires = f["header"].get("requires") or []
            loaded = {r.get("seed") for r in state["loads"] if r.get("status") == "loaded"} | (together - {f["name"]})
            missing = [n for n in requires if n not in loaded]
            if missing:
                raise SeedRefused("seed \"%s\" adds to the seed%s %s, which %s not loaded in %s. Load %s first, or name both in one load."
                                  % (f["name"], "s" if len(missing) > 1 else "", ", ".join("\"%s\"" % n for n in missing),
                                     "are" if len(missing) > 1 else "is", scope, "them" if len(missing) > 1 else "it"))
            if state["empty"] or f.get("prior"):
                continue
            if requires or f["header"].get("addition") or self.allow_existing:
                continue
            if state["empty"] is None:
                raise SeedRefused("cannot tell whether %s is empty (GET tenant-summary did not answer); "
                                  "pass --allow-existing to load anyway" % scope)
            others = sorted({str(r.get("seed")) for r in state["loads"]})
            what = "holds the seed%s %s" % ("s" if len(others) > 1 else "", ", ".join("\"%s\"" % o for o in others)) if others \
                else "holds data that no seed load accounts for"
            raise SeedRefused("%s already %s; seed \"%s\" was not loaded. Load into an empty tenant, or pass "
                              "--allow-existing to add to what is there." % (scope[0].upper() + scope[1:], what, f["name"]))

    # -- provenance ---------------------------------------------------------

    def _record_start(self, ctx):
        for f in self.files:
            row = {"seed": f["name"], "digest": f["digest"], "format": f["header"]["seed"], "loader": LOADER,
                   "as_of": ctx.as_of_text(), "status": "loading", "mode": self.mode, "started_at": _now(), "partial": self.partial}
            base = full_path("/api/v1", "/" + PROVENANCE_ENTITY, True, f["tenant"])
            prior = f.get("prior")
            if prior and "id" in prior:
                status, body = self.client.request("PATCH", "%s/%s" % (base, prior["id"]), body=row)
                f["load_path"] = "%s/%s" % (base, prior["id"])
            else:
                status, body = self.client.request("POST", base, body=row)
                f["load_path"] = "%s/%s" % (base, body.get("id")) if isinstance(body, dict) and "id" in body else None
                f["load_new"] = True
            if not 200 <= status < 300:
                raise SeedError("cannot record the load in \"%s\": HTTP %d: %s" % (PROVENANCE_ENTITY, status, _short(body)))

    def _record_end(self, status_word, stats):
        records = {kind: dict(counts) for kind, counts in stats.items()}
        for f in self.files:
            if f.get("load_path"):
                self.client.request("PATCH", f["load_path"], body={"status": status_word, "finished_at": _now(), "records": records,
                                                                 "done": f.get("done", {})})

    # -- the run ------------------------------------------------------------

    def _schema_guard(self, step):
        """Registering a schema over existing rows hides them: skip one already there, refuse one over rows."""
        entity = step["guard_schema"]
        status, _ = self.client.request("GET", "/api/v1/schema/%s" % urllib.parse.quote(entity, safe=""))
        if status == 200:
            return "present", "schema '%s' is already registered" % entity
        if status != 404:
            raise SeedError("cannot check whether a schema for '%s' exists: HTTP %d" % (entity, status))
        path = full_path("/api/v1", "/%s" % urllib.parse.quote(entity, safe=""), True, step["tenant"])
        status, body = self.client.request("GET", path, query={"per_page": 1})
        rows = False
        if status not in (404,) and status < 300:
            data = body.get("data") if isinstance(body, dict) else body
            rows = bool(data) or bool(((body.get("pagination") or {}) if isinstance(body, dict) else {}).get("total_items"))
        elif status >= 300 and status != 404:
            raise SeedError("cannot check whether '%s' already has rows: GET %s -> HTTP %d" % (entity, path, status))
        if rows:
            return "failed", ("%s: refusing to register a schema for '%s': the type already has rows and registering would "
                              "hide them. Promote it with POST /entities/promote/..., or pass --force-schemas" % (step["where"], entity))
        return None

    def run(self, plan, ctx):
        """Load the plan. Returns a result dict; raises SeedRefused when a guard stops it before any write."""
        if not self.tenants:
            self.inspect()
        self._guard(plan)
        self._record_start(ctx)
        bindings, stats, failures, written, kept = {}, {}, [], [], {}
        present = set()
        done_of = {f["path"]: f.setdefault("done", {}) for f in self.files}

        def tally(kind, outcome):
            stats.setdefault(kind, dict.fromkeys(OUTCOMES, 0))[outcome] += 1

        def interpolate(text, where):
            def sub(match):
                if match.group(1) not in bindings:
                    raise SeedError("%s: ${%s} has no binding yet" % (where, match.group(1)))
                return str(bindings[match.group(1)])
            return re.sub(r"\$\{(\w+)\}", sub, text)

        def resolve(value, where):
            if isinstance(value, dict):
                if set(value) == {"$seed"}:
                    if value["$seed"] not in bindings:
                        raise SeedError("%s: placeholder {\"$seed\": \"%s\"} has no binding yet" % (where, value["$seed"]))
                    return bindings[value["$seed"]]
                if set(value) == {"$text"} and isinstance(value["$text"], str):
                    def bound(match):
                        if match.group(1) not in bindings:
                            raise SeedError("%s: ${seed:%s} has no binding yet" % (where, match.group(1)))
                        return str(bindings[match.group(1)])
                    return re.sub(r"\$\{seed:(\w+)\}", bound, value["$text"])
                return {k: resolve(v, where) for k, v in value.items()}
            if isinstance(value, list):
                return [resolve(v, where) for v in value]
            return value

        stopped = False
        for step in plan:
            where, fresh = step["where"], self.tenants.get(step["tenant"], {}).get("fresh", False)
            done = done_of.get(step["file"], {})
            if step["tracked"] and not step["match"] and step["key"] in done:
                # the load record says this ran: it is not run twice, and what it bound is bound again.
                # A record with "match" is looked up instead, every time.
                if step["as"]:
                    bindings[step["as"]] = done[step["key"]]
                tally(step["kind"], "present")
                continue
            try:
                path = interpolate(step.get("path_template") or step["path"], where)
                body = resolve(step["body"], where) if step["body"] is not None else None
                query = resolve(step["query"], where) if step["query"] else None
            except SeedError as exc:
                tally(step["kind"], "failed")
                failures.append(str(exc))
                if self.keep_going:
                    continue
                stopped = True
                break

            if step.get("guard_schema") and not self.force_schemas:
                verdict = self._schema_guard(step)
                if verdict:
                    outcome, message = verdict
                    tally(step["kind"], outcome)
                    if outcome == "failed":
                        failures.append(message)
                        if not self.keep_going:
                            stopped = True
                            break
                    elif self.verbose:
                        self.say("  [present] %s" % message)
                    continue

            if step["match"]:
                try:
                    found = find(self.client, step, body)
                except SeedError as exc:
                    tally(step["kind"], "failed")
                    failures.append(str(exc))
                    if self.keep_going:
                        continue
                    stopped = True
                    break
                if step["op"] == "find":
                    if found is None:
                        tally(step["kind"], "failed")
                        failures.append("%s: nothing in %s has %s; this file adds to data that must be there first" % (
                            where, step["entity"], ", ".join("%s=%s" % (f, body[f]) for f in step["match"])))
                        if self.keep_going:
                            continue
                        stopped = True
                        break
                    bindings[step["as"]] = found
                    tally(step["kind"], "present")
                    continue
                if found is not None:
                    outcome = "present"
                    if step["op"] == "match" and self.mode == "replace":
                        status, parsed = self.client.request("POST", "%s/save/%s" % (step["item_path"], found), body=body)
                        outcome = "replaced" if 200 <= status < 300 else "failed"
                        if outcome == "failed":
                            failures.append("%s: %s: HTTP %s: %s" % (where, step["label"], status, _short(parsed)))
                    if step["as"]:
                        bindings[step["as"]] = found
                    done[step["key"]] = found
                    tally(step["kind"], outcome)
                    if outcome == "failed" and not self.keep_going:
                        stopped = True
                        break
                    continue

            key = (step["tenant"], step["entity"], step["id"])
            existed = False
            if step["op"] == "save" and not fresh:
                status, found = self.client.request("GET", step["read_path"])
                existed = status == 200
                if existed and self.mode == "create":
                    present.add(key)
                    if step["as"]:
                        bindings[step["as"]] = step["id"]
                    tally(step["kind"], "present")
                    continue
            if step["op"] == "patch" and key in present:
                continue        # the record was left as it is, and so are its references

            status, parsed = self.client.request(step["method"], path, body=body, raw=step["raw"], headers=step["headers"], query=query)
            if 200 <= status < 300 or status in step["expect"]:
                outcome = "replaced" if existed else "created"
                if step["op"] == "patch":
                    outcome = None      # part of a record already counted
            elif step["exists_ok"] and status == 409:
                outcome = "present"
            else:
                outcome = "failed"

            if outcome != "failed" and step["as"]:
                try:
                    bindings[step["as"]] = step["id"] if step["op"] == "save" else _dig(parsed, step["bind"])
                except (KeyError, TypeError):
                    outcome = "failed"
                    parsed = "response has no \"%s\" to bind as \"%s\" (got %s)" % (step["bind"], step["as"], _short(parsed))
            if outcome == "created":
                if step["op"] == "save":
                    written.append("%s/%d" % (step["item_path"], step["id"]))
                elif step["op"] in ("create", "match") and isinstance(parsed, dict) and "id" in parsed:
                    written.append("%s/%s" % (step["item_path"], parsed["id"]))
                elif step["op"] == "call":
                    kept[step["kind"]] = kept.get(step["kind"], 0) + 1
            if step["tracked"] and outcome in ("created", "present"):
                done[step["key"]] = bindings.get(step["as"], True) if step["as"] else (parsed.get("id", True) if step["match"] and isinstance(parsed, dict) else True)
            if outcome:
                tally(step["kind"], outcome)
            if self.verbose or outcome == "failed":
                self.say("  [%s] %s %s -> %s" % (outcome or "refs", step["method"], path, status))
            if outcome == "failed":
                hint = V2_HINT if status == 404 and "/api/v2/" in path else ""
                failures.append("%s: %s: HTTP %s: %s%s" % (where, step["label"], status, _short(parsed), hint))
                if not self.keep_going:
                    stopped = True
                    break

        result = {"ok": not failures, "stats": stats, "failures": failures, "stopped": stopped, "steps": len(plan),
                  "as_of": ctx.as_of_text(), "mode": self.mode, "seeds": [f["name"] for f in self.files],
                  "fresh": all(t["fresh"] for t in self.tenants.values()), "undone": None, "requests": self.client.requests}
        if failures and self.undo_on_failure:
            result["undone"] = self._undo(written, kept, result["fresh"])
        if not (failures and result["undone"] and result["undone"]["complete"]):
            self._record_end("failed" if failures else "loaded", stats)
        return result

    def _undo(self, written, kept, fresh):
        """Delete the entities this run created. Records that existed before it are not touched."""
        deleted, failed = 0, []
        for path in reversed(written):
            status, _ = self.client.request("DELETE", path)
            if 200 <= status < 300 or status == 404:
                deleted += 1
            else:
                failed.append(path)
        for f in self.files:
            if f.get("load_new") and f.get("load_path"):
                self.client.request("DELETE", f["load_path"])
        left = dict(kept)
        if failed:
            left["entity"] = len(failed)
        return {"complete": not left, "deleted": deleted, "left": left,
                "why": "" if not left else "these cannot be deleted through the API and were left: " + ", ".join("%d %s" % (n, k) for k, n in sorted(left.items()))}
