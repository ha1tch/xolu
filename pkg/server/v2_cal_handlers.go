// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// T-18: cal subsystem HTTP surface.
//
// This file exposes xolu's cal subsystem via four /api/v2/cal/* endpoints,
// the minimum surface documented in the T-18 debt entry:
//
//   POST /api/v2/cal/check     — feasibility dry-run for a booking
//   POST /api/v2/cal/openings  — search for windows admitting a duration
//   POST /api/v2/cal/propose   — create a proposed booking
//   POST /api/v2/cal/confirm   — transition proposed -> binding
//
// All four are POST because their inputs are structured (spans, objectives,
// booking overrides) — the same choice xolu made for /fsm/def/validate and
// other analysis endpoints. All four are tenant-scoped via the standard v2
// routing. All four return XOLU-CAL* error codes on the taxonomy defined in
// pkg/errors/errors.go.
//
// Wire format notes:
//   - Time instants use xolutime.Instant's JSON marshalling (RFC 3339 with
//     zone). Callers must include a timezone offset in their timestamps.
//   - Spans are {"start": <instant>, "end": <instant>} with Start strictly
//     before End (cal.Span.Valid()).
//   - Objectives are one of the four cal.Objective values: "earliest",
//     "first-fit", "emptiest", "longest-clear-margin".
//   - Mode is "exclusive" — the only value the occupancy engine honours
//     since the T-30 mode reduction (v0.14.12); anything else is rejected
//     with XOLU-CAL007.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ha1tch/xolu/pkg/cal"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// ─── Wire-format types ─────────────────────────────────────────────────────

// spanWire is the JSON representation of a cal.Span. Kept separate from
// cal.Span so the handler layer is free to add fields later without
// disturbing the internal type.
type spanWire struct {
	Start ot.Instant `json:"start"`
	End   ot.Instant `json:"end"`
}

func (s spanWire) toCal() cal.Span {
	return cal.Span{Start: s.Start, End: s.End}
}

func spanFromCal(s cal.Span) spanWire {
	return spanWire{Start: s.Start, End: s.End}
}

// bookingWire is a booking as it appears on the wire. Times use ot.Instant's
// JSON marshalling. Fields optional at request time are echoed on response
// with their server-assigned values.
type bookingWire struct {
	BookingID   string     `json:"booking_id,omitempty"`
	CalendarID  string     `json:"calendar_id"`
	State       string     `json:"state,omitempty"`
	Span        spanWire   `json:"span"`
	Mode        string     `json:"mode"`
	Bearer      uint64     `json:"bearer"`
	BufferAfter ot.Instant `json:"buffer_after,omitempty"`
	CreatedAt   ot.Instant `json:"created_at,omitempty"`
	UpdatedAt   ot.Instant `json:"updated_at,omitempty"`
	DetailRef   string     `json:"detail_ref,omitempty"`
}

func bookingFromCal(b cal.Booking) bookingWire {
	return bookingWire{
		BookingID:   b.BookingID,
		CalendarID:  b.CalendarID,
		State:       string(b.State),
		Span:        spanFromCal(b.Span),
		Mode:        string(b.Mode),
		Bearer:      b.Bearer,
		BufferAfter: b.BufferAfter,
		CreatedAt:   b.CreatedAt,
		UpdatedAt:   b.UpdatedAt,
		DetailRef:   b.DetailRef,
	}
}

// ─── Route setup ───────────────────────────────────────────────────────────

// setupV2CalRoutes registers the /cal/* routes on a router that is already
// scoped to a tenant. Called from setupV2TenantRoutes when CalEnabled=true.
func (s *Server) setupV2CalRoutes(r chi.Router) {
	r.Post("/cal/check", s.handleCalCheck)
	r.Post("/cal/openings", s.handleCalOpenings)
	r.Post("/cal/propose", s.handleCalPropose)
	r.Post("/cal/confirm", s.handleCalConfirm)
	r.Get("/cal/bookings", s.handleCalListBookings)
	r.Get("/cal/bookings/by-bearer", s.handleCalListBookingsForBearer)
	r.Post("/cal/calendars", s.handleCalCreateCalendar)
	r.Get("/cal/calendars", s.handleCalListCalendars)
}

// classifyCalError maps a cal-layer error to an HTTP status and an xolu
// error code, honouring the sentinel taxonomy in pkg/cal/errors.go. The
// booleans (matched) let the caller distinguish "recognised failure kind"
// from "unclassified, fall back to XOLU-CAL006".
//
// The dispatch is ordered by specificity: unknown-calendar and unknown-
// booking come before the more generic invalid-span and illegal-transition
// so that a wrapped chain is classified by its most specific sentinel.
func classifyCalError(err error) (status int, code xoluerr.Code, matched bool) {
	switch {
	case errors.Is(err, cal.ErrUnknownCalendar):
		return http.StatusNotFound, xoluerr.ErrCalCalendarNotFound, true
	case errors.Is(err, cal.ErrUnknownBooking):
		return http.StatusNotFound, xoluerr.ErrCalBookingNotFound, true
	case errors.Is(err, cal.ErrIllegalTransition):
		return http.StatusUnprocessableEntity, xoluerr.ErrCalTransitionRejected, true
	case errors.Is(err, cal.ErrInvalidSpan):
		return http.StatusBadRequest, xoluerr.ErrCalInvalidSpan, true
	case errors.Is(err, cal.ErrBearerRequired):
		return http.StatusUnprocessableEntity, xoluerr.ErrCalTransitionRejected, true
	case errors.Is(err, cal.ErrModeNotSupported):
		return http.StatusBadRequest, xoluerr.ErrCalModeNotSupported, true
	case errors.Is(err, cal.ErrCalendarExists):
		return http.StatusConflict, xoluerr.ErrCalCalendarExists, true
	}
	return 0, "", false
}

// calGuard verifies the cal subsystem is enabled and returns the tenant's
// Lifecycle. Writes a structured error and returns nil if the subsystem is
// off or a per-tenant Lifecycle cannot be obtained.
func (s *Server) calGuard(w http.ResponseWriter, r *http.Request) *cal.Lifecycle {
	if s.calMgr == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrCalDisabled,
			"cal subsystem is not enabled on this server (XOLU_CAL_ENABLED=false)")
		return nil
	}
	tenantID := getTenantIDNumeric(r.Context())
	lc, err := s.calMgr.CalFor(tenantID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"failed to obtain calendar lifecycle: "+err.Error())
		return nil
	}
	return lc
}

// ─── POST /cal/check ───────────────────────────────────────────────────────

func (s *Server) handleCalCheck(w http.ResponseWriter, r *http.Request) {
	lc := s.calGuard(w, r)
	if lc == nil {
		return
	}

	var req struct {
		CalendarID string   `json:"calendar_id"`
		Span       spanWire `json:"span"`
		Mode       string   `json:"mode"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.CalendarID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalCalendarNotFound,
			"calendar_id is required")
		return
	}
	span := req.Span.toCal()
	if !span.Valid() {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"span: start must be strictly before end")
		return
	}
	if req.Mode == "" {
		req.Mode = string(cal.ModeExclusive)
	}

	tenantID := getTenantIDNumeric(r.Context())
	src := s.calMgr.SourceFor(tenantID)
	c, ok := src.Calendar(req.CalendarID)
	if !ok {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrCalCalendarNotFound,
			"calendar not found: "+req.CalendarID)
		return
	}

	idx := s.calMgr.IndexFor(tenantID)
	res, err := idx.Check(c, span, cal.Mode(req.Mode))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// Convert nearest-openings spans to wire format.
	openings := make([]spanWire, 0, len(res.NearestOpenings))
	for _, sp := range res.NearestOpenings {
		openings = append(openings, spanFromCal(sp))
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"feasible":         res.Feasible,
		"nearest_openings": openings,
	})
}

// handleCalCreateCalendar creates a new calendar on the tenant --
// XM-8, xoluman's own report: no route anywhere in pkg/server created
// a calendar at all, despite Manager.CreateCalendar already existing
// and working correctly (confirmed directly -- this session's own
// integration tests use it constantly, via a test-only facade never
// exposed on the wire). This was the actual root blocker behind
// XM-2's own cal piece: CalListCalendars/CalListBookings both work
// correctly, but had nothing to list, since nothing could be created
// through the public API to list in the first place.
//
// CalendarID is required; DefaultState/MatchPolicy default sensibly
// (StateBinding/ConsiderBinding) when omitted, matching
// SQLiteBookingSource.CreateCalendar's own established defaults.
//
//	POST /api/v2/.../cal/calendars
func (s *Server) handleCalCreateCalendar(w http.ResponseWriter, r *http.Request) {
	lc := s.calGuard(w, r)
	if lc == nil {
		return
	}

	var req struct {
		CalendarID   string `json:"calendar_id"`
		EntityRef    uint64 `json:"entity_ref,omitempty"`
		DefaultState string `json:"default_state,omitempty"`
		MatchPolicy  string `json:"match_policy,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"invalid request body: "+err.Error())
		return
	}
	if req.CalendarID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"calendar_id is required")
		return
	}

	tenantID := getTenantIDNumeric(r.Context())
	created, err := s.calMgr.CreateCalendar(tenantID, cal.Calendar{
		CalendarID:   req.CalendarID,
		EntityRef:    req.EntityRef,
		DefaultState: cal.State(req.DefaultState),
		MatchPolicy:  cal.MatchConsiders(req.MatchPolicy),
	})
	if err != nil {
		if status, code, matched := classifyCalError(err); matched {
			s.writeError(w, status, code, err.Error())
			return
		}
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"calendar_id":   created.CalendarID,
		"entity_ref":    created.EntityRef,
		"default_state": string(created.DefaultState),
		"match_policy":  string(created.MatchPolicy),
	})
}

// handleCalListCalendars returns every calendar defined on the tenant
// -- confirmed missing during the XOT180 audit (2026-08-11), not
// hypothetical: xoluman's own original XM-2 report flagged, as a
// secondary and explicitly unconfirmed observation, that no
// calendarID provisioning step was visible from the client side.
// Checked directly here: the storage layer's own Calendars() method
// already existed and was already tenant-scoped, but was never called
// from anywhere in pkg/server -- genuinely unreachable, not merely
// undocumented. Any UI building an occupancy grid needs to know which
// calendars exist before it can ask what's booked on any one of them.
//
//	GET /api/v2/.../cal/calendars
func (s *Server) handleCalListCalendars(w http.ResponseWriter, r *http.Request) {
	lc := s.calGuard(w, r)
	if lc == nil {
		return
	}
	tenantID := getTenantIDNumeric(r.Context())
	src := s.calMgr.SourceFor(tenantID)

	cals := src.Calendars()
	type calendarWire struct {
		CalendarID   string `json:"calendar_id"`
		EntityRef    uint64 `json:"entity_ref"`
		DefaultState string `json:"default_state"`
		MatchPolicy  string `json:"match_policy"`
	}
	out := make([]calendarWire, 0, len(cals))
	for _, c := range cals {
		out = append(out, calendarWire{
			CalendarID:   c.CalendarID,
			EntityRef:    c.EntityRef,
			DefaultState: string(c.DefaultState),
			MatchPolicy:  string(c.MatchPolicy),
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"calendars": out})
}

// handleCalListBookingsForBearer returns every live (proposed,
// binding, or honoured) booking held by bearer across every calendar
// on the tenant -- the cross-calendar query named explicitly in
// XOT180's own filing ("what bookings does bearer X hold across
// every calendar") as an example of the gap CalListBookings's own
// per-calendar shape leaves open.
//
//	GET /api/v2/.../cal/bookings/by-bearer?bearer={uint64}
func (s *Server) handleCalListBookingsForBearer(w http.ResponseWriter, r *http.Request) {
	lc := s.calGuard(w, r)
	if lc == nil {
		return
	}

	bearerStr := r.URL.Query().Get("bearer")
	if bearerStr == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"bearer is required")
		return
	}
	bearer, err := strconv.ParseUint(bearerStr, 10, 64)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"invalid bearer: "+err.Error())
		return
	}

	tenantID := getTenantIDNumeric(r.Context())
	src := s.calMgr.SourceFor(tenantID)

	bookings := src.BookingsForBearer(bearer)
	out := make([]bookingWire, 0, len(bookings))
	for _, b := range bookings {
		out = append(out, bookingFromCal(b))
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"bookings": out})
}

// ─── GET /cal/bookings ──────────────────────────────────────────────────────

// handleCalListBookings returns every live booking (proposed, binding,
// or honoured) on a calendar whose own span overlaps the requested
// [from, to) window -- xoluman's own XM-2 report: CalOpenings returns
// free slots, the inverse of what an occupancy grid needs; this is
// the missing "what's already booked" read. GET, matching xoluman's
// own proposed shape (a read-only query, unlike the POST-only
// surface every other cal handler uses).
//
//	GET /api/v2/.../cal/bookings?calendar_id={id}&from={RFC3339}&to={RFC3339}
func (s *Server) handleCalListBookings(w http.ResponseWriter, r *http.Request) {
	lc := s.calGuard(w, r)
	if lc == nil {
		return
	}

	q := r.URL.Query()
	calendarID := q.Get("calendar_id")
	if calendarID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalCalendarNotFound,
			"calendar_id is required")
		return
	}
	from, err := ot.Parse(q.Get("from"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"invalid from: "+err.Error())
		return
	}
	to, err := ot.Parse(q.Get("to"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"invalid to: "+err.Error())
		return
	}
	if !from.Before(to) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"from must be strictly before to")
		return
	}

	tenantID := getTenantIDNumeric(r.Context())
	src := s.calMgr.SourceFor(tenantID)
	if _, ok := src.Calendar(calendarID); !ok {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrCalCalendarNotFound,
			"calendar not found: "+calendarID)
		return
	}

	bookings := src.BookingsInRange(calendarID, from, to)
	out := make([]bookingWire, 0, len(bookings))
	for _, b := range bookings {
		out = append(out, bookingFromCal(b))
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"bookings": out})
}

// ─── POST /cal/openings ────────────────────────────────────────────────────

func (s *Server) handleCalOpenings(w http.ResponseWriter, r *http.Request) {
	lc := s.calGuard(w, r)
	if lc == nil {
		return
	}

	var req struct {
		CalendarID string     `json:"calendar_id"`
		From       ot.Instant `json:"from"`
		To         ot.Instant `json:"to"`
		DurationMs int64      `json:"duration_ms"`
		Objective  string     `json:"objective"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.CalendarID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalCalendarNotFound,
			"calendar_id is required")
		return
	}
	if !req.From.Before(req.To) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"from must be strictly before to")
		return
	}
	if req.DurationMs <= 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"duration_ms must be positive")
		return
	}
	obj := cal.Objective(req.Objective)
	if req.Objective == "" {
		obj = cal.ObjEarliest
	}
	switch obj {
	case cal.ObjEarliest, cal.ObjFirstFit, cal.ObjEmptiest, cal.ObjLongestClr:
		// ok
	default:
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidObjective,
			"objective must be one of: earliest, first-fit, emptiest, longest-clear-margin")
		return
	}

	tenantID := getTenantIDNumeric(r.Context())
	src := s.calMgr.SourceFor(tenantID)
	if _, ok := src.Calendar(req.CalendarID); !ok {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrCalCalendarNotFound,
			"calendar not found: "+req.CalendarID)
		return
	}

	idx := s.calMgr.IndexFor(tenantID)
	occ, err := idx.ReadOccupancy(req.CalendarID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	openings, err := occ.Openings(req.From, req.To, time.Duration(req.DurationMs)*time.Millisecond, obj)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	type openingWire struct {
		Start  ot.Instant `json:"start"`
		End    ot.Instant `json:"end"`
		Margin int64      `json:"margin_ms"`
	}
	out := make([]openingWire, 0, len(openings))
	for _, o := range openings {
		out = append(out, openingWire{
			Start:  o.Start,
			End:    o.End,
			Margin: o.Margin.Milliseconds(),
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"objective": string(obj),
		"openings":  out,
	})
}

// ─── POST /cal/propose ─────────────────────────────────────────────────────

func (s *Server) handleCalPropose(w http.ResponseWriter, r *http.Request) {
	lc := s.calGuard(w, r)
	if lc == nil {
		return
	}

	var req struct {
		BookingID   string     `json:"booking_id"`
		CalendarID  string     `json:"calendar_id"`
		Span        spanWire   `json:"span"`
		Mode        string     `json:"mode"`
		Bearer      uint64     `json:"bearer"`
		BufferAfter ot.Instant `json:"buffer_after,omitempty"`
		DetailRef   string     `json:"detail_ref,omitempty"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.CalendarID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalCalendarNotFound,
			"calendar_id is required")
		return
	}
	if req.BookingID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalBookingNotFound,
			"booking_id is required (client-generated identity, e.g. a ULID)")
		return
	}
	span := req.Span.toCal()
	if !span.Valid() {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalInvalidSpan,
			"span: start must be strictly before end")
		return
	}
	mode := cal.Mode(req.Mode)
	if req.Mode == "" {
		mode = cal.ModeExclusive
	}

	// Verify the calendar exists in the tenant before hitting Lifecycle.Create.
	tenantID := getTenantIDNumeric(r.Context())
	src := s.calMgr.SourceFor(tenantID)
	if _, ok := src.Calendar(req.CalendarID); !ok {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrCalCalendarNotFound,
			"calendar not found: "+req.CalendarID)
		return
	}

	b := cal.Booking{
		BookingID:   req.BookingID,
		CalendarID:  req.CalendarID,
		State:       cal.StateProposed, // T-18: propose always creates in proposed state
		Span:        span,
		Mode:        mode,
		Bearer:      req.Bearer,
		BufferAfter: req.BufferAfter,
		DetailRef:   req.DetailRef,
	}
	created, err := lc.Create(b)
	if err != nil {
		if status, code, ok := classifyCalError(err); ok {
			s.writeError(w, status, code, err.Error())
			return
		}
		// Unclassified: something outside the sentinel taxonomy. Treat as
		// storage-layer failure rather than lifecycle validation, since
		// the sentinel set covers every documented lifecycle failure.
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			err.Error())
		return
	}
	s.writeJSON(w, http.StatusCreated, bookingFromCal(created))
}

// ─── POST /cal/confirm ─────────────────────────────────────────────────────

func (s *Server) handleCalConfirm(w http.ResponseWriter, r *http.Request) {
	lc := s.calGuard(w, r)
	if lc == nil {
		return
	}

	var req struct {
		CalendarID string `json:"calendar_id"`
		BookingID  string `json:"booking_id"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.CalendarID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalCalendarNotFound,
			"calendar_id is required")
		return
	}
	if req.BookingID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCalBookingNotFound,
			"booking_id is required")
		return
	}

	if err := lc.Confirm(req.CalendarID, req.BookingID); err != nil {
		if status, code, ok := classifyCalError(err); ok {
			s.writeError(w, status, code, err.Error())
			return
		}
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			err.Error())
		return
	}

	// Re-read the booking to return the updated record.
	tenantID := getTenantIDNumeric(r.Context())
	src := s.calMgr.SourceFor(tenantID)
	b, ok := src.Booking(req.CalendarID, req.BookingID)
	if !ok {
		// Race: the confirm succeeded but the booking vanished. Extremely
		// unusual; surface as internal error.
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"booking disappeared after successful confirm")
		return
	}
	s.writeJSON(w, http.StatusOK, bookingFromCal(b))
}
