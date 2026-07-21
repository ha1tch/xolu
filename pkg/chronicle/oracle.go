// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package chronicle

import (
	"context"
	"fmt"
	"strings"
)

// The rebuild-oracle harness (@C §4 extraction #3): one test-and-tooling
// shape — derive(journal) == current — generalising cal's
// rebuild-from-SQLite oracle ("the source is authoritative and the index
// is derived") into a form any primitive instantiates with a deriveFn.
//
// The contract is fingerprint-based: Derive replays the authoritative
// record into a CANONICAL, DETERMINISTIC serialisation of the state it
// implies; Current serialises the live derived state the same way. Equal
// fingerprints prove the derived state is exactly what the record
// implies; unequal ones localise the first divergence. Line-oriented
// fingerprints (one fact per line, sorted) make divergences readable.
//
// Two consumers by design: tests (AssertRebuildOracle) and operations
// tooling (`iolu db check` runs CheckAll without a testing.T — the
// operations roadmap item 5 shape).

// RebuildOracle names one derived-state invariant for one primitive.
type RebuildOracle struct {
	// Name identifies the oracle in reports, e.g. "cal.index",
	// "graph.edges", "ts.rollups".
	Name string
	// Derive replays the authoritative record (journal, source table)
	// into the canonical fingerprint of the state it implies.
	Derive func(ctx context.Context) (string, error)
	// Current serialises the live derived state into the same canonical
	// form.
	Current func(ctx context.Context) (string, error)
}

// OracleResult reports one oracle's outcome.
type OracleResult struct {
	Name  string
	Equal bool
	// FirstDivergence describes the first differing line when not equal:
	// "line N: derived=... current=..." — enough to localise, not a full
	// diff (fingerprints are available to the caller for that).
	FirstDivergence string
	Derived         string
	Current         string
}

// Check runs one oracle.
func (o RebuildOracle) Check(ctx context.Context) (*OracleResult, error) {
	if o.Derive == nil || o.Current == nil {
		return nil, fmt.Errorf("chronicle: oracle %q needs both Derive and Current", o.Name)
	}
	derived, err := o.Derive(ctx)
	if err != nil {
		return nil, fmt.Errorf("oracle %q: derive: %w", o.Name, err)
	}
	current, err := o.Current(ctx)
	if err != nil {
		return nil, fmt.Errorf("oracle %q: current: %w", o.Name, err)
	}
	res := &OracleResult{Name: o.Name, Derived: derived, Current: current, Equal: derived == current}
	if !res.Equal {
		res.FirstDivergence = firstDivergence(derived, current)
	}
	return res, nil
}

// CheckAll runs a set of oracles, failing fast only on execution errors —
// divergences are results, not errors, so one broken invariant does not
// hide another's report.
func CheckAll(ctx context.Context, oracles []RebuildOracle) ([]*OracleResult, error) {
	out := make([]*OracleResult, 0, len(oracles))
	for _, o := range oracles {
		r, err := o.Check(ctx)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// firstDivergence localises the first differing line between two
// line-oriented fingerprints.
func firstDivergence(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	n := len(al)
	if len(bl) < n {
		n = len(bl)
	}
	for i := 0; i < n; i++ {
		if al[i] != bl[i] {
			return fmt.Sprintf("line %d: derived=%q current=%q", i+1, al[i], bl[i])
		}
	}
	if len(al) != len(bl) {
		return fmt.Sprintf("length: derived has %d lines, current has %d", len(al), len(bl))
	}
	return "" // equal — not a divergence
}

// TestingT is the subset of *testing.T the assertion helper needs; an
// interface so the harness does not import testing (tooling links this
// package too).
type TestingT interface {
	Helper()
	Fatalf(format string, args ...interface{})
	Logf(format string, args ...interface{})
}

// AssertRebuildOracle runs the oracle and fails the test on divergence —
// the test-side consumption form.
func AssertRebuildOracle(t TestingT, ctx context.Context, o RebuildOracle) {
	t.Helper()
	res, err := o.Check(ctx)
	if err != nil {
		t.Fatalf("rebuild oracle %q: %v", o.Name, err)
	}
	if !res.Equal {
		t.Fatalf("rebuild oracle %q DIVERGED: %s\n-- derived --\n%s\n-- current --\n%s",
			o.Name, res.FirstDivergence, res.Derived, res.Current)
	}
	t.Logf("rebuild oracle %q: derived == current", o.Name)
}
