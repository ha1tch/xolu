// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ha1tch/xolu/pkg/tenant"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// SQLiteBookingSource is the persistent, authoritative booking record (H1),
// backed by the S11 cal_* tables in the tenant's primary store. It implements
// the same Store interface as MemBookingSource, so the lifecycle, commit, and
// seal logic — and every test — are unchanged behind it.
//
// TIME DISCIPLINE (xolutime invariant, docs/TIME_HANDLING.md):
//   - Every persisted timestamp column is an absolute UTC instant stored as
//     int64 UnixNano. The boundary is exactly instantToNanos / nanosToInstant
//     below — instant.UnixNano() (always UTC, since ot.Instant is UTC-internal)
//     on write, ot.FromUnixNano(ns) on read. No time.Time ever crosses this
//     boundary, and this file never calls time.Now(): "now" for created/updated
//     is minted upstream via ot.Now() (in Lifecycle.Create) and merely serialised
//     here, so storage is a pure serialiser with no clock access.
//
// Tenancy follows xolu config (GATE-3 #1): the tenant_id column discriminates in
// shared-file mode and is constant 0 in per-file mode, identical to every other
// v2 table. The caller supplies the tenant id; this source does not decide it.
type SQLiteBookingSource struct {
	db       *sql.DB
	tenantID tenant.TenantID
	reuse    bool // OrdinalReuse policy (GATE-3 #2); retired-pool lives in 0x03 metadata
}

// NewSQLiteBookingSource binds a source to an open *sql.DB (the tenant's primary
// store, with the S11 schema already migrated) and a tenant id. reuse selects
// the OrdinalReuse policy (false = retire, the safe default).
func NewSQLiteBookingSource(db *sql.DB, tenantID tenant.TenantID, reuse bool) *SQLiteBookingSource {
	return &SQLiteBookingSource{db: db, tenantID: tenantID, reuse: reuse}
}

// TenantID returns the source's bound tenant identity — the dxp cal
// adapter uses this to validate the coordinator-supplied tenant key
// against the store it actually holds, matching bal.Store.TenantID's
// own precedent exactly.
func (s *SQLiteBookingSource) TenantID() tenant.TenantID { return s.tenantID }

// --- The xolutime boundary: the ONLY place instants become nanos and back ---

// instantToNanos serialises a UTC instant to int64 UnixNano for storage. The
// instant is already UTC (ot.Instant is UTC-internal), so this is the
// invariant-correct write form — no .UTC() juggling, no time.Time.
func instantToNanos(i ot.Instant) int64 { return i.UnixNano() }

// nanosToInstant reconstructs a UTC instant from stored UnixNano. Always via
// ot.FromUnixNano (never time.Unix without .UTC()).
func nanosToInstant(ns int64) ot.Instant { return ot.FromUnixNano(ns) }

// nullableInstantToNanos returns a *int64 for a buffer/optional instant: NULL
// (nil) when the instant is zero, the UnixNano otherwise.
func nullableInstantToNanos(i ot.Instant) *int64 {
	if i.IsZero() {
		return nil
	}
	n := i.UnixNano()
	return &n
}

// nanosToNullableInstant reconstructs an optional instant: the zero Instant for
// a NULL column, otherwise the UTC instant.
func nanosToNullableInstant(ns sql.NullInt64) ot.Instant {
	if !ns.Valid {
		return ot.Zero()
	}
	return ot.FromUnixNano(ns.Int64)
}

// --- Calendar CRUD ---

// CreateCalendar inserts a calendar, allocating its dense ordinal via the
// cal_ord_seq monotonic allocator (the SQLite analogue of the in-memory counter;
// GATE-3 #5). Defaults mirror MemBookingSource.
func (s *SQLiteBookingSource) CreateCalendar(c Calendar) (Calendar, error) {
	if c.CalendarID == "" {
		return Calendar{}, fmt.Errorf("cal: CreateCalendar: empty calendar_id")
	}
	if c.DefaultState == "" {
		c.DefaultState = StateBinding
	}
	if c.MatchPolicy == "" {
		c.MatchPolicy = ConsiderBinding
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Calendar{}, err
	}
	defer tx.Rollback() //nolint:errcheck // rolled back unless committed

	// Reject duplicates explicitly for a clear error (the PK would also catch it).
	var exists int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM cal_calendars WHERE tenant_id=? AND calendar_id=?`,
		s.tenantID, c.CalendarID).Scan(&exists); err != nil {
		return Calendar{}, err
	}
	if exists > 0 {
		return Calendar{}, fmt.Errorf("cal: CreateCalendar: %w: %q", ErrCalendarExists, c.CalendarID)
	}

	ord, err := s.allocOrdinalTx(tx)
	if err != nil {
		return Calendar{}, err
	}
	c.Ordinal = ord

	if _, err := tx.Exec(
		`INSERT INTO cal_calendars
		   (tenant_id, calendar_id, ordinal, entity_ref, default_state, match_policy)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		s.tenantID, c.CalendarID, uint32(c.Ordinal), c.EntityRef,
		string(c.DefaultState), string(c.MatchPolicy)); err != nil {
		return Calendar{}, err
	}
	if err := tx.Commit(); err != nil {
		return Calendar{}, err
	}
	return c, nil
}

// allocOrdinalTx returns the next dense ordinal using the cal_ord_seq allocator,
// the same INSERT ... ON CONFLICT DO UPDATE ... RETURNING pattern xolu's node and
// fsm sequences use. The retire/reuse policy's retired-pool is index (0x03)
// metadata, not modelled here; under retire (default) the counter only advances.
func (s *SQLiteBookingSource) allocOrdinalTx(tx *sql.Tx) (CalOrdinal, error) {
	var next uint32
	err := tx.QueryRow(
		`INSERT INTO cal_ord_seq (tenant_id, next_ord) VALUES (?, 1)
		 ON CONFLICT(tenant_id) DO UPDATE SET next_ord = next_ord + 1
		 RETURNING next_ord`,
		s.tenantID).Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("cal: ordinal allocation: %w", err)
	}
	return CalOrdinal(next), nil
}

// DeleteCalendar removes a calendar. Its bookings must already be gone.
func (s *SQLiteBookingSource) DeleteCalendar(calendarID string) error {
	res, err := s.db.Exec(
		`DELETE FROM cal_calendars WHERE tenant_id=? AND calendar_id=?`,
		s.tenantID, calendarID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("cal: DeleteCalendar: unknown %q", calendarID)
	}
	return nil
}

// --- Booking CRUD ---

// PutBooking inserts or replaces a booking record.
func (s *SQLiteBookingSource) PutBooking(b Booking) error {
	if err := s.validatePutBooking(&b, func(id string) bool { _, ok := s.calendar(id); return ok }); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`INSERT INTO cal_bookings
		   (tenant_id, calendar_id, booking_id, state, start_utc, end_utc, mode,
		    bearer, buffer_after_utc, created_utc, updated_utc, detail_ref)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id, calendar_id, booking_id) DO UPDATE SET
		    state=excluded.state, start_utc=excluded.start_utc, end_utc=excluded.end_utc,
		    mode=excluded.mode, bearer=excluded.bearer,
		    buffer_after_utc=excluded.buffer_after_utc,
		    updated_utc=excluded.updated_utc, detail_ref=excluded.detail_ref`,
		s.tenantID, b.CalendarID, b.BookingID, string(b.State),
		instantToNanos(b.Span.Start), instantToNanos(b.Span.End), string(b.Mode),
		b.Bearer, nullableInstantToNanos(b.BufferAfter),
		instantToNanos(b.CreatedAt), instantToNanos(b.UpdatedAt), b.DetailRef)
	return err
}

// validatePutBooking is PutBooking's guard logic, extracted so
// putBookingInTx (T-54/item 19: the dxp cal adapter's Execute path)
// enforces the identical rules -- mode coercion mutates b, so both
// callers must go through this rather than duplicate it and risk the
// two drifting apart. calendarExists is injected rather than hardcoded
// to s.calendar because the tx-scoped caller needs a tx-scoped
// existence check (calendarInTx) -- calling the non-tx calendar()
// while a *sql.Tx is open on the same underlying *sql.DB deadlocks
// SQLite (found by running the dxp adapter's tests, not by reasoning
// about it).
func (s *SQLiteBookingSource) validatePutBooking(b *Booking, calendarExists func(string) bool) error {
	if b.BookingID == "" || b.CalendarID == "" {
		return fmt.Errorf("cal: PutBooking: empty booking_id or calendar_id")
	}
	if !b.Span.Valid() {
		return fmt.Errorf("cal: PutBooking: invalid span")
	}
	if !calendarExists(b.CalendarID) {
		return fmt.Errorf("cal: PutBooking: %w: %q", ErrUnknownCalendar, b.CalendarID)
	}
	// Mode rule: only ModeExclusive is valid. Empty mode coerces to
	// ModeExclusive for callers that omit it. Any other value is
	// rejected — the vocabulary was reduced in v0.14.12 (see Mode godoc).
	if b.Mode == "" {
		b.Mode = ModeExclusive
	}
	if b.Mode != ModeExclusive {
		return fmt.Errorf("cal: PutBooking: %w: %q", ErrModeNotSupported, string(b.Mode))
	}
	// Bearer rule (review issue 2): a binding booking requires a live bearer.
	if b.State == StateBinding && !ValidEntity(b.Bearer) {
		return fmt.Errorf("cal: PutBooking: %w", ErrBearerRequired)
	}
	return nil
}

// putBookingInTx is PutBooking's transactional twin (composability
// locality, @D06): writes through a caller-supplied *sql.Tx instead of
// s.db directly, so it can participate in dxp's shared, single-tenant
// SQL transaction (proposal §11) the same way bal.transferInTx and
// fsm's tx-scoped apply already do. cal's H1 record (this table) is
// guard-bearing and lives in the same tenant SQLite file as
// bal/fsm/entity per dxp-composed-commitment.md's own composability
// locality principle; H3 (the Pebble occupancy index) is advisory and
// deliberately NOT touched here -- it is updated after commit, exactly
// like bal's rollup plane, never inside the guard's own transaction.
func (s *SQLiteBookingSource) putBookingInTx(ctx context.Context, tx *sql.Tx, b Booking) error {
	if err := s.validatePutBooking(&b, func(id string) bool { _, ok := s.calendarInTx(ctx, tx, id); return ok }); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO cal_bookings
		   (tenant_id, calendar_id, booking_id, state, start_utc, end_utc, mode,
		    bearer, buffer_after_utc, created_utc, updated_utc, detail_ref)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id, calendar_id, booking_id) DO UPDATE SET
		    state=excluded.state, start_utc=excluded.start_utc, end_utc=excluded.end_utc,
		    mode=excluded.mode, bearer=excluded.bearer,
		    buffer_after_utc=excluded.buffer_after_utc,
		    updated_utc=excluded.updated_utc, detail_ref=excluded.detail_ref`,
		s.tenantID, b.CalendarID, b.BookingID, string(b.State),
		instantToNanos(b.Span.Start), instantToNanos(b.Span.End), string(b.Mode),
		b.Bearer, nullableInstantToNanos(b.BufferAfter),
		instantToNanos(b.CreatedAt), instantToNanos(b.UpdatedAt), b.DetailRef)
	return err
}

// SetStateFrom transitions a booking's state if and only if it is still
// in the expected from-state. The guarded UPDATE is the compare half of
// the T-34 compare-and-swap: under concurrent racers, SQLite serialises
// the writes and exactly one matches the WHERE state=? clause; the rest
// see zero rows affected and fail with ErrIllegalTransition.
func (s *SQLiteBookingSource) SetStateFrom(calendarID, bookingID string, from, to State) error {
	b, ok := s.booking(calendarID, bookingID)
	if !ok {
		return fmt.Errorf("cal: SetStateFrom: unknown booking %q/%q", calendarID, bookingID)
	}
	if to == StateBinding && !ValidEntity(b.Bearer) {
		return fmt.Errorf("cal: SetStateFrom: binding requires a live bearer")
	}
	res, err := s.db.Exec(
		`UPDATE cal_bookings SET state=? WHERE tenant_id=? AND calendar_id=? AND booking_id=? AND state=?`,
		string(to), s.tenantID, calendarID, bookingID, string(from))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// The booking exists (checked above) but is no longer in the
		// expected state: a concurrent transition won.
		return fmt.Errorf("%w: %s -> %s for %q (state changed concurrently; lost the race)",
			ErrIllegalTransition, from, to, bookingID)
	}
	return nil
}

// setSpan updates a booking's span (the move write).
func (s *SQLiteBookingSource) setSpan(calendarID, bookingID string, to Span) error {
	if !to.Valid() {
		return fmt.Errorf("cal: setSpan: invalid span")
	}
	res, err := s.db.Exec(
		`UPDATE cal_bookings SET start_utc=?, end_utc=?
		 WHERE tenant_id=? AND calendar_id=? AND booking_id=?`,
		instantToNanos(to.Start), instantToNanos(to.End),
		s.tenantID, calendarID, bookingID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("cal: setSpan: unknown booking %q/%q", calendarID, bookingID)
	}
	return nil
}

// --- Reads ---

func (s *SQLiteBookingSource) calendar(calendarID string) (Calendar, bool) {
	var c Calendar
	var ord uint32
	var defState, matchPol string
	err := s.db.QueryRow(
		`SELECT calendar_id, ordinal, entity_ref, default_state, match_policy
		 FROM cal_calendars WHERE tenant_id=? AND calendar_id=?`,
		s.tenantID, calendarID).Scan(
		&c.CalendarID, &ord, &c.EntityRef, &defState, &matchPol)
	if err == sql.ErrNoRows {
		return Calendar{}, false
	}
	if err != nil {
		return Calendar{}, false
	}
	c.Ordinal = CalOrdinal(ord)
	c.DefaultState = State(defState)
	c.MatchPolicy = MatchConsiders(matchPol)
	return c, true
}

// calendarInTx is calendar()'s transactional twin — reads through the
// caller-supplied tx instead of s.db directly, for the same reason
// putBookingInTx exists at all: a read against s.db's own connection
// while tx is still open on the same underlying *sql.DB deadlocks
// SQLite. Found by actually running the dxp cal adapter's tests, not
// by reasoning about it — putBookingInTx's own validation was calling
// the non-tx calendar() internally until this was added.
func (s *SQLiteBookingSource) calendarInTx(ctx context.Context, tx *sql.Tx, calendarID string) (Calendar, bool) {
	var c Calendar
	var ord uint32
	var defState, matchPol string
	err := tx.QueryRowContext(ctx,
		`SELECT calendar_id, ordinal, entity_ref, default_state, match_policy
		 FROM cal_calendars WHERE tenant_id=? AND calendar_id=?`,
		s.tenantID, calendarID).Scan(
		&c.CalendarID, &ord, &c.EntityRef, &defState, &matchPol)
	if err != nil {
		return Calendar{}, false
	}
	c.Ordinal = CalOrdinal(ord)
	c.DefaultState = State(defState)
	c.MatchPolicy = MatchConsiders(matchPol)
	return c, true
}

// Calendar is the exported calendar accessor: returns the persisted calendar
// record for an id. Used by callers outside the package (and tests).
func (s *SQLiteBookingSource) Calendar(calendarID string) (Calendar, bool) {
	return s.calendar(calendarID)
}

func (s *SQLiteBookingSource) booking(calendarID, bookingID string) (Booking, bool) {
	row := s.db.QueryRow(
		`SELECT calendar_id, booking_id, state, start_utc, end_utc, mode, bearer,
		        buffer_after_utc, created_utc, updated_utc, detail_ref
		 FROM cal_bookings WHERE tenant_id=? AND calendar_id=? AND booking_id=?`,
		s.tenantID, calendarID, bookingID)
	b, err := scanBooking(row)
	if err != nil {
		return Booking{}, false
	}
	return b, true
}

// Booking is the exported booking accessor: returns the persisted record for a
// (calendar, booking) pair. Used by callers outside the package (and tests) that
// need to read a booking back from the authoritative store.
func (s *SQLiteBookingSource) Booking(calendarID, bookingID string) (Booking, bool) {
	return s.booking(calendarID, bookingID)
}

// Calendars implements BookingSource.
func (s *SQLiteBookingSource) Calendars() []Calendar {
	rows, err := s.db.Query(
		`SELECT calendar_id, ordinal, entity_ref, default_state, match_policy
		 FROM cal_calendars WHERE tenant_id=?`, s.tenantID)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []Calendar
	for rows.Next() {
		var c Calendar
		var ord uint32
		var defState, matchPol string
		if err := rows.Scan(&c.CalendarID, &ord, &c.EntityRef, &defState, &matchPol); err != nil {
			return out
		}
		c.Ordinal = CalOrdinal(ord)
		c.DefaultState = State(defState)
		c.MatchPolicy = MatchConsiders(matchPol)
		out = append(out, c)
	}
	return out
}

// LiveBookings implements BookingSource: every booking in a plane-occupying
// state. The state filter is pushed into SQL using the live-state set.
func (s *SQLiteBookingSource) LiveBookings() []Booking {
	rows, err := s.db.Query(
		`SELECT calendar_id, booking_id, state, start_utc, end_utc, mode, bearer,
		        buffer_after_utc, created_utc, updated_utc, detail_ref
		 FROM cal_bookings
		 WHERE tenant_id=? AND state IN (?, ?, ?)`,
		s.tenantID, string(StateProposed), string(StateBinding), string(StateHonoured))
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	return scanBookings(rows)
}

// LiveBookingsOn implements PlaneBookingSource: live bookings for one calendar on
// one plane. The plane maps to its state set; the (calendar_id, state) index
// serves this — the hot path for lifecycle/move/commit.
func (s *SQLiteBookingSource) LiveBookingsOn(calendarID string, plane Plane) []Booking {
	var states []State
	if plane == PlaneProposed {
		states = []State{StateProposed}
	} else {
		states = []State{StateBinding, StateHonoured}
	}
	// build the IN-list dynamically (1 or 2 states).
	q := `SELECT calendar_id, booking_id, state, start_utc, end_utc, mode, bearer,
	             buffer_after_utc, created_utc, updated_utc, detail_ref
	      FROM cal_bookings WHERE tenant_id=? AND calendar_id=? AND state IN (`
	args := []interface{}{s.tenantID, calendarID}
	for i, st := range states {
		if i > 0 {
			q += ", "
		}
		q += "?"
		args = append(args, string(st))
	}
	q += ")"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	return scanBookings(rows)
}

// --- Row scanning (the read side of the xolutime boundary) ---

type rowScanner interface {
	Scan(dest ...interface{}) error
}

// scanBooking reads one booking row, converting stored UnixNano back to UTC
// instants via the xolutime boundary.
func scanBooking(row rowScanner) (Booking, error) {
	var b Booking
	var state, mode, detailRef string
	var startN, endN, createdN, updatedN int64
	var bufferN sql.NullInt64
	if err := row.Scan(
		&b.CalendarID, &b.BookingID, &state, &startN, &endN, &mode, &b.Bearer,
		&bufferN, &createdN, &updatedN, &detailRef); err != nil {
		return Booking{}, err
	}
	b.State = State(state)
	b.Mode = Mode(mode)
	b.DetailRef = detailRef
	b.Span = Span{Start: nanosToInstant(startN), End: nanosToInstant(endN)}
	b.BufferAfter = nanosToNullableInstant(bufferN)
	b.CreatedAt = nanosToInstant(createdN)
	b.UpdatedAt = nanosToInstant(updatedN)
	return b, nil
}

func scanBookings(rows *sql.Rows) []Booking {
	var out []Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return out
		}
		out = append(out, b)
	}
	return out
}
