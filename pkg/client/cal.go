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
