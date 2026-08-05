# xolu — Resolution Record

Append-only record of closed register items and resolved issues, newest first.
Each entry preserves the item's full text as at closure, stamped with its
closing version and date.

## [0.28.0] XOT167 — New guard-syntax warning: a double-quoted string in an FSM guard or set clause now surfaces in analysis.warnings instead of silently doing nothing -- reported by the Seam AMS team; original AST-walking plan confirmed not to work (the quote distinction doesn't survive tokenizing), pivoted to a verified raw-text scan; auto-conversion considered and rejected (a legitimate use existed until XOT166 closed it, and a silent rewrite has real costs regardless) (v0.28.0, 2026-08-05)

Theme: server · closed 0.28.0 · 2026-08-05


- **Trigger:** Seam AMS's own report, followed all the way through to delivery -- "still deciding the right scope" from an earlier response, resolved when Horacio's own "clarify or deliver" made clear a decision, not another hedge, was owed. A guard written `payload.technician_signature != ""` parses without error and silently compares against nothing, forever, since xolu's guard language is T-SQL: `'...'` is a string literal, `"..."` is a quoted identifier reference -- the opposite of JSON/JavaScript convention, and exactly what cost Seam real debugging time before landing on the fix (single quotes).
- **The original plan (detect this at the parsed-AST level) turned out not to work, checked directly rather than assumed:** confirmed empirically that a double-quoted token and a bare identifier lex to the identical token type in this parser -- the quote-style distinction does not survive tokenizing, so there is no way to ask a parsed guard's own AST "was this originally double-quoted." Pivoted to a raw-text, quote-aware character scan instead (`hasSuspiciousDoubleQuote`, `pkg/server/v2_fsm_common.go`) -- verified correct against T-SQL's own escaped-single-quote convention (`'it''s fine'`) before trusting it, since a naive character-toggle could plausibly have mishandled that case (it doesn't: two toggles from the escaped pair cancel out).
- **Auto-converting instead of warning was considered and rejected** -- see XOT166's own record for why (a legitimate use for double-quoted identifiers existed until that item closed it) and confirmed even after XOT166 that a silent rewrite changes a stored definition without any signal to the person who wrote it, a real cost regardless of whether the case being rewritten was ever legitimate.
- **Fix:** every guard and set-clause expression is now scanned for a double-quote character outside any single-quoted string, during the same parse-validation pass `validateDefinition` already ran (no new pass added). A match appends a warning to `analysis.warnings` -- the same field structural warnings (unreachable states, etc.) already surface through -- naming the transition, the offending expression, and the fix. Never rejects the definition; a false positive costs nothing but a look, while a hard rejection on a case this scan can't perfectly disambiguate would be the wrong failure mode.
- **Verification:** direct end-to-end test against a real server with Seam's own exact original guard text (confirmed the warning fires) and its corrected single-quoted form (confirmed it doesn't), plus a set-clause variant. Three new permanent Go tests for the positive cases, two for the negative, all five proven to genuinely discriminate -- the three positive-case tests fail when the check is disabled, the two negative-case tests pass regardless since they test unrelated behaviour. Full `pkg/server` suite green, no regressions.

Cross-ref: CHANGELOG 0.28.0.

## [0.28.0] XOT166 — New XOLU-FSM014: FSM transition payload keys must now be strict identifiers (letters/digits/underscores, leading letter), matching entity schema field naming -- Horacio's own decision that key names with spaces are illegal in xolu, closing the one legitimate reason a double-quoted identifier reference could ever have been needed in a guard (v0.28.0, 2026-08-05)

Theme: server · closed 0.28.0 · 2026-08-05


- **Trigger:** while confirming Seam AMS's own guard-syntax report couldn't be fixed by auto-converting double quotes to single quotes (a real risk raised and checked directly, not dismissed): payload."odd key" turned out to be the only syntax able to reference a transition payload field whose name wasn't a bare identifier, since payload.<field> access resolves whatever text an identifier token carries regardless of whether it came from a bare word or a double-quoted one -- confirmed directly by reading the evaluator's own resolution code, not assumed. Auto-converting would have silently broken that legitimate case.
- **Closed by policy, not by narrowing the fix:** Horacio's own decision, stated directly -- key names with spaces (and by the same rule, anything else outside strict-identifier characters) are illegal in xolu, full stop. Confirmed first that entity schema fields were already constrained this way (`validateSchemaFieldNames`, `qs.IsValidStrictIdentifier`); the transition payload -- caller-supplied JSON, decoded straight into an unconstrained `map[string]interface{}` -- was the one boundary letting them through.
- **Fix:** new `validatePayloadKeys` (`pkg/server/v2_fsm_walk.go`), called immediately after decoding a transition's request body, before the query pre-fetch or the walk transaction opens. Rejects with the new `XOLU-FSM014` (422) if any top-level payload key fails `qs.IsValidStrictIdentifier` -- the same rule already applied to entity schema fields, applied consistently rather than inventing a second one. Only top-level keys checked: guard/set expressions only ever access `payload.<single-field>` (the evaluator's own `QualifiedIdentifier` handling resolves a two-part dotted name, nothing deeper), so a nested object's own keys are opaque data as far as a guard is concerned.
- **Verification:** direct end-to-end test against a real running server (a space-containing key correctly rejected, a validly-named key correctly still works), plus two new permanent Go tests, both proven to genuinely fail against the check disabled, not just pass with it enabled. Full `pkg/server` suite green, no regressions in any existing FSM/walk test. Documented in `docs/API_V2.md`'s own error-code table.

Cross-ref: CHANGELOG 0.28.0.

## [0.27.4] XOT165 — guards.py's multi-date 'Last exercised' parsing (T-163's own record falsely claimed this was already correct in v0.27.2) had a real bug -- two wrong fix attempts caught before a third, correct one (max() over the dates found, not a positional index, since this file carries hand-written multi-date bullets that don't follow cmd_record's own append order) (v0.27.4, 2026-08-04)

Theme: tooling · closed 0.27.4 · 2026-08-04


- **Trigger:** while preparing a clean patch of the guards.py multi-date fix to contribute to upstream repoman (per Horacio's own request, "so the other teams are in sync"), a direct empirical test of the fix against a realistic multi-date bullet returned the WRONG date -- exposing that T-163's own closed record (RESOLVED.md) made a false claim: "xolu's own guards.py is already ahead of upstream on a related fix, not behind." It was not ahead. It had a real bug, shipped in v0.27.2, never actually exercised against realistic input before that release.
- **Corrected here, honestly, not silently:** T-163's own text is left as-is in RESOLVED.md (append-only, historical record, never rewritten) -- this item is the correction, not a retroactive edit of that one.
- **The actual bug, in two wrong attempts before the right one, in order:**
  1. The version shipped in v0.27.2 used `dates[-1]` (last date in the bullet's own text) on the strength of a comment claiming that was "the most recent" -- never empirically checked against how dates actually get appended. Wrong: `cmd_record`'s own append order puts the NEW date first and the previous recording after it as "Previous: ..." -- `dates[-1]` reads the OLDEST date in a two-entry bullet as current.
  2. Fixing that by switching to `dates[0]` (checked directly against `cmd_record`'s own code this time, confirmed correct for its structured output) still produced a wrong answer against the REAL register: `docs/KNOWN_ISSUES.md` carries many hand-written, free-form "Last exercised" bullets (G-13's own multi-sentence narrative, among others) where dates appear in whatever order the prose happened to put them, not `cmd_record`'s own convention. `dates[0]` is wrong for those.
  3. The actual, robust fix: `max(dates)`, not a positional index at all. ISO-8601 "YYYY-MM-DD" strings sort lexicographically the same as chronologically, so position in the text never matters -- correct for `cmd_record`'s own structured output AND for hand-written bullets in any order.
- **Verified this time against the real file, not just a synthetic test:** `guards.py stale` against the actual `docs/KNOWN_ISSUES.md` now reports G-13 as `last=2026-08-03`, matching this session's own earlier, independently-observed value from before any of this touched the file -- the first two (wrong) attempts both silently changed this date without that discrepancy being checked at the time.
- **Also fixed in the upstream repoman clone**, prepared as a clean, isolated patch for Horacio to review and push -- separate from xolu's own scripts/ adaptations, since this fix has no xolu-specific dependency and is a genuine improvement to the generic tool itself.

Cross-ref: CHANGELOG 0.27.4.

## [0.27.3] XOT164 — Forward-only per-project id prefix (XOT for xolu, matching xoluman=XMT, seam=SET), directly motivated by a real cross-project id collision found while syncing repoman upstream (T-163) -- register.py and wave_progress.py both updated to recognize old and new formats together, new .repoman.json as a discoverable record of the prefix (v0.27.3, 2026-08-04)

Theme: tooling · closed 0.27.3 · 2026-08-04


- **Trigger:** Horacio's own proposal, directly motivated by a real, concrete incident found minutes earlier while syncing repoman upstream (T-163): a closure narrative in a different project's own register (seam-ui) was confused by xolu's own bare "T-160"/"T-161" mentioned in prose, corrupting their id sequence -- their own next_id() jumped from T-70 to T-161. A per-project prefix (xolu="XOT", xoluman="XMT", seam="SET") makes that class of collision structurally impossible rather than merely unlikely, since an id from one project can never be mistaken for another's regardless of what free-form prose quotes it.
- **Decision, confirmed directly rather than assumed:** forward-only, not retroactive -- this project's own "IDs are never reused and never renumbered" rule treats the full id string as permanent once assigned; renaming T-1 through T-163 would itself be a renumbering. They keep their original unprefixed form forever. Only this item onward uses the new "XOT<n>" shape (no hyphen, matching the literal example given when this was decided: "XOT100", not "XOT-100").
- **What changed:** `scripts/register.py` -- new `NEW_PREFIX`/`_ID_ALT`/`_id_num()` (a format-aware id-number extractor, replacing every fixed `t[2:]` slice, which was only ever correct for the 2-character "T-" prefix and would have silently miscomputed for "XOT"'s 3 characters); every regex that matches an id (table rows, detail headers, the RESOLVED.md closure-header scan for `next_id()`) now matches either format. `scripts/wave_progress.py` picked up the identical fix independently -- it scans `docs/TRACKING.md`'s own table rows and `After:` fields for a completely separate purpose (wave debt/blocker computation) and would have silently missed any new-format item without it. New `.repoman.json` at the repo root: a discoverable, documented record of xolu's own prefix (matching upstream repoman's own `id_prefix` config key name for future compatibility), even though `register.py` itself hardcodes the value directly rather than reading the file -- consistent with this project's own existing style of hardcoded paths throughout its in-repo tooling, not the generic repoman config.py system. `.repoman.json` added to `release.py`'s own zip source allowlist.
- **A second convention, not code -- for cross-team communication specifically:** when referencing any xolu register item in a letter or document to another team (xoluman, Seam, etc.), use the "XOT" prefix even for an old, internally-unprefixed item (e.g. write "XOT160" for what xolu's own RESOLVED.md still records as "T-160"). The risk this whole change addresses exists identically for old and new ids the moment they appear in someone else's document; prefixing removes the ambiguity there without touching xolu's own permanent internal record.
- **Verification:** full add→close→next_id lifecycle tested in an isolated fixture (not the real register) -- old and new formats coexist correctly in the same table, sorted correctly, closed correctly, and `next_id()` after a close correctly continues the sequence in the new format without reissuing anything. `register.py check` and `wave_progress.py --check` both clean against the real, current register and SUBSTRATE_TRACKING.md afterward. Whole tree build clean.

Cross-ref: CHANGELOG 0.27.3.

## [0.27.2] T-163 — Synced one genuine upstream fix from repoman into xolu's own scripts/register.py (next_id's RESOLVED.md scan now requires trailing whitespace after the id digits, matching upstream) -- checked every other in-repo repoman tool individually rather than assuming a wholesale sync, and found xolu's own guards.py is actually ahead of upstream on a related fix, not behind (v0.27.2, 2026-08-04)

Theme: tooling · closed 0.27.2 · 2026-08-04


- **Trigger:** repoman (github.com/ha1tch/repoman) was updated upstream. Cloned the latest and ran its own 18-check selftest (green) before comparing anything, then diffed each of xolu's own in-repo copies (ed.py, register.py, guards.py, roles.py, syncver.py) against it individually rather than assuming a wholesale sync was safe -- xolu's own copies are adapted, not identical, to the generic upstream tool.
- **A real mistake caught before acting on it:** initially misread the diff direction on `guards.py`'s "Last exercised" date-parsing logic and nearly "fixed" something already correct -- re-checked and found xolu's own copy already has the more advanced behaviour (returns the most recent date when a bullet carries multiple dated addenda, matching this project's own "append, don't rewrite" convention; upstream still only takes the first date via a single-line `.search()`). Xolu is ahead there, not behind -- no change made, and that fix is a candidate to contribute back upstream at some point, not something xolu needed.
- **The one genuine fix, confirmed and applied:** `register.py`'s `next_id()` RESOLVED.md scan gained a trailing-whitespace requirement after the id digits (`T-(\d+)\s`, not just `T-(\d+)`), matching upstream's own refinement -- prevents a malformed adjacent-text id-shaped string from being misparsed. Verified directly: constructed the exact failure shape and confirmed the old pattern over-matched, the new one doesn't, and every real closure header in xolu's own RESOLVED.md (which always has "— " immediately after the id) still matches correctly. `register.py check` clean against the real, current register afterward.
- **Everything else checked, nothing else needed:** `ed.py` and `roles.py` are byte-identical to upstream apart from a cosmetic path comment. `syncver.py`'s differences are architectural, not bugs -- xolu's own version needs to keep `VERSION` and `pkg/version/version.go` in sync, a xolu-specific requirement generic repoman has no equivalent for. `relcore.py`/`config.py` don't exist in xolu at all -- xolu uses its own `release.py` orchestrator instead, a different and more elaborate tool for a different scope.

Cross-ref: CHANGELOG 0.27.2.

## [0.27.1] T-162 — CreateMachineDef/ReplaceMachineDef gained a cheap client-side check that Name/Initial are non-empty, matching a suggestion in the Seam AMS team's own letter (which otherwise turned out to be requesting a method that already shipped in T-153/v0.26.0 -- they'd checked v0.23.0 and v0.25.0, not the newer release) (v0.27.1, 2026-08-04)

Theme: client · closed 0.27.1 · 2026-08-04


- **Trigger:** Seam AMS (a separate internal team, github.com/ha1tch/seam-ui) submitted a request for `CreateMachineDef` -- confirmed directly that it already existed (T-153, v0.26.0), well before their letter, since they had checked v0.23.0 and v0.25.0 specifically but not the newer release.
- **The one genuine gap in their own comparison, incorporated:** their proposed shape included a cheap client-side check that `Name`/`Initial` are non-empty before making the request -- both are trivially always-invalid on the server too, so failing locally saves a round trip for the most common mistake. Added to both `CreateMachineDef` and `ReplaceMachineDef` (the latter takes the same `MachineSpec` and would hit the identical server-side rejection, so fixed for consistency, not just where asked).
- **Not incorporated, and why:** their own response type used `json.RawMessage` for `Analysis`; xolu's existing `MachineDefCreateResult` already uses the structured `*MachineDefAnalysis` from T-153's own earlier work, which is strictly more complete.
- **Verification:** four new tests confirming both methods now reject an empty `Name`/`Initial` client-side. Fixing this surfaced three existing tests that were implicitly relying on incomplete specs reaching the mock server -- updated to supply complete specs, so they continue exercising the server-side behaviour they were actually written to test.

Cross-ref: CHANGELOG 0.27.1.

## [0.27.0] T-161 — New Client.TestConnection() -- Health() cannot verify a credential (/health is deliberately auth-exempt server-side, confirmed directly, so adding an auth header there would be a no-op); TestConnection hits the genuinely-authenticated /schemas endpoint instead, giving xoluman's Test-connection UI the reachable-and-credential-accepted check it actually needs (v0.27.0, 2026-08-04)

Theme: client · closed 0.27.0 · 2026-08-04


- **Trigger:** xoluman team letter, item #5 (the oldest open item in the letter, "simple and has sat unaddressed the longest"): `Client.Health()` doesn't apply the configured auth header, so their "Test connection" and "Test before saving" UI actions can only confirm the server is reachable, never that the configured credential is actually valid.
- **Not simply "add an auth header to Health," confirmed directly before choosing a fix:** `/health` is deliberately exempt from auth server-side (alongside `/ready`, `/version`, `/metrics` -- the standard convention for liveness/readiness probes, which an orchestrator needs to be able to call without a credential). Adding an auth header to `Health()` would be a pure no-op: the server ignores it for that route regardless of what the client sends.
- **Fix:** new `Client.TestConnection(ctx) error`, hitting `GET /api/v1/schemas` -- genuinely authenticated (goes through the normal request pipeline, unlike `/health`), cheap, and tenant-independent (works before a tenant is even chosen, uses the same `buildURLRoot` fixed for T-158). Returns nil only on a genuine 200; `*client.Error` on non-2xx, in particular 401/403 for a rejected or missing credential -- the exact distinction `Health` cannot make. `Health`'s own doc comment updated to explain plainly why it doesn't and never will check auth, and to point callers at `TestConnection` instead.
- **Surfaced a much more serious bug while verifying this properly** (T-160): testing `TestConnection` against all three real auth modes, not just mocks, found the client's own `apikey` auth mode was completely non-functional. Filed and fixed separately; `TestConnection` inherits the fix.
- **Verification:** four mock tests (success, a rejected credential surfacing as `*client.Error` with `HTTPStatus` 401, tenant-configured-but-not-tenant-prefixed, and the correct `ApiKey` scheme) plus the same real integration test that verifies T-160 (correct key succeeds, wrong key and no-credential both correctly fail against a genuine credential-enforcing server). Whole tree clean.

Cross-ref: CHANGELOG 0.27.0.

## [0.27.0] T-160 — Client's AuthAPIKey mode sent 'Bearer <key>', but the server's own apikey validator never accepts that -- every WithAPIKey-configured client was silently unauthenticated on every request, regardless of key correctness; three existing tests actively asserted the buggy format as correct (one literally named TestWithAPIKeySendsBearer), which is why it went uncaught -- found while building T-161 (v0.27.0, 2026-08-04)

Theme: client · closed 0.27.0 · 2026-08-04


- **Trigger:** found while investigating T-161 (xoluman letter item #5, Client.Health()'s missing auth). Building a new authenticated connectivity check meant testing all three client auth modes properly against a real, credential-enforcing server -- not the mock-only coverage the existing apikey tests had.
- **Root cause:** `Client.authHeader()`'s `AuthAPIKey` case sent `"Bearer " + c.apiKey`. The server's own `pkg/authmw.validateAPIKey` checks `X-API-Key` first, then falls back to `Authorization: ApiKey <key>`, then a `?api_key=` query param -- it never accepts a Bearer-prefixed Authorization header for apikey auth type. Every request made with `WithAPIKey` configured was silently rejected as unauthenticated, regardless of how correct the key itself was. Confirmed directly against the real client library and a real running server before touching anything: `ListEntityTypes` with the correct key failed `XOLU-AU001`; after the one-line fix (the correct key), succeeded.
- **Why this went uncaught:** three existing tests actively asserted the buggy "Bearer" format as the *correct* expected behaviour, all using mock servers that just capture whatever the client sends rather than a real server that would actually enforce the format -- `pkg/client/client_test.go`'s own `TestAuthHeader` and `TestWithAPIKeySendsBearer` (the bug was even in the test's own name), and `pkg/client/raw_test.go`'s `TestRawAppliesAuth`. All three fixed to assert `"ApiKey <key>"`; the second renamed to `TestWithAPIKeySendsApiKeyScheme`.
- **Fix:** `authHeader()`'s `AuthAPIKey` case now returns `"ApiKey " + c.apiKey`. `bearertoken` and `jwt` modes were already correct (genuinely use `Bearer`, confirmed directly against the server's own `validateBearerToken`/JWT validation) and were not touched. The doc comment above `authHeader` previously claimed all three auth modes uniformly used "Bearer" -- corrected to state the real, verified behaviour.
- **Verification:** a new real integration test (`pkg/client/integration_test.go`, `bootServerWithAPIKeyAuth` helper) against a genuinely apikey-enforcing server -- correct key succeeds, wrong key gets a real 401, no credential configured gets a real 401. Whole tree clean.

Cross-ref: CHANGELOG 0.27.0.

## [0.27.0] T-159 — Any additionalProperties:false schema failed PUT/PATCH/save outright, XOLU-VL001 id: unexpected field, regardless of what the caller changed -- PUT/save inject id before validating, PATCH validates the merged existing+patch document which always carries id and _version, neither ever declared in a user schema; reported by the xoluman team as their top-priority open item, their own working theory (undeclared ref target) checked and found wrong (v0.27.0, 2026-08-04)

Theme: server · closed 0.27.0 · 2026-08-04


- **Trigger:** xoluman team letter, item #9, filed as their own highest-priority open item ("no workaround exists; this is the one actively blocking real usage today"). A `"format": "ref"` property with no explicit `"target"` -- exactly the pattern their own `examples/crm` seed script uses for every ref field -- worked fine on create, but updating an entity that already had a value in that field (PUT or PATCH, whether the update touched the field or not) always failed with `XOLU-VL001`, `"id: unexpected field"`.
- **xoluman's own working theory checked directly before trusting it, and found wrong:** their diagnosis was that this was specific to an undeclared ref target. Tested precisely: a plain schema with `additionalProperties: false` and no ref fields at all reproduces the identical failure, and declaring a `target` on the ref field does not fix it -- both directly contradict their own theory.
- **Real root cause:** `handleUpdate` (PUT) and `handleSave` (upsert) both explicitly inject `"id"` into the decoded body before calling `s.validator.Validate`; PATCH's own `validate` callback validates the merged (existing + patch fields) document, which always carries both `"id"` and `"_version"` from the stored row. None of the three ever declares `id`/`_version` in a user schema -- they're xolu's own system fields -- so `additionalProperties: false` correctly rejected them per its own spec, on every single update, regardless of what the caller actually changed. `handleCreate` never had this problem by construction: a not-yet-created entity has no `id` yet, so there was nothing to strip in the first place.
- **Fix:** new shared `stripSystemFieldsForValidation` helper (`pkg/server/handlers.go`), applied at all three broken call sites -- validated on a filtered copy with `id`/`_version` removed; the real document (with both intact) is left untouched for graph-edge validation and the actual store write.
- **Verification:** six new tests (`pkg/server/integration_test.go`) -- PATCH/PUT/save each succeed against an `additionalProperties: false` schema; the exact original report shape (an undeclared-target ref field with an existing value) succeeds; and two negative-case regression guards confirm the fix did not make validation over-permissive (a genuinely unknown field and a genuinely wrong type are both still correctly rejected). All four positive-case tests proven to genuinely fail against the reverted code. Full `pkg/server` suite green, whole tree clean (`go test -short ./...`, `golangci-lint`).

Cross-ref: CHANGELOG 0.27.0.

## [0.26.2] T-158 — pkg/client: GetEntitySchema/DefineEntitySchema/ListEntityTypes sent tenant-prefixed URLs for endpoints the server registers only outside the tenant scope by design -- new buildURLRoot (mirroring the already-proven buildURLv2Root) fixes all three; reported and root-caused by the xoluman team, independently re-verified before acting on it (v0.26.2, 2026-08-04)

Theme: client · closed 0.26.2 · 2026-08-04


- **Trigger:** letter from the xoluman team, 2026-08-04 -- a real user of theirs reported "clicking on any entity gives a 400," hard to reproduce for a long stretch since it only manifests when a client has a tenant configured, and every early test case used `AuthType=none` with no tenant set. Root-caused by xoluman via a direct curl comparison and reproduced conclusively against `examples/crm` (shipped as part of T-152) -- exactly the realistic, tenant-scoped, multi-entity-type dataset needed to surface it.
- **Independently re-verified before acting on it, not trusted at face value** -- the report's own diagnosis was precise and matched exactly on direct reproduction against a real server binary (properly provisioned tenant, real curl comparison): `GET /api/v1/tenant/{id}/schema/companies` returns `XOLU-ST004: Invalid ID`, byte-for-byte matching xoluman's own report, because chi's router matches the malformed path against the entity-by-id pattern instead (`/tenant/{id}/{entity}/{id}`), landing `"schema"` in `{entity}` and the real entity name in the numeric `{id}` slot.
- **Root cause:** `pkg/client/client.go`'s `buildURL` applies the `/tenant/{id}/` prefix to every request whenever a tenant is configured, with no awareness that some endpoints are deliberately global. `/schema/{entity}` and `/schemas` are registered only outside the tenant router group by design -- confirmed directly against the server's own comment, `pkg/server/server.go`: `// Schema operations (tenant-independent, always available)`. Checked whether this bug class extended further (generators, sequences) -- it doesn't; those are genuinely, correctly tenant-scoped on both client and server.
- **Fix chosen over xoluman's own two proposed options** (teach `buildURL` about `/schema` specifically, or inline tenant-less URL construction separately in each of the three affected methods) **after finding a better, already-proven precedent first:** `buildURLv2Root` already exists, solving the identical problem for v2's own availability endpoint. New `buildURLRoot` mirrors it exactly for v1 (`%s/api/v1%s`, never tenant-prefixed). Self-documenting at each call site, reuses an established convention instead of inventing a third one, and any future global v1 endpoint gets the same one-line treatment. `GetEntitySchema`, `DefineEntitySchema`, `ListEntityTypes` (`pkg/client/schema.go`) all switched from `c.do(...)` (which internally calls tenant-aware `buildURL`) to `c.doURL(ctx, method, c.buildURLRoot(path), ...)`.
- **Why this specific combination had never been tested:** every existing test for these three methods, unit and integration, across the client's entire history, constructed its client via `New(url)` with no tenant configured -- the exact combination (tenant set + a schema call) had never been exercised even once, checked directly by grepping every test client construction in the file before writing anything.
- **Verification:** 5 mock tests (one per affected method plus the `buildURLRoot` unit tests themselves, mirroring `buildURLv2Root`'s own existing test shape), each proven to genuinely fail against the reverted, pre-fix code, not just pass against the fix. One real integration test against a live server confirming all three calls reach the correct global paths with a tenant configured -- log output confirms zero tenant-prefixed requests. Full `pkg/client` suite green (`-short` and `-tags integration`), `golangci-lint` clean.

Cross-ref: CHANGELOG 0.26.2.

## [0.26.2] T-157 — gc.Worker.RunOnce now recovers a panic from the underlying Sweeper's own Sweep method -- logs at Error with a full stack trace, distinguishable from a normal sweep error, converts it to a returned error instead of letting it crash the process -- defense-in-depth requested directly after T-156 ('we never want a server to panic') (v0.26.2, 2026-08-04)

Theme: gc · closed 0.26.2 · 2026-08-04


- **Trigger:** directly following T-156's own investigation, the team's own framing: "we never want a server to panic do we" -- a request for defense-in-depth, not just the one-off fix.
- **Gap:** `gc.Worker.RunOnce` (the one place `gc.Worker` calls into a third-party `Sweeper` implementation -- the exact, expected boundary for an unanticipated bug like T-156's own type assertion) had no panic recovery at all before this. `Worker.run()`'s own periodic ticker goroutine called `RunOnce` directly, meaning any Sweeper panic propagated all the way up through an unrecovered goroutine and crashed the entire process -- confirmed to be exactly what happened in T-156's own CI failure.
- **Fix:** `RunOnce` now recovers a panic from `Sweep`, captures a full stack trace (`runtime/debug.Stack()`, taken directly inside the deferred recovery function itself so it reflects the actual panic-time call stack, not just the recovery frame), logs at Error level (`Msg("GC sweep panicked")`, distinct from the pre-existing `Msg("GC sweep error")` for a normal returned error -- an operator scanning logs needs to tell "reported a routine failure" apart from "crashed and had to be caught"), and converts the panic into a normal `error` return so every caller (the periodic loop, the admin `POST /api/v1/admin/gc/{name}/run` endpoint, direct test callers) sees an ordinary failure, never a crash. `Duration`/`LastReport`/`LastAt` are still recorded correctly even on the panic path, matching T-154's own established discipline (a sweep attempt's duration is always measured, error or not, panic or not).
- **Scope decision, deliberate:** no separate recovery added around `run()`'s own body (ticker creation, the select loop) -- its only real panic-surface is the call into `RunOnce`, which is now covered; `run()`'s own remaining code is plain, well-understood stdlib usage with negligible panic risk, and a second recovery layer there would be redundant insurance with no real marginal safety.
- **Verification:** a `panicSweeper` test double (panics unconditionally, any value) proves `RunOnce` itself survives and returns a normal error carrying the panic value and worker name. A capturing logger (not the existing tests' `zerolog.Nop()`, which discards everything) confirms the actual log line: Error level, the distinct "panicked" message, the worker name, the panic value, and a real stack trace (`goroutine` present). A third test confirms a normal Sweep error and a Sweep panic produce genuinely distinguishable log messages. The end-to-end proof: the real periodic loop (`Start`/`run`, not a direct `RunOnce` call) survives a sweeper that panics on its first tick and continues ticking normally afterward -- the exact production path a registered `gc.Worker` runs. `-race` clean, 20x. Full `go test -short ./...` and `golangci-lint` both clean.

Cross-ref: CHANGELOG 0.26.2.

## [0.26.2] T-156 — pkg/timeseries: RetentionWorker's sweep()/Sweep() both type-asserted manager.stores' sync.Map key as uint16, but the actual key type is tenant.TenantID (a distinct named type) -- panicked unconditionally on any genuine Purge error, crashing CI; the error path had zero prior test coverage since every existing test used a store whose Purge never fails (v0.26.2, 2026-08-04)

Theme: timeseries · closed 0.26.2 · 2026-08-04


- **Trigger:** CI failure, reported with the full panic stack trace attached. `panic: interface conversion: interface {} is tenant.TenantID, not uint16` in `pkg/timeseries.(*RetentionWorker).sweep.func1`.
- **Root cause:** `manager.stores` (a `sync.Map`) is keyed by `tenant.TenantID` (`type TenantID uint16`, `pkg/tenant/tenant.go` -- a distinct named type, confirmed directly against `Provision`'s own parameter type before touching anything), not bare `uint16`. Both `RetentionWorker.sweep()` (the path `run()` actually takes) and its `gc.Sweeper` twin `Sweep()` (the path the real server actually drives it through, registered as a `gc.Worker`) type-asserted the map key as `key.(uint16)` inside the error-logging branch -- correct only when `store.Purge(ctx)` returns nil, which is every existing test's own case, since all of them use a real, working Pebble-backed store whose `Purge` never fails. The error path itself had zero test coverage before this.
- **Severity beyond the CI failure:** `RetentionWorker` runs as a registered `gc.Worker` in the real server. Before this fix, no `recover()` existed anywhere in that call chain (see T-157) -- any genuine `Purge` failure in a real deployment, for any reason, would have crashed the whole server process, not just failed a test.
- **Fix:** both sites corrected to `key.(tenant.TenantID)`, converted to `uint16` for the existing `.Uint16()` structured-logging call.
- **Verification, through the real public API, no internals reached into:** a `failingStore` test double (embeds `Store` as a nil interface, overrides only `Purge` and `Close`) injected via a custom `StoreFactory` passed to the real `NewManager`/`Provision` path. Two direct regression tests -- one through `sweep()`, one through `Sweep()` (the copy that actually crashed CI) -- both proven to genuinely panic against the reverted, pre-fix code (not just pass against the fix), confirming the tenant ID is now logged correctly, not just "doesn't crash." A third test confirms the success path is completely unaffected. `-race` clean on the whole package, full `go test -short ./...` (exact `make test` invocation) clean, `golangci-lint` clean.

Cross-ref: CHANGELOG 0.26.2.

## [0.26.1] T-155 — golangci-lint reproduced locally and brought to genuinely 0 issues (43 real fixes across three rounds -- errcheck, staticcheck, unused -- after discovering the linter doesn't surface every finding of a kind in one run against this tree, confirmed by a systematic tree-wide sweep, not further linter re-runs) -- --no-lint's own justification in every release since 0.20.6 no longer holds (v0.26.1, 2026-08-04)

Theme: tooling · closed 0.26.1 · 2026-08-04


- **Trigger:** raised alongside T-154's own race report, with an explicit caveat not to discount a connection between the two -- "the linter is not passing in CI, we've been testing this with the linter disabled for a while." `--no-lint` had been the release default since 0.20.6.
- **Confirmed unrelated to T-154:** golangci-lint never touched `pkg/gc` at all across every run of this investigation. Two separate problems with two separate causes, established by actually reproducing both rather than assumed.
- **Reproduced CI locally, not assumed:** installed the project's own pinned `golangci-lint v2.12.2` from source via the approved Go proxy (`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2`), ran it with the repo's existing `.golangci.yml` -- the first lint run against this tree since 0.20.6. First pass: 26 findings (errcheck 19, staticcheck 6, unused 1).
- **A real tooling gap found along the way: golangci-lint does not surface every finding of a given kind in one run against this tree's size.** Discovered empirically, not assumed: fixing the 26 originally-reported errcheck sites and re-running surfaced 3 more identical, previously-unreported sites in files already touched (`pkg/loc/api_support.go`, `geometry.go`). Fixing those and re-running was clearly not going to converge one report at a time, so a manual, regex-verified tree-wide sweep for the exact pattern (`defer <ident>.Close()` / bare `<ident>.Close()` on its own line, in non-test production code) found 14 more across `pkg/loc/verify.go` (8), `store.go`, `patterns.go`, `capacity_contents.go`, `pkg/obj/verify.go` (2), `pkg/server/v2_loc_handlers.go` -- none of which golangci-lint had reported in any of the three runs so far. Fixed via a small, guarded Python script (dry-run reported exactly what would change before writing anything, verified zero remaining matches after, per this project's own mass-substitution discipline for a single-role, uniform-treatment pattern). `golangci-lint run` then genuinely converged to 0 issues, confirmed stable across 5 further runs including one from a fully cleared cache (`golangci-lint cache clean`).
- **All 43 real fixes** (26 + 3 + 14, all errcheck/staticcheck/unused, none security- or logic-relevant -- style and hygiene only):
  - **errcheck (36 total across the three rounds):** every unchecked `rows.Close()`/`db.Close()`/`f.Close()`/`os.Remove(...)`/`os.RemoveAll(...)` in production code, `pkg/loc` and `pkg/tenantexport` (this session's own T-149 export work) and `pkg/server`/`pkg/obj`, fixed to the project's own already-established convention (`defer func() { _ = x.Close() }()`, confirmed by grepping pre-existing, non-session code first rather than inventing a new pattern) or the bare `_ = x.Close()` form for non-deferred call sites. Test files correctly left untouched throughout -- `.golangci.yml`'s own documented policy deliberately relaxes errcheck there ("an unchecked error in setup or assertion helpers surfaces as a test failure anyway"), respected rather than second-guessed.
  - **staticcheck (6):** 3 `QF1002` tagged-switch simplifications in this session's own test files (`blob_export_test.go`, `schema_promotion_test.go`) -- and, noticing the same one-report-doesn't-mean-only-one-instance pattern from the errcheck rounds, 4 more identical unflagged occurrences in the same two files fixed alongside them for genuine consistency. One switch in `blob_export_test.go` correctly left alone -- compound `&&` conditions, not a real tagged-switch candidate, which is exactly why staticcheck didn't flag it either. 2 `QF1008` embedded-field selector simplifications in `pkg/loc/anchor_journal_test.go` (confirmed `Placement Placement` is genuinely an embedded field before simplifying; left the debug-print `%+v` of the whole struct untouched since only the condition selectors were flagged). 1 `S1016` direct struct conversion in `pkg/server/v2_loc_pattern_handlers.go` (confirmed `locPatternDefReq`/`locPatternResponse` are field-for-field identical in name, type, order, and tag before converting).
  - **unused (1):** `pkg/obj/store.go`'s `getInTx` deleted, not just suppressed -- pre-existing code, not this session's, so checked for a deliberate-keep signal first (no register history, tree-wide zero call sites) before removing. Its own doc comment claimed it was "used by containment.go's own guard," which was stale, not just unused: `containment.go`'s `diagnoseContainerRefusal` implements the identical existence check inline via a lighter-weight raw `COUNT(*)` query (it only needs existence, not the full `Subject` `getInTx` would return), evidently having replaced `getInTx` as a caller at some point without the orphaned helper or its now-inaccurate comment being cleaned up.
- **Verification:** `golangci-lint run ./...` -- 0 issues, confirmed 5x including from a cleared cache. `go test -short ./...` (exact `make test` invocation) -- 0 exit code, 41 packages ok, 0 failures. Every touched package individually rebuilt, vetted, and its own affected tests run before moving to the next.

Cross-ref: CHANGELOG 0.26.1.

## [0.26.1] T-154 — pkg/gc: intermittent TestWorker_SweepErrorLogged failure on Apple Silicon was clock-granularity, not a data race (confirmed via -race, formally ruled out) -- fixed by construction; also found and fixed two real T-140-family Worker lifecycle holes (Start's documented double-call panic was never implemented; Stop before Start blocked forever) while auditing the package per the team's own request (v0.26.1, 2026-08-04)

Theme: gc · closed 0.26.1 · 2026-08-04


- **Trigger:** the team's `make test` hit an intermittent failure on `pkg/gc: TestWorker_SweepErrorLogged`, twice-reproduced-then-vanished (fail once, pass twice on immediate retry). Framed explicitly as a possible race, with an explicit instruction to study T-140's own methodology (pkg/server's Start/Shutdown data race, 0.24.3) before touching anything, since the new gc work this session might be "reiterating some of the same errors."
- **Root cause: not a data race.** `-race` clean, 30x, on the whole `pkg/gc` package before any fix -- ruled out formally, not just by absence of symptoms. The actual mechanism is clock granularity: Apple Silicon's monotonic clock ticks at 24 MHz (one tick = 41.67ns), and a warm, zero-work `Sweep()` call (interface dispatch plus one atomic add, no sleep) can genuinely start and finish inside a single tick, making `time.Since` read exactly 0 -- `TestWorker_SweepErrorLogged`'s own assertion (`if r.Duration == 0 { t.Error(...) }`) was testing an assumption that isn't always true. Measured directly on the Linux sandbox: 97.3% of these zero-work windows are under 42ns (min 29ns observed across 2M samples), but this platform's finer clock never actually reads 0 -- which is why hundreds of sandbox repro attempts never reproduced what the M1 hit once. Cold first-call overhead on the M1 usually pushes the one measurement past a tick boundary; occasionally everything's warm and aligned -- hence intermittent, not constant, and gone on retry.
- **Fixed by construction, not by widening tolerance:** both `TestWorker_SweepErrorLogged` and `TestWorker_RunOnce` (identical fragile assertion, hadn't fired yet) now give their sweeper a real 1ms delay and assert `Duration >= delay` instead of `!= 0` -- holds on any platform's clock, and still proves what the test is actually for (RunOnce assigns Duration on the error path too). Tree-wide sweep confirmed these were the only two sites with this assertion shape.
- **Two real T-140-family lifecycle holes found while auditing `Worker`, unrelated to the reported flake but caught by the same investigation:**
  - `Start()`'s own doc comment documented a panic on double-Start; the guard was never implemented. A second `Start()` call silently launched a second `run()` goroutine, whose eventual symptom is a "close of closed channel" panic in `run()`'s own deferred `close(w.done)` -- in a background goroutine, crashing the whole process, far from the call that caused it. Demonstrated live, not theorized: the pre-fix run of the new discriminating test (`TestWorker_StartTwicePanics`) took down the entire test binary mid-suite, attributed to whichever test happened to run next.
  - `Stop()` before `Start()` blocked forever on `<-w.done` -- the identical sharp edge as pkg/server's own pre-T-140 Shutdown-before-Start, kept alive here purely by documentation (`Worker.Stop`'s own comment already warned about it) rather than fixed. Not a live bug on any known call path (every worker the server registers is Started at registration, checked directly), but a footgun with no upside.
  - Fixed: `Worker` gained a `started` bool alongside `stopped` (same mutex). `Start()` now checks-and-sets it, panicking on a true double-call as documented. `Stop()` returns immediately, without waiting on `<-w.done`, when the worker was never started.
  - New tests proving both fixes by construction: `TestWorker_StartTwicePanics` (confirmed to actually crash the test binary pre-fix, not just fail an assertion), `TestWorker_StopBeforeStartReturnsImmediately` (confirmed to hang pre-fix via a 2s timeout guard).
- **Verification:** `pkg/gc` under `-race`, 30x, clean, both before and after. Plain (non-race) 100x in one process, clean. Full `pkg/server` suite (exercises `Worker.Stop` via `Server.Stop`) green. `go test -short ./...` (exact `make test` invocation) clean, 41 packages, 0 failures.

Cross-ref: CHANGELOG 0.26.1.

## [0.26.0] T-153 — pkg/client: full FSM definition write surface -- CreateMachineDef/ReplaceMachineDef/DeleteMachineDef/ValidateMachineDef, plus a structured MachineDefAnalysis type (a real improvement over the letter's own opaque-json.RawMessage proposal) and a backward-compatible ParsedAnalysis() convenience method on the existing MachineDef type (v0.26.0, 2026-08-04)

Theme: client · closed 0.26.0 · 2026-08-04


- **Trigger:** xoluman team letter, 2026-08-04: building an FSM definition editor (Lit+canvas, reusing the approach already proven in Seam AMS's own FSM editor), needed to persist edits back to xolu. `pkg/client` only wrapped the two reads (`ListMachineDefs`/`GetMachineDef`) -- no way to create, replace, delete, or validate a definition through the client.
- **Treated as a proposal, not a spec, per the same discipline as the `/blob/export` redesign:** every wire shape the letter claimed was verified directly against `pkg/server/v2_fsm_def_handlers.go` before writing anything, not trusted at face value -- all of it held up (request/response shapes, status codes, the always-200 validate contract). The client's own existing `MachineSpec` type was checked field-by-field against the server's `fsmDefinitionSpec`, including the trickiest nested shape (`TransitionDef.From`, a JSON string-or-array union) -- genuinely reusable for writes, not just reads.
- **One real design improvement over the letter's own proposal:** the letter followed the *existing* `MachineDef.Analysis` field's own precedent (`json.RawMessage`, opaque, with a doc comment claiming the shape "is xolu-server-internal and may evolve"). That claim was checked directly rather than trusted -- `pkg/server/v2_fsm_common.go`'s own `fsmAnalysis` struct is a stable, well-defined set of fields (reachability, determinism, cycles, warnings) with no instability markers anywhere in the server code, and is exactly the kind of structural feedback an FSM editor UI wants to render directly (unreachable states, cycles, warnings), not reverse-engineer from raw JSON. New `MachineDefAnalysis` struct, used directly in all four new methods. The *existing*, already-shipped `MachineDef.Analysis` field was deliberately left as `json.RawMessage` (avoiding any breaking-change risk to an existing caller), with a new, additive `(*MachineDef).ParsedAnalysis()` convenience method decoding into the same structured type -- fixes the same gap for the existing surface too, zero breaking risk.
- **What shipped**, all in `pkg/client/schema.go` (methods) and `types_schema.go` (types):
  - `CreateMachineDef(ctx, MachineSpec) (*MachineDefCreateResult, error)` -- `POST /api/v2/fsm/def`.
  - `ReplaceMachineDef(ctx, id int64, MachineSpec) (*MachineDefReplaceResult, error)` -- `PUT /api/v2/fsm/def/{id}`. Doc comment states plainly, confirmed directly against the server's own route comment: affects future machines only, not retroactive to machines already running against the old spec.
  - `DeleteMachineDef(ctx, id int64) error` -- `DELETE /api/v2/fsm/def/{id}`. Doc comment states plainly, confirmed directly: always permitted, no check against machines still referencing the definition. No server-side query exists to count machines by definition ID to build a client-side safety check against -- documented as a real caveat rather than faked or silently added as new server scope.
  - `ValidateMachineDef(ctx, MachineSpec) (*MachineDefValidation, error)` -- `POST /api/v2/fsm/def/validate`. Always 200; a non-nil Go error means transport/decode failure only, never an invalid spec -- `MachineDefValidationError` deliberately NOT `*client.Error` (that type represents an actual non-2xx response; reusing it here would misrepresent what happened, confirmed directly against its own doc comment before deciding).
- **Verification:** 15 mock tests (including a distinguishing pair proving an invalid spec produces no Go error while a genuine transport failure does) plus one real end-to-end integration test against a live server -- create, read back both raw and via `ParsedAnalysis`, replace with the new spec's effect confirmed on refetch, validate an invalid spec and a valid one, delete with the resulting 404 confirmed on a subsequent get. Full `pkg/client` suite green throughout, both `-short` and `-tags integration`.

Cross-ref: CHANGELOG 0.26.0.

## [0.25.0] T-152 — examples/crm added to the repository (launcher + seed script + README) -- previously built and verified as standalone deliverables only, referenced in the xoluman letter as a real example before it actually existed anywhere findable (v0.25.0, 2026-08-04)

Theme: tooling · closed 0.25.0 · 2026-08-04


- **Trigger:** the `examples/crm` launcher (`launch_xolu_for_crm.sh`) and seed script (`xolu_crm_seed.py`) were built and verified earlier this same session as standalone deliverables, never added to the repository itself -- the team noticed the letter to the xoluman team referenced "our own CRM demo launcher" as a real, checkable example, but nobody could actually find it since it had never shipped anywhere. Added to `examples/crm/` and backdated into 0.25.0 (this session's own release, not yet taken anywhere by anyone else) rather than deferred to a new release, since the letter's own claim needed to become true before it went out.
- **Real fix required moving it, not just copying it:** the launcher's own `REPO_ROOT` detection (`dirname "${BASH_SOURCE[0]}"`) assumed the script lived at the repository root; living at `examples/crm/` instead broke every path derived from it (`go build` targets, default data directory). Fixed to walk up two levels. Verified directly, not assumed: ran the launcher from an entirely unrelated working directory (`/tmp`), confirmed it still correctly located and built `cmd/xolu`/`cmd/iolu`, then ran the full seed script against it end to end (all six entity types, nested REF resolution confirmed correct three levels deep) with a clean server log.
- **Also tightened while moving it:** the default data directory used to land at the repository root (`$REPO_ROOT/xolu-crm-data`); rescoped to `examples/crm/xolu-crm-data` so running the example doesn't scatter runtime state outside its own directory. A scoped `.gitignore` added for it.
- **New:** `examples/crm/README.md` -- prerequisites, quick start, what each script actually does and why they're separate, and the one known gap (the seed script's own standalone `iolu` fallback can't outrun `TenantMode=strict`'s registry-loads-once-at-startup constraint the way the launcher's own correct sequencing does).

Cross-ref: CHANGELOG 0.25.0.

## [0.25.0] T-151 — Entity listing (GET /entities, ListEntities) plus a full schemaless-to-schemaful promotion mechanism: heuristic schema inference over real data, a read-only suggestion preview, and two promotion modes (flex: fast, no migration; strict: validates every row first, migrates atomically only if all pass, route shape designed for future RBAC) (v0.25.0, 2026-08-04)

Theme: server · closed 0.25.0 · 2026-08-04


- **Trigger:** xoluman team request #3 ("a better /entity/list and ListEntities() in the API... listing all existing entities for a tenant including all available metadata") plus request #4, extended into a full design conversation: "think what can we do to have a promotion mechanism from schemaless entity to schemaful, with a set of clever heuristics to make a good guess about the schema."
- **Entity listing** (`GET /api/v1/entities`, `Client.ListEntities`): row counts, schema/adapted-table status, adapted table columns/indexes, and opt-in graph footprint (`?include_graph=true` -- one indexed pass over the graph table per entity type, deliberately not computed by default). A real bug caught directly: an adapted entity type's data never gets a row in the generic `nodes` table at all (`SQLiteStore.createInner`'s own comment: "Insert entity: adapted table or blob") -- the first implementation queried `nodes` alone via `GROUP BY entity_type` and silently omitted every adapted entity type. Fixed by enumerating entity type names from the node-ID sequence table (`nseq`) instead, which is incremented before the adapted/blob branch splits and is therefore the one place every entity type that has ever had a row created is guaranteed to appear. A genuine routing risk (`/entities` vs. the existing `/{entity}/{id}` CRUD route -- same path shape) tested adversarially with an entity type literally named `entity`, confirmed both resolve correctly and independently.
- **Schema inference engine** (`pkg/server/schema_inference.go`): samples up to 500 rows, infers per-field type/coverage/required, detects REF fields structurally (not just "has a type key" -- tested against a decoy object with its own unrelated `type` field), detects enum candidates with guards against both high-cardinality and sparse-sample false positives, flags decimal-*looking* strings as medium-confidence with an explicit caveat rather than a confident type claim, and defaults the suggested schema to permissive (`additionalProperties` left unset) since inference runs on a sample, not necessarily all data. 15 tests.
- **Promotion, two modes** (`GET /entity/{type}/schema-suggestion` read-only preview; `POST /entities/promote/flex/{type}` and `POST /entities/promote/strict/{type}`): route shape is the team's own design, not inferred -- the action (`flex`/`strict`) is a stable path prefix independent of entity type specifically so a future RBAC layer can grant or deny "who may run strict promotions" as a single pattern match, without per-entity-type rules.
  - `flex`: fast, registers the schema and creates the adapted table immediately, matching `DefineEntitySchema`'s own shape. Does NOT migrate pre-existing rows -- verified directly: a pre-existing row stays reachable by `GET /{entity}/{id}` (falls back to blob storage) but disappears entirely from `GET /{entity}` (LIST) and from `/entities`' own count, since LIST queries the adapted table exclusively. Surfaced explicitly via a `warning` field in the response rather than left as a silent gap.
  - `strict`: the real fix for that gap. Compiles the candidate schema standalone (not registered anywhere) and validates *every* existing row against it before touching anything; migrates all rows into the adapted table as one atomic transaction only if every row passes. If any row fails, nothing changes at all -- verified directly (a 2-row entity type, one conforming and one not: rejected, precise per-row failure reported, entity type confirmed still exactly as schemaless as before). Async (ticket + `GET /entities/promote/status/{ticket}`), throttled per (tenant, entity type) rather than per tenant, since promoting two different entity types for the same tenant concurrently is fine -- a new, small `PromoteJobManager` modeled on `tenantexport.JobManager`'s own proven design rather than forcing that manager to generalize over a different key shape and result type. New `SQLiteStore.MigrateBlobEntitiesToAdapted` (dialect-specific internals kept properly encapsulated in `pkg/storage`). Two new error codes (`XOLU-ST010`/`011`).
  - **Two real bugs caught by actually running it, both before shipping:** a literal JSON `null` request body (Go's own typed-nil-in-interface behaviour when a caller passes a nil map) decodes successfully to a nil schema map, which a naive `ContentLength != 0` check treated as "explicit schema given" rather than "no schema, infer one" -- fixed to check decoded content, not body length. `queryfy.Strict` validation mode rejects any field not explicitly listed in a schema's own properties regardless of `additionalProperties` -- the stored row's `id` field is deliberately never part of an inferred schema, so an all-conforming dataset was being rejected outright on "id: unexpected field" until rows were stripped of `id` before validation, matching what `inferSchema` already did at the analysis stage.
- **Client side:** `GetSchemaSuggestion`, `PromoteFlex`, `PromoteStrictStart`/`PromoteStrictStatus`, and `PromoteStrict` (the one-call convenience wrapper -- start, poll, return -- same shape as `Export()`). One more subtlety confirmed by a real test, not assumed: `PromoteFlex(ctx, type, nil)` sends a literal `"null"` body (the same typed-nil behaviour noted above), and only round-trips correctly because the server-side fix already treats a decoded-to-nil schema the same as no body at all.
- **Verification:** 11 server-side tests (flex + strict, including the throttle test rebuilt to control a job's completion directly via a blocking channel after an HTTP-timing version proved racy -- a real migration can finish before a second sequential HTTP call even lands) plus 16 client-side mock tests plus 3 real end-to-end integration tests (strict success verified via `ListEntities`, strict rejection verified nothing mutated, flex's warning verified populated against real pre-existing data). Clean under `-race`. Full `pkg/server` and `pkg/client` suites green throughout.

Cross-ref: CHANGELOG 0.25.0.

## [0.25.0] T-150 — iolu tenant create only registered the tenant in the shared registry table, never provisioned its own entity/graph tables -- a 'created' tenant was a registry row plus a promise, producing a real (if low-severity) hydration warning on every boot until first write (v0.25.0, 2026-08-04)

Theme: iolu · closed 0.25.0 · 2026-08-04


- **Trigger:** the team's own correction (2026-08-03): "It seems wrong that iolu creates an incomplete tenant" -- a boot-time `WRN loadEntitiesFromEdgeTable: tenant hydration failed; skipping ... no such table: t0001_graph` was initially defended as "expected, lazy table creation" (the same pattern as `n_sch` elsewhere); the correction was decisive -- a warning under completely normal, correct usage is itself the bug, regardless of architectural explanation.
- **Root cause:** `iolu tenant create` only inserted the new tenant's row into the shared `tenants` registry table (via `openTenantStore(base, 0, ...)`, hardcoded to tenant 0 purely to reach that table). It never opened a store scoped to the NEW tenant's own ID, so `SQLiteStore.initialize()` -> `createSchema()` (which creates a tenant's own `t<XXXX>_*` table family, including the graph table when `GraphEnabled`) never ran for it. A "created" tenant was a registry row plus a promise, not a complete, ready one.
- **Fix:** `cmdTenantCreate` now opens a second store scoped to the new tenant's own ID immediately after registration, triggering the same table creation the server would run on that tenant's first write -- proactively, at create time. New `--graph` flag (default `true`, matching the server's own `GraphEnabled` default). Verified directly: all 18 expected tables exist immediately after `iolu tenant create`, before the server ever runs, and a subsequent server boot produces zero hydration warnings.

Cross-ref: CHANGELOG 0.25.0.

## [0.25.0] T-149 — Async, tenant-scoped, blob-backed tenant export (supersedes T-145's original synchronous scope, per the team's own design correction) -- new pkg/tenantexport package, server wiring, TTL sweep, and the client-side Export()/BlobExportStart/Status methods completing the actual requirement (v0.25.0, 2026-08-04)

Theme: server · closed 0.25.0 · 2026-08-04


- **Trigger:** the team's own design correction (2026-08-03) while T-145 was underway: the original scope (a synchronous, non-tenant-scoped streaming client method against the existing `GET /api/v1/export`) was rejected on two grounds -- any authenticated credential could exfiltrate the entire cross-tenant database in one call (blast radius far larger than any other endpoint), and a synchronous HTTP call is the wrong shape for a potentially slow, whole-database operation. Redesigned as async, tenant-scoped, and blob-backed, modeled on Sulpher's own async-query pattern and reusing the newly-built `/blob` primitive (T-142) as the delivery mechanism rather than inventing a second one.
- **What shipped:** new `pkg/tenantexport` package -- `ExportSQLiteTable`/`ExportSQLiteTables` (iterate a table, filtered by `tenant_id` for shared tables or unfiltered for already tenant-prefixed ones -- two real bugs caught and fixed here: tables need real invariant methods (`TenantID.NodesTableName()` etc.), not hand-built `prefix+suffix` strings, and a lazily-created table like `n_sch` must be treated as zero rows, not an error), `ExportPebbleStore`/`ExportPebbleStores` (base64 key/value dump, missing-store-directory treated as legitimate "never used", not an error), `PackageAndStore` (zip + real blob upload), `ExportTenant` (full orchestration across the verified, authoritative primary-store table list plus `loc`/`obj`'s own dedicated files plus `ts`/`cal`/`bal_rollup`'s Pebble stores), `JobManager` (ticket-based async jobs, one export in flight per tenant, bounded total concurrency -- proven under `-race`), and `SweepExpiredExports` (TTL cleanup for completed export blobs, wired into `pkg/gc`'s existing `Sweeper`/`Worker` framework as its own independent worker, deliberately separate from ordinary blob GC since an export blob is still referenced by its own key alias and GC alone would never reclaim it).
- **Server wiring:** `POST`/`GET /api/v1/tenant/{id}/blob/export{,/​{ticket}}`, `blobManager`'s own `blobExportSweeper` (separate `gc.Sweeper`, not folded into `blobManager.Sweep`), new config (`BlobExportSweepEnabled`/`IntervalSecs`, `BlobExportTTLSecs`, defaults 15min/4hr), two new error codes (`XOLU-BL007`/`008`).
- **A real bug caught before it shipped:** the export job's own background closure originally captured `r.Context()`, which Go's HTTP server cancels the instant the handler returns -- the export would fail with `context canceled` almost immediately after the 202 response went out. Fixed to `context.Background()`, matching `oql.JobManager`'s own established pattern for async work that outlives its triggering request.
- **Client side (completing the actual T-145 requirement -- the client still had to deliver export data to the caller, streamed; only the mechanism changed):** `Client.BlobExportStart`/`BlobExportStatus` (the raw primitives) plus `Client.Export(ctx, io.Writer)` -- a convenience wrapper running the whole flow (start, poll, download via the existing streaming `BlobGet`) and returning the same one-call experience the original synchronous design would have had. 8 unit tests plus a real end-to-end integration test (real entity seeded, real async job, real zip downloaded and verified to contain that entity's data, cross-checked against a direct `BlobGet` on the same key).
- **Verification:** `pkg/tenantexport` — 20 tests, clean under `-race`. Full `pkg/server` and `pkg/client` suites green throughout. Integration tests exercise the real HTTP flow end to end, not mocks.

Cross-ref: CHANGELOG 0.25.0.

## [0.25.0] T-147 — pkg/client: a schema-registration wrapper (POST /api/v1/schema/{entity}) -- GetEntitySchema exists for reading, nothing exists for writing. Blocks part of xoluman's blob-folder design (T-09), not blob access itself (T-142). Side effects (adapted table creation, immediate validation enforcement) mean the API shape is xolu's own design call, not inferred here (v0.25.0, 2026-08-04)

Theme: client · closed 0.25.0 · 2026-08-04


- **Trigger:** xoluman team letter, request #4. Blocks part of xoluman's blob-hierarchy design (T-09, xoluman's own tracking) -- not blob access itself (T-142/Wave 12 covers that), the "virtual folders" layer on top of it, which needs to register a schema for a bookkeeping entity type (`xoluman_blob_folder`) it creates.
- **Scope:** a client wrapper for `POST /api/v1/schema/{entity}` (`docs/JSON_SCHEMA.md`) -- confirmed directly that `pkg/client` has `GetEntitySchema` for reading but nothing for writing.
- **Explicitly flagged by xoluman's own letter, and worth repeating here rather than deciding unilaterally:** schema registration has real side effects -- creates an adapted table and starts enforcing validation immediately, per xolu's own docs. The exact method shape (sync vs. explicit two-step confirm, how validation errors on the schema document itself surface) is the xolu team's design call, not something to infer from the client side. xoluman's letter states this outright: "flagging the need, not proposing an API."
- **Lower priority than #1/#5 (already resolved) and #2/#3:** per xoluman's own stated priority ordering -- blocks part of a feature (T-09) that isn't their current focus.

Cross-ref: CHANGELOG 0.25.0.

## [0.25.0] T-146 — pkg/client: a minimal raw request method exposing the existing do/doURL/doOnce auth+retry machinery -- xoluman's REST query console is blocked on this, and the point is reusing existing auth logic, not reimplementing it per consumer (v0.25.0, 2026-08-04)

Theme: client · closed 0.25.0 · 2026-08-04


- **Trigger:** xoluman team letter, request #3. Blocks xoluman's ad hoc REST query console (one of three planned query modes; OQL and Sulpher are already fully covered by `Client.OQL`/`Client.Sulpher`).
- **Scope:** an exported method exposing the existing `do`/`doURL`/`doOnce` request machinery -- xoluman's own letter is explicit that the exact signature isn't the point, only that the already-correct auth/retry logic lives in one place rather than being reimplemented by every consumer wanting raw access. A reasonable shape: `Raw(ctx context.Context, method, path string, body io.Reader) (status int, respBody []byte, err error)`.
- **Design question left open, not decided here:** should this bypass structured-error decoding (`*client.Error`) entirely, or attempt it and fall back to raw bytes on non-JSON responses? xoluman's use case (an operator manually issuing arbitrary requests) probably wants the raw response even on error, not a decode failure -- but that's the implementer's call, not scoped here.

Cross-ref: CHANGELOG 0.25.0.

## [0.25.0] T-145 — pkg/client: a streaming Export method, writing to an io.Writer rather than buffering -- xoluman's own backup feature is blocked on this, and /export's own server-side design (no temp file, unbounded response size) means a buffering client would defeat the point (v0.25.0, 2026-08-04)

Theme: client · closed 0.25.0 · 2026-08-04


- **Trigger:** xoluman team letter, request #2. Blocks xoluman's backup feature (directly requested in its own spec).
- **Scope:** `Client.Export(ctx context.Context, w io.Writer) error` (or similar -- xoluman's own letter explicitly left the exact signature open: "the point isn't the exact signature, just that the existing auth/retry logic lives in one place"). Must write to an `io.Writer`, not buffer into `[]byte` -- `EXPORT_API.md` (fixed this session, T-144's own sibling doc-fix work) is explicit that `/export` streams a zip without a server-side temp file, so nothing bounds response size in advance; a client that buffers the whole response into memory before returning defeats that design for any database large enough to matter.
- **Confirmed directly, not assumed, while scoping this:** `/export` is registered only among xolu's non-tenant-scoped routes (`server.go`, inside the `!s.config.Tenancy().Has(config.TenantRequireRoute)` block) and is disabled entirely under `XOLU_TENANT_MODE=strict`. A client method must NOT apply the usual tenant-path-prefixing (`buildURL`'s standard behaviour for nearly every other method) -- doing so would construct a URL that doesn't exist server-side. This needs its own low-level request path, not the standard tenant-prefixed JSON helpers.
- **Not scoped here, left for whoever implements it:** the exact Go signature, whether progress/size reporting is exposed, and how the manifest (`manifest.json` inside the zip -- `database_file`, `graph_json` keys, per `EXPORT_API.md`) gets surfaced to the caller if at all.

**Post-closure correction (2026-08-04, same session):** the scope above describes what was originally planned, not what shipped. Mid-implementation, the team rejected the synchronous, non-tenant-scoped design on two grounds -- any authenticated credential could exfiltrate the entire cross-tenant database in one call, and a synchronous HTTP call is the wrong shape for a potentially slow, whole-database operation. Redesigned as async, tenant-scoped, and blob-backed; see T-149 for the actual implementation and full history. The underlying requirement this item names -- the client has to deliver export data to the caller, streamed -- was still fulfilled, just via `Client.Export`/`BlobExportStart`/`BlobExportStatus` against the new mechanism, not the one described above. Left as historical record per this project's own convention (never silently edit a closed item to match reality); T-149 is the accurate account.

Cross-ref: CHANGELOG 0.25.0.

## [0.25.0] T-142 — pkg/client blob support (item 57, wave 11): reopens the client's own documented v0.16.0 exclusion of /blob, triggered by a real consumer (Seam AMS) needing it -- exactly the condition docs/CLIENT_STAGE6_PLAN.md named for revisiting scope (v0.25.0, 2026-08-04)

Theme: client · closed 0.25.0 · 2026-08-04


- **Trigger:** direct request while diagnosing an OQL issue on the Seam AMS side -- Seam needs `pkg/client` to support `/blob`, currently one of the client's own documented exclusions (`pkg/client/client.go`'s own package doc: "Deliberately out of scope -- documented exclusions, not omissions: timeseries, blob, meta, admin..."). That doc names exactly this situation as the intended trigger for reopening scope: "revisitable when a consumer needs them... stability declared over a surface nobody exercises is a liability, not an asset" (`docs/CLIENT_STAGE6_PLAN.md`, the original T-02/Stage-6 audit, 2026-07-18).
- **Scope, mirroring T-67's own shape (bal's client addition -- `bal.go`+`types_bal.go`, matching `cal.go`'s established pattern) exactly:** `pkg/client/blob.go` + `pkg/client/types_blob.go`, wrapping the six native (non-S3-compat) blob endpoints confirmed directly against `pkg/server/server.go`'s own route table: `POST /blob` (put), `GET /blob` (list), `GET /blob/usage`, `GET /blob/{key}` (get), `HEAD /blob/{key}` (head), `DELETE /blob/{key}`. The S3-compatible surface (`/{bucket}/{key}` etc., `pkg/server/blob_s3_handlers.go`) is deliberately NOT wrapped here -- that surface exists for real S3 SDKs/tools, not xolu's own first-party Go client; wrapping it would duplicate what any S3 client already does, not extend xolu's own client surface.
- **Package doc update, not silently expanding scope:** `pkg/client/client.go`'s own "Deliberately out of scope" list moves `blob` out of the exclusion list into the declared surface, with a dated note on why (mirroring how the doc already explains its own boundary) -- the whole point of the original exclusion being *documented* rather than accidental is that reopening it should be recorded the same way, not just quietly patched.
- **Test coverage, matching T-26's own established pattern (build-tagged integration suite against an in-process server, happy paths, not an exhaustive error matrix):** extend `pkg/client/integration_test.go` with blob's own six methods, mirroring `TestIntegration_BalFullFlow`'s shape.
- **Deliberately NOT bundled into this item, left as a clearly separate decision:** `timeseries` is the other primitive on the SAME exclusion list, named in the same original audit for the same reason. Not filed here since only `/blob` was actually requested -- but it's the obvious next candidate under the same rationale, cheap to add as a sibling item under this same wave if wanted.
- **Exit:** `pkg/client/blob.go`/`types_blob.go` shipped, package doc's exclusion list updated, integration suite green against a real in-process server (not mocks -- T-32's own history is the standing argument for why mocks alone aren't trusted here), `docs/CLIENT_STAGE6_PLAN.md`-style scope note recorded.

Cross-ref: CHANGELOG 0.25.0.

## [0.24.7] T-144 — OQL ORDER BY DESC on a string field (e.g. timestamp) silently fell back to insertion order for tied values -- fmt.Sscanf's %f verb parsed '2026-08-03T...' as the bare year 2026, making every same-year timestamp compare as numerically equal. Same bug in 3 places: pkg/oql (sort comparator + arithmetic eval), pkg/qs (shared aggregates), pkg/sulpher (WHERE-clause comparisons) (v0.24.7, 2026-08-03)

Theme: oql · closed 0.24.7 · 2026-08-03


- **Trigger:** Seam AMS's own diagnostic work (two rounds -- an initial isolation, then a second, decisive round that deliberately broke the ambiguity in the first by constructing a dataset where insertion order and true field-value order genuinely differ). Their own conclusion, quoted directly: "DESC correctly finds/places the extremum, then the remainder of the ordering falls back to ascending rather than continuing descending." That specific, unusual shape -- not "DESC ignored," but "single extremum right, rest reverts to insertion order" -- was the decisive clue.
- **Root cause, confirmed directly with a standalone Go snippet before touching any code:** `pkg/oql/aggregator.go`'s `toFloatSafe` (used by `havingCompare`, the comparator `OrderBy`'s `sort.SliceStable` call uses) fell back to `fmt.Sscanf(s, "%f", &f)` to detect numeric-looking strings. `Sscanf`'s `%f` verb matches only a LEADING numeric prefix and reports success (`err == nil`) even with unconsumed trailing characters. Confirmed directly: `Sscanf("2026-08-03T02:04:33Z", "%f", &f)` succeeds with `f=2026`, silently discarding everything after the year. Every timestamp from the same year therefore compared as numerically EQUAL under `havingCompare`. `sort.SliceStable`'s own stability guarantee then preserved the ORIGINAL (insertion) order among these "tied" values -- while the one genuinely distinguishable value (Seam's deliberately-injected 2099 outlier) sorted correctly to the true extreme, since it wasn't tied with anything. This exactly explains the reported shape without any other mechanism needed.
- **Three separate call sites had the identical pattern, found by grep sweep after fixing the first:** `pkg/oql/aggregator.go`'s `toFloatSafe` (ORDER BY comparator) AND a second, separate function `toFloat` in the same file (used in arithmetic/aggregate expression evaluation); `pkg/qs/aggregate.go`'s `toNumeric` (shared by both OQL's and Sulpher's own aggregate functions -- SUM/AVG/MIN/MAX on a string-typed field could have silently used just a leading numeric prefix); `pkg/sulpher/executor.go`'s `parseFloat` (used in `<`/`>`/`<=`/`>=` comparison-operator evaluation -- meaning WHERE-clause filtering in Sulpher path queries could ALSO have been affected, not just OQL sorting/aggregates). Full-tree sweep (`grep -rn 'Sscanf.*"%f"'`, not just `pkg/`) confirmed no fourth occurrence.
- **Fix, identical shape at all three sites:** replace `fmt.Sscanf(s, "%f", &f)` with `strconv.ParseFloat(s, 64)`, which requires the ENTIRE string to be a valid float -- rejects `"2026-08-03T02:04:33Z"` cleanly (confirmed directly: returns an error, not a silently-wrong partial value) while still correctly parsing the genuine numeric strings these functions exist for (`"9369.41"`-style denormalised decimal aggregate results, confirmed unaffected). `fmt` import removed from `pkg/qs/aggregate.go` and `pkg/sulpher/executor.go` where it became unused after the fix (both files' remaining `fmt.` references were only inside comments, confirmed before removing).
- **New regression test**, `pkg/oql/eval_coverage_test.go`'s `TestOrderBy_StringTimestampField_FullOrderNotJustExtremum` -- mirrors Seam's own exact repro shape (nine same-year timestamps in genuinely ascending order, one outlier), and deliberately asserts the FULL descending order, not just the first row, since asserting only the first element is exactly the check that would have let this bug ship undetected (the first row was always correct). Confirmed genuinely discriminating: reverted the fix, watched the test fail with the exact reported symptom (outlier first, then ascending 1..9 instead of descending 9..1), restored the fix, watched it pass.
- **Verified:** `pkg/oql`, `pkg/qs`, `pkg/sulpher` full suites green; whole-tree `go build`/`go vet` clean.
- **Not yet done:** no regression test added for the `pkg/qs`/`pkg/sulpher` call sites specifically (only the OQL ORDER BY path has a new test) -- the shared root cause and identical fix make this lower-risk, but it's an honest gap, not an oversight to hide.

Cross-ref: CHANGELOG 0.24.7.

## [0.24.7] T-143 — oql.Calibrate() ran uncached on every Server construction -- 965 redundant calls measured in one test shard, ~17.3ms each (~16.7s pure waste); also settles whether admin tooling is slow due to Python (no, measured <0.1%) or warrants a Go rewrite (no, wrong diagnosis -- go test itself is the cost) (v0.24.7, 2026-08-03)

Theme: tooling · closed 0.24.7 · 2026-08-03


- **Trigger:** direct challenge -- "way too long... let me know if the problem is xolu or the python tools... propose a Go rewrite if the tools are inadequate."
- **Measured before proposing anything:** `testrun.run_shard`'s own Python-side JSON parsing (`count()`) took 0.07s against a 133.8s total shard run -- under 0.1%. Over 99.9% of wall-clock time is `go test` itself (compile + execute), not Python. A Go rewrite of the orchestration layer (release.py/testrun.py) would not make `go test` run any faster -- it would be a Go program waiting on the same subprocess for the same duration. Not recommending it; the diagnosis it would be based on is wrong.
- **A real, separate inefficiency found and fixed instead:** `oql.Calibrate()` (pkg/oql/calibrate.go) ran uncached, unconditionally, on every `Server` construction with the default/auto performance profile -- confirmed zero caching anywhere (`sync.Once`, cache var, nothing). Measured directly: 965 calibration calls within a single 8-package test shard (pkg/server constructs hundreds of test servers via its own shared helpers), each costing ~17.3ms measured directly (not the doc comment's own more conservative 100-300ms estimate) -- roughly 16.7s of pure, repeated measurement of the SAME hardware property within one process.
- **Fixed:** `Calibrate()` now caches its result process-wide via `sync.Once` -- correct because it measures a hardware property (relative Go-JSON-processing vs SQLite-query-engine speed, via a fixed self-contained benchmark table) invariant across different callers' databases within the same process, not anything about the calling database's own current content. A production binary calls this once at real startup regardless, so caching changes nothing there; it only removes redundant work in processes (chiefly tests) that construct many Server instances. Verified: recount after the fix, 965 -> 1 calibration call for the identical shard. Full `pkg/oql` suite green; existing `TestCalibrate` (the only test directly exercising this function) unaffected -- no test assumes fresh-per-call behavior.
- **Honest result, not oversold:** re-measuring the identical shard post-fix showed no meaningful total-time improvement (133.8s before, 137.2s after -- within this sandbox's own observed run-to-run variance, which ranges 84-137s for the same work across earlier measurements this session). The fix is correct and worth keeping regardless -- there is no reason to remeasure the same hardware property 965 times -- but it is not, by itself, what determines whether a release completes within one tool call here. That's dominated by this sandbox's own variance, which no code-level fix addresses.
- **Verdict on the actual question asked:** not a Python-tooling problem (measured), not fixable by a Go rewrite (would address the wrong bottleneck). T-141 (SPLIT_THRESHOLD, this session, ~100s/release saved) remains the largest real win found. This item is a second, smaller, genuine one. The residual friction (occasional single-call timeouts) is sandbox compute variance plus this suite's own size (5330+ tests) -- `release.py --resume`'s journaled resumability is the correct existing tool for that, not a rewrite.

Cross-ref: CHANGELOG 0.24.7.

## [0.24.4] T-141 — scripts/testrun.py: SPLIT_THRESHOLD=150 is stale -- pkg/server (957 tests, well over it) runs cleanly unsplit in 68.8s vs 91.8s split into 11 segments (34% pure overhead); the whole shard it belongs to runs in 84s unsplit vs ~183s as actually released. The capacity ceiling T-111 set this threshold against no longer applies at current sizes (v0.24.4, 2026-08-03)

Theme: tooling · closed 0.24.4 · 2026-08-03


- **Trigger:** the team's own direct challenge -- "it's not possible for the shards to take more time as shards than the total time the tests take to execute" -- after a foreground `release.py` invocation timed out mid-pipeline. Investigated directly rather than accepted or dismissed.
- **Measured, not assumed:** `pkg/server` alone (957 top-level tests today, well over `testrun.py`'s `SPLIT_THRESHOLD=150`), run as ONE unsplit `go test` invocation: **68.8s.** The same 957 tests run through `testrun.run_package_split` exactly as `release.py`/`baseline.py`/`regrun.py`/`runtests.py --shard-size` all do it (11 segments of ~90 tests each, per `SPLIT_SEGMENT_SIZE=90`): **91.8s -- 23s (34%) of pure overhead**, zero test-count difference, for identical, all-passing work. The actual 8-package shard this belongs to (`s3sig, server, storage, storelayout, sulpher, tdigest, tenant, timeseries` -- `sulpher` and `timeseries` also cross the threshold and get split individually): unsplit, **84s total**; the real release run (all three packages split) took **~183s for the same shard -- more than double, for identical work, zero failures either way.**
- **Root cause of the overhead:** each split segment is a fully separate `go test` process -- its own process startup, its own test-binary compile+link (Go's build cache speeds recompiling the underlying packages, but linking a large test binary and starting a fresh process still costs real wall-clock time), paid 11 times instead of once. Multiplying fixed per-invocation overhead by the segment count is the entire mechanism; nothing about it scales sub-linearly.
- **Why the threshold was ever needed, and why that reason may no longer hold:** `SPLIT_THRESHOLD=150`/`SPLIT_SEGMENT_SIZE=90` were set by T-111 (2026-08-01), itself triggered when `pkg/server` (869 tests then) tripped this sandbox's own single-process capacity ceiling -- T-111's own text attributes this to TIME_WAIT/socket-table pressure from cumulative `httptest.Server` volume, the same class of resource-exhaustion symptom (though not confirmed to be the identical mechanism) as T-98's own leak and this session's own T-139 (unclosed `bal` rollup Pebble handles + `cal` manager, fixed 2026-08-03). Not overclaiming a single causal fix here -- what's DIRECTLY verified today, by measurement, not inference: `pkg/server` and its shard siblings now run cleanly, unsplit, at their current size, in this same sandbox. Whatever combination of fixes across the intervening sessions raised the effective ceiling, it has been raised past `pkg/server`'s current 957 tests.
- **Practical impact, matching the team's own original complaint exactly:** this overhead is the direct, measurable reason foreground release/test invocations have been running long enough to hit this environment's own per-call execution ceiling. Not the only contributor to total pipeline time, but a real, fixable, currently-active one -- ~100s of pure waste across shard-04 alone in the run that prompted this investigation.
- **Fix:** raise `SPLIT_THRESHOLD` substantially rather than remove the mechanism outright -- the safety net stays available for a package that genuinely does re-exceed real capacity later (test count keeps growing; a future leak could reintroduce the original pressure). Not re-deriving a precise new ceiling empirically (that's its own investigation, matching how T-98 originally established evidence before picking a number) -- raising well clear of `pkg/server`'s current 957 with headroom for near-term growth, documented as provisional pending a proper capacity re-derivation if it's ever tripped again.
- **Verified after the fix, with the real number:** the exact shard-04 package set, through `testrun.run_shard()` itself (the same function `release.py` calls, not a hand-rolled bypass) -- zero `split.*` files produced (confirming no package routed through `run_package_split`), completed in **94.4s**, in line with the 84s unsplit baseline measurement, not the ~183s figure the old threshold produced.

Cross-ref: CHANGELOG 0.24.4.

## [0.24.3] T-140 — pkg/server: Server.Start()/Shutdown() data race on unsynchronized httpServer/metricsServer/s3Server fields -- caught by runtests.py --race, exactly the conventional goroutine-Start-then-signal-handler-Shutdown production pattern, not a test artifact (v0.24.3, 2026-08-03)

Theme: server · closed 0.24.3 · 2026-08-03


- **Trigger:** verifying `scripts/runtests.py --race`'s flag threading (0.24.2's own tooling-consolidation work) by running it against a real shard rather than trusting the code by inspection alone. It worked exactly as intended and immediately caught a real, previously-undetected race -- direct evidence of the value T-139's tooling consolidation was meant to provide (a canonical `--race` sweep that wasn't convenient to run before now caught something the standard non-race release/baseline pipeline never would have).
- **The race:** `Server.Start()` unconditionally assigns `s.httpServer`, `s.metricsServer`, `s.s3Server` (plain, unsynchronized pointer fields) before calling the blocking `s.httpServer.ListenAndServe()`. `Server.Shutdown()` reads those same fields (nil-checks then calls `.Shutdown(ctx)` on each) with no synchronization between the two. `TestServerMetricsHost_Branches` (`pkg/server/lifecycle_test.go`) calls `go server.Start()` in one goroutine and `server.Shutdown()` in another without waiting for Start()'s field assignments to complete first -- caught by `-race` as multiple concurrent read/write pairs on `s.httpServer` and its embedded atomic shutdown flags, both directions (Start's writes racing Shutdown's reads, and vice versa on the fields Shutdown itself sets).
- **Not just a test artifact -- the same shape as real production usage.** The conventional pattern (`go server.Start()` in a goroutine, `server.Shutdown()` later from a signal handler in the main goroutine) is exactly this race. The test isn't inventing an unrealistic scenario; it's exercising the ordinary lifecycle pattern without an artificial delay, and the race detector is right that nothing in `Server`'s own code guarantees Start()'s field assignments happen-before a concurrent Shutdown()'s reads.
- **Why nothing caught this before:** the standard release/baseline test pipeline doesn't run a full `-race` sweep (only specific dormant guards -- G-13/14/15/16/17 -- target `-race` at particular packages/tests, not this lifecycle path). `runtests.py --race` is the first convenient, canonical way to run `-race` broadly; this is the first time it's been run against pkg/server broadly since the tool existed.
- **Fixed:** `pkg/server/server.go`. Added `startReady chan struct{}`, initialised in `New()`, closed by `Start()` immediately after `httpServer`/`metricsServer`/`s3Server` are all assigned (right before the blocking `ListenAndServe` call). `Shutdown(ctx)` now `select`s on `<-s.startReady` vs `<-ctx.Done()` before touching any of the three fields -- a real Go-memory-model happens-before edge (channel close/receive), not a timing heuristic. Solves both halves of the bug at once: the race itself, and the silent-shutdown-lost failure mode (Shutdown called before Start had set anything up previously just saw nil and returned success having done nothing; it now properly waits, bounded by the caller's own context, and returns that context's error if Start never comes).
- **A pre-existing test encoded the bug as "correct" and had to be updated, not just the new code:** `TestServerLifecycle_ShutdownWithNilHTTPServer` asserted `Shutdown()` before `Start()` returns `nil` -- exactly the silent-no-op this fix removes. Updated to assert the new, correct contract (a context-deadline error), with its own doc comment explaining why "safe" changed meaning. Caught by running the full `pkg/server` suite under `-race` after the fix, not assumed clean.
- **New tests, proving the fix by construction, not by reduced flakiness:** `TestServer_StartShutdown_NoSleepRace` (`pkg/server/lifecycle_test.go`) calls `Shutdown()` the instant `Start()` is launched -- zero delay, the tightest possible window, deliberately tighter than `TestServerMetricsHost_Branches`'s own 100ms sleep. `TestServer_Shutdown_BeforeStart` pins the corrected before-Start contract directly. Both confirmed genuinely discriminating: temporarily reverted the fix and watched both fail (the race reappeared, and `Shutdown` silently returned nil again) before restoring it.
- **Exit, met -- and why this one doesn't need real multi-core hardware to trust, unlike G-13/14/15/T-138:** those needed genuine physical parallelism because their failure modes were about actual scheduling/timing pressure a single-core sandbox might paper over (admission CAS races that only manifest under real concurrent load; a lock-order deadlock needing enough interleaving pressure to actually wedge). This is a different category: a plain unsynchronized-memory-access race, and Go's race detector proves its absence via happens-before tracking (vector clocks over instrumented reads/writes), not by observing whether a failure happened to occur in wall-clock time -- detection doesn't depend on core count, and the fix itself (channel close/receive) is a happens-before edge guaranteed by Go's memory model specification, not an empirically-tuned timing margin. Proven by construction: 5x clean on the exact racing test, 10x clean on both new tests including the zero-delay one, both new tests confirmed to fail without the fix and pass with it, full `pkg/server` suite clean under `-race` after the one conflicting pre-existing test was updated. Closing on sandbox evidence is the correct call here, not a shortcut.

Cross-ref: CHANGELOG 0.24.3.

## [0.24.2] T-139 — pkg/server: Server.Stop() leaked bal rollup and cal manager Pebble file descriptors -- root cause of an unreproducible make test failure (too many open files), confirmed by direct reproduction and fixed (v0.24.2, 2026-08-03)

Theme: server · closed 0.24.2 · 2026-08-03


- **Trigger:** the team ran `make test` locally and hit an unspecified failure once, unable to reproduce, and separately observed pkg/server's own test wall-time growing over recent minor versions. Our own diagnosis, given honestly rather than assumed correct or dismissed: possible state carrying over between tests, differences between test-orchestration tooling allowing interference. Investigated directly: reproduced `make test`'s exact bare invocation (`go test -short ./...`) in-sandbox and hit the same class of failure on the first attempt.
- **Root cause, confirmed by direct reproduction and fix-then-reproduce-again:** `Server.Stop()` never released two long-lived, Pebble-backed caches populated lazily per-tenant during requests -- `s.balRollup` (one `*bal.RollupPebble` per tenant, T-62's own pattern) and `s.calMgr` (`*cal.Manager`, whose own `Close()` releases every tenant's Pebble-backed IndexStore but was never called from `Stop()`). Every test server touching `/bal` or cal leaked that instance's Pebble file descriptors for the lifetime of the TEST BINARY PROCESS, not the individual test -- exactly the same shape `stdTestServer.cleanup()`'s own comment already documents for a DIFFERENT cache (`s.tenantStores`), fixed previously, with this pair simply missed at the time. pkg/server has several hundred `/bal`-touching tests running in one process under the default (unsharded) `go test ./...` invocation; the leak accumulates until this environment's `ulimit -n` (1024) is exceeded, at which point whichever test happens to run next fails with `"...too many open files"` -- generic-looking, and which specific test trips it is order-dependent, explaining "unspecified error, unreproducible."
- **Why the sandbox's own sharded tooling (baseline.py/release.py/regrun.py) never surfaced this:** splitting the suite into shards of 8 packages (separate OS processes, each starting with a fresh FD budget) dilutes the odds of any single process accumulating enough leaked handles to cross 1024 -- it doesn't fix the leak, it just makes it far less likely to manifest. the team's unsharded `make test`/`run_tests.sh` runs the whole tree, and pkg/server's `/bal`-touching tests specifically, in one process, maximally exposed.
- **Fix:** `pkg/server/server.go`, `Server.Stop()`. Added a `s.balRollup.Range()` block (mirroring the existing `s.tenantStores.Range()` pattern immediately above it) closing and deleting each cached `*bal.RollupPebble`, plus an `s.calMgr.Close()` call. `s.balSealer` deliberately left untouched -- checked directly: `chronicle.Sealer` is a pure in-memory struct (mutex + frontier + WindowFn), no Close method, nothing to leak.
- **Verification:** `go test -short ./...` (exact `make test` invocation) run 3x from a cold test cache post-fix -- clean every time, versus the reproduced pre-fix failure. New test `TestServer_Stop_DoesNotLeakBalRollupFileDescriptors` (`pkg/server/server_shutdown_leak_test.go`) pins the fix directly: opens+Stops a `/bal`-touching server 30 times, asserts the process's own open-FD count (via `/dev/fd`, portable across Linux and macOS) doesn't grow with iteration count. Confirmed genuinely discriminating, not just decorative: temporarily reverting the fix made this test fail (16 -> 226 FDs over 30 iterations, ~7 FDs/iteration leaked); restoring the fix makes it pass. A first test design (reopen the same Pebble directory after `Stop()`, expecting a lock-held error if the original handle leaked) was tried and discarded -- empirically it did not discriminate: a same-process reopen succeeded regardless of whether `Close()` had actually run, so it proved nothing. Recorded here so a future session doesn't re-attempt the same design.
- **A separate, smaller robustness gap noticed in passing, not fixed here:** `AdaptiveLock.Stop()` (`pkg/storage/adaptive_lock.go:138`) panics on a second call, unlike `gc.Worker.Stop()` elsewhere in this same `Server.Stop()` method, which is explicitly idempotent by design (guarded by its own `stopped` flag under a mutex, per that code's own comment). Surfaced while an early draft of this item's own regression test called `Stop()` twice by accident (fighting a test helper's auto-registered `t.Cleanup`, not a real double-Stop in production code) -- not a live bug on any known call path, but worth its own item if `AdaptiveLock.Stop()` is ever called from more than one place.
- **Exit:** met. Fix shipped, regression test proven to discriminate in both directions, three clean cold-cache full-suite runs.

Cross-ref: CHANGELOG 0.24.2.

## [0.24.1] T-136 — dxp: intermittent request-completion slowdown under sustained heavy concurrent load -- hypothesis REFUTED by the team's M1 goroutine dump; real cause is T-138's lock-order deadlock (v0.24.1, 2026-08-03)

Theme: dxp/storage · closed 0.24.1 · 2026-08-03


- **Resolution status (2026-08-02):** the exit criterion below has been met -- the team ran the reproducer on the M1 and the resulting goroutine dump identified the actual mechanism: an AB/BA lock-order inversion between bal.Adapter.Execute (Tx held, tenant lock wanted) and bal Reserve/Validate/Transfer (tenant lock held, writer connection wanted). The connection-pool hypothesis below is REFUTED: the writer pool is deliberately MaxOpenConns=1, and no busy_timeout retry storm is involved. Full diagnosis, site map, and fix directions live in T-138; this item closes as subsumed at T-138's own closing release rather than being deleted now, per the closure procedure.
- **Trigger:** found alongside T-135's own two confirmed data-race fixes, 2026-08-02, during the same adversarial /obj testing session. Deliberately filed as its own item rather than folded into T-135, since T-135's own fixes are done/verified and this is a separate, unconfirmed, lower-priority lead.
- **Scope:** 1 of 5 repeated count=5 runs of TestObjAdversarial_EnsureSystemDxpDef_ConcurrentFirstUse (12-way concurrent promote, each a 3-leg bal+entity+obj dxp transaction) hit the server's own 60s request timeout on some requests. Not a deadlock: no hang observed on isolated runs, no incorrect commit, all committed transactions verified correct. Only sustained, repeated heavy concurrent load in the sandbox specifically reproduced the slowdown.
- **Leading hypothesis, unconfirmed:** the shared primary store's own sql.DB connection pool has no explicit MaxOpenConns cap (unlike pkg/storage's own documented "MaxOpenConns=1 (WAL single-writer)" default noted elsewhere in the same file) -- under heavy concurrent write pressure against bal+entity's own shared "sql" engine, Go may open multiple physical connections that then contend for SQLite's own single-writer file lock, causing a busy_timeout retry storm whose cumulative wait time can exceed the request timeout for unlucky requests.
- **Exit:** confirm or rule out the hypothesis via real M1 hardware testing (genuine multi-core, not sandbox-constrained), matching this session's own established discipline for concurrency findings (G-13/G-14/G-15's own real-hardware confirmations). If confirmed, the fix is likely a small, additive MaxOpenConns cap on the primary store's own writer connection -- not attempted here without confirming the actual cause first.

Cross-ref: CHANGELOG 0.24.1.

## [0.24.1] T-138 — dxp/bal: AB/BA lock-order deadlock between bal.Adapter.Execute (holds coordinator Tx on the MaxOpenConns=1 writer pool, wants tenant MemCache lock) and bal Reserve/Validate/Transfer (hold tenant lock, want writer connection) -- proven by goroutine dump from the team's M1; presents as 60s full-tenant stalls rescued only by chi's request timeout; supersedes T-136's refuted pool hypothesis (v0.24.1, 2026-08-03)

Theme: dxp/storage · closed 0.24.1 · 2026-08-03


- **Trigger:** the team's own M1 run of T-136's reproducer (`GOMAXPROCS=8 go test ./pkg/server/ -run TestObjAdversarial_EnsureSystemDxpDef_ConcurrentFirstUse -race -count=20`), 2026-08-02, log supplied. 9 of the first 10 runs failed at ~60.5s (chi's request timeout), 1 passed in 0.75s, and the binary's cumulative 10m alarm then fired mid-run-11, capturing a full goroutine dump — the direct evidence the sandbox never produced.
- **The deadlock, both parties identified by address in one dump:** goroutine 10821 (`dispatchPhased.func1` -> `bal.Adapter.Execute` at `dxp_adapter.go:248`) holds an open `*sql.Tx` on the shared writer pool `0xc000c2c9c0` (proven by its `database/sql.(*Tx).awaitDone` sibling, goroutine 10886, created by `beginDC` in 10821) and is blocked acquiring tenant mutex `0xc0001c6000` via `MemCache.Lock`. Goroutine 10610 (`bal.Adapter.Validate`, past `dxp_adapter.go:198`) holds that same mutex and is blocked in `sql.(*DB).conn` on that same pool (`balanceAndFloor` -> `QueryRowContext`; the writer pool is deliberately `MaxOpenConns=1`). Classic AB/BA lock-order inversion. 8 further goroutines queue at `dxp_adapter.go:198` and 2 at `obj/dxp_adapter.go:207`, all victims behind the tenant mutex; the dispatch's own `wg.Wait` (`v2_dxp_dispatch.go:545`) pins its request too.
- **Why it presents as 60s timeouts, not a permanent hang:** `sql.(*DB).conn` is context-aware and every HTTP request context is bounded by chi's 60s Timeout middleware — Validate's wait aborts at 60s, releases the tenant lock, the wedged Execute proceeds, everything self-heals. Each collision is therefore a ~60s full-stop of ALL dxp and ordinary bal traffic for that tenant, repeating under sustained load. `MemCache.Lock` itself is a bare `sync.Mutex` with no context path — any lock-order victim without a request deadline would wedge permanently.
- **The two lock orders, mapped exhaustively across all five adapters:** Order A (tenant lock held across DB I/O): `bal.Reserve` (145, defer, `balanceAndFloor` on the writer pool), `bal.Validate` (198, same), `bal.Store.Transfer` (~337, defer held across `BeginTx`+`transferInTx`+`Commit` — the tenant lock spans the entire SQL transaction), `obj.Validate` (207, `store.Get` on obj's own pool), `entity.reserveUpdate`/`reserveAppend` (128/183, reads on the READER pool — NumCPU conns, low but nonzero risk), `cal.Reserve`/`cal.Validate` (164/237 — `spanConflicts` under lock, DB involvement needs audit). Order B (Tx/connection held, then tenant lock): `bal.Adapter.Execute` (248) — the ONLY Execute in the codebase that takes the cache lock, reached with the coordinator's per-participant Tx already open in `dispatchPhased` (line 506) and equally under `sharedTx` in `dispatchCollapsed` — the collapsed path is exposed to the identical cycle, this is not phased-only.
- **Provenance — the team's "recent minor versions" suspicion confirmed:** the Order-B half has existed since bal's claim-sum-in-Execute (T-54 wave); the cycle became REACHABLE when dispatch started running adapter Execute with a coordinator-opened Tx concurrent with other requests' Reserve/Validate — dispatchCollapsed/dispatchPhased, v0.20.0 (T-105). Invisible since: the race detector cannot see lock-order inversions (no unsynchronised access anywhere), and the single-vCPU sandbox rarely produces the interleaving (1-in-5 at 0.23.0 vs 9-in-10 on 8 real cores).
- **Relationship to the test-sharding history (T-110/T-111), stated honestly:** 60s convoys inside pkg/server's dxp tests would inflate single-process suite time in exactly the "quiet cumulative slowdown" shape that forced sharding — plausibly a contributor for pkg/server specifically. NOT established as the cause: T-110's graph_path flake had its own confirmed resource-pressure explanation reproduced in isolation, and T-111 triggered on raw test count. Both can be true.
- **T-136 disposition:** its leading hypothesis (unbounded `sql.DB` pool causing a busy_timeout retry storm) is REFUTED — the pool is capped at 1 by design; the mechanism is this inversion. T-136's own exit criterion ("confirm or rule out via real M1 hardware") is thereby met; it closes as subsumed by this item at this item's own closing release.
- **Fix directions (decision needed, not taken unilaterally — guard semantics are involved):** (a) minimal, breaks the cycle: `bal.Execute` stops acquiring the tenant lock — snapshot `srcClaimed`/`dstClaimed` earlier (Reserve/Validate time, stashed in `pending`) or read via a lock-free snapshot; leaves Order-A convoy costs intact but no deadlock. (b) structural: tenant-lock critical sections become memory-only everywhere — no `BeginTx`/`QueryRowContext` under the lock (directly contradicts `bal.Validate`'s own comment demanding balance-read and claims-read under ONE lock to close a TOCTOU; the authoritative guard is arguably `transferInTx`'s guarded UPDATE, making Validate advisory — needs an explicit ruling). (c) both: (a) now for correctness, (b) as follow-up, with a recorded invariant ("tenant-lock sections are memory-only") in KNOWN_ISSUES and, if feasible, a static check. Note `bal.Store.Transfer` holding the lock across its whole SQL transaction is also the very serialisation T-66 records as "masking" chronicle's Append race — fixing (b) may unmask T-66, which must be sequenced accordingly.
- **Decision (2026-08-02, the team, provisional):** (c), sequenced -- (a) shipped this release as a probe, explicitly to be judged on both correctness AND performance before (b) is even scoped; (b) stays unstarted, gated on (a)'s real-hardware numbers and an explicit TOCTOU ruling, and sequenced against T-66 as noted above.
- **(a) implemented and shipped, this release:** `pkg/bal/dxp_adapter.go`. `pending` now stores a `pendingTransfer` (embeds `TransferParams` plus `srcClaimed`/`dstClaimed`) instead of bare `TransferParams`. Reserve computes both sums under its own already-held lock, BEFORE `Hold` inserts the claim -- self excluded with no subtraction needed. Validate's pessimistic path refreshes the snapshot under ITS already-held lock (nesting order `cache -> mu`, matching Reserve), self excluded by explicit subtraction. Execute now touches `a.cache` not at all -- reads the snapshot, full stop. `TestDxpSnapshot_ExecuteTakesNoTenantLock` (`pkg/bal/dxp_snapshot_test.go`) pins this by construction: it holds the tenant lock for Execute's ENTIRE run and asserts Execute still completes -- deadlocks on its own if the acquisition is ever reintroduced, rather than requiring a race detector or multi-core hardware to notice the regression.
- **Sandbox verification, this release (multi-core still owed, see Exit):** full default suite (`scripts/regrun.py`, sharded, matches TESTING.md's own counting convention): 5314 run, 0 fail, 1 skip, 3427 top-level pass (3424 + this release's 3 new snapshot tests, exactly). `pkg/server`'s dxp/bal-relevant tests under `-race`: 64/64 pass. The reproducer itself (`TestObjAdversarial_EnsureSystemDxpDef_ConcurrentFirstUse`) under `-race`, count=10: **10/10 pass, 27s total (~2.7s/run)** -- against the pre-fix log's 9-of-10 failures at 60.5s each. `pkg/bal`, `pkg/dxp`, `pkg/dxp/integration` full suites under `-race`: all green, zero regression.
- **Exit, MET (2026-08-03):** the team ran G-17's canonical invocation on real Apple M1 silicon -- `GOMAXPROCS=8 go test ./pkg/server/ -run TestObjAdversarial_EnsureSystemDxpDef_ConcurrentFirstUse -race -count=20 -v` -- **20/20 PASS, 15.962s total (~0.8s/run), zero 60s-class timeouts, race detector silent.** Against the pre-fix log's 9-of-10 failures at 60.5s each on the same hardware. Both halves of the guard's own pass condition met: correctness (20/20, zero races) AND throughput the team judges acceptable (~0.8s/run, better than even the sandbox's own post-fix number). Bonus same-session evidence: G-13 (bal admission race, -tags stress, count=20) also re-run on the M1, PASS, 48.518s -- no regression to the adjacent admission-CAS path. G-17 (docs/KNOWN_ISSUES.md) updated with the full M1 record. Closing this release, taking T-136 with it.

Cross-ref: CHANGELOG 0.24.1.

## [0.23.0] T-135 — dxp coordinator: two real, confirmed data races found via /obj adversarial stress testing, fixed -- SQLiteStore.dxpClaims and dispatchPhased's own missing ConfirmTxn/ReleaseTxn locks (v0.23.0, 2026-08-03)

Theme: dxp/storage · closed 0.23.0 · 2026-08-03


- **Trigger:** intense adversarial testing of /obj requested directly (not tied to any filed wave-10 exit criterion), 2026-08-02, after wave 10 completed. TestObjAdversarial_EnsureSystemDxpDef_ConcurrentFirstUse (24-way, later 12-way concurrent promote calls racing to bootstrap the same lazily-created system dxp def) immediately surfaced two real, previously-undetected data races in already-shipped, foundational code -- neither specific to /obj, both affecting every primitive's own dxp transactions.
- **Scope, finding 1:** pkg/storage.SQLiteStore.dxpClaims was a plain, unsynchronized *dxp.MemCache field, written on every single dxp dispatch touching entity (NewEntityAdapter -> SetDxpClaims) and read on every ordinary, non-dxp entity/fsm write path (checkDxpEntityHold, FsmWalkInTx). SQLiteStore instances are shared across concurrent HTTP requests (unlike pkg/obj.Store's own per-request wrapper), so this was a genuine, confirmed write-write and write-read race under concurrent dispatch. Fixed: converted to atomic.Pointer[dxp.MemCache], all three access sites (sqlite.go's own write and read, fsm_walk.go's own read) updated to load the pointer once into a local before use.
- **Scope, finding 2:** pkg/dxp.MemCache.ConfirmTxn and ReleaseTxn both carry an explicit doc comment -- "Requires the caller to hold tenant's lock" -- but all six call sites in pkg/server/v2_dxp_dispatch.go's own dispatchPhased were calling them with no lock acquired at all, a systemic bug across the entire phased-dispatch success/failure/torn paths, not one isolated spot. Fixed: each of the six sites wrapped in cache.Lock(tenantKey)/cache.Unlock(tenantKey), verified individually for correct indentation after an initial mistake on the first site was caught and corrected.
- **Verification:** race detector silent on repeated re-runs of the adversarial test after both fixes (previously firing on the very first run). Full pkg/storage regression suite re-run clean. go build/go vet clean across the whole tree.
- **Open lead, NOT root-caused, worth its own follow-up:** intermittent severe request-completion slowdown under repeated heavy concurrent load in the sandbox specifically (1 of 5 repeated count=5 runs at 12-way concurrency hit the server's own 60s request timeout; most runs and all count=1 runs at n=6/n=12 complete correctly in well under a second). Not a deadlock -- confirmed no hang, no incorrect commit, only completion latency under sustained repeated contention. Suspected but not confirmed: sql.DB's own unbounded connection pool against the shared primary store's single SQLite writer file, causing a retry-storm under sustained load (the same class of question raised and left open earlier in this session for /obj's own separate store). Deliberately left for real M1 hardware investigation rather than chased further in the sandbox, which may itself be resource-constrained after this session's own accumulated work.

Cross-ref: CHANGELOG 0.23.0.

## [0.22.5] T-124 — obj API surface completion: capacity/contents/retire endpoints, full XOLU-OBJ error-code reachability sweep (item 50, wave 10) (v0.22.5, 2026-08-02)

Theme: obj · closed 0.22.5 · 2026-08-02


- **Trigger:** wave 10 created 2026-08-01, same pass as T-115.
- **Scope:** obj-02-implementation.md Stage 7; obj-01-rest-api.md. Every remaining endpoint, full error-code coverage, tree-wide completion sweep (section 7.8-shaped: grep for every XOLU-OBJ code named in obj-01 and confirm each is actually reachable, not just documented).
- **Exit (adjusted 2026-08-02 against what was actually built):** every remaining endpoint green except patterns (\u00a74a), deferred deliberately as its own item (T-134) once its real scope became clear against the time remaining -- confirmed directly with the team, not silently built or silently dropped. Retire correctly refuses when contents are non-empty (XOLU-OBJ012), confirmed. Documentation accuracy pass done: obj-01-rest-api.md carries its own reconciliation banner (added 2026-08-02), naming every correction against what shipped, including the patterns deferral, the two real promote/demote wire-contract corrections found under T-121, and one likely-orphaned error code (XOLU-OBJ020) the completion sweep surfaced.

Cross-ref: CHANGELOG 0.22.5.

## [0.22.4] T-123 — obj graph mirroring + events + dxp participant, including the adversarial same-primitive-collision test (item 49, wave 10) (v0.22.4, 2026-08-02)

Theme: obj · closed 0.22.4 · 2026-08-02


- **Trigger:** wave 10 created 2026-08-01, same pass as T-115.
- **Scope:** obj-02-implementation.md Stages 5-6. Graph mirroring reuses bal.Adapter.PostCommit/EmitDeltas's commit-first-authoritative/mirror-second-best-effort shape near-verbatim -- kept as its own stage, not folded into item 46, so item 46's guard structurally never depends on the graph (guard-locality, obj-00-design.md §10). dxp participant is the standard five-verb shape (Reserve/Validate/Execute/Release/PostCommit) every other primitive implements.
- **Exit (adjusted 2026-08-02 -- see own note below):** PostCommit proven through a real dxp dispatch on the phased path, not a unit test alone. "Both collapsed and phased" (this item's own original wording) is not achievable for /obj specifically and not a gap: dxpEngineOf tags /obj "sql-obj" (its own dedicated per-tenant SQLite file, T-119), and EngineHomogeneous checks for the literal string "sql" per participant -- any transaction touching /obj, in any combination, always forces phased, collapsed is structurally unreachable. This constraint post-dates the original exit wording, written before /loc (and its own identical "sql-loc" separate-file precedent) had actually been built and this consequence understood -- corrected here rather than chased as an impossible target. Plus an adversarial test constructing obj's own T-109-shaped risk explicitly (two obj participants of the same primitive colliding in one dxp transaction), not left to "at least one transaction" to happen to cover it.
- Not started.

Cross-ref: CHANGELOG 0.22.4.

## [0.22.3] T-122 — obj journal + rebuild oracle: obj's own derive(journal) == current (item 48, wave 10) (v0.22.3, 2026-08-02)

Theme: obj · closed 0.22.4 · 2026-08-02


- **Trigger:** wave 10 created 2026-08-01, same pass as T-115.
- **Scope:** obj-02-implementation.md Stage 4. Same shape as loc's item 43 (T-117) -- immutable append-only journal of every position/containment change, fold query proving derive(journal) == current.
- **Exit:** confirms agreement on a non-trivial fixture (attach, several moves including at least one promote/demote cycle, one retire). May land as a package-internal check rather than a shipped iolu subcommand -- wave 6 (iolu operations) is still 0% built as of this writing.
- Not started.

Cross-ref: CHANGELOG 0.22.4.

## [0.22.3] T-121 — obj promote/demote: bal decrement + entity create-or-reuse + obj attach/detach as one dxp-dispatched atomic transition (item 47, wave 10) (v0.22.3, 2026-08-02)

Theme: obj · closed 0.22.3 · 2026-08-02


- **Trigger:** wave 10 created 2026-08-01, same pass as T-115.
- **Scope:** obj-02-implementation.md Stage 3; obj-01-rest-api.md §5. Each individual piece (bal decrement, entity create, obj attach) reuses an existing per-primitive operation; the new part is composing three primitives atomically via dxp, which the coordinator already supports generically.
- **Open-question risk, named plainly (obj-00-design.md §13):** whether attachment needs its own edge shape distinct from containment is unresolved. If it resolves before this item is built, promote's own request schema may need a third position kind alongside loc_leaf/obj -- flagged so it is not a surprise, not scope creep if it happens.
- **Progress note, 2026-08-02:** substantial infrastructure landed under this item, not yet closing it -- storage.SQLiteStore.AllocateNodeID (pre-allocates an entity id outside dxp, needed because dxp has no mechanism for one leg's execution result to feed another leg's params, confirmed directly against EntityAdapter.Execute); obj's own dxp.Participant (pkg/obj/dxp_adapter.go: Reserve/Validate/Execute/Release for attach_and_contain and detach), fully wired into the coordinator (dxpPrimitiveOps/dxpEngineOf's new "sql-obj" tag/dxpParticipantRegistry/decodeDxpParticipantParams/objDB threaded through dispatchDxpTxnCore-\>dispatchPhased); createAndDispatchDxpTxn extracted from handleDxpTxnCreate's own inline body so promote/demote can reuse the identical def-lookup+bindings+jsonplate-render+dispatch logic rather than duplicating it (verified zero regression, full existing dxp suite). Remaining: the two parametrized system defs ("obj.promote"/"obj.demote"), the actual POST /obj/promote and /obj/demote handlers, and end-to-end tests.
- **Exit (corrected 2026-08-02 against what was actually built):** full promote->demote round trip green under -race, proving atomicity wherever dxp actually guarantees it for this composition -- the collapsed path, and any refusal caught during Reserve/Validate before Execute runs. Since /obj and bal are separate SQL engines, this always dispatches phased; a refusal discovered only during Execute can leave one leg committed while another is refused (dxp's own "expired" outcome, an accepted risk of the phased path generally, not specific to this item). The original wording claimed this never happens -- proven false directly by the built end-to-end tests. See obj-00-design.md \u00a79's own matching correction.
- Not started.

Cross-ref: CHANGELOG 0.22.3.

## [0.22.2] T-120 — obj containment + cycle safety + multi-dimensional capacity (item 46, wave 10) -- the single highest-risk item across waves 9 and 10 (v0.22.2, 2026-08-02)

Theme: obj · closed 0.22.2 · 2026-08-02


- **Trigger:** wave 10 created 2026-08-01, same pass as T-115.
- **Scope:** obj-02-implementation.md Stage 2. Combines multi-target CAS (same shape as loc's item 41) with a genuinely two-dimensional individual guard (weight AND volume, obj-00-design.md §7) AND a traversal-shaped cycle-safety check with no analog anywhere else in this codebase's guard-bearing write paths. Requires extracting pkg/graph's FlatGraph.wouldCreateCycle (bounded BFS) into a form parameterized over a neighbor-lookup function, callable against obj's own transaction-scoped rows rather than g.nodes directly -- a small, deliberate refactor, decided (shared helper vs. duplicated-with-attribution) before obj's own guard is written, not after. Design-then-race, not TDD, the same instruction cal's hardest stage got.
- **Exit:** stress harness (concurrent cycle-construction from multiple directions; concurrent capacity contention on both dimensions independently) passes locally (sandbox-bounded); a fresh dormant-guard entry exists in docs/KNOWN_ISSUES.md before this item is called done, not after.
- Not started.

Cross-ref: CHANGELOG 0.22.2.

## [0.22.1] T-119 — obj core: package skeleton reusing meta_subject.go's validator, attach/detach/position for non-containment termination kinds, no cycle safety yet (item 45, wave 10) (v0.22.1, 2026-08-02)

Theme: obj · closed 0.22.1 · 2026-08-02


- **Trigger:** wave 10 created 2026-08-01, same pass as T-115, depends on wave 9.
- **Scope:** obj-02-implementation.md Stages 0-1. Reuses pkg/storage/meta_subject.go's namespaced-subject validator directly, not reimplemented. Report handling mirrors loc's own directly -- no new logic, just routing through it. Proves position resolution's two simpler termination cases (loc_leaf, null) end-to-end before cycle safety (item 46) enters the picture.
- **Exit:** full round trip (attach -> move to a loc_leaf -> resolve -> detach) green under -race.
- Not started.

Cross-ref: CHANGELOG 0.22.1.

## [0.22.0] T-132 — Populate warnings: mixed-CRS-anchor (locations §1) and degenerate-polygon (fences §2) (item 56, wave 9b) (v0.22.0, 2026-08-02)

Theme: loc · closed 0.22.0 · 2026-08-02


**Trigger:** the Warnings []string wire field shipped in v0.21.0 but nothing populates it -- both detection cases were named in loc-00-design.md/loc-01-rest-api.md from the start and confirmed still inert by direct code check (pkg/server/v2_loc_handlers.go).
**Scope:** degenerate-polygon detection (zero area or fewer than three effective vertices after simplification) on fence attach/patch, using existing geometry.go helpers -- mechanical. Mixed-CRS-anchor detection on location def/patch needs a real design decision first: the schema has no explicit CRS tag (everything is WGS84 lat/lon), so "mixed real-world reference" has to be a distance/plausibility heuristic against the nearest anchored ancestor, not a lookup -- pin the exact heuristic before coding, not after.
**Exit:** a fence with near-zero polygon area returns a non-empty warnings array, never a hard refusal; a location tree mixing implausibly distant anchors triggers the same, non-fatal.

Cross-ref: CHANGELOG 0.22.0.

## [0.22.0] T-131 — Fence-type patterns: loc_patterns, cloned-child + lineage, XOLU-LOC022 (loc-00-design.md §5d) (item 55, wave 9b) (v0.22.0, 2026-08-02)

Theme: loc · closed 0.22.0 · 2026-08-02


**Trigger:** many fences of a shared kind carry the same capacity default; loc-00-design.md §5d and loc-01-rest-api.md §2a fully spec the mechanism, mirroring fsm/dxp's already-proven definition-snapshot-lineage pattern and obj-01-rest-api.md's XOLU-OBJ013 shape -- not yet built.
**Scope:** loc_patterns definitional table (name, capacity); POST/GET/LIST/DELETE /loc/patterns/...; optional pattern field on fences/attach and locations/def, mutually exclusive with inline capacity (XOLU-LOC022); cloned-child snapshot at creation with a lineage pointer and computed pattern_deleted (not stored, recompute-and-compare per §5c's own precedent); a pattern changing later never retroactively touches already-cloned fences/locations. Sequenced after T-127 for consistent standalone-fence addressing.
**Exit:** a fence or location def'd with a pattern clones that pattern's capacity at creation time; deleting the source pattern leaves existing clones intact with pattern_deleted: true; XOLU-LOC022 refuses both capacity and pattern set together.

Cross-ref: CHANGELOG 0.22.0.

## [0.22.0] T-130 — Fence geometry PATCH + reconcile (loc-00-design.md §5c, loc-01-rest-api.md §2b) (item 54, wave 9b) (v0.22.0, 2026-08-02)

Theme: loc · closed 0.22.0 · 2026-08-02


**Trigger:** fence geometry can change but has no update path today; §5c's own reconciliation mechanism (chronicle.RebuildOracle-shaped) has a full wire spec in loc-01-rest-api.md §2b but no shipped code.
**Scope:** PATCH /loc/fences/{kind}/{key} (same geometry validation as attach, self-intersection rejected XOLU-LOC020, never touches loc_fence_capacity.count or loc_fence_membership directly); GET .../reconcile (read-only, reuses the already-shipped loc_fence_membership reverse index, re-tests each currently-recorded member against current geometry, advisory only). Sequenced after T-127 so both endpoints address fences the same way the corrected identity model will, not the bare-string shortcut.
**Exit:** PATCH updates geometry without touching guard-bearing counts; reconcile correctly reports drift after a synthetic geometry change (a subject inside old geometry, outside new); adversarial test confirming reconcile never writes loc_fence_capacity.count or loc_fence_membership.

Cross-ref: CHANGELOG 0.22.0.

## [0.21.4] T-133 — pkg/bal's stress-tagged dxp cross-path race test failed to build: stale Reserve call, missing participantID param added by T-109 (v0.21.4, 2026-08-02)

Theme: bal · closed 0.21.4 · 2026-08-02


**Trigger:** found 2026-08-02 when the team ran G-13's canonical invocation (`go test -tags stress ./pkg/bal/ -run TestBalAdmission_Race -count=20 -race`) on our M1 for real multi-core confirmation -- the run never got past compilation. Exactly the failure mode docs/KNOWN_ISSUES.md's own dormant-guards intro warns about: a shipped guard that never runs guards nothing, and being stress-tagged, this file is invisible to every normal go build/go test/CI invocation, including this project's own full release pipeline.
**Root cause:** pkg/bal/dxp_cross_path_race_stress_test.go's TestOrdinaryTransfer_RespectsLiveDxpHold_Race called a.Reserve with 6 arguments, missing the participantID string T-109 added to disambiguate multiple same-primitive participants (dxp.Participant.Reserve's own doc comment names T-109 directly). The file was last touched before that interface change and, being stress-tagged, was never rebuilt against it since.
**Scope:** one-line fix (insert "p1" as the missing participantID, matching the working pattern already used identically in pkg/bal/dxp_adapter_test.go's own Reserve calls). Verified this is isolated, not systemic: go build/go vet -tags stress ./... and -tags integration ./... both clean across the whole tree after the fix -- no other bit-rotted build-tagged file found.
**Exit:** builds and vets clean under -tags stress; the test itself passes under -race in sandbox (weak evidence, single-core) -- real multi-core confirmation is the team's to re-run and report, same as G-13 itself.

Cross-ref: CHANGELOG 0.21.4.

## [0.21.3] T-129 — Stage 2 write-path throughput benchmark, number recorded (item 53, wave 9b) (v0.21.3, 2026-08-02)

Theme: loc · closed 0.21.3 · 2026-08-02


**Trigger:** loc-02-implementation.md Stage 2 added a benchmark exit criterion after wave 9 had already started ("a write-path throughput number recorded, however rough, before Stage 2 is called done") -- never met before v0.21.0 shipped.
**Scope:** sustained-load benchmark against the guard-bearing leaf/fence CAS write path only (single-leaf, single-fence, no geometry) -- matches what Stage 2 actually built, not Stage 3's geometry-bearing report path. No production code changes.
**Exit:** a number recorded in loc-02-implementation.md's own Stage 2 section (or CHANGELOG.md), however rough, closing the named gap.

Cross-ref: CHANGELOG 0.21.3.

## [0.21.3] T-128 — Anchor PATCH appends one journal entry, closing loc-00-design.md §5b's residual (item 52, wave 9b) (v0.21.3, 2026-08-02)

Theme: loc · closed 0.21.3 · 2026-08-02


**Trigger:** wave 9b, 2026-08-02. loc-00-design.md §5b names a live gap: a discretely-repositioned anchor (ordinary PATCH, no device, no continuous signal) has no historical record at all, since §8's journal records subject movement only.
**Scope:** location PATCH (pkg/server/v2_loc_handlers.go, pkg/loc/store.go) already permits placement/anchor changes today, silently. Append one loc_journal entry on any anchor field change, mirroring §8's existing discipline for subject moves -- same table, new write path, no new schema.
**Exit:** PATCH changing anchor_lat/lon/alt/true_north produces one journal entry, retrievable via the subject-history read path's own shape; PATCH not touching anchor fields produces none, mirroring §8a's no-op-writes-nothing discipline.

Cross-ref: CHANGELOG 0.21.3.

## [0.21.3] T-127 — Standalone fence identity: wire attach/get/delete through meta_subject.go's (kind,key) resolution; live XOLU-LOC005/006 (item 51, wave 9b) (v0.21.3, 2026-08-02)

Theme: loc · closed 0.21.3 · 2026-08-02


**Trigger:** wave 9b proposed 2026-08-02, reconciling shipped /loc (wave 9, v0.21.0-v0.21.2) against loc-00-design.md/loc-01-rest-api.md's own reconciliation banners -- the entity-composition fence-identity model was named as the headline divergence between what shipped and what's specified.
**Scope:** pkg/storage/meta_subject.go's ParseMetaSubject already ships (v0.16.11, item 7) -- this is not blocked on /meta wiring the way loc-00-design.md's own text worried when it was written. Rewire handleLocFenceAttach/Get/Delete (pkg/server/v2_loc_handlers.go) and DefFence (pkg/loc/store.go) to resolve subject via the (kind,key) validator instead of storing req.Subject as a bare string. Live XOLU-LOC005 (unknown subject) / XOLU-LOC006 (subject already composed) -- both reserved but currently dead. Covers both composition cases named in loc-00-design.md §5: a dedicated place-entity with no position of its own, and an entity that already composes obj.position or a /loc tree-anchor.
**Exit:** fences/attach refuses a nonexistent subject (XOLU-LOC005) and a double-attach (XOLU-LOC006); existing tree-aligned fence tests unaffected; adversarial test for both failure paths.

Cross-ref: CHANGELOG 0.21.3.

## [0.21.2] T-126 — Follow-up audit (T-125's own flagged gap): checked cal, fsm, and entity for the same read-first-key-allocation and missing-duplicate-typed-error bug classes found in loc and bal. cal's own duplicate-check shape looked structurally similar and was tested directly (30x under -race, confirmed safe); fsm and entity are structurally immune by construction (auto-allocated ids only, genuine atomic upserts throughout every call site checked) (v0.21.2, 2026-08-01)

Theme: cal,fsm,entity · closed 0.21.2 · 2026-08-01


**Trigger:** T-125's own closing note named this as a real, not hypothetical, follow-up: the read-first-key-allocation and missing-duplicate-typed-error bug classes were found independently in `loc` and `bal`, two primitives sharing no code, suggesting a possible systemic gap rather than two unrelated coincidences. This item is that audit.

**Method:** for each primitive, locate every caller-facing "define/create" entry point, read its key-allocation SQL directly, and where the shape looked even plausibly safe, test it empirically under real concurrent load rather than conclude safety from reading code alone — the same discipline that caught `loc`'s own two rounds of division-guard bugs (T-125), including the second one where hand-reasoning alone had already been wrong once.

**`cal` — audited, one genuinely different shape, tested directly, confirmed safe.** `CreateCalendar` has an explicit pre-check (`SELECT COUNT(*) ... calendar_id=?`) before `allocOrdinalTx`'s own ordinal allocation — structurally a read-first shape, the same class as `loc`/`bal`'s bug, just serving duplicate-detection rather than key-allocation. `allocOrdinalTx` itself is a genuine atomic `INSERT...ON CONFLICT...RETURNING` upsert, not a separate read. Rather than conclude the intervening atomic write serializes the race away (a real hypothesis, not yet a fact), wrote `TestCreateCalendar_ConcurrentSameID_ExactlyOneSucceeds` and ran it 30 times under `-race` (20 goroutines each, 570 total losing attempts across all runs) with the failure-type check tightened specifically to distinguish "caught by the typed `ErrCalendarExists` pre-check" from "leaked through as a raw, unmapped constraint violation at the final INSERT" — the exact shape a raced pre-check would produce. Zero unmapped errors across all 570. `allocOrdinalTx`'s own atomicity confirmed separately (`TestCreateCalendar_ConcurrentDistinctIDs_NoOrdinalCollision`, 30 concurrent distinct calendar_ids, zero collisions). Conclusion: safe, and now proven rather than assumed — the intervening atomic ordinal-allocation write does force real serialization before any concurrent attempt can reach the vulnerable final INSERT.

**`fsm` — audited, structurally immune, not merely untested.** Both `fsm_definitions` and `fsm_machines` allocate their own `id` via `allocFSMID`, a genuine atomic `INSERT...ON CONFLICT(tenant_id,kind)...DO UPDATE...RETURNING` upsert — the identical safe shape as `cal`'s own `allocOrdinalTx`. Critically, `id` is never caller-supplied for either table (auto-allocated only), and `fsm_definitions.name` carries a plain index, not a UNIQUE constraint (multiple definitions may legitimately share a name) — so there is no caller-named-identity collision surface at all, unlike `loc`/`bal`/`cal`'s external-string-id model. Neither bug class applies by construction, not by luck.

**`entity` (pkg/storage's adapted/document store) — audited across every `adaptedCreate` call site, same conclusion.** All five call sites (plain create, upsert-with-known-id ×3, edge creation) allocate their id via the tenant's own node-sequence table, every one a genuine atomic upsert (`INSERT...ON CONFLICT(entity_type)...DO UPDATE...RETURNING`, or the equivalent `MAX(next_id, excluded.next_id+1)` variant used for explicit-id upserts to never let the counter regress below an already-used id). `nextEdgeID` is the one structural outlier: it upserts the counter first (a genuine write), then reads the value back via a *separate* `SELECT` rather than `RETURNING` in the same statement — noted directly as a minor stylistic inconsistency, not a bug: that read runs *after* the transaction's own write, so it always observes that transaction's own uncommitted change, never a stale cross-transaction snapshot. Entity ids are auto-allocated throughout, the same structural immunity as `fsm`.

**Testing:** 2 new tests in `pkg/cal` (`create_calendar_adversarial_test.go`), run 30× under `-race` for the same-ID case specifically. `pkg/cal` suite green, whole-tree build/vet/`c04dcheck` clean, zero regression.

**Net finding:** the two bug classes discovered in `loc`/`bal` do not recur in `cal`/`fsm`/`entity`. This was not assumed from the absence of a report — each primitive's own real code was read and, where the shape warranted it, tested under adversarial concurrency before reaching that conclusion.

Cross-ref: CHANGELOG 0.21.2.

## [0.21.1] T-125 — Adversarial hardening pass on /loc post-v0.21.0: 5 real bugs found and fixed (concurrent-write race, negative-radius silent uselessness, duplicate-ID 500-not-409, near-pole division corruption caught twice -- once by hand, once by fuzzing -- and GeoJSON hole-dropping), plus the identical concurrent-write-race and duplicate-ID bugs found and fixed in bal (v0.21.1, 2026-08-01)

Theme: loc,bal · closed 0.21.1 · 2026-08-01


**Trigger:** requested adversarial hardening pass on /loc following v0.21.0's release (wave 9 complete), extended to bal once the loc pass surfaced an identical pattern there.

**Scope:** systematic adversarial testing across concurrency, geometry edge cases, tree/invariant attacks, oracle corruption detection, dxp-adapter edge cases, malformed GeoJSON, and RFC 7946 compliance -- not ad-hoc test-writing; each category chosen because it is a class of input real production traffic or a determined adversary could plausibly produce.

**Real bugs found and fixed (loc):**
1. `Def`/`DefFence` used a read-first dense-key allocation (`SELECT MAX(key)+1` as a separate statement before the INSERT) that raced under concurrent load -- the identical WAL read-then-write-upgrade class T-115 already found and fixed in `Move`, never applied to these two functions. Caught live: 30 concurrent `Def` calls produced real `SQLITE_BUSY` failures (9 of 30) on the first run. Fixed: write-first `INSERT...SELECT...RETURNING`, now clean 10/10 under `-race`.
2. Negative-radius circles silently created an unenterable fence -- `Contains` uses `distance <= radius`, never satisfiable for a negative radius, so no error was raised, just a fence nobody could ever enter with no explanation. Fixed: refused at `SetFenceGeometry` write time.
3. A duplicate `location_id`/`fence_id` surfaced as an unwrapped `*sqlite.Error`, falling through `writeLocError`'s default case to a bare 500 instead of a 409. Fixed: typed `DuplicateLocationError`/`DuplicateFenceError` (XOLU-LOC014/015), detected via the stable SQLite result code (2067, not string-matching), mapped correctly.
4. Near-pole division: at `lat=90` exactly, `cos()` does not hit literal `0.0` (float64 representation of `math.Pi`), so an exact-zero guard let a near-zero-denominator division through, producing a "valid" longitude delta of ~1.47e14 degrees for a 1km offset -- silent corruption, not a panic, found duplicated in three call sites (`Circle.BoundingBox`, `nearbyFences`, `ComposeAbsolutePosition`). The first fix attempt (a fixed epsilon on the denominator) was itself wrong: `go test -fuzz` found a counterexample (offset=1000, denom=4.22, well above the threshold) within seconds. Fixed properly: clamp the division's own result to +/-180 degrees, provably correct regardless of the specific values, not a threshold that has to be guessed right. ~440,000 fuzz executions across four targets, zero crashes after the real fix; the failing corpus entry is saved permanently under testdata/fuzz/ and auto-runs on every future `go test`.
5. GeoJSON polygons with holes (RFC 7946 §3.1.6, an explicitly normal, valid structure -- the RFC's own Appendix A.3 shows a worked "with holes" example) were silently accepted with the hole dropped: `coordinates[1:]` was never even inspected. Fixed: explicitly refused with a clear error, not silently ignored.

**Real bugs found and fixed (bal), confirmed present via loc's own tests applied directly, not assumed by analogy:**
6. `DefineAccount` had the identical read-first key-allocation race as loc's `Def`/`DefFence` (bug #1 above). Fixed with the identical write-first pattern.
7. `DefineAccount` had the identical missing-typed-error gap as loc's duplicate-ID case (bug #3 above). Fixed: typed `DuplicateAccountError` (XOLU-BAL007), mapped to 409.

**Testing:** ~50 new tests across pkg/loc, pkg/bal, and pkg/server -- concurrency races under `-race`, geometry adversarial inputs (degenerate/extreme polygons, pole cases), tree/invariant attacks (self-parent, duplicate IDs, a 1000-level-deep tree), oracle corruption-detection proofs (deliberate raw-SQL corruption, confirming each oracle actually detects divergence rather than trivially agreeing with itself), dxp-adapter edge cases (orphan Execute/Validate, double-Reserve, a leaf deleted mid-flight between Reserve and Execute), malformed GeoJSON at the HTTP layer, an RFC 7946-cited compliance battery, and 4 Go native fuzz targets. pkg/loc: 32 -> 79 tests. pkg/server: 1506 -> 1527. pkg/bal: +2 tests. All passing, zero regression, confirmed via `testrun.py`'s real shard runner (not a bare `go test`, which hits this sandbox's known FD ceiling), not a subset.

**Known remaining gap, not fixed in this pass:** `cal` and `fsm`/`entity` were not audited for the same read-first-key-allocation / missing-duplicate-typed-error pattern. Worth a follow-up sweep: the same pattern was found independently in two primitives (`loc`, `bal`) that share no code, suggesting a genuine systemic convention gap rather than two unrelated coincidences.

Cross-ref: CHANGELOG 0.21.1.

## [0.20.6] T-118 — loc events + dxp participant + REST API surface: locParticipant, all 15 endpoints from loc-01-rest-api.md, two-identity regression check (item 44, wave 9) (v0.20.6, 2026-08-01)

Theme: loc · closed 0.20.6 · 2026-08-01


- **Trigger:** wave 9 created 2026-08-01, same pass as T-115.
- **Scope:** loc-02-implementation.md Stages 5-6. locParticipant mirrors ts's dxp adapter (T-86 precedent) against a coordinator that is already live -- lower risk than when the design was first drafted. Client library and iolu CLI are explicit v1 non-goals, matching bal's own T-67 deferral.
- **Stage 5 complete (2026-08-01).** `pkg/loc/dxp_adapter.go`: `Adapter` implementing the full five-method `dxp.Participant` (Reserve/Validate/Execute/Release/PostCommit). `DxpMoveParams` is deliberately narrower than `admission.go`'s own `MoveParams` -- no caller-supplied fence keys (Stage 2's own test hook, superseded by Stage 3's real geometry) -- with the real v1 gap named explicitly in its own doc comment: a dxp-triggered move does not yet auto-resolve tree-aligned fence membership. Admission logic mirrors `cal`'s adapter (slot-shaped mixed-weight rule: pessimistic claims count toward the ceiling, optimistic invisible to arithmetic everywhere), not `bal`'s (balance-shaped) -- checked directly which existing adapter's shape actually matched loc's own counted-capacity guard before writing this, not assumed. `Move` refactored into a thin wrapper plus `moveInTx` (mirrors `bal.Transfer`/`transferInTx`'s own extraction exactly), so Execute can drive the identical guarded core against a coordinator-supplied transaction. Server wiring: `pkg/server/v2_loc_handlers.go` (new file) with `locStore`/`LocStoreForTest`, `locInit`/`locDB` cache fields on `Server`; `loc` wired into `dxpPrimitiveOps`, `dxpEngineOf`, `dxpParticipantRegistry`, `decodeDxpParticipantParams`.
- **A real architectural bug found and fixed, not a coding mistake.** The first version tagged `loc` as `dxpEngineOf["loc"] = "sql"`, the same tag `bal`/`cal`/`fsm`/`entity` share -- but those four genuinely share ONE tenant SQLite file, while loc has its own dedicated per-tenant file (Stage 0's own decision). Tagging loc `"sql"` made `EngineHomogeneous` true for a loc+bal def, which collapsed both onto ONE shared transaction opened against the WRONG database (the primary store, which has no loc tables at all) -- surfaced as `"no such table: loc_capacity"` on the very first end-to-end test run, not caught by any unit test in isolation (Reserve alone never touches a coordinator-supplied store, so this was invisible until a real multi-participant dispatch ran). Root-caused via direct instrumentation (traced `*sql.DB` pointers and `sqlite_master` state at every step) rather than guessed at. Fixed the same way `ts` already solved the identical shape of problem: a distinct engine tag (`"sql-loc"`) that can never equal literal `"sql"`, which `EngineHomogeneous`'s own check (`!= "sql"`) is already built to key off of -- forces the phased path whenever loc participates, exactly like `ts`'s own `"pebble"` tag does. `locDB` threaded through `dispatchDxpTxn` -> `dispatchDxpTxnCore` -> `dispatchPhased`, mirroring `pebbleDB`'s own existing threading pattern precisely, plus a genuine new `"sql-loc"` case in `dispatchPhased`'s per-participant engine switch. Two pre-existing direct callers of `dispatchDxpTxnCore` in `v2_dxp_dispatch_core_test.go` updated for the new parameter via a guarded Python substitution (2 occurrences, both confirmed), not `sed`.
- **Testing:** 9 standalone adapter tests (`pkg/loc/dxp_adapter_test.go` -- Reserve success/unknown-location/at-capacity/optimistic-coexistence, Validate, Execute read-back-after-commit, Execute aborted-tx-leaves-no-trace [this stage's own version of the T-86 aborted-batch proof], wrong-store-type refusal, Release idempotency) plus Stage 5's own actual exit criterion: `pkg/server/v2_dxp_loc_test.go`, two real end-to-end tests dispatched through the genuine HTTP API and the real coordinator (`dispatchDxpTxn`), not `pkg/dxp/integration`'s own hand-wired doubles -- `TestDxpTxnAPI_LocAndBal_BothCommit` (both legs verified independently through their own real stores, mirroring the hotel test's own discipline) and `TestDxpTxnAPI_LocAndBal_LocRefusalReleasesBalClaim` (loc's refusal correctly releases bal's already-held claim, the same attendance property the hotel test's own overlap tests prove for cal). All pass. Full `pkg/server` suite re-run through `testrun.py`'s real shard runner (not a bare `go test`, which hits this sandbox's known FD ceiling) after the fix: 1498 pass, 0 fail -- up from 1496 before this session's changes, confirming zero regression across every other primitive's own dxp wiring. Whole-tree `go build`/`go vet`/`c04dcheck` (both `pkg/loc` and `pkg/server`) clean; raw-map lint still returns nothing.
- **Stage 6 complete (2026-08-01).** All 15 endpoints (`pkg/server/v2_loc_handlers.go`, new file): location CRUD (def/list/get/patch/delete, with the `XOLU-LOC012` occupied-descendant check -- a real recursive-CTE tree walk added this stage, not the direct-children-only check Stage 1/2 explicitly deferred), position (move/report/subject position/history), fences (attach/list/get/delete), containment reads (contains/nearby). Routes registered unconditionally in the main router, matching dxp's own unconditional wiring and Stage 0's finding that nothing warrants loc being independently optional. Five new `pkg/loc` methods added for the read side (`FenceIDsFor`, `CurrentFenceKeys`, `SubjectPosition`, `SubjectHistory`, `Nearby`), plus `TreeAlignedFenceDelta` exported. `loc_journal` gained `report_lat/lon/alt` columns -- Stage 5's `Report` never persisted the actual reported coordinate anywhere at all, a real gap (needed for `SubjectPosition`'s `last_report_point`) closed here, not carried forward silently. 19 string-prefixed errors retrofitted into 9 typed error types (`pkg/loc/errors.go`) so `writeLocError` can use `errors.As`-based status mapping matching `bal`'s own convention, not string-prefix parsing.
- **v1 scope narrowings, named plainly, not silent deviations:** standalone fences keep this package's own bare `fence_id` identity rather than `loc-01-rest-api.md`'s revised entity-subject composition (needs `/meta` wiring beyond this stage); self-anchored fences (`geometry.center.self=true`) are rejected outright at the handler with an explanatory 400, since they depend on `/obj`, wave 10, not built yet.
- **Tree-aligned fence support built for real, closing Stage 5's own named gap.** `fences` gained `aligned_location_key`; `Move` auto-derives entered/exited fence membership via a genuine ancestor-chain walk (recursive CTE) whenever the caller supplies no explicit fence keys -- proven against a leaf several levels *beneath* the aligned location, not just an exact-match case, and proven not to break any of Stage 2's own tests that rely on explicit fence keys as a guard-testing hook (a dedicated bypass test, not just assumed safe).
- **A real bug caught by the HTTP tests themselves, not found by inspection.** `chi.URLParam` returns the raw, still-`%2F`-encoded form for a slash-containing path segment -- `location_id` is explicitly path-structured by design (`loc-01-rest-api.md`'s own examples: `"site-mvd/bldg-a/floor-3/room-204"`), so three tests failed on the first run with a bogus `XOLU-LOC003` 404 before a `pathParam` helper (URL-decode before use) fixed it. Load-bearing for real callers, not a test-only workaround.
- **Two-identity regression check implemented rigorously, not as a fuzzy string search.** The first version searched response bodies for the internal key's digit sequence as a substring and only logged a warning on a hit -- correctly recognised as too weak (a capacity value or offset could coincidentally match) and replaced before commit: `TestLocAPI_TwoIdentitySplit_InternalKeyNeverOnWire` reads the actual internal `LocationKey` via `LocStoreForTest`, confirms it's structurally distinct from the external id, then asserts every `location_id`/`parent_id` field in every response is the exact external string, never a bare number.
- **Testing:** 8 HTTP-level tests (`pkg/server/v2_loc_handlers_http_test.go`) covering the full location lifecycle, root-without-anchor refusal, move + position + history, move capacity refusal, report lifecycle including the `changed:false` no-op case exposed through the real API, containment reads, the self-anchored-fence rejection, and the two-identity check. All pass. Full `pkg/server` suite re-run through `testrun.py` after Stage 6's changes: **1506 pass, 0 fail** (up from 1498 after Stage 5 -- the 8 new tests, zero regression elsewhere). Whole-tree `go build`/`go vet`/`c04dcheck` clean.
- **Wave 9 complete.** All four items (T-115/T-116/T-117/T-118) shipped and closed the same day, each with at least one real bug caught by its own tests rather than found later: T-115's read-first `Move` (WAL snapshot invalidation), T-116's one-directional adjacency check (`SelfIntersects` flagging every valid polygon), T-117's unconditional `Report` journal write (violating §8a), and this item's `%2F` path-decoding bug.

Cross-ref: CHANGELOG 0.20.6.

## [0.20.6] T-117 — loc journal + rebuild oracle: derive(journal) == current proof, iolu db check hook point (item 43, wave 9) (v0.20.6, 2026-08-01)

Theme: loc · closed 0.20.6 · 2026-08-01


- **Trigger:** wave 9 created 2026-08-01, same pass as T-115.
- **Scope:** loc-02-implementation.md Stage 4. Near-direct copy of bal's own §8 fold-query shape (loc-02's own Principles section names this as the spine). Thinner than bal's verification story by design, stated rather than silent: loc has no local-chain check equivalent to bal's previous_balance+amount=current_balance, since a move's journal entry carries no running total.
- **Complete (2026-08-01).** `pkg/loc/verify.go`: four `chronicle.RebuildOracle` instances (`AssignmentFoldOracle`, `OccupancyFoldOracle`, matching the plan's own two named SQL targets exactly; `FenceMembershipFoldOracle`/`FenceOccupancyFoldOracle`, extending the identical discipline to Stage 3's fence-membership state, which the plan's own SQL snippets didn't name explicitly but which deserves the same rigor as leaf state, not a thinner story for fences than for leaves). Fence membership required a genuinely different fold shape from leaf assignment -- not "last value wins" (a subject enters/exits the SAME fence repeatedly), a real net +1/-1 delta fold per (subject_ref, fence_key) unnested from each journal row's JSON arrays via SQLite's `json_each` (confirmed working against the pinned `modernc.org/sqlite` version directly before relying on it, not assumed from general SQLite documentation). `Oracles()` is the `iolu db check` hook point named in Stage 4's own goal -- the hook itself; iolu (wave 6) is still 0% built, so no CLI surface, per Stage 0's own scope decision.
- **A real spec violation caught and fixed, not found by inspection.** Re-reading Stage 4's own text against Stage 3's already-shipped `Report` surfaced that `Report` wrote a journal row unconditionally, violating §8a's explicit rule: "a report producing no containment change writes nothing... no event, no ts record." Fixed: the membership delta is now computed before any transaction opens, and an empty delta returns immediately, no write of any kind -- proven by `TestReport_NoOpWritesNothing`, not just asserted by the fix.
- **Testing:** `TestOracles_AgreeFromEmpty` (§8c's own stated acceptance criterion, proven from empty, not assumed); `TestOracles_AgreeAfterBasicSequence`; `TestReport_NoOpWritesNothing`; `TestOracles_RandomizedSequenceWithRefusals` -- Stage 4's own named requirement, 300 randomised move/report steps across 6 subjects and 4 tight-ceiling leaves, 78 real refusals actually exercised (checked, not just hoped for -- the test fails outright if refusals never fire), each refusal's pre/post oracle fingerprint compared directly to prove no trace was left, all four oracles agreeing after the full sequence. All pass; whole-tree `go build`/`go vet`/`c04dcheck` clean; raw-map lint still returns nothing.
- Stage 5 (events, ts emission, dxp participant adoption, T-118's own scope): not started.

Cross-ref: CHANGELOG 0.20.6.

## [0.20.6] T-115 — loc core: package skeleton, containment tree + placement-chain composition, assignment/capacity/move CAS with multi-target atomicity (item 41, wave 9) -- the guard-bearing core, highest-risk item in wave 9 (v0.20.6, 2026-08-01)

Theme: loc · closed 0.20.6 · 2026-08-01


- **Trigger:** wave 9 created 2026-08-01, per direct instruction, following an effort-estimate pass against loc-00/01/02-design.md.
- **Scope:** loc-02-implementation.md Stages 0-2. Mirrors bal's CAS-predicate discipline and file layout directly; multi-target atomicity (one CAS sequence spanning a destination leaf and N fences) is the one place this guard is structurally more complex than bal's own single-account guard -- design-then-race, not TDD, per the same caution cal's hardest stage got.
- **Exit:** per SUBSTRATE_DEVELOPMENT_PLAN.md's wave 9 entry -- multi-target atomicity race harness registered as a dormant guard (sandbox pass only, multi-core confirmation is the team's); a write-path throughput number recorded, however rough.
- **Stage 0 complete (2026-08-01):** `pkg/loc/doc.go` (package doc, the two-write-path distinction checked by direct diff against loc-01-rest-api.md §0, one dropped clause caught and restored) and `pkg/loc/model.go` (`Placement`, `GeoAnchor`, the `Geometry` interface, `Circle`/`Polygon`/`Point` type declarations -- methods land in Stage 3, T-116) written. `storelayout.TenantLocDir` and 11 `XOLU-LOC` error constants (`pkg/errors`) added, pinning Stage 0's storage-location and error-taxonomy decisions as code. All four exit criteria verified: whole-tree `go build`/`go vet` clean; the raw-map lint check (`pkg/loc/*.go`, outside test files) returns nothing -- caught and fixed against its own doc comments, which literally contained the searched-for string as prose, not as a violation; `c04dcheck` clean.
- **Stage 1 complete (2026-08-01):** `pkg/loc/model.go` gained `LocationKey`/`Location`/`LocationDef`. `pkg/loc/store.go`: `Store`/`NewStore`/`Init` (bare, unprefixed table names -- loc gets its own dedicated per-tenant SQLite file, unlike bal's shared-store prefixing) and the structural CRUD (`Def`/`Get`/`List`/`Patch`/`Delete`), mirroring `bal.DefineAccount`'s dense-key-allocation shape directly. `XOLU-LOC010` (root without anchor) enforced at `Def`/`Patch` time -- the typed `GeoAnchor` (all four fields or none) makes the SQL design's "any anchor_* column NULL" rule collapse to a simpler "Anchor is nil" check at the Go level, a stricter invariant by construction, not a weaker one. `Delete`'s occupied-descendant refusal (XOLU-LOC012) is explicitly not reachable yet (no assignment table until Stage 2) and is documented as such in the code, not silently absent. `pkg/loc/placement.go`: `ComposeAbsolutePosition`, the placement-chain composition walk -- root-to-leaf transform composition, converted to WGS84 via the flat-Earth approximation loc-00-design.md §4e already accepts at this scale. A real bug (string comparison against an error message instead of `errors.Is`) caught and fixed before the first build, not after.
- **Testing:** `pkg/loc/store_test.go` -- full CRUD round-trip; `XOLU-LOC013`/force-cascade-empty-only; `XOLU-LOC010` refusal; the placement-chain composition table-driven test at 1/2/4 hops, hand-computed by hand *and* verified against the actual test run (all pass) rather than assumed correct from the derivation alone; a transform-invertibility round-trip test as the practical form of loc-00-design.md §10's "top-down vs. bottom-up-then-invert agree" property -- noted honestly in the test's own comment that a second, independently-implemented bottom-up algorithm is not what this covers, only algebraic invertibility of the one implementation that exists. All tests pass; whole-tree `go build`/`go vet`/`c04dcheck` clean.
- **Stage 2 complete (2026-08-01) — item 41's own highest-risk piece.** `pkg/loc/admission.go`: `CapacityError`/`InvariantError` typed errors (mirroring `bal.BoundsError`'s shape for race-test discrimination), the four CAS predicates (leaf/fence entry/exit, `bal`'s §6 pattern applied to admission), and `Move` — multi-target atomicity across one destination leaf and every entered/exited fence in one transaction, first zero-rows CAS rolling back everything, proven directly (`TestMultiTargetAtomicity`) not just asserted by design. `store.go`/`model.go` extended: `loc_capacity`, `fences` (bare identity — real geometry is Stage 3, T-116), `loc_fence_capacity`, `loc_assignment`, `loc_journal` tables; `Def` now pairs every location with a `loc_capacity` row in the same transaction (mirroring `bal.DefineAccount`'s balances-row pattern); `DefFence`; `PatchParams` gained `Ceiling` (`XOLU-LOC011` on a non-postable node).
- **A real concurrency bug found and fixed, not just a clean pass reported.** The first version of `Move` resolved `location_id` via a preceding `SELECT` before the guarded `UPDATE` — read-first. The first sandbox race run (`TestLocAdmission_Race`, 32-way contention) reproduced `bal`'s own historical G-13 failure mode exactly: WAL snapshot invalidation, `SQLITE_BUSY` past the busy handler, most claimants failing with an unexpected error rather than a clean win/refusal split. Fixed the same way `bal.Transfer` was — `Move` is now WRITE-FIRST, the leaf entry CAS (location_id resolved via subquery, `RETURNING location_key`) as the transaction's opening statement, with failure-path diagnosis (unknown location vs. at capacity) added only after, mirroring `bal.diagnoseRefusal` directly. Re-run clean, 5x under `-race`, both `TestLocAdmission_Race` and `TestLocAdmission_Race_MultiTarget`.
- **Dormant guard G-14 registered the same session it was written** (`docs/KNOWN_ISSUES.md`), per house discipline — sandbox pass recorded (2026-08-01, single-CPU, `-race`, count=5, 32 claimants, both race tests), multi-core confirmation flagged as owed to the team, not claimed as done.
- **Write-path throughput recorded** (Stage 2's own newly-added exit criterion): `BenchmarkMove`, single-leaf CAS, no fences — ~1.1ms/op, ~900 ops/sec, single-core sandbox, rough by design (a real number for planning purposes, not a production capacity claim).
- **Testing:** `TestLeafCapacityCAS`, `TestFenceCapacityCAS` (single-threaded CAS correctness, both boundaries); `TestMultiTargetAtomicity` (the rollback proof: leaf CAS succeeds, fence CAS fails, leaf count and `loc_assignment` both confirmed unchanged after rollback); `TestMoveExitsAndEntersLeaf` (leaf-to-leaf, exactly one journal row per move). All pass; whole-tree `go build`/`go vet`/`c04dcheck` clean; the Stage 0 raw-map lint check still returns nothing.
- Stage 3 (real geometry, wiring Stage 2's fence-membership test hook to actual `Contains` tests — T-116's own scope): not started.

Cross-ref: CHANGELOG 0.20.6.

## [0.20.6] T-116 — loc geometry: Circle/Polygon, ray-casting containment, R-tree/Geopoly SQL-plane pre-filter wired into the real fence-membership test (item 42, wave 9) (v0.20.6, 2026-08-01)

Theme: loc · closed 0.20.6 · 2026-08-01


- **Trigger:** wave 9 created 2026-08-01, same pass as T-115.
- **Scope:** loc-02-implementation.md Stage 3. R-tree/Geopoly already confirmed compiled into modernc.org/sqlite v1.29.0 empirically (loc-00-design.md §6b) -- no re-verification owed. Genuinely new work: ray-casting containment (concave polygons, not just convex), self-intersection rejection at write time, the axis-aligned-rectangle fast path.
- **Complete (2026-08-01).** `pkg/loc/geometry.go`: `Circle`/`Polygon` now genuinely satisfy the `Geometry` interface Stage 0 only declared -- `Contains` (ray-casting, even-odd rule, with the axis-aligned-rectangle O(1) fast path checked first per §4c), `Distance` (haversine for `Circle`, point-to-segment for `Polygon`), `BoundingBox`, `SelfIntersects` (orientation-based segment-crossing test), `DecodeGeoJSONPolygon` (RFC 7946, [lon,lat] order handled explicitly against the package's own [Lat,Lon] `Point` -- named as a deliberate friction point, not assumed safe by convention). SQL wiring: `fences` extended with real geometry columns (kind/circle fields/polygon vertices-as-JSON/bounding box), a genuine `USING rtree(...)` virtual table as the pre-filter (not a fallback BETWEEN-query stand-in), `SetFenceGeometry`, `ResolveFenceMembership` (bounding-box candidates, then exact `Contains` -- never the box alone), and `Report` -- the second write path (`loc-01-rest-api.md` §0), resolving fence membership only, `loc_assignment` never touched, proven directly by `TestReport_EndToEnd`, not just designed that way.
- **Two real bugs found and fixed by the tests themselves, not caught by inspection.** (1) `SelfIntersects`'s adjacency-skip only handled the wrap-around case (last edge vs. first), not the ordinary case of two consecutive middle edges sharing a vertex -- `TestPolygon_SelfIntersectionRejected`'s own plain-rectangle case failed on first run, flagging every valid simple polygon as self-intersecting. (2) A real design risk was tested for directly, not just assumed handled: `TestResolveFenceMembership_PrefilterNotTrustedAlone` checks a point inside a circle fence's square bounding box but outside the actual circle -- exactly the failure mode §7b's guard-locality rule warns against (trusting the pre-filter's cached box instead of the exact test). It passed, confirming the two-stage design holds, but the point is that this was checked, not presumed from the architecture being correct on paper.
- **Testing:** `geometry_test.go` -- axis-aligned rectangle, a hand-verified triangle, a concave L-shape (Stage 3's own named requirement: correct via ray-casting, not just convex), self-intersection (bowtie flagged, simple shapes and triangles not), a non-finite-input defensive test (no panic, even though Stage 0's decode discipline should make this unreachable in practice). `geometry_store_test.go` -- self-intersection refused at `SetFenceGeometry` time, the pre-filter-not-trusted-alone proof above, polygon membership resolution, `Report`'s end-to-end enter/exit/journal behaviour, and fence-capacity refusal through `Report` specifically (not just `Move`). All pass; whole-tree `go build`/`go vet`/`c04dcheck` clean; raw-map lint still returns nothing.
- Stage 4 (journal + rebuild oracle, T-117's own scope): not started.

Cross-ref: CHANGELOG 0.20.6.

## [0.20.5] T-113 — Tenant ID -> uint32 widening (item 8, wave 1): 218 call sites, 2 codec sites, dir format, fixtures -- never filed despite the plan's hard production-deploy gate on this wave (v0.20.5, 2026-08-01)

Theme: tenant · closed 0.20.5 · 2026-08-01


- **Trigger:** discovered 2026-08-01 while auditing wave-by-wave completion
  for `docs/SUBSTRATE_TRACKING.md`. Found no register entry, open or
  closed, for plan item 8 -- unlike its sibling item 9 (sysmask, closed as
  T-43/T-44). Confirmed directly against code, not inferred from the
  register's silence: `pkg/tenant/tenant.go` still declares `type
  TenantID uint16`. Confirmed against history: zero matches for
  "item 8" anywhere in `CHANGELOG.md`, across 39 other plan items that
  are each tagged at least once.
- **Scope, per the plan (wave 1, item 8):** `/ts`'s codec widens its
  tenant-key prefix from 2 bytes to 4, so per-tenant `TimelineID`
  becomes `uint32`. `/cal` gets an audit for an analogous cap and
  either widens or documents "no cap today". `/bal`'s ID width was
  chosen at its own implementation time (`uint32` default) and may
  already satisfy this independently -- not re-verified here, worth
  checking before scoping the remaining work. The plan's own effort
  estimate: 218 call sites, 2 codec sites, directory format, fixtures --
  3 ideal days.
- **Why this matters now, not just for hygiene:** the plan states
  *"Hard gate: no production deployment before this wave completes."*
  Waves 3, 4, and 5 have since shipped in full without item 8 landing,
  so that gate has been bypassed in practice, not formally waived. This
  item's resolution is either: complete the widening, or have the team
  make an explicit, recorded decision to waive/defer the gate (e.g. if
  `uint16`'s ~65k per-tenant-primitive ceiling is judged acceptable for
  the deployment horizon actually planned).
- **RETRACTED 2026-08-01, same day, before any code touched.** This
  item was filed against the wrong field. The plan's own §1 rationale
  (not read carefully enough before filing) is explicit: *"Cross-tenant
  scaling is not here: tenant ID stays uint16... the answer if it ever
  did [overflow] would be a second xolu, not a wider ID."* Item 8 widens
  `/ts`'s `TimelineID` -- the per-tenant object-count ceiling (SKUs,
  rooms, patients within one tenant) -- not the tenant identifier this
  item's own title named. `pkg/tenant.TenantID` (checked when filing)
  was never item 8's target and correctly remains `uint16` by design.
  `pkg/timeseries.TimelineID` (the actual target, unchecked when filing)
  is already `uint32`: `codec.go`'s own comment states *"TimelineID
  prefix widened from 2 to 4 bytes in wave 1 (@P)"*; a dedicated
  regression test (`codec_widening_test.go`) probes values above the
  old `0xFFFF` ceiling; `CHANGELOG.md`'s v0.16.3 entry ships items 8 and
  9 together under "per-primitive ID widening + sysmask mechanism,"
  stating the identical rationale. **Item 8 was already done. Wave 1 is
  2/2, complete.** Closing as filed-in-error, not as resolved work --
  the register's own discipline (Part 3 §3) doesn't distinguish the two
  mechanically, so this note is the distinction.
- Not started.

Cross-ref: CHANGELOG 0.20.5.

## [0.20.5] T-111 — pkg/server's own single-package test binary now exceeds the sandbox's single-process capacity on its own -- release.py's shard-by-package-count sharding does NOT insulate against this (T-110's own reassurance was wrong); a manual 10-way -run split was needed to cut 0.20.1, no permanent fix built (v0.20.5, 2026-08-01)

Theme: testing-infra · closed 0.20.5 · 2026-08-01


- **Trigger:** direct instruction to cut the 0.20.1 release; the release-gate's own sharded test run hit this for the first time, contradicting T-110's own (now-closed) reassurance that sharding alone would insulate a formal release from the cumulative single-process artifact.
- **What happened:** `scripts/release.py 0.20.1 --short --shard-size 8` ran cleanly through `test-shard-00` through `test-shard-02`, then `test-shard-03-of-6` (8 packages, including `pkg/server`) failed with the exact same `TestGraphPath*`/`TestGraphShortestPath*` symptom class T-98/T-110 already characterised — 11 tests failing with `XOLU-ST006 Failed to initialise tenant context`, all passing cleanly in isolation. A retry of `--resume` reproduced the identical failure deterministically (not flaky/order-sensitive at this volume).
- **Why T-110's own reassurance was wrong:** `go test pkgA pkgB pkgC` compiles and runs one independent test binary (one OS process) per package regardless of how many packages share one `go test` invocation or one shard. Sharding by package count therefore does nothing to insulate `pkg/server` specifically — its own single-package test binary, alone, in one process, now carries enough cumulative `httptest.Server` volume (869 top-level test functions after T-107–T-110) to trip the same TIME_WAIT/socket-table pressure T-98 diagnosed for the whole-tree case. Confirmed directly: `go test ./pkg/server/...` alone, no other packages involved, reproduces it every time at this session's sandbox.
- **Workaround used to cut 0.20.1, not a permanent fix:** `pkg/server`'s 869 top-level test functions were partitioned into 10 groups of ~87 (via `-run` alternation), each run as its own `go test` process/coverprofile, plus the other 7 packages from shard 03 run together separately; all 11 outputs merged with `testrun.merge` into `.test-shard-03.json`/`cover.03.out`, journaled green, and `release.py --resume` picked it up from there. All 3022 tests in the reconstructed shard passed, 0 failures — this was a real, complete run, not a bypass of coverage or correctness, just split across processes.
- **What remains open, named plainly:** `scripts/testrun.py`'s `run_shard`/`chunk` have no concept of splitting *within* one large package's own test binary — only package-level grouping. As `pkg/server` keeps growing, this manual per-release workaround will be needed again, and will need re-deriving (or re-running) each time, unless `testrun.py` gains a per-package `-run` split for packages above some test-count threshold (or `pkg/server`'s own tests get restructured to share fewer `httptest.Server` lifecycles — T-110's own §5.3 option (a), previously judged not obviously worth the cost, worth reconsidering now that it has to be paid on every release rather than a hypothetical).
- **Not yet done:** no code or tooling change made this session beyond the one-off manual split described above. `docs/KNOWN_ISSUES.md`'s existing note on the cumulative sandbox-capacity finding (filed under T-110, now in `RESOLVED.md`) should be cross-referenced from wherever this item lands, since it's the same underlying symptom, now confirmed to reach further than T-110 assumed.

Cross-ref: CHANGELOG 0.20.5.

## [0.20.3] T-112 — dxp client library (item 23, wave 5): six methods against dxp/def and dxp/txn, mirroring bal.go/cal.go's established shape -- def-as-tool surface for molu itself deliberately out of scope (v0.20.3, 2026-07-31)

Theme: dxp · closed 0.20.3 · 2026-07-31


- **Trigger:** direct instruction to complete wave 5 -- item 23 (dxp client + def-as-tool surface for molu) was the one remaining piece with zero existing code (`find pkg/client -iname "*dxp*"` returned nothing, checked directly before starting, not assumed).
- **Scope, plan item 23:** the dxp client library half of the item. The def-as-tool surface for molu itself is deliberately NOT built here -- molu's own tool-registration convention (how a def's participants/bindings_schema become a callable tool description) is molu's concern, not xolu's client library's; `DxpDefGet` already returns everything a molu-side adapter would need (spec, analysis, bindings_schema) to build one, so nothing here forecloses it.
- **Built:** `pkg/client/dxp.go` + `types_dxp.go`, six methods against the six existing dxp/def and dxp/txn endpoints (`DxpDefCreate`, `DxpDefList`, `DxpDefGet`, `DxpTxnCreate`, `DxpTxnList`, `DxpTxnGet`), mirroring bal.go/cal.go's established shape exactly -- checked directly against `pkg/server/v2_dxp_def_handlers.go` and `v2_dxp_read_handlers.go`'s actual response shapes before writing any type, not assumed from the doctrine's worked examples. Wire types split cleanly where the server's own def-create vs def-get responses differ (Spec/BindingsSchema populated by Get only) and txn-create vs txn-get responses differ (DefName/DeadlineNs populated by Get only) -- one field being empty on one call's response and populated on another's is documented in each type's own doc comment, not left for a caller to discover by surprise.
- **A real distinction the client surface makes explicit:** `DxpTxnCreate` returning a non-committed status (`released` or `expired`) is NOT an error -- it is a normal 201 response with `Status` and `Reason` set accordingly, matching the server's own doctrine that a refused instance and a failed request are different things. `TestDxpTxnCreateReleasedIsNotAnError` and the integration test's own refused-instance case both prove this holds, not just document it.
- **Verified:** 20 mock-based unit tests (happy path, structured error via each method's relevant XOLU-DXP00x code, client-side validation) across all six methods. One real-server integration test (`TestIntegration_DxpFullFlow`, `-tags integration`): register a def, instantiate it, confirm via `BalBalance` -- a completely separate primitive's own read path -- that the dxp-driven transfer actually landed, round-trip `DxpDefList`/`DxpDefGet` and `DxpTxnList`/`DxpTxnGet` against what was just created, then instantiate again with an amount that genuinely exceeds `~in`'s floor and confirm the response comes back `released` with a reason and the balance is unchanged, not a client error. Full `pkg/client` suite (mock + integration, `-race`) and full tree `go build`/`go vet` green.

Cross-ref: CHANGELOG 0.20.3.

## [0.20.3] T-85 — dxp coordinator design complete (item 21, wave 5): ParticipantStore abstraction, attendance protocol, no-durability decision with two independent justifications, canonical doctrine verified and corrected -- full detail in docs/proposals/dxp-coordinator-design.md (v0.20.3, 2026-07-31)

Theme: dxp · closed 0.20.3 · 2026-07-31


- **Trigger:** direct instruction (2026-07-29) -- the coordinator (item 21) design worked out across an extended session had grown substantial enough that leaving it in conversation history rather than a real document was itself a risk. Full design now in docs/proposals/dxp-coordinator-design.md; this entry is a pointer and summary, not a duplicate of it.
- **Scope, item 21 rescoped (2026-07-29 deviation, already recorded in TRACKING.md's Plan deviations and SUBSTRATE_DEVELOPMENT_PLAN.md):** 3PS as the sole execution model, 2PS dropped to pending/deferred. This item covers everything needed to make that real: the ParticipantStore/SQLStore/PebbleStore abstraction replacing Execute's hardcoded *sql.Tx, the attendance protocol (verified to be exactly plain 3PS's own formal definition per the canonical framework's dxp-11-proof-3ps.md, not an extension of it), Ready()-triggered per-participant guards with the coordinator owning all timing, concurrent commit dispatch (verified safe for both the collapsed and non-collapsed cases, and better than sequential -- shrinks the torn-commit window from a sum to a max), and Result capture (Execute returning an opaque JSON value, two independent future consumers -- dependency-binding and webhook/log delivery -- neither built yet, only capture is in scope now).
- **A real, load-bearing decision, not a detail:** no durable transactions across instance restarts (direct instruction, "we may never do that"). Justified two independent ways, both actually worked through rather than assumed: mid-execute crashes are expected to be extremely rare (with concrete evidence for why true resume would be expensive if it were needed -- bal's own shipped transfer_id has no uniqueness constraint or dedup check today, confirmed directly, so re-execution would silently double-apply); and hotswapping is a separate, cooperative, nolu-owned mechanism (redirect + await confirmation, not an instant kill), not a dxp durability requirement. Consequence: idempotency is not needed anywhere in this design, since nothing is ever resumed after a crash. Torn instances need no new terminal state -- they fall into the existing item-18/T-54 expire-and-sweep machinery unchanged.
- **Canonical doctrine actually verified, not cited from memory -- corrections made and fixed at the source, in both dxp-composed-commitment.md and SUBSTRATE_DEVELOPMENT_PLAN.md, not left standing:** formal proofs for 3PS and 2PS genuinely exist (dxp-11/dxp-12 in the framework's own repo, found only after cloning it fully -- an earlier claim that no proofs existed anywhere was wrong, based on checking 2 of 17 doc files). The Quorum Modifier (QM) is participant-quorum-within-one-transaction (majority tolerance for unavailable participants), not a cross-instance replication mechanism -- an earlier claim conflating the two, based on word-association rather than checking, is corrected at both places it was written. Canonical 3PS's own proof assumes durable-decision-log-based resume-to-completion for mid-execute coordinator crashes -- a stronger guarantee than this design's tombstone+GC choice; the trade-off is named explicitly as deliberate, not hidden behind "we're doing 3PS, proven correct."
- **The cost of true, durable 3PS assessed concretely, for if this is ever revisited:** durable OpParams persistence (not the in-memory pending maps all four current adapters use), idempotency retrofitted individually into bal/fsm/entity/cal (each its own migration against already-shipped code), and a standalone recovery/resume subsystem comparable in scope to the coordinator itself. Filed as pending in the plan's own Deferred section, same treatment as 2PS, same trigger (cross-tenant/cross-instance dxp).
- **Deferred, deliberately, not part of this item:** sequential/dependent participants (a participant whose params need an earlier participant's result). Established this is NOT plain 3PS -- it's a heterogeneous mix, the dependent leg closer to 1ps (saga-shaped) per the framework's own "declared per-participant mixtures" framing, not a named modifier among OV/TBS/GA/SC/QM despite checking all five directly. Design sketched (depends_on DAG, execution waves, result.<id>.<field> binding reusing pkg/fsm/eval's existing resolution path) but not committed -- wave 5's own exit criterion (the hotel example) doesn't need it, all four of its participants are genuinely independent.
- Not started. Design complete; implementation has not begun.

Cross-ref: CHANGELOG 0.20.3.

## [0.20.3] T-83 — Cross-substrate invalidation: the dxp.Participant post-commit verb (item 39, wave 5) -- T-57 properly scoped and budgeted, not just flagged (v0.20.3, 2026-07-31)

Theme: dxp · closed 0.20.3 · 2026-07-31


- **Reconsidered (2026-07-29), reverted off item 21's critical path.** Filed at P2 as an item-21 prerequisite the same session it was raised, in direct response to being asked whether entity CREATE and invalidation were covered -- elevated quickly, not weighed separately against what the wave-5 exit gate (the hotel test) actually checks. Reviewed on request: the gate verifies guard-bearing writes land correctly and consistently across participants -- journal entries, the booking record, the entity row. Neither bal's rollup nor cal's H3 index is guard-bearing (both are advisory, "no guard ever consults a rollup" -- established throughout this session, restated at T-83's own original filing). A stale rollup or H3 index after a dxp commit is the same self-healing-via-oracle state T-57 originally described, not a correctness failure the gate needs to catch. Demoted back near T-57's original P4 posture; real, valuable, not blocking "complete."
- **Trigger:** direct instruction (2026-07-29) -- "the invalidation cases" needed as part of "a complete 3ps implementation across all substrates." Properly scopes and budgets what T-57 had only flagged as a bare cross-reference ("item 21 should design the verb").
- **Scope, plan item 39:** dxp.Participant has no post-commit verb. Execute applies an effect inside a transaction the COORDINATOR opens and commits -- Execute itself never observes whether that commit actually succeeds. Every participant with a derived/rollup plane needs a "now safe to do derived-plane work" signal that fires strictly after commit, never before, never if the commit fails.
- **The doctrine already names the rule, precisely, even though no mechanism exists yet:** dxp-composed-commitment.md section 5c's three-tier visibility taxonomy, tier 3 ("analytic and read planes -- commit-fed, strictly"): "Rollups (ts buckets, bal cascade deltas, checkpoints)... ingest at confirm only... reserved state never cascades into any rollup -- consistent with the standing law that no guard consults a rollup." That's the WHAT; this item builds the HOW.
- **Concrete participants that need this, checked directly, not assumed:** bal's rollup plane (T-62, Pebble-backed, EmitDeltas best-effort today, called outside any dxp awareness); cal's H3 occupancy index (T-82's own dxp adapter doc states explicitly: "It does NOT touch the Pebble occupancy index (H3)... dxp.Participant has no post-commit verb today... the coordinator that drives Execute is responsible for its own post-commit H3 pass, same gap bal's own Execute doc already names, not silently new here"). Both adapters were built KNOWING this gap existed and documenting it in place rather than papering over it -- this item is where it actually gets closed, whenever it's picked up.
- **Caveat found and recorded while scoping this (not to be treated as settled doctrine without re-reading):** dxp-composed-commitment.md section 5c also describes a three-tier GUARD-plane taxonomy (tentative rows, edge-table tagging) that predates T-54's in-memory reservation pivot -- T-54's own record states the pivot superseded persisted tentative rows as dxp's reservation medium. Tier 3 (relevant here) still holds; tier 1's persisted-tentative-row description does not describe what was actually built. Whoever implements this item should re-verify section 5c's tiers 1-2 against the actual shipped cache design before relying on their wording, not just tier 3.
- **Roadmap's own effort estimate:** ~2 days.
- **Built (T-108, 2026-07-31, direct instruction):** `dxp.Participant.PostCommit` exists now, exactly the verb this item specified. cal is wired to it and proven correct (`/cal/check`/`/cal/openings` both verified against a real dxp-committed booking, both dispatch strategies). bal/fsm/entity/ts implement it as documented no-ops. **Remaining scope, narrowed to exactly one thing:** wiring bal's own rollup plane (T-62) to `PostCommit` — the mechanism exists, this item's own original two-participant list is now half done, not zero.

Cross-ref: CHANGELOG 0.20.3.

## [0.20.3] T-57 — dxp.Participant has no post-commit verb for derived-plane work (rollup emission etc.) (v0.20.3, 2026-07-31)

Theme: dxp · closed 0.20.3 · 2026-07-31


- **Trigger:** discovered building bal's dxp.Participant adapter (T-54's implementation phase, item 19's bal half). `Execute(ctx, tx, c)` applies the effect inside a transaction the CALLER opens and commits (proposal §11, single shared tx per instance) — but bal's own doctrine requires rollup-delta emission strictly AFTER commit (@C04a: no guard reads the rollup, but emission itself must follow the authoritative commit or it folds deltas for a transfer that might still roll back). Execute has no way to know when — or whether — the coordinator's shared commit succeeds, and dxp.Participant has no fifth verb for "now safe to do derived-plane work."
- **Scope:** every participant with a derived/rollup plane hits this, not just bal — cal's occupancy index and any future ts-cascade consumer will need the same hook.
- **Shape (not yet decided):** likely a `PostCommit(ctx, c Claim)` verb the coordinator calls once per participant, strictly after its shared tx commits, best-effort (a PostCommit failure must not unwind an already-committed instance — same doctrine as Transfer's existing `onRollupError` seam).
- **Current state:** bal's `Adapter.Execute` and `Store.transferInTx` are written and tested WITHOUT rollup emission for the dxp path — a dxp-executed transfer's rollup plane goes stale exactly as if `EmitDeltas` failed, and self-heals via the existing rollup oracle + `RebuildRollup` (T-54/item 15's machinery already handles this class of staleness; nothing new is at risk, but it is dormant on the dxp path until this is designed).
- **Owner:** item 21 (dxp coordinator), which is where the Participant contract itself would gain the verb.

Cross-ref: CHANGELOG 0.20.3.

## [0.20.2] T-67 — bal client library: pkg/client/bal.go + types_bal.go, mirroring cal.go's established pattern -- plus a real BackdatedError/XOLU-BAL006 HTTP-mapping bug found and fixed along the way (v0.20.2, 2026-07-31)

Theme: bal · closed 0.20.2 · 2026-07-31


- **Trigger:** direct instruction ("First E, then C") -- add /bal support to pkg/client, following on from finishing bal itself.
- **Investigated the existing convention before writing anything, not assumed:** pkg/client/cal.go and types_cal.go are the closest analog (a similarly-shaped primitive: accounts<->calendars, transfers<->bookings). Confirmed the established pattern precisely -- types mirror server request/response shapes byte-for-byte, client-side validation catches obvious mistakes before a round trip, errors decode into the structured *client.Error type keyed on the XOLU-<AREA><NUM> code family, and the two-tier test convention (mock-based unit tests in package client own the error matrix; a real-server integration test in package client_test, tag integration, owns wire-shape-drift detection that mocks structurally cannot catch).
- **A real, separate bug found and fixed BEFORE writing client code, not incidentally:** checking the exact server-side error family needed to document client methods correctly found that BackdatedError (XOLU-BAL006) existed in pkg/bal/store.go but writeBalError's switch (pkg/server/v2_bal_handlers.go) had no case for it -- a normal, expected backdated-entry refusal was falling through to the default case: 500 Internal Server Error with ErrStorageFailed, not a 409 with the correct code. The error constant itself, ErrBalBackdated, did not exist anywhere in pkg/errors either -- added it (matching the exact doc-comment style of its five siblings, including the deliberate note distinguishing it from ErrBalSealedPeriod: a per-account default overridable by policy, versus a tenant-wide, policy-independent boundary) and added the missing switch case. New regression test (TestBalAPI_BackdatedErrorMapping, pkg/server) hits the real handler and confirms 409 + XOLU-BAL006 specifically, not just that an error occurs.
- **New files, mirroring cal's shape exactly:**
  - pkg/client/types_bal.go -- BalDefineRequest/BalAccount, BalTransferRequest/BalTransferResult, BalBalanceResult, BalEntry/BalEntriesResult, BalAsOfResult, BalCloseResult. Amounts are decimal strings at the account's declared scale on the wire, never JSON numbers (@B04) -- the client never constructs a numeric amount either.
  - pkg/client/bal.go -- BalDefine, BalTransfer, BalBalance, BalEntries, BalAsOf, BalClose. Doc comments document the full XOLU-BAL001-006 family and which HTTP status each maps to. Noted explicitly in BalClose's doc: sealing is tenant-wide, not per-account, and cannot be undone over the API or otherwise -- matching T-64's own design, not a client-side limitation being worked around.
  - pkg/client/bal_test.go -- 18 mock-based tests: happy path, structured error (BAL001 bounds, BAL002 unknown account, BAL006 backdated), and client-side validation (missing required fields, From==To, zero time) per method.
  - pkg/client/integration_test.go extended (existing file, not new) -- TestIntegration_BalFullFlow: define two accounts, transfer, read authoritative balance AND derived as-of balance and confirm they agree, read entries, seal a period tenant-wide, then attempt a backdated transfer into the now-sealed period and confirm the real server refuses it with XOLU-BAL003 over actual HTTP -- proving the seal enforces, not just that the close endpoint returns 2xx. cfg.BalEnabled = true added to the shared bootServer helper alongside the existing cfg.CalEnabled, matching how cal's own coverage is wired into the same shared harness.
- **Deliberately NOT exposed, because the server itself doesn't expose it:** account temporal_policy (backdated vs append_only) has no HTTP surface today -- accounts are always created append_only via the API. The client mirrors this limitation rather than inventing a parameter the server would ignore. Noted in BalTransferRequest's own doc comment so a caller isn't left guessing why setting it isn't possible.
- **Verified exhaustively, applying the lesson from the 0.17.0 release directly (build+vet is not sufficient verification on its own):** full pkg/client suite green under -race; the integration-tagged suite green under -race against a real in-process server; full pkg/server suite green; a genuine go test ./... sweep across the WHOLE tree (not just build/vet) -- clean, every package.

Cross-ref: CHANGELOG 0.20.2.

## [0.20.2] T-58 — bal checkpoints: replace stale-flag/lazy-recompute with eager delta-propagation (prerequisite for item 16 prefix-collapse) (v0.20.2, 2026-07-31)

Theme: bal · closed 0.20.2 · 2026-07-31


- **Trigger:** found auditing the T-54 dxp reservation-cache work for a related but distinct concern (whether rollups need range-based invalidation, prompted by scoping cal's dxp participation). Full design: docs/proposals/bal-checkpoint-delta-propagation.md.
- **The gap:** Checkpoint's recompute and VerifyCheckpoints's oracle both sum the full journal from the epoch. Item 16 (seal/close + prefix-collapse retention, not yet built) prunes old journal entries once a checkpoint covers them. The moment it ships, both functions silently degrade the same way for backdated-policy (museum-style) accounts, and the oracle cannot catch it because both sides of its comparison are wrong identically.
- **The fix:** checkpoints and rollup buckets are both folds under SumInt64 (associative, commutative). Buckets already exploit this (Append combines a delta into the fold directly, no recompute, correct for any insertion order). Checkpoints don't, only because Checkpoint/VerifyCheckpoints recompute from source instead of combining a correction into the existing value. Replace transferInTx's stale-flag write with eager delta-adjustment (balance = balance + signed_amount, same range, same transaction) -- never touches the journal, so pruning it changes nothing.
- **Bonus, not just forward-compat:** the original lazy/stale design was working around a real cost concern (O(checkpoints x journal-length) recompute per T-51's own defect record). Delta-adjustment is O(checkpoints), cheap scalar increments -- dissolves the reason to defer at all. Net simplification: the stale column's write path goes away, VerifyCheckpoints's stale exemption goes away, BalanceAsOf's skip-stale fallback becomes unreachable for new data.
- **Migration stance (recommended, adopted):** leave the stale column and its READ-side filters (BalanceAsOf, VerifyCheckpoints) in place, inert -- correctly exempts any pre-existing stale=1 row from before this change, costs nothing, and the exemption becomes naturally vacuous since nothing writes stale=1 going forward. No ALTER TABLE, no explicit migration step.
- **Not solved here, item 16's own obligation:** VerifyCheckpoints's oracle still sums the full journal for its comparison value and will need re-scoping to the earliest retained checkpoint once prefix-collapse exists -- already named as owed in the bal proposal (@C04b). Orthogonal to this item.
- **Scope:** pkg/bal/store.go (transferInTx), pkg/bal/rollup.go (doc comments), pkg/bal/policy_test.go (T-51 test assertions updated for eager correctness; new multi-checkpoint range test; new two-leg sign test).
- **Implemented and verified (same session):** transferInTx's stale-flag write replaced with two signed delta-adjustment UPDATEs (one per leg -- opposite signs, so the old single IN (?, ?) form could not express this). Doc comments updated on Checkpoint, VerifyCheckpoints, and the stale column's DDL to state its new role (migration-safety-only, no writer sets it anymore). Both open questions resolved as recommended: stale left inert rather than dropped; filed as its own item rather than folded into item 16. Two pre-existing tests asserting the OLD stale=1 behavior found and updated (policy_test.go's T-51 test, and a SECOND, separately-discovered one -- backdate_defect_test.go's TestBackdatedTransfer_StaleCheckpoint, the original T-51 defect-acceptance test -- missed on the first pass since it lives in a different file; caught by -race full-suite run, not by inspection). Two new tests added per the spec's testing obligations (multi-checkpoint range, two-leg opposite-sign correctness). Full bal suite green under -race; full tree build+vet clean; no code path writes stale=1 anywhere (swept). Not yet released (no version bump, no CHANGELOG entry) -- sitting on the working tree pending a release pass.

Cross-ref: CHANGELOG 0.20.2.

## [0.20.1] T-110 — kitchen-sink (many of every primitive, both substrates, one instance) and adversarial failure-at-scale tests, completing T-109's own remaining scope -- all ten pass, including a genuine torn-commit-at-scale proof; also a real, investigated (not glossed over) finding about cumulative single-process sandbox capacity at this session's grown test volume (v0.20.1, 2026-07-31)

Theme: dxp · closed 0.20.1 · 2026-07-31


- **Trigger:** direct instruction, continuing T-109's own remaining scope named explicitly as not yet done: the "multiple substrates several times" kitchen-sink combination, and adversarial tests proving a single failure among many correctly aborts (collapsed) or tears (phased) the whole set.
- **Built, in `pkg/server/v2_dxp_scale_test.go` alongside T-109's own four scale tests:**
  - `TestDxpTxnAPI_Scale_KitchenSink_ManyOfEverything_AllCommit` -- eleven participants in one instance (3 bal, 2 cal, 2 fsm, 2 entity, 2 ts), both substrates, forcing `dispatchPhased`. Every earlier test varied one axis at a time (many-of-one-primitive, or five distinct primitives once each); this is the first to combine many-of-each AND multi-substrate AND repeated, together. Passed on the first run -- direct confirmation T-109's fix generalises past the four narrow cases that found the bug.
  - Four "one failure among many, none commit" adversarial tests -- five bal legs (one exceeds available funds), four cal bookings (one conflicts with a real pre-existing booking), three fsm transitions (one sends an input the spec never defines), six entity creates (one collides with an existing id). Each proves attendance correctly refuses the WHOLE instance at scale, not just a pair -- including participants that would have succeeded cleanly on their own, checked by verifying every one of them independently (all balances 0, all calendars free, all machines still `reserved`), not just the failing one.
  - `TestDxpTxnAPI_Adversarial_MultiSubstrateAtScale_OnePhaseParticipantFails_TornAccepted` -- deliberately the odd one out: four participants (two bal, one cal, one ts), one bal leg fails at Execute time specifically (bal only ever checks the credit ceiling at Execute, never Reserve). Phased participants commit independently, so this does NOT roll back siblings that already committed -- proves the honest, accepted torn-commit outcome (§6) holds at real scale, not just a pair: `committed_through` reads exactly 3, and the three legs that genuinely committed are each verified independently.
- **One test-writing mistake caught and fixed, the same class T-107/T-108 already taught, applied correctly this time without needing a second round:** the multi-substrate torn test's own cal-leg verification first used `/cal/check`, which reads H3 -- and H3 is only ever brought current by `PostCommit` (T-108), which fires strictly on a genuine FULL-instance commit and never for a torn/expired one. Caught before declaring the test done, not after; fixed to verify H1 (`cal_bookings`) directly, the same pattern T-107 established.
- **A genuine, honest finding about cumulative sandbox capacity, investigated directly rather than glossed over:** running the FULL `pkg/server` suite in one process, at this session's now-substantially-grown test volume (T-109 + T-110 together add 15 tests, each a full `httptest.Server` lifecycle), reproduces the exact symptom T-98 already characterized and fixed the LEAK for: two `graph_path` tests fail deterministically at a specific point in cumulative execution order, passing cleanly in complete isolation every time. Confirmed directly, not assumed: removing this session's new test file restores a fully clean single-process run; restoring it reproduces the failure every time. Raising `ulimit -n` to 4096 does NOT resolve it (ruling out simple FD-count pressure specifically); `/proc/net/tcp` showed hundreds of lingering connections at rest, consistent with T-98's own original suspicion (TIME_WAIT/socket-table pressure from many `httptest.Server` instances across a fast sequence of test invocations, not a per-run leak). This is legitimate additional test volume tipping an already-marginal single-process sandbox ceiling, not a new leak -- confirmed T-109's new adapters use the already-fixed `stdTestServer` cleanup path throughout, not the old buggy one T-98 fixed. Reassuring, checked directly rather than assumed: `release.py`'s own test step shards by default (8 packages per shard), so the actual formal release process -- already verified clean for 0.20.0 -- would not hit this whole-package-in-one-process artifact at all.
- **Verified:** all ten new tests green, including under `-race` for the dxp-specific run. Full existing dxp suite (53+ tests) green under `-race` -- nothing regressed from T-109's own interface change being exercised this much harder. Every other package (`pkg/dxp`, `pkg/bal`, `pkg/cal`, `pkg/timeseries`, `pkg/storage`) unaffected. `pkg/server` in isolation/scoped runs: clean. `pkg/server`, full, single-process, unsharded: the cumulative-volume artifact above, not a functional defect.
- Left at ◐, not closed, per this repo's own closure procedure -- closes at the next release, not on verification alone.

Cross-ref: CHANGELOG 0.20.1.

## [0.20.1] T-109 — adversarial scale testing (many participants of one primitive) found a genuine, serious, cross-cutting bug: every adapter's own pending map was keyed by Txn alone, silently breaking ANY dxp instance with two or more same-primitive participants -- fixed across the interface (Claim.ParticipantID, Reserve's signature) and all five adapters, not just tests added (v0.20.1, 2026-07-31)

Theme: dxp · closed 0.20.1 · 2026-07-31


- **Trigger:** direct instruction -- more adversarial dxp tests: multiple entities, multiple substrates several times, multiple fsms, multiple cals, multiple bals.
- **What this actually found: a genuine, serious, cross-cutting bug, not a test-writing exercise.** The first scale test written (five independent bal legs in one instance) failed with "no pending transfer for txn 1" -- not a test-setup mistake. Checked directly: every one of the five adapters' own `pending` maps (bal, cal, fsm, entity, ts) was keyed by `Txn` alone. `Txn` identifies the dxp INSTANCE, shared by every participant in it by design -- so a second participant of the SAME primitive in one instance silently overwrote the first's stashed Reserve params under the shared key, and whichever Execute goroutine ran first consumed the one surviving entry. Every other same-primitive participant's Execute then failed outright. Confirmed for bal, cal, fsm, and entity by direct reproduction (four of my first four scale tests, before any fix, all failed this exact way) -- ts had the identical latent bug, unexercised until now since no prior test had ever dispatched two same-primitive participants together.
- **Root cause, precisely:** neither `Txn` (shared by design, cache's own ClaimsByTxn/ConfirmTxn/ReleaseTxn require every claim in an instance to share it) nor `Resource` alone (not always unique -- two bal legs debiting the SAME source account, one payer several line items, is an entirely ordinary pattern, not a corner case) can distinguish two same-primitive participants within one instance.
- **Fix: `dxp.Claim` gained `ParticipantID`, `dxp.Participant.Reserve` gained a `participantID` parameter.** The coordinator passes the def's own participant `id` (`dxpParticipantSpec.ID`) into every Reserve call; each adapter carries it through unchanged into the Claim it returns. Every adapter's `pending` map is now keyed by `(txn, participantID)`, not `txn` alone -- a `pendingKey(txn, participantID string) string` helper, defined once per package (bal, cal, storage [shared by fsm+entity], timeseries).
- **A second, related bug found and fixed in the same pass:** entity's auto-id append path (`reserveAppend`, `EntityAppendParams` with `ID == nil`) built its own txn-scoped resource key as `"entity:" + Entity + ":~append:" + txn` -- ALSO collision-prone for two auto-id-append participants sharing one instance, same root cause. Fixed to `... + txn + ":" + participantID`. An existing unit test asserted the exact old key format; updated to the corrected one, not reverted around.
- **Scope of the fix, checked directly, not assumed complete:** all five adapters (bal, cal, fsm, entity, ts), the `dxp.Participant` interface itself, the coordinator's own Reserve call site (`dispatchDxpTxnCore`), and every direct caller of `Reserve` across the tree -- unit tests in `pkg/bal`, `pkg/cal`, `pkg/storage` (fsm + entity), `pkg/timeseries`, `pkg/dxp/integration`, and this session's own T-103 test fakes (`slowFakeParticipant`, `failingFakeParticipant`), all updated to the new five-argument signature.
- **The four scale tests that found this, all now green:** `TestDxpTxnAPI_Scale_FiveBalLegs_AllCommit` (5 independent bal legs, same source account, distinct destinations, verified via 5 separate balance reads), `TestDxpTxnAPI_Scale_FourCalBookings_AllCommit` (4 independent calendars, verified via 4 separate `/cal/check` calls), `TestDxpTxnAPI_Scale_ThreeFsmTransitions_AllCommit` (3 independent machines, verified via 3 separate `/fsm/machine/{id}` reads), `TestDxpTxnAPI_Scale_SixEntityCreates_AllCommit` (6 independent entity creates, `committed_through` checked as 6).
- **Verified:** every one of the 45 dxp tests in `pkg/server` green under `-race`, including all of T-99/T-104/T-105/T-107/T-108's own tests -- nothing regressed from this interface change. `pkg/dxp`, `pkg/dxp/integration`, `pkg/bal`, `pkg/cal`, `pkg/timeseries`, `pkg/storage` all green under `-race`. Full `pkg/server` and full `go test ./...` (37 packages) green at the sandbox's default ulimit.
- **What this does NOT yet cover, named plainly, not silently deferred:** the "multiple substrates several times" and adversarial (failure-at-scale) parts of the original request -- a kitchen-sink def mixing several bal + several cal + several fsm + several entity + several ts in one instance, and tests proving a single failure among many correctly aborts (collapsed) or tears (phased) the whole set, not just a pair. Both are natural next steps on the same foundation this item just fixed; neither was attempted here given the scope of what the first four tests alone turned up.
- Left at ◐, not closed, per this repo's own closure procedure -- closes at the next release, not on verification alone.

Cross-ref: CHANGELOG 0.20.1.

## [0.20.1] T-108 — dxp.Participant gained the post-commit signal T-83/dxp.go/cal's own adapter comment all named as missing -- cal's H3 occupancy index now genuinely updates after a dxp booking commits, /cal/check and /cal/openings both proven correct through both dispatch strategies, on direct instruction (v0.20.1, 2026-07-31)

Theme: dxp · closed 0.20.1 · 2026-07-31


- **Trigger:** direct instruction, following from the review discussion: "/cal/check and /cal/openings MUST become correct for dxp-driven bookings." Not a suggestion to weigh against T-83's own P4 -- an explicit mandate to build it.
- **Built exactly what dxp.Participant's own doc, T-83's own body, and cal's adapter comment all already named as missing, not improvised:** `PostCommit(ctx, c Claim) error` added to the `dxp.Participant` interface. Fires strictly after the coordinator itself confirms an instance genuinely, durably committed -- never before, never at all for an instance that ends `released` or `expired`.
- **All five adapters implement it.** bal/fsm/entity/ts are no-ops, documented as such -- bal's own rollup (T-62) is named explicitly as the next real consumer of this same mechanism, not wired here (out of this instruction's own scope). cal's is the real implementation: re-reads the booking from H1 (safe -- no transaction is open by the time PostCommit runs, checked directly against the exact deadlock lesson `Execute`'s own doc already recorded for the identical class of read) and calls `addToPlane`, documented idempotent on its own terms (an OR over a bitmap cannot corrupt shared bits from being re-added).
- **A real, necessary lifecycle change in cal's adapter, not just an addition:** `Execute` no longer deletes its `pending[c.Txn]` entry on success -- `PostCommit` needs `tp.BookingID` to know what to re-read from H1, so the entry has to survive past Execute now. Cleaned up by whichever of `PostCommit` or `Release` actually runs for that txn, matching Release's own already-idempotent, unconditional-no-op-on-missing-entry contract.
- **Coordinator wiring, both dispatch strategies, not just one:** `postCommitAll`/`releaseAll` helpers, called from both `dispatchCollapsed` and `dispatchPhased`. `postCommitAll` fires exactly once, only on the genuine full-instance commit path, strictly after the terminal state is durably recorded. `releaseAll` fires on every non-committed outcome -- including phased-torn cases where a participant's own Execute already succeeded independently: the SQL/Pebble row it wrote correctly stays (§6's own accepted-torn-commit stance, unchanged), but `Release` still cleans up that participant's LOCAL bookkeeping (cal's `pending` map entry), which would otherwise leak, since the phased failure branch previously called neither `Release` nor `PostCommit` for anyone.
- **Logging added, matching the interface's own documented contract ("the coordinator logs failures here"):** both helpers log via the same `zerolog.Logger` already threaded through the collapsed/phased split (T-105), best-effort, never fails the response -- by the time either runs, the instance's own terminal status is already the authoritative outcome.
- **The actual proof, not just the wiring, two variants covering both dispatch strategies:** `TestDxpTxnAPI_PostCommit_CalOccupancyIndexReflectsCommittedBooking` (bal+cal, all-SQL, forces `dispatchCollapsed`'s own `postCommitAll` call specifically) and `TestDxpTxnAPI_PostCommit_CalOccupancyIndex_PhasedPath` (cal+ts, forces `dispatchPhased`'s own separate call). Both check `/cal/check` reports `feasible: false` for the committed span; the collapsed-path test additionally checks `/cal/openings` reports zero openings inside the fully-booked window -- a second, independently-implemented read of the same occupancy (T-29's own regression guard exists specifically because Check/Openings agreement is a property to prove, not assumed). Both passed on their first run.
- **Verified:** all 41 dxp tests (including every T-99/T-104/T-105/T-107 test already in the suite) green under `-race`. `pkg/cal`'s own full native suite green under `-race` -- the `Execute`/pending lifecycle change touches that package directly, worth confirming nothing there regressed even though nothing in it calls `PostCommit`. `pkg/dxp`, `pkg/bal`, `pkg/timeseries`, `pkg/storage` all green under `-race`. Full `pkg/server` suite and full `go test ./...` (37 packages) green at the sandbox's default ulimit.
- **What this does NOT do, named plainly:** bal's own rollup plane (T-62) is not wired to `PostCommit` -- same shape of consumer, explicitly out of this instruction's scope, left for whoever picks that up. The mount-time `abandoned`-state design (`dxp-reservation-cache.md §11-13`) remains its own, separate, unbuilt piece, unrelated to this item.
- Left at ◐, not closed, per this repo's own closure procedure -- closes at the next release, not on verification alone.

Cross-ref: CHANGELOG 0.20.1.

## [0.20.1] T-107 — the full five-participant hotel worked example (cal+bal+fsm+ts+entity) finally proven end-to-end through the real HTTP API, plus six interval-overlap tests through the dxp path -- found and fixed a real tenant-ID bug in test setup and confirmed T-83's H1/H3 gap empirically for the first time (v0.20.1, 2026-07-31)

Theme: dxp · closed 0.20.1 · 2026-07-31


- **Trigger:** direct instruction -- more multi-substrate, multi-participant dxp tests with increasing difficulty, negative tests for interval overlap (start/middle/end/complete), through the real dxp path specifically.
- **Built:** `pkg/server/v2_dxp_hotel_test.go`. Two groups.
- **The headline proof, finally:** `TestDxpTxnAPI_Create_FullHotelExample_FiveParticipants_AllCommit` -- cal (room booking) + bal (payment) + fsm (booking confirmation) + ts (audit event) + entity (guest record update), five participants, two substrates, dispatched together through `dispatchPhased` and checked against all five real side effects independently. This is the actual doctrine worked example (`dxp-composed-commitment.md`'s own hotel_reserve, §5a) that the whole multi-session dxp effort has been building toward -- never exercised end to end through the real HTTP API until this test. T-105's own register entry named this explicitly as still open ("Wave 5's original hotel-worked-example gate... is not itself proven yet").
- **Five real, small issues found and fixed getting there, each a genuine gap in either the test or (in one case) in what the test was checking against -- not the coordinator itself:**
  1. fsm def specs require an explicit `determinism` field -- quick fix.
  2. `cal`'s dxp participant requires `booking_id` non-empty, not just `calendar` -- the JSON tag's `omitempty` only affects marshalling, not the validation requirement.
  3. **A genuine tenant-ID mismatch, not a typo.** Tenant `0` is reserved in `pkg/tenant`'s own registry (first auto-assigned id is `1`, `0` skipped explicitly) -- but the existing `seedCalendar`/`seedBooking` test helpers (`v2_cal_handlers_test.go`) hardcode tenant `0` directly against `cal.Manager`, correct for cal's own tests (whose URLs carry no `/tenant/{name}/` segment and so never auto-register "default" at all) but wrong the moment "default" is also touched via a URL that does carry a tenant segment -- every dxp/bal/ts/fsm/entity URL in this file does, landing on `1`. Wrote `defaultTenantID`/`seedCalendarForDefaultTenant`/`seedBookingForDefaultTenant`, resolving the real id explicitly rather than assuming either number.
  4. A binding-state booking requires a non-zero `bearer` -- missed on the first pass, added.
  5. **The real finding, not just a test bug: `/cal/check` cannot verify a dxp-committed booking, and this is T-83's own already-registered gap made concrete for the first time, not a new one.** `handleCalCheck` queries `s.calMgr.IndexFor(tenantID)` -- the H3 Pebble occupancy index -- while `dxpEngineOf["cal"] = "sql"` covers H1 alone, by design (H3 is deliberately outside dxp's reach, T-83, still open). A dxp-committed booking genuinely leaves `/cal/check` reporting `feasible: true` for the same span -- confirmed empirically against the real code, not assumed from the design doc. Fixed the test to verify against `cal_bookings` (H1) directly via SQL, which is both the actual source of truth and what dxp's own write is supposed to (and does) update. T-83 itself is unaffected -- correctly demoted P4 already, since H3 is advisory, never guard-consulted -- but now has a real, reproducible symptom on record rather than only a theoretical one.
- **Negative: interval-overlap admission through the dxp path itself, not cal's own native admission tests (which already exist separately).** `overlapTwoParticipantDef` pairs a bal payment with a cal room reservation specifically so the assertion can check that dxp's own attendance mechanism releases bal's already-held claim too when cal refuses -- not just that cal refuses on its own. Six tests against one fixed real booking (14:00-18:00, `StateBinding` -- found the hard way that `StateProposed`, the shared `seedBooking` helper's own default, sits in a different admission PLANE entirely and is invisible to a Binding-plane conflict check by design, not a bug; fixed by seeding Binding directly): start-overlap (12:00-15:00), middle-overlap (15:00-16:00), end-overlap (17:00-20:00), complete-overlap both directions (10:00-22:00 containing the existing booking, and 15:30-16:30 contained by it), all five correctly refused (`released`, bal's claim genuinely never committed, balance stays 0) -- plus one positive contrast (18:00-19:00, immediately adjacent, half-open semantics, correctly commits) proving the five refusals are about genuine overlap, not merely proximity.
- **Verified:** all seven new tests (the hotel proof plus six overlap variants) green under `-race`. Full `pkg/server` suite green at the sandbox's default ulimit. Full `go test ./...`, all 41 packages, green.
- Left at ◐, not closed, per this repo's own closure procedure -- closes at the next release, not on verification alone.

Cross-ref: CHANGELOG 0.20.1.

## [0.20.0] T-105 — phased (non-collapsed) dxp execution built, plus the HTTP wiring for ts -- genuine multi-substrate dispatch (bal+ts) now works end-to-end through the real API, both the full-success and torn-commit-accepted cases proven, not just designed (v0.20.0, 2026-07-31)

Theme: dxp · closed 0.20.0 · 2026-07-31


- **Trigger:** direct continuation, closing the second and final piece named as remaining after T-86: "the phased (non-collapsed) execution path in dispatchDxpTxnCore (still explicitly refused, unbuilt), plus the HTTP-facing wiring." Both done together, deliberately, exactly as T-86's own entry said they should be -- "so registering a ts-touching def and actually dispatching it become possible in the same change, not two separately confusing steps."
- **Built, following dxp-coordinator-design.md §2-3's already-specified shape directly, not improvised:** `dispatchDxpTxnCore`'s Phase 3 now branches on `CollapseEligible && EngineHomogeneous`. The existing, T-99/T-104-fixed collapsed logic was extracted verbatim into `dispatchCollapsed` (a pure refactor -- checked directly: the full 30-test existing dxp suite re-ran unchanged and green, one pre-existing test excepted, see below). New: `dispatchPhased` -- each participant gets its own genuinely independent store (`dxp.NewSQLStore` per SQL participant, always `owns:true`; `dxp.NewPebbleStore` over a fresh `*pebble.Batch` for the one Pebble primitive, `ts`), concurrent Execute+Commit per participant with **no barrier**, unlike the collapsed path -- correct, not an oversight: there is no shared resource here for one participant's real commit to race against a sibling's still-in-flight Execute, which is exactly the condition T-99's fix exists to guard against for the collapsed case specifically. `committed_through` is a genuine partial count for this path (never artificially 0 or N), matching the design's own accepted torn-commit stance (§6) rather than the collapsed path's stronger all-or-nothing guarantee.
- **HTTP wiring:** `ts`/`append` added to `dxpPrimitiveOps`, `dxpEngineOf` (`"pebble"` -- the only non-`"sql"` entry, which is what makes `EngineHomogeneous` ever actually false), `decodeDxpParticipantParams`, and `dxpParticipantRegistry` (resolves the tenant's `*timeseries.PebbleStore` via `s.tsManager.StoreFor`, type-asserted -- `timeseries.NewAdapter` needs the concrete type, matching how fsm/entity already need `*storage.SQLiteStore` for the same reason).
- **New store-construction seam, mirroring an existing pattern exactly rather than inventing a new one:** `timeseries.PebbleDBProvider` (`pkg/timeseries/store.go`) -- one method, `PebbleDB() *pebble.DB`, the direct Pebble-side analog of `storage.WriterDBProvider`. Used once, by `dispatchDxpTxn`'s own resolution of `pebbleDB` before calling the core function (a second call to `s.tsManager.StoreFor` beyond the one `dxpParticipantRegistry` already makes to build the adapter itself -- a small, accepted duplication rather than entangling adapter construction with store-handle resolution in one function).
- **Real end-to-end proof, not a standalone reproduction elsewhere:** two new HTTP-level tests in `pkg/server/v2_dxp_phased_test.go`. `TestDxpTxnAPI_Create_MultiSubstrateDispatch_BothCommit` -- a real bal transfer and a real ts append dispatched together, checked against both participants' own real side effects (the bal balance, and the ts event queried back through its own real HTTP API), not just the coordinator's reported status. `TestDxpTxnAPI_Create_MultiSubstrateDispatch_PartialFailure_TornAccepted` -- bal's own documented Execute-time-only ceiling check (Reserve only checks the debit side, per `bal.TransferParams`' own doc) forces a genuine partial failure: ts's append durably lands, bal's transfer doesn't, `committed_through` reads 1 (not 0, not 2) and the instance reads `expired` -- the actual torn-commit acceptance §6 describes, confirmed as real behaviour, not asserted from documentation alone. Both passed on first run.
- **Two pre-existing tests found obsolete by this change, fixed rather than left red:** `TestDxpDefAPI_Create_UnknownPrimitive_Refused` and `TestDecodeDxpParticipantParams_UnknownPrimitive_Refused` both used `"ts"` specifically *because* it had no registered primitive/decoder yet -- both said so in their own comments. Now that `ts` is real, both correctly stopped being refused; fixed to use a genuinely unregistered primitive name instead, preserving each test's actual intent rather than deleting or weakening either.
- **Verified:** full existing dxp suite (32 tests total now) green under `-race`, including the two new phased-path tests and the two new multi-substrate ones. `TestDispatchDxpTxnCore`'s own T-103 tests and all ten `TestTsAdapter` tests re-confirmed green under `-race` too. Full `pkg/server` suite green at the sandbox's default ulimit. Full `go test ./...`, all 41 packages, green.
- **What this completes:** the two pieces T-86's own entry named as still needed for "a genuine end-to-end multi-substrate dispatch" -- both now real, both proven working end-to-end through the actual HTTP API, not asserted from design docs or unit-level isolation alone. Wave 5's original hotel-worked-example gate (cal+bal+fsm+ts, five participants including entity) is not itself proven yet -- this item proves the phased mechanism generically (bal+ts, the minimal genuine multi-substrate case), not that specific five-participant worked example, which remains open, separately, if it's still wanted as its own proof.
- Left at ◐, not closed, per this repo's own closure procedure -- closes at the next release, not on verification alone.

Cross-ref: CHANGELOG 0.20.0.

## [0.20.0] T-104 — dispatchDxpTxnCore's own failure-handling branches deadlocked against the writer pool's MaxOpenConns=1 -- opened a second transaction while sharedTx was still genuinely open, 100% reproducible, found by T-103's own new adversarial test hanging on first run (v0.20.0, 2026-07-31)

Theme: dxp · closed 0.20.0 · 2026-07-31


- **Trigger:** found by T-103's own new adversarial test (`TestDispatchDxpTxnCore_SlowParticipantFails_NothingCommits`), which hung on its first run rather than failing cleanly.
- **The bug, in `dispatchDxpTxnCore`'s Phase 3b (`pkg/server/v2_dxp_dispatch.go`), both failure branches (a participant's Execute failing, and the owning store's own Commit failing):** each opens a second transaction (`tx2, err2 := db.BeginTx(ctx, nil)`) to mark the instance `expired`, while `sharedTx` -- the Phase 3 transaction every participant executed against -- is still genuinely open. The writer pool is `MaxOpenConns=1` by deliberate design (`pkg/storage/sqlite.go`'s own documented single-writer WAL discipline, confirmed directly, not assumed). `sharedTx`'s only planned close was the function's own deferred `sharedTx.Rollback()`, which cannot run until the function returns -- and the function cannot return because it is blocked forever inside `db.BeginTx` waiting for a connection the pool will never free. A goroutine dump (`go test -timeout 30s`) confirmed the exact blocking point precisely: `database/sql.(*DB).conn` inside the `tx2 := db.BeginTx` call at the line in question.
- **100% reproducible, not adversarial-timing-dependent** -- unlike T-99, this doesn't need any particular interleaving to manifest; any dispatch where a participant's Execute genuinely fails during the collapsed path's Phase 3 hits it, every time. It sat in the code since T-99's own fix; nothing exercised this branch through the real coordinator before T-103's own new tests -- `pkg/dxp/integration/multiparticipant_test.go`'s partial-failure test predates the coordinator and hand-wires a single sequential `Tx.Commit`, never touching this code path at all.
- **Fix:** both failure branches now explicitly `sharedTx.Rollback()` and set `txCommitted = true` (making the deferred rollback a safe no-op) BEFORE opening `tx2` -- the connection is genuinely free by the time the second transaction is requested. Applied to both branches for consistency and defensiveness, though only the participant-Execute-failure branch was confirmed to actually deadlock; the commit-failure branch likely already released its connection via `Tx.Commit`'s own failure path, but relying on that precisely was judged too fragile to leave alone once the sibling branch's bug was found.
- **Verified:** the exact test that hung now passes in 0.12s. Both new adversarial tests (T-103) green under `-race`. Full existing dxp suite green under `-race`. Full `pkg/server` and full `go test ./...` (41 packages) green at the sandbox's default ulimit.
- **Pattern worth naming for future dxp work on this file:** any code path that opens a second `db.BeginTx` while an earlier one from the same function may still be open is a latent deadlock against this store's single-writer pool, not merely a style concern -- worth an explicit audit before any future change to `dispatchDxpTxnCore`'s own transaction handling, collapsed or (once built) phased.
- Left at ◐, not closed, per this repo's own closure procedure -- closes at the next release, not on verification alone.

Cross-ref: CHANGELOG 0.20.0.

## [0.20.0] T-103 — dispatchDxpTxn refactored into a thin Server-coupled wrapper plus a testable dispatchDxpTxnCore, closing the testability gap T-99 named explicitly as still open -- two new deterministic adversarial tests now force the exact interleaving that exposed T-99's bug against the real coordinator (v0.20.0, 2026-07-31)

Theme: dxp · closed 0.20.0 · 2026-07-31


- **Trigger:** direct instruction to proceed on the testability gap named explicitly in T-99's own register entry: "A fully deterministic regression test for the exact race... would need dispatchDxpTxn refactored for participant injection, which this fix does not attempt -- noted as a real, remaining gap, not claimed away."
- **Refactor:** extracted `dispatchDxpTxnCore(ctx, db, cache, registry, tenantID, txnID, snapshot, analysis, deadlineNs)` -- the actual orchestration logic, now taking `*sql.DB`/`*dxp.MemCache`/`map[string]dxp.Participant` as explicit parameters instead of reaching into `*Server`/`*http.Request` via `s.fsmDB(r)`/`s.dxpMemCache(tenantID)`/`s.dxpParticipantRegistry(r, needed)`. `dispatchDxpTxn` (the `*Server` method) is now a thin wrapper that constructs the real dependencies and calls it -- every existing HTTP-level caller and test keeps working completely unmodified, checked directly: the full existing dxp suite (28 tests before this item) re-ran unchanged and green.
- **A real editing mistake made and caught in the same pass, not hidden:** the first `str_replace` extraction left the original function's tail duplicated verbatim after the new one, producing a syntactically broken file (`go build` failed with a cascade of "non-declaration statement outside function body" errors starting mid-file). Caught immediately by the very next build step, not discovered later -- fixed with a guarded Python truncation once the exact duplicate boundary was located by direct inspection, not guessed. Worth naming as a pattern: `str_replace`'s `old_str` must span the FULL region being replaced when `new_str` is a complete rewrite of that region, not just up to wherever a change starts.
- **New tests, using the refactor for exactly what it exists for:** `TestDispatchDxpTxnCore_ConcurrentExecuteCommit_SurvivesSlowParticipant` -- a real bal transfer (index 0, fast, the store that owns the real Commit) paired with a fake, deliberately slow participant (index 1, 100ms delay) registered under the `entity` key, forcing the exact interleaving T-99's fix must survive. `TestDispatchDxpTxnCore_SlowParticipantFails_NothingCommits` -- the companion case, the slow participant fails instead of succeeding; checks the collapsed path's strengthened all-or-nothing guarantee (`committed_through` is always 0 or `len(reserved)`, confirmed via the real bal balance staying at 0, not just the coordinator's own reported status).
- **What this proves that no earlier test could:** the standalone reproduction that found T-99 lived entirely outside `dispatchDxpTxn` (proved the pattern unsafe in isolation, not that the real function was fixed correctly). The HTTP-level test added alongside T-99's own fix used only real adapters with no way to force any particular timing. This file checks the actual, current `dispatchDxpTxnCore` under adversarial, controlled interleaving for the first time.
- **Verified:** both new tests green under `-race`. Full existing dxp suite (30 tests total now) green under `-race`. Full `pkg/server` suite green at the sandbox's default ulimit. Full `go test ./...`, all 41 packages, green.
- **Direct, immediate payoff, filed separately as T-104:** the second new test (`SlowParticipantFails_NothingCommits`) hung on its first real run -- a genuine, 100%-reproducible connection-pool deadlock in `dispatchDxpTxnCore`'s own failure-handling branches, sitting in the code since T-99's own fix, never exercised by any test until this one. Exactly the payoff this refactor was built for.
- Left at ◐, not closed, per this repo's own closure procedure -- closes at the next release, not on verification alone.

Cross-ref: CHANGELOG 0.20.0.

## [0.20.0] T-102 — dxp_txn tombstone retention: configurable purge window for terminal instances (default 48h), folded into T-100's own DxpSweeper -- direct instruction, in place of the fuller mount-time abandoned-state build (v0.20.0, 2026-07-31)

Theme: dxp · closed 0.20.0 · 2026-07-31


- **Trigger:** direct instruction (2026-07-31), in response to the abandoned-vs-expired design finding raised while scoping T-100's own limits: "it's fine to keep tombstones for a configurable period, create an environment variable for that, defaults to 48 hours before they're gone." Declines the fuller mount-time-fsck build (dxp-reservation-cache.md §11-13's own `abandoned` terminal state, still not built, remains its own separate scope) in favour of the smaller, immediately actionable piece: bound how long terminal dxp_txn rows stick around.
- **Built:** extended `DxpSweeper` (T-100) rather than a separate mechanism -- one worker, one sweep cycle, does both jobs. New config `DxpTxnRetentionSecs` (default 172800 = 48h), env var `XOLU_DXP_TXN_RETENTION_SECS`. Each sweep now also runs `DELETE FROM dxp_txn WHERE status IN ('committed','released','expired') AND created_at < cutoff`. `retentionSecs <= 0` disables purging entirely -- tombstones kept forever -- matching how `BlobGCGracePeriodSecs` already treats non-positive as off, not "purge immediately," checked directly against that field before choosing the same convention.
- **Measured from `created_at`, not a `terminated_at` this schema doesn't have.** Dispatch is synchronous, so creation and termination are the same instant for every ordinary instance; only T-100's own sweep-caught crash residue terminates later than it was created, and `created_at` is still the honest, simpler choice there -- this is a coarse cleanup window, not a tight SLA, and adding a schema column for it wasn't worth it here.
- **Verified:** three tests -- expiry-only (retention disabled, unaffected by this change), purge-past-retention (a 1-second retention window plus a short sleep, deterministic and fast rather than faking `created_at`; committed/released rows purged, an `active` row of the same age never touched regardless), and retention-disabled-keeps-forever. All green under `-race`. Full `pkg/server` suite green at the sandbox's default ulimit. Full `go test ./...`, all 41 packages, green.
- **What this does NOT do:** distinguish a crash-caused tombstone from an ordinary one, or provide the incident-signal value `dxp-reservation-cache.md` describes for a genuine `abandoned` state. Retention purging and that distinction are orthogonal -- this item deliberately only does the one the team actually asked for.
- Left at ◐, not closed, per this repo's own closure procedure -- closes at the next release, not on verification alone.

Cross-ref: CHANGELOG 0.20.0.

## [0.20.0] T-101 — GET/list for dxp/def and dxp/txn (item 20's remaining scope) -- built, with a ?status= filter on the txn list, the whole point being to finally observe swept/expired/torn instances (v0.20.0, 2026-07-31)

Theme: dxp · closed 0.20.0 · 2026-07-31


- **Trigger:** item 20's own remaining scope, named explicitly in the handover: "GET/list for dxp/def and dxp/txn -- real, smaller, independent gap, not blocking anything above." Picked up directly after T-100, since observability was the natural next piece and this was the smallest well-scoped remainder.
- **Built:** `GET /dxp/def` (list), `GET /dxp/def/{id}` (retrieve), `GET /dxp/txn` (list, optional `?status=` filter), `GET /dxp/txn/{id}` (retrieve). Shape follows `handleFSMDefList`/`handleFSMDefGet` directly -- `dxp_defs` mirrors `fsm_definitions` structurally on purpose (T-87's own recorded reasoning), so its read surface does too, checked against the real fsm handlers rather than invented fresh.
- **`?status=` on the list endpoint is the actual point of building this now:** without it there was no way to see a swept/expired instance (T-100) or a torn one at all -- `TestDxpTxnAPI_List_StatusFilter_SeparatesTerminalOutcomes` proves both that a committed instance shows up under its own status and that a pre-dispatch-refused request (a bindings-schema violation, 422) never creates a row at all, so it correctly appears nowhere.
- **Delete still not built** -- the remaining slice of item 20's own scope, left open, not attempted here (no strong need identified yet; would need its own design pass on what deleting a def with existing instances against it should mean).
- **Verified:** six new tests (def list/get/404, txn list-with-filter/get/404), all green under `-race`. Full `pkg/server` suite green at the sandbox's default ulimit. Full `go test ./...`, all 41 packages, green.
- Left at ◐, not closed, per this repo's own closure procedure -- closes at the next release, not on verification alone.

Cross-ref: CHANGELOG 0.20.0.

## [0.20.0] T-100 — dxp_txn had no crash-recovery sweep at all -- neither claim in the codebase (dxp.go's own doc, dxp-coordinator-design.md's own §6) that this was already handled was true, checked directly; fixed, plus a related pre-existing gcWorkers shutdown leak found and fixed alongside it (v0.20.0, 2026-07-31)

Theme: dxp · closed 0.20.0 · 2026-07-31


- **Trigger:** direct question -- "what is still lacking for intense and adversarial, multi-participant, multi-substrate /dxp transactions" -- answered by actually checking, not assuming. `pkg/dxp/dxp.go`'s own package doc names "the mount-time tombstone pass this package does not itself implement -- that is the dxp coordinator's concern, item 21." `dxp-coordinator-design.md` §6 independently claims the other half: "the sweep worker that already exists (item 18, T-54) picks it up as ordinary expired -- no new state, no new subsystem." Checked both claims directly against the code rather than trusting either: neither was true. `pkg/dxp.Janitor` (T-54's own sweep) only trims lapsed claims from the in-memory reservation cache -- hygiene, explicitly documented "never correctness" -- and every `s.gcWorkers` registration in `server.go` was audited directly: `ts-retention`, `blob-gc`, `meta-gc`. No `dxp` entry existed anywhere.
- **What this means concretely:** a `dxp_txn` row only remains `active` past its own `deadline_ns` if a process crash or an unrecovered panic interrupted `dispatchDxpTxn` between the initial snapshot insert and its own `markDxpTxnTerminal` call (dispatch is otherwise fully synchronous, within one request). Before this item, nothing anywhere would ever notice or resolve such a row -- it sits forever, and since `GET`/list for `dxp/txn` also doesn't exist (item 20's own remaining scope, separately tracked), it's invisible through the API entirely.
- **Fixed: `DxpSweeper` (`pkg/server/v2_dxp_sweeper.go`), a `gc.Sweeper` registered as `dxp-gc`.** One bulk `UPDATE dxp_txn SET status='expired', committed_through=0 WHERE status='active' AND deadline_ns < ?` per sweep, across every tenant in one query (`dxp_txn` is a global table with `tenant_id` as a column, matching `MetaSweeper`'s own shape). Guarded by the same `WHERE status = 'active'` CAS discipline `markDxpTxnTerminal` uses -- a row a genuinely in-flight dispatch is about to terminate itself races the sweep on the deadline boundary only in the narrow, benign sense that whichever `UPDATE` matches first wins; the other affects zero rows. New config: `DxpGCEnabled`/`DxpGCIntervalSecs` (default true/60s -- shorter than meta's 300s default, since dxp's own `phase_ttl` deadlines run in seconds-to-minutes).
- **Explicitly out of scope, by design, not an oversight:** this sweeper does not attempt to determine whether a swept instance's participants actually committed before whatever interrupted it. A crash between the collapsed path's one real `Tx.Commit` and `dispatchDxpTxn`'s own separate `markDxpTxnTerminal` write is the narrow torn-bookkeeping window `dxp-coordinator-design.md` §6-7 already names and accepts ("the instance's own record never claims success it didn't achieve") -- marking `expired` with `committed_through=0` is the honest default, not a claim that nothing happened, and not something this item resolves by reconciling against participants directly.
- **A second, related, pre-existing bug found and fixed in the course of verifying this one's own shutdown path, not a new one introduced by it:** `Server.Stop()` never actually stopped anything in `s.gcWorkers` -- confirmed by reading every reference to the field, not just the registration sites: appended to in four places, ranged over read-only by the admin listing endpoint, never once passed to `.Stop()`. `meta-gc`'s own ticker worker has leaked on every `Server.Stop()` call since it was built, independent of anything this session touched; the new `dxp-gc` worker would have inherited the identical leak the moment anyone enabled it. Fixed by adding the missing `for _, w := range s.gcWorkers { w.Stop() }` loop. `gc.Worker.Stop()` is itself idempotent (guarded by `w.stopped` under its own mutex), which matters because `ts-retention`'s worker is registered into both `s.tsRetention` (its own dedicated field, stopped separately, pre-existing) and `s.gcWorkers` -- so it now gets `Stop()` called twice, safely, checked directly rather than assumed.
- **New test, since no sweeper in this codebase had one before:** `TestDxpSweeper_MarksStuckActiveInstanceExpired` (`pkg/server/v2_dxp_sweeper_test.go`), written directly against the schema (the only way to construct a row genuinely stuck `active`, since normal dispatch never leaves one there). Three rows: stuck-active-past-deadline (swept), active-not-yet-past-deadline (untouched), already-terminal-past-deadline (untouched) -- proving both the sweep itself and its CAS guard against re-marking or touching live instances.
- **Verified:** the new test passes under `-race`. Full `pkg/server` suite green at the sandbox's *default* ulimit (1024) -- meaningful here specifically because `Server.Stop()`'s fix touches the teardown path nearly every test in the package exercises. Full `go test ./...`, all 41 packages: green. `pkg/dxp`, `pkg/dxp/integration`, and `pkg/server`'s `TestDxp*` suite re-run under `-race` once more after the `Stop()` change: clean.
- **What this does NOT close, named plainly:** this is one piece of a larger gap analysis (multi-substrate needs T-86 plus an unbuilt phased execution path in `dispatchDxpTxn`; adversarial multi-participant coverage needs `dispatchDxpTxn` refactored for participant injection before deterministic timing-based tests are possible; observability needs item 20's `GET`/list for `dxp/def`/`dxp/txn`). Filed and fixed as its own item because it was small, self-contained, and a genuine prerequisite -- not because it completes the larger picture.

Cross-ref: CHANGELOG 0.20.0.

## [0.20.0] T-99 — dxp coordinator's collapsed-path concurrent Execute+Commit races a real commit against a sibling participant's still-in-flight Execute, breaking the atomicity guarantee collapse (@D06) exists to provide -- confirmed by direct reproduction, not just code-reading (v0.20.0, 2026-07-31)

Theme: dxp · closed 0.20.0 · 2026-07-31


- **Trigger:** independent review of the dxp primitive's current state, requested directly (2026-07-31) as a fresh-eyes pass over `dispatchDxpTxn` rather than the account already recorded in the handover/CHANGELOG.
- **The bug, in `dispatchDxpTxn`'s Phase 3 (`pkg/server/v2_dxp_dispatch.go`):** every participant's Execute-then-Commit runs in its own goroutine against the shared `*sql.Tx` (collapsed case). Only the `i==0` store has `owns: true`, so only its `store.Commit(ctx)` call ever touches the real `Tx.Commit()` — but that call fires the moment `i==0`'s own goroutine finishes `Execute`, with no barrier waiting for the other goroutines to have even started theirs. If participant 0 is fast and a later participant is doing real work (a calendar admission check, a validation query), participant 0's real commit can land while the later participant's `Execute` is still in flight or hasn't begun.
- **The design doc's own safety argument (dxp-coordinator-design.md §5) is incomplete, not wrong on its own terms.** It reasons about Commit-vs-Commit concurrency ("only the owns:true wrapper's Commit ever touches the real Tx... nothing to race") and correctly rules that out. It never considers Commit-vs-Execute concurrency — one goroutine's real `Commit()` racing another goroutine's still-in-flight `Exec`/`Query` against the same `*sql.Tx`. That is the actual race, and it is real.
- **Confirmed by direct reproduction, not just code-reading.** A minimal standalone program (2 goroutines sharing one `*sql.Tx` against `modernc.org/sqlite v1.29.0` — xolu's own pinned driver — one fast-then-commit, one sleeping 50ms before its own `Exec`) reproduces it every run: the slow goroutine's `Exec` fails with `sql: transaction has already been committed or rolled back`, and the database ends up containing only the fast participant's write. Run under `-race`: clean, no data-race report — this is a logical ordering bug, not a memory race, so the project's existing `-race`-gated adversarial testing is structurally the wrong tool to catch it.
- **Doesn't need multi-core hardware to manifest, unlike T-53's chronicle.Engine race.** The repro's "slow" goroutine only needs to yield once (a `time.Sleep`, or in a real adapter, any SQL query before its write) for the Go scheduler to interleave the fast goroutine's commit ahead of it — confirmed reproducing under `GOMAXPROCS=1` (this sandbox's actual core count). A genuine multi-participant dispatch test would very likely expose this on the first run, in this sandbox, without needing real parallel hardware.
- **Why this is worse than the already-accepted torn-commit risk (design doc §6), not a restatement of it.** §6's torn-commit discussion is scoped to the phased/non-collapsed path and process-crash windows in the collapsed path — cases the design is explicit are fundamentally hard and handled by tombstone+GC, not prevented. The collapse mechanism (@D06) exists specifically so the *collapsed* case gets genuine SQL-transaction atomicity "for free," structurally, not merely a well-handled failure mode. This bug means the collapsed path's atomicity is not actually structural — it depends on incidental goroutine timing — which converges its real risk profile toward the phased path's, for exactly the case (single-tenant, all-SQL) that collapse was supposed to make strictly safer.
- **Concrete consequence, not just an abstract atomicity violation:** `committed_through` and the final `expired` status *do* correctly reflect that something tore (the bookkeeping isn't lying) — but a participant's real-world effect (money moved via `bal.Transfer`, a calendar slot booked) can be durably committed alongside an instance record that says the transaction didn't succeed. For the hotel worked example: payment charged, room never booked, and the caller's own dxp_txn record reads "expired."
- **Untested, not merely under-tested.** Audited every def dispatched through a real `POST /dxp/txn` call across the whole test suite (`pkg/server`, `pkg/dxp/integration`): the only one is `simplePaymentDef`, a single-`bal`-participant def. The 3-participant hotel def (`cal`+`bal`+`fsm`) is registered and validated in `TestDxpDefAPI_Create_HotelExample_Succeeds` but a `/dxp/txn` instance is never created against it — dispatch's actual multi-goroutine path has never executed with more than one participant anywhere in the codebase. `pkg/dxp/integration/multiparticipant_test.go`'s two tests predate the coordinator and hand-wire Execute calls sequentially with a single `tx.Commit()` at the end (the safe pattern) — they do not exercise `dispatchDxpTxn` at all.
- **Fix shape, not yet implemented:** split Phase 3 into two barriers — concurrently run every participant's `Execute` alone (safe; concurrent `Exec`/`Query` calls against one `*sql.Tx` serialize correctly, confirmed in the same repro with commit removed from the race), `wg.Wait()`, check all results, and only then call the single owning store's `Commit()` — sequenced after the barrier, never racing it. This keeps the "shrinks exposure window" benefit for the execute phase without racing the one real commit against it.
- **Also noted in passing, same review pass, minor, not yet acted on:** `ParticipantStore.Ready()` (`pkg/dxp/store.go`) sets an internal `ready` bool on both `SQLStore` and `PebbleStore` that is never read anywhere in the codebase. The doc comment describes it as starting "the coordinator's own guard... what happens on timeout" — no such timeout/guard mechanism exists yet. Not a correctness bug (nothing relies on it today), but the doc comment overclaims a mechanism that is not implemented; worth fixing the comment or building the mechanism, not urgent either way.
- **Fix, implemented 2026-07-31 (`pkg/server/v2_dxp_dispatch.go`, Phase 3):** split into two barriers. 3a runs every participant's `Execute` concurrently against the shared `Tx` but no goroutine calls `Commit` or `Abort` on its own. 3b is the barrier (`wg.Wait()`) — only after every participant has either succeeded or failed does the code decide Commit-vs-Rollback for the one real `Tx`, via the single owning (`i==0`) store, sequenced, never racing a sibling's `Execute`. No mutex needed any more (removed): each goroutine writes only its own `results[i]` slot, and `wg.Wait()` establishes happens-before for everything read after it. Direct, positive consequence: for the collapsed path, `committed_through` is now always exactly 0 or `len(reserved)` — never a partial count — a strictly stronger guarantee than before, not merely a side effect of the fix.
- **New regression coverage, closing the actual gap that let this go untested, not just patching the code:** `TestDxpTxnAPI_Create_TwoParticipants_DispatchesBothAtomically` (`pkg/server/v2_dxp_def_handlers_http_test.go`) is the first test anywhere in the codebase to dispatch a real `/dxp/txn` instance with more than one participant through `dispatchDxpTxn` itself — bal+entity, checked against both the coordinator's own reported outcome (`status: committed`, `committed_through: 2`) and one real side effect (the bal balance, fetched back over HTTP), not just a string. A fully deterministic regression test for the exact race (forcing one participant's Execute to lag another's under controlled timing) would need `dispatchDxpTxn` refactored for participant injection, which this fix does not attempt — noted as a real, remaining gap, not claimed closed.
- **Verified:** `pkg/dxp`, `pkg/dxp/integration`, and `pkg/server`'s `TestDxp*` suite all green under `-race` (confirming no new race was introduced, though `-race` was never going to catch the original bug either — it's a logical ordering defect, not a memory race). Full `go test ./...`, all 41 packages: green, at the sandbox's default ulimit.

Cross-ref: CHANGELOG 0.20.0.

## [0.20.0] T-98 — pkg/server suite hit a sandbox FD ceiling under cumulative load -- root cause found and fixed: setupTestServer/TestServer.cleanup (server_test.go) never closed its underlying SQLite store or drained lazily-created per-tenant stores, leaking real file descriptors across 36 call sites until graph_path_e2e_test.go's turn in file order (v0.20.0, 2026-07-31)

Theme: testing · closed 0.20.0 · 2026-07-31


- **Trigger:** observed while verifying T-97's dispatch orchestration -- the full pkg/server suite, run cumulatively, hung and then failed with "accept4: too many open files" during the two 50-connection dxp adversarial concurrency tests (T-92).
- **What was found, checked directly rather than assumed:** this sandbox's ulimit -n is 1024, and is not raisable within this session -- `ulimit -n 65536` returns "Operation not permitted" explicitly, confirmed by running it directly. Reducing T-92's two tests from 50 to 20 concurrent connections (matching markDxpTxnTerminal's own concurrent test, T-93, already proving the identical zero-collision property at that scale) resolved the hang -- the suite now completes.
- **After that fix, a second, different, but related symptom remained: TestGraphPath_NoPath_Returns404 and TestGraphPath_SelfPath (graph_path_e2e_test.go, unrelated to dxp entirely) fail consistently and reproducibly, every full-suite run, with XOLU-ST006 ("Failed to initialise tenant context").** Checked directly, not assumed: both pass cleanly, every time, when run in complete isolation. This is deterministic, not randomly flaky -- the same two tests fail every run, at the same point in the suite's own execution order, consistent with cumulative FD/socket pressure (likely OS-level TIME_WAIT accumulation from the many HTTP test servers this session's own additions have grown the suite to include) peaking at that specific point rather than random contention.
- **Correction to this item's own original framing:** the "not a code defect" verdict above was wrong. Investigated directly in a fresh, pristine sandbox (2026-07-31) rather than assumed: `nproc` there is 1, so this was never about goroutine/test parallelism (default `-parallel` = GOMAXPROCS = 1 already) -- it was sequential FD accumulation within one process. Isolating the two failing tests alone (`-run 'TestGraphPath|TestGraphShortestPath'`, nothing else in the package run first) passed clean in 0.56s, ruling out a regression in those tests themselves and pointing squarely at leakage from whatever ran before them in file order.
- **Root cause, found by auditing every `httptest.NewServer` call site in `pkg/server`'s test suite (55 call sites, most going through the shared `stdTestServer.cleanup()` path, which is correct):** `setupTestServer`/`TestServer` (`server_test.go`, 36 call sites across 6 files) is a second, older test-server helper that never wired into that shared path. `TestServer.cleanup()` only closed `ts.sqliteStore` if non-nil -- but `setupTestServer`'s own return statement never populated that field, so the SQLite store it constructed was never closed, ever. It also never called `server.Stop()`, so any per-tenant stores lazily created via the tenant registry during a test leaked too. Every one of the 36 call sites correctly `defer`s `cleanup()` -- the bug was entirely inside the helper, not at any call site.
- **Fix:** `setupTestServer` now sets `sqliteStore: store` on the returned struct; `TestServer.cleanup()` now calls `ts.server.Stop()` (draining `s.tenantStores`, matching `stdTestServer.cleanup()`'s own documented reasoning for why that call exists) before closing the base store.
- **Verified, not assumed fixed:** `pkg/server` alone, isolated: green. Full `go test ./...` across all 41 packages: green -- at the sandbox's *default* 1024 ulimit, no manual raise, no test-count reduction, no sharding. The fix eliminates the pressure rather than working around it.
- **Left open at ◐, not closed:** per this repository's own closure procedure, closure happens alongside the release that ships the fix, not immediately on verification. Ready to close whenever the next release cuts.

Cross-ref: CHANGELOG 0.20.0.

## [0.20.0] T-86 — ts dxp adapter (item 40, wave 5): restores ts to the hotel gate per the foundational worked example's own participant list -- the first real Pebble-backed participant, and what forces the gate's phased-path run to be genuine cross-engine coordination rather than an all-SQL stand-in (v0.20.0, 2026-07-31)

Theme: dxp · closed 0.20.0 · 2026-07-31


- **Trigger:** direct instruction (2026-07-29) -- reviewing "what's left to complete dxp" surfaced that the wave-5 exit criterion had quietly dropped ts as a participant somewhere in this session's own narrative-writing, without ever flagging that as a real deviation from the foundational proposal's own worked example.
- **Restores what the doctrine already specified, not a new requirement.** dxp-composed-commitment.md's own hotel_reserve worked example (section 5a) has always included a ts participant: {"id": "audit", "primitive": "ts", "op": "append", "params": {"series": "bookings.confirmed", "value": 1}}. Item 19 actually built bal/fsm/entity/cal -- the union of primitive types across two DIFFERENT worked examples (section 3's place_order: bal+bal+cal+entity; section 5a's hotel_reserve: cal+bal+fsm+ts) -- matching neither example's exact participant list. This item's gate restores ts to the actual test, alongside entity (independently load-bearing per T-84, from the other example): five participants total.
- **Investigated directly before filing, not filed speculatively -- the actual admission logic, checked against real code, not assumed:**
  1. validateEvent (pkg/timeseries/store.go) is pure and stateless -- timeline ID non-zero, timestamp not before epoch, at most 7 numeric fields, no NaN -- no I/O, no registry lookup, trivially reusable in Reserve with zero adaptation.
  2. Append's own existing code already has a REAL "does this exist" check -- `s.reg.get(e.Timeline)` (timeline must be defined) plus a dims-count check -- directly analogous to cal's calendar-existence check and bal's account-existence check, a proven pattern to follow, not a novel one.
  3. No conflict/admission concept exists beyond existence -- ts events are independent, immutable, append-only points; there is nothing analogous to cal's interval-overlap or bal's balance-sufficiency to design. Reserve/Validate are expected to be near-trivial by the primitive's own nature, not by cutting corners.
- **The one genuine, real complication found, not hidden:** Append's actual implementation has a write-coalescer branch -- if enabled for a timeline (a tenant-wide dynconfig flag, ts.writecoal), the write hands off to an async goroutine that batches appends on its own schedule, incompatible with a dxp-driven write needing to land synchronously inside a coordinator-supplied *pebble.Batch. Checked precisely whether bypassing the coalescer for dxp writes while ordinary writes keep using it creates any hazard: it does not -- ts events have no shared mutable state to get out of sync (unlike bal's balance), so bypass is functionally equivalent to coalescing with a batch size of one. Correct, not just convenient.
- **No raw-handle-exposure blocker, checked and resolved:** PebbleStore (ts's own, pre-existing type -- a genuine naming collision with dxp.PebbleStore from T-85, different packages so no compile conflict, but worth knowing as a reader) does not expose its internal *pebble.DB anywhere. Resolved by precedent, not by adding an accessor: the adapter file lives inside pkg/timeseries itself, exactly like every other adapter lives inside its own primitive's package (pkg/cal/dxp_adapter.go, pkg/bal/dxp_adapter.go), giving natural internal access without new exported surface.
- **Sysmask (pkg/timeseries/sysmask.go), checked, not assumed irrelevant:** governs system-vs-user TimelineID scope. Read as an access-control concern orthogonal to dxp participation -- whatever enforcement exists already applies at the registry/validation layer Reserve already calls into; no dxp-specific handling identified as necessary, flagged as worth re-confirming at implementation time rather than settled with full certainty here.
- **What this genuinely proves, and why it's the highest-value remaining piece of wave 5's exit gate:** Checked directly against the canonical framework's own source (2026-07-29, correcting a hallucinated paraphrase this entry originally carried): @D06's actually-stated condition is that the participant set is single-tenant -- trivially true for v1, since cross-tenant dxp does not exist yet. Whether a Pebble participant specifically blocks collapse is not something @D06 states at all; it is a separate, unstated-in-the-doctrine mechanical necessity (*pebble.Batch has no representation inside *sql.Tx, so "collapse into one SQL transaction" cannot include it regardless of tenant scope). Full detail in docs/proposals/dxp-coordinator-design.md. Restoring ts turns the gate's "phased path" run from a hypothetical or an artificially-forced test case into a genuine cross-engine coordination proof -- the actual thing this whole multi-turn dxp design conversation was originally motivated by.
- **Honest risk assessment, not overclaimed:** the primitive's own logic (Reserve/Validate/the tx-scoped write) is low-risk, well-understood, following proven patterns twice already established (bal.transferInTx, cal.putBookingInTx). The real, unavoidable unknown is whether ParticipantStore/PebbleStore (T-85's design, never yet exercised by real code) work correctly the first time something actually writes through them -- expected, not a reason to doubt the item, matching this session's own repeated finding that adapters reveal real bugs in shared infrastructure regardless of how carefully the design was reasoned through beforehand (cal's two deadlocks being the direct precedent).
- **Sequencing:** the adapter itself (appendInBatch, Reserve/Validate/Execute/Release, unit tests against a hand-wired harness) can be built alongside item 20 and T-84, independent of the coordinator's own implementation -- matching how bal/fsm/entity/cal were each built and tested before any coordinator existed. Proving the actual five-participant gate, collapsed and phased paths producing identical records, requires item 21 (T-85's design, implemented) to exist too.
- **Rough estimate:** ~2 days for the adapter's own logic and tests; verification budget beyond that is genuinely open, pending what PebbleStore's first real exercise surfaces.
- **Built, this session:** `pkg/timeseries/dxp_adapter.go` -- `Adapter` (`Reserve`/`Validate`/`Execute`/`Release`), `AppendParams` (dxp.OpParams). Follows `bal.Adapter`'s own exact structural pattern (pending-params stash by txn id, mutex-guarded, Release/Execute both clear it) -- checked directly against `pkg/bal/dxp_adapter.go` before writing this, not approximated.
- **Confirms the investigation already recorded in this item, all three points checked directly against the real code rather than re-derived:** `validateEvent` is pure/stateless, timeline existence is the one real admission check, and no conflict/admission concept exists beyond existence -- Reserve/Validate are genuinely near-trivial. Only real addition beyond what was already investigated: a dims-count check in Reserve too (mirroring `Append`'s own real caller-side check, `len(e.Dims) != int(cfg.Dims)`), which the original investigation's item body didn't call out explicitly.
- **The coalescer bypass, confirmed safe by construction, not just by the earlier investigation's reasoning:** `Execute` never calls `Append` -- it calls `EncodeKey`/`EncodeValue` directly (the same pair `Append`'s own non-coalesced path uses) and writes via the coordinator-supplied `*pebble.Batch`'s own `Set`, exactly mirroring `Append`'s `s.db.Set` call one layer down. `TestTsAdapter_Execute_WritesViaBatch_ReadableAfterCommit` proves this produces a genuinely readable, correctly-decodable entry after commit (queried back via the store's own `QueryRange`, values checked, not just "Execute returned nil") -- and `TestTsAdapter_Execute_AbortedBatch_NothingPersists` proves an aborted batch leaves no trace.
- **Deliberately, explicitly NOT done in this item, named plainly rather than silently deferred:** `ts` is not wired into `dxpPrimitiveOps`/`dxpEngineOf`/`dxpParticipantRegistry` (`pkg/server/v2_dxp_def_handlers.go`). Doing so today would let a def register successfully with `ts` as a participant while every dispatch against it refuses outright (`EngineHomogeneous` becomes false the instant any non-SQL primitive is registered at all -- checked directly: it is not "same engine as its siblings," it is "SQL-only," so even a single-participant, ts-only def would hit the phased-path refusal) -- an accept-then-always-refuse state that is honest but confusing. That wiring belongs with whichever item builds the phased execution path itself, so registering a ts-touching def and actually dispatching it become possible together, not two separately confusing steps.
- **Tested standalone, matching this item's own recorded sequencing** ("can be built alongside item 20 and T-84, independent of the coordinator's own implementation"): ten tests in `pkg/timeseries/dxp_adapter_test.go`, hand-wired against a real `PebbleStore` and real `*pebble.Batch` -- Reserve (success, undefined timeline, dims mismatch, invalid event), Validate (success, timeline deleted since Reserve), Execute (readable-after-commit with value checks, nothing-after-abort, wrong-store-type refused), Release (idempotent, Execute-after-Release fails). Not exercised through `dispatchDxpTxnCore` or any HTTP path -- there isn't one yet, by the design above.
- **Verified:** all ten green under `-race`. Full `pkg/timeseries` suite green. Full `go test ./...`, all 41 packages, green.
- **What remains before a genuine end-to-end multi-substrate dispatch is possible:** the phased (non-collapsed) execution path in `dispatchDxpTxnCore` (still explicitly refused, unbuilt), plus the HTTP-facing wiring named above. Both substantial, both deliberately out of this item's own scope.
- Left at ◐, not closed, per this repo's own closure procedure -- closes at the next release, not on verification alone.

Cross-ref: CHANGELOG 0.20.0.

## [0.19.3] T-97 — dispatchDxpTxn implemented and wired into POST /dxp/txn (item 21, wave 5): the coordinator's own orchestration, calling T-93/T-95/T-96 together for the first time -- a real dxp transaction now runs end to end through the actual HTTP API, not just a hand-wired test harness (v0.19.3, 2026-07-31)

Theme: dxp · closed 0.19.3 · 2026-07-31


- **Trigger:** direct instruction to continue building item 21's dispatch orchestration, following T-93/T-95/T-96's independently-built pieces (terminal transition, params decoding, adapter registry). This item is what finally calls them together, and the first time any dxp transaction has actually run end to end through the real HTTP API rather than a hand-wired test harness.
- **Scope: dispatchDxpTxn (new file, pkg/server/v2_dxp_dispatch.go) implemented and wired synchronously into POST /dxp/txn -- Reserve every participant, gate on full attendance, Validate, concurrent Execute+Commit (collapsed case -- one shared *sql.Tx, one owning store), markDxpTxnTerminal at the end.** Matches dxp-coordinator-design.md's own recorded correction directly: dispatch is not a separate step, POST /dxp/txn is one complete, stateless invocation.
- **Two real bugs found by actually running dispatch for the first time, not by writing more tests in isolation:**
  1. dxpParticipantRegistry (T-96) constructed all four adapters unconditionally -- a bal-only transaction failed outright whenever cal happened to be disabled on the server, even though that specific instance never touched cal. Fixed: dxpParticipantRegistry now takes a needed map and only constructs the primitives a given instance's own snapshot actually requires.
  2. Multiple test fixtures supplied bal amounts as JSON numbers in dxp bindings (e.g. "amount": 150). @B04's smuggling check (T-95) correctly refused every one of them -- the bug was in the fixtures, not the code, since dxp's binding path is the same JSON boundary bal's own /transfer endpoint already enforces this at. Fixed simplePaymentDef's own bindings_schema (integer -> string) and every place supplying a numeric amount; rewrote TestDxpTxnAPI_Adversarial_SchemaViolations' table entirely, since several of its cases (zero/negative "violates minimum:1") no longer apply to a string-typed schema at all.
- **A real, reproducible resource constraint found and documented, not silently worked around or ignored:** the two dxp adversarial concurrency tests (T-92, originally 50 simultaneous connections each) caused the full pkg/server suite to hang and then fail with "too many open files" -- this sandbox's own ulimit -n is 1024 and not raisable (confirmed directly: `ulimit -n 65536` returns "Operation not permitted"). Reduced both to 20, matching markDxpTxnTerminal's own concurrent test (T-93), which already proves the identical property (zero id collisions under real concurrent load) at that scale -- not a weakening of what either test proves, a resource-budget adjustment. After that fix, two unrelated tests (TestGraphPath_NoPath_Returns404, TestGraphPath_SelfPath) still fail consistently and reproducibly when run as part of the full, cumulative suite, with the identical XOLU-ST006 "tenant context" error -- but pass cleanly, every time, in complete isolation. Checked directly, not assumed: this is cumulative sandbox resource pressure from how substantially the suite has grown this session (T-87 through T-97), not a code defect in either dxp or graph_path_e2e_test.go. Filed separately (T-98) for visibility, same treatment as T-90's own timeseries flake.
- **Verified exhaustively within what the sandbox actually permits:** every dxp-specific test (T-87 through T-96's own suites, plus this item's own dispatch path) green, including the full run of TestDxpTxnAPI/TestDxpDefAPI/TestValidateDxpDef/TestParsePhaseTTL/TestAllocDXPID/TestDecodeDxpParticipantParams/TestMarkDxpTxnTerminal together; pkg/dxp and pkg/dxp/integration green; full tree build/vet clean. The full pkg/server suite itself is NOT fully green in this sandbox as of this filing -- T-98's own two tests fail when run cumulatively, a documented, isolated, non-dxp-related resource constraint, not a regression this item introduced into dxp's own correctness.
- Not started: the phased (non-collapsed) execution path is refused explicitly rather than silently wrong (no Pebble participant exists yet to require it, T-86) -- genuinely multi-substrate dispatch remains unbuilt and untestable until that adapter exists. GET/list/delete for txn instances (matching the same, already-recorded gap for defs, T-87) also not built.

Cross-ref: CHANGELOG 0.19.3.

## [0.19.3] T-96 — Per-tenant dxp.MemCache wiring and dxpParticipantRegistry implemented (item 21, wave 5): the production adapter-construction path that lets all four participants for one tenant genuinely share one reservation cache -- previously existed only as a hand-wired test-code pattern, never as real server infrastructure (v0.19.3, 2026-07-31)

Theme: dxp · closed 0.19.3 · 2026-07-31


- **Trigger:** continuing item 21's coordinator work following T-95's params decoding. Attempting to construct the actual dispatch logic surfaced that no production wiring exists anywhere for a per-tenant dxp.MemCache or for constructing all four adapters sharing one -- only the hand-wired test harness (pkg/dxp/integration/multiparticipant_test.go) had ever done this, and it constructs everything directly in test code, never through any Server-level mechanism.
- **Found precisely, not assumed:** a comment in v2_bal_handlers.go already referenced "dxp.MemCache/SetClaimsCache's own long-lived-resource pattern" as an analogy for bal's own rollup-handle caching -- but grepping for the actual field (s.dxpCache) found nothing. The comment described a pattern dxp.MemCache was designed to follow (T-54), not something anyone had actually wired up, since the coordinator that would need it never existed until now.
- **s.dxpCache sync.Map added to the Server struct, mirroring balRollup/balSealer's own exact pattern** (checked directly against both before adding this, not approximated): tenantID -> long-lived *dxp.MemCache. dxpMemCache(tenantID) lazily constructs-or-retrieves it via LoadOrStore. Without this, four adapters constructed independently per request would each get their own, isolated cache, and the entire cross-primitive conflict-detection mechanism dxp exists for would silently do nothing -- no error, just four participants that can never see each other's claims.
- **dxpParticipantRegistry(r) constructs all four currently-registered adapters (bal/cal/fsm/entity -- "ts" absent, matching dxpPrimitiveOps exactly, T-86 still open) sharing that one cache -- discovered three genuinely different, pre-existing store-access patterns along the way, not one uniform one:** bal uses balStore's own fresh-per-request-plus-cached-long-lived-resource pattern directly. cal uses calMgr's own Manager (CalFor for the Lifecycle, SourceFor for the booking source) -- a single, tenant-scoped instance kept on the Server itself, checked directly against v2_cal_handlers.go rather than assumed to match bal's shape. fsm and entity share the tenant's own *storage.SQLiteStore directly, type-asserted from getStore the same way fsmDB does internally, but returning the full store rather than just its *sql.DB, since both adapters need the store's own fsmResolveInTx/saveInTx/createInTx methods specifically.
- **Verified exhaustively:** full pkg/server suite green; full tree build/vet clean; a genuine go test ./... sweep across the whole tree. No dedicated unit test written for dxpParticipantRegistry itself -- deliberate, not an oversight: it has no meaningful caller yet (the dispatch loop that will actually use it doesn't exist), and testing it in isolation now would mean fabricating the same context-setup the real HTTP middleware already does correctly; it will be exercised properly, end to end, once the dispatch endpoint exists and runs a real transaction through it.
- Not started: the actual attendance/dispatch orchestration itself -- decode each participant via T-95, Reserve all of them, gate on full attendance, Ready()-triggered concurrent Execute+Commit, markDxpTxnTerminal (T-93) at the end. This item is the last piece of foundational wiring that orchestration needs; the orchestration itself is still the one substantial piece of item 21 that remains unbuilt.

Cross-ref: CHANGELOG 0.19.3.

## [0.19.3] T-95 — decodeDxpParticipantParams implemented (item 21, wave 5): turns a dxp_txn instance's resolved participant params back into the correct concrete OpParams type, replicating bal's own @B04 safety path exactly rather than a generic unmarshal -- proven safe by test, not just designed to be (v0.19.3, 2026-07-31)

Theme: dxp · closed 0.19.3 · 2026-07-31


- **Trigger:** continuing item 21's coordinator work following T-94's JSON tag additions -- the actual decode function those tags exist to support, needed by the coordinator's own dispatch logic (still not built) to turn a dxp_txn instance's resolved, snapshotted participant params back into the correct concrete dxp.OpParams type before calling Reserve/Execute.
- **Scope: decodeDxpParticipantParams(primitive, op string, paramsJSON []byte, tenantID tenant.TenantID) (dxp.OpParams, error) implemented, covering all five currently-registered participant type/op pairs.** bal/transfer replicates handleBalTransfer's own @B04-aware decode path exactly -- UseNumber-based decoding, refuse a bare json.Number for "amount", then bal.ParseAmount(s, scale) -- checked directly against that handler before writing this, not approximated. cal/book, fsm/transition, entity/update, entity/create use plain json.Unmarshal, safe given T-94's own tag work already closed the gaps that would have made that unsafe.
- **fsm's TenantID is set explicitly from the passed-in tenantID parameter, never trusted from the participant's own JSON** -- belt and suspenders with T-94's own json:"-" tag on that field (which already prevents unmarshal from setting it at all): this function is the one place it's actually assigned, from the dxp_txn instance's own real tenant.
- **9 new tests, including two that prove real safety properties directly rather than asserting them by design:** a decimal-string amount decodes to the correct int64 at a given scale; a bare JSON number for amount is refused, proving @B04's smuggling test survives the dxp path exactly as it already protects bal's own direct HTTP endpoint; a participant's own params JSON attempting to smuggle a tenant_id has no effect whatsoever -- the decoded TenantID is always the explicitly-passed value, proven by supplying a JSON tenant_id that differs from the passed-in one and confirming the JSON value never wins. Plus the remaining four type/op pairs each decoding correctly, an unknown primitive (ts, T-86, no adapter yet) refused, and an unknown entity op refused.
- **Verified exhaustively:** all 9 new tests green; full pkg/server suite green; full tree build/vet clean; a genuine go test ./... sweep across the whole tree.
- Not started: the actual attendance protocol and per-participant dispatch orchestration itself (Reserve across all participants, attendance gate, Ready()-triggered Execute, concurrent Commit, markDxpTxnTerminal at the end) -- this item and T-93/T-94 together are now the complete set of pieces that orchestration will call; none of them call each other yet.

Cross-ref: CHANGELOG 0.19.3.

## [0.19.3] T-94 — JSON tags added to all five dxp.OpParams types, preparing them for the coordinator's own params-decoding step (item 21) -- two real corrections made along the way: entity's field matched the doctrine's illustrative example rather than entity's own real, shipped convention (fixed, per direct correction), and bal's Amount field would have silently bypassed @B04's smuggling-test safety guarantee under generic unmarshaling (fixed, excluded from generic decode entirely) (v0.19.3, 2026-07-31)

Theme: dxp · closed 0.19.3 · 2026-07-31


- **Trigger:** starting the coordinator's own dispatch logic (item 21) surfaced that none of the five existing dxp.OpParams types (bal.TransferParams, cal.CalTransitionParams, storage.FsmTransitionParams, storage.EntityUpdateParams, storage.EntityAppendParams) had JSON tags at all -- they had only ever been constructed directly in Go code before this session's dxp/def+dxp/txn work existed. The coordinator needs to decode a dxp_txn instance's own resolved, snapshotted participant params (JSON, produced by jsonplate.Render) back into the correct concrete type -- impossible without tags, and Go's case-insensitive fallback matching doesn't cover several real cases (checked directly, not assumed: "calendar" does not case-insensitively match "CalendarID" at all).
- **A real correction made mid-work, from the user, worth recording precisely rather than smoothing over:** the first pass matched entity's own field to the doctrine's own worked-example key ("type"), reasoning that matching the doctrine's illustrative JSON was the safer default. Corrected directly: the doctrine's worked examples are illustrative pseudocode, not a binding API spec -- the actual house style to match is xolu's own, already-shipped convention, not whichever word a generic example happened to use. Checked directly against CommitUpdate/CommitAppend (entity's own real, already-shipped /commit HTTP types) before reverting: both already use json:"entity", not "type" -- reverted to match that, the real precedent, not the doctrine's illustration.
- **A second, more serious issue found as a direct result of applying that same corrected principle to bal -- not a naming preference this time, a real safety gap:** bal.TransferParams.Amount is int64; a plain json:"amount" tag would let the coordinator's own generic decode accept a raw JSON number straight into it, silently bypassing @B04 (bal's own deliberate "smuggling test": amounts cross any JSON boundary as decimal strings only, checked directly against handleBalTransfer, pkg/server/v2_bal_handlers.go, which uses UseNumber()-based decoding specifically to detect and refuse a bare json.Number before bal.ParseAmount ever runs). Fixed: Amount is now json:"-", excluded from generic unmarshaling entirely, with the reasoning recorded in the struct's own doc comment -- the coordinator's own params-decoding step (not yet built) must replicate bal's real UseNumber/ParseAmount path specifically for bal participants, never a generic Unmarshal for this one field. Confirmed bal.ParseAmount(s string, scale uint8) is already-built, already-tested, already-proven Option 1 (a decimal string including the point, e.g. "100.50", validated against a separate scale) -- reused directly rather than introducing shopspring/decimal or any new dependency, per direct instruction that reuse was preferred if bal's own existing mechanism already covered it, which it did.
- **FsmTransitionParams.TenantID is deliberately json:"-" too, for a related but distinct reason:** never trusted from a participant's own JSON params, regardless of tags -- the coordinator must always set it from the dxp_txn instance's own actual tenant. A def author supplying an arbitrary tenant_id in their own params, if it were ever unmarshaled, could reference a different tenant's machine entirely, bypassing the instance's own tenant scope.
- **A real, self-inflicted mistake caught and fixed before it shipped:** one of the doc-comment edits (TransferParams' own, adding the Amount-specific paragraph) duplicated an existing sentence rather than merging cleanly -- the old_str used for that edit didn't include the full pre-existing comment block, so the new paragraph landed alongside a redundant restatement rather than replacing anything. Caught by re-grepping the file immediately after the edit rather than assuming it landed clean, fixed before any test ran.
- **Verified exhaustively:** full pkg/bal, pkg/cal, pkg/storage, pkg/dxp, pkg/dxp/integration suites green -- confirming the tag additions changed nothing about how these types are constructed directly in Go elsewhere in the codebase, only how they would decode from JSON, which nothing yet does; full tree build/vet clean; a genuine go test ./... sweep across the whole tree.
- Not started: the coordinator's own actual params-decoding function (per-primitive, replicating bal's @B04-aware path specifically) and the full attendance/dispatch orchestration itself -- this item is preparatory groundwork for both, not either of them.

Cross-ref: CHANGELOG 0.19.3.

## [0.19.3] T-93 — markDxpTxnTerminal implemented (item 21, wave 5): the guarded T-34 CAS transition for dxp_txn's own phase state, proven correct under genuine concurrent load, not trusted by design (v0.19.3, 2026-07-31)

Theme: dxp · closed 0.19.3 · 2026-07-31


- **Trigger:** continuing item 21's coordinator work following T-88's ParticipantStore migration -- the first foundational sub-piece the actual orchestration logic will need to call repeatedly, built and proven on its own before the larger attendance/dispatch logic.
- **Scope: markDxpTxnTerminal implemented -- the guarded CAS transition for dxp_txn's own phase state, matching the T-34 pattern the doctrine's own §4a (guard locality) requires, explicitly not built on pkg/fsm (the decision worked through directly several turns earlier, grounded in dxp-composed-commitment.md's own "dxp refuses to host its phase state on fsm" language).** Mirrors fsm_walk.go's identical UPDATE-then-RowsAffected shape, checked directly against that file before writing this, not approximated. A real correction folded in: newStatus is validated against all three real terminal states (committed, released, expired) -- an earlier pass of this schema's own comment wrongly listed only two, missing released entirely, corrected several turns back but worth re-confirming was actually reflected here too.
- **committed_through is written atomically alongside the status transition, in the same guarded query -- the final, observable value at the moment an instance actually reaches its terminal state, not tracked incrementally per-participant-commit**, matching dxp-coordinator-design.md's own reasoning: durability only matters for the sweep worker's post-mortem observability at expiry, never for mid-flight resume, since none exists (§7's own no-durability decision).
- **4 new tests, including a genuine concurrent proof, not a trusted-by-design one:** each real terminal state succeeds from a fresh 'active' row; an invalid target status is refused (including "torn", deliberately never made a real state); a second transition attempt on an already-terminal instance is refused cleanly (ok=false, not an error) and the original state is preserved; and 20 concurrent transition attempts against one instance produce exactly one winner, actually run rather than assumed correct from the SQL pattern's theoretical atomicity -- this codebase's own T-34 lesson applied directly, not just cited.
- **A real bug found and fixed while writing the tests themselves, not in the function under test:** the "already terminal, second transition refused" test hung indefinitely on first write. Diagnosed precisely rather than guessed at: a read via a separate pool connection (checking the row's final status) was issued before the second transaction's own explicit rollback, while that transaction was still holding SQLite's write lock from its own attempted (zero-row) UPDATE. The deferred rollback pattern used elsewhere in this test file only fires at function end, too late for a read within the same function body. Fixed by rolling back explicitly before the read, with the reasoning recorded in the test's own comment rather than silently patched.
- **Verified exhaustively:** all 4 new tests green, including under -race; full pkg/server suite green; full tree build/vet clean; a genuine go test ./... sweep across the whole tree.
- Not started: the actual attendance protocol and per-participant dispatch (Reserve -> attendance gate -> Execute with Ready()-triggered guards -> concurrent Commit, calling this function at the end) -- this item is the transition primitive alone, a real prerequisite, not the orchestration logic itself.

Cross-ref: CHANGELOG 0.19.3.

## [0.19.2] T-92 — Adversarial testing pass on dxp/def and dxp/txn (T-87/T-89): 11 new tests covering concurrent load, cross-tenant isolation, jsonplate edge cases, schema-violation boundaries, malformed HTTP bodies, and SQL-injection-style content -- one real bug found and fixed, one real pre-existing discrepancy found and filed separately (T-91), one real test-design gap diagnosed and fixed at its actual root cause (v0.19.2, 2026-07-30)

Theme: dxp · closed 0.19.2 · 2026-07-30


- **Trigger:** direct instruction to test more intensely and adversarially, given the earlier test set (T-87/T-89) was happy-path-focused and had never exercised concurrent load, cross-tenant isolation, nested/array jsonplate paths, schema-violation boundaries, malformed HTTP bodies, or hostile string content.
- **Scope: 11 new adversarial tests added to pkg/server/v2_dxp_def_handlers_http_test.go, covering ground the earlier suite never touched.** Concurrent load (50 simultaneous POST /dxp/def and POST /dxp/txn calls, proving allocDXPID's atomic sequence produces zero id collisions, checked directly rather than trusted from the SQL pattern's theoretical atomicity -- confirmed clean under -race too). Cross-tenant isolation (a def registered under one tenant cannot be instantiated by referencing its numeric id from a different tenant -- a real security property never previously exercised). Nested and array-indexed jsonplate paths (order.customer_acct, order.items[0].price) -- the earlier test set only ever exercised flat, top-level refs. A $ref to a path absent from the bindings resolving to JSON null, proven directly against jsonplate's own documented claim rather than trusted on faith. A $ref resolving to a whole object, not just a scalar -- the doctrine's own "cal.span" case. Table-driven schema-violation boundaries (wrong type, zero/negative against minimum:1, null, extra undeclared fields, exactly-the-minimum, very large integers). Malformed HTTP bodies (empty, truncated, non-JSON, wrong top-level shape, deeply nested garbage) proving a clean 4xx rather than a panic or hang. SQL-injection-style content in free-form strings (name, participant id) proving parameterized queries hold -- verified by a second, unrelated registration succeeding normally afterward, not just by the first call not erroring.
- **A real bug found by this pass, not a false alarm -- and a real, separate pre-existing discrepancy it led to, filed separately (T-91):** the schema-violations test's own expectation that extra, undeclared fields would be ALLOWED (matching pkg/validation's doc comment) was wrong -- the actual, real behavior (queryfy.Strict, copied directly from pkg/validation's real code before this was written) rejects them. handleDxpTxnCreate's own code was already correct, matching the real behavior; the test's expectation was the bug, now fixed. The doc-comment-vs-code discrepancy in pkg/validation itself is real, pre-existing, and outside this session's scope to fix -- filed as T-91.
- **A second real issue found, diagnosed properly rather than dismissed, and fixed at the actual root cause -- a genuine test-design gap, not a production bug:** the concurrent-load tests initially failed intermittently under full-suite parallel contention with XOLU-ST006 ("Failed to initialise tenant context"). Traced precisely rather than assumed: 50 simultaneous FIRST-TOUCH requests to a never-before-accessed tenant race TenantAutoRegister's own GetOrRegister path, a separate, pre-existing piece of shared infrastructure this test was never meant to be about -- conflated with allocDXPID's own concurrency safety, which is what the test actually exists to prove. Direct correction from the user: these tests don't run correctly without first setting up proper tenant access. Fixed by warming up the tenant with one sequential request before the concurrent burst begins, isolating what the test is actually testing. Confirmed clean 5/5 full-suite runs and clean under -race after the fix -- not a production concern (a real tenant is provisioned once, then used; it is never hammered concurrently on its very first touch in practice).
- **Verified exhaustively:** all 11 new adversarial tests plus the full pre-existing T-87/T-89 suite green; the two concurrent tests specifically green under -race; full tree build/vet clean; a genuine go test ./... sweep across the whole tree, re-run multiple times given the intermittent failure investigated above, all clean after the fix.
- Not started: nothing further -- this item is the adversarial pass itself, complete for dxp/def and dxp/txn's current scope. Item 21's own coordinator, once built, will need its own adversarial pass in turn, not covered by this one.

Cross-ref: CHANGELOG 0.19.2.

## [0.19.2] T-89 — POST /dxp/txn implemented (item 20, wave 5): bindings_schema validation, jsonplate-based param resolution, and instance snapshotting -- proven end to end, not just designed (v0.19.2, 2026-07-30)

Theme: dxp · closed 0.19.2 · 2026-07-30


- **Trigger:** direct instruction to continue implementing dxp/txn (item 20's own remaining scope), following the bindings_schema/jsonplate design settled across several turns of back-and-forth (corrections: the "slot" naming replaced with the doctrine's own "bindings" term; the recognition that POST /dxp/txn is one complete, stateless invocation -- closer to a stored procedure call than an assembled SQL transaction -- rather than one instance built up incrementally, itself a second concrete instance of the ACID-bias pattern named in dxp-coordinator-design.md §13).
- **Scope: POST /dxp/txn implemented and tested end to end -- def lookup, bindings validation against bindings_schema_json (skipped entirely when absent, matching JSONSchemaValidator's own "no schema means validation passes" behavior), jsonplate.Render resolving every participant's {"$ref": ...} against the caller's bindings, and the resolved result snapshotted into a new dxp_txn row.** GET/list/delete for txn instances not built -- matches the same, already-recorded gap for dxp/def itself (T-87).
- **dxpTxnSnapshot mirrors fsmMachineSnapshot's own "clone the whole spec, not just part of it" principle, checked directly against handleFSMMachineCreate before choosing this shape** -- Pattern, resolved Participants (concrete values, never templates by the time this is stored), and PhaseTTL, all cloned into snapshot_json at creation, insulating the instance from any later re-registration of the same-named def.
- **deadline_ns computed as ot.Now().UnixNano() + the parsed phase_ttl.reserve duration** -- an absolute deadline in the same unix-nanoseconds-UTC convention as dxp.Claim.Deadline (checked directly against pkg/dxp/dxp.go before choosing this, not assumed), not a bare relative duration.
- **A real, deliberate divergence from the doctrine's own worked-example syntax, decided explicitly rather than followed reflexively:** participant params use pkg/jsonplate's native {"$ref": "path"} object form, not the doctrine's bare "$qty" string convention -- jsonplate requires the object form specifically to distinguish a reference from a literal string value unambiguously, and reusing an already-built, already-tested mechanism was preferred over a parallel one that only looks similar. Same reasoning that led dxp_defs' own id scheme to fsm's real pattern over the doctrine's aspirational def_id-plus-version one.
- **9 new HTTP-level tests (external package server_test, real requests against a real running server):** the critical one, TestDxpTxnAPI_Create_ResolvesBindingsIntoSnapshot, proves the full pipeline end to end -- a {"$ref": "amount"} template resolves to the concrete bound value 150 in the actual response, not merely asserted to work by design. Also: unknown def_id refused (404), bindings failing schema refused (422), a def with no bindings_schema declared skips validation entirely and still succeeds, and two separate calls to the same def correctly get independent instance ids -- proving the "many separate, complete calls" reading directly rather than just documenting it.
- **A real, unrelated flake observed and investigated, not ignored:** the first full-tree sweep after this work showed pkg/timeseries's TestDeleteTimeline_DeletingMarker/concurrent_reader_during_delete_never_sees_defined-but-empty failing once. Checked directly rather than assumed unrelated: pkg/timeseries has zero dependency on anything touched this session; the test passed cleanly 5/5 times when re-run in isolation; a second full-tree sweep passed clean with no reproduction. Consistent with a pre-existing, load-sensitive flake surfacing only under full-suite parallel contention, not a regression from this work -- filed separately (T-90) for visibility rather than left unrecorded.
- **Verified exhaustively:** full pkg/server suite green; full tree build/vet clean; a genuine go test ./... sweep across the whole tree, re-run twice given the timeseries flake, both times clean apart from that one isolated, non-reproducing failure.
- Not started: the attendance protocol itself and the sweep-worker extension for expired/torn dxp_txn instances -- item 21's actual orchestration logic, which this item (registration and instantiation) is a real prerequisite for, not a substitute.

Cross-ref: CHANGELOG 0.19.2.

## [0.19.2] T-88 — dxp.Participant.Execute migrated to ParticipantStore/Result across the whole codebase (item 21, wave 5): core interface, all four adapters, every test file -- plus a real, pre-existing interface-guard gap found and closed (v0.19.2, 2026-07-30)

Theme: dxp · closed 0.19.2 · 2026-07-30


- **Trigger:** direct instruction to continue implementing item 21 (the coordinator), following T-87's dxp/def core and the prior turn's ParticipantStore/SQLStore/PebbleStore design (docs/proposals/dxp-coordinator-design.md §2).
- **Scope: dxp.Participant's Execute signature migrated across the entire codebase -- the core interface plus all four existing adapters plus every test file calling it, with zero remaining old-signature callers anywhere.** Execute(ctx, tx *sql.Tx, c Claim) error -> Execute(ctx, store ParticipantStore, c Claim) (Result, error), matching §2/§10 exactly.
- **A genuine, pre-existing gap found and closed, not introduced by this change:** none of the four adapters (bal, cal, fsm, entity) had a compile-time `var _ dxp.Participant = (*Adapter)(nil)` guard before this. Proven concretely, not asserted: after the interface's own signature changed, `go build ./...` still succeeded across the whole tree -- the four adapters had silently stopped satisfying dxp.Participant, and nothing caught it, because nothing yet requires any concrete type to satisfy the interface at compile time (no coordinator exists to consume them as such). Added the guard to all four adapter files as part of this migration; going forward this class of drift fails the build immediately rather than staying silent until some future caller discovers it.
- **Ready() placement, deliberate, checked against the design doc rather than guessed per-adapter:** called immediately before each adapter's actual write (transferInTx for bal, putBookingInTx for cal, the resolve-then-apply pair for fsm -- placed before the FIRST of the two writes, since that's genuinely when the participant starts touching the store -- and the update/append switch for entity), never at the top of Execute alongside the pending-lookup and claim-sum logic, which is internal work, not yet "about to write."
- **Unused `database/sql` imports removed where the compiler actually confirmed they were unused, not assumed per-file:** bal and fsm's adapter files, both files entirely within pkg/storage but each with its own import list -- cal's own dxp_adapter.go also lost it. entity's own file kept the import, confirmed still needed for entityVersion's sql.ErrNoRows check (Reserve/Validate side, untouched by this migration) -- checked directly rather than removed reflexively alongside the other three.
- **Test-file migration, five files, done individually rather than by blind batch substitution after one real near-miss:** pkg/bal/dxp_adapter_test.go (3 call sites -- one had a lowercase "execute:" error message differing from the other two's "Execute:", which a first-pass identical-string replace missed entirely; caught by re-grepping after the fix rather than assuming success), pkg/cal/dxp_adapter_test.go (1), pkg/storage/entity_dxp_adapter_test.go (5, handled via a regex matching on captured variable name and indentation rather than hand-crafted per-occurrence strings, given they genuinely varied), pkg/storage/fsm_dxp_adapter_test.go (3, same regex approach). pkg/dxp/integration/multiparticipant_test.go (6 call sites across both its tests) required a DIFFERENT constructor -- NewSharedSQLStore, not NewSQLStore -- since both tests share one literal *sql.Tx across all three participants and commit it exactly once, externally, matching the doc's own collapsed-to-ACID case precisely, not the independent case the other four files' tests exercise.
- **Verified exhaustively:** full pkg/bal, pkg/cal, pkg/dxp, pkg/dxp/integration, pkg/storage, pkg/server suites green; the adapter and multi-participant tests specifically green under -race; full tree build/vet clean; a genuine go test ./... sweep across the whole tree, not just the packages touched.
- Not started: the attendance protocol itself, dxp_txn instance persistence, and the sweep-worker extension -- this item is the interface/adapter migration only, a real prerequisite for those, not those themselves.

Cross-ref: CHANGELOG 0.19.2.

## [0.19.2] T-87 — dxp/def core implemented (item 20, wave 5): schema, registration, validation, static analysis, POST /dxp/def -- list/get/delete not yet built, a real remaining gap (v0.19.2, 2026-07-30)

Theme: dxp · closed 0.19.2 · 2026-07-30


- **Trigger:** direct instruction to proceed with implementing dxp, following the extended design/verification pass on item 20's own identification scheme, validation checklist, and item 21's coordinator design.
- **Scope: item 20's core (registration, validation, static analysis) -- not its full CRUD surface.** POST /dxp/def implemented and tested end to end. GET (list/single) and DELETE are not built -- the doctrine's own item 20 wording ("registration, validation, versioning, static analysis") does not require them, and the coordinator's own need ("look up a def by id when a txn instance references it") is a simpler internal function, not necessarily a full HTTP surface. Noted as a real, smaller remaining gap, not silently complete.
- **Schema (S15-dxp, pkg/storage/sqlite.go's InitV2Schema): dxp_defs, dxp_txn, dxp_id_seq.** Mirrors fsm_definitions/fsm_machines/fsm_id_seq structurally -- checked directly against the real schema before writing any of this, not approximated from the doctrine's own worked examples (which use a different, aspirational def_id-plus-version scheme fsm itself doesn't actually implement -- corrected twice in conversation before settling this). dxp_defs.analysis_json is persisted, matching fsm_definitions' own precedent -- an earlier pass of this design reasoned this shouldn't be persisted ("cheap to recompute, risk of drift"), which doesn't hold once checked: definitions are insert-only and immutable, so there is nothing for a persisted analysis to drift from. dxp_txn is the durable instance record docs/proposals/dxp-composed-commitment.md's own §7 assumes -- checked directly that item 18 never built it, nothing here duplicates existing work. status is active/committed/expired only -- no separate torn state, matching dxp-coordinator-design.md §6's own resolution (a torn instance falls into ordinary expired handling).
- **pkg/server/v2_dxp_def_handlers.go: dxpParticipantSpec/dxpDefSpec/dxpPhaseTTLSpec/dxpAnalysis, dxpPrimitiveOps/dxpEngineOf (a real registry checked directly against all four existing adapters' concrete OpParams types, not assumed), validateDxpDef, parsePhaseTTL (a narrow, purpose-built ISO 8601 subset parser -- no such parser existed anywhere in the codebase, and a general one would be over-engineering for what phase_ttl actually needs), allocDXPID (the exact allocFSMID pattern, checked directly before writing), and handleDxpDefCreate (mirrors handleFSMDefCreate's exact structure, reuses s.fsmDB(r) directly rather than duplicating it -- despite the name, nothing about that function is fsm-specific).**
- **A real design correction kept visible in the code itself, not just in conversation:** dxpAnalysis deliberately keeps two facts separate that an earlier pass of this conversation conflated into one -- CollapseEligible (@D06's own actual, verbatim condition: single-tenant) and EngineHomogeneous (this validator's own explicit inference: no non-SQL participant present). The coordinator (item 21) will need to consult both, not just @D06's own.
- **Canonical participant ordering is absent from this checklist entirely, deliberately, not by oversight** -- worked through directly and confirmed unnecessary (dxp-coordinator-design.md §12): Reserve never blocks, so no circular wait is possible regardless of ordering.
- **entity's op name diverges from its own Go type name, deliberately:** "create" (matching the doctrine's own worked example, the more legible choice for a def author) maps to EntityAppendParams (matching entity's own internal CommitAppend vocabulary, T-84's own naming decision) -- two different audiences, allowed to diverge.
- **"ts" is deliberately absent from dxpPrimitiveOps and dxpEngineOf** -- real primitive, but no adapter exists yet (T-86). Confirmed a def naming it is correctly refused today, by test.
- **20 new tests total: 15 unit (internal package server, testing validateDxpDef/parsePhaseTTL/allocDXPID directly, using the doctrine's own hotel worked example as the canonical valid case rather than an invented one) plus 5 HTTP-level (external package server_test, real requests against a real running server, real 201/422 status codes) -- covering the hotel example succeeding, two registrations under the same name correctly getting independent ids (fsm's own no-uniqueness-on-name precedent, proven not just cited), invalid pattern refused, unknown primitive (ts) refused, and entity's "create" op accepted.** All passed on first run for both suites -- no bugs found this time, unlike cal's adapter (T-82), plausibly because this is pure in-memory validation plus a well-proven sequence pattern, without cal's transaction-boundary complexity.
- **Verified exhaustively:** full pkg/server suite green (both internal and external test packages); full tree build/vet clean; a genuine go test ./... sweep across the whole tree, not just the packages touched.
- Not started: item 20's remaining CRUD surface (list/get/delete), and everything in item 21 (the coordinator itself) that would actually consume a registered def.

Cross-ref: CHANGELOG 0.19.2.

## [0.19.0] T-84 — Entity CREATE as a dxp op (item 38, wave 5): EntityAdapter is UPDATE-only today; needed for the hotel worked example's entity leg and for the rescoped 3PS coordinator to be complete across all four substrates (v0.19.0, 2026-07-29)

Theme: dxp · closed 0.19.0 · 2026-07-29


- **Trigger:** direct instruction (2026-07-29) -- "entity create, and the invalidation cases" needed as part of "a complete 3ps implementation across all substrates."
- **Scope, plan item 38:** EntityAdapter (pkg/storage/entity_dxp_adapter.go) is UPDATE-only today -- checked directly, EntityUpdateParams's own doc comment states CREATE "is future work, not folded in here." The proposal's own worked example (dxp-composed-commitment.md section 3, the place_order def) lists an entity participant with "op": "create" -- a real gap between the doc's own illustration and what item 19 actually built, found while answering a direct check-only question before any implementation started.
- **Why CREATE is a distinct admission shape, not a trivial extension (per EntityUpdateParams' own doc comment, quoted precisely):** "reserving a not-yet-taken id before another writer claims it... existence must be false, not true, and the sequence-allocation timing in saveInTx's create branch wants its own thought." UPDATE's admission check confirms a row EXISTS and matches an expected version; CREATE's admission check must confirm a row does NOT exist yet, and must reserve the identity (likely a sequence-allocated id) against a competing Reserve racing for the same slot -- a different race shape than anything item 19 built for bal/fsm/cal/entity-update.
- **Relationship to item 21 (dxp coordinator, being rescoped to 3PS-only in the same instruction):** filed as a companion/prerequisite to the rescoped coordinator, not an afterthought -- the coordinator's own exit criterion (the hotel worked example, per @D05a) specifically needs entity's leg to be a real CREATE, matching the proposal's own example, not a stand-in UPDATE.
- **Implemented (2026-07-29).** Matched entity's own vocabulary rather than inventing a parallel term -- the new type is EntityAppendParams, not "Create", mirroring CommitAppend (the existing non-dxp /commit path's identical type) exactly. Both explicit-id and auto-generated-id shapes covered, matching CommitAppend's own documented contract precisely: explicit id refuses via ErrAlreadyExists if taken, sharing dxpEntityResource's resource-key namespace with EntityUpdateParams so an update and a create racing for the same id correctly contend for one claim rather than two independent ones (proven directly by TestEntityAdapter_Reserve_UpdateAndAppendShareResourceNamespace, not just asserted); auto-generated id has no real conflict to guard (the sequence table's own atomic allocation makes collision impossible by construction, proven directly by TestEntityAdapter_Reserve_ConcurrentAutoIDsNeverCollide running two real Executes and checking two distinct rows land) so Reserve holds a txn-scoped, inherently-unique resource key purely for the coordinator's own bookkeeping uniformity, not because there's anything to guard.
- **The write path needed no new tx-scoped function, unlike bal and cal** -- createInTx already existed (built for the /commit HTTP path) and was checked directly, not assumed, to be genuinely tx-scoped throughout before reuse: every helper it calls (adaptedCreate, syncGraphEdges, indexForFTS, and the in-memory-only AdaptedRegistry.Get) was read in full, confirming none of them fall back to a non-tx read the way cal's putBookingInTx originally did (T-82's own found-and-fixed deadlock). No repeat of that bug this time -- the careful reading this session settled on doing before writing any code appears to have paid off directly.
- **9 new tests** (pkg/storage/entity_dxp_adapter_test.go), all passing alongside the 6 pre-existing update-path tests unchanged: Reserve success for both id shapes, refusal when an explicit id already exists, the shared-namespace cross-conflict test, Validate catching a competitor's explicit-id create after Reserve consented, Execute creating real rows for both shapes (verified via a fresh read, not just "no error"), and the concurrent-auto-id-never-collides test actually running two Executes rather than asserting the reasoning alone.
- **Verified exhaustively:** full pkg/storage suite green (33.8s); the entity adapter tests specifically green under -race; pkg/dxp and pkg/dxp/integration green, including both multi-participant hotel-style tests (unaffected by this change, confirmed rather than assumed); full tree build/vet clean; a genuine go test ./... sweep across the whole tree -- clean, every package.
- **Roadmap's own effort estimate:** ~1.5 days.

Cross-ref: CHANGELOG 0.19.0.

## [0.18.0] T-82 — cal dxp adapter (item 19, wave 5, participant #4 of 4): day-bucket resource keys, full cross-path guarantee, plus two real deadlocks found by running the tests and fixed at the source (v0.18.0, 2026-07-29)

Theme: dxp · closed 0.18.0 · 2026-07-29


- **Trigger:** direct instruction to resume dxp work -- item 19's fourth and last participant adapter, cal, left blocked since earlier this session on SQLiteBookingSource having no transactional write path.
- **Prerequisite fixed first:** pkg/cal/sqlitesource.go gained putBookingInTx (mirroring bal.transferInTx exactly) via a shared validatePutBooking helper refactored out of PutBooking, so both the tx and non-tx write paths enforce identical mode/bearer rules rather than risk drift between two copies.
- **New file pkg/cal/dxp_adapter.go:** CalTransitionParams (dxp.OpParams), Adapter (dxp.Participant) with Reserve/Validate/Execute/Release, matching bal/fsm's established shape precisely -- a pending map[txn]OpParams stashing the full reservation, since dxp.Claim is deliberately resource-shaped not op-shaped (same reasoning bal's own adapter doc already states).
- **Design confirmed, not re-derived:** day-bucket resource keys ("cal:<calendarID>:<YYYY-MM-DD>", matching dxp.Claim.Resource's own doc example verbatim) via SpanDays, the same day-decomposition cal's own occupancy index already uses. A booking spanning N days holds N claims under one txn. Coarser than cal's 5-minute occupancy quantum -- a documented, deliberate approximation (T-54's own register note already named this the hardest remaining piece; the approximation itself was settled before this session's implementation pass, not invented now).
- **The cross-path guarantee, both directions, not just the easier half:** Reserve checks spanConflicts against H1 (a live ORDINARY booking must block a new dxp reservation) AND the mixed-weight admission rule against every day-bucket's live dxp claims (a live PESSIMISTIC claim blocks any new reservation of either weight; a live OPTIMISTIC claim only blocks a new PESSIMISTIC one -- the same rule fsm's own adapter already established). BookingConflictError (new) reports which side refused.
- **Two real, serious deadlocks found by actually running the tests, not by reasoning about the code -- confirmed with goroutine stack traces, not guessed at:**
  1. Adapter.Execute originally called a.src.calendar(...) -- a NON-transactional read through the store's own connection -- while the coordinator-supplied *sql.Tx was still open on the same underlying *sql.DB. SQLite blocked the second connection waiting for the first to release; the test hung forever rather than failing. Fixed architecturally, not patched: the target State is now resolved ONCE during Reserve and stored in pending, so Execute never needs a second database read at all.
  2. Deeper, found on the SECOND test run after the first fix: putBookingInTx ITSELF had the identical bug, one layer down -- its shared validatePutBooking call did s.calendar(...) internally, the same non-tx read, deadlocking any caller that invokes putBookingInTx while holding tx open, which is putBookingInTx's entire reason to exist. This was a bug in putBookingInTx as built earlier this session, not specific to the adapter -- every future caller would have hit it. Fixed at the source: added calendarInTx (tx-scoped calendar lookup) and parameterized validatePutBooking's existence check as an injected closure, so PutBooking and putBookingInTx share the identical mode/bearer validation logic while each supplies the connection-appropriate existence check.
- **Six new tests** (pkg/cal/dxp_adapter_test.go), against a real SQLite-backed store, not MemBookingSource, since putBookingInTx is specifically what needed proving: Reserve success; a 3-day span correctly holding 3 day-bucket claims; Reserve refused by a live ORDINARY booking (cross-path, ordinary-blocks-dxp direction); Reserve refused by a live dxp claim (dxp-blocks-dxp direction); Execute writing a real booking through an actual shared *sql.Tx end to end, then verified via a fresh read after commit; Release's idempotent-on-repeat contract.
- **Verified exhaustively, applying the exact lesson from the 0.17.0 release (build+vet is not sufficient verification on its own):** the specific test that hung was re-run in isolation with a hard timeout after EACH fix attempt to prove the deadlock was actually gone, not just that the code compiled differently; the full six-test suite run together (matching the original failure's actual trigger -- it never hung running alone, only as part of the full run); the complete pkg/cal suite green under -race (31s); full tree build/vet clean; a genuine go test ./... sweep across the whole tree, not just the packages touched -- clean, every package.
- **Item 19 status: 4 of 4 participants done (bal, fsm, entity, cal).** Item 18's own core is done (T-54); items 20-23 (defs, coordinator, 3PS, client surface) remain, per T-54's own "blocks 19-21" note now partially discharged.

Cross-ref: CHANGELOG 0.18.0.

## [0.17.0] T-65 — Prefix-collapse retention (item 16, wave 4 complete): PruneJournal + re-scoped rebuild oracles (all three needed it, not just the one T-58 flagged), plus iolu bal prune wiring (v0.17.0, 2026-07-28)

Theme: bal · closed 0.17.0 · 2026-07-28


- **Trigger:** continuation of "finish bal properly" (T-64's own note flagged this as the remaining wave-4 gap). Design grounded precisely against chronicle-substrate.md §4b (the finiteness law) before writing anything, not assumed: "entries older than a sealed checkpoint are derivationally redundant... policy may archive-then-prune... the rebuild oracle re-scopes to the earliest retained checkpoint."
- **Scope decision, explicit:** Go-only, no HTTP route -- recorded in docs/KNOWN_ISSUES.md's bal section (destructive, irreversible operation; operations-shaped, not a routine API call). cmd/iolu's `bal prune` command is the operator-facing path, wired directly to the Go function rather than duplicating its logic. Archival ("optional everywhere, never a precondition for correctness" per §4b) deliberately not implemented -- prune only.
- **New file pkg/bal/prune.go:** PruneJournal(ctx, before time.Time) (int, error), tenant-wide across every postable account. `before` is a caller-supplied retention FLOOR, not a ceiling the seal doesn't already impose -- an account is never pruned past what's actually sealed regardless of `before`; `before` only lets a caller retain MORE than the seal strictly requires, never less.
- **All THREE existing rebuild oracles needed re-scoping, not just the one T-58 had already flagged.** T-58's own "not solved here" note named VerifyCheckpoints specifically. Checking the other two before writing anything found they have the identical unconditional-from-epoch assumption: GlobalFoldOracle sums the whole journal per account against the current balance directly; VerifyChains asserts every account's first-seen entry has version==1, which becomes a false positive on every pruned account once entries before it are gone. All three fixed:
  - VerifyCheckpoints: delta between consecutive RETAINED checkpoints instead of an absolute-from-epoch sum.
  - GlobalFoldOracle: baseline from each account's latest checkpoint (0 if none) plus journal strictly after it, instead of the whole journal unconditionally.
  - VerifyChains: the version==1 assertion is skipped only when a checkpoint whose balance matches the entry's own previous_balance precedes it -- the signal a real prune happened, not evidence of a genuine gap. Arithmetic and linkage checks (prev+amount==cur, consecutive-entry linkage among what's retained) are untouched; only the single "was this truly entry #1" assertion needed re-scoping. Deliberately avoided a schema change (a version column on checkpoints) that an earlier pass through this had assumed was necessary -- re-examined and found unnecessary.
  - All three rewrites verified behaviour-identical to the originals when nothing has ever been pruned: full pre-existing pkg/bal suite green, unmodified, immediately after each rewrite.
- **Two real bugs found and fixed during implementation, not just theorised in the design pass:**
  1. Boundary bug: PruneJournal's first draft checked chronicle.Sealer.Sealed(checkpoint.at) to decide prune-eligibility. A checkpoint at a month boundary (e.g. 2026-07-01T00:00:00Z, sealing June) is, under MonthWindows' half-open convention, the FIRST instant of July -- so Sealed() was answering "is July sealed?" not "is this checkpoint at or before the frontier?". Fixed by comparing directly against Sealer.Frontier() instead of routing through the window-containing-a-point check, which answers a genuinely different question.
  2. VerifyCheckpoints' first draft treated an account's first-retained checkpoint as having a zero baseline unconditionally -- correct only when nothing was ever pruned. Once PruneJournal removes a checkpoint's own covering evidence, that checkpoint becomes permanently unverifiable BY DESIGN (chronicle-substrate.md §4b: "forgetting is not editing") -- not corrupt, just untraceable. The fix distinguishes "genuinely zero activity before this checkpoint" (verifiable, real corruption still caught) from "evidence was pruned" (trusted as a genesis point, not flagged) by checking whether ANY covering journal evidence remains -- unambiguous given PruneJournal only ever deletes a full range at once, never partially.
- **Nine new tests, verified with the same discipline as everything else this session -- teeth proven by reverting and confirming the exact predicted failure, then restoring:**
  - pkg/bal/prune_test.go (5): removes only sealed-covered entries; a retention floor can keep MORE than the seal requires; unsealed data is never touched; conservation and all three oracles survive pruning (not just "balance still looks right"); VerifyChains still catches a genuine corruption on a RETAINED entry after pruning (proving the relaxation isn't a blindfold). The conservation/oracle-survival test's fix was reverted and confirmed to reproduce the exact predicted false divergence before being restored.
  - cmd/iolu/balprune_test.go (3): --yes gates all database mutation (verified the row count is provably unchanged without it); --yes actually prunes the full sealed history; the retention floor reaches PruneJournal correctly through the whole iolu command-line wiring, not just when permissive. Run through the real command functions against a real per-file SQLite tenant store, not mocked.
- **New command: iolu bal prune --base-dir <dir> --before <RFC3339> [--yes].** Registered in main.go's dispatch, usage doc comment, and printUsage. No separate "dry-run" mode -- a true dry run would require PruneJournal itself to accept an external, rollback-only transaction, which was judged more invasive than the ask warranted; --yes as an explicit confirmation gate (default: report which tenants have anything eligible, touch nothing) was chosen instead as the simpler, safer, non-duplicating path.
- **Documented in docs/KNOWN_ISSUES.md** (bal section): the Go-only decision, why, and what would need to be designed (an explicit confirm/dry-run parameter shaped for the risk) before ever adding HTTP access.
- **Verified exhaustively:** full tree build/vet clean in single whole-tree passes; complete stress-tagged pkg/bal suite green under -race; complete cmd/iolu suite green; the real iolu binary built and exercised end-to-end against a real per-file SQLite tenant store (not just in-process function calls), correctly identifying which tenants have bal tables and pruning exactly the expected two journal rows (one seeded transfer, two account legs).

Cross-ref: CHANGELOG 0.17.0.

## [0.17.0] T-64 — Seal-frontier enforcement (item 16 seal/close, wave 4): bal/close now actually seals a period tenant-wide (XOLU-BAL003), reusing chronicle.Sealer built in wave 3 for exactly this (v0.17.0, 2026-07-28)

Theme: bal · closed 0.17.0 · 2026-07-28


- **Trigger:** direct instruction to work toward completing bal (wave 4 exit criteria). Checked the actual gap against the wave 4 plan's own list ("chain triple, inline memo, hierarchical accounts, int64 doctrine, checkpoints, seal, prefix-collapse") before assuming scope: chain triple, inline memo, int64 doctrine, and checkpoints (T-51/T-58) were already done. Seal and prefix-collapse were not. This item is seal only; prefix-collapse retention remains open (see T-65 gap note below) and the Go client library surface remains a separate, unstarted gap.
- **What "seal" actually required, per bal-conservation-primitive.md §7, checked precisely rather than assumed:** bal/close previously just wrote a single account's checkpoint (a thin wrapper around Store.Checkpoint) -- it enforced nothing. The design specifies a tenant-wide seal frontier: a closed period rejects ANY entry dated within it (XOLU-BAL003), unconditionally -- independent of an account's own temporal_policy, which is a separate, per-account axis. The error code was already reserved in pkg/errors but never referenced anywhere in pkg/bal before this.
- **Reused existing, purpose-built infrastructure rather than inventing new machinery:** chronicle.Sealer was extracted from cal in wave 3 specifically anticipating this consumer -- its own doc comment: "bal's period close (item 16) is the first native consumer, sealing calendar months." chronicle.MonthWindows already exists, sized exactly for bal's period shape. Both had been sitting unused.
- **New file pkg/bal/seal.go:** InitSeal (persistence table DDL, separate from Init/InitRollup for the same reason both of those are separate -- planes can exist independently), LoadSealer (package-level constructor recovering the persisted frontier from SQL -- chronicle.Sealer itself is memory-only by design, "recovery is the consumer's" per its own doc), SetSealer (setter mirroring SetRollupPebble/SetClaimsCache exactly), SealPeriod (advances the frontier, persists the POST-AdvanceTo value so a stale or repeated call can never regress it, checkpoints every postable account), and SealedPeriodError (XOLU-BAL003).
- **A real design tradeoff made and stated, not hidden:** chronicle.Sealer.Guard holds the seal lock across a caller-supplied closure -- the fully correct way to serialise a mutation against a concurrent AdvanceTo. transferInTx does NOT use it: wrapping the two-leg guarded UPDATE (already tested, working admission logic) in a closure for a rare event (bal/close is a deliberate, human-triggered administrative action, not high-frequency contention) was judged not worth the rewrite risk. Instead, transferInTx does an early, unlocked Sealer.Sealed(at) check. This leaves a narrow race -- a transfer already past the check when AdvanceTo runs can still commit into what becomes sealed a moment later. Documented in seal.go's own doc comment as an accepted tradeoff, not an oversight.
- **Production wiring, same shape as T-62's rollup handle:** Server gained balSealer sync.Map (one *chronicle.Sealer per tenant, loaded once inside the existing per-tenant sync.Once, re-attached to every request's freshly-built Store via SetSealer) -- necessary for the same reason the rollup handle needed it: bal.Store is built fresh per request, but the Sealer must be the SAME long-lived instance across requests for AdvanceTo's monotonicity to mean anything.
- **API shape change, deliberate, not accidental:** POST bal/close no longer accepts account_id -- sealing is tenant-wide per the design doc, not per-account, which the endpoint's own PREVIOUS doc comment already claimed ("advances the account-set's seal frontier") without actually doing. Response changed from {account_id, checkpoint} to {sealed_through, accounts_closed}. The existing test (TestBalAPI_AsOfAndClose) happened to keep passing unmodified since it never asserted the specific response shape, only behaviour.
- **Six new tests, all verified to have real teeth by reverting the fix and confirming the predicted failure, then restoring:** five pkg/bal unit tests (SealPeriod checkpoints only postable accounts; entries within a sealed period are refused; sealing overrides backdated policy -- the single most important property test, since a policy override would silently downgrade sealing to advisory; no sealer attached preserves exact pre-seal behaviour; LoadSealer recovers a persisted frontier across a fresh instance, simulating a process restart) plus one pkg/server HTTP-level test proving the balSealer cache actually survives across separate real HTTP requests, not just in isolated unit construction.
- **Verified exhaustively:** full tree build/vet clean; complete stress-tagged pkg/bal suite green under -race; complete pkg/server suite green (54s, not just the bal-filtered subset); cmd/iolu green (constructs bal.NewStore directly, unaffected).

Cross-ref: CHANGELOG 0.17.0.

## [0.17.0] T-63 — More stringent/adversarial bal tests: ceiling race, dxp cross-path race under real concurrency -- surfaced and fixed a latent flakiness bug in the pre-existing floor race test (v0.17.0, 2026-07-28)

Theme: bal · closed 0.17.0 · 2026-07-28


- **Trigger:** direct request, following T-62's rollup migration, to add more stringent and adversarial tests to bal generally -- not scoped to the rollup plane specifically.
- **Coverage gaps identified before writing anything, not assumed:** checked the existing test files first. `TestBalAdmission_Race` (admission_race_stress_test.go) only ever raced claimants against the floor boundary -- the ceiling side of the guard (structurally distinct SQL: COALESCE'd ceiling, addition instead of subtraction) had zero adversarial coverage. `TestOrdinaryTransfer_RespectsLiveDxpHold` (dxp_adapter_test.go) proves the dxp/ordinary-path cross-path guarantee, but entirely sequentially -- Reserve, then Transfer, one after another -- never under actual concurrent race pressure, which is exactly where a subtle admission bug would hide.
- **Three tests added, all stress-tagged (G-13 dormant-guard pattern) except the third:**
  - `TestBalAdmission_Race_Ceiling` -- mirrors the floor test on the ceiling boundary, 32 concurrent claimants for the last unit of ceiling headroom.
  - `TestOrdinaryTransfer_RespectsLiveDxpHold_Race` -- 32 concurrent ordinary Transfer attempts racing against a live pessimistic dxp hold covering the entire balance; every one must be refused, none may slip through a race window. Verified 15 consecutive -race passes (480 total concurrent attempts across the runs).
  - `TestTransfer_DegradesGracefullyWithoutRollupPlane` (not stress-tagged; deterministic) -- filed under T-62 above, cross-referenced here since it was written in the same pass.
- **A real, previously-unnoticed flakiness bug found in the PRE-EXISTING floor test, not introduced by this work.** Writing the ceiling test and running both together surfaced intermittent failures on BOTH tests with XOLU-BAL006 (the anti-backdating guard), not the expected BoundsError. Root cause: concurrent goroutines each calling time.Now() independently can commit out of timestamp order, tripping the backdating guard as an unintended confound on what's supposed to be a pure floor/ceiling admission race. This was latent in the original TestBalAdmission_Race the whole time -- rare enough under this sandbox's scheduling not to have been caught before, not something this session's work created.
- **Fix:** set Policy: "backdated" on every account that receives concurrent writes in both tests -- initially only fixed one side per test (the primary "hot" account) and had to go back for the other leg (sink/source), which also receives 32 concurrent writes and is equally exposed. Isolates the property under test (floor/ceiling admission) from timestamp ordering, which the tests were never meant to exercise. The dxp cross-path race test did not need this fix -- it uses the package's shared fixed `now` constant rather than a fresh time.Now() per goroutine, so the confound cannot arise there; confirmed this reasoning by verification (15 consecutive -race passes), not just by argument.
- **Verified exhaustively:** full tree build/vet clean; the complete stress-tagged pkg/bal suite (34 tests, not just the new ones in isolation) green under -race in one pass; the non-stress pkg/bal suite green under -race; the fix for the pre-existing floor test's flakiness verified with 15 consecutive -race runs showing zero failures, versus failing on the first of 3 runs before the fix.

Cross-ref: CHANGELOG 0.17.0.

## [0.17.0] T-62 — bal's rollup plane (@B05) is SQL-resident despite being non-guard-bearing and its own design doc saying it should be Pebble -- chronicle.Engine was built storage-agnostic for exactly this case (v0.17.0, 2026-07-28)

Theme: bal · closed 0.17.0 · 2026-07-28


- **Trigger:** working through the cal dxp adapter's storage-engine question, corrected twice by the user on the actual rationale (guard locality, not "SQL for ACID") and then asked directly why bal/cal use SQLite at all given performance was supposedly the point of using Pebble for them. That question led to checking bal's rollup plane specifically, which turned out to be a real, confirmed defect, not just a framing question.
- **The law, stated precisely (docs/proposals/chronicle-substrate.md, "guard locality"):** a guard's read and its authorised write must commit atomically together, in whatever engine hosts them. This is engine-agnostic -- it does not require SQL, only co-location with the guard's own transaction. cal's H1 (SQLite, guard-bearing) / H3 (Pebble, advisory, "no guard ever consults it") split is the doc's own worked example of this law applied correctly.
- **The defect:** bal's rollup/checkpoint plane (@B05) is, by its own doc comment (pkg/bal/rollup_store.go), explicitly non-guard-bearing -- "DERIVED, never authoritative: no guard consults it (@C04a)". By guard locality's own logic this puts it in exactly cal's H3 category: free to live in Pebble. chronicle.Engine's BucketStore interface was deliberately built storage-agnostic for this reason (store_contract.go: "SQL-plane store at wave 4; any Pebble-plane store"). bal's own design proposal (bal-conservation-primitive.md) states in its comparison table that rollup deltas live "in the Pebble plane." None of that happened: the shipped bucketStore (pkg/bal/rollup_store.go) is 100% SQL, using s.db directly for every Get/Put/Delete. Every rollup write today lands on the same SQLite WAL as the guard-bearing journal writes, for a plane that the substrate's own law says never needed to be there.
- **What this is NOT:** bal's journal (the authoritative ledger the admission guard reads) is correctly SQL-resident today, because the guard is implemented as a SQL-transaction read (chronicle-substrate.md's own bal example). That's a narrower, defensible, implementation-level choice -- whether it COULD have been built Pebble-native instead (using Pebble's own atomic batch to satisfy guard locality in a different engine) is a real, open architectural question, but not a confirmed contradiction the way the rollup plane is. Keeping these two claims separate deliberately: one is checked and proven, the other is speculative and un-investigated.
- **Impact:** bal's WAL carries load from a plane specifically designed to not need to be there, contending with the guard-bearing journal writes for the same single-writer SQLite bottleneck the whole substrate is otherwise careful about (docs/TIMESERIES_DESIGN_V3.md's own numbers: ~1,000 events/s SQLite ceiling vs 10,000-160,000/s Pebble). Given bal's own benchmark figures (bal-conservation-primitive.md) already cite "~5-6k/s per tenant" as bal's throughput, an unnecessarily-SQL rollup plane consuming writer bandwidth on the same file is a real, not theoretical, throughput cost.
- **Resolved 2026-07-28:** implemented, not just diagnosed. New `pkg/bal/rollup_pebble.go`: a fixed 20-byte big-endian key (`accountKey | level | startUnix`) implementing `chronicle.BucketStore[int64]`'s `Get`/`Put`/`Delete`/`RangeLevel` exactly, following cal's `OpenIndexStore` directory convention precisely (new `storelayout.TenantBalRollupDir`, `<base>/tXXXX/bal_rollup/db`). The old SQL `bucketStore` (`rollup_store.go`) deleted outright, not kept as a fallback.
  - **Checkpoints deliberately NOT moved.** They're a different table from buckets, and stay SQL for a different, still-valid reason: `transferInTx` updates checkpoint balances in the same transaction as the journal write (T-58's eager delta-adjustment) — a write-locality requirement, not guard-locality. Buckets have no equivalent (`EmitDeltas` runs strictly after commit), which is exactly why only they moved.
  - **A real bug found and fixed during implementation, not just theorised:** `Transfer` calls `EmitDeltas` unconditionally as best-effort ("derived-plane failure must never fail an authoritative transfer" — rollup.go's own comment), but a nil rollup handle produced a nil-pointer *panic*, not a returned error — silently defeating that contract. Added a proper nil-check to `engineFor` returning a clean error instead, matching the SQL-era behaviour the migration was supposed to preserve.
  - **A real structural problem found and solved, not glossed over:** `bal.Store` is built fresh per HTTP request, but a `*pebble.DB` handle holds an exclusive on-disk lock and cannot be reopened per request the way `CREATE TABLE IF NOT EXISTS` tolerated. Solved by mirroring `dxp.MemCache`/`SetClaimsCache`'s existing pattern exactly: `bal.OpenRollupPebble` is now a package-level function returning a long-lived, exported `*bal.RollupPebble`; `pkg/server` caches one per tenant (new `Server.balRollup sync.Map`, opened once inside the existing per-tenant `sync.Once` that used to just run DDL) and re-attaches it to every request's freshly-built `Store` via `SetRollupPebble`.
  - `cmd/iolu`'s db-check oracle wiring updated to match: the SQL `bal_buckets` existence check replaced with a filesystem check on the Pebble directory, opening/attaching/closing the handle around each tenant's oracle run (a short-lived CLI process, not a cached-across-requests server).
  - One existing test (`TestRollup_OracleDetectsCascadeDivergence`) directly SQL-corrupted `bal_buckets` to prove oracle divergence detection; rewritten with a `corruptBucket` helper doing the equivalent fault injection natively against Pebble via `RangeLevel`+`Put` — same intent, same coverage, different storage.
  - **Verified exhaustively:** full tree `go build`/`go vet` clean in single whole-tree passes; `pkg/bal`'s full suite green under `-race`; `pkg/server`'s full suite green (not just the bal-filtered subset), including `/bal/asof` specifically exercising the rollup read path end-to-end over real HTTP, confirming the `balRollup` cache actually works in the request path, not just in isolation; `cmd/iolu` and `cmd/xolu` green.
  - **Follow-up 2026-07-28: the open question above was investigated, not left as a hunch.** Full assessment recorded in `docs/KNOWN_ISSUES.md`'s new "`bal` design — recorded decisions" section: checked directly against the vendored Pebble source (`cockroachdb/pebble@v1.1.5/batch.go`) that no conditional-write primitive exists there at all; the guard turns out to span three tables with a correlated anti-backdating subquery, more complex than assumed; the throughput case is weaker than "Pebble is faster" once SQLite's per-tenant-file (not per-server) write ceiling is accounted for. Conclusion: buildable, not pursued, revisit only against a measured throughput problem — not a general instinct.
  - **Regression coverage added the same day:** `TestTransfer_DegradesGracefullyWithoutRollupPlane` (pkg/bal/rollup_test.go) pins the exact contract the nil-pointer panic above violated — a Store with no rollup plane attached must still let Transfer succeed, with the degradation observable via `onRollupError`, not silent. Verified it has real teeth the same way the T-60 regression test was verified: temporarily removed engineFor's nil-check, confirmed the identical panic reproduces (same stack trace), restored the fix, confirmed green again.

Cross-ref: CHANGELOG 0.17.0.

## [0.16.27] T-61 — TenantID type introduced (pkg/tenant.TenantID); entire tree converted from bare uint16 -- old free functions deleted, not deprecated (v0.16.27, 2026-07-28)

Theme: storage-config · closed 0.16.27 · 2026-07-28

- **Trigger:** direct instruction, following T-59's discovery that bal lacked a tenantID field: "ensure that TenantID and its invariants become strictly the only way you can manipulate tenant IDs." T-59 fixed one primitive's gap; this closes the gap everywhere, structurally rather than by convention.
- **The type:** `tenant.TenantID` (`type TenantID uint16`) in pkg/tenant, with the full naming surface as methods on the type itself (TablePrefix, DirName, GraphNodePrefix, StorageDirSegment, ScopeKey, NodeID, CacheKey/CachePattern/CacheTenantPattern/CacheListPattern, every Table/Index name function, GraphEdgesTableName). Every corresponding free function (uint16-parametered) was DELETED, not kept as a deprecated wrapper -- the old signature simply does not compile anymore anywhere in the tree, which is what makes this an invariant rather than a convention. Registry (the actual minting authority for tenant IDs) converted throughout: Persister interface, byName/byID maps, Register/Lookup/GetOrRegister/Name/List.
- **Scope of the tree-wide conversion (all verified individually, then together):** pkg/tenant, pkg/bal (Store gained a first-class tenantID field it never had before T-59's audit -- NewStore, TenantID() accessor, the dxp adapter's cross-check), pkg/storage (SQLiteConfig, StoreConfig, AdaptedTableSpec, TenantIDLister, GraphEdgeScanner, FsmWalker interfaces, and every table/index-naming call site across sqlite.go, adapted*.go, schema_evolution.go, dialect_sqlite.go, graph_oracle.go, edge_fts.go, edge_schema.go, fsm_walk.go, tenant_persist.go), pkg/storelayout (TenantSegment, ParseTenantSegment, TenantRoot, and every Tenant*Dir/Path function), pkg/graph (the Graph interface's UpdateFromEntityForTenant, both NodeID call sites), pkg/cal (Manager and SQLiteBookingSource), pkg/timeseries (the Manager interface and DefaultManager), pkg/oql (Executor's genDispatcher/seqIncrementor fields and every Set*/newSeqSessionState), pkg/sulpher (tenantIDFromPrefix -- the one deliberately-deferred decode-direction function from T-59's audit, reconsidered and converted for full consistency once the mandate became "strictly the only way"), pkg/server (the large one: every xDB(r) (*sql.DB, uint16) helper -- genDB, eventDB, fsmDB, metaDB, seqDB -- and everything downstream: loadEventDef, allocEventIDTx, loadMachineSnapshot, runWalkPrequery, allocFSMID, definitionExists, seqIncrement, and both dispatcher-returning closures), cmd/iolu (listTenantIDs, openTenantStore, storePathFor, registerTenant, provisionTenantDirs, tenantNodeCount, discoverTSTenants -- CLI-flag decimal parsing in tenantFlags.Set left alone, correctly: user-typed --tenant name:5 is decimal input, a different context from the T-60 wire-format bug, not the same mistake), cmd/xolu (graph edge-table hydration, entity-to-graph loading -- one place a zerolog .Uint16() log field needed an explicit uint16(tid) cast back, since the logging library itself wants a primitive, not the domain type).
- **Deliberately NOT converted, and why:** pkg/client's WithTenantID -- confirmed pkg/client imports zero other internal xolu packages; a standalone wire client has no reason to depend on a server-side naming-convention package, and that independence is worth protecting given a real downstream consumer is imminent (see T-60). This is the one intentional boundary in an otherwise unconditional sweep.
- **Bugs found as a byproduct of the sweep, not the goal of it:**
  - cmd/iolu/dbcheck.go: a graph-table lookup used `%04d` (decimal) instead of `%04X` (hex) -- for any tenant ID >= 10 this silently checked the wrong table name, so `iolu db check` would report "no graph table" for tenants that had one. Fixed as part of T-59's own follow-up, carried forward correctly here.
  - T-60 (filed separately, P1, unfixed pending explicit go-ahead): pkg/client.WithTenantID formats hex, pkg/server.resolveNumericTenant parses decimal -- silent wrong-tenant resolution for some ID ranges. Found while assessing this exact migration's impact on pkg/client ahead of a real downstream consumer's integration.
- **Verified exhaustively, not just per-package:** `go build ./...` and `go vet ./...` clean across the entire tree in single passes (not just the packages touched). `go test ./... -count=1` green across the entire tree in ONE single pass -- every package, ~150s of real test execution, zero failures, including pkg/client (unaffected, confirming its independence held) and every package this session touched or depended on transitively.
- **T-60 cross-reference update:** the "unfixed" note above was accurate when T-61 was filed; T-60 closed in this same release (0.16.27) -- see its own resolution record.

Cross-ref: CHANGELOG 0.16.27.

## [0.16.27] T-60 — pkg/client.WithTenantID (hex) and pkg/server.resolveNumericTenant (decimal) disagree on wire format -- silent wrong-tenant resolution for some ID ranges (v0.16.27, 2026-07-28)

Theme: storage-config · closed 0.16.27 · 2026-07-28

- **Trigger:** assessing pkg/client's TenantID impact ahead of a real downstream consumer project consuming it. Checked whether the numeric-tenant-ID wire path actually works against the current server, rather than assuming the client's own doc comment was accurate.
- **The bug:** pkg/client.WithTenantID(id uint16) formats as 4-digit UPPERCASE HEX (fmt.Sprintf("%04X", id)) -- consistent with every other tenant-ID string convention in xolu (pkg/tenant.IDString and everything built on it this session). pkg/server's resolveNumericTenant (server.go) parses the same field as BASE-10 DECIMAL (strconv.ParseUint(raw, 10, 16)). The two sides disagree.
- **Severity, precisely:** not a uniform failure -- a silent, ID-range-dependent one.
  - Tenant IDs 0-9: hex and decimal digit strings coincide ("0005" reads as 5 either way) -- accidentally correct.
  - Tenant IDs 10-15 (0xA-0xF): the hex string ("000B") is not valid decimal at all -- resolveNumericTenant fails to parse, falls through as "unknown tenant" (or, if TenantAutoRegister is enabled, auto-registers a NEW tenant literally named "000B" instead of resolving to the intended one -- a wrong-tenant, not just a failed request).
  - Tenant ID 16 (0x10) and similar: the hex string ("0010") happens to ALSO be a valid decimal number (10) -- resolveNumericTenant succeeds and silently resolves to the WRONG tenant (whichever tenant actually has ID 10, if one exists), with no error at all. This is the worst case: wrong-tenant data access with no signal anything went wrong.
- **Why this matters now:** a real downstream consumer is about to start using pkg/client. Any deployment with more than 9 tenants that uses WithTenantID (rather than WithTenant by name) hits this the moment tenant IDs reach double digits -- not a hypothetical scale concern, a near-term one.
- **Where the fix belongs:** almost certainly resolveNumericTenant, not the client -- hex is the convention everywhere else in xolu (this session's whole pkg/tenant hardening), and the client's own doc comment already asserts hex is what "xolu requires". Change ParseUint's base from 10 to 16. Two call sites in server.go (strict and non-strict tenantMiddleware branches) both go through this one function, so the fix is one line plus a regression test across the ID-10-15 and ID-16-coincidence cases specifically (the second is the dangerous one -- a naive test only covering the "fails to parse" case would miss the silent-wrong-tenant case entirely).
- **Not fixed here:** this is a wire-protocol bug in pkg/server, not a TenantID-invariant tidying task -- filed for explicit go-ahead rather than folded into the current pkg/tenant migration work.
- **Resolved 2026-07-28:** fixed at the root, exactly where predicted -- resolveNumericTenant's ParseUint base changed from 10 to 16. Doc comment rewritten to state the hex format explicitly and cross-reference the regression test rather than leave the wrong claim ("decimal uint16") in place for the next reader. New test file (pkg/server/tenant_numeric_resolve_test.go) covers both failure modes named above, not just the loud one: TestResolveNumericTenant_HexNotDecimal exercises single-digit (must keep working), double-digit hard-fail (0x0B), and the dangerous silent-collision case (0x10 = hex 16, which a base-10 reader would misresolve to tenant 10 -- a tenant deliberately also registered in the test fixture to prove the collision doesn't happen anymore, not merely that parsing succeeds). TestResolveNumericTenant_MatchesClientWireFormat round-trips tenant.TenantID.String() (pkg/client.WithTenantID's exact construction) for every ID in the fixture. Verified the regression tests actually have teeth before trusting them: reverted the fix, confirmed the predicted subset failed (including the silent-collision case, not just the hard-fail ones), restored the fix, confirmed green. Full pkg/server suite and full tree build+vet+test all green with the fix in place.

Cross-ref: CHANGELOG 0.16.27.

## [0.16.27] T-59 — Tenant ID invariant: bal lacked tenantID as a first-class field, unlike every other primitive; fixed at the root (pkg/tenant.IDString) (v0.16.27, 2026-07-28)

Theme: storage-config · closed 0.16.27 · 2026-07-28

- **Trigger:** discovered while asking how much of a /dxp transaction is testable today. A multi-participant integration test (pkg/dxp/integration/) needed ONE tenant identifier comparable across bal, fsm, and entity, and found they had none in common: bal's dxp tenant key was its own table-prefix string ("t0000_"); fsm/entity's was a hex encoding of tenantID ("0000"). Checked further on request and found this was NOT a dxp-specific quirk: cal (SQLiteBookingSource), pkg/storage (fsm/entity), and timeseries (Manager) all carry tenantID uint16 as a first-class field; bal.Store was the sole outlier, retaining only the derived prefix string with no tenantID field at all -- a pre-existing architectural inconsistency the dxp work happened to be the first thing needing a cross-primitive-comparable key.
- **The invariant, named:** every primitive that produces a tenant-scoped string for anything other primitives might need to compare against must derive it from ONE canonical function, never reinvent the encoding locally. Added as pkg/tenant.IDString(tenantID uint16) string -- the bare 4-digit-hex form underneath TablePrefix/GraphNodePrefix's own decorated forms.
- **Fixed at the root, not papered over:** bal.Store gained tenantID uint16 as a first-class field; NewStore(db, tenantID) now derives prefix internally via tenant.TablePrefix(tenantID) rather than accepting a bare prefix string with nothing behind it -- eliminates the drift possibility by construction, not just convention. pkg/storage's own dxpTenantKey now delegates to tenant.IDString instead of keeping an independent copy. bal.Adapter.Reserve gained the same tenant-key cross-check fsm/entity's adapters already had (refuses a caller-supplied tenant string that doesn't match the canonical derivation).
- **A second bug the fix itself surfaced:** Store.Transfer's OWN internal claims lookup (the ordinary, non-dxp write path) was still keyed on s.prefix, not the corrected canonical key -- caught immediately by TestOrdinaryTransfer_RespectsLiveDxpHold failing after the adapter side was fixed but this call site wasn't. Two different, both-"correct"-looking cache shards for the same tenant. Fixed; full bal suite green under -race afterward.
- **Blast radius, checked by full build not just grep:** 3 production/test call sites needed the NewStore signature change (pkg/server/v2_bal_handlers.go, cmd/iolu/dbcheck.go -- the second one missed by an initial pkg/-scoped grep and only found by running go build ./... across the whole tree, bal's own two test files) plus the dxp adapter test file and the integration test's two-constant workaround, which collapsed to one shared value once the fix landed -- proof by a passing test, not just a claim in a comment.
- **Verified:** full tree build + vet clean; pkg/bal, pkg/dxp, pkg/dxp/integration, pkg/storage, pkg/tenant, cmd/iolu, and pkg/server's bal handlers all green (bal/dxp/integration under -race). No remaining bal.NewStore call anywhere passes a bare string (swept).
- **Follow-up audit (same session, prompted by "are we ONLY using tenant invariants now?"):** checked comprehensively rather than assumed. Found the T-54/bal fix addressed only the dxp-cache-key slice; five more independent reimplementations of the same tenant-hex encoding existed elsewhere, none related to dxp:
  - pkg/storage/adapted_crud.go (x2), schema_evolution.go (x2), dialect_sqlite.go (x1): all constructed "t%04X_n_sch" inline via fmt.Sprintf despite tenant.NodeSchemaTableName already existing and doing exactly this -- the canonical function was there and simply wasn't being called. Fixed: all five now call it.
  - pkg/storelayout/storelayout.go: TenantSegment reimplemented the bare "t%04X" encoding independently -- its OWN doc comment already said it was modeled "in the same spirit as" pkg/tenant's invariants, without actually sharing the code. Added tenant.TenantDirName (a genuine gap: neither StorageDirSegment, which special-cases tenant 0 to "", nor the suffixed Table*Name family fit) and delegated to it.
  - cmd/iolu/dbcheck.go: two bal_journal/bal_buckets table names reimplemented via fmt.Sprintf("t%04X_...", tid) instead of composing tenant.TablePrefix(tid) with bal's own suffix. Fixed.
- **A real, previously-unknown functional bug found as a side effect, not just style:** the SAME file had `fmt.Sprintf("t%04d_graph", tid)` for the graph-table existence check -- decimal %04d, not hex %04X. For any tenant ID >= 10 this looks up the wrong table name entirely (e.g. "t0010_graph" instead of the real "t000A_graph"), so `iolu db check` would silently SKIP the graph oracle for every tenant ID 10 and above, reporting "no graph table" when one exists. Invisible in any testing done with small tenant IDs (decimal and hex coincide for 0-9). Fixed via tenant.GraphTableName; a matching display-only %04d two lines below fixed alongside it via tenant.TenantDirName.
- **Confirmed NOT a violation, left alone:** pkg/client/client.go's WithTenantID reimplements the same hex encoding, but pkg/client imports zero other internal xolu packages (checked) -- a standalone client library talking HTTP has no reason to depend on a server-side naming-convention package. Deliberate, correct independence, not drift.
- **Noted, not fixed:** pkg/sulpher/sqlgen.go's tenantIDFromPrefix decodes a hex string back to uint16 -- the inverse operation, no existing canonical counterpart in pkg/tenant to delegate to, and a parse error surfaces immediately at the call site rather than silently diverging the way two independent ENCODERS can. Lower priority; flagged rather than actioned this session.
- **Verified:** full tree build + vet clean; pkg/tenant, pkg/storelayout, pkg/storage, pkg/bal, pkg/dxp/integration, cmd/iolu, and pkg/server (bal/dbcheck/graph-touching tests) all green.

Cross-ref: CHANGELOG 0.16.27.

## [0.16.24] T-51 — Stale checkpoint after a backdated transfer (wrong as-of; oracle blind) (v0.16.24, 2026-07-23)

Theme: bal · closed 0.16.24 · 2026-07-23


- **Reproduced 2026-07-21** (probe, not committed): two transfers, a `Checkpoint` written after both, then a transfer dated *before* the checkpoint. `BalanceAsOf` returned **150**; `BalanceAsOfExact` (journal) returned **157**. The checkpoint was computed before the backdated entry existed and is now a frozen wrong number; as-of reads `checkpoint + intervening buckets`, so it inherits the error.
- **Second defect, worse:** `RollupOracle().Check` reported **equal = true**. The oracle compares bucket sums against the journal sum, and both include the backdated entry, so they agree. **Checkpoints are never verified by any oracle.** This would have shipped silently.
- **Why this is not solved by sealing.** Sealing (@B07, XOLU-BAL003) refuses entries dated inside a closed period, which is correct for a financial ledger. It is *inapplicable* to domains where backdating is normal — a museum inventory accessioning an artefact dated 1897 in 2026 is legitimate and must not be refused. A stale checkpoint returning a wrong balance is a bug in **both** domains; sealing merely prevents one route to it.
- **Fix, both parts required regardless of any sealing policy:**
  1. A transfer dated at or before an existing checkpoint must invalidate or recompute that checkpoint and every later one. `chronicle.Engine` already provides `Invalidate` and `Recompute` for exactly this shape; bal currently uses neither.
  2. The oracle must verify `checkpoint.balance == SUM(journal WHERE at <= checkpoint.at)` for every checkpoint. This is the check that should have caught it.
- **Cost question to settle during the fix:** naive recompute is O(checkpoints at or after the entry). A museum backdating to 1897 against 130 years of monthly checkpoints rewrites ~1,560 rows per accession. Decide whether checkpoints are invalidated lazily (mark stale, recompute on read) or eagerly, and whether checkpoints should be optional entirely for backdating-heavy workloads — their purpose is making as-of independent of journal length, which is a poor trade if they churn constantly.

Cross-ref: CHANGELOG 0.16.24.

## [0.16.16] T-48 — G-12 RI restrict strategy: resolved as a subsystem-config defect (v0.16.16, 2026-07-21)

Theme: ri · closed 0.16.16 · 2026-07-21

The G-12 RI restrict race, falsified repeatedly on multi-core CI, was traced NOT to a concurrency defect but to a test-harness misconfiguration: the map-based `storage.NewStore` silently defaulted the graph subsystem OFF, so `syncGraphEdges` short-circuited and in-transaction RI enforcement never ran. The three switchable strategies (serialize, intx-only, serialize-intx) introduced to "arbitrate on multi-core" were all falsified (1/8–4/80) because none can close a race whose enforcement code is skipped. Production was never affected (it uses `NewStoreFromConfig`, which propagates `GraphEnabled`).

Resolution:
- Map builder now honours `graph_enabled` and defaults it on; graph + timeseries default on generally.
- Test harness store enables graph, matching production.
- Parity guard added to `rebuildRIRegistry`: x-ref policies present with graph disabled → loud error, fatal under `XOLU_STRICT_SUBSYSTEMS`.
- With enforcement actually running, the plain in-transaction check closes the race — verified 0/80 under -race on multi-core (macOS).
- All strategy code removed (ri_strategy.go, RILock/ForceLock/NoLock store variants, the RIStrategy config knob + env var, the benchmark). G-14 retired. release.yml and cross-build.yml restored; the ri-strategy-probe job removed from ci.yml.
- Three adversarial/embed tests that had been passing only because enforcement was silently off were corrected to assert enforced behaviour (RI003 on dangling REF at write time; dangling-via-post-hoc-delete for the embed-degradation case).

Cross-ref: CHANGELOG 0.16.16; G-12 in KNOWN_ISSUES (resolved).

is the item's full text as it stood at closure, stamped with closing version
and date, newest first. `CHANGELOG.md` says what shipped; this file records
what was wrong or needed and how it was resolved — the two reference, never
duplicate, each other. Never edit or delete existing entries.

---

## T-45 — Mechanical guard for @C04d (sized-id wire discipline)

**Closed:** 2026-07-21 · **Version:** post-v0.16.3 (ships with the next tagged release)
**Resolution:** `tools/c04dcheck` — a type-aware go/analysis checker (separate
module; main go.mod untouched) enforcing chronicle-substrate §4d with three
checks: (1) narrowing conversions of registered sized-id types
(TimelineID, CalOrdinal; matched by name + uint32 underlying) to
int/int8/int16/int32/uint8/uint16, covering ceiling constants like
int(MaxTimelineID); (2) values parsed via strconv.ParseUint/ParseInt with
a constant bitSize below the id's width and converted to the id type in
the same function; (3) ids constructed FROM lossy-width sources (the
`Timeline uint16` wire-field shape). Verified: catches all five violation
shapes on the analysistest fixture (five `want` annotations), zero
findings on the current tree, and the legal patterns (int64 carry, 32-bit
parse, tenant-id 16-bit parses, untyped constants, the explicit uint32()
assertion idiom for test loop counters) stay silent. Wired into ci.yml as
a failing step (test + build + run over ./...), making @C04d
self-enforcing rather than review-enforced. Six pre-existing test-file
loop-counter constructions were normalised to the uint32() idiom. bal's
internal account key joins the registry when bal lands (@B §9a).

**Original item as at closure:**

### T-45. Mechanical guard for @C04d (sized-id wire discipline)

Theme: tooling · Priority: P3 · Status: ☐
Blocks/after: enforces @C04d (chronicle-substrate §4d). Independent; can land any time. Strengthens every current and future primitive that exposes a numeric id.

- **What:** a mechanical check (go vet-style analyzer, or a CI grep/lint
  pass) that flags the four @C04d violation sites: `int(<sized-id>)` and
  `uintM(<sized-id>)` narrowing conversions, `ParseUint(..., 10, 16)` (or
  any bitsize narrower than the id's width) on an id parse, and
  conversion of a sized-id ceiling constant to `int`. Sized-id types are
  registered by name (TimelineID, CalOrdinal, future bal AccountID).
- **Why P3, why now on the radar:** @C04d was canonised only after the
  /ts 32-bit break (2026-07-20). A law enforced solely by review is a
  law that will be forgotten under deadline — exactly how the /ts
  boundary fields slipped through the wave-1 widening. A mechanical guard
  makes the law self-enforcing and would have caught that defect
  pre-merge. This is the "should have been an early dependency" lesson
  turned into a standing check.
- **Scope note:** the guard complements, does not replace, the per-
  primitive range regression test (@C04d stage-1 obligation). The test
  proves one primitive's boundary is correct; the guard proves no new
  narrowing is introduced anywhere.
- **Reference:** docs/proposals/chronicle-substrate.md §4d;
  pkg/timeseries/timeline_id_width_test.go (the test pattern it backstops).


## T-38 — closed 2026-07-20 (wave 0 item #6)

**Finding upgraded during work:** T-38 was filed as a P3 deployment-quality item
about the *absent* trusted-proxy extraction. In flight, an audit surfaced that
`pkg/middleware/ratelimit.go`'s `getClientIP` was itself *honouring
X-Forwarded-For and X-Real-IP unconditionally* — the same
GHSA-3fxj-6jh8-hvhx-class spoofing hazard chi's middleware.RealIP was
retired for, sitting one directory over from the deliberate comment
that had rejected chi's version. Five existing tests codified this
vulnerable behaviour as expected.

**Fix:** The rate limiter's `getClientIP` is now a method on
`*RateLimiter` (so it can consult its configured trusted-proxy CIDR
set), with the trust model:

- The TCP peer (r.RemoteAddr) is always authoritative.
- X-Forwarded-For and X-Real-IP are consulted only when the peer sits
  in the configured trusted-proxy CIDR list.
- When honoured, XFF is walked right-to-left past any hop that is also
  a trusted proxy, and the first non-trusted address is returned —
  yielding the real client behind proxy chains and resisting
  attacker-prepended forgeries.

**Config surface:**

- `config.Config.TrustedProxies` — comma-separated CIDR ranges (bare
  IPs accepted, treated as /32 or /128).
- `XOLU_TRUSTED_PROXIES` env var. Empty by default: header-based IP
  extraction is refused and the peer is authoritative — the safe default.
- Parsed via `parseTrustedProxies`; malformed entries silently skipped.
- Membership test via `ipInAnyCIDR`.

**Tests (rewrites plus additions):** Nine test cases in
`pkg/middleware/ratelimit_test.go` covering: no trust config → headers
ignored; untrusted peer with forged XFF → peer wins; trusted peer with
XFF → hop honoured; trusted chain → walk past to first non-trusted;
attacker-prepended forgery → correctly ignored; X-Real-IP fallback;
trusted peer with no headers → peer wins; bare-IP trust spec accepted;
XFF-over-X-Real-IP precedence when trusted. **The pre-fix tests
codified the vulnerability; replacing them was the largest single
correction.**

**Verification:** middleware tests green; full suite (31 packages)
green; lint 0 issues; gate PASS. The deliberate comment in server.go
about "coarsen to per-proxy until T-38 ships" is updated to describe
the shipped mechanism.

### T-38. Trusted-proxy-aware client-IP extraction

Theme: server · Priority: P3 · Status: ☐
Blocks/after: Nothing; deployment-quality item. Born from the 0.16.1 re-cut's removal of `chi/middleware.RealIP` (deprecated upstream citing GHSA-3fxj-6jh8-hvhx: it trusts X-Forwarded-For/X-Real-IP unconditionally, letting any client spoof its identity to the rate limiter and logs).

- **Current behaviour (safe default):** client identity is the TCP peer
  (`r.RemoteAddr` untouched). Deployed behind a reverse proxy, per-client
  rate limits coarsen to per-proxy and logs show the proxy address.
- **Work required:** a small middleware taking a configured trusted-proxy
  CIDR list (e.g. `XOLU_TRUSTED_PROXIES`); only when the direct peer is
  in the list, honour the rightmost non-trusted X-Forwarded-For hop.
  Config plumbing through pkg/authconfig is NOT needed — this is
  transport identity, not auth; it belongs beside the rate limiter.
- **Estimate:** half a day including tests for the spoofing cases.

---

## T-44 — closed 2026-07-20 (wave 1 item #9 display; @S §4 R10)

**Fix:** `iolu ts status --base-dir <dir> [--tenant <id>]` reports each
tenant's ts store metadata, notably the immutable sysmask width. `db
status` inspects the SQL storage layer; the width lives in the ts
Pebble store's meta.json (a separate directory tree), so it is surfaced
by this ts-scoped command. The width is read directly from meta.json
(lightweight — no Pebble store open/lock) and rendered via
`SysmaskWidth.String()` for consistency with the type's own display.
Tenant discovery walks tXXXX dirs carrying a ts/ store; `--tenant`
restricts to one. Usage/help text updated.

**Tests:** `cmd/iolu/ts_status_test.go` — meta JSON parse (guards
against field-name/type drift between iolu's `tsStoreMeta` and
`timeseries.storeMeta`, which would silently report width 0);
absent-width-defaults-0; tenant discovery finds only ts-bearing dirs.
Verified end-to-end against width-8 and width-0 stores.

**Verification:** full suite green (31 packages); lint 0; gate PASS.

**Wave 1 complete:** items #8 (per-primitive ID widening) and #9
(sysmask mechanism + enforcement + display) all shipped.

---

## T-43 — closed 2026-07-20 (wave 1 item #9 enforcement; @S §8)

**Fix:** the sysmask partition now enforces rather than merely
describes. `/ts` gains two allocation paths that partition the id space
with no overlap:

- **`DefineTimeline`** (user-facing, HTTP-reachable) refuses any id in
  the system region under the store's sysmask width — `IsSystem(id)` →
  typed error `XOLU-TS027` (`ErrTSSystemScopeID`). With the default
  width 0 the guard is inert (no id is system), so pre-existing
  behaviour is unchanged until an operator opts in.
- **`DefineSystemTimeline`** (system-internal, added to the `Store`
  interface, NOT wired to the tenant HTTP surface) mints system-region
  ids and refuses user-region ids — the symmetric guard.

`/ts` is client-supplied-id (no auto-allocator), so the proposal's
"user-space exhaustion typed error" and "sequential system allocator"
clauses (@S §8) do not apply today; they become relevant only if `/ts`
grows an auto-allocator. The enforceable clause for a client-supplied
API — refuse explicit system ids on the user path — is implemented.

`classifyTSError` maps `XOLU-TS027` to a client error rather than 500.

**Tests:** `pkg/timeseries/sysmask_enforcement_test.go` — two-paths
partition (user refuses system, system refuses user); default-width-0
guard is inert; HTTP-boundary error carries the classifiable code.

**Verification:** full suite green (31 packages); lint 0; gate PASS.

---

## T-41 — closed 2026-07-20 (wave 0 item #5; @R08 stage 1)

**Fix:** `cascadeDelete` in `pkg/server/handlers.go` now performs
inbound-edge discovery via `s.graph.GetIncomingEdges(nodeID)` before
the delete, then parses each referrer nodeID back into `(entity, id)`
and enqueues it for BFS traversal. Cycle detection and the
`MaxCascadeDeletions` budget from the stub are preserved. `parseNodeID`
helper added to split tenant-prefixed node IDs (via
`tenant.NodeIDStripped`) and their `entity:id` payload. When the graph
is disabled, cascade degrades to a plain delete of the target with a
warning logged — no silent bypass.

**Regression test:** `pkg/server/cascade_delete_test.go` creates a
parent + two children referring to it, cascade-deletes the parent,
asserts >=3 cascaded_deletes reported and all three GETs return 404.
The pre-fix stub returned 1 and left both children reachable — the
exact failure signature the test refuses. Test is not race-shaped;
runs by default.

**Scope:** minimum fix per @R08 stage 1. Policies remain binary
(CascadingDelete flag on = cascade all, off = restrict none); the
full per-x-ref policy design (cascade | restrict | nullify) is
wave 2's work per the RI proposal. This closes the "flag misleads
operators" defect without pre-empting the full RI programme.

**Verification:** cascade test green; full suite green (31 packages);
lint 0 issues; gate PASS.

### T-41. `CascadingDelete` is a stub — cascades to nothing

Theme: server · Priority: P2 · Status: ☐
Blocks/after: Nothing hard; stage 1 of docs/proposals/referential-integrity.md, which designs the full per-ref policy system this defect motivated.

- **Finding:** `handlers.go` `cascadeDelete` seeds its work queue with the
  target entity and never appends referents — the discovery step exists
  only as a comment ("would require scanning all entities — simplified
  here"). With `config.CascadingDelete` enabled, behaviour is identical
  to a plain delete, but the response reports `cascaded_deletes` as if a
  cascade ran. A flag that changes the response's claims but not the
  behaviour misleads any operator who enables it.
- **The right engine is already present:** the graph materialises ref
  edges and is consulted (`removeGraph`) in the same code path. Referent
  discovery is an inbound-edge query, O(edges), no entity scans — the
  scan-based sketch in the comment predates or ignores this.
- **Work required (minimum):** implement discovery via inbound graph
  edges within the existing `MaxCascadeDeletions` budget, or remove the
  flag and the response field until real semantics exist. (Maximum,
  separate decision: schema-level per-ref delete policies —
  cascade | restrict | nullify — with restrict as the safest default.)
- **Estimate:** minimum fix ~half a day; the full policy design is its
  own small proposal.

---

## T-35 — closed 2026-07-19 (wave 0 item #4)

**Verification record:** Race harness `TestConcurrentMove_ExactlyOneOccupiesTarget`
added at `pkg/cal/move_race_test.go` — races N distinct bookings into
one free target window, asserts exactly one wins. Pre-fix, `GOMAXPROCS=8
-race -count=10` reproduced the defect: 2 winners into one window,
14 losers, 0 errors. T-35 **confirmed as real**, not merely suspected.

**Fix:** per-calendar Move mutex on `Lifecycle` (`moveLoks map[string]*sync.Mutex`
guarded by `moveLoksMu`). Move now acquires the calendar's mutex before
`destinationConflicts` and holds it through `setSpan`, serialising the
check-then-act sequence within each calendar. Distinct calendars remain
parallel; only concurrent Moves within one calendar are serialised, and
only within Move itself — reads and other lifecycle operations are
unaffected. The alternative fix considered (setSpanFrom CAS on expected
old span) was rejected during implementation: it guards against the
same booking's span changing, but the T-35 race is between *different*
bookings racing into the same window over shared calendar occupancy,
so span-CAS misses the actual shared state.

**Verification status:** the defect reproduces under `-race` on a
single-CPU host — the pre-fix run produced 2 winners into one window,
confirming T-35 as real. The fix is a mutex, whose semantics are
CPU-count-independent. Full cal package `-race` green, full suite
green, lint 0.

**Multi-core verification (real silicon):** 2026-07-20 env:m1,
`GOMAXPROCS=8 go test ./pkg/cal/ -run TestConcurrentMove -count=20
-race` — 10.8 s, green. The per-calendar mutex holds under true
parallelism across 100 races (20 counts × 5 trials internal). T-35
fully verified.

### T-35. Investigate: Move's conflict-check window (suspected T-34-class race)

Theme: cal · Priority: P2 · Status: ☐
Blocks/after: Investigation only; suspected, not proven. Run after T-34's verification, since its outcome shapes the fix pattern if confirmed.

- **Trigger:** T-34's diagnosis. `Move` is structurally the same
  check-then-act: conflict/feasibility check, then `setSpan` — an
  unconditional span overwrite guarded only by existence. Two concurrent
  Moves of different bookings into the same window could both pass the
  check and both land, double-booking.
- **Caveats:** the seal stress and `TestMoveConflictLeavesUntouched`
  pass, but neither races two Moves onto one window; absence of a test
  is not absence of a race. May also be mitigated by serialisation
  further up — that is what the investigation determines.
- **Work required:** write the racing test (T-34's harness pattern,
  two bookings, one free window, N racers each); if it fails, apply the
  CAS pattern to `setSpan` (guard on expected span) or serialise Move
  per-calendar.
- **Estimate:** half a day for the test; fix cost separately scoped.

---

## T-22 — closed 2026-07-19 (wave 0 item #3)

**Verification record:** `scripts/release_gate.py` implemented and
integrated into `release.sh` as step 8b. Consolidates six check
groups: register consistency (A1–A3), header discipline (B1),
changelog hygiene (C1), resolution-record hygiene (D1), toolchain
pin coherence (E1), and dormant-guards discipline (F1). Immediately
caught two real conditions on first run: a lingering `[Unreleased]`
CHANGELOG section (folded into 0.16.2) and the Docker Go-version
drift (already deferred as T-42). Gate now passes with 2
informational warnings pointing at T-42.

### T-22. Release-hygiene test for stale version strings

Theme: tooling · Priority: P2 · Status: ☐
Blocks/after: Gates: the release-hygiene checks in `TRACKING_PRACTICES.md` §6, including register/RESOLVED and theme/field–table consistency.

- **Trigger:** during the v0.14.1 T-01 rename release, `pkg/errors/errors_test.go` was found to assert hardcoded integer offsets (`9` for error-code length, `s[:4]` for prefix, `s[6:]` for numeric portion) that assumed the old `OLU-` prefix length. All three had to be updated by hand after the rename. Same class of hardcoded assumption caused four version-string updates in tsqlparser during the v0.6.1 release.
- **Work required:**
  - Add a `scripts/check_release_hygiene.py` that scans the tree for:
    - Test files carrying a version string that doesn't match `VERSION` or `pkg/version/version.txt`.
    - Integer literals in test files adjacent to `error-code`, `prefix`, `length`, or similar terms that look like structural assumptions.
    - Hardcoded release dates in CHANGELOG entries that predate the file's last-modified time.
  - Wire into `release.sh` as an optional gate (`--strict-hygiene`).
- **Impact:** small utility, high value on rename-class changes. Would have flagged both the errors_test and the version_test issues before the release scripts ran.

---

## T-36 — closed 2026-07-19 (wave 0 item #2)

**Verification record:** dormant-guards table appended to
`docs/KNOWN_ISSUES.md` (Part 3 §8 canonical home), seeded with five
existing guards each carrying gating condition, hardware requirement,
canonical invocation, and last-exercised record. Five future guards
registered as owed against their originating proposals, per the rule
that specification and registration are one act. G-03x (fuzz targets)
flagged as overdue for a session.

### T-36. Create the dormant-guards table

Theme: tooling · Priority: P2 · Status: ☐
Blocks/after: Required by working-agreement Part 3 §8 (2026-07-18); the §6 release gate cannot check guard exercise until the table exists. Pairs naturally with T-22.

- **Work:** enumerate every dormant guard — `stress`-tagged tests (incl.
  `TestConcurrentTerminalTransition_ExactlyOneWins`, `TestSealStressLocal`),
  the `integration`-tagged client suite, fuzz targets from the D-003/4/7/8
  family — into a table in this register or KNOWN_ISSUES: name, gating
  condition, hardware needs, canonical invocation, last-exercised date +
  environment. Seed last-exercised from today's recorded runs (M1, 8 cores,
  -race, 2026-07-18).
- **Estimate:** an hour.

---

## T-37 — closed 2026-07-19 (wave 0 item #1)

**Verification record:** git init run in the checkpoint at
/home/claude/xolu-checkpoint, initial commit `v0.16.2 baseline
(retrofit from checkpoint)` at HEAD, tagged v0.16.2 locally. Session
audit commit follows on top. Sandbox-only artefacts (fuzz corpus,
crashers) recorded in .gitignore per xolu upstream practice.

### T-37. Git history inside the checkpoint

Theme: tooling · Priority: P3 · Status: ☐
Blocks/after: Decided in principle 2026-07-18 (zip-with-.git hybrid; bundle optional for incremental sync). Execute at the start of the next working session while release boundaries remain reconstructible from CHANGELOG.

- **Work:** `git init` in the checkpoint; retrofit commits at the
  v0.15.0-import, v0.15.1, v0.15.2, v0.15.3, v0.16.0, v0.16.1 boundaries
  (content reconstructible from CHANGELOG entries); tag each; adopt
  commit-per-release thereafter; checkpoint zips ship with `.git` included.
- **Not included (separate decision):** GitHub Actions as dormant-guard
  executor — proposed, undecided; would close the loop between T-36's
  table and mechanical execution, but needs the team's call on repo
  visibility and CI wiring.
- **Estimate:** half an hour for the retrofit.

---

## T-40 — closed v0.16.2 (2026-07-19)

**Verification record:** GitHub Actions run on commit 5332542
(ubuntu-latest multi-core runner, 2026-07-19): conclusion success, both
jobs, two runs. The immediately preceding commit (5037943, carrying only
the T-39 fix) failed on this defect's `fatal error: concurrent map
writes` — demonstrating the fix was necessary — and the fixed commit
went green — demonstrating it sufficient. Discovered, located (by the
runtime's own fatal with full stack), fixed, and verified within the CI
runner's first day of service.

### T-40. Data race in pkg/server process-shared state (unlocated)

Theme: server · Priority: P1 · Status: ◐
Blocks/after: Blocks trusting any multi-core -race run of pkg/server; every parallel graph e2e test fails with "race detected during execution of test" on M1 (count=5) while single-core runs are silent. Post-0.9.9 regression window (older versions ran -race clean per the team).

- **Evidence:** detector-confirmed on M1; even near-no-op tests
  (`TestGraphPath_MissingParams`) trip it, and each test boots its own
  server — so the racing state is process-global, not per-server or
  per-test.
- **Suspects eliminated by inspection (2026-07-19):** oql profile
  presets (Calibrate/DefaultProfile return fresh values; ProfileByName
  pointees never mutated); oql Executor.SetProfile (instance-scoped
  planner swap); pkg/tenant registry (instance state under RWMutex);
  zerolog package-global logger (never assigned anywhere; only atomic
  SetGlobalLevel in oql tests). The concurrent-calibration garbage
  thresholds (82–90k ns/row vs normal 3–8k under load) remain a
  separate robustness observation, not the race.
- **Next step (required, cannot be generated in-sandbox):** the race
  detector's WARNING: DATA RACE report — two stack traces naming the
  writing and reading file:line — from a multi-core run of any single
  test in graph_path_e2e_test.go.
- **Estimate:** unknown until located; the fix is usually an hour once
  the stacks land.
- **LOCATED (2026-07-19)** by the runtime's own fatal on the CI runner:
  `fatal error: concurrent map writes` at `RegisterScalarFunc`
  (scalar.go:54) ← `RegisterSeqGenFuncs` ← `NewEngineWithSchemaValidator`
  ← `server.New`. Every engine construction registered @SEQ/@GEN closures
  — **capturing that engine's executor** — into the package-global
  `ScalarFunctions` map. Two defects in one: concurrent map writes when
  servers boot in parallel, and last-writer-wins meaning every engine's
  @SEQ/@GEN dispatched to the most recently built executor (cross-engine
  sequence-session leakage; invisible in single-server production).
- **Fix shipped (v0.16.2):** Executor gains an instance `scalars` overlay
  consulted before the package defaults (`EvalScalarFunctionWith`);
  `RegisterSeqGenFuncs` writes the overlay; the package map carries inert
  @SEQ/@GEN stubs so membership checks stay correct; `RegisterScalarFunc`
  contract hardened to init()-only. The four stateless generator
  registrations in v2_gen_handlers were audited and exonerated (init()-
  time, within contract). **Closure on** a multi-core CI/M1 green run.

---

## T-39 — closed v0.16.2 (2026-07-19)

**Verification record:** same CI run as T-40 (commit 5332542, success ×2,
2026-07-19). Note the intermediate commit 5037943 carried this fix alone
and still failed — on T-40, not on this test — confirming the two
defects were independent.

### T-39. `TestBlobManager_GlobalUsage_MultiTenant` races the sampler's initial sample

Theme: server · Priority: P1 · Status: ◐
Blocks/after: Blocks CI green (the sole plain-run failure on multi-core; passes single-core). Fix shape pending design confirmation from the team.

- **Trigger:** first multi-core executions of the full suite (GitHub runner,
  then reproduced on M1: `TenantCount = 3, want 2`).
- **Mechanism (diagnosed):** `pkg/blob` `UsageSampler.run()` takes an
  immediate `u.sample()` on goroutine start (sampler.go:100). The test
  opens tenant 3 via `StoreFor(3)` and asserts its `SampledAt` is still
  zero — a state the implementation makes deliberately transient. On
  multi-core the sampler's first sample completes before `GlobalUsage()`;
  single-core scheduling always let the assertion win, masking it since
  the test shipped.
- **Reading:** `GlobalUsage`'s skip-unsampled contract exists to cover the
  startup window, implying the immediate initial sample is intended and
  the TEST is the defect (asserting transient state timing-dependently).
- **Fix shape (pending confirmation):** prove the skip-unsampled contract
  deterministically — construct a sampler with zero `SampledAt` directly
  at unit level, or gate tenant 3's sampler start — rather than racing
  `StoreFor`.
- **Estimate:** an hour once the design ruling lands.
- **Fix shipped (2026-07-19):** the test now injects tenant 3 as an open
  store with a constructed-but-never-Started sampler — zero SampledAt by
  construction, no timing anywhere. Passes ×3 in-sandbox; **closure on a
  multi-core run confirming** (single-core cannot arbitrate this class).

---

## T-34 — closed v0.16.1 (2026-07-18)

Fix shipped in v0.16.1 (see that changelog entry); **verified on 8-core
M1 under `-race`: five consecutive runs of
`TestConcurrentTerminalTransition_ExactlyOneWins` pass** (previously
2–4 of 32 racers won in every trial). The defect existed since the cal
lifecycle shipped; the v0.14.11 race guard encoded the invariant
correctly but was never executed until 2026-07-18 — a lesson now
carried by T-22's remit: a shipped guard that never runs guards
nothing. Single-core environments cannot reproduce or verify this
class; the 1-CPU sandbox passed the failing code and the fixed code
identically.

### T-34. Terminal transitions are check-then-act, not atomic — multiple racers win

Theme: cal · Priority: P1 · Status: ◐
Blocks/after: molu Part 2 booking tools (which will drive concurrent confirms); the v0.16.0 stress blessing. Fix verification requires multi-core hardware — the 1-CPU sandbox cannot reproduce.

- **Trigger:** first-ever local run of the stress-tagged
  `TestConcurrentTerminalTransition_ExactlyOneWins` (shipped v0.14.11,
  never executed in any recorded campaign — the T-15 record covers the
  seal harness only). On an 8-core M1: 2–4 of 32 racers succeed in every
  trial across all four transition kinds; `success + illegal = 32`
  always; zero data races.
- **Diagnosis:** `Lifecycle.transition` reads the booking, checks
  `allowedTransition`, then calls `BookingSource.SetState` — an
  unconditional overwrite. All goroutines that read the stale state pass
  the check and return nil. The invariant the test encodes ("state graph
  as natural mutex") was specified but never implemented.
- **Fix shape:** compare-and-swap at the source: `SetState` gains the
  expected from-state (`SetStateFrom(cal, id, from, to)`); SQLite:
  `UPDATE … SET state=? WHERE … AND state=?` with RowsAffected==1, else
  `ErrIllegalTransition`; Mem source: mutex-guarded check+set. Losers
  return before touching the index; the winner does index work once.
- **Consequence unfixed:** N callers each believe they performed the
  terminal transition — compensation logic, notifications, and any
  exactly-once accounting downstream silently multiply.
- **Estimate:** half a day including both sources, call-site sweep, and
  local re-verification on multi-core hardware.
- **Fix shipped (v0.16.1):** `SetState` → `SetStateFrom(cal, id, from, to)`
  across the Store interface and both sources; SQLite guarded UPDATE
  (`AND state=?`) with RowsAffected==1, Mem check-and-set under the
  source lock; losers get `ErrIllegalTransition` before any index work.
  **Closure gated on** the race test passing on multi-core hardware.

---

## T-02 — closed v0.16.0 (2026-07-18)

The client roadmap completed: Stages 0–4 shipped v0.14.2–v0.14.6,
Stage 5 (cal methods) v0.15.3, Stage 6 (coverage audit) v0.16.0 per
`docs/CLIENT_STAGE6_PLAN.md`. The audit's conclusion: declared-scope
coverage was already complete; Stage 6 delivered the scope declaration
itself (package doc naming the stable surface and the deliberate
exclusions), the T-32 wire fix, and the T-26 integration suite. The
client is declared stable and version-tied at v0.16.0.

### T-02. Ship an official Go client as `pkg/client` in the xolu repo — ↻ **partially shipped through v0.14.6 (Stages 0–4 complete)**

Theme: client · Priority: P1 · Status: ◐
Blocks/after: Blocks: molu Part 2 tools; only Stage 6 (coverage audit) remains. Plan: MOLU_READINESS_PLAN.md M4.

Being executed as a staged roadmap. Progress to date:

- **Stage 0 (v0.14.2)** — ✓ Import an early consumer's `internal/olu` client as `pkg/client` scaffolding. Package renamed to `client`; doc comments and identifiers brought into line with the T-01 rename. 638 lines of client + 810 lines of tests, stdlib only.
- **Stage 1 (v0.14.3)** — ✓ `Ready(ctx)` for the molu health probe (`/ready`, distinct from `/health`); three auth modes (`WithAPIKey`, `WithBearerToken`, `WithJWT`) via explicit `AuthMode` field; structured `Error` type carrying `XOLU-*` code, HTTP status, message, and raw detail — parses both xolu's current structured shape and the legacy flat shape. 12 new tests.
- **Stage 2 (v0.14.4)** — ✓ Semantic-map endpoints: `GetEntitySchema`, `ListMachineDefs`, `GetMachineDef`, `ListGenerators` (per kind, plus `AllGeneratorKinds` for iteration), `GetSequence`, `ListEventDefs`, `GetEventDef`, `V2Availability`. Full typed structures in `types_schema.go` mirroring xolu's internal `fsmDefinitionSpec`, `eventDef`, and related shapes byte-for-byte. 18 new tests.
- **Stage 3 (v0.14.5)** — ✓ FSM machine operations: `CreateMachine`, `ListMachines` (with `Definition`/`State`/`Ref` filter), `GetMachine`, `PatchMachine`, `DeleteMachine`, `WalkMachine`, `GetMachineState`, `GetMachineResult`, `GetMachineVars`, `GetMachineTransitions`, `GetMachineHistory`. Walk returns `*WalkResult` on success or `*Error` with `XOLU-FSM003` through `XOLU-FSM008` on rejection. 25 new tests. Client now has 78 tests total.
- **Stage 4 (v0.14.6)** — ✓ Operational hardening: retry policy via `WithRetryPolicy` (HTTP-idempotent methods only per RFC 9110 §9.2.2; POST/PATCH never retry under any configuration), structured `log/slog` telemetry via `WithLogger`, per-call timeouts. All opt-in; default client behaves identically to v0.14.5. All 78 pre-Stage-4 tests pass unchanged.

Remaining stages:

- **Stage 5 (v0.15.3)** — ✓ `cal` HTTP methods: `CalCheck`, `CalOpenings`, `CalPropose`, `CalConfirm`, with typed wire shapes (`types_cal.go`), client-side objective validation, XOLU-CAL error mapping, and 12 tests including the Openings→Check→Propose sequence at the wire level.
- **Stage 6** — Full v1 endpoint coverage audit; type-safe request/response models where the current code uses `map[string]any` and structure exists; complete godoc coverage.

Original acceptance criteria still apply to the final release: unified auth handling (done in Stage 1), sensible timeouts, retry policy for idempotent operations only (done in Stage 4), telemetry hooks via `slog` (done in Stage 4), zero non-stdlib dependencies beyond what xolu itself already pulls (maintained throughout), version-tied to the server.

Of the xolu-side gaps discovered during client work, T-24 and T-25 closed in v0.15.1 (see RESOLVED.md); T-28 (history pagination) remains open below.

---

## T-26 — closed v0.16.0 (2026-07-18)

Shipped in minimal form per the M4a decision (D-iii):
`pkg/client/integration_test.go`, build-tagged `integration`, eight
flows covering every declared-scope method's happy path against an
in-process server over real HTTP, wired into `release.sh
--with-integration`. It earned its keep before it was even finished:
first runs surfaced three wire-shape assumptions in the harness itself
(FSM defs require a terminal state; event defs use flat `action_type`;
FTS is double-gated server+store) and led to filing T-33. Run locally
with `-race` before blessing a release; the sandbox runs it without.

### T-26. In-process xolu integration test suite for `pkg/client`

Theme: client · Priority: P4 · Status: ☐
Blocks/after: Optional standalone, or fold into T-02 Stage 6 — both approaches are honest. Plan: MOLU_READINESS_PLAN.md M4a decides (D-iii).

- **Trigger:** every client test written through Stages 0-3 uses `httptest.NewServer` with hand-constructed responses that match what xolu's handlers actually write. This is a real-enough approximation for unit tests but not an end-to-end verification. Wire-format drift on the server side would not be caught.
- **Impact:** medium. False confidence in client correctness against future server versions; regressions in either direction (server response shape changes, client parsing changes) would go undetected until a real deployment.
- **Work required:**
  - Add `pkg/client/integration_test.go` guarded by a build tag (`//go:build integration`) so it only runs when explicitly requested.
  - The suite boots an in-process xolu server via `server.New(config.Default())` against a memory-backed storage.
  - Exercises every client method against the running server, asserting shape and behaviour, not just HTTP status.
  - Wire into `release.sh` as an optional `--with-integration` flag.
  - Could roll into Stage 6 (full v1 endpoint coverage audit) instead of shipping as a separate task; both approaches are honest.
- **Estimate:** 2-3 days if done as a standalone task; less if bundled with Stage 6.

---

## T-32 — closed v0.16.0 (2026-07-18)

Fixed as specified: `Step` → `IncrementBy` (`increment_by`), `Cycle`
added, `CreatedAt` dropped, plus the optional `MinVal`/`MaxVal` bounds
the original shape never captured. `GetSequence` unit tests migrated to
the true wire; the integration suite asserts `IncrementBy` round-trips
against a real server, which is the regression class this item came
from. Deliberate breaking change recorded in the v0.16.0 changelog.

### T-32. Client `Sequence` type does not match the wire

Theme: client · Priority: P4 · Status: ☐
Blocks/after: Candidate for M4b (Stage 6 type audit); breaking client type change, so land before the v0.16.0 stability declaration.

- **Trigger:** found during M1 (T-25 client work). `pkg/client/types_schema.go`
  declares `Sequence.Step` with tag `json:"step"` and a `CreatedAt` field —
  but `handleSeqGet` sends `increment_by` and no timestamp. `Step` has
  therefore been silently zero since Stage 2, and `Cycle` is not captured
  at all.
- **Work required:** rename `Step` → `IncrementBy` with the correct tag,
  add `Cycle bool`, drop `CreatedAt` (or keep only if the server ever
  grows the column); update `GetSequence` tests, which currently pass
  because they assert the same wrong shape they construct.
- **Impact:** any consumer reading `Sequence.Step` gets zero. No known
  consumers today; molu would have been the first.
- **Estimate:** an hour, plus the deliberate breaking-change note in the
  client changelog entry.

---

## T-19 — closed v0.15.2 (2026-07-18)

Shipped in the M2 stage of `docs/MOLU_READINESS_PLAN.md`; see the 0.15.2
changelog entry. Executed with a recorded deviation from both the item
text below and the frozen plan: the lean type went into a new
`pkg/authconfig` package (per decision D-ii) and the middleware itself
moved to a new `pkg/authmw` package rather than being refactored in
place, because per-package Go dependencies meant `ratelimit.go` kept
dragging `pkg/config` into any importer of `pkg/middleware`. Full
deviation record in `docs/MOLU_READINESS_TRACKING.md`. The field set
extracted is the source-verified read-set (including `InternalToken`,
which the text below omits, and excluding `TenantAuthMode`, which
auth.go never read).

### T-19. Extract auth machinery for reuse by molu hub

Theme: auth · Priority: P2 · Status: ☐
Blocks/after: Blocks: molu hub (else it duplicates auth or drags the full config surface). After: land with or shortly after T-02 completion. Plan: MOLU_READINESS_PLAN.md M2.

- **Trigger:** molu Part 3 §5 specifies that the molu hub reuses xolu's authentication code (bearer, API key, JWT modes) rather than reimplementing it.
- **Current state:** `pkg/middleware/auth.go` is under `pkg/` and therefore importable, but it depends on `pkg/config.Config` — the whole xolu server config surface. Importing it into a separate binary (molu hub) means dragging in every unrelated config field.
- **Work required:**
  - Extract a lean auth-config subset (`AuthType`, `JWT` settings, `APIKeys`, `APIKeyGrants`, `TenantAuthMode`, `AuthExcludePaths`) into its own type — either as a new package `pkg/authconfig` or as a struct embedded in `pkg/config.Config`.
  - Refactor `AuthMiddleware` and its helpers to take the lean type rather than `*config.Config`.
  - Preserve backward compatibility: the xolu server continues to work unchanged by wiring the lean subset from its full config at startup.
  - Publish the auth package as a stable import surface with documented semantics.
- **Estimate:** small — one to two days of refactoring plus tests.
- **Sequencing:** should land before or with T-02 so the hub can consume both packages together at v0.15.0.

---

## T-21 — closed v0.15.2 (2026-07-18)

Shipped in the M2 stage; see the 0.15.2 changelog entry. Decision taken
(D-i): option (a), short area codes `MF` and `MH`, reserved in
`ERROR_CODES.md`'s category table with a satellite-project reservation
note. No code change, as specified.

### T-21. Add `MOLU-FRONT-*` and `MOLU-HUB-*` error-code prefixes to the reserved catalogue

Theme: conventions · Priority: P2 · Status: ☐
Blocks/after: Blocks: molu error-code allocation (collision prevention; trivial size, non-negotiable importance). Plan: MOLU_READINESS_PLAN.md M2.

- **Trigger:** molu Part 2 §8.5 defines a family of error codes (`XOLU-MOLU-FRONT-UNAVAILABLE`, `XOLU-MOLU-FRONT-STARTUP`, `XOLU-MOLU-FRONT-TIMEOUT`, `XOLU-MOLU-FRONT-CONTRACT`, `XOLU-MOLU-FRONT-HUB-UNAVAILABLE`) following the xolu error-code convention `XOLU-<AREA><NUM>`. molu Part 3 §5.2 defines the `MOLU-HUB-NS001` diagnostic in the same family.
- **Current state:** xolu's error catalogue in `pkg/errors/errors.go` uses two-letter to three-letter area codes (`ST`, `GR`, `QL`, `VL`, `TN`, and so on). The `MOLU-FRONT` and `MOLU-HUB` families would break the pattern by being longer than three characters.
- **Work required:**
  - Decide whether to (a) shorten to `MF` and `MH` at three characters, or (b) formalise a longer area code convention for extension products.
  - Reserve the chosen prefixes in xolu's error documentation as belonging to satellite projects, not to xolu itself.
  - No code change in xolu — this is a documentation-and-convention task to prevent future conflict.
- **Impact:** trivial in size, non-negotiable in importance: without this, molu error codes could collide with xolu's own if xolu expands its area-code space.
- **Note:** T-01 (rename) already updates the prefix from `OLU-*` to `XOLU-*`. T-21 is downstream of that rename and can land at the same time.

---

## T-25 — closed v0.15.1 (2026-07-18)

Shipped in the M1 stage of `docs/MOLU_READINESS_PLAN.md`; see the
0.15.1 changelog entry. The `created_at` field suggested below was not
implemented — the `sequences` table has no such column. Found and filed
during this work: T-32 (client `Sequence` wire mismatch).

### T-25. Add `GET /api/v2/gen/seq` list endpoint for named sequences

Theme: api-surface · Priority: P2 · Status: ☐
Blocks/after: Blocks: molu Part 2 §4 SemanticMap (sequence advertisement). Pairs: T-24 (one server-side release). Plan: MOLU_READINESS_PLAN.md M1.

- **Trigger:** discovered during client Stage 2 source reconnaissance. xolu v0.14.5 exposes `POST /api/v2/gen/seq` (define), `GET /api/v2/gen/seq/{name}` (get), and `GET /api/v2/gen/seq/{name}/next` (increment) but has no route to enumerate named sequences.
- **Impact:** consumers can only access sequences whose names they already know. Molu's semantic-map builder cannot advertise available sequences.
- **Work required:**
  - Add `handleSeqList` in `pkg/server/v2_seq_handlers.go`, returning `{"sequences": [{"name": "...", "current": N, "created_at": "..."}]}`.
  - Register `r.Get("/gen/seq", s.handleSeqList)` — will conflict with the existing `POST /gen/seq` route only if chi's routing is method-blind (it isn't; they can coexist).
  - Consider mirroring at `/seq` for the permanent alias.
  - Add a client method `ListSequences(ctx) ([]SequenceSummary, error)` once the endpoint ships.
- **Estimate:** half a day.

---

## T-24 — closed v0.15.1 (2026-07-18)

Shipped in the M1 stage of `docs/MOLU_READINESS_PLAN.md`; see the
0.15.1 changelog entry. The `created_at` field suggested below was not
implemented — the validator tracks no registration timestamps.

### T-24. Add `GET /api/v1/schemas` list endpoint for entity types

Theme: api-surface · Priority: P2 · Status: ☐
Blocks/after: Blocks: molu Part 2 §4 SemanticMap (entity-type discovery). Pairs: T-25 (one server-side release). Plan: MOLU_READINESS_PLAN.md M1.

- **Trigger:** discovered during client Stage 2 source reconnaissance. xolu v0.14.5 exposes `GET /api/v1/schema/{entity}` for a single type but has no route to enumerate registered entity types.
- **Impact:** blocks the molu Part 2 §4 SemanticMap builder from discovering entity types at runtime. Consumers must currently supply entity-type names as configuration.
- **Work required:**
  - Add `handleListSchemas` in `pkg/server/handlers.go`, returning `{"schemas": [{"name": "...", "created_at": "..."}]}` or similar. Envelope key choice should be consistent with existing v1 list endpoints.
  - Route registration under `r.Get("/schemas", s.handleListSchemas)` next to the existing `/schema/{entity}` routes.
  - Add a client method `ListEntityTypes(ctx) ([]EntityTypeSummary, error)` in `pkg/client/schema.go` once the endpoint ships.
- **Estimate:** half a day.

---

## T-15 — closed v0.15.0 (2026-07-18)

### T-15. `cal` seal concurrency stress at production scale — CLOSED (v0.15.0)

- **Status:** closed 2026-07-18.
- **Resolution:** stress harness (`pkg/cal/seal_stress_local_test.go`,
  build-tagged `stress`, shipped in v0.14.14) run on local hardware
  (M1 Mac, 8 GOMAXPROCS) under `-race`.
- **Default scale run:** 5 trials × 16 workers × 5000 bookings ×
  2000 ops/worker × 10 calendars × 90 days = 160,000 mutation
  attempts across all trials in 118.85s. All trials passed. Zero
  data races, zero invariant failures, zero seal frontier
  regressions.
- **Extended scale run:** 5 trials × 32 workers × 5000 ops/worker
  = 800,000 mutation attempts in 129.59s at 6,048–6,295 ops/s
  (under `-race`; ~5-10x higher without race detection). All
  trials passed with the same clean signals.
- **Observed success ratios:** Confirm ~0.24%, Cancel ~0.68%,
  Move ~0.27% — the natural rates for random-selection stress
  against a mostly-terminal-state population, and stable across
  trials. Consistency itself is a healthy sign; a race under
  contention would have produced variable ratios.

---

## T-31 — closed v0.14.14 (2026-07-18)

### T-31. `cal` fault injection at the SQL boundary — CLOSED (v0.14.14)

- **Status:** closed 2026-07-18. `AddToPlaneFaultHook` and `RemoveFromPlaneFaultHook` hooks on `IndexStore` shipped in v0.14.14, plus four tests in `pkg/cal/fault_injection_test.go` exercising SetState-succeeds-index-fails scenarios and verifying `RebuildFrom` reconciles.
- **Trigger:** `Lifecycle.transition` applies the SQL state change (`SetState`) first, then updates the in-memory index. If the SQL succeeds but the subsequent `removeFromPlane` or `addToPlane` fails (Pebble I/O error, disk full, corruption), the SQL source of truth reflects the new state but the index does not. The scoped-recompute-from-source pattern is designed to make this recoverable via the next `RebuildFrom`, but the recovery path has never been exercised under injected failure — only under natural operation where it never fires.
- **Impact:** low frequency, unknown blast radius. Real production databases do hiccup. Without evidence for the recovery behaviour, the first time a production tenant sees an index/source disagreement, whoever's debugging it will have to reason from first principles rather than pointing at a passing test.
- **Work required:**
  - Introduce a fault-injection hook in `IndexStore.addToPlane` and `removeFromPlane` accepting an optional `errAfter` counter for tests.
  - Test scenarios: `SetState` succeeds then `addToPlane` fails; `SetState` succeeds then `removeFromPlane` fails; both operations succeed but a subsequent operation on the same booking sees a partially-updated state.
  - After each injected failure, verify `assertIndexMatchesRebuild` holds after a rebuild (recovery works) even though it did not hold immediately (mid-failure state is genuinely inconsistent).
  - Document the observed behaviour in `pkg/cal` package godoc: "under SQL/index disagreement, the next rebuild reconciles."
- **Estimate:** 1-2 days. The fault-injection hook is the most invasive piece; the test scenarios are straightforward once it exists.

---

## T-17 — closed v0.14.14 (2026-07-18)

### T-17. Reconcile `cal` proposal docs with the v0.14 implementation — CLOSED (v0.14.14)

- **Status:** closed 2026-07-18. Each doc now carries a dated "Reconciliation status" banner naming what actually shipped vs. what was proposed, without rewriting the historical content. Full rewrites deferred as unwarranted scope given the code and CHANGELOG are the source of truth.
- `docs/proposals/cal-rest-api.md`, `docs/proposals/cal-pebble-codec.md`, `docs/proposals/SESSION-2026-06-22-NOTES.md` predate the implementation.
- Describe `cal` as design-only with open questions; several were resolved in code during v0.14.0.
- Called out in the v0.14 changelog as "a separate, deliberate task".

---

## T-29 — closed v0.14.13 (2026-07-18)

### T-29. `cal` Openings ↔ Check agreement property test — CLOSED (v0.14.13)

- **Status:** closed 2026-07-18. Property test shipped in v0.14.13 as `TestOpeningsCheckAgreement_ForwardProperty` and `TestOpeningsCheckAgreement_ReverseProperty` in `pkg/cal/openings_check_property_test.go`. 50 trials × 20 queries per state per direction. Mutation-verified: forcing `freeRuns` to report every quantum as free triggers the forward test; forcing `Check` to always feasible triggers the reverse test.
- **Trigger:** during T-18 wire-up work (v0.14.7 through v0.14.11), the HTTP-level tests confirmed that `Openings` results do not overlap existing bookings, but did NOT confirm the stronger property: every span returned by `Openings(from, to, dur, obj)` passes `Check(span, mode)` with `feasible=true`. The two functions share the underlying `quantaInPeriod` and `dayOn` primitives, so the drift surface is narrow, but "narrow" is not "proven." A downstream caller (client Stage 5, molu Part 2 tools) will exercise `Openings → Check → Create` sequences immediately; if there is a boundary-condition drift between the two functions, that caller will hit it first.
- **Impact:** low likelihood, medium blast radius. A drift means callers are told a window is free and then have `Create` refuse a booking there — a confusing failure mode that surfaces only under specific quantum-boundary alignments.
- **Work required:**
  - Property test in `pkg/cal/availability_test.go` (or new `openings_check_property_test.go`) using the existing property-test harness pattern from `codec_property_test.go`.
  - Generate random calendar states via sequences of Create/Confirm/Cancel/Move.
  - For each state, call `Openings` with a range of `(from, to, duration, objective)` inputs and assert every returned span passes `Check` with `feasible=true`.
  - Reverse direction as a bonus: sample random spans in the query window; if `Check` says `feasible=true`, assert `Openings` (with objective `earliest`) returned an opening containing or preceding the sampled span.
- **Estimate:** half a day. If the test finds a bug, fix cost is separately scoped.

---

## T-30 — closed v0.14.12/v0.14.13 (2026-07-18)

### T-30. Remove `ModeShared` and `ModeSubPrefix` from the `cal` type surface — CLOSED (v0.14.12/v0.14.13)

- **Status:** closed 2026-07-18. Mode reduction shipped in v0.14.12 (`ModeShared` and `ModeSubPrefix` removed from types, source layers reject non-`ModeExclusive` with `ErrModeNotSupported` / XOLU-CAL007). `Calendar.Capacity` removed entirely in v0.14.13.
- **Trigger:** the `Mode` constants declare vocabulary the occupancy engine does not honour. `ModeShared` and `ModeSubPrefix` are stored on Booking records but the engine treats every booking as exclusive regardless. The v0.14.10 review with Google Calendar comparison confirmed cal's target model is "exclusive-only, like Google Calendar" (Option A); pooled resources (Option B) were explicitly rejected as 8x storage and disproportionate implementation cost. The vocabulary items existing but doing nothing is a footgun for anyone reading the code who assumes they work.
- **Impact:** low. Nothing in xolu or any downstream consumer references `ModeShared` or `ModeSubPrefix`. Removal is a compile-time signal of the truthful state.
- **Work required:**
  - Remove `ModeShared` and `ModeSubPrefix` constants from `pkg/cal/booking.go`.
  - Update `Booking.Mode` godoc to name `ModeExclusive` as the only valid value.
  - Reject any non-`ModeExclusive` value at `SQLiteBookingSource.PutBooking` and `MemBookingSource.PutBooking` with a new `ErrModeNotSupported` sentinel wrapped via `%w` per the taxonomy from v0.14.8.
  - Reject at the HTTP handler layer via the existing `classifyCalError` helper (needs a new mapping entry).
  - Decide separately (still pending) on `Calendar.Capacity`: keep-and-redocument as descriptive metadata (Google's model) or remove entirely. This decision is a prerequisite for closing this item.
  - Update the stale comment in `pkg/cal/availability.go:339` naming "Stage 2 treats the calendar as a single exclusive resource" — the comment is truthful today but references a stage boundary that no longer applies.
  - Add tests for the new rejections.
- **Estimate:** half a day once the Capacity decision is in.

---

## T-18 — shipped v0.14.7, hardened through v0.14.11 (2026-07-18)

### T-18. Expose `cal` via HTTP endpoints — ✓ **SHIPPED in v0.14.7 (hardened through v0.14.11)**

- **Status:** shipped. Four endpoints under `/api/v2/cal/*` in `pkg/server/v2_cal_handlers.go`: `check`, `openings`, `propose`, `confirm`. Opt-in via `XOLU_CAL_ENABLED`.
- The minimum surface required by molu (Part 2 §5.1.10–§5.1.13) is covered: `openings` accepts an `objective` parameter with the four implemented values `earliest`, `first-fit`, `emptiest`, `longest-clear-margin`, validated at the handler.
- Follow-up hardening shipped in v0.14.8 through v0.14.11: typed error taxonomy (XOLU-CAL001–007 with `errors.Is` status dispatch), 22 HTTP-level tests, `Manager.CreateCalendar` facade, concurrent-transition and rebuild regression guards.
- Still future, once agentic usage patterns are observed: `move` (atomic reschedule), `complete` / `cancel` (terminal lifecycle transitions).

---

## T-01 — shipped v0.14.1 (2026-07-18)

### T-01. Rename `olu` to `xolu` project-wide — ✓ **SHIPPED in v0.14.1**

- Go module path: `github.com/ha1tch/olu` → `github.com/ha1tch/xolu`.
- All `OLU_*` environment variables → `XOLU_*` (127 variables).
- Binary names: `cmd/olu` → `cmd/xolu`; `cmd/iolu` retained deliberately (interactive olu → interactive xolu-admin was rejected in favour of preserving the well-known name).
- Error code prefix `OLU-*` → `XOLU-*` (132 codes and 8 family prefixes).
- Internal package renames: `pkg/olutime` → `pkg/xolutime`; import aliases `oluerr`/`oluMiddleware` → `xoluerr`/`xoluMiddleware`.
- Prometheus metric names emitted at `/metrics`: `olu_*` → `xolu_*`.
- Secret-name arguments to `readSecret`: `olu_jwt_secret` / `olu_internal_token` → `xolu_jwt_secret` / `xolu_internal_token`.
- API paths `/api/v1/…` and `/api/v2/…` do **not** carry the product name — wire protocol untouched, as originally planned.
- CHANGELOG.md deliberately preserved as historical record; entries prior to v0.14.1 continue to reference the software as it was called at the time.
- Actual scope: 3120 hits across 205 files, 4 path renames, executed via a resumable classifier/applier pipeline with role-based classification (ENV, ERRCODE, IDENT_LC, IDENT_UC, IDENT_CMP, STRING_LC, ENV_GLOB) so false positives (`column`, `resolution`, `volume`, `solution`, and so on) were provably untouched.

---

## Namespace retirement: TD-nnn — 2026-07-18 (v0.15.0)

The TD-nnn namespace from `docs/KNOWN_ISSUES.md` is retired. Its two items
were already mirrored in the register and their full detail now lives there:

- TD-001 (unified function registry) → **T-03** in `docs/TRACKING.md`.
- TD-002 (OQL set operations) → **T-04** in `docs/TRACKING.md`.

No content was lost; the KNOWN_ISSUES detail text was merged verbatim into
the register entries as "Extended detail (from retired TD-00n)".

---

## `cal` SQLite secondary indices — resolved in the S11 cal migration (v0.14.0)

From the former KNOWN_ISSUES `cal` schema-gaps section:

- **The `cal` SQLite booking/calendar schema specifies no secondary indices
  (RESOLVED in the S11 migration; recorded here for provenance).** The
  booking-record design (`docs/proposals/cal-gate3-booking-record.md`) lists the
  `cal_calendars` / `cal_bookings` / `cal_participants` field sets and names their
  primary keys in prose, but specified **no `CREATE INDEX`** for the non-PK query
  patterns the tables face. Every use of "index" across the three `cal` proposal
  docs refers to the *Pebble occupancy bitmap* (the derived index), never to a
  SQLite secondary index. This was an omission in the spec.

  Resolved when the schema was implemented: the S11 cal stage in
  `pkg/storage/sqlite.go` (`initV2Schema`) creates the index set the
  `pkg/cal` query patterns require:
  - `idx_cal_bookings_cal_state` on `(tenant_id, calendar_id, state)` — the hot
    path for `LiveBookingsOn(calendarID, plane)`, used by every lifecycle
    mutation, `Move` feasibility check, and `MatchCommit` pre-check; also covers
    `RebuildFrom` / `LiveBookings` per-calendar scans via the leading columns.
  - `idx_cal_bookings_state` on `(tenant_id, state)` — the cross-calendar
    `bookings/list?state=missed` (§7 non-occurrence) scan.
  - `idx_cal_calendars_ordinal` (unique) on `(tenant_id, ordinal)` — enforces the
    dense ordinal's uniqueness within a tenant.
  - `booking(calendarID, bookingID)` point lookups and the `cal_participants`
    join are covered by the respective composite primary keys.

  The absence of these indices never affected correctness — the derived Pebble
  index and the `index == rebuild` invariant are independent of SQLite indices;
  the gap was purely query-performance (table scans where lookups belong), now
  closed.

---

## `cmd/iolu` normalized layout — resolved v0.13.1

From the former KNOWN_ISSUES storage-layout deferred-items section:

- **`cmd/iolu` does not use the normalized layout. — RESOLVED in 0.13.1.**
  The standalone admin CLI previously used its own `--db`/`--ts-dir` flag model
  and composed the old backend-first `ts/tXXXX/` paths via
  `tenant.StorageDirSegment`, so `iolu` and `xolu` disagreed about on-disk paths.
  All eight subcommands now operate on a `--base-dir` data root and derive every
  path through `pkg/storelayout`, matching exactly what the server writes:
  per-tenant `tXXXX/{store,ts,blobs}` (or `shared/store` for the SQLite primary
  in shared mode; ts and blobs are always per-tenant). The store organisation is
  auto-detected from disk with a `--mode per-file|shared` override. `tenant
  delete` is mode-aware (removes the whole `tXXXX/` directory in per-file mode;
  drops the tenant table family and removes ts/blobs dirs in shared mode), and
  inspection commands report per-tenant blob footprint alongside ts. A new
  `cmd/iolu/main_test.go` covers both modes, the layout assertions, the two
  per-file read regressions found during the rework (a connection-blocking hang
  in `tenant list`, and reads issued against tenant 0's store instead of the
  tenant's own store), the re-init refusal, and mode-aware delete. The `--db`
  flag is gone with no compatibility shim; this is an intentional CLI break, as
  the previous interface was already documented as unsafe against an
  `xolu`-managed root.

---

## Blob plane tenant-awareness — resolved v0.13.0

From the former KNOWN_ISSUES storage-layout deferred-items section:

- **Blob plane is not tenant-aware (security-relevant). — RESOLVED in 0.13.0.**
  Previously blobs were a single server-level store at `<BaseDir>/blobs/` that
  partitioned tenants internally by name (isolation-by-convention, not a security
  boundary). Both halves of the debt are now closed:
  - *Layout:* blobs are a first-class per-tenant role at `<BaseDir>/tXXXX/blobs/`,
    keyed by tenant ID and uniform with the timeseries plane (tenant 0 included).
    A per-tenant blob manager (mirroring `timeseries.DefaultManager`) hands out
    one single-tenant `blob.Store` per tenant; `sanitiseTenant` and the
    server-level `<BaseDir>/blobs/` path are removed, and `storelayout.Check`
    now treats a server-level `blobs/` directory as a violation. The
    tenant-name-vs-ID addressing is resolved at the handler seam (route tenant
    string → ID via the registry; tenant 0 for the unscoped route).
  - *Enforcement:* both the native JSON plane and the S3 plane enforce the
    per-identity tenant grant under `TenantEnforceGrant`, fail-closed, so a
    credential scoped to one tenant is rejected (403) on another tenant's blobs.
  There were no known users of the blob API, so no migration path was required.
  See D-004 below (SHA-validation guard), which was folded into this rework.

---

## Security defects D-001 – D-009 — fixed 2026-06-20 (v0.10.2 – v0.10.5 remediation series)

Full text of the adversarial-audit defect entries and their cross-cutting
notes, moved verbatim from `docs/KNOWN_ISSUES.md`. All nine are FIXED with
committed regression tests (named per entry below). Note: subsequent
hardening passes found further defects **D-010 through D-017**, fixed in
v0.10.4 and v0.10.5; those never had KNOWN_ISSUES entries and are recorded
in `CHANGELOG.md` only.

## Defects

### Summary

| ID | Defect | Severity | Reachable | Committed test |
|------|--------|----------|-----------|----------------|
| D-001 | `NEWID()` malformed UUID (undefined 64-bit shift) — **FIXED** | Low | Latent (no FSM calls `NEWID()`) | ✓ |
| D-002 | JWT `exp`/`nbf` expiry skipped for non-numeric claim type — **FIXED** | Low | Secret-gated | ✓ |
| D-003 | `jsonic` tokeniser has no nesting-depth guard — **FIXED** | Low | Latent (stdlib decoder shields write path) | ✓ |
| D-004 | `blob` SHA-addressed read accepts unvalidated digest (panic) — **FIXED** | Low | Unwired (no SHA-addressed handler yet) | ✓ |
| D-005 | SQL injection via OQL JOIN field names — **FIXED** | **High** | Yes — default storage mode | ✓ |
| D-006 | Timeseries `int`→`uint8` narrowing before range check — **FIXED** | Low | Yes (silent-correctness only) | ✓ |
| D-007 | OQL scalar functions panic / emit non-serialisable values — **FIXED** | Low (contained to a 500) | Yes (contained to a 500) | ✓ |
| D-008 | FSM functions panic on bad indices; unbounded allocation — **FIXED** | Low panic / **High** OOM | Yes — guard eval at transition time | ✓ |
| D-009 | DDL injection via JSON-schema field names — **FIXED** | **High** | Yes — any authenticated caller | ✓ |

Severity legend: **High** = remotely reachable integrity/availability impact;
Low = contained (caught by existing recovery, bounded by a downstream guard, or
gated behind a secret or unwired path). "Committed test" = a regression test for
this defect exists in the source tree; ✗ means it was confirmed during review
with a one-off harness that was not committed (see *Regression-coverage status*
under Cross-cutting notes). Full detail for each defect follows.

### D-001 — `NEWID()` produces a malformed UUID via undefined 64-bit shift

**Package:** `pkg/fsm/eval`
**Location:** `functions.go:1218` (`fnNewID`), registered at `functions.go:123`
**Introduced:** patch007 (S6, extracted verbatim from `aulsql/pkg/tsqlruntime`)
**Detected by:** `go vet ./pkg/fsm/...` —
`functions.go:1227:3: now (64 bits) too small for shift of 64`

`fnNewID` synthesises a UUID-shaped string from a single `int64`
timestamp (`now := time.Now().UnixNano()`):

```go
now := time.Now().UnixNano()        // int64, 64 bits
uuid := fmt.Sprintf("%08X-%04X-%04X-%04X-%012X",
    now&0xFFFFFFFF,
    (now>>32)&0xFFFF,
    (now>>48)&0xFFFF,
    uint16(now>>56)&0xFFFF,
    now>>64&0xFFFFFFFFFFFF,          // <- shift of 64 on a 64-bit value
)
```

The final field shifts a 64-bit value right by 64. In Go a shift count
equal to the operand width is well-defined for unsigned operands and
yields zero, but here the operand is signed and the constant shift is
caught by `vet` as a likely mistake. The field is always `0` regardless
of input, so the last 48 bits of every generated identifier are constant.
The output is therefore not a real UUID (no version/variant bits, last
segment fixed) and collides trivially under rapid generation, since all
remaining entropy derives from one nanosecond timestamp.

The code itself flags its own provisional status: `// In production, use
a proper UUID library`.

**Why this is reachable in xolu.** `NEWID()` is registered on the FSM
`FunctionRegistry` and so is callable from FSM `set` clauses. However,
patch007 also registers proper generators (`UUID_V4()`, `UUID_V7()`,
`CUID()`, `ULID()`) on every `Evaluator` at `New()` time. `NEWID()` is
thus a redundant, lower-quality alternative to `UUID_V4()` that survived
the aulsql extraction.

**Current impact:** Low. Build is clean; `vet` exits 0 (this is a
diagnostic, not a hard error). No FSM spec in the repository calls
`NEWID()` — guards and set clauses use `UUID_V4()`/`UUID_V7()`. The
defect is latent: it bites only if a future FSM definition uses
`NEWID()` and relies on its output being unique or well-formed.

**Candidate resolutions (not yet decided — deferred for deeper analysis):**

1. Reroute `NEWID()` to `UUID_V4()` and delete `fnNewID` entirely.
   Removes the malformed generator; preserves T-SQL `NEWID()` surface
   compatibility. Likely correct, but changes the registered function's
   behaviour and so is a design decision, not a mechanical fix.
2. Repair `fnNewID` to emit a well-formed value (e.g. combine two
   independent 64-bit sources, set version/variant bits). More code to
   maintain for a function option 1 makes redundant.
3. Drop `NEWID()` from the registry if T-SQL `NEWID()` surface
   compatibility is not required for FSM expressions.

The intended semantics of the original `now>>64` field cannot be
recovered from the source — any in-place repair is a guess at intent.
Resolution is left to a deeper review that can weigh T-SQL surface
compatibility against the existing first-class generators.

**Related:** TD-001. The `NEWID()`/`UUID_V4()` consolidation is part of
the same function-surface work as the post-S8 registry refactor. Whoever
retires the redundant generator (option 1 above) should do it alongside,
not before, that refactor — they touch the same FSM eval function
registry.

**Resolution (FIXED, 2026-06-20).** Decision taken by the project owner:
**keep the `NEWID()` surface** (it is wanted in OQL) and **bind it to the
correct UUID v4 implementation** rather than dropping or aliasing-away the
function. This is a deliberate hybrid of candidate options 1 and 2 — the name is
retained on both surfaces, and the generator now produces a real v4 UUID:

- **FSM eval.** `fnNewID` (`pkg/fsm/eval/functions.go`) now returns
  `uuid.NewRandom().String()` — the same generator backing `UUID_V4()`. The
  timestamp-synthesis code (and its undefined `now>>64` shift) is gone; `go vet
  ./pkg/fsm/eval/` is now clean, closing the diagnostic that opened this entry.
- **OQL.** `NEWID()` was **added** to the OQL scalar surface: a new
  `qs.ScalarNewID` (`pkg/qs/scalar.go`), backed by the same `uuid.NewRandom`, is
  registered as `"NEWID"` in `pkg/oql.ScalarFunctions`. It was previously not in
  the OQL map at all; it is now reachable through the normal OQL dispatch path.

Both bindings produce a unique, unpredictable, structurally valid version-4
UUID. The TD-001 registry-refactor note no longer gates this: the redundant
malformed generator is not being *retired*, it is being *corrected* in place, so
there is no surface change to coordinate.

This defect had no committed test in the audit bundle; tests were added across
all three surfaces: `pkg/fsm/eval/newid_d001_test.go` (valid v4, version nibble,
no constant tail, uniqueness over 1000 calls, parity with `UUID_V4()`),
`pkg/qs/scalar_newid_test.go` (the OQL-bound scalar), and
`pkg/oql/scalar_newid_test.go` (NEWID registered in the map and dispatching to a
v4 UUID through `EvalScalarFunction`). Full `pkg/fsm`, `pkg/qs`, and `pkg/oql`
suites pass.

---

### D-002 — JWT `exp`/`nbf` expiry check silently skipped for non-numeric claim types

**Package:** `pkg/middleware`
**Location:** `auth.go:163` (`exp`) and `auth.go:170` (`nbf`), in `parseAndValidateJWT`
**Detected by:** adversarial test — a token whose `exp` is a JSON string
(rather than a number) is accepted even when the encoded time is in the past.

The expiry and not-before checks are each guarded by a type assertion to
`float64`:

```go
if exp, ok := claims["exp"].(float64); ok {
    if time.Now().Unix() > int64(exp) {
        return nil, false
    }
}
```

When `exp` is present but not a JSON number — for example `"exp":"1700000000"`
(a string) — the assertion yields `ok == false`, the body is skipped, and the
token passes expiry validation unconditionally. The numeric path is correct: a
past numeric `exp` is rejected. Only the non-numeric branch is affected. The
same pattern applies to `nbf`.

**Why this is reachable.** The check sits after signature verification, so an
attacker must already hold a token bearing a valid HS256 signature — i.e. they
must know `JWTSecret`. This is therefore a defence-in-depth weakness, not an
unauthenticated bypass: it does not let an outsider forge a token, but it does
let a holder of an otherwise-expired (or leaked, or insider-minted) token evade
the expiry control by encoding `exp` as a string. A token's lifetime should not
depend on the JSON type used to encode its expiry.

**Related (same location, policy rather than defect):** a token with **no**
`exp` claim never expires — the `if ok` guard simply does not fire. This is a
silent default; whether unbounded-lifetime tokens are acceptable should be a
deliberate policy decision, not a side effect of an absent claim.

**Candidate resolution (not yet decided).** Normalise the claim before
comparison — decode with `json.Number`, or accept both `float64` and a
string-parsed numeric form — and decide explicitly whether a missing `exp` is
permitted. Changing the missing-`exp` behaviour alters auth semantics, so it is
a design decision, not a mechanical fix.

**Current impact:** Low, contingent on secret secrecy. No change to the
unauthenticated attack surface.

**Resolution (claim-type FIXED, 2026-06-20; missing-`exp` policy OPEN).** The
type-assertion weakness is closed in `pkg/middleware/auth.go`. A helper
`claimAsUnixTime` normalises an `exp`/`nbf` claim from either a JSON number
(`float64`/`json.Number`) or a numeric string to a Unix timestamp; both `exp`
and `nbf` are checked through it. Two behaviour points:

- A token's lifetime no longer depends on the JSON type of its expiry: a past
  `exp` encoded as the string `"…"` is now rejected exactly like a past numeric
  `exp`.
- A claim that is **present but not a parseable number** now **rejects** the
  token (`!ok → return nil, false`) rather than silently skipping the check —
  the secure default for a malformed time claim.

**Missing-`exp` policy — RESOLVED (Option B, 2026-06-20).** A token with **no**
`exp` claim is now **rejected**. The project owner chose to make `exp` mandatory
rather than treat an absent expiry as never-expiring: every token must carry a
parseable expiry, so a leaked or insider-minted token cannot be valid
indefinitely. `nbf` remains optional (a token without it is valid from
issuance). Regression tests added: `TestAuthMiddleware_JWT_MissingExp_Rejected`
(no `exp` → 401) and `TestAuthMiddleware_JWT_MissingNbf_Accepted` (`exp` present,
`nbf` absent → 200). This is a deliberate auth-semantics change: any issuer that
previously minted `exp`-less tokens must now set an expiry.

This defect had no committed test in the audit bundle; a regression test was
written first (red), then the fix applied: `pkg/middleware/auth_d002_test.go` —
a past string `exp` and a future string `nbf` are rejected (401); a valid
(future) string `exp` is accepted; and the numeric path still rejects a past
`exp` (control against weakening the existing check). The four pre-existing JWT
tests still pass. Full `pkg/middleware` suite passes.

---

### D-003 — `jsonic` tokeniser has no nesting-depth guard (stack-overflow DoS)

**Package:** `pkg/jsonic`
**Location:** `tokeniser.go` — `parseValue` → `parseObject`/`parseArray` →
`parseValue` recursion; no depth counter
**Detected by:** adversarial test — input nested to ~2,000,000 levels triggers
`fatal error: stack overflow` (Go's 1 GB goroutine-stack limit). Depths up to
~500,000 parse without error; the failure is an unrecoverable runtime fatal,
not a `panic`, so it cannot be contained by `recover()` and kills the process.

The recursive-descent tokeniser tracks no depth and relies entirely on the Go
runtime's stack-growth limit as its only bound. Once that limit is crossed the
process dies; there is no graceful error return.

**Why this is currently NOT remotely reachable.** `jsonic` tokenises document
rows *already stored* in the database, not raw request bodies. The entity write
path decodes through the standard library (`json.NewDecoder().Decode`,
`handlers.go:1357`), whose `maxNestingDepth` rejects input beyond 10,000 levels
regardless of body size. A document deep enough to overflow `jsonic` therefore
cannot be stored through the normal write path — the stdlib decoder rejects it
first. The current safety is provided by that upstream decoder, **not** by
`jsonic` itself.

**Residual exposure.** Any path that writes raw JSON bytes into a document
column while bypassing the stdlib decoder — bulk import, backup restore, a
future direct-ingestion route, or a backend that stores client bytes verbatim —
would reintroduce the DoS, because `jsonic` would then tokenise attacker-shaped
nesting on read. The guarantee rests on an upstream coincidence rather than a
local invariant.

**Candidate resolution (not yet decided).** Add an explicit depth counter to
`parseObject`/`parseArray` with a configurable maximum (defaulting at or below
the stdlib's 10,000) that returns a normal error rather than recursing into a
fatal. This makes `jsonic` self-protecting independent of who fills the document
column.

**Related hygiene gap.** `config.MaxEntitySize` is validated only as `> 0`
(`config.go:1012`) with no upper bound. It is not the active guard here (the
stdlib depth limit is), but an unbounded value removes one layer of the
body-size defence; an explicit ceiling would be prudent.

**Current impact:** Low under the default write path; latent for any
decoder-bypassing ingestion path.

**Resolution (FIXED, 2026-06-20).** The tokeniser now tracks nesting depth. A
`depth` field on `Tokeniser` is incremented on entry to `parseObject`/
`parseArray` and decremented on exit (via `defer`); when it would exceed the new
`MaxNestingDepth` constant (10000, matching the stdlib json decoder ceiling),
the parser returns a normal error (`jsonic: maximum nesting depth N exceeded`)
instead of recursing into an unrecoverable fatal. `depth` is reset at the start
of every `Tokenise` call and cleared in `PutTokeniser`, so a pooled tokeniser
carries no depth state between uses. jsonic is now self-protecting independent of
who fills the document column, closing the residual exposure for any
decoder-bypassing ingestion path.

This defect had no committed test in the audit bundle; a regression test was
written first (it would not even compile before the fix, since `MaxNestingDepth`
did not exist), then the fix applied: `pkg/jsonic/tokeniser_depth_test.go` —
excessive array and object nesting each return a clean error, within-limit
nesting tokenises normally, and a pooled-reuse test confirms depth does not leak
across uses. Verified the deep-input error is the depth-limit error specifically.
Full `pkg/jsonic` suite passes, as do the `pkg/oql` and `pkg/storage` consumers.

The related `config.MaxEntitySize` hygiene gap (validated only as `> 0`, no upper
bound) is noted in this entry but not the active guard here; it is left as a
separate prudence item.

---

### D-004 — `blob` SHA-addressed reads accept an unvalidated digest (panic; latent traversal)

**Package:** `pkg/blob`
**Location:** `store.go:546` (`blobPath`, `hexSHA[:2]`), reached via
`GetBySHA`/`getBySHA` (`store.go:308`/`312`) and `PutBySHA` (`store.go:199`)
**Detected by:** adversarial test — `GetBySHA(tenant, key, "")` panics with
`slice bounds out of range [:2] with length 0`.

`blobPath` slices the first two characters of the SHA for git-style prefix
sharding (`hexSHA[:2]`) and joins the digest into the on-disk path. The
SHA argument to `GetBySHA`/`PutBySHA` is passed straight through with **no
validation** — there is no length check and no hex check, unlike the caller-key
path, which is guarded by `validateKey`. Two consequences:

1. **Panic / DoS.** A SHA shorter than two characters (`""` or `"a"`) panics in
   `hexSHA[:2]`. The panic is in an exported method.
2. **Path components from the digest.** Because the digest is never constrained
   to hex, characters such as `.` and `/` reach `filepath.Join` in the SHA
   position. `filepath.Join` normalises the result, but a relative digest can
   still resolve outside the intended shard layout.

**Relationship to the documented blob-isolation debt.** The *cross-tenant read
by content hash* risk is already recorded under "Storage-layout normalization —
deferred items" (blob plane not tenant-aware; a leaked hash could allow
cross-tenant read). That entry concerns a **well-formed** hash and the
isolation policy. D-004 is the distinct **mechanism** gap: a **malformed** SHA
reaches the filesystem layer at all, giving a panic and an unvalidated path
component. The two should be fixed together when the blob plane is reworked.

**Why this is currently NOT remotely reachable.** `GetBySHA`/`PutBySHA` are not
wired to any HTTP handler in this revision — the blob handlers route exclusively
through `Put`/`Get` by key, and keys *are* validated. The methods are exported
and callable internally, and the blob handler already emits an
`X-Blob-SHA256`/`ETag` header; the moment a SHA-addressed retrieval endpoint is
added (a natural part of the deferred tenant-aware blob rework) the panic and
the unvalidated-path behaviour become remotely reachable.

**Candidate resolution (not yet decided).** Validate the digest at the entry of
`GetBySHA`/`PutBySHA`/`blobPath`: require exactly 64 lowercase hex characters
and return `ErrNotFound` (or a dedicated `ErrSHAInvalid`) otherwise, before any
slicing or path join. Fold this into the blob-plane tenant-isolation rework
rather than shipping it standalone.

**Current impact:** Low while unwired; a loaded gun for the blob-plane rework.

**Resolution (FIXED, 2026-06-20).** A boundary validator
`validateSHA256Hex` (exactly 64 lowercase hex characters) and a dedicated
`ErrSHAInvalid` were added in `pkg/blob/store.go`. It is called at the entry of
`getBySHA` and `PutBySHA`, before `blobPath` is reached — so a short digest no
longer panics in `hexSHA[:2]`, and a non-hex digest (path separators, `..`,
wrong length) is rejected cleanly instead of contributing path components to the
on-disk layout. The internal `Put`/`PutRaw` paths are unaffected: they compute
the digest with `sha256` and always produce valid lowercase hex.

This defect had no committed test in the audit bundle; a regression test was
written first (red), then the fix applied:
`pkg/blob/store_sha_validation_test.go` — `TestGetBySHA_ShortDigest_NoPanic`
(empty/short digests return a clean error, no panic, for both Get and Put),
`TestGetBySHA_NonHexDigest_Rejected` (path-bearing and wrong-length digests
rejected), and `TestGetBySHA_ValidDigest_RoundTrip` (a real 64-hex digest is
accepted). The broader blob-plane tenant-isolation debt this was to be folded
into is now itself **resolved in 0.13.0** (see "Storage-layout normalization —
deferred items"); the per-tenant layout removes the server-level shared blob
root entirely, so the malformed-digest guard now protects a per-tenant store
root. Full `pkg/blob` suite passes.

---

### D-005 — SQL injection via OQL JOIN field names (delimited identifier bypass) — HIGH

**Package:** `pkg/oql`
**Location:** `joinFieldRef` (`sqlgen_join.go:278`), blob branches at
`sqlgen_join.go:298` and `:309`, which call `SQLDialect.JSONFieldAliasedAs`
(`sqlgen.go:157` → `:159`/`:161`); the latter interpolates the field name into
`json_extract(<alias>.data, '$.<field>')` with `fmt.Sprintf`, no
parameterisation and no escaping.
**Detected by:** adversarial test — a JOIN query whose field identifier is a
T-SQL delimited identifier (`[ ... ]`) carrying a quote/paren breakout produces
SQL with attacker-controlled text outside any bound parameter.

**Root cause.** OQL field names are validated by `validateFieldName`
(`sqlgen.go:250`), whose allowlist regex `^[a-zA-Z_][a-zA-Z0-9_.]*$` correctly
rejects quotes, parentheses, semicolons and whitespace. The **single-table**
generator routes every field through `g.fieldPath()` (`sqlgen.go:582`), which
calls `validateFieldName`. The **JOIN** generator does not: `joinFieldRef` reads
the raw `Identifier.Value` / `QualifiedIdentifier` field part (`sqlgen_join.go`
~`:248`, `:365`, and the WHERE translator ~`:362`) and passes it to
`JSONFieldAliasedAs` for blob (non-adapted) entities **without ever calling
`validateFieldName`**. The adapted-entity branch is safe because it resolves
through `adaptedNativeColumn`, a column-existence lookup that returns `""` for
unknown names; only the blob branch interpolates the raw field.

The character-level breakout is supplied by the parser. tsqlparser
(`lexer/lexer.go:318`, `readBracketedIdentifier`) strips the surrounding
brackets and stores the inner text verbatim, so `[x') UNION SELECT ...--]`
becomes an `Identifier.Value` of `x') UNION SELECT ...--`. Double-quoted
delimited identifiers (`readQuotedIdentifier`, `:343`) behave the same way.

**Confirmed payloads (generated SQL, abridged):**

- SELECT-list field `a.[x') UNION SELECT data,_version FROM t0000_nodes--]`
  produces
  `SELECT json_extract(a.data, '$.x') UNION SELECT data,_version FROM t0000_nodes--') AS ...`
  — a `UNION` that exfiltrates the entire nodes table.
- WHERE field `a.[x' OR '1'='1]` produces a predicate containing
  `... OR '1'='1` outside any placeholder.

Both the SELECT and WHERE/ON clauses of a join route through `joinFieldRef`, so
the whole join surface is affected, not just the projection list.

**Reachability.** OQL is exposed over HTTP (graph/query handlers in
`pkg/server`) and via OQL event actions (`event_dispatch.go:386`,
`runOQLAction`). Blob storage (non-adapted entities) is the default, so the
vulnerable branch is the common case, not an edge configuration. Any caller able
to submit an OQL JOIN query can reach it. In shared-tenancy mode a `UNION` over
`t0000_nodes` crosses tenant boundaries; even in per-tenant mode it defeats
entity-type scoping within the tenant. This is an integrity/confidentiality
defect, not merely a panic or a defence-in-depth gap.

**Severity:** HIGH. Unlike D-002/D-003/D-004 (latent or secret-gated), this is
reachable by an authenticated-but-unprivileged query caller against the default
storage mode and yields arbitrary read (and, depending on the executing
statement context, potentially more) outside the intended query.

**Fix (mechanical, low-risk, recommended now — not deferred).** Route the JOIN
path through the same validation as the single-table path: call
`validateFieldName` on the field inside `joinFieldRef` before the blob-branch
`JSONFieldAliasedAs`, returning an error on rejection. This mirrors
`g.fieldPath()` and is consistent with the existing allowlist; it does not change
the layout or public API. A regression test is provided
(`sqlgen_injection_test.go`): bracketed-identifier payloads in both the SELECT
and WHERE positions must be rejected at generation time, and the single-table
path is asserted to already reject the same payload.

**Note on the parser.** The bracketed-identifier passthrough in tsqlparser is
correct T-SQL lexing (delimited identifiers are supposed to hold arbitrary
text); the defect is xolu trusting that text as SQL-safe on one code path. The
fix belongs in `pkg/oql`, not in tsqlparser.

**Resolution (FIXED, 2026-06-20).** `joinFieldRef` (`pkg/oql/sqlgen_join.go`)
now calls `validateFieldName(field)` at the top of the function, before the
alias switch — so every JOIN field, in both the SELECT and the WHERE/ON
positions and on both the blob and adapted branches, is validated against the
existing allowlist (`validFieldName` `^[a-zA-Z_][a-zA-Z0-9_.]*$` plus the
`dangerousFieldChars` blocklist) before it can reach `JSONFieldAliasedAs`. This
brings the JOIN path to parity with the single-table path (`g.fieldPath` →
`validateFieldName`). A bracketed-identifier breakout is rejected at generation
time with a non-nil error; no raw payload reaches SQL text. Dotted nested paths
(e.g. `address.city`) remain allowed, as on the single-table path. Regression
test: `pkg/oql/sqlgen_injection_test.go` (both JOIN payloads rejected; the
single-table control still rejects). Full `pkg/oql` and `pkg/server` suites
pass.

---

### D-006 — Timeseries request fields narrowed `int`→`uint8` before range validation

**Package:** `pkg/server` (timeseries handlers), validated downstream in
`pkg/timeseries`
**Locations:**
- `ts_handlers.go:886` — `NumField: uint8(req.NumField)`; validated as
  `q.NumField > 6` afterwards in `store.go:600`.
- `ts_handlers.go:407` — `Dims: uint8(req.Dims)`; validated as
  `cfg.Dims < MinDims || cfg.Dims > MaxDims` afterwards in `registry.go:113`.
**Detected by:** characterization test — request integers whose low byte lands
in the valid window pass the range check as a different value.

Both request structs declare the field as `int` (decoded from JSON), then
convert to `uint8` *before* the range check runs on the already-truncated value.
The high bits are discarded, so an out-of-range request can alias an in-range
one:

- `num_field`: `256 → 0`, `262 → 6`, `513 → 1` — all `≤ 6`, all pass the `> 6`
  guard. The request is served against the wrong numeric field.
- `dims`: `257 → 1` … `261 → 5` — all land in `[1,5]` and are accepted; the
  timeline is defined with a different dimension count than requested. (`256 → 0`
  is correctly rejected because 0 is below `MinDims`, so only the `257..261`
  band aliases.)

**Impact: low (correctness, not memory safety).** This is *not* an out-of-bounds
read: the aggregate path has a second guard, `int(q.NumField) >= len(nums)`
(`store.go:668`/`:887`), that clamps the field index against the actual array,
and the dims value is internally consistent with whatever was stored. The defect
is that an invalid request is silently accepted as a valid-but-different one
instead of being rejected — the caller's intent is changed with no error
returned. For `dims` this also persists: a timeline is created with the wrong
shape.

**Root cause is a single pattern.** Narrowing a request `int` to `uint8` before
validating its range. It appears in (at least) the two sites above; any future
handler that follows the same shape will inherit the bug.

**Candidate resolution (mechanical).** Validate the request value as an `int`
against its intended inclusive range *before* the `uint8` conversion, e.g.
reject `req.NumField < 0 || req.NumField > 6` and
`req.Dims < int(MinDims) || req.Dims > int(MaxDims)` at the handler, then
convert. Low-risk; does not change the wire format or the valid-input behaviour.
A characterization test is provided (`ts_numeric_validation_test.go`)
demonstrating the aliasing; it should be tightened to assert handler-level
rejection once the guard is added.

**Resolution (FIXED, 2026-06-20).** Both handler sites in
`pkg/server/ts_handlers.go` now validate the raw request `int` against its
intended range *before* the `uint8` conversion:

- `HandleTSAggregate`: rejects `num_field < 0 || num_field > 6` with a `400`
  (`XOLU-TS009`, the established num-field code) before building the
  `AggregateQuery`.
- `HandleTSDefineTimeline`: rejects `dims` outside `[MinDims, MaxDims]` with a
  `400` before constructing the `TimelineConfig`.

The characterization test was replaced with handler-level tripwires
(`ts_numeric_validation_test.go`, now package `server_test`): the aliasing
values that previously slipped past the downstream `uint8` guards — `num_field`
256/262/513 (→0/6/1) and `dims` 257–261 (→1–5) — are now asserted to return
`400`, with a control test confirming legitimate `num_field` 0–6 and `dims` 1–5
are still accepted. Verified as a genuine tripwire (fails with the guard
removed: the aliasing values return 200).

Two pre-existing tests were updated to match the now-earlier, more precise
rejection: `TestTSError_NumFieldOutOfRange` still expects `XOLU-TS009` (the guard
preserves that code), and `TestTSError_MalformedBody_Define/missing_dims` now
expects `400` instead of `409` — `dims=0` is a bad request, which the handler
previously could not discriminate from a conflict (the test's own comment
anticipated this). Full `pkg/server` and `pkg/timeseries` suites pass.

---

### D-007 — OQL-exposed scalar functions panic / produce non-serialisable values on edge inputs

**Package:** `pkg/qs` (function bodies), exposed via `pkg/oql/scalar.go`,
executed per-row by the OQL executor and marshalled in `handleOQLQuery`
**Detected by:** adversarial test — `SUBSTRING` with a negative length panics;
`ROUND` with a large precision yields NaN, which cannot be JSON-encoded.

Two reachable edge cases in functions registered on the OQL scalar surface
(`pkg/oql/scalar.go`) and callable from `SELECT`:

1. **`SUBSTRING` slice panic.** `ScalarSubstring` (`scalar.go:169`) computes
   `end := start + length` and clamps only `end > len(s)` (`:184`), not
   `end < start`. A negative length (`SELECT SUBSTRING(field, 2, -5)`) makes
   `end < start`, so `s[start:end]` panics with `slice bounds out of range`.
   The same applies across a range of start/length combinations.

2. **`ROUND` → NaN → response marshal failure.** `ScalarRound` (`scalar.go:317`)
   computes `shift := math.Pow(10, precision)` (`:331`); for precision ≳ 309 the
   shift overflows to `+Inf` and `math.Round(f*Inf)/Inf` is `NaN`. `ROUND` is
   OQL-exposed, so `SELECT ROUND(field, 400)` puts a `NaN` in the result row.
   `encoding/json` rejects `NaN`/`Inf`, so the entire response fails to marshal.

**Impact: low, contained by existing infrastructure.**
- The `SUBSTRING` panic is caught by chi's `Recoverer` middleware
  (`server.go:691`), so the process survives; the request returns a 500 rather
  than crashing the server. It is a robustness defect (a query function should
  return a value or a typed error, not panic), not an availability threat.
- The `ROUND`/`NaN` case is handled gracefully at the response layer: the
  marshal error is caught (`handlers.go:1117`) and converted to a clean
  `500 failed to encode response`. The query cannot return a result, but
  nothing crashes.

Both are reachable by any caller able to submit an OQL `SELECT` with these
functions; neither crosses a tenant or data boundary.

**Note on scope.** `SQRT` and `POWER` (which also produce NaN/Inf on
`SQRT(-1)` / `POWER(1e308, 2)`) exist in `pkg/qs` but are **not** registered in
`pkg/oql/scalar.go`, so they are not reachable through OQL today. Only
OQL-registered functions (`SUBSTRING`, `ROUND`, and any other numeric/index
function with unguarded edges) are in scope. Any future registration of an
unguarded `qs` scalar would extend this surface.

**Candidate resolution (mechanical).** Guard the edge cases at the function
boundary: in `ScalarSubstring`, clamp `end = max(end, start)` (or return `""`
when `end < start`); in numeric functions that can yield `NaN`/`Inf` (e.g.
`ROUND` with extreme precision), bound the precision argument or coerce a
non-finite result to `nil` before it reaches the result set. Returning `nil`
for non-finite scalar results would also make a future `SQRT`/`POWER`
registration safe by default. A regression test is provided
(`scalar_adversarial_test.go`): the `SUBSTRING` panic tests must go green
(clamped, no panic) after the fix.

**Resolution (FIXED, 2026-06-20).** Both edge cases are guarded in
`pkg/qs/scalar.go`:

- **`ScalarSubstring` panic.** After the existing upper-bound clamp, `end` is
  now clamped to `max(end, start)`, so a negative length yields an empty result
  instead of slicing backwards. No panic.
- **`ScalarRound` non-finite result.** The rounded value is checked with
  `math.IsNaN`/`math.IsInf` and coerced to `nil` (SQL NULL) when non-finite, so
  a large precision can no longer put a NaN/Inf into a result row and break
  response marshalling. (Per the audit's note, coercing non-finite scalars to
  `nil` would also make a future `SQRT`/`POWER` registration safe by default;
  those remain unregistered in `pkg/oql/scalar.go` and out of scope here.)

The `ROUND` test was tightened from characterization to a tripwire: it now
asserts the result is `nil` and JSON-serialisable. Full `pkg/qs`, `pkg/oql`, and
`pkg/server` suites pass.

**Follow-up (2026-06-20).** The fuzz target `FuzzScalarFunctions`
(`pkg/qs/fuzz_scalar_test.go`), added during the post-release property/fuzz
hardening pass, surfaced the same non-finite issue in `SQRT` (`SQRT(-1) = NaN`)
and `POWER` (`POWER(1e308, 2) = +Inf`). These are **not** registered on the OQL
surface (so they were correctly out of D-007's original scope), but they share
the `pkg/qs` scalar registry used by OQL and Sulpher. Per the audit's own
recommendation that coercing non-finite results to `nil` would make a future
`SQRT`/`POWER` registration safe by default, both now coerce `NaN`/`Inf` to `nil`
(`pkg/qs/scalar.go`), pinned by `TestScalarSqrt_NonFinite_CoercedToNil` and
`TestScalarPower_NonFinite_CoercedToNil`.

---

### D-008 — FSM functions panic on bad indices and allocate unboundedly (guard-driven DoS)

**Package:** `pkg/fsm/eval` (`functions.go`), reached via
`ExpressionEvaluator.Evaluate` → `FunctionRegistry.Call` (`evaluator.go:346`)
**Detected by:** a systematic panic-fuzz of all ~180 registered functions plus
bounded allocation probes. Across the whole registry, exactly two functions
panic on hostile arguments and three allocate without bound.

The FSM function library (extracted from aulsql; the same lineage as D-001 and
the `qs` scalars in D-007) is exposed to guard and set expressions. There is no
function allowlist: a guard may call any registered function.

**Panics (invalid slice).**
- `SUBSTRING` (`functions.go:247`): `length` is read from the third argument with
  no lower bound (`:239`); `end = start + length` is clamped only for
  `end > len(s)` (`:243`), not for `end < start`. A negative length
  (`SUBSTRING('hello', 1, -5)`) makes `end < 0`, so `s[start:end]` panics with
  `slice bounds out of range`.
- `STUFF` (`functions.go:449`): identical shape — `end = start + length`
  (`:444`) clamped only on the upper side, so a negative length panics in
  `s[end:]`.

**Unbounded allocation (OOM).**
- `REPLICATE` (`functions.go:481`): `strings.Repeat(s, n)` with `n` a
  user-supplied int guarded only `n < 0`. `REPLICATE('x', 1e12)` allocates ~1 TB.
- `SPACE` (`functions.go:495`): `strings.Repeat(" ", n)`, same.
- `STR` (`functions.go:517`): `length` becomes a `fmt` field width
  (`fmt.Sprintf("%%%d.%df", length, decimals)`); a huge width allocates a
  correspondingly huge padded string.

**Severity split.**
- The **panics** are caught per-request by chi's `Recoverer` (`server.go:691`):
  the process survives, the triggering request 500s. Robustness defect, low
  severity — consistent with D-007.
- The **allocations** are the more serious case. A large enough count produces
  `fatal error: runtime: out of memory`, which is **not** a `panic` and **cannot
  be caught by `recover()`** — `Recoverer` does not help. One evaluated
  `REPLICATE('x', <huge>)` can kill the whole server process. This is a
  guard-driven, process-wide DoS.

**Reachability.** FSM definitions are accepted over HTTP (`pkg/server`,
`v2_fsm_common.go`); each transition carries `guard`/set expression strings.
Definition validation is **parse-only** — "Guard and set expression syntax
(parse only, no evaluation)" (`v2_fsm_common.go:420`, `ParseGuard` at `:424`) —
so neither the panic nor the OOM fires at definition time. They fire when the
guard is **evaluated at transition time**, i.e. when an event drives the machine
through that transition. The expressions can be constant
(`REPLICATE('x', 1000000000) = ''`), so no attacker-controlled data is required
beyond the definition and an event that triggers the transition. An actor able
to register an FSM definition and fire a matching event can therefore OOM the
server.

**Note on existing coverage.** The existing `eval` adversarial suite
(`eval_adversarial_test.go`, `TestAdversarial_NoPanicOnHostileInput`) exercises
operator and structural hostility (chained equality, deep nesting, mixed
null/bool) but contains no function-call payloads, which is why this surface was
not previously caught. `pkg/fsm/eval/functions.go` (2082 lines, ~40% of the
package) had no adversarial coverage.

**Candidate resolution (mechanical).**
- `SUBSTRING`/`STUFF`: clamp `end = max(end, start)` (and `end ≥ 0`) before
  slicing, returning an empty/`NULL` result for a negative length, matching
  T-SQL semantics.
- `REPLICATE`/`SPACE`/`STR`: bound the count/width argument against a configured
  maximum (e.g. the existing query/response size limits) and return a clean
  error when exceeded, so a guard cannot request an arbitrarily large
  allocation. A shared helper for "function output size limit" would cover all
  three and any future allocator.

A regression test is provided (`functions_adversarial_test.go`): the
`SUBSTRING`/`STUFF` panic tests must go green after the fix; the allocation test
documents the unbounded behaviour and should assert a bounded error once a limit
is added.

**Resolution (FIXED, 2026-06-20).** Both sub-classes are closed in
`pkg/fsm/eval/functions.go`:

- **Slice panics.** `fnSubstring` and `fnStuff` now clamp `end = max(end, start)`
  after the existing upper-bound clamp, so a negative `length` yields an empty
  selection (matching T-SQL) instead of slicing backwards. No panic.
- **Unbounded allocation.** A package-level limit `maxFunctionOutputBytes`
  (16 MiB) and a shared `checkOutputSize(fn, n)` helper were added. `fnReplicate`
  (projected size `len(s)*n`), `fnSpace` (`n`), and `fnStr` (the format field
  width) check their projected output against the limit *before* allocating and
  return a clean error when it is exceeded — so a guard can no longer drive an
  out-of-memory fatal. The limit is well above any legitimate guard/set output.

The allocation test was tightened from characterization to a tripwire: it now
asserts an attack-scale count (`~1e12`) returns a clean error (no panic, no
allocation) and that small counts still succeed. Verified end-to-end: an
attack-scale `REPLICATE` surfaces through `EvalGuard` as
`REPLICATE output size … exceeds limit 16777216`. Full `pkg/fsm/eval` suite
passes.

---

### D-009 — DDL injection via JSON-schema field names (adapted-table registration) — HIGH

**Package:** `pkg/storage` (DDL construction), reached from `pkg/server`
**Location:** field name → column name verbatim in `DeriveAdaptedTableSpecFrom`
(`adapted.go:218`, from `schema.FieldNames()` at `:151`); interpolated into DDL
by `CreateTableSQL` (`dialect_sqlite.go:78`) and `schema_evolution.go`
(`ALTER TABLE … DROP COLUMN %s` `:221`, `CREATE … INDEX %s ON %s (%s)` `:251`);
executed by `RegisterAdaptedTable` (`adapted_crud.go:127`,
`db.ExecContext(ddl)`).
**Detected by:** end-to-end test — a schema property key
`evil TEXT); DROP TABLE t0000_nodes;--` becomes a column name and is emitted
into the `CREATE TABLE` verbatim.

**Root cause.** When an entity schema is registered, each JSON-schema property
key is turned directly into a SQL column name (`Name: fieldName`,
`adapted.go:218`) with **no identifier validation** — no allowlist, no quoting,
no rejection of non-identifier characters. The keys come straight from
`schema.FieldNames()` (`:151`), i.e. the `properties` object of the uploaded
schema, which is entirely caller-controlled. The resulting column name is
interpolated with `fmt.Sprintf("%s …")` into `CREATE TABLE`, `ALTER TABLE …
ADD/DROP COLUMN`, and `CREATE INDEX` statements, none of which can parameterise
an identifier, and all of which are executed via `ExecContext`.

**Confirmed output (abridged).** A property key
`evil TEXT); DROP TABLE t0000_nodes;--` yields:

```sql
CREATE TABLE … (
    evil TEXT); DROP TABLE t0000_nodes;-- TEXT,
    …
```

The key closes the column definition and the `CREATE TABLE` with `)`, then
introduces a second statement.

**The injection chains.** `modernc.org/sqlite` (the configured driver) executes
multiple `;`-separated statements in a single `ExecContext` call — confirmed by
test (a chained `DROP TABLE` in one `Exec` drops the table). So the payload is
not limited to corrupting the single `CREATE`; it can append arbitrary
DDL/DML — `DROP TABLE`, `ALTER TABLE`, `UPDATE`/`DELETE`, or `ATTACH DATABASE`
(which can create files on disk). This is materially more powerful than the
read-oriented `UNION` of D-005.

**Reachability.** `POST /api/v1/schema/{entity}` (`server.go:903`,
`handleCreateSchema`) decodes the schema body and calls
`RegisterAdaptedEntity` → `RegisterAdaptedTable` for the default `SQLiteStore`
(`handlers.go:1292`), which generates and executes the DDL. The `entity` URL
segment is validated (`validateEntityName`), but the **property keys in the body
are not**. The route is exempt from the tenant-context requirement in strict
mode (`server.go:376` allows the `/api/v1/schema/` prefix through), so the only
gate is global authentication: any authenticated caller, with no tenant scoping
or elevated privilege, can register a schema. The injected DDL runs against the
store backing the adapted tables.

**Severity:** HIGH — the most serious issue in this document. Unlike D-005 (read
via `UNION`, JOIN queries only), this permits destructive and schema-altering
SQL (`DROP`/`ALTER`/`ATTACH`) reachable by any authenticated user through a
single schema upload, against the default storage backend.

**Fix (recommended now).** Validate every schema field name as a strict SQL
identifier before it becomes a column or index name — reuse the same allowlist
discipline as OQL's `validateFieldName` (`^[a-zA-Z_][a-zA-Z0-9_]*$`), applied at
the boundary in `DeriveAdaptedTableSpecFrom` (and to index names/columns in
`schema_evolution.go`), rejecting the schema with a 400 on any non-identifier
key. Identifier quoting alone is insufficient and error-prone here; an allowlist
that rejects is safer. This is an additive guard, not a redesign.

**Regression guard.** The provided test
(`adapted_injection_test.go`) asserts a malicious field name is rejected at
derivation; the companion test pins the driver's multi-statement behaviour so
the chained-DDL assumption is documented. A property test over
`DeriveAdaptedTableSpecFrom` — for any property key, every emitted column/index
name matches the identifier allowlist — would be the stronger long-term guard,
matching the D-005 recommendation.

**Related:** D-005. Both are identifier-trust failures where one code path skips
a validation that the allowlist already expresses; D-005 is read-scoped in OQL
JOINs, D-009 is write/DDL-scoped in schema registration. They share a fix shape
(validate identifiers at the boundary) and should be closed together.

**Resolution (FIXED, 2026-06-20).** Identifier validation was added at both
boundaries:

- **Storage layer (primary fix).** `validateAdaptedFieldName` (allowlist
  `^[a-zA-Z][a-zA-Z0-9_]*$`) is called for every schema field name at the top of
  the per-field loop in `DeriveAdaptedTableSpecFrom` (`pkg/storage/adapted.go`),
  before any column or index name is derived. A non-identifier key now returns
  an error from derivation, so no injected DDL can be generated. Both derivation
  entry points are covered (`DeriveAdaptedTableSpec` delegates to `…From`).
- **HTTP layer (clean 400).** `validateSchemaFieldNames` runs in
  `handleCreateSchema` (`pkg/server/handlers.go`) immediately after JSON decode
  and *before* `LoadSchema`/`SaveSchema`, so a malicious schema is rejected with
  a `400` (`XOLU-ST003`) before any persistence, rather than failing late with a
  `500` after the schema is half-committed.

The allowlist is letter-first to match the existing entity/field-name
convention (`identifierRe`, OQL `validFieldName`). Regression tests:
`pkg/storage/adapted_injection_test.go` (derivation rejects; driver chains
statements) and `pkg/server/schema_injection_test.go`
(`TestHandleCreateSchema_MaliciousFieldName_Rejected` → 400;
`TestHandleCreateSchema_ValidFieldNames_Accepted` control). Full `pkg/storage`
and `pkg/server` suites pass.

---

No other open issues at this revision.

---

## Cross-cutting notes (D-005 – D-009)

These defects fall into two families, each with a light, low-risk fix shape and
a matching way to keep it from regressing. None of this is a remediation plan —
just the recommended direction.

### Input-validation defects (D-005, D-006, D-009)

Well-formed input is mishandled: a delimited identifier reaches SQL unescaped in
OQL JOINs (D-005); a schema field name reaches DDL unescaped (D-009); an
out-of-range integer is narrowed before it is checked (D-006). D-005 and D-009
are the same identifier-trust failure on two different write/read paths and
should be fixed together.

- **Fix shape.** Validate at the boundary the safe path already uses. D-005 and
  D-009: route the field/column name through an identifier allowlist (OQL's
  `validateFieldName`, `^[a-zA-Z_][a-zA-Z0-9_]*$`) — the allowlist already
  exists; the JOIN generator and the schema-derivation path each skip it. D-006:
  check the request `int` against its intended range *before* the `uint8`
  conversion. All are additive guards, not redesigns.
- **Regression guard.** Table-driven unit tests asserting the boundary rejects
  the bad value. For D-005/D-009 the stronger guard is a *property* test over
  the SQL/DDL generator: for any identifier, the emitted SQL must contain no
  unescaped quote, parenthesis, or semicolon from that identifier. A property
  check catches breakouts that a fixed payload list would miss.

### Hostile-input robustness defects (D-003, D-004, D-007, D-008)

Malformed or extreme input causes a panic or an unbounded allocation: deep JSON
nesting (D-003), a short/invalid SHA (D-004), out-of-range string indices and
non-finite numerics (D-007), negative lengths and unbounded `REPLICATE`/`SPACE`/
`STR` (D-008).

- **Fix shape.** Clamp or bound at the function boundary and return a value or a
  typed error instead of slicing/allocating blindly: clamp slice indices to
  `[0, len]` with `end ≥ start`; cap user-supplied repeat counts and format
  widths against a configured maximum; coerce non-finite numerics to `NULL`
  before they reach a result set; add an explicit depth counter to the
  tokeniser. The OOM cases (D-008 `REPLICATE`/`SPACE`/`STR`) deserve priority
  within this family: a fatal out-of-memory is **not** a `panic` and is **not**
  caught by chi's `Recoverer`, so it takes the whole process down, unlike the
  slice panics which currently degrade to a per-request 500.
- **Regression guard.** These are the textbook case for Go's native fuzzing
  (`go test -fuzz`). A fuzz target per boundary — the JSON tokeniser, the blob
  SHA path, the OQL/`qs` scalar functions, and the FSM function registry — with
  a simple invariant ("must not panic; output size bounded") will re-find this
  whole class automatically and persist any crasher into `testdata/` as a
  permanent seed. The one-off harnesses used to find D-007/D-008 can be promoted
  into such targets later; the committed regression tests
  (`scalar_adversarial_test.go`, `functions_adversarial_test.go`) already pin the
  specific cases found here.

### Auth-logic defects (D-002)

A security check is silently skipped because of a type assumption rather than a
malformed or out-of-range value: a JWT `exp`/`nbf` encoded as a non-numeric JSON
type fails the `.(float64)` assertion, so the expiry branch never runs (D-002).
This is neither an injection sink nor a hostile-input crash — the token is
well-formed and validly signed; the bug is that one encoding evades a control.

- **Fix shape.** Normalise the claim before comparison (decode with
  `json.Number`, or accept both a number and a numeric string) and decide
  explicitly whether a missing `exp` is permitted. The missing-`exp` decision
  changes auth semantics, so it is a design choice, not a mechanical edit.
- **Regression guard.** Unit tests asserting that a token with `exp`/`nbf` in a
  non-numeric type, or with the claim absent, is treated per the chosen policy
  (rejected when past / when required). Fuzzing does not help here — the input is
  well-formed; only a test that knows the intended security semantics catches it.

### Regression-coverage status

Committed regression tests now exist for every defect: D-002
(`auth_d002_test.go`), D-003 (`tokeniser_depth_test.go`), D-004
(`store_sha_validation_test.go`), D-005 (`sqlgen_injection_test.go`), D-006
(`ts_numeric_validation_test.go`), D-007 (`scalar_adversarial_test.go`), D-008
(`functions_adversarial_test.go`), and D-009 (`adapted_injection_test.go`).
D-005–D-009 came from the original audit bundle; D-002, D-003, and D-004 were
added when those defects were fixed (each written red-first). D-001 remains a
deferred design decision (see its entry) rather than a code defect with a guard.

### Scope note

Fuzzing covers the robustness family (D-003/D-004/D-007/D-008) but not the
auth-logic or input-validation families (D-002/D-005/D-006/D-009), which were
found by reading the relevant code paths and which random input does not
surface. Both bug
classes are present here, so both kinds of check are worth keeping:
property/unit tests for the logic defects, fuzz targets for the robustness
defects.

---

---
