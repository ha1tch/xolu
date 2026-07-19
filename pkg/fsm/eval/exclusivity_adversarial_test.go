// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

// exclusivity_adversarial_test.go — adversarial contract for the guard
// exclusivity recognizer, written BEFORE the recognizer.
//
// The recognizer answers: given a set of guards on one (state, input), are they
// PROVABLY mutually exclusive? It must be SOUND — it may never report "exclusive"
// for guards that can both be true. It is allowed to be INCOMPLETE — reporting
// "not proven" for guards that are in fact exclusive but outside a recognized
// pattern is acceptable (the author falls back to firstmatch).
//
// The cardinal sin is a FALSE POSITIVE: claiming exclusivity where an overlap
// exists. Every case below where guards can both be true MUST be rejected, with
// a reason precise enough to drive a smart error message. Cases that are
// provably exclusive in a recognized pattern MUST be accepted.

import "testing"

// helper: parse a list of guard strings into AST nodes for the recognizer.
func guards(t *testing.T, exprs ...string) []GuardExpr {
	t.Helper()
	out := make([]GuardExpr, len(exprs))
	for i, e := range exprs {
		node, err := ParseGuard(e)
		if err != nil {
			t.Fatalf("parse guard %q: %v", e, err)
		}
		out[i] = GuardExpr{Source: e, AST: node}
	}
	return out
}

// ─── MUST ACCEPT: provably exclusive in a recognized pattern ──────────────────

func TestExclusivity_AcceptsNullPartition(t *testing.T) {
	// The packet-validator triple: present-valid / present-invalid / absent.
	g := guards(t,
		"payload.v IS NOT NULL AND payload.v = 1",
		"payload.v IS NOT NULL AND payload.v != 1",
		"payload.v IS NULL",
	)
	r := CheckExclusivity(g)
	if !r.Exclusive {
		t.Errorf("null-partition triple should be provably exclusive; reason: %s", r.Reason)
	}
}

func TestExclusivity_AcceptsDistinctLiteralEquality(t *testing.T) {
	g := guards(t, "@v = 1", "@v = 2", "@v = 3")
	r := CheckExclusivity(g)
	if !r.Exclusive {
		t.Errorf("distinct-literal equality should be exclusive; reason: %s", r.Reason)
	}
}

func TestExclusivity_AcceptsEqualityVsNegation(t *testing.T) {
	g := guards(t, "@v = 5", "@v != 5")
	r := CheckExclusivity(g)
	if !r.Exclusive {
		t.Errorf("@v=5 vs @v!=5 should be exclusive; reason: %s", r.Reason)
	}
}

func TestExclusivity_AcceptsComplementaryThreshold(t *testing.T) {
	g := guards(t, "@v < 10", "@v >= 10")
	r := CheckExclusivity(g)
	if !r.Exclusive {
		t.Errorf("@v<10 vs @v>=10 should be exclusive; reason: %s", r.Reason)
	}
	g2 := guards(t, "@v <= 10", "@v > 10")
	if r2 := CheckExclusivity(g2); !r2.Exclusive {
		t.Errorf("@v<=10 vs @v>10 should be exclusive; reason: %s", r2.Reason)
	}
}

func TestExclusivity_AcceptsSingleGuard(t *testing.T) {
	// One guard is trivially "exclusive" (nothing to overlap with).
	if r := CheckExclusivity(guards(t, "@v = 1")); !r.Exclusive {
		t.Errorf("single guard should be exclusive; reason: %s", r.Reason)
	}
}

// ─── MUST REJECT: guards that can both be true (false-positive = correctness hole)

func TestExclusivity_RejectsOverlappingRanges(t *testing.T) {
	// @v >= 5 AND @v <= 5 both fire at v=5.
	g := guards(t, "@v >= 5", "@v <= 5")
	r := CheckExclusivity(g)
	if r.Exclusive {
		t.Error("@v>=5 vs @v<=5 OVERLAP at 5 — must not be reported exclusive")
	}
	if r.Reason == "" {
		t.Error("rejection must carry a reason")
	}
}

func TestExclusivity_RejectsOverlappingOpenRanges(t *testing.T) {
	// @v > 0 and @v < 100 overlap on (0,100).
	if r := CheckExclusivity(guards(t, "@v > 0", "@v < 100")); r.Exclusive {
		t.Error("@v>0 vs @v<100 overlap — must not be exclusive")
	}
}

func TestExclusivity_RejectsSameLiteralEquality(t *testing.T) {
	// @v = 1 and @v = 1 are identical — both fire together, not exclusive.
	if r := CheckExclusivity(guards(t, "@v = 1", "@v = 1")); r.Exclusive {
		t.Error("identical equality guards both fire — must not be exclusive")
	}
}

func TestExclusivity_RejectsDifferentVariables(t *testing.T) {
	// @a = 1 and @b = 1 are independent; both can be true.
	if r := CheckExclusivity(guards(t, "@a = 1", "@b = 1")); r.Exclusive {
		t.Error("guards on different variables can both be true — must not be exclusive")
	}
}

func TestExclusivity_RejectsEqualityVsDifferentNegation(t *testing.T) {
	// @v = 5 and @v != 6 both fire at v=5.
	if r := CheckExclusivity(guards(t, "@v = 5", "@v != 6")); r.Exclusive {
		t.Error("@v=5 vs @v!=6 both true at 5 — must not be exclusive")
	}
}

func TestExclusivity_RejectsNullPartitionOnDifferentFields(t *testing.T) {
	// IS NULL on @a does not partition a guard about @b.
	g := guards(t, "@a IS NULL", "@b IS NOT NULL AND @b = 1")
	if r := CheckExclusivity(g); r.Exclusive {
		t.Error("null check on @a vs condition on @b — must not be exclusive")
	}
}

func TestExclusivity_RejectsOverlappingThresholds(t *testing.T) {
	// @v < 10 and @v < 5: both fire for v<5. Not complementary.
	if r := CheckExclusivity(guards(t, "@v < 10", "@v < 5")); r.Exclusive {
		t.Error("@v<10 vs @v<5 overlap for v<5 — must not be exclusive")
	}
}

func TestExclusivity_RejectsUnrecognizedShape(t *testing.T) {
	// Arithmetic guards the recognizer cannot reason about: must be sound and
	// report not-proven rather than guessing.
	g := guards(t, "@a + @b > 10", "@a + @b <= 10")
	r := CheckExclusivity(g)
	// These ARE actually complementary, but if the recognizer cannot prove it,
	// it must report not-exclusive (sound incompleteness), never a false yes by
	// accident. We assert only that it does not crash and gives a reason if it
	// says not-exclusive.
	if !r.Exclusive && r.Reason == "" {
		t.Error("not-proven result must carry a reason")
	}
	// The critical property: it must never claim exclusivity it cannot justify.
	// (We cannot assert true here since proving it is out of scope; we assert it
	// does not falsely claim exclusivity for a DIFFERENT, overlapping pair below.)
}

func TestExclusivity_RejectsPartialOverlapInTriple(t *testing.T) {
	// Three guards where two are exclusive but the third overlaps one of them.
	// The whole set must be rejected — exclusivity is a property of the SET.
	g := guards(t,
		"@v = 1",
		"@v = 2",
		"@v >= 1", // overlaps both @v=1 and @v=2
	)
	if r := CheckExclusivity(g); r.Exclusive {
		t.Error("set with an overlapping third guard must not be exclusive")
	}
}

// ─── Soundness stress: never panic, always a reason on rejection ──────────────

func TestExclusivity_NeverPanics(t *testing.T) {
	sets := [][]string{
		{"@v = 1", "@v = 2", "@v = 3", "@v = 4", "@v = 5"},
		{"payload.a IS NULL", "payload.a IS NOT NULL"},
		{"@v < 0", "@v > 0", "@v = 0"},
		{"NOT (@v = 1)", "@v = 1"},
		{"@a = 1 AND @b = 2", "@a = 1 AND @b = 3"},
		{"1 = 1", "2 = 2"}, // constant guards, both always true
	}
	for _, s := range sets {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Errorf("CheckExclusivity panicked on %v: %v", s, rec)
				}
			}()
			_ = CheckExclusivity(guards(t, s...))
		}()
	}
}

func TestExclusivity_ConstantGuardsNotExclusive(t *testing.T) {
	// Two always-true constant guards both fire — never exclusive.
	if r := CheckExclusivity(guards(t, "1 = 1", "2 = 2")); r.Exclusive {
		t.Error("two always-true constant guards both fire — must not be exclusive")
	}
}

// ─── Extension: interval atoms (chained AND of bounds on one variable) ────────

func TestExclusivity_AcceptsDisjointIntervals(t *testing.T) {
	// Two non-overlapping ranges on the same variable.
	g := guards(t,
		"@v > 0 AND @v <= 100",
		"@v > 100 AND @v <= 200",
	)
	if r := CheckExclusivity(g); !r.Exclusive {
		t.Errorf("disjoint intervals should be exclusive; reason: %s", r.Reason)
	}
}

func TestExclusivity_AcceptsValidVsOutOfBoundsInterval(t *testing.T) {
	// The packet-validator length pattern: valid range vs its complement,
	// partitioned by IS NULL / IS NOT NULL.
	g := guards(t,
		"payload.len IS NOT NULL AND payload.len > 0 AND payload.len <= 1024",
		"payload.len IS NOT NULL AND (payload.len <= 0 OR payload.len > 1024)",
		"payload.len IS NULL",
	)
	if r := CheckExclusivity(g); !r.Exclusive {
		t.Errorf("length valid/invalid/missing partition should be exclusive; reason: %s", r.Reason)
	}
}

func TestExclusivity_RejectsOverlappingIntervals(t *testing.T) {
	// Ranges that share values must be rejected.
	g := guards(t,
		"@v > 0 AND @v <= 100",
		"@v > 50 AND @v <= 200", // overlaps (50,100]
	)
	if r := CheckExclusivity(g); r.Exclusive {
		t.Error("overlapping intervals (50,100] shared — must not be exclusive")
	}
}

func TestExclusivity_RejectsTouchingIntervalsThatShareEndpoint(t *testing.T) {
	// @v <= 100 and @v >= 100 share exactly v=100.
	g := guards(t,
		"@v > 0 AND @v <= 100",
		"@v >= 100 AND @v < 200",
	)
	if r := CheckExclusivity(g); r.Exclusive {
		t.Error("intervals sharing endpoint 100 — must not be exclusive")
	}
}

// ─── Extension: variable-vs-variable equality complementarity ─────────────────

func TestExclusivity_AcceptsVarVsVarEqualityNegation(t *testing.T) {
	// @a = @b and @a != @b are exact logical complements regardless of values.
	g := guards(t, "@received = @expected", "@received != @expected")
	if r := CheckExclusivity(g); !r.Exclusive {
		t.Errorf("@a=@b vs @a!=@b should be exclusive (exact complement); reason: %s", r.Reason)
	}
}

func TestExclusivity_AcceptsVarVsVarWithNullPartition(t *testing.T) {
	// The packet-validator checksum pattern.
	g := guards(t,
		"payload.crc IS NOT NULL AND payload.crc = @received",
		"payload.crc IS NOT NULL AND payload.crc != @received",
		"payload.crc IS NULL",
	)
	if r := CheckExclusivity(g); !r.Exclusive {
		t.Errorf("crc match/mismatch/missing partition should be exclusive; reason: %s", r.Reason)
	}
}

func TestExclusivity_RejectsVarVsVarDifferentRHS(t *testing.T) {
	// @a = @b and @a != @c are NOT complements — both can hold (a=b, c!=a).
	g := guards(t, "@a = @b", "@a != @c")
	if r := CheckExclusivity(g); r.Exclusive {
		t.Error("@a=@b vs @a!=@c are not complements — must not be exclusive")
	}
}

func TestExclusivity_RejectsVarVsVarSameComparison(t *testing.T) {
	// @a = @b and @a = @b are identical — both fire together.
	g := guards(t, "@a = @b", "@a = @b")
	if r := CheckExclusivity(g); r.Exclusive {
		t.Error("identical var-vs-var equality — must not be exclusive")
	}
}

// ─── Extension: null-OR-region predicates ("missing or invalid") ──────────────

func TestExclusivity_AcceptsNullOrRegionVsPresentValid(t *testing.T) {
	// The packet-validator chunk pattern: "absent or non-positive" vs
	// "present and positive". These partition cleanly.
	g := guards(t,
		"payload.size IS NOT NULL AND payload.size > 0",
		"payload.size IS NULL OR payload.size <= 0",
	)
	if r := CheckExclusivity(g); !r.Exclusive {
		t.Errorf("present-valid vs missing-or-invalid should be exclusive; reason: %s", r.Reason)
	}
}

func TestExclusivity_RejectsNullOrRegionThatOverlapsValue(t *testing.T) {
	// "absent or <= 5" vs "present and >= 5" overlap at value 5.
	g := guards(t,
		"@v IS NULL OR @v <= 5",
		"@v IS NOT NULL AND @v >= 5",
	)
	if r := CheckExclusivity(g); r.Exclusive {
		t.Error("null-or-(<=5) vs present-and-(>=5) overlap at 5 — must not be exclusive")
	}
}

func TestExclusivity_RejectsNullOrRegionBothAllowNull(t *testing.T) {
	// Two predicates that both admit the null case overlap when the var is null.
	g := guards(t,
		"@v IS NULL OR @v <= 0",
		"@v IS NULL OR @v >= 100",
	)
	if r := CheckExclusivity(g); r.Exclusive {
		t.Error("both predicates admit null — overlap when var is null — must not be exclusive")
	}
}
