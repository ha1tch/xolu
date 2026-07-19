// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import "errors"

// Sentinel errors returned by the cal subsystem, enabling callers (notably
// the HTTP handler layer in pkg/server) to classify failures via errors.Is
// without inspecting error message strings.
//
// Each sentinel is wrapped by errors that fmt.Errorf produces at the call
// site, so the wrapping message retains context (which calendar, which
// booking) while the sentinel identifies the failure kind.
//
// This taxonomy was introduced in v0.14.8 as part of the T-18 follow-up
// work; before it, the HTTP handlers surfaced all cal errors as
// XOLU-CAL006, which was too coarse for programmatic dispatch.

// ErrUnknownCalendar reports that a request references a calendar_id
// that does not exist in the current tenant scope.
var ErrUnknownCalendar = errors.New("cal: unknown calendar")

// ErrUnknownBooking reports that a request references a booking_id
// that does not exist on the named calendar.
var ErrUnknownBooking = errors.New("cal: unknown booking")

// ErrIllegalTransition reports that a lifecycle transition (confirm,
// decline, complete, cancel, mark-missed) is not permitted from the
// booking's current state per the A9 rules.
var ErrIllegalTransition = errors.New("cal: illegal lifecycle transition")

// ErrInvalidSpan reports that a span carries Start >= End, or that either
// instant is zero.
var ErrInvalidSpan = errors.New("cal: invalid span")

// ErrCalendarExists reports that CreateCalendar was called with a
// calendar_id already present in the tenant.
var ErrCalendarExists = errors.New("cal: calendar already exists")

// ErrBearerRequired reports that a binding booking was requested without
// a live bearer entity handle.
var ErrBearerRequired = errors.New("cal: binding booking requires a live bearer")

// ErrModeNotSupported reports that a booking was submitted with a mode
// outside the exclusive-only vocabulary. Introduced in v0.14.12 when
// ModeShared and ModeSubPrefix were removed from the type surface (see
// Mode godoc in pkg/cal/booking.go for the rationale).
var ErrModeNotSupported = errors.New("cal: mode not supported (only exclusive)")
