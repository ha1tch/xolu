# Upgrade Guide

This document covers the upgrade path for production deployments. It is
organised by the version you are upgrading *from*.

---

## From v0.10.1 to v0.10.2

### Breaking: JWTs must now carry an `exp` claim

v0.10.2 makes the JWT `exp` (expiry) claim **mandatory** when `AuthType` is
`jwt`. A presented token with no `exp` claim is now rejected with `401`, where
previously an absent `exp` was treated as "never expires" and the token was
accepted.

This was a deliberate security decision (audit defect D-002): an unbounded-
lifetime token cannot be invalidated except by rotating the signing secret, so a
leaked token would remain valid indefinitely.

**Action required:** ensure every issuer that mints tokens for this server sets
an `exp` claim. The claim may be encoded as a JSON number or a numeric string —
both are accepted (v0.10.2 also fixes D-002's type-assertion bug, where a
string-encoded `exp` previously bypassed the expiry check entirely). The `nbf`
(not-before) claim remains optional.

No action is needed if your tokens already set `exp` (the conventional case).

This is the only breaking change in v0.10.2; it is otherwise a drop-in upgrade
over v0.10.1 with no on-disk or schema changes.

---

## From v0.9.7 or v0.9.8 to v0.9.9

### Breaking: database schema changed

v0.9.9 (specifically rc11–rc14) replaced the global `entities`,
`entity_sequences`, and `entities_fts` tables with per-tenant tables using a
new naming scheme:

| Old table | New table |
|---|---|
| `entities` | `t0000_nodes` |
| `entity_sequences` | `t0000_nseq` |
| `entities_fts` | `t0000_nfts` |

**v0.9.9 will not read data from an old-schema database.** If you start
v0.9.9 against a v0.9.7 or v0.9.8 database, the new table family will be
created empty and the server will appear to work but have no data.

**Migration procedure:**

There is currently no automated migration tool for this schema change.
The supported upgrade path is:

1. Export your data from the old server: `GET /api/v1/export`
2. Deploy v0.9.9 against a fresh database
3. Re-import your data

If you have a small dataset and need in-place migration, the manual steps are:

```sql
-- Run against your existing database before starting v0.9.9

-- Copy entities into the new table
CREATE TABLE IF NOT EXISTS t0000_nodes AS SELECT * FROM entities;

-- Create the sequence table (v0.9.9 will not auto-populate from the old table)
CREATE TABLE IF NOT EXISTS t0000_nseq (
    entity_type TEXT NOT NULL PRIMARY KEY,
    next_id     INTEGER NOT NULL DEFAULT 1
);
INSERT INTO t0000_nseq (entity_type, next_id)
    SELECT entity_type, MAX(id) + 1 FROM entities GROUP BY entity_type;

-- FTS: v0.9.9 will rebuild this on next startup if the table is absent
```

This is a manual procedure. Test it on a copy of your data first.

### Tool compatibility against v0.9.9

**`iolu`** — commands that touch only the `tenants` table work correctly:
`tenant create`, `tenant provision-ts`. Commands that query `entities`
(`db init`, `db status`, `db upgrade`, `tenant list`, `tenant info`,
`tenant delete`) return wrong counts or do nothing useful against a v0.9.9
database. See [IOLU.md](IOLU.md) for per-command details.

---

## From v0.9.6 or earlier to v0.9.7

v0.9.7 introduced multi-tenancy and the `tenant_id` column. The migration
tool that handled this transition has been removed. If you are upgrading from
v0.9.6 or earlier to v0.9.9 directly, use the manual SQL procedure described
in the v0.9.7→v0.9.9 section above.

---
