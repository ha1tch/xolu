# jsonplate

A *jsonplate* is a JSON document that doubles as a template: most of it is
literal output, but any leaf of the form `{"$ref": "path"}` is a reference that
is replaced, at render time, by the value found at that path in a supplied data
context. It is the mechanism xolu uses to shape event-notification payloads.

## What it is

A jsonplate is ordinary JSON with one special form. An object consisting of
exactly one key, `$ref`, whose value is a string, is a *reference*:

```json
{ "$ref": "affected[0].ref.id" }
```

At render time that object is replaced by whatever the path `affected[0].ref.id`
resolves to in the data. Everything else — objects, arrays, strings, numbers,
booleans, null — is a literal and is copied through unchanged. The shape of the
jsonplate is the shape of the output; references only substitute values, never
structure.

Rendering has two operations, at two different times:

- **Validation, at definition time.** `Validate` checks that the jsonplate is
  well-formed JSON and that every `$ref` is a lone string-valued key. A
  malformed template is rejected when the event def is created, not when it
  fires.
- **Resolution, at render time.** `Render` walks the template, resolves each
  `$ref` against the data via the path engine, and emits the result. A reference
  whose path is absent resolves to `null` rather than failing the whole render.

Path resolution is delegated to queryfy's query language. The reference path
supports field access, nested access, and array indexing:

```
current
vars.retries
affected
affected[0]
affected[0].ref.id
```

## What it is good for

jsonplate exists for the case where the output is **structured data with a few
dynamic values pulled from a context** — which is exactly the shape of an event
notification. The payload is mostly a fixed structure (a known set of fields the
receiver expects), and a handful of those fields come from the event.

It is well suited to:

- **Webhook and notification payloads** whose shape the receiver depends on, but
  whose values vary per event. The author writes the structure once; the dynamic
  values are references.
- **Reaching into nested event data** — `affected[0].ref.id`, `vars.retries` —
  where the value the receiver wants is several levels inside the event.
- **Carrying structured values whole** — `{"$ref": "affected"}` substitutes an
  entire array; `{"$ref": "request"}` substitutes an entire object. The result
  is valid, typed JSON, not a stringified blob.

## Why it is better than string templating for these cases

The alternative — a free-text body string with `{{...}}` holes — has two
problems that jsonplate does not:

1. **String templating produces strings.** Interpolating a nested object or an
   array into a `{{...}}` body yields a stringified rendering, not structured
   JSON. A receiver cannot parse `map[ref:map[...]]` cleanly. jsonplate
   substitutes the *value*, so a referenced object stays an object and a
   referenced number stays a number.

2. **String templating cannot be validated as a structure.** A body string is
   opaque text until it is rendered; a malformed reference is discovered only
   when the webhook fires (or never). A jsonplate is a structure, so it can be
   validated when the event def is created — a bad reference is caught up front.

In short: when the output is JSON and the receiver cares about its shape and
types, a structured template that *is* JSON beats a string with holes punched in
it.

## Why it is not Go's template system

jsonplate is **not** `text/template` (or `html/template`), and the difference is
deliberate.

- **No template language.** `text/template` is a small programming language:
  `{{if}}`, `{{range}}`, `{{with}}`, pipelines, function calls. jsonplate has
  exactly one construct — the `$ref` leaf. There is no control flow, no
  iteration, no functions. A jsonplate cannot loop, branch, or compute; it can
  only substitute a value at a path. That is a feature: the template surface is
  tiny, there is nothing to misuse, and a template cannot error at render time
  the way a `text/template` action can.

- **Data in, data out.** `text/template` renders to a string. jsonplate renders
  JSON to JSON — the input is a JSON structure and the output is a JSON
  structure. Substitution preserves types and nesting; there is no
  stringification step.

- **Structure is fixed by the author, values are referenced.** In
  `text/template` the template *is* text and the data is threaded through it. In
  a jsonplate the template *is* the output shape, and references name values to
  drop into it.

If you need conditionals, loops, or computed output, jsonplate is the wrong
tool — that is `text/template`'s job, and adopting it would mean taking on its
whole language surface. jsonplate intentionally does less.

## How to apply it correctly

**Write the output shape you want, then replace the dynamic values with
references.**

Start from the literal payload the receiver expects:

```json
{ "asset": "", "transitioned_to": "", "retries": 0 }
```

Replace each value that should come from the event with a reference:

```json
{
  "asset":           { "$ref": "machine_id" },
  "transitioned_to": { "$ref": "current" },
  "retries":         { "$ref": "vars.retries" }
}
```

Guidance:

- **A reference is a lone `$ref` key.** `{"$ref": "current"}` is a reference.
  `{"$ref": "current", "label": "x"}` is *malformed* and is rejected by
  validation — a reference object may not carry other keys. Put the literal
  alongside, not inside: `{"value": {"$ref": "current"}, "label": "x"}`.

- **Reference whole structures when you want them whole.**
  `{"$ref": "affected"}` gives the entire array; `{"$ref": "request"}` the
  entire request object. Reference a path into them when you want a piece:
  `{"$ref": "affected[0].ref.id"}`.

- **Absent paths render as `null`.** A reference to a path that is not present in
  the event data becomes `null`, not an error. Design the receiver to tolerate a
  null where an optional field's source may be absent.

- **Literals pass through untouched at any depth.** Strings, numbers, booleans,
  null, and nested objects/arrays that contain no `$ref` are copied verbatim.
  Mix literals and references freely.

- **The reference context is the event's data.** Paths resolve against the
  event's `data` payload. Which fields exist depends on the event type (for
  example, an `fsm.step` event's data has `previous`, `current`, `terminal`,
  `vars`, `machine_id`; a `commit.applied` event's data has `affected` and
  `request`). Reference only fields the event carries.

## A complete example

Template (the jsonplate, as carried in an event def's `config`):

```json
{
  "all_affected": { "$ref": "affected" },
  "first_id":     { "$ref": "affected[0].ref.id" },
  "created":      { "$ref": "affected[0].created" },
  "the_request":  { "$ref": "request" }
}
```

Event data it is rendered against:

```json
{
  "affected": [
    { "created": true, "ref": { "entity": "asset", "id": 9200, "type": "REF" }, "version": 1 },
    { "ref": { "entity": "audit_log", "id": 1, "type": "REF" } }
  ],
  "request": {
    "update": { "entity": "asset", "id": 9200, "data": { "state": "x" } },
    "append": [ { "entity": "audit_log", "data": { "note": "n" } } ]
  }
}
```

Rendered result:

```json
{
  "all_affected": [
    { "created": true, "ref": { "entity": "asset", "id": 9200, "type": "REF" }, "version": 1 },
    { "ref": { "entity": "audit_log", "id": 1, "type": "REF" } }
  ],
  "first_id": 9200,
  "created": true,
  "the_request": {
    "update": { "entity": "asset", "id": 9200, "data": { "state": "x" } },
    "append": [ { "entity": "audit_log", "data": { "note": "n" } } ]
  }
}
```

Each `$ref` was replaced by the value at its path; `first_id` resolved to the
number `9200` (a number, not the string `"9200"`); whole-structure references
(`affected`, `request`) were substituted intact.

## Where it sits in delivery

When jsonplate is used for a webhook event def, the rendered result becomes the
`message` half of the delivered payload. xolu always wraps it in an `origin`
provenance block, so the delivered body is:

```json
{ "origin": { ... }, "message": <the rendered jsonplate> }
```

The jsonplate author controls `message`; `origin` is stamped by xolu and is not
part of the template.
