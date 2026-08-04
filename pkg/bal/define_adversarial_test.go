// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// define_adversarial_test.go — the same two bugs /loc's own
// adversarial pass (T-118) found in Def/DefFence, confirmed present
// in DefineAccount and fixed here: (1) a read-first dense-key
// allocation racing under concurrent load (T-115's own WAL class),
// (2) a duplicate account_id surfacing as an unwrapped SQLite driver
// error instead of a typed, correctly-mapped one.

package bal

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestDefineAccount_DuplicateAccountID_TypedError is the direct
// regression test: a duplicate account_id must return
// *DuplicateAccountError, not an unwrapped *sqlite.Error that would
// map to a bare 500 at the HTTP layer.
func TestDefineAccount_DuplicateAccountID_TypedError(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	def := AccountDef{ID: "dup-acct", Unit: "EUR", Scale: 2, Postable: true}
	if _, err := s.DefineAccount(ctx, def); err != nil {
		t.Fatal(err)
	}
	_, err := s.DefineAccount(ctx, def)
	if _, ok := err.(*DuplicateAccountError); !ok {
		t.Fatalf("duplicate account_id: want *DuplicateAccountError, got %T: %v", err, err)
	}

	// The original account must be completely undamaged.
	bal, _, err := s.Balance(ctx, "dup-acct")
	if err != nil {
		t.Fatalf("original account damaged by the failed duplicate define: %v", err)
	}
	if bal != 0 {
		t.Fatalf("original account's balance changed by the failed duplicate: want 0, got %d", bal)
	}
}

// TestDefineAccount_ConcurrentDistinctAccounts_NoKeyCollision is
// loc's own TestDef_ConcurrentDistinctLocations_NoKeyCollision,
// applied directly to DefineAccount — proving the write-first fix
// holds here too, not assumed safe by analogy to loc's own fix alone.
func TestDefineAccount_ConcurrentDistinctAccounts_NoKeyCollision(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	const n = 30
	var wg sync.WaitGroup
	var errs int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("concurrent-acct-%d", i)
			if _, err := s.DefineAccount(ctx, AccountDef{ID: id, Unit: "EUR", Scale: 2, Postable: true}); err != nil {
				atomic.AddInt64(&errs, 1)
				t.Logf("DefineAccount(%q) failed: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	if errs > 0 {
		t.Fatalf("%d of %d concurrent DefineAccount calls for DISTINCT account_ids failed — dense-key allocation raced incorrectly", errs, n)
	}

	var total, distinctKeys int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+s.accountsTable()+` WHERE account_id LIKE 'concurrent-acct-%'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != n {
		t.Fatalf("want %d distinct accounts created, got %d", n, total)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT account_key) FROM `+s.accountsTable()+` WHERE account_id LIKE 'concurrent-acct-%'`).Scan(&distinctKeys); err != nil {
		t.Fatal(err)
	}
	if distinctKeys != n {
		t.Fatalf("want %d distinct internal keys, got %d — a key collision occurred", n, distinctKeys)
	}
}
