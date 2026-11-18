# The `.seed` format, version 2

Updated: 2026-11-18

Status: **in use**. This is the specification the loader in `scripts/seed_system/` implements
(`scripts/seed.py` is its command) and the conformance suite in `scripts/seed_conformance/` checks
on a real xolu. It extends version 1, the format xolu's loader read up to 0.30.44; a version 1
file is valid and means the same. The reasons for each addition are in
`docs/SEED_FORMAT_PROPOSAL.md`. Seam AMS carries the same loader and the same specification.

## 1. A file

A `.seed` file is UTF-8 text holding JSON objects, called records. A byte-order mark and CRLF
line ends are accepted.

- A record starts in the first column of a line.
- A record may continue on the lines that follow it, as long as each of them starts with a space
  or a tab (version 2). A version 1 record is one line.
- Blank lines, and lines whose first character is `#`, are not part of any record. A `#` line
  ends the record before it, so a comment cannot sit inside a record.
- The first record is the header. Every other record has a `kind`.

```
# a comment
{"seed": 2, "name": "demo"}
{"kind": "entity", "entity": "brands", "id": 1, "data": {"name": "Caterpillar"}}
{"kind": "entity", "entity": "models", "id": 1, "data": {
  "name": "320 GC",
  "brand_id": 1,
  "warranty_end": {"$date": "+2y"}
  }}
```

A key the format does not define is an error, in the header and on every record. Notes that a
project wants to keep in a file go under the key `x`, which is ignored by the loader. Seam AMS,
for one, keeps two things there under `x.seam` for its own scripts: the accounts a seed creates
and the module an addition holds data for.

## 2. The header

| Key | Version | Meaning |
|---|---|---|
| `seed` | 1 | The format version: `1` or `2`. Required. Any other value is refused. |
| `name` | 1 | The seed's name. It is what a load is recorded under (section 7). Default: the file name without `.seed`. |
| `tenant` | 1 | The tenant every tenant-scoped record goes to, as a path scope (`/tenant/<name>`). Without it the load is not scoped. `--tenant` overrides it. |
| `description` | 2 | One line saying what the seed is. |
| `tags` | 2 | A list of words, for catalogs. |
| `version` | 2 | The version of the data, a string. |
| `requires_xolu` | 2 | The lowest xolu version the file needs, a string. Informative: the loader does not check it. |
| `entities` | 2 | The entity types the file may write. With it, an `entity` or `schema` record for any other type is an error. |
| `vars` | 2 | Default values for variables (section 5). |
| `exclusive` | 2 | `true` says the seed is a whole dataset. Two whole datasets are never loaded together; a whole dataset may be loaded together with files that are not, such as its schemas. |
| `addition` | 2 | `true` says the seed adds to whatever the tenant holds, so the guard on a tenant that is not empty does not apply to it (section 6). Its records are written to be found, not placed: see `match` and `find` in section 3.1. |
| `requires` | 2 | A list of seed names. The load is refused unless each is loaded in the tenant already, or is named in the same load. A file with `requires` is an addition. |
| `notes` | 2 | Text printed after a successful load, such as the accounts the seed creates. |
| `x` | 2 | Anything else. Not read by the loader. |

A file that uses anything marked version 2, in the header or in a record, must say `"seed": 2`.

## 3. Records

Every record has `kind`. These keys are allowed on any record:

| Key | Meaning |
|---|---|
| `as` | Bind a value from the response to this name (section 5). |
| `bind` | The dotted path of the value to bind, in the response. Default `id`. |
| `phase` | Run the record in this phase instead of its kind's (section 4). |
| `expect` | A list of HTTP statuses to treat as success, beyond 2xx. |
| `x` | Anything else. Not read. |

### 3.1 `entity`

`{"kind": "entity", "entity": NAME, "id": N, "data": {...}}`

With an `id`, the record is saved under that id, and the id survives the load. Without one it is
created and xolu assigns the id, which `as` can bind. `id` and `_version` inside `data` are
dropped: the server manages them. `id` is a non-negative integer.

A record with an `id` can be loaded again safely (section 6). A record without one is created
once: the load record (section 7) remembers it and the value it bound, and a second load of the
same seed does not send it again.

**`match`.** `{"kind": "entity", "entity": NAME, "match": ["code"], "data": {...}}` is a record
with no id that is found by fields of its own. The loader asks the tenant for a record of that
type whose listed fields have the values in `data`. If there is one, the record is present: it
is left as it is (or written again with `--replace`) and `as` binds its id. If there is none it
is created. The fields are text or whole numbers and may be bindings. A record has an `id` or a
`match`, not both. This is what lets a file be loaded over data whose ids it cannot know.

**`find`.** `{"kind": "entity", "entity": NAME, "find": {"email": "a@b.c"}, "as": "who"}` is a
lookup. Nothing is written: the id of the one record with those values is bound to the name. No
record, or more than one, stops the load. It takes no `id`, `data` or `match`, it needs `as`,
and its entity type need not be in the header's `entities`. An addition uses it for the records
of the seed it requires.

A value of the form `{"type": "REF", "entity": E, "id": N}` is a reference. Records with ids are
written so that the records they refer to exist first. Where references form a cycle, the
records of the cycle are saved without those fields, which are then added with a PATCH.

### 3.2 `schema`

`{"kind": "schema", "entity": NAME, "schema": {...}}` registers an entity schema. Schemas are not
tenant-scoped. A schema that is already registered is left alone. A schema for a type that
already holds rows is refused, because registering it would hide them; `--force-schemas` turns
both checks off.

### 3.3 `call`

`{"kind": "call", "method": M, "path": "/api/v1/...", ...}` sends one request, for anything
without a kind of its own. The path starts with `/api/v1` or `/api/v2`; `"scoped": true` puts
the tenant scope after that. Other keys: `query`, `headers`, `content_type`, and one of `body`
(JSON), `body_file` (a path relative to the file's directory, and inside it), `body_b64`,
`body_text`. In a path, `${name}` is replaced by a bound value.

A file with `call` or `config` records can reach any route of the server. Such a file is a
program, not data: `--safe` refuses both kinds, for a file from a source that is not trusted.

### 3.4 `blob`

`{"kind": "blob", "key": K, "content_type": T, "body_file": F}` uploads a blob; one of
`body_file`, `body_b64`, `body_text`.

### 3.5 The other kinds

Each sends the record's body to one route of xolu. `body` is the JSON the route accepts.

| Kind | Fields | Route |
|---|---|---|
| `config` | `namespace`, `key`, `body` | `PUT /api/v1/admin/config/{namespace}/{key}` |
| `seq` | `body` | `POST /api/v2/seq` |
| `gen` | `type`, `body` | `POST /api/v2/gen/{type}` |
| `bal_account` | `body` | `POST /api/v2/bal/def` |
| `bal_transfer` | `body` | `POST /api/v2/bal/transfer` |
| `bal_close` | `body` | `POST /api/v2/bal/close` |
| `loc_pattern` | `body` | `POST /api/v2/loc/patterns/def` |
| `loc` | `body` | `POST /api/v2/loc/def` |
| `loc_fence` | `body` | `POST /api/v2/loc/fences/attach` |
| `obj` | `body` | `POST /api/v2/obj/attach` |
| `fsm_def` | `body` | `POST /api/v2/fsm/def` |
| `fsm_machine` | `body` | `POST /api/v2/fsm/machine` |
| `fsm_walk` | `machine`, `input`, `payload` | `POST /api/v2/fsm/machine/{machine}/walk` |
| `dxp_def` | `body` | `POST /api/v2/dxp/def` |
| `dxp_txn` | `body` | `POST /api/v2/dxp/txn` |
| `cal_calendar` | `body` | `POST /api/v2/cal/calendars` |
| `cal_propose` | `body` | `POST /api/v2/cal/propose` |
| `cal_confirm` | `body` | `POST /api/v2/cal/confirm` |
| `ts_provision` | `body` | `POST /api/v2/ts/provision` |
| `ts_timeline` | `body` | `POST /api/v2/ts/tl/def` |
| `ts_event` | `body` | `POST /api/v2/ts/events` |
| `ts_batch` | `body` | `POST /api/v2/ts/events/batch` |
| `edge` | `from`, `to`, `rel`, `props` | `POST /api/v1/graph/edges` |
| `event_def` | `body` | `POST /api/v2/event/def` |
| `meta` | `entity`, `id`, `key`, `value`, `expires_at` | `PUT /api/v2/meta/{entity}/{id}/{key}` |

For `seq`, `gen`, `bal_account`, `loc_pattern`, `loc`, `cal_calendar`, `ts_provision` and
`ts_timeline`, an answer of 409 means the thing exists and counts as present.

An `fsm_def` record may say `"match": ["name"]`: a definition with that name that already
exists is present and `as` binds its id, so a seed and an application that makes the same
definition on first use end with one.

The routes under `/api/v2` exist only when xolu runs with `XOLU_API_V2_ENABLED=true`; without
it every one answers 404, and the loader says so in the failure.

The same record written twice in a file is two records, sent twice: two equal `fsm_walk`
records are two steps.

## 4. Order

Records do not run in file order. Each kind has a phase; records run by phase, lowest first, and
within a phase in file order, across all the files of one load that are not additions. Each
addition (section 2) then runs as a pass of its own, by phase, in the order the files were
named: `load dataset issues accounting` loads the dataset, then one addition, then the other,
so what an addition looks up with `find` is there.

| Phase | Kinds |
|---|---|
| 5 | `entity` with `find` |
| 10 | `config` |
| 20 | `schema` |
| 30 | `seq`, `gen`, `bal_account`, `loc_pattern`, `fsm_def`, `dxp_def`, `ts_provision` |
| 35 | `loc`, `ts_timeline` |
| 39 | `entity` without an id |
| 40 | `entity` with an id |
| 41 | the PATCH that closes a cycle of references |
| 45 | `cal_calendar` |
| 46 | `fsm_machine` |
| 47 | `fsm_walk` |
| 48 | `ts_event`, `ts_batch` |
| 50 | `edge` |
| 55 | `event_def` |
| 60 | `meta`, `blob` |
| 70 | `dxp_txn`, `obj`, `loc_fence` |
| 75 | `call` |
| 80 | `bal_transfer`, `cal_propose` |
| 81 | `cal_confirm` |
| 90 | `bal_close` |

Event definitions come after the data so that loading a seed does not fire them.

`phase` on a record overrides its kind's. A file whose entities carry the id of a state machine
puts its `fsm_machine` and `fsm_walk` records before them, in phases 36 and 37.

## 5. Placeholders

A placeholder is a JSON object with exactly one key, one of those below. It may appear anywhere
inside a record's values, at any depth, and is replaced by a value before the record is sent.

| Placeholder | Version | Becomes |
|---|---|---|
| `{"$seed": "name"}` | 1 | The value an earlier record bound to `name` with `as`. |
| `{"$date": OFFSET}` | 2 | A day, `YYYY-MM-DD`, relative to the reference time. |
| `{"$date": "OFFSET" + "THH:MM:SSZ"}` | 2 | That day with the time of day given: `{"$date": "-2dT08:30:00Z"}`. |
| `{"$now": OFFSET}` | 2 | A time, `YYYY-MM-DDTHH:MM:SSZ`, relative to the reference time. |
| `{"$var": "name"}` | 2 | The variable's value: from `--set name=value`, else from the header's `vars`. No value is an error. |
| `{"$text": "..."}` | 2 | The string, with `${date:OFFSET}`, `${now:OFFSET}`, `${var:name}` and `${seed:name}` inside it replaced. `${seed:name}` is a bound value written as text, for a field that holds an id inside a string. |

An offset is the empty string, meaning the reference time itself, or a sign, a whole number and
a unit: `s`, `m` (minutes), `h`, `d`, `w`, `mo` (months) or `y`. `"+90d"`, `"-2h"`, `"+1mo"`.
Months and years move the calendar month and keep the day, shortened to the month's last day
where it does not exist: one month after January 31 is February 28.

**The reference time** is one moment for the whole load, in UTC. It is, in this order: the one
given with `--as-of` (a day, taken at 00:00:00, or a time); the one recorded when this seed was
first loaded into the tenant, so that a load that is continued or checked uses the same dates;
otherwise the time the load starts.

A name bound with `as` must be bound by a record that runs before the one that uses it (section
4). A use with no earlier binding is an error found before anything is written.

## 6. Loading

Before the first write the loader reads the whole file, checks everything it can check without
the server, and asks the server what the tenant holds. Then:

| The tenant | What happens |
|---|---|
| Is empty | The seed is loaded. |
| Holds this same seed (section 7) | The load continues: see the modes below. |
| Holds another seed, or data no load accounts for | Refused, exit 3, nothing written. `--allow-existing` loads anyway. |
| Cannot be asked | Refused, exit 3. `--allow-existing` loads anyway. |

An addition (`addition` or `requires` in the header) is loaded whatever the tenant holds. What
is checked for it is `requires`: a seed it names that is neither loaded nor part of the same
load refuses it, exit 3.

Modes, when the tenant already holds the seed:

| Mode | A record with an id, or found by `match`, that exists | One that does not |
|---|---|---|
| `create` (default) | Left as it is, counted as present | Created |
| `replace` (`--replace`) | Written again, counted as replaced | Created |

A record with neither an id nor a `match`, and every record of the other kinds, is sent once:
later loads of the same seed count it as present, in both modes.

The default leaves alone what users changed since the load, and is how an interrupted load is
continued: run the same command again. A record deleted since the load is created again in
either mode. A record a user added is never touched.

**Failure.** A record the server refuses stops the load, unless `--keep-going`. What was written
stays, and the load is recorded as failed; running the command again continues it. With
`--undo-on-failure` the entities the run created are deleted; records that were there before
it, found by id or by `match`, are not touched. Things that xolu's API cannot delete (schemas,
ledger accounts, calendars, state machines, anything made by a `call`) are reported as left
behind.

**Outcome of each record:** created, present, replaced, skipped or failed. The summary counts
them per kind; `--json` prints it as one JSON object.

| Exit status | Meaning |
|---|---|
| 0 | Loaded, or `check` found no difference |
| 1 | A record failed, or `check` found differences |
| 2 | Bad input: the file, a key, a placeholder, the command line. Nothing was written. |
| 3 | A guard refused the load. Nothing was written. |

## 7. What a load records

Each load writes one record of the entity type `seed_loads` in the tenant, before its first
data record, and updates it at the end:

| Field | Meaning |
|---|---|
| `seed` | The seed's name |
| `digest` | SHA-256 of the file |
| `format` | The file's format version |
| `loader` | The loader and its version (`seed_system 1.0.0`, `xoseed 1.0.0`) |
| `as_of` | The reference time of the load |
| `status` | `loading`, `loaded` or `failed` |
| `mode` | `create` or `replace` |
| `partial` | `true` when the load was limited with `--only` or `--skip` |
| `started_at`, `finished_at` | When |
| `records` | The count of each outcome, per kind |
| `done` | The records that are sent once (section 6) and were sent: a digest of each, with the value it bound |

This record is what tells "this seed is here" from "something else is here", and what gives a
continued load and a later `check` the same reference time. A tenant that holds only this
record is not empty.

## 8. Checking and dumping

`check` compares the tenant with a file and writes nothing. It looks at entity records that have
an id and reports those missing, those different (with the fields), and the ids of the same
entity types that the file does not have. References are compared as they are stored, not
expanded. A record with `match` is found the same way a load finds it and compared; a `find`
that finds nothing is reported as missing. A field whose value is a binding is not compared, and
a record whose `match` uses one is counted as not compared.

`dump` writes what a tenant holds as a version 2 file with explicit ids, entity types in name
order and ids in ascending order, laid out as in section 1. The `seed_loads` type is left out.

## 9. Command line

```
seed.py load  SEED... [--url U] [--tenant T] [--as-of WHEN] [--set NAME=VALUE]
                      [--replace] [--allow-existing] [--undo-on-failure] [--keep-going]
                      [--only KINDS] [--skip KINDS] [--safe] [--force-schemas]
                      [--dry-run] [--json] [-v]
seed.py load  --all
seed.py check SEED... [--url U] [--as-of WHEN] [--json]
seed.py dump  [--url U] [--tenant T] [--entities A,B] [--name N] [-o FILE]
seed.py list
seed.py info  SEED... [--json]
```

`seed.py COMMAND --help` lists every option. A `SEED` is a path, or a name looked up in the seeds directory: `demo` finds `demo.seed` or
`seed-demo.seed` there, or `seed-demo.seed` in one of its subdirectories, where a project can
keep its additions. `load` with no seed lists the seeds and stops; `--all` loads every file in the
directory itself, not those of its subdirectories; `list` shows both. The address is `--url`, else `XOLU_BASE_URL`, else `XOLU_HOST`, `XOLU_PORT` or
`XOLU_ADDR`, else `http://127.0.0.1:9090`. `XOLU_API_KEY` and `XOLU_TOKEN` are sent when set.

`xoseed` (`cmd/xoseed`) is the same command in Go, with the same commands, options, output and exit
statuses: `xoseed load demo` for `seed.py load demo`. Its seeds directory is `--seeds-dir`, else
`XOLU_SEEDS_DIR`, else `seeds` under the current directory. It adds `xoseed plan SEED...`, which
prints every step of the plan as JSON and contacts nothing. A program that wants the format
without the command imports `github.com/ha1tch/xolu/pkg/seed`: reading a file, building a plan,
loading, checking and dumping are functions of that package, over `pkg/client`.

## 10. Conformance

`scripts/seed_conformance/` holds cases: seed files, the steps to run, and the state the tenant
must end in. `run.py` starts a fresh xolu for each case, runs the steps with any loader that takes
the command line of section 9, and compares. An implementation conforms when every case passes.
The 22 cases here cover sections 1 to 8; Seam AMS runs the same 22 and adds cases of its own that
pin its datasets.

```bash
make test-seeds                                   # the tests that need no server
make test-seeds-live                              # the conformance suite, on the xolu this tree builds
XOLU_BIN=/path/to/xolu python3 scripts/seed_conformance/run.py --loader "xoseed"
make test-xoseed                                  # the same suite through xoseed, and the cross-check below
```

There are two implementations, the Python package `scripts/seed_system` and the Go package
`pkg/seed` with its command `xoseed`, and both pass the 22 cases. They are also held to each other:
`scripts/seed_conformance/crosscheck.py` plans every `.seed` file it is pointed at with both and
compares each step in full (method, path, body with the order of its keys, headers, phase, and the
key that identifies the record across loads), and where one refuses a file the other must refuse it
in the same words. Because the keys and the load record of section 7 are the same in both, a load
started by one is continued by the other.

## 11. Differences from the loader of xolu 0.30.44 and earlier

`scripts/seed.py` read version 1 only until 0.30.44. From 0.30.45 it is the loader of this
specification, and `scripts/seed.sh` keeps its old command line over it.

| | The version 1 loader | This specification |
|---|---|---|
| A tenant that holds data | Loaded over | Refused unless `--allow-existing` |
| Loading again | Every record replaced | Existing records left alone; `--replace` for the old behavior |
| Unknown keys | Ignored | An error |
| A binding used before it exists | Fails during the load | An error before the load |
| No seed named | Every file in the directory is loaded | Lists the seeds; `--all` to load them |
| A `body_file` outside the directory | Read | Refused unless `--allow-outside-files` |
| What was loaded | Not recorded | Recorded in `seed_loads` |
| A record with no id, loaded again | Created again | Sent once; `match` to find it by its fields |
| Version 2 additions | — | Records over several lines, `$date`, `$now`, `$var`, `$text`, `match`, `find`, the header keys of section 2 |

Not changed, and still open in the server (XOT256): after a save with an explicit id the next id
xolu assigns skips one, and id 0 is accepted.
