// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// types_cal.go — Stage 5 (plan M3): wire types for the /api/v2/cal/*
// surface. Shapes mirror pkg/server/v2_cal_handlers.go byte-for-byte per
// the Stage 2 convention. Time instants are RFC 3339 with an explicit
// zone offset on the wire (xolutime.Instant server-side); time.Time
// marshals compatibly and always carries an offset, so client-emitted
// timestamps are always accepted.

import "time"

// Objective selects how CalOpenings ranks candidate windows. The four
// values are the complete set the server implements; the zero value ""
// lets the server default to ObjectiveEarliest.
type Objective string

const (
	ObjectiveEarliest   Objective = "earliest"
	ObjectiveFirstFit   Objective = "first-fit"
	ObjectiveEmptiest   Objective = "emptiest"
	ObjectiveLongestClr Objective = "longest-clear-margin"
)

// validObjectives is the client-side gate: catching a typo here saves a
// round trip and mirrors the server's own validation exactly.
var validObjectives = map[Objective]bool{
	"":                  true, // server defaults to earliest
	ObjectiveEarliest:   true,
	ObjectiveFirstFit:   true,
	ObjectiveEmptiest:   true,
	ObjectiveLongestClr: true,
}

// CalSpan is a half-open time span; Start must be strictly before End.
type CalSpan struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// CalCheckResult is the response of CalCheck: whether the span is
// bookable, and if not, the nearest alternative openings.
type CalCheckResult struct {
	Feasible        bool      `json:"feasible"`
	NearestOpenings []CalSpan `json:"nearest_openings"`
}

// CalOpening is one candidate window returned by CalOpenings, carrying
// the clear margin around it in milliseconds.
type CalOpening struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	MarginMs int64     `json:"margin_ms"`
}

// CalOpeningsResult is the response of CalOpenings; Objective echoes the
// objective actually applied (the server substitutes "earliest" when the
// request omitted one).
type CalOpeningsResult struct {
	Objective Objective    `json:"objective"`
	Openings  []CalOpening `json:"openings"`
}

// CalProposeRequest creates a booking in the proposed state. BookingID is
// client-generated identity (e.g. a ULID) and required; Mode defaults to
// "exclusive", the only mode the occupancy engine honours.
type CalProposeRequest struct {
	BookingID   string     `json:"booking_id"`
	CalendarID  string     `json:"calendar_id"`
	Span        CalSpan    `json:"span"`
	Mode        string     `json:"mode,omitempty"`
	Bearer      uint64     `json:"bearer,omitempty"`
	BufferAfter *time.Time `json:"buffer_after,omitempty"`
	DetailRef   string     `json:"detail_ref,omitempty"`
}

// CalBooking is a booking record as the server returns it from
// CalPropose and CalConfirm.
type CalBooking struct {
	BookingID   string    `json:"booking_id"`
	CalendarID  string    `json:"calendar_id"`
	State       string    `json:"state"`
	Span        CalSpan   `json:"span"`
	Mode        string    `json:"mode"`
	Bearer      uint64    `json:"bearer"`
	BufferAfter time.Time `json:"buffer_after,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	DetailRef   string    `json:"detail_ref,omitempty"`
}
