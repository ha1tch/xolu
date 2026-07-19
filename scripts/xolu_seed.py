#!/usr/bin/env python3
"""
xolu_seed.py — Complex graph seed and query demonstration via the xolu REST API.

Dataset: a small software company.

  Node types (all with adapted native-column tables):
    person  — employees (name, department, level, location)
    project — work items  (name, status, budget_usd)
    skill   — competencies (name, category)

  Relationship types (topology via REF fields, all graph-traversable):
    reports_to       person → person   (management chain)
    works_on         person → project  (assignment)
    has_skill        person → skill    (competency link)
    requires_skill   project → skill   (project requirement)
    knows_a / _b / _c  person → person  (professional relationship,
                                          multiple per person via distinct field names)

Usage:
    python3 xolu_seed.py

Requires an xolu binary at /tmp/xolu (or XOLU_BINARY env var).
The script starts the server, seeds data, runs queries, then stops the server.
"""

import json, os, subprocess, sys, tempfile, time
import urllib.request, urllib.error
from typing import Any

BASE = "http://localhost:9090/api/v1"


# ── HTTP helpers ──────────────────────────────────────────────────────────────

def _req(method: str, path: str, body: Any = None) -> Any:
    url = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req  = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"HTTP {e.code} {method} {path}: {e.read().decode()[:200]}") from e

def post(path, body):   return _req("POST",  path, body)
def patch(path, body):  return _req("PATCH", path, body)

def create(entity: str, data: dict) -> int:
    return post(f"/{entity}", data)["id"]

def ref(entity: str, id_: int) -> dict:
    return {"type": "REF", "entity": entity, "id": id_}

def query(cypher: str, max_depth: int = 10) -> list[dict]:
    try:
        r = post("/graph/query", {"query": cypher, "max_depth": max_depth})
        return r.get("result", r.get("data", []))
    except RuntimeError as e:
        return [{"_error": str(e)[:160]}]

def wait_ready(timeout: int = 20) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            urllib.request.urlopen("http://localhost:9090/health", timeout=1)
            return
        except Exception:
            time.sleep(0.3)
    raise RuntimeError("Server did not become ready")

def hr(title: str) -> None:
    w = 64
    print(f"\n{'━'*w}\n  {title}\n{'━'*w}")

def show(label: str, rows: list[dict]) -> None:
    print(f"\n  ┌─ {label}")
    if not rows:
        print("  │  (no rows)")
    else:
        for row in rows:
            if "_error" in row:
                print(f"  │  ERROR: {row['_error']}")
            else:
                print(f"  │  {json.dumps(row, ensure_ascii=False)}")
        ok = [r for r in rows if "_error" not in r]
        print(f"  │  ({len(ok)} row{'s' if len(ok)!=1 else ''})")
    print("  └─")


# ── Schema registration ───────────────────────────────────────────────────────

def register_schemas() -> None:
    hr("STEP 1 — REGISTER SCHEMAS  (creates adapted native-column tables)")

    post("/schema/person", {
        "properties": {
            "name":       {"type": "string"},
            "department": {"type": "string"},
            "level":      {"type": "integer"},
            "location":   {"type": "string"},
        },
        "required": ["name", "department"],
    })
    print("  ✓ person   →  t0000_ndata_person")

    post("/schema/project", {
        "properties": {
            "name":       {"type": "string"},
            "status":     {"type": "string"},
            "budget_usd": {"type": "integer"},
        },
        "required": ["name", "status"],
    })
    print("  ✓ project  →  t0000_ndata_project")

    post("/schema/skill", {
        "properties": {
            "name":     {"type": "string"},
            "category": {"type": "string"},
        },
        "required": ["name"],
    })
    print("  ✓ skill    →  t0000_ndata_skill")


# ── Seeding ───────────────────────────────────────────────────────────────────

def seed() -> dict:
    hr("STEP 2 — SEED DATA")

    # ── Skills ────────────────────────────────────────────────────────────────
    sk = {}
    for name, cat in [
        ("Go",          "backend"),
        ("Python",      "backend"),
        ("Rust",        "systems"),
        ("PostgreSQL",  "data"),
        ("Kubernetes",  "infra"),
        ("React",       "frontend"),
        ("GraphQL",     "api"),
        ("ML/PyTorch",  "ml"),
        ("Figma",       "design"),
        ("dbt",         "data"),
    ]:
        sk[name] = create("skill", {"name": name, "category": cat})
    print(f"  ✓ {len(sk)} skills")

    # ── People (reporting chain seeded at creation time via reports_to REF) ───
    p = {}

    # ── Level 9: CTO ──────────────────────────────────────────────────────────
    p["Zara"]   = create("person", {
        "name": "Zara", "department": "Engineering", "level": 9, "location": "SF",
    })

    # ── Level 8: VPs ──────────────────────────────────────────────────────────
    p["Marcus"] = create("person", {
        "name": "Marcus", "department": "Product", "level": 8, "location": "NYC",
        "reports_to": ref("person", p["Zara"]),
    })
    p["Selin"]  = create("person", {
        "name": "Selin", "department": "Data", "level": 8, "location": "Berlin",
        "reports_to": ref("person", p["Zara"]),
    })

    # ── Level 6: Staff engineers / team leads ─────────────────────────────────
    p["Alice"]  = create("person", {
        "name": "Alice", "department": "Engineering", "level": 6, "location": "SF",
        "reports_to": ref("person", p["Zara"]),
        "has_skill":  ref("skill",  sk["Go"]),
    })
    p["Fatima"] = create("person", {
        "name": "Fatima", "department": "Data", "level": 6, "location": "Berlin",
        "reports_to": ref("person", p["Selin"]),
        "has_skill":  ref("skill",  sk["ML/PyTorch"]),
    })

    # ── Level 5: Senior engineers ─────────────────────────────────────────────
    p["Bob"]    = create("person", {
        "name": "Bob", "department": "Engineering", "level": 5, "location": "SF",
        "reports_to": ref("person", p["Alice"]),
        "has_skill":  ref("skill",  sk["Rust"]),
    })
    p["Carol"]  = create("person", {
        "name": "Carol", "department": "Engineering", "level": 5, "location": "London",
        "reports_to": ref("person", p["Alice"]),
        "has_skill":  ref("skill",  sk["Kubernetes"]),
    })
    p["Guo"]    = create("person", {
        "name": "Guo", "department": "Data", "level": 5, "location": "Berlin",
        "reports_to": ref("person", p["Fatima"]),
        "has_skill":  ref("skill",  sk["dbt"]),
    })
    p["Hana"]   = create("person", {
        "name": "Hana", "department": "Product", "level": 5, "location": "NYC",
        "reports_to": ref("person", p["Marcus"]),
        "has_skill":  ref("skill",  sk["GraphQL"]),
    })

    # ── Level 4: Mid-level ────────────────────────────────────────────────────
    p["Dev"]    = create("person", {
        "name": "Dev", "department": "Engineering", "level": 4, "location": "Bangalore",
        "reports_to": ref("person", p["Bob"]),
        "has_skill":  ref("skill",  sk["Python"]),
    })
    p["Eve"]    = create("person", {
        "name": "Eve", "department": "Engineering", "level": 4, "location": "SF",
        "reports_to": ref("person", p["Bob"]),
        "has_skill":  ref("skill",  sk["Go"]),
    })
    p["Ivan"]   = create("person", {
        "name": "Ivan", "department": "Design", "level": 4, "location": "NYC",
        "reports_to": ref("person", p["Marcus"]),
        "has_skill":  ref("skill",  sk["Figma"]),
    })

    print(f"  ✓ {len(p)} people with reporting chain and skill links")

    # ── Projects ──────────────────────────────────────────────────────────────
    pr = {}
    pr["Orion"]  = create("project", {
        "name": "Orion",  "status": "active",    "budget_usd": 800_000,
        "requires_skill": ref("skill",  sk["Go"]),
        "lead":           ref("person", p["Alice"]),
    })
    pr["Nebula"] = create("project", {
        "name": "Nebula", "status": "active",    "budget_usd": 1_200_000,
        "requires_skill": ref("skill",  sk["ML/PyTorch"]),
        "lead":           ref("person", p["Fatima"]),
    })
    pr["Pulsar"] = create("project", {
        "name": "Pulsar", "status": "planning",  "budget_usd": 400_000,
        "requires_skill": ref("skill",  sk["Kubernetes"]),
        "lead":           ref("person", p["Carol"]),
    })
    pr["Quasar"] = create("project", {
        "name": "Quasar", "status": "completed", "budget_usd": 200_000,
        "requires_skill": ref("skill",  sk["React"]),
        "lead":           ref("person", p["Hana"]),
    })
    print(f"  ✓ {len(pr)} projects")

    # ── Work assignments (works_on REF, patched onto each person) ─────────────
    assignments = [
        ("Alice",  "Orion"),  ("Bob",   "Orion"),  ("Eve",   "Orion"),
        ("Fatima", "Nebula"), ("Guo",   "Nebula"), ("Dev",   "Nebula"),
        ("Carol",  "Pulsar"), ("Bob",   "Pulsar"),
        ("Hana",   "Quasar"), ("Ivan",  "Quasar"),
    ]
    # Each person can only have one works_on REF (first wins; Bob is on two projects
    # so use a second field name for the second assignment)
    seen_works = set()
    for name, proj in assignments:
        field = "works_on" if name not in seen_works else "works_on_b"
        patch(f"/person/{p[name]}", {field: ref("project", pr[proj])})
        seen_works.add(name)
    print(f"  ✓ {len(assignments)} project assignments")

    # ── KNOWS edges (professional relationships, one patch per edge) ──────────
    # Rules:
    #   - Each source person may have at most 3 KNOWS targets (knows_a/b/c).
    #   - No KNOWS target may duplicate an existing REF target on that person
    #     (reports_to, has_skill, works_on, lead, requires_skill).
    #
    # Existing REF targets per person (summarised):
    #   Alice  → Zara(rpt), Go(skill), Orion(work)
    #   Bob    → Alice(rpt), Rust(skill), Orion(work), Pulsar(work_b)
    #   Carol  → Alice(rpt), K8s(skill), Pulsar(work)
    #   Dev    → Bob(rpt), Python(skill), Nebula(work)
    #   Eve    → Bob(rpt), Go(skill), Orion(work)
    #   Fatima → Selin(rpt), ML(skill), Nebula(work)
    #   Guo    → Fatima(rpt), dbt(skill), Nebula(work)
    #   Hana   → Marcus(rpt), GraphQL(skill), Quasar(work)
    #   Ivan   → Marcus(rpt), Figma(skill), Quasar(work)
    #   Marcus → Zara(rpt)
    #   Selin  → Zara(rpt)
    #   Zara   → (none)
    #
    knows = [
        # (src, dst, field)   — field suffix must be unique per src person
        ("Zara",   "Alice",   "knows_a"),
        ("Zara",   "Selin",   "knows_b"),
        ("Alice",  "Fatima",  "knows_a"),
        ("Alice",  "Marcus",  "knows_b"),
        ("Alice",  "Carol",   "knows_c"),
        ("Bob",    "Carol",   "knows_a"),
        ("Bob",    "Dev",     "knows_b"),
        ("Bob",    "Eve",     "knows_c"),
        ("Carol",  "Dev",     "knows_a"),
        ("Carol",  "Guo",     "knows_b"),
        ("Dev",    "Eve",     "knows_a"),
        ("Eve",    "Dev",     "knows_a"),
        ("Fatima", "Alice",   "knows_a"),
        ("Fatima", "Guo",     "knows_b"),
        ("Guo",    "Selin",   "knows_a"),  # Guo knows VP of Data
        ("Marcus", "Hana",    "knows_a"),
        ("Marcus", "Ivan",    "knows_b"),
        ("Marcus", "Alice",   "knows_c"),
        ("Hana",   "Ivan",    "knows_a"),
        ("Selin",  "Fatima",  "knows_a"),
    ]
    for src, dst, field in knows:
        patch(f"/person/{p[src]}", {field: ref("person", p[dst])})
    print(f"  ✓ {len(knows)} KNOWS edges")

    stats = _req("GET", "/graph/stats")
    print(f"  ✓ Graph: {stats['node_count']} nodes, {stats['edge_count']} edges")

    return {"p": p, "pr": pr, "sk": sk}


# ── Queries ───────────────────────────────────────────────────────────────────

def run_queries(ids: dict) -> None:
    hr("STEP 3 — COMPLEX GRAPH QUERIES")

    # Q1: two-hop reporting chain
    show(
        "Q1  Two hops below Zara (junior → manager → Zara)",
        query(
            "MATCH (junior:person)-[:reports_to]->(mid:person)-[:reports_to]->(top:person) "
            "WHERE top.name = 'Zara' "
            "RETURN junior.name AS junior, junior.department AS dept, "
            "       junior.location AS city, mid.name AS via "
            "ORDER BY mid.name, junior.name"
        ),
    )

    # Q2: three-hop reporting chain
    show(
        "Q2  Three hops below Zara (Dev → Bob → Alice → Zara)",
        query(
            "MATCH (j:person)-[:reports_to]->(m:person)"
            "     -[:reports_to]->(s:person)-[:reports_to]->(z:person) "
            "WHERE z.name = 'Zara' "
            "RETURN j.name AS junior, m.name AS direct_mgr, s.name AS senior_mgr",
            max_depth=6,
        ),
    )

    # Q3: cross-department KNOWS (engineering → data)
    show(
        "Q3  Engineers who KNOW someone in Data",
        query(
            "MATCH (eng:person)-[:knows_a|knows_b|knows_c]->(data:person) "
            "WHERE eng.department = 'Engineering' AND data.department = 'Data' "
            "RETURN eng.name AS engineer, eng.location AS from_city, "
            "       data.name AS data_person, data.location AS to_city"
        ),
    )

    # Q4: reverse direction
    show(
        "Q4  Data team members who KNOW someone in Engineering",
        query(
            "MATCH (d:person)-[:knows_a|knows_b]->(e:person) "
            "WHERE d.department = 'Data' AND e.department = 'Engineering' "
            "RETURN d.name AS data_person, e.name AS engineer"
        ),
    )

    # Q5: shared contacts (diamond pattern)
    show(
        "Q5  Who do Alice and Bob both KNOW? (shared contacts)",
        query(
            "MATCH (alice:person)-[:knows_a|knows_b|knows_c]->(shared:person)"
            "     <-[:knows_a|knows_b|knows_c]-(bob:person) "
            "WHERE alice.name = 'Alice' AND bob.name = 'Bob' "
            "RETURN shared.name AS mutual, shared.department AS dept"
        ),
    )

    # Q6: people working on active projects
    show(
        "Q6  People working on active projects",
        query(
            "MATCH (person:person)-[:works_on]->(proj:project) "
            "WHERE proj.status = 'active' "
            "RETURN person.name AS person, person.department AS dept, proj.name AS project "
            "ORDER BY proj.name, person.name"
        ),
    )

    # Q7: skill → project alignment
    show(
        "Q7  People whose skill is required by a project they work on",
        query(
            "MATCH (person:person)-[:has_skill]->(skill:skill)"
            "     <-[:requires_skill]-(proj:project) "
            "WHERE proj.status = 'active' "
            "RETURN person.name AS person, skill.name AS matched_skill, proj.name AS project "
            "ORDER BY proj.name"
        ),
    )

    # Q8: direct reporting relationships (avoids optional MATCH push-down issue)
    show(
        "Q8  Direct reporting relationships (who reports to whom)",
        query(
            "MATCH (p:person)-[:reports_to]->(mgr:person) "
            "RETURN p.name AS person, p.location AS city, p.level AS level, mgr.name AS manager "
            "ORDER BY p.level DESC, p.name",
            max_depth=3,
        ),
    )

    # Q9: shortest path
    show(
        "Q9  Shortest path from Dev to Zara",
        query(
            "MATCH path = shortestPath((dev:person)-[*]->(zara:person)) "
            "WHERE dev.name = 'Dev' AND zara.name = 'Zara' "
            "RETURN length(path) AS hops",
            max_depth=8,
        ),
    )

    # Q10: three-hop chain: lead ← project (active) and lead → knows → data person
    show(
        "Q10 Leads of active projects who KNOW a Data person (single MATCH chain)",
        query(
            "MATCH (contact:person)<-[:knows_a|knows_b|knows_c]-(lead:person)"
            "     <-[:lead]-(proj:project) "
            "WHERE proj.status = 'active' AND contact.department = 'Data' "
            "RETURN lead.name AS team_lead, proj.name AS project, contact.name AS data_contact"
        ),
    )

    # Q11: Berlin-to-Berlin KNOWS edges
    show(
        "Q11 KNOWS connections within the Berlin office",
        query(
            "MATCH (a:person)-[:knows_a|knows_b]->(b:person) "
            "WHERE a.location = 'Berlin' AND b.location = 'Berlin' "
            "RETURN a.name AS from_person, b.name AS to_person"
        ),
    )

    # Q12: reachability — can Eve reach Marcus? (any direction)
    show(
        "Q12 Can Eve reach Marcus through any relationship?",
        query(
            "MATCH path = shortestPath((eve:person)-[*]-(marcus:person)) "
            "WHERE eve.name = 'Eve' AND marcus.name = 'Marcus' "
            "RETURN length(path) AS hops",
            max_depth=8,
        ),
    )

    # Q13: skill category traversal — who works on projects requiring ML skills?
    show(
        "Q13 Who works on projects requiring ML skills?",
        query(
            "MATCH (person:person)-[:works_on]->(proj:project)-[:requires_skill]->(skill:skill) "
            "WHERE skill.category = 'ml' "
            "RETURN person.name AS person, proj.name AS project, skill.name AS skill"
        ),
    )

    # Q14: direct reports of Alice who KNOW a Data person (single MATCH chain)
    show(
        "Q14 Direct reports of Alice who KNOW someone in Data",
        query(
            "MATCH (data:person)<-[:knows_a|knows_b|knows_c]-(junior:person)"
            "     -[:reports_to]->(alice:person) "
            "WHERE alice.name = 'Alice' AND data.department = 'Data' "
            "RETURN junior.name AS person, data.name AS data_contact"
        ),
    )

    # Q15: two-hop KNOWS cycle: A knows B who knows A back (via outgoing + incoming)
    show(
        "Q15 Mutual KNOWS — Fatima and Guo know each other",
        query(
            "MATCH (a:person)-[:knows_a|knows_b]->(b:person)-[:knows_a|knows_b]->(a) "
            "WHERE a.name = 'Fatima' "
            "RETURN a.name AS person_a, b.name AS person_b"
        ),
    )

    # Q16: London office members and their managers
    show(
        "Q16 London employees and their managers",
        query(
            "MATCH (p:person)-[:reports_to]->(mgr:person) "
            "WHERE p.location = 'London' "
            "RETURN p.name AS londoner, p.department AS dept, mgr.name AS manager"
        ),
    )

    # Q17: project budget query (adapted table — direct SQL column access)
    show(
        "Q17 Active projects ordered by budget (adapted column query)",
        query(
            "MATCH (proj:project) "
            "WHERE proj.status = 'active' "
            "RETURN proj.name AS project, proj.budget_usd AS budget_usd "
            "ORDER BY proj.budget_usd DESC"
        ),
    )


# ── Main ──────────────────────────────────────────────────────────────────────

def _req(method, path, body=None):
    url  = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req  = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"HTTP {e.code} {method} {path}: {e.read().decode()[:200]}") from e


def main() -> None:
    binary = os.environ.get("XOLU_BINARY", "/tmp/xolu")
    if not os.path.exists(binary):
        sys.exit(f"xolu binary not found at {binary}. Set XOLU_BINARY.")

    db_file = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
    db_file.close()
    db_path = db_file.name

    env = os.environ.copy()
    env.update({
        "XOLU_DB_PATH":               db_path,
        "XOLU_PORT":                  "9090",
        "XOLU_GRAPH_MODE":            "flat",
        "XOLU_GRAPH_CYCLE_DETECTION": "warn",
        "XOLU_TENANT_MODE":           "path",
        "XOLU_TENANT_AUTO_REGISTER":  "true",
        "XOLU_LOG_LEVEL":             "error",
        "XOLU_MAX_QUERY_DEPTH":       "12",
    })

    print("Starting xolu server…")
    proc = subprocess.Popen(
        [binary], env=env,
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )

    try:
        wait_ready()
        print(f"Server ready  (PID {proc.pid})")

        register_schemas()
        ids = seed()
        run_queries(ids)

        hr("DONE")
        print(f"  DB: {db_path}")

    except KeyboardInterrupt:
        print("\nInterrupted.")
    except Exception as e:
        print(f"\nERROR: {e}", file=sys.stderr)
        import traceback; traceback.print_exc()
    finally:
        proc.terminate()
        try:   proc.wait(timeout=5)
        except subprocess.TimeoutExpired: proc.kill()
        try:   os.unlink(db_path)
        except OSError: pass


if __name__ == "__main__":
    main()
