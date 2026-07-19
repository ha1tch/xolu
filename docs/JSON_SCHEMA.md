# JSON Schema in xolu

xolu uses JSON Schema to validate entity data and to drive adapted table
creation. This document covers the supported keywords, how they affect
validation and storage, and the two xolu-specific extensions (`format: "ref"`
and `format: "decimal"`).

---

## Overview

Schemas are registered per entity type via the schema API:

```http
POST /api/v1/schema/{entity}
Content-Type: application/json

{ ...JSON Schema... }
```

Registering a schema does two things simultaneously:

1. **Validates** all subsequent writes to that entity type against the schema.
   Writes that fail validation are rejected with `XOLU-VL001` (422).
2. **Creates an adapted table.** The SQLite store derives a native-column table
   from the schema. Subsequent reads and OQL queries against that entity type
   use direct column access instead of `json_extract`, which is significantly
   faster (2.6×–124× depending on query pattern and dataset size).

Neither effect is optional when a schema is registered. Both take effect
immediately and persist across restarts (schemas are saved to `XOLU_SCHEMA_DIR`
as JSON files and reloaded at startup).

---

## Validation behaviour

xolu's validator runs in **Strict** mode for type checking but with
**permissive extra-field defaults**:

- Type mismatches are rejected. A field declared `"type": "integer"` rejects
  string values.
- Extra fields (fields present in the document but not in the schema) are
  **allowed by default**. If `additionalProperties: false` is set, extra fields
  are rejected at validation time and routed to an `_extra` overflow column in
  the adapted table (see [Adapted table columns](#adapted-table-columns)).
- Absent optional fields are allowed. A field is optional unless it appears in
  the top-level `"required"` array.
- Validation runs on every `POST`, `PUT`, `PATCH`, and `/commit` write. It does
  not run on reads.

If no schema is registered for an entity type, all writes pass (no-op
validator).

---

## Supported JSON Schema keywords

### `type`

Controls the SQL column type for adapted tables and the runtime type check.

| `type` value | SQL column | Validation |
|---|---|---|
| `"string"` | `TEXT` | Rejects non-string values |
| `"integer"` | `INTEGER` | Rejects non-integer values |
| `"number"` | `REAL` | Rejects non-numeric values |
| `"boolean"` | `INTEGER` (0/1) | Rejects non-boolean values |
| `"object"` | `TEXT` (JSON blob) | No deep validation unless nested schema declared |
| `"array"` | `TEXT` (JSON blob) | No item-level validation |

`"object"` and `"array"` fields are always stored as JSON text in the adapted
table regardless of their contents. OQL cannot push predicates into nested
object or array fields.

### `required`

```json
{
  "type": "object",
  "properties": {
    "name":  {"type": "string"},
    "email": {"type": "string"}
  },
  "required": ["name"]
}
```

Fields listed in `required` must be present in every write. In the adapted
table, required fields have `NOT NULL` column constraints. Optional fields
have nullable columns.

### `properties`

Defines the named fields of the entity. Every key under `properties` becomes
either a column (scalars, REF, decimal) or a blob column (objects, arrays) in
the adapted table.

The `id` field is special: it is a system-managed auto-increment column and
is silently ignored if declared in `properties`.

### `additionalProperties`

Controls what happens to fields not listed in `properties`:

| Value | Validation | Adapted table |
|---|---|---|
| `true` (default) | Extra fields allowed | `_extra TEXT` overflow column present |
| `false` | Extra fields rejected with `XOLU-VL001` | No `_extra` column |

The default is `true`. Set `additionalProperties: false` only when the schema
is exhaustive and you want to enforce it strictly.

**Data migration note:** Changing `additionalProperties` from `true` to `false`
after data already exists in the `_extra` column is rejected at schema
registration time to prevent data loss. The reverse change (false → true) adds
the `_extra` column automatically.

### `enum`

Restricts a field to a set of string values:

```json
"status": {
  "type": "string",
  "enum": ["draft", "published", "archived"]
}
```

Validated at write time. In the adapted table, enum fields are automatically
indexed (low-cardinality column, likely filtered on).

### Nested objects

Nested `"type": "object"` with a `properties` definition is supported in the
schema for documentation purposes, but the nested object is stored as a single
`TEXT` column (JSON blob) in the adapted table. OQL cannot push `WHERE`
predicates into nested fields.

```json
"address": {
  "type": "object",
  "properties": {
    "street": {"type": "string"},
    "city":   {"type": "string"}
  }
}
```

This creates a single `address TEXT` column. `WHERE address.city = 'Oslo'` is
not a supported OQL form against an adapted table.

---

## xolu-specific field types

### REF fields — `"format": "ref"`

A REF field declares a foreign-key relationship to another entity type. It
creates a graph edge when an entity is written, and is the mechanism that feeds
the Sulpher graph query engine.

```json
"author": {
  "type":   "object",
  "format": "ref"
}
```

The wire format in entity documents is:

```json
{
  "type":   "REF",
  "entity": "users",
  "id":     42
}
```

| Field | Type | Description |
|---|---|---|
| `type` | string, always `"REF"` | Marker that identifies this as a reference |
| `entity` | string | The entity type being referenced |
| `id` | integer | The ID of the referenced entity |

**Adapted table columns:** A REF field named `author` creates two columns:

| Column | SQL type | Content |
|---|---|---|
| `REF_author_entity` | `TEXT` | The value of `entity` |
| `REF_author_id` | `INTEGER` | The value of `id` |

The `_id` column is automatically indexed to support join lookups.

**Graph edge:** On every write, xolu creates a graph edge from the current node
to `entity:id` with the relationship label equal to the field name (`"author"`
in this example). The edge is updated on PATCH/PUT and removed on DELETE.

**OQL JOIN:** REF fields are the intended join key in OQL queries:

```sql
SELECT p.title, u.name AS author_name
FROM   posts AS p
INNER JOIN users AS u ON p.REF_author_id = u.id
```

**Naming convention:** Avoid naming REF fields with a `_ref` suffix
(e.g. `author_ref`). The resulting columns would be
`REF_author_ref_entity` / `REF_author_ref_id` and the graph edge label would
be `author_ref` — valid but redundant. The validator warns on this pattern.

**TSREF:** `"format": "tsref"` is a separate type for timeseries links. It
stores `{"type": "TSREF", "timeline": N, "dims": [...]}` and explicitly does
**not** create a graph edge. It is not currently validated by the schema layer
but is recognised by the OQL INSERT `@TIMESERIES` syntax.

### Decimal fields — `"format": "decimal"`

Exact fixed-point numbers for financial, metering, or scientific data where
IEEE 754 floating-point approximation is unacceptable.

```json
"price": {
  "type":             "string",
  "format":           "decimal",
  "decimalPrecision": 10,
  "decimalScale":     2
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `type` | string, must be `"string"` | — | Decimal values travel as JSON strings |
| `format` | `"decimal"` | — | Activates fixed-point storage |
| `decimalPrecision` | integer | 18 | Total significant digits (max 18) |
| `decimalScale` | integer | 4 | Digits after the decimal point |

**Why `"type": "string"`:** JSON numbers are IEEE 754 doubles. Sending a
decimal value like `10.99` as a JSON number loses precision in transit.
Decimal values are serialised as JSON strings (`"10.99"`) on both read and
write, bypassing the float64 round-trip entirely.

**Wire format:** Send and receive decimal values as strings:

```json
{
  "price": "10.99",
  "quantity": "1.500"
}
```

**Adapted table storage:** Stored as a scaled integer in an `INTEGER` column
(`value × 10^scale`). A `price` with scale 2 stores `10.99` as `1099`. This
preserves sort order and allows range queries via standard SQL comparisons.

**OQL aggregation:** `SUM`, `AVG`, `MIN`, `MAX` on decimal fields use exact
arithmetic via `shopspring/decimal`. Results are returned as strings.

**Validation:** The write path checks that the string value parses as a valid
decimal and that it fits within the declared precision and scale. Values that
would require truncation are rejected rather than silently rounded.

---

## Adapted table columns

For reference, the complete column set for an adapted entity:

| Column | SQL type | Source |
|---|---|---|
| `id` | `INTEGER NOT NULL` | System-managed auto-increment |
| `{field}` | varies | Each scalar `properties` field |
| `REF_{field}_entity` | `TEXT` | Each REF field (entity part) |
| `REF_{field}_id` | `INTEGER` | Each REF field (ID part) |
| `_extra` | `TEXT` | Only when `additionalProperties: true` (default) |
| `_version` | `INTEGER NOT NULL DEFAULT 1` | Optimistic concurrency counter |
| `created_at` | `TIMESTAMP` | Set on create |
| `updated_at` | `TIMESTAMP` | Updated on every write |

`_extra` stores extra fields as a JSON blob when `additionalProperties: true`.
Reading an entity from an adapted table merges the named columns back with any
`_extra` content to reconstruct the original document shape.

---

## Automatic indexing

The following fields are automatically indexed in the adapted table:

- All REF `_id` columns (to support join lookups)
- All decimal fields (likely used in range queries)
- All enum fields (low-cardinality, typically filtered on)

Other fields are not automatically indexed. Add explicit indexes at the
SQLite level if needed (`CREATE INDEX` against the adapted table directly).

---

## Schema evolution

Schemas can be updated by re-POSTing to the same endpoint. xolu computes the
diff between the old and new schema and applies it transactionally:

| Change | Behaviour |
|---|---|
| Add a field | `ALTER TABLE ADD COLUMN`; existing rows have `NULL` for the new column |
| Remove a field | Data moved to `_extra` (if present), then column dropped |
| Change a field's type | **Rejected.** Must be handled manually. |
| Change `additionalProperties` true → false | Rejected if `_extra` data exists |
| Change `additionalProperties` false → true | `_extra` column added |

**Type changes are always rejected** and must be handled out-of-band. The
typical approach is to add a new field with the correct type, backfill it, then
remove the old field in a second schema update.

---

## Schema loading at startup

On startup, xolu scans `XOLU_SCHEMA_DIR` for `{entity}.json` files and loads
them in alphabetical order. The OQL query engine also scans this directory
to discover entity types for query planning. Schema directory path defaults
to `{XOLU_BASE_DIR}/{XOLU_SCHEMA_NAME}/_schemas`.

---

## Complete example

```json
{
  "type": "object",
  "properties": {
    "title": {
      "type": "string"
    },
    "status": {
      "type": "string",
      "enum": ["draft", "published", "archived"]
    },
    "author": {
      "type":   "object",
      "format": "ref"
    },
    "price": {
      "type":             "string",
      "format":           "decimal",
      "decimalPrecision": 10,
      "decimalScale":     2
    },
    "tags": {
      "type": "array"
    },
    "metadata": {
      "type": "object"
    }
  },
  "required": ["title", "status"],
  "additionalProperties": false
}
```

This schema produces the following adapted table columns:

```
id                   INTEGER NOT NULL
title                TEXT NOT NULL
status               TEXT NOT NULL
REF_author_entity    TEXT
REF_author_id        INTEGER
price                INTEGER
tags                 TEXT
metadata             TEXT
_version             INTEGER NOT NULL DEFAULT 1
created_at           TIMESTAMP
updated_at           TIMESTAMP
```

`_extra` is absent because `additionalProperties: false`. `status` and
`REF_author_id` and `price` are automatically indexed. `title` is `NOT NULL`
because it is in `required`. The `tags` and `metadata` fields are stored as
JSON blobs in `TEXT` columns.
