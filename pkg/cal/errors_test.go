// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Tests for the typed sentinel error taxonomy introduced in v0.14.8.
// These verify:
//
//   - Sentinels are non-nil and carry sensible identifying messages.
//   - Sentinels are pairwise distinct — errors.Is(a, b) is false when a
//     and b are different sentinels.
//   - Wrapping via %w preserves errors.Is dispatch, and the wrapping
//     message context is preserved verbatim for humans.
//   - A double-wrap (sentinel wrapped in one fmt.Errorf, then wrapped
//     again) still resolves via errors.Is.
//
// These are unit tests on pkg/cal alone; the pkg/server tests exercise
// the handler-layer dispatch that consumes this taxonomy.

func TestSentinelsAreNonNil(t *testing.T) {
	sentinels := map[string]error{
		"ErrUnknownCalendar":   ErrUnknownCalendar,
		"ErrUnknownBooking":    ErrUnknownBooking,
		"ErrIllegalTransition": ErrIllegalTransition,
		"ErrInvalidSpan":       ErrInvalidSpan,
		"ErrCalendarExists":    ErrCalendarExists,
		"ErrBearerRequired":    ErrBearerRequired,
	}
	for name, s := range sentinels {
		if s == nil {
			t.Errorf("%s is nil", name)
			continue
		}
		if !strings.HasPrefix(s.Error(), "cal:") {
			t.Errorf("%s message %q does not have expected 'cal:' prefix", name, s.Error())
		}
	}
}

func TestSentinelsPairwiseDistinct(t *testing.T) {
	// errors.Is against a different sentinel must return false.
	all := []error{
		ErrUnknownCalendar,
		ErrUnknownBooking,
		ErrIllegalTransition,
		ErrInvalidSpan,
		ErrCalendarExists,
		ErrBearerRequired,
	}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("errors.Is(%v, %v) returned true but sentinels are supposed to be distinct", a, b)
			}
		}
	}
}

func TestWrappingPreservesErrorsIs(t *testing.T) {
	// The pattern every pkg/cal call site uses: fmt.Errorf with %w for
	// the sentinel plus additional context. errors.Is must still
	// resolve the sentinel through this wrap.
	wrapped := fmt.Errorf("%w: %q", ErrUnknownCalendar, "cal-42")
	if !errors.Is(wrapped, ErrUnknownCalendar) {
		t.Error("errors.Is failed to unwrap %w-wrapped sentinel")
	}
	// And the sentinel we didn't wrap must NOT match.
	if errors.Is(wrapped, ErrUnknownBooking) {
		t.Error("errors.Is matched the wrong sentinel through a wrapped error")
	}
}

func TestDoubleWrappingPreservesErrorsIs(t *testing.T) {
	// A double-wrap: sentinel first, then re-wrapped with more context.
	// This models the pattern where a package prepends its own context.
	inner := fmt.Errorf("%w: %q", ErrUnknownBooking, "book-9")
	outer := fmt.Errorf("cal: Move: %w", inner)
	if !errors.Is(outer, ErrUnknownBooking) {
		t.Error("errors.Is failed through a double wrap")
	}
}

func TestWrappingPreservesMessageContext(t *testing.T) {
	// The wrapping message text must reach humans; %w should include the
	// sentinel's own message.
	wrapped := fmt.Errorf("%w: %q", ErrUnknownCalendar, "cal-p")
	msg := wrapped.Error()
	if !strings.Contains(msg, "cal:") {
		t.Errorf("wrapped message %q lost 'cal:' prefix", msg)
	}
	if !strings.Contains(msg, "cal-p") {
		t.Errorf("wrapped message %q lost the specific calendar id context", msg)
	}
}

func TestLifecycleCreateWrapsUnknownCalendar(t *testing.T) {
	// End-to-end proof: Lifecycle.Create's actual error path wraps the
	// sentinel. Uses the same setupLifecycle scaffolding as the rest of
	// pkg/cal, then attempts to create a booking against a calendar that
	// wasn't seeded.
	lc, _, _ := setupLifecycle(t, "seeded-cal")

	_, err := lc.Create(Booking{
		BookingID:  "b1",
		CalendarID: "nonexistent",
		State:      StateProposed,
	})
	if err == nil {
		t.Fatal("expected error for booking against nonexistent calendar")
	}
	if !errors.Is(err, ErrUnknownCalendar) {
		t.Errorf("expected error to wrap ErrUnknownCalendar, got %v", err)
	}
}

func TestLifecycleConfirmWrapsUnknownBooking(t *testing.T) {
	lc, _, _ := setupLifecycle(t, "cal1")

	// Confirm a booking that doesn't exist.
	err := lc.Confirm("cal1", "nonexistent-booking")
	if err == nil {
		t.Fatal("expected error for confirm of nonexistent booking")
	}
	if !errors.Is(err, ErrUnknownBooking) {
		t.Errorf("expected error to wrap ErrUnknownBooking, got %v", err)
	}
}

func TestLifecycleConfirmWrapsIllegalTransition(t *testing.T) {
	// A double-confirm attempts binding -> binding, which
	// allowedTransition rejects. This is the same programmatic-dispatch
	// path molu Part 2 §6 needs.
	lc, _, _ := setupLifecycle(t, "cal1")

	// A proposed booking with a valid span (Start strictly before End).
	start := ot.FromUnixNano(1_700_000_000_000_000_000)
	end := ot.FromUnixNano(1_700_000_003_600_000_000)
	_, err := lc.Create(Booking{
		BookingID:  "b1",
		CalendarID: "cal1",
		State:      StateProposed,
		Span:       Span{Start: start, End: end},
		Bearer:     1,
	})
	if err != nil {
		t.Fatalf("seed booking: %v", err)
	}
	if err := lc.Confirm("cal1", "b1"); err != nil {
		t.Fatalf("first confirm should succeed: %v", err)
	}
	// Second confirm — now binding -> binding, illegal.
	err = lc.Confirm("cal1", "b1")
	if err == nil {
		t.Fatal("expected error on second confirm")
	}
	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("expected error to wrap ErrIllegalTransition, got %v", err)
	}
}

// TestPutBookingRejectsNonExclusiveMode: with ModeShared and
// ModeSubPrefix removed from the type surface in v0.14.12, the source
// layer must reject any non-exclusive mode string. This guards against
// a future accidental reintroduction, or against a caller sending a
// mode string not present in the type declarations.
func TestPutBookingRejectsNonExclusiveMode(t *testing.T) {
	lc, _, src := setupLifecycle(t, "cal1")
	_ = lc

	start := ot.FromUnixNano(1_700_000_000_000_000_000)
	end := ot.FromUnixNano(1_700_000_003_600_000_000)

	for _, badMode := range []string{"shared", "sub:room-a", "arbitrary", "SUB:X"} {
		err := src.PutBooking(Booking{
			BookingID:  "b-" + badMode,
			CalendarID: "cal1",
			State:      StateProposed,
			Span:       Span{Start: start, End: end},
			Mode:       Mode(badMode),
			Bearer:     1,
		})
		if err == nil {
			t.Errorf("mode=%q: expected rejection, got nil", badMode)
			continue
		}
		if !errors.Is(err, ErrModeNotSupported) {
			t.Errorf("mode=%q: expected error to wrap ErrModeNotSupported, got %v", badMode, err)
		}
	}
}

// TestPutBookingCoercesEmptyModeToExclusive: callers that omit Mode
// entirely get ModeExclusive by default. This preserves compatibility
// with any code path that constructs a Booking without setting Mode
// explicitly and would otherwise fail the mode check.
func TestPutBookingCoercesEmptyModeToExclusive(t *testing.T) {
	lc, _, src := setupLifecycle(t, "cal1")
	_ = lc

	start := ot.FromUnixNano(1_700_000_000_000_000_000)
	end := ot.FromUnixNano(1_700_000_003_600_000_000)

	err := src.PutBooking(Booking{
		BookingID:  "b-empty-mode",
		CalendarID: "cal1",
		State:      StateProposed,
		Span:       Span{Start: start, End: end},
		Mode:       "", // deliberately empty
		Bearer:     1,
	})
	if err != nil {
		t.Fatalf("empty mode should be coerced to exclusive, got: %v", err)
	}

	// Confirm the stored booking has ModeExclusive.
	b, ok := src.booking("cal1", "b-empty-mode")
	if !ok {
		t.Fatal("booking not stored")
	}
	if b.Mode != ModeExclusive {
		t.Errorf("expected Mode=%q after coercion, got %q", ModeExclusive, b.Mode)
	}
}
