// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_dxp_def_handlers_test.go tests validateDxpDef, parsePhaseTTL,
// and allocDXPID directly — internal package server, since all three
// are unexported (matching v2_dxp_def_handlers.go's own file, not the
// external server_test package the HTTP-level suite uses).

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/ha1tch/xolu/pkg/bal"
	"github.com/ha1tch/xolu/pkg/cal"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// hotelDef is the doctrine's own worked example (dxp-composed-
// commitment.md §5a), used verbatim as the canonical valid-def test
// case rather than an invented one.
func hotelDef() *dxpDefSpec {
	return &dxpDefSpec{
		Name:    "hotel_reserve",
		Pattern: "3ps",
		Participants: []dxpParticipantSpec{
			{ID: "room", Primitive: "cal", Op: "book"},
			{ID: "payment", Primitive: "bal", Op: "transfer"},
			{ID: "booking", Primitive: "fsm", Op: "transition"},
		},
		PhaseTTL: dxpPhaseTTLSpec{Reserve: "PT90S"},
	}
}

func TestValidateDxpDef_HotelExample_Succeeds(t *testing.T) {
	analysis, err := validateDxpDef(hotelDef())
	if err != nil {
		t.Fatalf("validateDxpDef: %v", err)
	}
	if !analysis.CollapseEligible {
		t.Error("expected CollapseEligible: single-tenant is trivially true for v1")
	}
	if !analysis.EngineHomogeneous {
		t.Error("expected EngineHomogeneous: cal/bal/fsm are all SQL-backed")
	}
}

func TestValidateDxpDef_MissingPattern_Refused(t *testing.T) {
	spec := hotelDef()
	spec.Pattern = ""
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func TestValidateDxpDef_UnsupportedPattern_Refused(t *testing.T) {
	spec := hotelDef()
	spec.Pattern = "2ps"
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func TestValidateDxpDef_NoParticipants_Refused(t *testing.T) {
	spec := hotelDef()
	spec.Participants = nil
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func TestValidateDxpDef_EmptyParticipantID_Refused(t *testing.T) {
	spec := hotelDef()
	spec.Participants[0].ID = ""
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func TestValidateDxpDef_DuplicateParticipantID_Refused(t *testing.T) {
	spec := hotelDef()
	spec.Participants[1].ID = spec.Participants[0].ID
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func TestValidateDxpDef_UnknownPrimitive_Refused(t *testing.T) {
	spec := hotelDef()
	spec.Participants[0].Primitive = "ts" // real primitive, but no adapter yet (T-86)
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func TestValidateDxpDef_UnsupportedOp_Refused(t *testing.T) {
	spec := hotelDef()
	spec.Participants[0].Op = "delete" // cal has no such op
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func TestValidateDxpDef_EntityBothOpsAccepted(t *testing.T) {
	for _, op := range []string{"create", "update"} {
		spec := hotelDef()
		spec.Participants = append(spec.Participants, dxpParticipantSpec{
			ID: "order", Primitive: "entity", Op: op,
		})
		if _, err := validateDxpDef(spec); err != nil {
			t.Errorf("entity op %q: unexpected refusal: %v", op, err)
		}
	}
}

func TestValidateDxpDef_MissingPhaseTTL_Refused(t *testing.T) {
	spec := hotelDef()
	spec.PhaseTTL.Reserve = ""
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func TestValidateDxpDef_InvalidPhaseTTL_Refused(t *testing.T) {
	spec := hotelDef()
	spec.PhaseTTL.Reserve = "2 minutes" // not ISO 8601
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func assertDxpValidationError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a validation error, got none")
	}
	ve, ok := err.(*dxpValidationError)
	if !ok {
		t.Fatalf("expected *dxpValidationError, got %T: %v", err, err)
	}
	if ve.Code != "XOLU-DXP006" {
		t.Fatalf("expected XOLU-DXP006, got %s", ve.Code)
	}
}

// ─── parsePhaseTTL ──────────────────────────────────────────────────────────

func TestParsePhaseTTL_DoctrineExamples(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"PT2M", 120_000_000_000},
		{"PT90S", 90_000_000_000},
		{"PT1H", 3600_000_000_000},
		{"PT1H30M", 5400_000_000_000},
	}
	for _, c := range cases {
		got, err := parsePhaseTTL(c.in)
		if err != nil {
			t.Errorf("parsePhaseTTL(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parsePhaseTTL(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParsePhaseTTL_InvalidInputs(t *testing.T) {
	for _, in := range []string{"", "2M", "PT", "PTM", "PT2X", "PT-2M", "2 minutes"} {
		if _, err := parsePhaseTTL(in); err == nil {
			t.Errorf("parsePhaseTTL(%q): expected an error, got none", in)
		}
	}
}

// ─── allocDXPID ─────────────────────────────────────────────────────────────

func testDxpStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	dir, err := os.MkdirTemp("", "dxp-def-alloc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dbPath := dir + "/store.db"
	st, err := storage.NewSQLiteStore(dbPath, storage.SQLiteConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.InitV2Schema(context.Background()); err != nil {
		t.Fatalf("InitV2Schema: %v", err)
	}
	return st
}

func TestAllocDXPID_SequentialWithinKind(t *testing.T) {
	st := testDxpStore(t)
	ctx := context.Background()

	var ids []int64
	for i := 0; i < 3; i++ {
		tx, err := st.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		id, err := allocDXPID(ctx, tx, tenant.TenantID(0), "def")
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("allocDXPID: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for i, id := range ids {
		if id != int64(i+1) {
			t.Errorf("ids[%d] = %d, want %d", i, id, i+1)
		}
	}
}

func TestAllocDXPID_KindsAreIndependent(t *testing.T) {
	st := testDxpStore(t)
	ctx := context.Background()

	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defID, err := allocDXPID(ctx, tx, tenant.TenantID(0), "def")
	if err != nil {
		t.Fatal(err)
	}
	txnID, err := allocDXPID(ctx, tx, tenant.TenantID(0), "txn")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if defID != 1 || txnID != 1 {
		t.Fatalf("expected both kinds to start at 1 independently, got def=%d txn=%d", defID, txnID)
	}
}

// ─── bindings_schema / jsonplate ────────────────────────────────────────────

func TestValidateDxpDef_ValidBindingsSchemaAndJsonplateRefs_Accepted(t *testing.T) {
	spec := hotelDef()
	spec.BindingsSchema = map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"amount": map[string]interface{}{"type": "integer"}},
		"required":   []interface{}{"amount"},
	}
	spec.Participants[1].Params = map[string]interface{}{
		"from": "~in", "to": "acct", "amount": map[string]interface{}{"$ref": "amount"},
	}
	if _, err := validateDxpDef(spec); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
}

func TestValidateDxpDef_MalformedBindingsSchema_Refused(t *testing.T) {
	spec := hotelDef()
	// "type" must be a string per JSON Schema; this is structurally invalid.
	spec.BindingsSchema = map[string]interface{}{"type": 12345}
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func TestValidateDxpDef_MalformedJsonplateRef_Refused(t *testing.T) {
	spec := hotelDef()
	// A $ref object with more than one key is not a valid reference —
	// jsonplate.Validate must catch this at registration time.
	spec.Participants[0].Params = map[string]interface{}{
		"span": map[string]interface{}{"$ref": "delivery", "extra": "not allowed"},
	}
	_, err := validateDxpDef(spec)
	assertDxpValidationError(t, err)
}

func TestValidateDxpDef_NoBindingsSchema_StillValid(t *testing.T) {
	// A def with no bindings requirement at all — bindings_schema
	// absent entirely — must remain valid, matching the doctrine's
	// own worked examples, none of which declare one.
	spec := hotelDef()
	if _, err := validateDxpDef(spec); err != nil {
		t.Fatalf("unexpected refusal for a def with no bindings_schema: %v", err)
	}
}

// ─── markDxpTxnTerminal ─────────────────────────────────────────────────────

// insertTestDxpTxn creates a minimal, valid, 'active' dxp_txn row
// directly (not through the HTTP handler — this file's own concern is
// the transition primitive, not instantiation, which v2_dxp_def_
// handlers_http_test.go already covers), returning its id.
func insertTestDxpTxn(t *testing.T, st *storage.SQLiteStore, tenantID tenant.TenantID) int64 {
	t.Helper()
	ctx := context.Background()
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := allocDXPID(ctx, tx, tenantID, "txn")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO dxp_txn (tenant_id, id, dxp_def_id, dxp_def_name, snapshot_json, status, committed_through, deadline_ns)
		VALUES (?, ?, 1, 'test_def', '{}', 'active', 0, 0)`, tenantID, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return id
}

func dxpTxnStatus(t *testing.T, st *storage.SQLiteStore, tenantID tenant.TenantID, id int64) (string, int) {
	t.Helper()
	var status string
	var committedThrough int
	err := st.DB().QueryRow(`SELECT status, committed_through FROM dxp_txn WHERE tenant_id = ? AND id = ?`,
		tenantID, id).Scan(&status, &committedThrough)
	if err != nil {
		t.Fatal(err)
	}
	return status, committedThrough
}

func TestMarkDxpTxnTerminal_EachRealTerminalState_Succeeds(t *testing.T) {
	for _, target := range []string{"committed", "released", "expired"} {
		t.Run(target, func(t *testing.T) {
			st := testDxpStore(t)
			id := insertTestDxpTxn(t, st, tenant.TenantID(0))
			ctx := context.Background()

			tx, err := st.DB().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			ok, err := markDxpTxnTerminal(ctx, tx, tenant.TenantID(0), id, target, 3)
			if err != nil {
				t.Fatalf("markDxpTxnTerminal: %v", err)
			}
			if !ok {
				t.Fatal("expected the transition to succeed on a fresh 'active' instance")
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}

			gotStatus, gotCommitted := dxpTxnStatus(t, st, tenant.TenantID(0), id)
			if gotStatus != target {
				t.Errorf("status = %q, want %q", gotStatus, target)
			}
			if gotCommitted != 3 {
				t.Errorf("committed_through = %d, want 3", gotCommitted)
			}
		})
	}
}

func TestMarkDxpTxnTerminal_InvalidStatus_Refused(t *testing.T) {
	st := testDxpStore(t)
	id := insertTestDxpTxn(t, st, tenant.TenantID(0))
	ctx := context.Background()

	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = markDxpTxnTerminal(ctx, tx, tenant.TenantID(0), id, "active", 0)
	if err == nil {
		t.Fatal("expected an error for a non-terminal target status")
	}
	_, err = markDxpTxnTerminal(ctx, tx, tenant.TenantID(0), id, "torn", 0)
	if err == nil {
		t.Fatal("expected an error for a status that was deliberately never made a real state (§6: torn falls into ordinary expired handling)")
	}
}

// TestMarkDxpTxnTerminal_AlreadyTerminal_SecondTransitionRefused proves
// outcome uniqueness directly: once an instance reaches a terminal
// state, no further transition — including to a DIFFERENT terminal
// state — succeeds.
func TestMarkDxpTxnTerminal_AlreadyTerminal_SecondTransitionRefused(t *testing.T) {
	st := testDxpStore(t)
	id := insertTestDxpTxn(t, st, tenant.TenantID(0))
	ctx := context.Background()

	tx1, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := markDxpTxnTerminal(ctx, tx1, tenant.TenantID(0), id, "committed", 3)
	if err != nil || !ok {
		t.Fatalf("first transition should succeed: ok=%v err=%v", ok, err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}

	tx2, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ok2, err := markDxpTxnTerminal(ctx, tx2, tenant.TenantID(0), id, "expired", 3)
	if err != nil {
		_ = tx2.Rollback()
		t.Fatalf("second transition attempt should not itself error: %v", err)
	}
	if ok2 {
		_ = tx2.Rollback()
		t.Fatal("expected the second transition to be refused — the instance is already committed")
	}
	// Roll back explicitly, before reading — tx2 already attempted a
	// write (even though it matched zero rows), so it's still holding
	// SQLite's write lock at this point. A read from a separate pool
	// connection (dxpTxnStatus, below) would otherwise contend with
	// that lock rather than the deferred rollback releasing it in
	// time — found by actually hitting the hang, not anticipated.
	if err := tx2.Rollback(); err != nil {
		t.Fatal(err)
	}

	gotStatus, _ := dxpTxnStatus(t, st, tenant.TenantID(0), id)
	if gotStatus != "committed" {
		t.Errorf("status should still be committed after the refused second attempt, got %q", gotStatus)
	}
}

// TestMarkDxpTxnTerminal_ConcurrentTransitions_ExactlyOneWins is the
// genuine concurrency proof — actually running many simultaneous
// transition attempts against one instance, not trusting the SQL
// guard's theoretical correctness (this codebase's own T-34 lesson: a
// race that passed months of single-core testing, only found by
// actually running it).
func TestMarkDxpTxnTerminal_ConcurrentTransitions_ExactlyOneWins(t *testing.T) {
	st := testDxpStore(t)
	id := insertTestDxpTxn(t, st, tenant.TenantID(0))
	ctx := context.Background()

	const n = 20
	results := make([]bool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tx, err := st.DB().BeginTx(ctx, nil)
			if err != nil {
				errs[idx] = err
				return
			}
			ok, err := markDxpTxnTerminal(ctx, tx, tenant.TenantID(0), id, "committed", idx)
			if err != nil {
				_ = tx.Rollback()
				errs[idx] = err
				return
			}
			if ok {
				errs[idx] = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			results[idx] = ok
		}(i)
	}
	wg.Wait()

	wins := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("call %d: unexpected error: %v", i, errs[i])
		}
		if results[i] {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly 1 winning transition out of %d concurrent attempts, got %d", n, wins)
	}

	gotStatus, _ := dxpTxnStatus(t, st, tenant.TenantID(0), id)
	if gotStatus != "committed" {
		t.Errorf("final status should be committed, got %q", gotStatus)
	}
}

// ─── decodeDxpParticipantParams ─────────────────────────────────────────────

func TestDecodeDxpParticipantParams_Bal_DecimalString_Succeeds(t *testing.T) {
	op, err := decodeDxpParticipantParams("bal", "transfer",
		[]byte(`{"from":"~in","to":"acct","amount":"100.50","scale":2}`), tenant.TenantID(0))
	if err != nil {
		t.Fatalf("decodeDxpParticipantParams: %v", err)
	}
	tp, ok := op.(bal.TransferParams)
	if !ok {
		t.Fatalf("expected bal.TransferParams, got %T", op)
	}
	if tp.From != "~in" || tp.To != "acct" {
		t.Errorf("From/To not decoded correctly: %+v", tp)
	}
	if tp.Amount != 10050 {
		t.Errorf("Amount = %d, want 10050 (100.50 at scale 2)", tp.Amount)
	}
}

// TestDecodeDxpParticipantParams_Bal_JSONNumber_Refused is the direct
// proof that @B04's smuggling test survives the dxp path — a bare
// JSON number for amount, which a generic json.Unmarshal into an
// int64 field would have silently accepted, must be refused here
// exactly as handleBalTransfer already refuses it at the HTTP
// boundary.
func TestDecodeDxpParticipantParams_Bal_JSONNumber_Refused(t *testing.T) {
	_, err := decodeDxpParticipantParams("bal", "transfer",
		[]byte(`{"from":"~in","to":"acct","amount":100.50,"scale":2}`), tenant.TenantID(0))
	if err == nil {
		t.Fatal("expected a bare JSON number for amount to be refused (@B04)")
	}
}

func TestDecodeDxpParticipantParams_Bal_NoScale_DefaultsToZero(t *testing.T) {
	op, err := decodeDxpParticipantParams("bal", "transfer",
		[]byte(`{"from":"~in","to":"acct","amount":"150"}`), tenant.TenantID(0))
	if err != nil {
		t.Fatalf("decodeDxpParticipantParams: %v", err)
	}
	tp := op.(bal.TransferParams)
	if tp.Amount != 150 {
		t.Errorf("Amount = %d, want 150 (scale 0)", tp.Amount)
	}
}

func TestDecodeDxpParticipantParams_Cal_Succeeds(t *testing.T) {
	op, err := decodeDxpParticipantParams("cal", "book",
		[]byte(`{"calendar":"main","span":{"start":"2026-08-01T10:00:00Z","end":"2026-08-01T11:00:00Z"}}`),
		tenant.TenantID(0))
	if err != nil {
		t.Fatalf("decodeDxpParticipantParams: %v", err)
	}
	tp, ok := op.(cal.CalTransitionParams)
	if !ok {
		t.Fatalf("expected cal.CalTransitionParams, got %T", op)
	}
	if tp.CalendarID != "main" {
		t.Errorf("CalendarID = %q, want %q", tp.CalendarID, "main")
	}
}

// TestDecodeDxpParticipantParams_Fsm_TenantIDNeverTrustedFromJSON proves
// the security property directly: even if a participant's own params
// JSON tried to smuggle a tenant_id, it has no effect — TenantID is
// always the value explicitly passed in, from the instance's own real
// tenant, never from the def author's own params.
func TestDecodeDxpParticipantParams_Fsm_TenantIDNeverTrustedFromJSON(t *testing.T) {
	op, err := decodeDxpParticipantParams("fsm", "transition",
		[]byte(`{"tenant_id":99999,"machine_id":5,"input":"confirm"}`), tenant.TenantID(7))
	if err != nil {
		t.Fatalf("decodeDxpParticipantParams: %v", err)
	}
	tp, ok := op.(storage.FsmTransitionParams)
	if !ok {
		t.Fatalf("expected storage.FsmTransitionParams, got %T", op)
	}
	if tp.TenantID != tenant.TenantID(7) {
		t.Errorf("TenantID = %v, want 7 (the passed-in value, not the JSON's own smuggled 99999)", tp.TenantID)
	}
	if tp.MachineID != 5 {
		t.Errorf("MachineID = %d, want 5", tp.MachineID)
	}
}

func TestDecodeDxpParticipantParams_EntityUpdate_Succeeds(t *testing.T) {
	op, err := decodeDxpParticipantParams("entity", "update",
		[]byte(`{"entity":"widget","id":42,"data":{"name":"gadget"}}`), tenant.TenantID(0))
	if err != nil {
		t.Fatalf("decodeDxpParticipantParams: %v", err)
	}
	tp, ok := op.(storage.EntityUpdateParams)
	if !ok {
		t.Fatalf("expected storage.EntityUpdateParams, got %T", op)
	}
	if tp.Entity != "widget" || tp.ID != 42 {
		t.Errorf("unexpected decode: %+v", tp)
	}
}

func TestDecodeDxpParticipantParams_EntityCreate_Succeeds(t *testing.T) {
	op, err := decodeDxpParticipantParams("entity", "create",
		[]byte(`{"entity":"widget","data":{"name":"gadget"}}`), tenant.TenantID(0))
	if err != nil {
		t.Fatalf("decodeDxpParticipantParams: %v", err)
	}
	tp, ok := op.(storage.EntityAppendParams)
	if !ok {
		t.Fatalf("expected storage.EntityAppendParams, got %T", op)
	}
	if tp.Entity != "widget" {
		t.Errorf("unexpected decode: %+v", tp)
	}
}

func TestDecodeDxpParticipantParams_UnknownPrimitive_Refused(t *testing.T) {
	_, err := decodeDxpParticipantParams("nonexistent_primitive", "append", []byte(`{}`), tenant.TenantID(0))
	if err == nil {
		t.Fatal("expected an error for a primitive with no registered decoder")
	}
}

func TestDecodeDxpParticipantParams_EntityUnknownOp_Refused(t *testing.T) {
	_, err := decodeDxpParticipantParams("entity", "delete", []byte(`{}`), tenant.TenantID(0))
	if err == nil {
		t.Fatal("expected an error for an unknown entity op")
	}
}
