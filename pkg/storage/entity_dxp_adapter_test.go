// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/tenant"
)

func testEntityStore(t *testing.T) *SQLiteStore {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "entity-adapter")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	store, err := NewSQLiteStore(tmpDir+"/entity.db", SQLiteConfig{DBPath: tmpDir + "/entity.db"})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func entityFutureDeadline() int64 { return time.Now().Add(time.Minute).UnixNano() }

func mustCreateEntity(t *testing.T, store *SQLiteStore, entity string, data map[string]interface{}) int {
	t.Helper()
	id, err := store.Create(context.Background(), entity, data)
	if err != nil {
		t.Fatalf("create %s: %v", entity, err)
	}
	return id
}

func TestEntityAdapter_Reserve_Success(t *testing.T) {
	store := testEntityStore(t)
	id := mustCreateEntity(t, store, "widget", map[string]interface{}{"name": "gadget"})
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)

	cl, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		EntityUpdateParams{Entity: "widget", ID: id, Data: map[string]interface{}{"name": "gizmo"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if cl.Primitive != "entity" {
		t.Fatalf("unexpected claim: %+v", cl)
	}
}

func TestEntityAdapter_Reserve_NonexistentRefused(t *testing.T) {
	store := testEntityStore(t)
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)

	_, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		EntityUpdateParams{Entity: "widget", ID: 999, Data: map[string]interface{}{"name": "x"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err == nil {
		t.Fatal("expected reservation of a nonexistent entity to be refused")
	}
}

func TestEntityAdapter_Reserve_VersionMismatchRefused(t *testing.T) {
	store := testEntityStore(t)
	id := mustCreateEntity(t, store, "widget", map[string]interface{}{"name": "gadget"})
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)

	wrongVersion := 999
	_, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		EntityUpdateParams{Entity: "widget", ID: id, Data: map[string]interface{}{"name": "gizmo"}, ExpectVersion: &wrongVersion},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err != ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestEntityAdapter_Reserve_PessimisticExcludesSecondPessimistic(t *testing.T) {
	store := testEntityStore(t)
	id := mustCreateEntity(t, store, "widget", map[string]interface{}{"name": "gadget"})
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)
	ctx := context.Background()
	tenant := tenant.TenantID(0).String()
	params := EntityUpdateParams{Entity: "widget", ID: id, Data: map[string]interface{}{"name": "gizmo"}}

	if _, err := a.Reserve(ctx, tenant, params, "txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	_, err := a.Reserve(ctx, tenant, params, "txn-2", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err == nil {
		t.Fatal("expected second pessimistic reserve to be excluded")
	}
}

func TestEntityAdapter_Execute_AppliesUpdateAndClearsPending(t *testing.T) {
	store := testEntityStore(t)
	id := mustCreateEntity(t, store, "widget", map[string]interface{}{"name": "gadget"})
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)
	ctx := context.Background()

	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		EntityUpdateParams{Entity: "widget", ID: id, Data: map[string]interface{}{"name": "gizmo"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var dataJSON string
	if err := store.DB().QueryRow(`SELECT data FROM `+store.nodesTable()+` WHERE entity_type=? AND id=?`, "widget", id).Scan(&dataJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dataJSON, "gizmo") {
		t.Fatalf("expected updated data to contain gizmo, got %s", dataJSON)
	}

	tx2, _ := store.DB().BeginTx(ctx, nil)
	defer func() { _ = tx2.Rollback() }()
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx2), cl); err == nil {
		t.Fatal("expected second Execute for the same txn to fail (pending cleared)")
	}
}

func TestEntityAdapter_Release_IdempotentOnUnknownTxn(t *testing.T) {
	store := testEntityStore(t)
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)
	cl := dxp.Claim{Txn: "never-reserved", Primitive: "entity", Tenant: tenant.TenantID(0).String(), Resource: "entity:widget:1"}
	if err := a.Release(context.Background(), cl); err != nil {
		t.Fatalf("Release on unknown txn must be a no-op, got %v", err)
	}
}

// TestOrdinarySave_RefusedByLivePessimisticHold is entity's version of
// bal's and fsm's load-bearing cross-path proof: an ordinary (non-dxp)
// Save call must be refused while a live PESSIMISTIC dxp claim holds
// the entity.
func TestOrdinarySave_RefusedByLivePessimisticHold(t *testing.T) {
	store := testEntityStore(t)
	id := mustCreateEntity(t, store, "widget", map[string]interface{}{"name": "gadget"})
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)
	ctx := context.Background()

	if _, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		EntityUpdateParams{Entity: "widget", ID: id, Data: map[string]interface{}{"name": "gizmo"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	_, err := store.Save(ctx, "widget", id, map[string]interface{}{"name": "sneaky"})
	if err != ErrConflict {
		t.Fatalf("expected ordinary Save refused by the live pessimistic hold (ErrConflict), got %v", err)
	}
}

// TestOrdinarySave_UnaffectedByOptimisticHold confirms the flip side:
// an optimistic claim must not block ordinary traffic.
func TestOrdinarySave_UnaffectedByOptimisticHold(t *testing.T) {
	store := testEntityStore(t)
	id := mustCreateEntity(t, store, "widget", map[string]interface{}{"name": "gadget"})
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)
	ctx := context.Background()

	if _, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		EntityUpdateParams{Entity: "widget", ID: id, Data: map[string]interface{}{"name": "gizmo"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Optimistic); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	if _, err := store.Save(ctx, "widget", id, map[string]interface{}{"name": "fine"}); err != nil {
		t.Fatalf("ordinary Save should proceed past an optimistic hold, got %v", err)
	}
}

// ─── EntityAppendParams (T-84) ──────────────────────────────────────────────

func TestEntityAdapter_Reserve_AppendExplicitID_Success(t *testing.T) {
	store := testEntityStore(t)
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)

	id := 42
	cl, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		EntityAppendParams{Entity: "widget", ID: &id, Data: map[string]interface{}{"name": "gadget"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if cl.Primitive != "entity" || cl.Resource != "entity:widget:42" {
		t.Fatalf("unexpected claim: %+v", cl)
	}
}

func TestEntityAdapter_Reserve_AppendAutoID_Success(t *testing.T) {
	store := testEntityStore(t)
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)

	cl, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		EntityAppendParams{Entity: "widget", Data: map[string]interface{}{"name": "gadget"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if cl.Resource != "entity:widget:~append:txn-1:p1" {
		t.Fatalf("expected a (txn, participantID)-scoped resource key, got %q", cl.Resource)
	}
}

func TestEntityAdapter_Reserve_AppendExplicitID_RefusedWhenAlreadyExists(t *testing.T) {
	store := testEntityStore(t)
	id := mustCreateEntity(t, store, "widget", map[string]interface{}{"name": "gadget"})
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)

	_, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		EntityAppendParams{Entity: "widget", ID: &id, Data: map[string]interface{}{"name": "dup"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

// TestEntityAdapter_Reserve_UpdateAndAppendShareResourceNamespace proves
// the deliberate design choice: an update and an explicit-id create on
// the SAME id correctly contend for one claim, not two independent ones
// that would let both proceed and race each other into Execute.
func TestEntityAdapter_Reserve_UpdateAndAppendShareResourceNamespace(t *testing.T) {
	store := testEntityStore(t)
	id := mustCreateEntity(t, store, "widget", map[string]interface{}{"name": "gadget"})
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)

	if _, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		EntityUpdateParams{Entity: "widget", ID: id, Data: map[string]interface{}{"name": "gizmo"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("first reserve (update): %v", err)
	}

	// A second, DIFFERENT id's append should not be affected...
	otherID := id + 1000
	if _, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		EntityAppendParams{Entity: "widget", ID: &otherID, Data: map[string]interface{}{"name": "unrelated"}},
		"txn-2", "p1", entityFutureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("unrelated append should not be blocked: %v", err)
	}

	// ...but an append attempting the SAME id the update already holds
	// must be refused, proving the shared resource-key namespace works.
	_, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		EntityAppendParams{Entity: "widget", ID: &id, Data: map[string]interface{}{"name": "collide"}},
		"txn-3", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err == nil {
		t.Fatal("expected the append to be refused: the update already holds this exact resource")
	}
}

func TestEntityAdapter_Validate_AppendExplicitID_CatchesCompetitorCreate(t *testing.T) {
	store := testEntityStore(t)
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)
	ctx := context.Background()

	id := 99
	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		EntityAppendParams{Entity: "widget", ID: &id, Data: map[string]interface{}{"name": "gadget"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	// A competitor creates the same id via the ordinary path, after this
	// reservation was granted. store.Create always auto-generates (it
	// ignores any "id" in its data map -- checked directly, not
	// assumed), so simulating an explicit-id competitor create means
	// calling createInTx directly, same-package, the way the /commit
	// HTTP path's CommitAppend{ID: ...} shape actually would.
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	competitorTx, err := store.DB().BeginTx(ctx2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.createInTx(ctx2, competitorTx, CommitAppend{
		Entity: "widget", ID: &id, Data: map[string]interface{}{"name": "beat you to it"},
	}); err != nil {
		_ = competitorTx.Rollback()
		t.Fatalf("competitor createInTx: %v", err)
	}
	if err := competitorTx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := a.Validate(ctx, cl); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected Validate to catch the competitor's create with ErrAlreadyExists, got %v", err)
	}
}

func TestEntityAdapter_Execute_AppendExplicitID_CreatesRow(t *testing.T) {
	store := testEntityStore(t)
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)
	ctx := context.Background()

	id := 7
	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		EntityAppendParams{Entity: "widget", ID: &id, Data: map[string]interface{}{"name": "gadget"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
		_ = tx.Rollback()
		t.Fatalf("Execute: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, "widget", id)
	if err != nil {
		t.Fatalf("Get after Execute+commit: %v", err)
	}
	if got["name"] != "gadget" {
		t.Fatalf("expected name=gadget, got %+v", got)
	}
}

func TestEntityAdapter_Execute_AppendAutoID_AllocatesAndCreatesRow(t *testing.T) {
	store := testEntityStore(t)
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)
	ctx := context.Background()

	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		EntityAppendParams{Entity: "widget", Data: map[string]interface{}{"name": "gadget"}},
		"txn-1", "p1", entityFutureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
		_ = tx.Rollback()
		t.Fatalf("Execute: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// The allocated id was never surfaced to the test (Execute's current
	// signature has no Result to carry it — see dxp-coordinator-design.md
	// §10) -- confirm indirectly: exactly one widget now exists.
	all, err := store.List(ctx, "widget")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0]["name"] != "gadget" {
		t.Fatalf("expected exactly one created widget, got %+v", all)
	}
}

// TestEntityAdapter_Reserve_ConcurrentAutoIDsNeverCollide is the direct
// test of reserveAppend's own claim: two concurrent auto-id creates get
// two different ids by the sequence's own construction, not by anything
// this adapter arranges -- proven by actually running both Executes and
// checking two distinct rows land, not by re-asserting the reasoning.
func TestEntityAdapter_Reserve_ConcurrentAutoIDsNeverCollide(t *testing.T) {
	store := testEntityStore(t)
	cache := dxp.NewMemCache()
	a := NewEntityAdapter(store, cache)
	ctx := context.Background()

	for _, txn := range []string{"txn-1", "txn-2"} {
		cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
			EntityAppendParams{Entity: "widget", Data: map[string]interface{}{"name": txn}},
			txn, "p1", entityFutureDeadline(), dxp.Pessimistic)
		if err != nil {
			t.Fatalf("Reserve %s: %v", txn, err)
		}
		tx, err := store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
			_ = tx.Rollback()
			t.Fatalf("Execute %s: %v", txn, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	all, err := store.List(ctx, "widget")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 distinct widgets, got %d: %+v", len(all), all)
	}
}
