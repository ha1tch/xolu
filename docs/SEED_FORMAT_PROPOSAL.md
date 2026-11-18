# The `.seed` format: review and proposed improvements

Updated: 2026-11-17

> **Reconciliation for xolu, 2026-11-18 (0.30.45).** The loader, the specification and the 22
> general conformance cases named below are in this repository from 0.30.45: `scripts/seed_system/`,
> `docs/SEED_FORMAT.md`, `scripts/seed_conformance/`. `scripts/seed.py` is that loader, and the
> version 1 loader this review examined is gone. Paths under `seeds/` in the note that follows and
> in the text are Seam's. P-6 is XOT256; the Go command is XOT255.
>
> **Reconciliation, 2026-11-18.** The decision was to proceed. Seam now carries a loader,
> `seeds/seed_system/`, a specification of format version 2, `docs/SEED_FORMAT.md`, and a
> conformance suite, `seeds/conformance/`; its six seeds are `.seed` files. Of the proposals
> below, these exist there: P-1, P-2, P-3, P-4, P-5, P-7, P-8, P-11, P-13, P-14, P-16, P-17,
> P-19, P-21, P-23, P-24, and P-22 on the Seam side (the specification and the tests). P-8 grew
> two forms the proposal did not have: a day with a fixed time of day, and dates inside a text.
> P-13 was narrowed: a whole dataset may load together with files that are not one. P-9 and P-12
> followed the same day, as `match` (a record found by its own fields), `find` (a lookup that
> writes nothing) and the header keys `addition` and `requires`; with them the work-order seeds
> became `.seed` files, and the Issues and accounting seeds were written as additions. Not done:
> P-6 (a xolu server fix), P-10, P-15, P-18 and P-20 beyond connection reuse. The Go command for xolu and the normalization of
> xoluman come next. The text below is the proposal as it was written and is not updated.

Status: **proposal, accepted and partly built** (see the note above), for the xolu and Seam teams.
Everything under "What was observed" was run, not inferred: xolu 0.30.44 and its
`scripts/seed.py` (format version 1), Seam 0.4.3 and its six seeds.

## 1. Summary

xolu ships a seed loader, `scripts/seed.py`, that reads `.seed` files: plain text, one JSON
record per line. Seam has six seeds written as Python scripts. The question was whether Seam's
seeds can be expressed in the `.seed` format, and whether the format is sound enough for xolu and
Seam to standardize on it, with xoluman following.

**All six Seam seeds convert, with no differences.** The format carries Seam's data as it is:
integer foreign keys, references nested in arrays, non-Latin text, password hashes. The loader
validates a whole file before it writes anything, needs only the Python standard library, and
loads Seam's largest seed in 0.3 s.

**The format is not yet safe to standardize on.** It has no protection against loading over
existing data, it resets edited records on a re-run, it accepts misspelt keys without a word,
and nothing in a file can be relative to the day it is loaded. Section 5 proposes 24 changes.
Nine are needed before adoption: the seven of section 5.1, P-8 for the dates in Seam's seeds,
and P-22. The rest make the format practical to author and to operate.

| Area | Proposals | Needed before adoption |
|---|---|---|
| Safety of a load | P-1 to P-7 | All seven |
| Authoring a file | P-8 to P-15 | P-8 (dates) for Seam |
| Tools around the format | P-16 to P-21 | None |
| Governance of the format | P-22 to P-24 | P-22 (a specification and tests) |

## 2. What was examined

| Item | Version | What was read or run |
|---|---|---|
| xolu | 0.30.44 | `scripts/seed.py` (791 lines), `scripts/seed.sh`, `seeds/example.seed`, the server's save and tenant-summary handlers, the release script |
| Seam | 0.4.3 | The six seeds in `seeds/`, the two work-order seeds, the entity schemas of seed 01 |
| xoluman | 0.7.32 | Its seed package format and apply engine, for the comparison in section 7 |

The loader was introduced in xolu 0.30.43-pre1_fixes01 (2026-10-11), a release whose changelog
entry says it was written without running the guards. The repository has no test that exercises
`seed.py`, and no document describes the format other than the script's own help text.

## 3. The format today

A `.seed` file is UTF-8 text. Blank lines and lines starting with `#` are ignored. The first
record is the header; every other record has a `kind`.

```
{"seed": 1, "name": "example", "tenant": "acme"}
{"kind": "schema", "entity": "person", "schema": { ... }}
{"kind": "entity", "entity": "person", "id": 7, "data": { ... }}
{"kind": "entity", "entity": "person", "as": "bob", "data": { ... }}
{"kind": "call", "method": "POST", "path": "/api/v1/...", "body": { ... }}
```

| Aspect | How it works today |
|---|---|
| Kinds | 26 typed kinds (configuration, schemas, sequences, state machines, ledgers, calendars, timeseries, locations, edges, metadata and more), plus `entity`, `blob` and a generic `call` |
| Order | Each kind has a phase; records run by phase, then by file order, across all the files loaded together |
| Entities with an `id` | Saved under that id (`POST /{entity}/save/{id}`): an upsert that replaces the record |
| Entities without an `id` | Created (`POST /{entity}`); the new id can be bound to a name with `as` |
| References | `{"$seed": "name"}` anywhere in a value is replaced by a bound value; `${name}` in a `call` path |
| REF values | Entities with ids are ordered so that REF targets exist first; cycles are broken with a PATCH |
| Schemas | Guarded: skipped when already registered, refused when the type has rows |
| Tenant | `tenant` in the header puts every tenant-scoped request under `/tenant/<name>` |
| Options | `--dry-run`, `--list`, `--only`, `--skip`, `--keep-going`, `--lenient`, `--force-schemas`, `--tenant` |
| With no file named | Every `*.seed` file in the seeds directory is loaded, in name order |
| Exit status | 0 success, 1 a record failed, 2 bad input |

## 4. What was observed

### 4.1 Conversion of Seam's seeds

Method: run the Python seed into a xolu instance; write that instance out as a `.seed` file with
explicit ids; load the file into a second, empty instance with `seed.py`; compare the two record
by record, field by field, types included; start Seam over each and compare 19 pages.

| Seed | Records | Entity types | File | Load | Differences | Seam pages identical |
|---|---|---|---|---|---|---|
| 01 Constructec | 164 | 20 | 43 KB | 0.2 s | 0 | 19 / 19 |
| 02 FrigoCold | 227 | 20 | 58 KB | 0.3 s | 0 | 19 / 19 |
| 03 Extinfire | 288 | 20 | 96 KB | 0.3 s | 0 | 19 / 19 |
| 04 VendiBeria | 265 | 20 | 86 KB | 0.3 s | 0 | 19 / 19 |
| 05 Fairborne | 165 | 20 | 43 KB | 0.2 s | 0 | 19 / 19 |
| 06 Asahi Kenki | 165 | 20 | 44 KB | 0.2 s | 0 | 19 / 19 |

The record count of each file equals the row count xolu reports for the instance, and no seed
writes to any store other than entities. Seed 01 with its 20 entity schemas converts (schemas
registered identically, skipped on a second load), and so does seed 01 with the work-order seeds
on top (180 of 180 records). Login works on every loaded instance and Seam logs no error.

The converted files were produced by dumping an instance. This shows the format can carry the
data; it does not show the files are pleasant to write or review by hand (see O-12).

### 4.2 Observations

Each observation has an identifier that the proposals refer to.

| # | Observation | Evidence |
|---|---|---|
| O-1 | A load over existing data is not detected. | Seed 02 loaded over an instance holding seed 01 exits 0, overwrites 159 of 164 records and leaves 229 records of two companies under the second one's name. |
| O-2 | A re-run replaces every record. | A second load rewrote all 164 records (version raised on each). A record edited in between returned to its seed values and lost a field the seed does not have; a deleted record came back; a record added by a user survived. |
| O-3 | Records without an `id` are not repeatable. | A file that uses `as` bindings and no ids duplicated every row on its second run. |
| O-4 | A reference to a record further down the file is found only while loading. | The run stops at that record with exit 1, after the records before it have been written. |
| O-5 | A server-side refusal leaves a partial load. | With a schema registered, the second of three records was refused: the first stays written, the third is never attempted, exit 1. Nothing says how to continue. |
| O-6 | Unknown keys are ignored. | A header with `tennant` instead of `tenant` loaded without a word, into the wrong scope. Misspelt optional keys on a record (`expct`, `phse`) are ignored as well. |
| O-7 | A misspelt entity name creates a new entity type. | `brnads` loaded with exit 0 and now exists beside `brands`. |
| O-8 | The next id xolu hands out after a save with an explicit id skips one. | After a single record saved as id 1, the next ids are 3 and 4, not 2 and 3; after id 100, 102. An instance seeded through `POST /{entity}` continues at max+1. |
| O-9 | Id 0 is accepted. | A record saved as id 0 exists and reads back. |
| O-10 | Nothing in a file can depend on the day it is loaded. | Each Seam seed computes 14 to 20 dates from today (warranties, expiries, services due) and stamps 37 to 70 fields with the load time. A file freezes all of them. |
| O-11 | The tenant of the header is a path scope; Seam's tenancy is a field. | A file with `"tenant": "acme"` loads under `/tenant/acme` and is invisible to an unscoped read, which is how Seam reads. Seam's records carry `tenant_id` as data, fixed at 1 in every seed. |
| O-12 | One record per line gives long lines. | The longest line of each converted seed is between 1,084 and 1,956 characters. |
| O-13 | With no file named, every seed in the directory is loaded together. | For seeds that are alternatives, as Seam's are, this is refused only because their ids collide ("duplicate entity regions/1"); seeds with disjoint ids would be merged. |
| O-14 | A `body_file` may point outside the seed's directory. | `"body_file": "../outside.txt"` is read and sent. A `call` record can reach any `/api` path, administration included. |
| O-15 | A file with a byte-order mark is refused with a parser message. | "Unexpected UTF-8 BOM (decode using utf-8-sig)". Windows line ends are accepted. |
| O-16 | One request per record, one connection per request. | 5,000 small records load in 8.6 s (585 records per second). |
| O-17 | There is no way to produce a `.seed` file from an instance. | The converter used for section 4.1 had to be written for this review. |
| O-18 | There is no way to check an instance against a file. | The comparison used for section 4.1 had to be written as well. |
| O-19 | The loader has no tests and the format has no specification. | No test in xolu runs `seed.py`; the format is described only in its help text; `seeds/example.seed` is not in the release package, although `scripts/` is. |
| O-20 | REF values nested inside an object are not seen by the ordering. | The planner looks at top-level values and at lists. Seam does not use REF values, so this was read in the code, not run. |

What worked and needs no change: validation before writing (an unknown kind, broken JSON or an
entity without data exits 2 with nothing written), duplicate ids across files are refused,
schema registration is guarded, `--dry-run` and `--list`, and the error messages name file and
line.

## 5. Proposals

Each proposal says where the change belongs: **loader** (`seed.py`), **format** (what a file may
say), **server** (xolu itself), or **Seam**.

### 5.1 Safety of a load

**P-1. Refuse to load into a tenant that has data, unless told otherwise.** (loader; O-1)
Before the first write, ask the server whether the tenant is empty (`GET /tenant-summary`, one
request, already in xolu 0.30.44) and stop if it is not. `--allow-existing` turns the check off.
A file whose records are all additions to an existing dataset says so in its header (P-12), and
the check then becomes "the seed it extends is loaded" (P-2).

**P-2. Record what was loaded.** (loader, format; O-1, O-13)
After a successful load, write one provenance record in the tenant: seed name, a digest of the
file, the format version, when, and how many records. With it the loader can tell three
situations apart that look the same today: this seed is already loaded, another seed is loaded,
and data exists that no seed accounts for. It is also what lets a seed declare that it needs
another (P-12). A reserved entity type, for example `seed_loads`, is enough; no server change.

**P-3. Make the effect of a re-run a choice, and make the safe one the default.** (loader; O-2)
Three modes: `create` writes only the records that do not exist and reports the rest as present;
`replace` is today's behavior; `check` writes nothing (P-17). Default `create`. Today's default
undoes a user's edits without saying so, which is the wrong default for a tool that is also the
documented way to continue an interrupted load. The summary should count created, unchanged,
replaced and skipped separately.

**P-4. Reject keys the format does not define.** (loader, format; O-6)
An unknown key in the header or on a record is an error at validation time, before anything is
written. `--lenient` already exists for unknown kinds and can cover unknown keys. Keys reserved
for users can live under one name, for example `"x"`, so that annotations remain possible.

**P-5. Stop a misspelt entity name from creating a type.** (loader, format; O-7)
The header lists the entity types the file may write (`"entities": [...]`), or the loader
requires a registered schema for each (`--require-schemas`). A record for a type outside the
list is refused at validation time. For Seam the list is 20 names per seed.

**P-6. Fix the id sequence after a save with an explicit id, and decide about id 0.** (server; O-8, O-9)
The sequence should continue at max+1, as it does after ordinary creates. Whether id 0 is a
valid id should be decided and enforced by the server; the loader accepts it today because the
server does. Both are for the xolu register.

**P-7. Make a failed load recoverable.** (loader; O-4, O-5)
Three parts. First, everything that can be known without the server is checked before the first
write: a placeholder with no earlier binding is a validation error, not a failure halfway.
Second, when a load fails, the summary says what was written and gives the command that
continues it. Third, when the tenant was empty at the start (P-1), `--undo-on-failure` deletes
the entities the run created. Kinds that xolu cannot delete (schemas, ledger accounts and
transfers, calendars) are listed as left behind; the loader should not claim a rollback it
cannot perform.

### 5.2 Authoring a file

**P-8. Values relative to the day of the load.** (format, loader; O-10)
Two placeholders, resolved once per load: `{"$date": "+90d"}` gives a date, `{"$now": "-2h"}` a
timestamp, both relative to the load, with `d`, `h`, `m` and `y` units. `--as-of 2026-11-17`
fixes the reference day, so that a load can be reproduced exactly and so that tests compare
equal. Without this Seam needs a generator that writes the file each time, and the file stops
being the source. A demo loaded six months after the file was made otherwise shows every
warranty expired and every service overdue.

**P-9. A repeatable way to write a record without choosing its id.** (format, loader; O-3)
A record may name the field or fields that identify it: `"match": ["code"]`. The loader looks
the record up by them; if it exists its id is bound and the record is treated as present (or
replaced, by P-3); if not, it is created. This keeps `as` bindings usable and makes such files
safe to run twice. Explicit ids remain the right choice for a full dataset like Seam's; `match`
is for add-ons that must fit whatever ids an instance already has, such as Seam's work-order
seeds, which today do this lookup in Python.

**P-10. Bindings resolved by a plan, not by file order.** (loader; O-4)
Records without ids are ordered by the bindings they use, as records with ids already are by
their REF values, so a file need not list things in dependency order in either case. A cycle is
reported at validation time.

**P-11. Records that span lines.** (format; O-12)
Allow a record to continue on following lines that start with white space. Every file valid
today stays valid, and a 1,900-character asset can be laid out one field per line, which is
what makes a review or a diff of a seed readable. A formatter (P-18) can then rewrite files in
either style.

**P-12. Seeds that build on other seeds.** (format, loader; O-13)
Two header keys. `"requires": ["01-constructec"]` says the file extends a seed that must be
loaded (checked through P-2). `"include": ["common.seed"]` loads another file's records as part
of this one. Seam has both cases: the work-order data extends any base seed, and seeds 05 and
06 are seed 01 with the visible text translated, today kept as three copies of the same
structure.

**P-13. Loading everything is asked for, not assumed.** (loader; O-13)
With no file named, list the seeds and stop; `--all` loads every file. A header may also say
`"exclusive": true`, meaning the seed is a whole dataset and is never combined with another.

**P-14. Variables.** (format, loader; O-11)
`{"$var": "name"}` is replaced by a value given on the command line (`--set name=value`) or by a
default in the header (`"vars": {"tenant_id": 1}`). This is what lets one file load into a
tenant other than 1 when tenancy is a data field, as in Seam, and it gives deployments a way to
supply values a file should not carry (P-15). A variable with no value and no default is a
validation error.

**P-15. Keep secrets out of the files where a deployment wants that.** (format; with P-14)
Seam's files would carry bcrypt hashes of the demonstration passwords, which is no more than the
Python seeds publish today. For anything else, a hash or a key comes in as a variable. The
loader should not compute bcrypt itself: it is not in the standard library, and the loader's
lack of dependencies is worth keeping.

### 5.3 Tools around the format

**P-16. Dump an instance as a `.seed` file.** (loader; O-17)
`seed.py dump [--entities a,b] [--relative-dates]` writes what a tenant holds as records with
ids, in a stable order, so that a dataset built in the application can become a seed and so that
two dumps can be compared as text. With `--relative-dates` it writes P-8 placeholders for dates
near the day of the dump.

**P-17. Check an instance against a file.** (loader; O-18)
`seed.py check FILE` (the third mode of P-3) reports records missing, different or extra, and
exits non-zero when they differ. This is the assertion a test suite or a release gate needs, and
it is how the conversions in section 4.1 were verified.

**P-18. A formatter.** (loader; O-12, with P-11)
`seed.py fmt` rewrites a file in a canonical layout: records grouped by kind and entity, keys in
a fixed order, one record per line or one field per line.

**P-19. A result a program can read.** (loader)
`--json` prints the summary as one JSON object: counts per kind and outcome, failures with file
and line, the provenance record. Seam's demo packaging and xoluman would both consume it.

**P-20. Reuse the connection, and batch where the server can.** (loader, server; O-16)
The loader opens a connection per request. A persistent connection (`http.client`, still
standard library) removes most of that cost. Beyond it, a batch save endpoint would make large
seeds practical; at 585 records per second a 100,000-record dataset takes about three minutes.
Not needed for Seam's seeds, which load in a third of a second.

**P-21. Small robustness fixes.** (loader; O-14, O-15)
Accept a byte-order mark. Refuse a `body_file` that resolves outside the seed's directory unless
`--allow-outside-files` is given. Add `--safe`, which refuses `call` and `config` records, for
loading a file from a source that is not trusted; and say in the specification that a seed with
`call` records is a program, not data.

### 5.4 Governance of the format

**P-22. A specification and tests.** (xolu; O-19)
Move the format description out of the script's help text into a document in xolu's `docs/`,
with the phases, the placeholders and the re-run rules. Add tests that run `seed.py` against a
real server: the example file, the cycle-breaking, the schema guard, and the cases of section
4.2. Put `seeds/` in the release package. A format that two more projects will depend on cannot
rest on an untested script.

**P-23. Say what changes the format version.** (format)
Most of the proposals are additions a version 1 reader would reject cleanly (new header keys,
new placeholders), given P-4. A file that uses them should declare `"seed": 2`, so that an older
loader refuses it with "unsupported version" instead of misreading it. Section 6 lists which
proposals need the bump.

**P-24. Descriptive header fields.** (format)
`description`, `tags`, a version of the data, and the minimum xolu version the file needs
(`"requires_xolu": "0.30.44"`). These are what a catalog needs to show a seed before loading it,
and they are most of what xoluman's separate manifest holds today (section 7).

## 6. Compatibility

| Proposal | Changes a file's meaning | Needs format version 2 | Changes default behavior of the loader |
|---|---|---|---|
| P-1 emptiness check | No | No | Yes: a load into a tenant with data stops |
| P-2 provenance | No | No | Yes: one more record is written |
| P-3 re-run modes | No | No | Yes: `create` replaces `replace` as the default |
| P-4 unknown keys | No | No | Yes: files with unknown keys stop loading |
| P-5 entity list | New header key | Yes | No |
| P-6 id sequence | No | No | Server fix |
| P-7 recoverable failure | No | No | No (opt-in undo) |
| P-8 dates | New placeholders | Yes | No |
| P-9 `match` | New record key | Yes | No |
| P-10 planned bindings | No | No | No: only accepts more files |
| P-11 multi-line records | New syntax | Yes | No |
| P-12 `requires`, `include` | New header keys | Yes | No |
| P-13 `--all` | New header key (`exclusive`) | Yes for the key | Yes: no file named no longer loads everything |
| P-14, P-15 variables | New placeholder and header key | Yes | No |
| P-16 to P-21 tools | No | No | No |
| P-24 descriptive fields | New header keys | Yes | No |

The four changes of default (P-1, P-3, P-4, P-13) are deliberate: each replaces a default that
loses or mixes data with one that stops and says why.

## 7. What it means for each project

**xolu.** Owns the format and the loader. P-6 is a server defect to file. P-22 comes first: the
rest should land with tests.

**Seam.** Its seeds become six `.seed` files and the Python scripts go, together with the
`requests` and `bcrypt` requirements. The files need P-8 for the dates; until it exists, a small
generator can write the files from the current scripts, which is how the conversions here were
made. `make dev`, the demo packaging script and the seeds catalog call the Python seeds today
and change with them. The work-order seeds fit P-9 and P-12. Seam's records should keep explicit
ids. A suggested order: adopt the files behind the existing commands once P-1, P-3, P-4 and P-8
exist; retire the scripts one release later.

**xoluman.** Its seed package is a directory with a manifest of typed steps, data in JSONL with
symbolic keys resolved to REF values, a manual and preview images, an emptiness check, and a
rollback limited to entities and state machine definitions. Normalizing it means its package
becomes a `.seed` file plus the manual and images, with the descriptive fields of P-24 in the
header; its apply engine gives way to the loader's semantics (P-1, P-3, P-7 cover what its
emptiness check and rollback do); and its browse and preview pages stay as they are, reading
P-19's result. Its `$ref` keys resolve only top-level fields into REF objects, which cannot
express Seam's integer keys or nested references; the `.seed` placeholders can.

## 8. Decisions asked for

| # | Decision | Recommendation |
|---|---|---|
| 1 | Adopt `.seed` as the one seed format for xolu, Seam and xoluman | Yes, conditional on section 5.1 |
| 2 | Change the loader's defaults as in P-1, P-3, P-4 and P-13 | Yes |
| 3 | Dates: placeholders in the format (P-8) or a generator kept in Seam | Placeholders: the file stays the source |
| 4 | Identity of Seam's records: explicit ids, or `match` keys | Explicit ids for the six seeds; `match` for add-ons |
| 5 | Where the loader lives once three projects use it | In xolu, released with it, with the specification and tests of P-22 |
