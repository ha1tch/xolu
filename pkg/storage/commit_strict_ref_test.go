// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

// commit_strict_ref_test.go
//
// XOT238: a /commit fsm_walk with strict_ref only walks the machine whose ref is
// the entity the commit updates or deletes.

import (
	"context"
	"errors"
	"testing"
)

func setMachineRef(t *testing.T, store *SQLiteStore, ref interface{}) {
	t.Helper()
	if _, err := store.DB().Exec(`UPDATE fsm_machines SET ref = ? WHERE tenant_id = 0 AND id = 1`, ref); err != nil {
		t.Fatal(err)
	}
}

func machineState(t *testing.T, store *SQLiteStore) string {
	t.Helper()
	var s string
	if err := store.DB().QueryRow(`SELECT state FROM fsm_machines WHERE tenant_id = 0 AND id = 1`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func walkCommit(strict bool, entity string, id int) CommitRequest {
	return CommitRequest{
		Update:  CommitUpdate{Entity: entity, ID: id, Data: map[string]interface{}{"n": 1}},
		FsmWalk: &CommitFsmWalk{Machine: 1, Input: "start", Payload: map[string]interface{}{"ok": "yes"}, StrictRef: strict},
	}
}

func TestCommitStrictRef(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		ref       interface{} // the machine's ref column
		strict    bool
		wantErr   bool
		wantState string
	}{
		{"strict, the machine is bound to this entity", "asset:5", true, false, "running"},
		{"strict, the machine is bound to another entity", "asset:6", true, true, "idle"},
		{"strict, the machine is bound to another type", "order:5", true, true, "idle"},
		{"strict, the machine has no ref", nil, true, true, "idle"},
		{"not strict, the machine is bound to another entity: walked as before", "asset:6", false, false, "running"},
		{"not strict, the machine has no ref: walked as before", nil, false, false, "running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := testFsmStore(t)
			testFsmMachine(t, store, "idle")
			setMachineRef(t, store, tc.ref)

			_, err := store.Commit(ctx, walkCommit(tc.strict, "asset", 5))
			if tc.wantErr {
				var we *FsmWalkError
				if !errors.As(err, &we) || we.Code != "XOLU-FSM014" {
					t.Fatalf("err = %v, want a FsmWalkError XOLU-FSM014", err)
				}
				if _, gerr := store.Get(ctx, "asset", 5); gerr == nil {
					t.Fatal("the update of a refused commit landed")
				}
			} else if err != nil {
				t.Fatalf("Commit: %v", err)
			}
			if got := machineState(t, store); got != tc.wantState {
				t.Fatalf("machine state = %q, want %q", got, tc.wantState)
			}
		})
	}
}

// A delete commit is checked against the entity it deletes.
func TestCommitStrictRef_Delete(t *testing.T) {
	ctx := context.Background()
	store := testFsmStore(t)
	testFsmMachine(t, store, "idle")
	if _, err := store.Save(ctx, "asset", 7, map[string]interface{}{"n": 0}); err != nil {
		t.Fatal(err)
	}
	req := CommitRequest{
		Delete:  &CommitDelete{Entity: "asset", ID: 7},
		FsmWalk: &CommitFsmWalk{Machine: 1, Input: "start", Payload: map[string]interface{}{"ok": "yes"}, StrictRef: true},
	}
	setMachineRef(t, store, "asset:8")
	var we *FsmWalkError
	if _, err := store.Commit(ctx, req); !errors.As(err, &we) || we.Code != "XOLU-FSM014" {
		t.Fatalf("a machine bound to asset:8 walked in a delete of asset:7: err = %v", err)
	}
	if _, err := store.Get(ctx, "asset", 7); err != nil {
		t.Fatalf("the entity was deleted by a refused commit: %v", err)
	}
	setMachineRef(t, store, "asset:7")
	if _, err := store.Commit(ctx, req); err != nil {
		t.Fatalf("a machine bound to the deleted entity: %v", err)
	}
	if _, err := store.Get(ctx, "asset", 7); err == nil {
		t.Fatal("the entity still exists after the committed delete")
	}
}

// A machine that does not exist is reported by the walk as before, not as a ref mismatch.
func TestCommitStrictRef_MissingMachineIsStillAWalkError(t *testing.T) {
	store := testFsmStore(t)
	req := walkCommit(true, "asset", 5)
	req.FsmWalk.Machine = 99
	var we *FsmWalkError
	if _, err := store.Commit(context.Background(), req); !errors.As(err, &we) || we.Code == "XOLU-FSM014" {
		t.Fatalf("err = %v, want the walk's own machine-not-found error", err)
	}
}
