// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ha1tch/xolu/pkg/chronicle"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	tmp, err := os.MkdirTemp("", "bal")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	// House SQLite contract: WAL + busy_timeout, matching pkg/storage's
	// configuration — concurrent write transactions queue rather than
	// failing SQLITE_BUSY. bal assumes this contract (see Store docs).
	db, err := sql.Open("sqlite",
		tmp+"/bal.db?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewStore(db, "t0000_")
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func mustDefine(t *testing.T, s *Store, def AccountDef) AccountKey {
	t.Helper()
	k, err := s.DefineAccount(context.Background(), def)
	if err != nil {
		t.Fatalf("define %s: %v", def.ID, err)
	}
	return k
}

var now = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

func TestTransfer_ConservationIdentity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~received", Unit: "widget", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "warehouse:A/widget", Unit: "widget", Postable: true})
	mustDefine(t, s, AccountDef{ID: "warehouse:B/widget", Unit: "widget", Postable: true})

	if err := s.Transfer(ctx, "tx1", "~received", "warehouse:A/widget", 100, "goods in", now); err != nil {
		t.Fatal(err)
	}
	if err := s.Transfer(ctx, "tx2", "warehouse:A/widget", "warehouse:B/widget", 30, "", now); err != nil {
		t.Fatal(err)
	}

	// Conservation: the system total is identically zero.
	var total int64
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM t0000_bal_journal`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("conservation broken: journal sums to %d", total)
	}
	if v, _, _ := s.Balance(ctx, "warehouse:A/widget"); v != 70 {
		t.Fatalf("A: %d, want 70", v)
	}
	if v, _, _ := s.Balance(ctx, "warehouse:B/widget"); v != 30 {
		t.Fatalf("B: %d, want 30", v)
	}
	if v, _, _ := s.Balance(ctx, "~received"); v != -100 {
		t.Fatalf("boundary: %d, want -100", v)
	}
}

func TestTransfer_FloorGuardRefuses(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "a", Unit: "u", Postable: true}) // floor 0, balance 0
	mustDefine(t, s, AccountDef{ID: "b", Unit: "u", Postable: true})

	err := s.Transfer(ctx, "tx1", "a", "b", 1, "", now)
	var be *BoundsError
	if !errors.As(err, &be) || be.Side != "floor" {
		t.Fatalf("got %v, want floor BoundsError", err)
	}
	// Nothing written: the refused transfer leaves no journal rows.
	var n int64
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM t0000_bal_journal`).Scan(&n)
	if n != 0 {
		t.Fatalf("refused transfer wrote %d entries", n)
	}
}

func TestTransfer_CeilingGuardRefuses(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ceil := int64(50)
	mustDefine(t, s, AccountDef{ID: "src", Unit: "u", Floor: -1000, Postable: true})
	mustDefine(t, s, AccountDef{ID: "capped", Unit: "u", Ceiling: &ceil, Postable: true})

	if err := s.Transfer(ctx, "t1", "src", "capped", 50, "", now); err != nil {
		t.Fatal(err)
	}
	err := s.Transfer(ctx, "t2", "src", "capped", 1, "", now)
	var be *BoundsError
	if !errors.As(err, &be) || be.Side != "ceiling" {
		t.Fatalf("got %v, want ceiling BoundsError", err)
	}
	// The debit leg must have rolled back with the refused credit.
	if v, _, _ := s.Balance(ctx, "src"); v != -50 {
		t.Fatalf("src after refused transfer: %d, want -50 (only t1)", v)
	}
}

func TestTransfer_ChainTriple(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})

	for i := 0; i < 5; i++ {
		if err := s.Transfer(ctx, "tx", "~in", "acct", 10, "", now); err != nil {
			t.Fatal(err)
		}
	}
	breaks, err := s.VerifyChains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(breaks) != 0 {
		t.Fatalf("chain breaks on clean journal: %+v", breaks)
	}

	// Tamper with one entry: the chain must localise it.
	if _, err := s.db.Exec(`UPDATE t0000_bal_journal SET amount = amount + 1 WHERE entry_id = 4`); err != nil {
		t.Fatal(err)
	}
	breaks, _ = s.VerifyChains(ctx)
	if len(breaks) == 0 {
		t.Fatal("tampered entry not detected")
	}
	found := false
	for _, b := range breaks {
		if b.EntryID == 4 {
			found = true
		}
	}
	if !found {
		t.Fatalf("break not localised to entry 4: %+v", breaks)
	}
}

func TestVerify_GlobalFoldOracle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})
	_ = s.Transfer(ctx, "t1", "~in", "acct", 42, "", now)

	res, err := s.GlobalFoldOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Equal {
		t.Fatalf("clean store diverged: %s", res.FirstDivergence)
	}
	// Corrupt balances behind the guard's back: the fold must catch it.
	if _, err := s.db.Exec(`UPDATE t0000_bal_balances SET value = value + 5 WHERE account_key = 2`); err != nil {
		t.Fatal(err)
	}
	res, _ = s.GlobalFoldOracle().Check(ctx)
	if res.Equal {
		t.Fatal("fold oracle missed a corrupted balance")
	}
	// And CheckAll runs both verifiers as results.
	results, err := chronicle.CheckAll(ctx, []chronicle.RebuildOracle{s.GlobalFoldOracle(), s.ChainOracle()})
	if err != nil || len(results) != 2 {
		t.Fatalf("CheckAll: %v %v", results, err)
	}
}

func TestTransfer_Refusals(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "1", Unit: "u", Postable: false}) // summary
	mustDefine(t, s, AccountDef{ID: "1.1", Unit: "u", Floor: -100, Postable: true})

	var np *NotPostableError
	if err := s.Transfer(ctx, "t", "1", "1.1", 1, "", now); !errors.As(err, &np) {
		t.Fatalf("summary source: %v, want NotPostable", err)
	}
	if err := s.Transfer(ctx, "t", "1.1", "1", 1, "", now); !errors.As(err, &np) {
		t.Fatalf("summary target: %v, want NotPostable", err)
	}
	var ua *UnknownAccountError
	if err := s.Transfer(ctx, "t", "1.1", "ghost", 1, "", now); !errors.As(err, &ua) {
		t.Fatalf("unknown target: %v, want UnknownAccount", err)
	}
	var as *AmountScaleError
	if err := s.Transfer(ctx, "t", "1.1", "1.1", 1, "", now); !errors.As(err, &as) {
		t.Fatalf("self transfer: %v, want AmountScale", err)
	}
	if err := s.Transfer(ctx, "t", "1.1", "ghost", 0, "", now); !errors.As(err, &as) {
		t.Fatalf("zero amount: %v, want AmountScale", err)
	}
}

// ─── Numerics (@B04): no float ever ─────────────────────────────────────────

func TestParseAmount_ExactAndRefusals(t *testing.T) {
	ok := func(s string, scale uint8, want int64) {
		t.Helper()
		v, err := ParseAmount(s, scale)
		if err != nil || v != want {
			t.Fatalf("ParseAmount(%q,%d) = %d,%v want %d", s, scale, v, err, want)
		}
	}
	bad := func(s string, scale uint8) {
		t.Helper()
		if v, err := ParseAmount(s, scale); err == nil {
			t.Fatalf("ParseAmount(%q,%d) = %d, want error", s, scale, v)
		}
	}
	ok("12.34", 2, 1234)
	ok("-0.05", 2, -5)
	ok("7", 2, 700)
	ok("0.000000000000000001", 18, 1)
	ok("9223372036854775807", 0, 1<<63-1)

	bad("12.345", 2)       // finer than scale
	bad("1e3", 0)          // float notation smuggling
	bad("0x10", 0)         // hex smuggling
	bad("NaN", 2)          // float vocabulary
	bad("1.0.0", 2)        // malformed
	bad("", 2)             // empty
	bad("9223372036854775808", 0) // overflow

	// Round trip through the canonical renderer.
	if got := FormatAmount(1234, 2); got != "12.34" {
		t.Fatalf("FormatAmount: %q", got)
	}
	if got := FormatAmount(-5, 2); got != "-0.05" {
		t.Fatalf("FormatAmount: %q", got)
	}
}

// ─── @C04d obligations for the internal key (@B09a) ─────────────────────────

func TestAccountKey_CodecFullRange(t *testing.T) {
	// The full uint32 span, including values above 2^31 and the ceiling:
	// codec round-trip must be lossless (the width regression analogous
	// to pkg/timeseries/timeline_id_width_test.go).
	cases := []AccountKey{0, 1, 65535, 65536, 1 << 31, 1<<31 + 1, MaxAccountKey - 1, MaxAccountKey}
	for _, k := range cases {
		if got := DecodeAccountKey(EncodeAccountKey(k)); got != k {
			t.Fatalf("codec round trip lost %d → %d", k, got)
		}
	}
	if uint64(MaxAccountKey) != 0xFFFFFFFF {
		t.Fatal("ceiling does not fit uint32 exactly")
	}
}
