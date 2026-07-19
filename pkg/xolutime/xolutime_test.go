// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package xolutime

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNowIsUTC(t *testing.T) {
	n := Now()
	if loc := n.Time().Location(); loc != time.UTC {
		t.Fatalf("Now() location = %v, want UTC", loc)
	}
}

func TestFromTimeNormalizesButPreservesInstant(t *testing.T) {
	loc, err := time.LoadLocation("America/Montevideo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	local := time.Date(2026, 7, 8, 9, 0, 0, 0, loc)
	inst := FromTime(local)

	if inst.Time().Location() != time.UTC {
		t.Fatalf("FromTime location = %v, want UTC", inst.Time().Location())
	}
	if !inst.Time().Equal(local) {
		t.Fatalf("FromTime changed the instant: got %v, want equal to %v",
			inst.Time(), local)
	}
}

func TestUnixNanoRoundTrip(t *testing.T) {
	// Mirrors the timeseries codec: uint64(ts.UnixNano()) and
	// time.Unix(0, ns).UTC() must agree with xolutime.
	orig := MustParse("2026-07-08T12:34:56.789Z")
	ns := orig.UnixNano()
	back := FromUnixNano(ns)
	if !back.Equal(orig) {
		t.Fatalf("UnixNano round-trip: got %v, want %v", back, orig)
	}
	if back.UnixNano() != ns {
		t.Fatalf("UnixNano not stable: %d != %d", back.UnixNano(), ns)
	}
}

func TestParseRejectsZoneNaive(t *testing.T) {
	naive := []string{
		"2026-07-08T09:00:00",
		"2026-07-08T09:00:00.123",
		"2026-07-08",
		"2026-07-08 09:00:00", // space, no zone
	}
	for _, s := range naive {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded; want rejection of zone-naive input", s)
		}
	}
}

func TestParseAcceptsExplicitZone(t *testing.T) {
	cases := map[string]string{
		"2026-07-08T09:00:00Z":          "2026-07-08T09:00:00Z",
		"2026-07-08T09:00:00-03:00":     "2026-07-08T12:00:00Z", // -03 -> UTC
		"2026-07-08T09:00:00+05:30":     "2026-07-08T03:30:00Z",
		"2026-07-08T09:00:00.500-03:00": "2026-07-08T12:00:00.5Z",
	}
	for in, wantUTC := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) failed: %v", in, err)
			continue
		}
		want := MustParse(wantUTC)
		if !got.Equal(want) {
			t.Errorf("Parse(%q) = %v, want %v", in, got, want)
		}
		if got.Time().Location() != time.UTC {
			t.Errorf("Parse(%q) location = %v, want UTC", in, got.Time().Location())
		}
	}
}

func TestFormatRequiresExplicitZone(t *testing.T) {
	inst := MustParse("2026-07-08T12:00:00Z")
	loc, err := time.LoadLocation("America/Montevideo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// -03:00 in July (no DST in Uruguay since 2015) -> 09:00 local.
	got := inst.Format("2006-01-02T15:04:05", loc)
	if got != "2026-07-08T09:00:00" {
		t.Fatalf("Format in Montevideo = %q, want 2026-07-08T09:00:00", got)
	}
	// nil location must not panic; defaults to UTC.
	if u := inst.Format(time.RFC3339, nil); u != "2026-07-08T12:00:00Z" {
		t.Fatalf("Format(nil loc) = %q, want UTC", u)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	type wrapper struct {
		At Instant `json:"at"`
	}
	orig := wrapper{At: MustParse("2026-07-08T12:00:00Z")}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got wrapper
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.At.Equal(orig.At) {
		t.Fatalf("JSON round-trip: got %v, want %v", got.At, orig.At)
	}
}

func TestUnmarshalRejectsNaive(t *testing.T) {
	var i Instant
	if err := i.UnmarshalJSON([]byte(`"2026-07-08T09:00:00"`)); err == nil {
		t.Fatal("UnmarshalJSON accepted zone-naive string; want rejection")
	}
}

func TestSubIsAbsoluteDuration(t *testing.T) {
	a := MustParse("2026-07-08T09:00:00-03:00")
	b := MustParse("2026-07-08T12:00:00Z") // same instant
	if d := a.Sub(b); d != 0 {
		t.Fatalf("same instant, different zone: Sub = %v, want 0", d)
	}
}

// TestZoneRuleChangeArithmetic verifies the *arithmetic* xolutime provides for an
// upstream layer to recover from a zone-rule (DST/offset) change. It deliberately
// does NOT test a cal "move": cal has no implementation, and by design the
// recovery trigger, the retained intention, and the move re-issue all live ABOVE
// cal, not in xolu (see docs/TIME_HANDLING.md). The only xolu-owned part of recovery
// is "resolve a wall-clock intention to an absolute instant," which is what this
// checks. It is intentionally a unit test of resolution, not a system test of
// recovery — the latter cannot be honestly written until cal exists and would
// then exercise an upstream application, not xolu.
func TestZoneRuleChangeArithmetic(t *testing.T) {
	loc, err := time.LoadLocation("America/Montevideo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	// An upstream layer holds the *intention*: 09:00 local, Montevideo.
	// It resolves that to an absolute instant for storage.
	resolve := func(localHour int, offsetRules *time.Location) Instant {
		return FromTime(time.Date(2027, 3, 15, localHour, 0, 0, 0, offsetRules))
	}

	// Under the current rules (Uruguay has had no DST since 2015; UTC-3 in March),
	// 09:00 local resolves to 12:00Z.
	stored := resolve(9, loc)
	wantUTC := MustParse("2027-03-15T12:00:00Z")
	if !stored.Equal(wantUTC) {
		t.Fatalf("resolution under current rules = %v, want %v", stored, wantUTC)
	}

	// Simulate a hypothetical rule change: Uruguay reinstates DST and is UTC-2 on
	// that date. We model the changed rule with a fixed zone (the test cannot
	// rewrite the system tzdata, and should not try). The SAME 09:00 local
	// intention now resolves to a DIFFERENT instant — 11:00Z.
	dstReinstated := time.FixedZone("UYST", -2*60*60)
	recomputed := resolve(9, dstReinstated)
	if recomputed.Equal(stored) {
		t.Fatal("recomputed instant equals the old one; a real rule change must shift it")
	}
	if !recomputed.Equal(MustParse("2027-03-15T11:00:00Z")) {
		t.Fatalf("recomputed instant = %v, want 2027-03-15T11:00:00Z", recomputed)
	}

	// The point the upstream layer relies on: the OLD stored instant is now stale
	// relative to the SAME human intention, and xolutime arithmetic surfaces the
	// gap (one hour) that the upstream layer would correct via a cal move. xolu
	// provides this delta; it does not detect the rule change or issue the move.
	gap := stored.Sub(recomputed)
	if gap != time.Hour {
		t.Fatalf("staleness gap = %v, want 1h", gap)
	}
}
