# Blob Conditional Requests, Ranges, and PATCH — Wire Contract

Updated: 2026-07-22
Status: design — new documentation; describes nothing that is implemented
Companions: `BLOB_EXTENDED_ROLES.md` (W-2 finding, role 2),
`BLOB_MANIFEST.md`, `BLOB_BACKUP.md`

This file is the normative wire contract for W-2 (conditional requests
and ranges on the existing v1 blob surface) and the role-2 PATCH
operation, plus the CAS mechanics they share. Everything here is a
non-breaking addition: requests carrying none of the new headers behave
exactly as today.

---

## 1. ETag semantics

The blob `ETag` is the **strong** validator `"<sha256hex>"` — the
quoted lowercase hex SHA-256 of the content, exactly as `GET` and
`HEAD` already emit. Strong is correct: equal ETags mean byte-identical
content by construction. Weak validators (`W/…`) are never emitted and
never match anything on this surface.

Comparison is bytewise on the unquoted digest. `*` matches any current
representation (i.e. the key exists). The list form
(`If-Match: "<a>", "<b>"`) is accepted per RFC 9110 and matches if any
member matches. A syntactically malformed ETag in a precondition
(wrong length, non-hex) fails validation as a non-match — it is not an
error in itself.

Surface note: the HTTP listener's validator is the SHA-256 ETag; the
S3 listener advertises the MD5-derived ETag S3 clients expect, so S3
preconditions compare against the current content's MD5, resolved via
the `.md5` sidecar the store already writes. Each listener compares
against the validator it advertises; neither accepts the other's.

## 2. Precondition matrix

| Verb | `If-Match: "<sha>"` | `If-Match: *` | `If-None-Match: "<sha>"` | `If-None-Match: *` |
|---|---|---|---|---|
| `GET` / `HEAD` | 200/206 if current = sha, else **412** | 200 if key exists, else 404 | **304** if current = sha, else 200 | 304 if key exists, else 404→200 path (i.e. 304 when it exists) |
| `POST /blob?key=…` (aliased put) | proceed if current = sha, else **412** | proceed if key exists, else **412** | proceed if current ≠ sha, else **412** | **create-only**: proceed if key absent, else **412** |
| `DELETE /blob/{key}` | proceed if current = sha, else **412** | proceed if key exists (existing 404 covers absent) | — (rejected 400) | — (rejected 400) |
| `PATCH /blob/{key}` | **required** — see §5 | rejected 400 (a blind patch is a bug) | rejected 400 | rejected 400 |

Rules:

- **412 responses carry the current `ETag`** (when the key exists) so a
  failed CAS is also a re-sync: the client learns the value to retry
  against in one round trip. Body is the standard error envelope with
  `XOLU-BL007`.
- Content-addressed `POST` (no key) ignores preconditions: there is no
  alias to guard; the operation is idempotent by identity.
- Absent preconditions preserve today's behaviour byte-for-byte
  (unconditional overwrite, unconditional delete).
- 304 responses carry `ETag`, `X-Blob-SHA256`, `X-Blob-Size` and no
  body, per RFC 9110.

## 3. CAS mechanics (server side)

Honouring write preconditions requires the alias update to be an actual
compare-and-swap; today `Put` overwrites unconditionally and a naive
read-compare-write has a window. The mechanism:

- The per-tenant `Store` gains a **striped per-key mutex** (fixed-size
  lock table indexed by key hash — bounded memory, no per-key
  allocation churn).
- **Content I/O happens outside the lock.** A conditional `Put`/`PATCH`
  streams, hashes and stores its content (temp file → content path)
  first; only then does it take the key's lock.
- **Under the lock, microseconds only:** `resolveKey` → compare against
  the precondition → `atomicWrite` the alias (or `os.Remove` for
  DELETE) → release. The compare that decides the outcome is this one,
  under the lock — any earlier read is advisory.
- **All alias mutations for a key take the lock**, including
  unconditional ones. Otherwise an unconditional write interleaves
  between a conditional operation's compare and swap and the guarantee
  is fiction. Reads never lock (an alias is one atomic file).
- A conditional write that fails its compare has already stored its
  content; the content is unreferenced and ages out through normal GC.
  No cleanup path is needed — the quarantine is the cleanup path.

Scope note: this is single-instance correctness. The multi-instance
form of alias CAS is fleet/federation territory
(`BLOB_EXTENDED_ROLES.md` §7) and is not promised here.

## 4. Range requests

`GET` supports **single byte ranges** (RFC 9110 grammar):

- `Range: bytes=a-b`, `bytes=a-`, `bytes=-n` → **206 Partial Content**
  with `Content-Range: bytes a-b/size` and `Accept-Ranges: bytes`.
- Multiple ranges (`bytes=a-b,c-d`) are **ignored** — the response is
  200 with the full body (explicitly permitted by RFC 9110; multipart
  ranges buy nothing here and cost a multipart writer).
- Unsatisfiable range (start ≥ size) → **416** with
  `Content-Range: bytes */size` and `XOLU-BL008`.
- `If-Range: "<sha>"` — if it matches the current ETag, serve 206 for
  the range; otherwise serve 200 with the full current body. The
  HTTP-date form of `If-Range` is treated as a mismatch (full body):
  mtimes are not validators on this surface.
- `HEAD` ignores `Range`. `Accept-Ranges: bytes` is advertised on
  `GET`/`HEAD` responses.

Implementation note: content files are plain files; range service is
`http.ServeContent`-shaped (seek + copy), no new storage capability.

## 5. PATCH

`PATCH /api/v1/blob/{key}` performs a **copy-on-write edit**: the
content file is never mutated; the result is new content under a new
SHA, and the alias is re-pointed under CAS. Contract:

- **`If-Match` is mandatory.** A `PATCH` without it is refused **428
  Precondition Required** (`XOLU-BL009`). A blind read-modify-write is
  precisely the lost update this surface exists to prevent.
- Body is `application/octet-stream` (the bytes to write). Two modes:
  - **Append** — no `Content-Range` header: body is appended to the
    current content.
  - **Positioned write** — `Content-Range: bytes start-end/*`: body
    overwrites `[start, end]` (end − start + 1 must equal the body
    length, else 400 `XOLU-BL010`). `start` may be at most the current
    size (writing at `start = size` is an append; `start > size` —
    a sparse gap — is refused 400: holes are not a supported
    representation). The result may extend beyond the current size.
- **Not supported, deliberately:** truncation (replace via `POST` with
  full content instead), sparse writes, multi-range patches, and any
  in-place mutation semantics.
- Mechanics: stream old content and patch into a new temp file, hash,
  store content-addressed; take the key lock; re-verify `If-Match`
  against the live alias (**the under-lock compare is the one that
  counts** — the content was built optimistically); swap; release.
  On compare failure: 412 + current ETag; the built content is
  unreferenced and GC-reclaimed.
- Response: 200 with the `POST`-shaped JSON (`key`, new `sha256`,
  `md5`, `size`, `created: false`) and the new `ETag`.
- **Size and quota:** the *result* size is checked against
  `XOLU_BLOB_MAX_SIZE` (append: `old + len`; positioned:
  `max(old, start + len)`) — refused 413 `XOLU-BL003` before any
  content write. The soft quota check applies exactly as on `POST`
  (`XOLU-BL006`). Cost is O(result size) per patch by design; if
  measurement ever shows this hurting, the chunked representation is
  the answer (`BLOB_EXTENDED_ROLES.md` §6), not in-place writes.

## 6. S3 surface parity

The S3 listener adopts the same semantics in S3 dress:

| S3 operation | Support added |
|---|---|
| `GetObject` | `Range` (single range, 206/416), `If-Match`/`If-None-Match` (412/304), `If-Range` |
| `HeadObject` | `If-Match`/`If-None-Match` |
| `PutObject` | `If-Match: "<etag>"` and `If-None-Match: *` (conditional writes; 412 `PreconditionFailed`) |
| `DeleteObject` | `If-Match` (412 on mismatch) |

Errors render as S3-format XML (`PreconditionFailed`,
`InvalidRange`), consistent with the listener's existing error style.
The S3 ETag remains the MD5-derived form S3 clients expect where it is
already emitted; conditional comparison on the S3 surface uses the
ETag that surface advertises. (One surface, one validator — the two
listeners each stay internally consistent.)

## 7. Relation to dxp (recorded, not designed)

The dxp proposal assigns blob the participant mechanism **"lease +
staged write"** with commit action **"promote"**, and its reservation
convention is deadline-bounded SQL rows flipped by CAS. This contract
is the substrate that arc lands on, with a pleasing division of
labour:

- **Staged write is free.** Content-addressed content is invisible
  until aliased and GC-reclaimed if abandoned — writing content *is*
  staging, no tentative state needed.
- **Promote is the conditional alias swap** — `If-Match` (expected
  prior state) or `If-None-Match: *` (create-only) — i.e. §2–§3 of
  this document, unchanged.
- **The lease** (reserving a key for the transaction window against
  competing promotes) is dxp-side state per its own tentative-row
  convention, out of scope here; when the "blob mutation arc" is
  designed, it composes a lease table with this CAS rather than
  replacing it.

## 8. Error codes

Continuing the existing `XOLU-BL001…006` family:

| Code | HTTP | Condition |
|---|---|---|
| `XOLU-BL007` | 412 | Precondition failed (`If-Match`/`If-None-Match`); response carries current `ETag` when the key exists |
| `XOLU-BL008` | 416 | Range not satisfiable; `Content-Range: bytes */size` |
| `XOLU-BL009` | 428 | `PATCH` without `If-Match` |
| `XOLU-BL010` | 400 | Malformed patch (`Content-Range` grammar, length mismatch, sparse gap, unsupported precondition combination per §2) |

## 9. Compatibility statement

Every behaviour in this document is additive. Requests without
conditional headers, without `Range`, and without `PATCH` are
byte-identical to 0.16.19 behaviour, on both listeners. The only
observable change for existing clients is the appearance of
`Accept-Ranges: bytes` on `GET`/`HEAD` responses.
