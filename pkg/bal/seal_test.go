// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/chronicle"
)

// sealedStore builds on rollupStore(t) (which already opens the Pebble
// rollup plane) and additionally wires the seal frontier — the third
// long-lived, optional-nil Store dependency, same setup shape as claims
// and rollup.
func sealedStore(t *testing.T) *Store {
	t.Helper()
	s := rollupStore(t)
	if err := s.InitSeal(context.Background()); err != nil {
		t.Fatal(err)
	}
	sealer, err := chronicle.NewSealer(chronicle.MonthWindows)
	if err != nil {
		t.Fatal(err)
	}
	s.SetSealer(sealer)
	return s
}

func TestSealPeriod_ChecksAllPostableAccounts(t *testing.T) {
	s := sealedStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "a", Unit: "u", Postable: true})
	mustDefine(t, s, AccountDef{ID: "b", Unit: "u", Postable: true})
	// A non-postable account must NOT be checkpointed (SealPeriod's own
	// query filters on postable = 1) — define one to prove it's excluded,
	// not merely absent from the count by coincidence.
	mustDefine(t, s, AccountDef{ID: "summary", Unit: "u", Postable: false})

	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := s.Transfer(ctx, "t1", "~in", "a", 100, "", at); err != nil {
		t.Fatal(err)
	}
	if err := s.Transfer(ctx, "t2", "~in", "b", 50, "", at); err != nil {
		t.Fatal(err)
	}

	sealAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	n, err := s.SealPeriod(ctx, sealAt)
	if err != nil {
		t.Fatalf("SealPeriod: %v", err)
	}
	// ~in, a, b are postable = 4 - 1 non-postable (summary) = 3.
	if n != 3 {
		t.Fatalf("SealPeriod checkpointed %d accounts, want 3 (postable only)", n)
	}
}

func TestSealPeriod_RefusesEntryWithinSealedPeriod(t *testing.T) {
	s := sealedStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})

	// Seal through the end of June.
	if _, err := s.SealPeriod(ctx, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	// A transfer dated in June — inside the sealed period — must be refused.
	err := s.Transfer(ctx, "t", "~in", "acct", 10, "", time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	if _, ok := err.(*SealedPeriodError); !ok {
		t.Fatalf("expected *SealedPeriodError for an entry inside the sealed period, got %T: %v", err, err)
	}

	// A transfer dated in July — after the frontier — must succeed.
	if err := s.Transfer(ctx, "t2", "~in", "acct", 10, "", time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("transfer after the frontier must succeed, got %v", err)
	}
}

// TestSealPeriod_OverridesBackdatedPolicy is the property item 16 §7
// is actually about: sealing is unconditional, independent of any
// account's own temporal_policy. A backdated-policy account normally
// accepts out-of-order entries freely (that's the whole point of the
// policy) — sealing must refuse it anyway. If this test passed with
// BoundsError or no error at all, sealing would be silently
// downgraded to "advisory," which defeats the point of it existing
// as a SEPARATE axis from temporal_policy.
func TestSealPeriod_OverridesBackdatedPolicy(t *testing.T) {
	s := sealedStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true, Policy: "backdated"})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true, Policy: "backdated"})

	if _, err := s.SealPeriod(ctx, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	err := s.Transfer(ctx, "t", "~in", "acct", 10, "", time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	if _, ok := err.(*SealedPeriodError); !ok {
		t.Fatalf("backdated policy must NOT override a sealed period; expected *SealedPeriodError, got %T: %v", err, err)
	}
}

func TestSealPeriod_NoSealerAttachedIsExactPreSealBehaviour(t *testing.T) {
	s := rollupStore(t) // deliberately no SetSealer: matches testStore(t)'s no-rollup precedent
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})

	// Any date, including one that would be sealed if a sealer were
	// attached, must succeed when no sealer is wired at all.
	if err := s.Transfer(ctx, "t", "~in", "acct", 10, "", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("with no sealer attached, no date should ever be refused for sealing, got %v", err)
	}
}

// TestLoadSealer_RecoversPersistedFrontierAcrossInstances pins the
// actual point of persisting the frontier at all: a NEW Sealer loaded
// from the same database (simulating a process restart, since
// bal.Store is built fresh per request in production and the Sealer
// is meant to be loaded once and cached, not reconstructed with no
// memory of prior seals) must recover the same frontier a previous
// instance advanced to, not silently reset to unsealed.
func TestLoadSealer_RecoversPersistedFrontierAcrossInstances(t *testing.T) {
	s := sealedStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})

	sealAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.SealPeriod(ctx, sealAt); err != nil {
		t.Fatal(err)
	}

	// A fresh Sealer loaded from the SAME database must recover the
	// persisted frontier, not start unsealed.
	recovered, err := LoadSealer(ctx, s.db, s.tenantID)
	if err != nil {
		t.Fatalf("LoadSealer: %v", err)
	}
	if !recovered.Sealed(time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("a freshly-loaded Sealer must recover the persisted frontier — June should still read as sealed")
	}
	if recovered.Sealed(time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("July (after the frontier) must not read as sealed")
	}
}
