#!/usr/bin/env python3
"""
xolu CRM seed script.

Creates a medium-sized CRM data model in a running xolu instance:
schema registration for six entity types (users, companies, contacts,
deals, activities, tasks) plus realistic, cross-referenced seed data.

Prerequisites
-------------
- A running xolu server (XOLU_BASE_URL, default http://localhost:8080).
- `iolu` on PATH (or pointed to via --iolu-bin) if the target tenant
  does not already exist -- this script shells out to
  `iolu tenant create --mode shared` to provision it. iolu needs direct
  filesystem access to the server's own data directory (--base-dir), so
  this only works when run on the same host as the server (or against
  a shared data volume). If the tenant already exists, iolu's own
  "already exists" response is treated as success, not an error --
  safe to re-run.
- `pip install requests`

IMPORTANT -- read this before assuming "a running server" is enough:
if the server is running with TenantMode=strict (the default this
project's own launcher configures) and the tenant does NOT already
exist, provisioning it via iolu here will NOT make it usable without a
server restart. The tenant registry is loaded into memory exactly once
at server startup; iolu writes through a separate connection an
already-running server never re-reads. This script's own iolu step
still runs (useful when the tenant already exists, or when you're
about to restart anyway), but if you're setting up a BRAND NEW tenant
from scratch, either restart the server after this script's iolu step
runs and before it starts creating entities, or -- much simpler --
provision the tenant before the server ever starts, which is exactly
what launch_xolu_for_crm.sh does. This isn't a corner case: it's the
normal path for a fresh setup, verified directly by actually hitting
it, not inferred from the docs.

Auth
----
If the server is running with tenant access control in scoped mode, an
API key is required. This script does NOT generate one for you
end-to-end -- xotogen mints a *candidate* key plus the config block the
server operator must add to the server's own APIKeyGrants
configuration (and typically restart the server for); a script cannot
safely do that half of the job on its own, and guessing at a running
server's dynconfig state is worse than just telling you the real
command:

    xotogen apikey --tenants <TENANT> --raw

Take the printed key, get it registered against the server (ask
whoever owns that config, or add it yourself if that's you), then pass
it here via --api-key or XOLU_API_KEY. Omit --api-key entirely for a
server running with AuthType=none (a typical local dev setup).

Usage
-----
    python3 xolu_crm_seed.py --tenant acme_crm --base-dir /var/lib/xolu

    python3 xolu_crm_seed.py --tenant acme_crm --base-dir /var/lib/xolu \\
        --api-key "$XOLU_API_KEY"

    python3 xolu_crm_seed.py --tenant acme_crm --base-dir /var/lib/xolu \\
        --skip-tenant-create  # tenant already provisioned

Volumes are modest by default (medium-sized, not a load-test dataset):
5 users, 15 companies, ~35 contacts, 25 deals, 50 activities, 20 tasks.
Override with --scale.
"""

import argparse
import json
import os
import random
import subprocess
import sys
from datetime import datetime, timedelta, timezone

try:
    import requests
except ImportError:
    sys.exit("This script requires the 'requests' package: pip install requests")


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def parse_args():
    p = argparse.ArgumentParser(description="Seed a xolu instance with CRM data.")
    p.add_argument("--tenant", required=True,
                    help="Tenant name to seed data into (created if it doesn't exist).")
    p.add_argument("--tenant-id", type=int, default=None,
                    help="Explicit numeric tenant ID (default: iolu auto-assigns).")
    p.add_argument("--base-url", default=os.environ.get("XOLU_BASE_URL", "http://localhost:8080"),
                    help="xolu server base URL (env: XOLU_BASE_URL).")
    p.add_argument("--base-dir", default=os.environ.get("XOLU_BASE_DIR"),
                    help="xolu server's data directory, needed for tenant provisioning via iolu "
                         "(env: XOLU_BASE_DIR). Required unless --skip-tenant-create is set.")
    p.add_argument("--api-key", default=os.environ.get("XOLU_API_KEY"),
                    help="API key for a scoped-auth server (env: XOLU_API_KEY). "
                         "Omit for AuthType=none.")
    p.add_argument("--iolu-bin", default="iolu", help="Path to the iolu binary (default: on PATH).")
    p.add_argument("--xotogen-bin", default="xotogen",
                    help="Path to the xotogen binary, used only to print a hint if auth fails.")
    p.add_argument("--skip-tenant-create", action="store_true",
                    help="Assume the tenant is already provisioned; skip the iolu step entirely.")
    p.add_argument("--scale", type=float, default=1.0,
                    help="Multiplier on the default seed volumes (default: 1.0).")
    p.add_argument("--seed", type=int, default=42, help="Random seed, for reproducible runs.")
    return p.parse_args()


# ---------------------------------------------------------------------------
# iolu: tenant provisioning
# ---------------------------------------------------------------------------

def ensure_tenant(iolu_bin, base_dir, tenant_name, tenant_id):
    if not base_dir:
        sys.exit("--base-dir (or XOLU_BASE_DIR) is required to provision a tenant via iolu. "
                  "Pass --skip-tenant-create if the tenant already exists and iolu isn't reachable here.")

    # --mode shared is required, not optional: on a fresh/empty base dir,
    # iolu's own auto-detection has nothing to detect and falls back to
    # per-file mode by default, while xolu's own server config defaults
    # to SQLitePerFileTenants=false (shared). Left unset here, iolu
    # writes the tenant registry into a per-tenant file the server's
    # shared-mode store never opens -- every request against that
    # tenant then 404s with "Unknown tenant", regardless of the order
    # iolu and the server run in. Verified directly against a real
    # server before this fix existed.
    cmd = [iolu_bin, "tenant", "create", "--base-dir", base_dir, "--mode", "shared", "--name", tenant_name]
    if tenant_id is not None:
        cmd += ["--id", str(tenant_id)]

    print(f"$ {' '.join(cmd)}")
    result = subprocess.run(cmd, capture_output=True, text=True)

    if result.returncode == 0:
        print(f"  {result.stdout.strip()}")
        return

    stderr = result.stderr.strip()
    if "already exists" in stderr:
        print(f"  tenant {tenant_name!r} already exists -- continuing.")
        return

    sys.exit(f"iolu tenant create failed:\n  {stderr}\n\n"
              f"(Is '{iolu_bin}' on PATH? Override with --iolu-bin.)")


def print_xotogen_hint(tenant_name, xotogen_bin):
    print()
    print("Authentication failed. If this server requires per-tenant API keys, generate one with:")
    print()
    print(f"    {xotogen_bin} apikey --tenants {tenant_name} --raw")
    print()
    print("...then get the accompanying config block (drop --raw to see it) registered against")
    print("the server, and re-run this script with --api-key <that key>.")
    print()


# ---------------------------------------------------------------------------
# xolu HTTP client
# ---------------------------------------------------------------------------

class XoluClient:
    def __init__(self, base_url, tenant, api_key=None):
        # Two distinct base paths, not one -- confirmed directly against
        # pkg/server/server.go's own route registration, not assumed:
        # schema operations are explicitly "tenant-independent, always
        # available" (the router's own comment), registered at the bare
        # /api/v1/schema/{entity} with NO tenant prefix at all, while
        # every entity CRUD path is tenant-scoped. Registering a schema
        # against the tenant-prefixed path returns 405 (a route pattern
        # matches, just not for POST) rather than a clean failure --
        # caught by running this end to end against a real server, not
        # assumed from the docs alone.
        root = base_url.rstrip("/") + "/api/v1"
        self.schema_base = root
        self.base = root + f"/tenant/{tenant}"
        self.session = requests.Session()
        if api_key:
            self.session.headers["Authorization"] = f"Bearer {api_key}"
        self.session.headers["Content-Type"] = "application/json"

    def register_schema(self, entity_type, schema):
        resp = self.session.post(f"{self.schema_base}/schema/{entity_type}", json=schema)
        if resp.status_code not in (200, 201):
            raise RuntimeError(f"schema registration for {entity_type!r} failed "
                                f"({resp.status_code}): {resp.text}")
        return resp.json()

    def create_entity(self, entity_type, data):
        resp = self.session.post(f"{self.base}/{entity_type}", json=data)
        if resp.status_code not in (200, 201):
            raise RuntimeError(f"create {entity_type!r} failed ({resp.status_code}): "
                                f"{resp.text}\npayload: {json.dumps(data)}")
        return resp.json()["id"]


def ref(entity_type, entity_id):
    """Wire format for a REF field value (docs/JSON_SCHEMA.md)."""
    return {"type": "REF", "entity": entity_type, "id": entity_id}


# ---------------------------------------------------------------------------
# CRM schema definitions
# ---------------------------------------------------------------------------

SCHEMAS = {
    "users": {
        "type": "object",
        "properties": {
            "name": {"type": "string"},
            "email": {"type": "string"},
            "role": {"type": "string", "enum": ["sales_rep", "sales_manager", "admin"]},
            "active": {"type": "boolean"},
        },
        "required": ["name", "email", "role", "active"],
        "additionalProperties": False,
    },
    "companies": {
        "type": "object",
        "properties": {
            "name": {"type": "string"},
            "industry": {"type": "string", "enum": [
                "technology", "manufacturing", "retail", "healthcare",
                "finance", "logistics", "education", "hospitality",
            ]},
            "website": {"type": "string"},
            "phone": {"type": "string"},
            "address": {
                "type": "object",
                "properties": {
                    "street": {"type": "string"},
                    "city": {"type": "string"},
                    "state": {"type": "string"},
                    "country": {"type": "string"},
                },
            },
            "size": {"type": "string", "enum": ["small", "medium", "large", "enterprise"]},
            "owner": {"type": "object", "format": "ref"},
        },
        "required": ["name", "industry", "size", "owner"],
        "additionalProperties": False,
    },
    "contacts": {
        "type": "object",
        "properties": {
            "first_name": {"type": "string"},
            "last_name": {"type": "string"},
            "email": {"type": "string"},
            "phone": {"type": "string"},
            "title": {"type": "string"},
            "company": {"type": "object", "format": "ref"},
            "owner": {"type": "object", "format": "ref"},
        },
        "required": ["first_name", "last_name", "email", "company", "owner"],
        "additionalProperties": False,
    },
    "deals": {
        "type": "object",
        "properties": {
            "name": {"type": "string"},
            "company": {"type": "object", "format": "ref"},
            "primary_contact": {"type": "object", "format": "ref"},
            "owner": {"type": "object", "format": "ref"},
            "stage": {"type": "string", "enum": [
                "prospecting", "qualification", "proposal",
                "negotiation", "closed_won", "closed_lost",
            ]},
            "amount": {
                "type": "string", "format": "decimal",
                "decimalPrecision": 12, "decimalScale": 2,
            },
            "currency": {"type": "string"},
            "close_date": {"type": "string"},
            "probability": {"type": "integer"},
            "created_date": {"type": "string"},
        },
        "required": ["name", "company", "primary_contact", "owner", "stage",
                     "amount", "currency", "close_date", "probability", "created_date"],
        "additionalProperties": False,
    },
    "activities": {
        "type": "object",
        "properties": {
            "type": {"type": "string", "enum": ["call", "email", "meeting", "note"]},
            "subject": {"type": "string"},
            "contact": {"type": "object", "format": "ref"},
            "deal": {"type": "object", "format": "ref"},
            "owner": {"type": "object", "format": "ref"},
            "activity_date": {"type": "string"},
            "notes": {"type": "string"},
            "duration_minutes": {"type": "integer"},
        },
        "required": ["type", "subject", "contact", "owner", "activity_date"],
        "additionalProperties": False,
    },
    "tasks": {
        "type": "object",
        "properties": {
            "title": {"type": "string"},
            "description": {"type": "string"},
            "due_date": {"type": "string"},
            "status": {"type": "string", "enum": ["open", "completed", "cancelled"]},
            "priority": {"type": "string", "enum": ["low", "medium", "high"]},
            "contact": {"type": "object", "format": "ref"},
            "deal": {"type": "object", "format": "ref"},
            "owner": {"type": "object", "format": "ref"},
        },
        "required": ["title", "due_date", "status", "priority", "owner"],
        "additionalProperties": False,
    },
}


# ---------------------------------------------------------------------------
# Seed data: name pools (hand-written, no faker dependency)
# ---------------------------------------------------------------------------

FIRST_NAMES = [
    "Ava", "Liam", "Noah", "Emma", "Oliver", "Sophia", "Elijah", "Mia",
    "Lucas", "Isabella", "Mason", "Amelia", "Ethan", "Harper", "James",
    "Evelyn", "Benjamin", "Abigail", "Henry", "Emily", "Alexander", "Ella",
    "Sebastian", "Scarlett", "Jack", "Grace", "Daniel", "Chloe", "Owen", "Lily",
]
LAST_NAMES = [
    "Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller",
    "Davis", "Rodriguez", "Martinez", "Hernandez", "Lopez", "Gonzalez",
    "Wilson", "Anderson", "Thomas", "Taylor", "Moore", "Jackson", "Martin",
    "Lee", "Perez", "Thompson", "White", "Harris", "Sanchez", "Clark",
]
COMPANY_PREFIXES = [
    "Northwind", "Vertex", "Summit", "Cascade", "Meridian", "Pinnacle",
    "Anchor", "Beacon", "Harbor", "Ironclad", "Lumen", "Nexus", "Orbit",
    "Quartz", "Redwood", "Sterling",
]
COMPANY_SUFFIXES = ["Systems", "Group", "Industries", "Partners", "Solutions", "Holdings", "Labs", "Works"]
STREET_NAMES = ["Main St", "Oak Ave", "Market St", "Industrial Pkwy", "Elm St", "Harbor Rd", "5th Ave"]
CITIES = [
    ("Austin", "TX"), ("Denver", "CO"), ("Seattle", "WA"), ("Chicago", "IL"),
    ("Atlanta", "GA"), ("Boston", "MA"), ("Portland", "OR"), ("Raleigh", "NC"),
]
DEAL_NOUNS = ["Platform Rollout", "Annual Contract", "Pilot Program", "Renewal",
              "Expansion", "Implementation", "Licensing Deal", "Upgrade"]
ACTIVITY_SUBJECTS = {
    "call": ["Discovery call", "Pricing discussion", "Check-in call", "Contract review call"],
    "email": ["Sent proposal", "Follow-up email", "Scheduling email", "Sent case study"],
    "meeting": ["Kickoff meeting", "Demo", "Quarterly business review", "Negotiation meeting"],
    "note": ["Internal note", "Competitor mention", "Budget note"],
}
TASK_TITLES = ["Send follow-up proposal", "Schedule demo", "Confirm contract terms",
               "Prepare pricing sheet", "Check in after onboarding", "Renew subscription discussion"]


def rand_date(start_days_ago, end_days_ago):
    days = random.randint(end_days_ago, start_days_ago)
    return (datetime.now(timezone.utc) - timedelta(days=days)).strftime("%Y-%m-%dT%H:%M:%SZ")


def rand_future_date(max_days_ahead):
    days = random.randint(1, max_days_ahead)
    return (datetime.now(timezone.utc) + timedelta(days=days)).strftime("%Y-%m-%dT%H:%M:%SZ")


# ---------------------------------------------------------------------------
# Seed data generators
# ---------------------------------------------------------------------------

def gen_users(n):
    roles = ["sales_manager"] + ["sales_rep"] * (n - 1) if n > 1 else ["sales_manager"]
    used = set()
    users = []
    for i in range(n):
        while True:
            first, last = random.choice(FIRST_NAMES), random.choice(LAST_NAMES)
            if (first, last) not in used:
                used.add((first, last))
                break
        users.append({
            "name": f"{first} {last}",
            "email": f"{first.lower()}.{last.lower()}@ourcrm.example",
            "role": roles[i],
            "active": True,
        })
    return users


def gen_companies(n, owner_ids):
    used_names = set()
    companies = []
    for _ in range(n):
        while True:
            name = f"{random.choice(COMPANY_PREFIXES)} {random.choice(COMPANY_SUFFIXES)}"
            if name not in used_names:
                used_names.add(name)
                break
        city, state = random.choice(CITIES)
        slug = name.lower().replace(" ", "")
        companies.append({
            "name": name,
            "industry": random.choice(SCHEMAS["companies"]["properties"]["industry"]["enum"]),
            "website": f"https://{slug}.example.com",
            "phone": f"+1-{random.randint(200,999)}-{random.randint(200,999)}-{random.randint(1000,9999)}",
            "address": {
                "street": f"{random.randint(100,9999)} {random.choice(STREET_NAMES)}",
                "city": city,
                "state": state,
                "country": "USA",
            },
            "size": random.choice(["small", "medium", "large", "enterprise"]),
            "owner": ref("users", random.choice(owner_ids)),
        })
    return companies


def gen_contacts(n, company_ids, owner_ids):
    contacts = []
    titles = ["VP of Operations", "Procurement Manager", "CTO", "IT Director",
              "Head of Sales", "Finance Director", "COO", "Product Manager"]
    for _ in range(n):
        first, last = random.choice(FIRST_NAMES), random.choice(LAST_NAMES)
        company_id = random.choice(company_ids)
        contacts.append({
            "first_name": first,
            "last_name": last,
            "email": f"{first.lower()}.{last.lower()}{random.randint(1,999)}@example.com",
            "phone": f"+1-{random.randint(200,999)}-{random.randint(200,999)}-{random.randint(1000,9999)}",
            "title": random.choice(titles),
            "company": ref("companies", company_id),
            "owner": ref("users", random.choice(owner_ids)),
        })
    return contacts


def gen_deals(n, companies_with_contacts, owner_ids):
    """companies_with_contacts: list of (company_id, [contact_ids])."""
    deals = []
    for _ in range(n):
        company_id, contact_ids = random.choice(companies_with_contacts)
        stage = random.choice(SCHEMAS["deals"]["properties"]["stage"]["enum"])
        probability = {
            "prospecting": 10, "qualification": 25, "proposal": 50,
            "negotiation": 75, "closed_won": 100, "closed_lost": 0,
        }[stage]
        deals.append({
            "name": f"{random.choice(DEAL_NOUNS)} - {datetime.now(timezone.utc).year}",
            "company": ref("companies", company_id),
            "primary_contact": ref("contacts", random.choice(contact_ids)),
            "owner": ref("users", random.choice(owner_ids)),
            "stage": stage,
            "amount": f"{random.randint(5, 500) * 1000}.{random.randint(0,99):02d}",
            "currency": "USD",
            "close_date": rand_future_date(120) if stage not in ("closed_won", "closed_lost") else rand_date(90, 0),
            "probability": probability,
            "created_date": rand_date(180, 30),
        })
    return deals


def gen_activities(n, contact_ids, deal_ids, owner_ids):
    activities = []
    for _ in range(n):
        atype = random.choice(list(ACTIVITY_SUBJECTS.keys()))
        entry = {
            "type": atype,
            "subject": random.choice(ACTIVITY_SUBJECTS[atype]),
            "contact": ref("contacts", random.choice(contact_ids)),
            "owner": ref("users", random.choice(owner_ids)),
            "activity_date": rand_date(90, 0),
            "notes": "Auto-generated seed activity.",
        }
        if deal_ids and random.random() < 0.6:
            entry["deal"] = ref("deals", random.choice(deal_ids))
        if atype in ("call", "meeting"):
            entry["duration_minutes"] = random.choice([15, 30, 45, 60])
        activities.append(entry)
    return activities


def gen_tasks(n, contact_ids, deal_ids, owner_ids):
    tasks = []
    for _ in range(n):
        entry = {
            "title": random.choice(TASK_TITLES),
            "description": "Auto-generated seed task.",
            "due_date": rand_future_date(30),
            "status": random.choices(["open", "completed", "cancelled"], weights=[70, 25, 5])[0],
            "priority": random.choice(["low", "medium", "high"]),
            "owner": ref("users", random.choice(owner_ids)),
        }
        if random.random() < 0.7:
            entry["contact"] = ref("contacts", random.choice(contact_ids))
        if deal_ids and random.random() < 0.5:
            entry["deal"] = ref("deals", random.choice(deal_ids))
        tasks.append(entry)
    return tasks


# ---------------------------------------------------------------------------
# Orchestration
# ---------------------------------------------------------------------------

def create_all(client, entity_type, records, label):
    ids = []
    for record in records:
        ids.append(client.create_entity(entity_type, record))
    print(f"  created {len(ids)} {label}")
    return ids


def print_unknown_tenant_hint(tenant_name):
    print()
    print(f"The server still doesn't recognise tenant {tenant_name!r}, even though this")
    print("script's own iolu step just reported success. This is a real, structural")
    print("limitation, not a bug in the retry logic: under TenantMode=strict, the")
    print("server loads its tenant registry into memory exactly ONCE, at startup")
    print("(pkg/tenant/tenant.go's own Registry.LoadFrom). iolu writes the new tenant")
    print("through a separate database connection that an already-running server never")
    print("re-reads -- no amount of retrying from this script will fix that.")
    print()
    print("Two ways forward:")
    print("  1. Restart the xolu server now that the tenant exists on disk, then re-run")
    print("     this script with --skip-tenant-create.")
    print("  2. Provision the tenant BEFORE the server starts next time -- this is what")
    print("     launch_xolu_for_crm.sh does, in the correct order, for exactly this")
    print("     reason.")
    print()


def main():
    args = parse_args()
    random.seed(args.seed)

    n_users = max(1, round(5 * args.scale))
    n_companies = max(1, round(15 * args.scale))
    n_contacts = max(1, round(35 * args.scale))
    n_deals = max(1, round(25 * args.scale))
    n_activities = max(1, round(50 * args.scale))
    n_tasks = max(1, round(20 * args.scale))

    print(f"=== xolu CRM seed: tenant={args.tenant!r} scale={args.scale} ===\n")

    if not args.skip_tenant_create:
        print("-- provisioning tenant via iolu --")
        ensure_tenant(args.iolu_bin, args.base_dir, args.tenant, args.tenant_id)
        print()

    client = XoluClient(args.base_url, args.tenant, args.api_key)

    print("-- registering schemas --")
    try:
        for entity_type, schema in SCHEMAS.items():
            client.register_schema(entity_type, schema)
            print(f"  registered schema: {entity_type}")
    except RuntimeError as e:
        if "401" in str(e) or "403" in str(e):
            print_xotogen_hint(args.tenant, args.xotogen_bin)
        sys.exit(str(e))
    print()

    print("-- seeding entities --")
    try:
        user_ids = create_all(client, "users", gen_users(n_users), "users")

        companies = gen_companies(n_companies, user_ids)
        company_ids = create_all(client, "companies", companies, "companies")

        contacts = gen_contacts(n_contacts, company_ids, user_ids)
        contact_ids = create_all(client, "contacts", contacts, "contacts")

        # Group contacts by company so deals reference a contact that
        # genuinely belongs to the deal's own company.
        contacts_by_company = {}
        for contact, contact_id in zip(contacts, contact_ids):
            company_id = contact["company"]["id"]
            contacts_by_company.setdefault(company_id, []).append(contact_id)
        companies_with_contacts = [(cid, cids) for cid, cids in contacts_by_company.items() if cids]

        deals = gen_deals(n_deals, companies_with_contacts, user_ids)
        deal_ids = create_all(client, "deals", deals, "deals")

        activities = gen_activities(n_activities, contact_ids, deal_ids, user_ids)
        create_all(client, "activities", activities, "activities")

        tasks = gen_tasks(n_tasks, contact_ids, deal_ids, user_ids)
        create_all(client, "tasks", tasks, "tasks")
    except RuntimeError as e:
        if "401" in str(e) or "403" in str(e):
            print_xotogen_hint(args.tenant, args.xotogen_bin)
        elif "Unknown tenant" in str(e):
            print_unknown_tenant_hint(args.tenant)
        sys.exit(str(e))

    print(f"\n=== done: tenant {args.tenant!r} seeded ===")


if __name__ == "__main__":
    main()
