// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// cal.go — Stage 5 (plan M3): the four cal methods against the
// /api/v2/cal/* surface shipped in T-18 (v0.14.7). All four endpoints
// are POST server-side (structured inputs); errors arrive as the
// XOLU-CAL001–007 family through the structured *Error type. The cal
// subsystem is opt-in (XOLU_CAL_ENABLED); against a server with it
// disabled every method returns *Error with code XOLU-CAL001 and HTTP
// status 501.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// CalCheck asks whether span is bookable on the calendar right now,
// without creating anything. On infeasibility the result carries the
// nearest alternative openings.
//
// Hits POST /api/v2/.../cal/check. Returns *client.Error on non-2xx.
func (c *Client) CalCheck(ctx context.Context, calendarID string, span CalSpan) (*CalCheckResult, error) {
	if calendarID == "" {
		return nil, fmt.Errorf("calendarID is required")
	}
	if !span.Start.Before(span.End) {
		return nil, fmt.Errorf("span: start must be strictly before end")
	}
	body := map[string]interface{}{
		"calendar_id": calendarID,
		"span":        span,
	}
	var res CalCheckResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/cal/check"), body, &res); err != nil {
		return nil, err
	}
	if res.NearestOpenings == nil {
		res.NearestOpenings = []CalSpan{}
	}
	return &res, nil
}

// CalOpenings searches [from, to) for windows admitting duration, ranked
// by objective. The zero Objective lets the server default to
// ObjectiveEarliest; any other value is validated client-side against
// the four implemented objectives before the request is sent.
//
// Hits POST /api/v2/.../cal/openings. Returns *client.Error on non-2xx.
func (c *Client) CalOpenings(ctx context.Context, calendarID string, from, to time.Time, duration time.Duration, objective Objective) (*CalOpeningsResult, error) {
	if calendarID == "" {
		return nil, fmt.Errorf("calendarID is required")
	}
	if !from.Before(to) {
		return nil, fmt.Errorf("from must be strictly before to")
	}
	if duration <= 0 {
		return nil, fmt.Errorf("duration must be positive")
	}
	if !validObjectives[objective] {
		return nil, fmt.Errorf("objective must be one of: %s, %s, %s, %s (or empty for the server default)",
			ObjectiveEarliest, ObjectiveFirstFit, ObjectiveEmptiest, ObjectiveLongestClr)
	}
	body := map[string]interface{}{
		"calendar_id": calendarID,
		"from":        from,
		"to":          to,
		"duration_ms": duration.Milliseconds(),
		"objective":   string(objective),
	}
	var res CalOpeningsResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/cal/openings"), body, &res); err != nil {
		return nil, err
	}
	if res.Openings == nil {
		res.Openings = []CalOpening{}
	}
	return &res, nil
}

// CalCreateCalendar creates a new calendar on the tenant -- XM-8,
// xoluman's own report: no route anywhere created a calendar at all
// before this, despite the underlying capability
// (Manager.CreateCalendar) already existing and working correctly.
// The actual root blocker behind their own XM-2 report:
// CalListCalendars/CalListBookings both worked correctly, but had
// nothing to list, since nothing could be created through the public
// API to list in the first place.
//
// Hits POST /api/v2/.../cal/calendars. Returns *client.Error on
// non-2xx — notably XOLU-CAL008 (ErrCalCalendarExists) if
// req.CalendarID is already taken.
func (c *Client) CalCreateCalendar(ctx context.Context, req CalCreateCalendarRequest) (*CalendarSummary, error) {
	if req.CalendarID == "" {
		return nil, fmt.Errorf("CalendarID is required")
	}
	var res CalendarSummary
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/cal/calendars"), req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// CalListCalendars returns every calendar defined on the tenant.
// Confirmed missing during the XOT180 audit (2026-08-11) -- the
// underlying storage capability already existed but was never
// reachable via HTTP; any UI building an occupancy grid needs to know
// which calendars exist before it can ask what's booked on any one.
//
// Hits GET /api/v2/.../cal/calendars. Returns *client.Error on non-2xx.
func (c *Client) CalListCalendars(ctx context.Context) (*CalListCalendarsResult, error) {
	var res CalListCalendarsResult
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2("/cal/calendars"), nil, &res); err != nil {
		return nil, err
	}
	if res.Calendars == nil {
		res.Calendars = []CalendarSummary{}
	}
	return &res, nil
}

// CalListBookings returns every live (proposed, binding, or honoured)
// booking on calendarID whose own span overlaps [from, to) -- the
// inverse of CalOpenings: what's already booked, not what's free.
// Requested by xoluman (XM-2) for an occupancy grid, where
// CalOpenings alone can't show what's actually on the calendar.
//
// Hits GET /api/v2/.../cal/bookings. Returns *client.Error on non-2xx
// — notably XOLU-CAL002 (ErrCalCalendarNotFound) for an unknown
// calendar.
func (c *Client) CalListBookings(ctx context.Context, calendarID string, from, to time.Time) (*CalListBookingsResult, error) {
	if calendarID == "" {
		return nil, fmt.Errorf("calendarID is required")
	}
	if !from.Before(to) {
		return nil, fmt.Errorf("from must be strictly before to")
	}
	q := url.Values{}
	q.Set("calendar_id", calendarID)
	q.Set("from", from.UTC().Format(time.RFC3339Nano))
	q.Set("to", to.UTC().Format(time.RFC3339Nano))
	u := c.buildURLv2("/cal/bookings") + "?" + q.Encode()
	var res CalListBookingsResult
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	if res.Bookings == nil {
		res.Bookings = []CalBooking{}
	}
	return &res, nil
}

// CalListBookingsForBearer returns every live (proposed, binding, or
// honoured) booking held by bearer across every calendar on the
// tenant -- the cross-calendar complement to CalListBookings, which
// is scoped to one calendar at a time. Requested by xoluman (XM-2)
// as an example of a gap the per-calendar shape leaves open: "what
// bookings does bearer X hold across every calendar."
//
// Hits GET /api/v2/.../cal/bookings/by-bearer. Returns *client.Error
// on non-2xx.
func (c *Client) CalListBookingsForBearer(ctx context.Context, bearer uint64) (*CalListBookingsResult, error) {
	if bearer == 0 {
		return nil, fmt.Errorf("bearer must be non-zero")
	}
	q := url.Values{}
	q.Set("bearer", strconv.FormatUint(bearer, 10))
	u := c.buildURLv2("/cal/bookings/by-bearer") + "?" + q.Encode()
	var res CalListBookingsResult
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	if res.Bookings == nil {
		res.Bookings = []CalBooking{}
	}
	return &res, nil
}

// CalPropose creates a booking in the proposed state. req.BookingID is
// client-generated identity (e.g. a ULID) and required; the returned
// booking carries the server-assigned fields (state, timestamps).
//
// Hits POST /api/v2/.../cal/propose. Returns *client.Error on non-2xx —
// notably XOLU-CAL004 when the span conflicts with existing occupancy.
func (c *Client) CalPropose(ctx context.Context, req CalProposeRequest) (*CalBooking, error) {
	if req.CalendarID == "" {
		return nil, fmt.Errorf("CalendarID is required")
	}
	if req.BookingID == "" {
		return nil, fmt.Errorf("BookingID is required (client-generated identity, e.g. a ULID)")
	}
	if !req.Span.Start.Before(req.Span.End) {
		return nil, fmt.Errorf("span: start must be strictly before end")
	}
	var b CalBooking
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/cal/propose"), req, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// CalConfirm transitions a proposed booking to binding and returns the
// updated record.
//
// Hits POST /api/v2/.../cal/confirm. Returns *client.Error on non-2xx —
// notably XOLU-CAL003 for an illegal state transition and XOLU-CAL005
// for an unknown booking.
func (c *Client) CalConfirm(ctx context.Context, calendarID, bookingID string) (*CalBooking, error) {
	if calendarID == "" {
		return nil, fmt.Errorf("calendarID is required")
	}
	if bookingID == "" {
		return nil, fmt.Errorf("bookingID is required")
	}
	body := map[string]interface{}{
		"calendar_id": calendarID,
		"booking_id":  bookingID,
	}
	var b CalBooking
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/cal/confirm"), body, &b); err != nil {
		return nil, err
	}
	return &b, nil
}
