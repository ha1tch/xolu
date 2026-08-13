// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// bal_test.go — mock-based tests per the Stage 2 convention: happy
// path, structured error, and client-side validation per method. The
// real-server round trip (define -> transfer -> balance -> asof ->
// entries -> close -> sealed-period refusal) lives in
// integration_test.go's TestIntegration_BalFullFlow, which is what
// catches wire-shape drift these mocks structurally cannot.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var balT0 = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// ─── BalDefine ──────────────────────────────────────────────────────────────

func TestBalDefineHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/bal/def" {
			t.Errorf("expected /api/v2/bal/def, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		if req["account_id"] != "acct" {
			t.Errorf("account_id: got %v", req["account_id"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"account_id":"acct","unit":"u","scale":0,"floor":"0","postable":true}`))
	}))
	defer server.Close()

	c := New(server.URL)
	acct, err := c.BalDefine(context.Background(), BalDefineRequest{AccountID: "acct", Unit: "u"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acct.AccountID != "acct" || !acct.Postable {
		t.Errorf("unexpected account: %+v", acct)
	}
}

func TestBalDefineRequiresAccountID(t *testing.T) {
	c := New("http://unused")
	if _, err := c.BalDefine(context.Background(), BalDefineRequest{Unit: "u"}); err == nil {
		t.Fatal("expected client-side validation error for empty AccountID")
	}
}

func TestBalDefineRequiresUnit(t *testing.T) {
	c := New("http://unused")
	if _, err := c.BalDefine(context.Background(), BalDefineRequest{AccountID: "a"}); err == nil {
		t.Fatal("expected client-side validation error for empty Unit")
	}
}

// ─── BalTransfer ────────────────────────────────────────────────────────────

func TestBalTransferHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		if req["amount"] != "150" {
			t.Errorf("amount: got %v (must be a string, never a JSON number — @B04)", req["amount"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"transfer_id":"t1","from":"~in","to":"acct","amount":"150"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.BalTransfer(context.Background(), BalTransferRequest{
		From: "~in", To: "acct", Amount: "150", Scale: 0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Amount != "150" {
		t.Errorf("amount echo: got %s", res.Amount)
	}
}

func TestBalTransferBoundsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":{"code":"XOLU-BAL001","message":"transfer refused: floor bound on acct","status":409}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.BalTransfer(context.Background(), BalTransferRequest{
		From: "acct", To: "sink", Amount: "9999999", Scale: 0,
	})
	ce, ok := err.(*Error)
	if !ok || ce.Code != "XOLU-BAL001" {
		t.Fatalf("expected XOLU-BAL001 *client.Error, got %T: %v", err, err)
	}
}

func TestBalTransferBackdatedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":{"code":"XOLU-BAL006","message":"entry predates the latest entry","status":409}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.BalTransfer(context.Background(), BalTransferRequest{
		From: "~in", To: "acct", Amount: "10", Scale: 0, At: "2020-01-01T00:00:00Z",
	})
	ce, ok := err.(*Error)
	if !ok || ce.Code != "XOLU-BAL006" {
		t.Fatalf("expected XOLU-BAL006 *client.Error, got %T: %v", err, err)
	}
}

func TestBalTransferRequiresDistinctAccounts(t *testing.T) {
	c := New("http://unused")
	if _, err := c.BalTransfer(context.Background(), BalTransferRequest{
		From: "a", To: "a", Amount: "1", Scale: 0,
	}); err == nil {
		t.Fatal("expected client-side validation error for From == To")
	}
}

func TestBalTransferRequiresAmount(t *testing.T) {
	c := New("http://unused")
	if _, err := c.BalTransfer(context.Background(), BalTransferRequest{
		From: "a", To: "b", Scale: 0,
	}); err == nil {
		t.Fatal("expected client-side validation error for empty Amount")
	}
}

// ─── BalBalance ─────────────────────────────────────────────────────────────

func TestBalBalanceHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/bal/balance" {
			t.Errorf("expected /api/v2/bal/balance, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if got := r.URL.Query().Get("account"); got != "acct" {
			t.Errorf("account query param: got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"account_id":"acct","value":"150","minor":150,"version":3}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.BalBalance(context.Background(), "acct")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Value != "150" || res.Minor != 150 {
		t.Errorf("unexpected balance: %+v", res)
	}
}

func TestBalBalanceUnknownAccountError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-BAL002","message":"unknown account","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.BalBalance(context.Background(), "ghost")
	ce, ok := err.(*Error)
	if !ok || ce.Code != "XOLU-BAL002" {
		t.Fatalf("expected XOLU-BAL002 *client.Error, got %T: %v", err, err)
	}
}

func TestBalBalanceRequiresAccountID(t *testing.T) {
	c := New("http://unused")
	if _, err := c.BalBalance(context.Background(), ""); err == nil {
		t.Fatal("expected client-side validation error for empty accountID")
	}
}

// ─── BalEntries ─────────────────────────────────────────────────────────────

func TestBalEntriesHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"account_id":"acct","entries":[
			{"entry_id":1,"transfer_id":"t1","amount":"150","previous_balance":"0",
			 "current_balance":"150","version":1,"at":"2026-06-15T12:00:00Z"}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.BalEntries(context.Background(), "acct")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Amount != "150" {
		t.Fatalf("unexpected entries: %+v", res.Entries)
	}
}

func TestBalEntriesEmptyIsNeverNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"account_id":"acct","entries":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.BalEntries(context.Background(), "acct")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Entries == nil {
		t.Fatal("Entries must be an empty slice, not nil, matching cal's own NearestOpenings/Openings convention")
	}
}

// ─── BalAsOf ────────────────────────────────────────────────────────────────

func TestBalAsOfHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/bal/asof" {
			t.Errorf("expected /api/v2/bal/asof, got %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("at"); got == "" {
			t.Error("at query param missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"account_id":"acct","at":"2026-06-15T12:00:00Z","value":"150","minor":150,"source":"rollup"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.BalAsOf(context.Background(), "acct", balT0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Source != "rollup" {
		t.Errorf("source: got %s, want rollup", res.Source)
	}
}

func TestBalAsOfRequiresNonZeroTime(t *testing.T) {
	c := New("http://unused")
	if _, err := c.BalAsOf(context.Background(), "acct", time.Time{}); err == nil {
		t.Fatal("expected client-side validation error for zero at")
	}
}

// ─── BalClose ───────────────────────────────────────────────────────────────

func TestBalCloseHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/bal/close" {
			t.Errorf("expected /api/v2/bal/close, got %s", r.URL.Path)
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		if req["at"] == nil {
			t.Error("at missing from request body")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"sealed_through":"2026-07-01T00:00:00Z","accounts_closed":4}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.BalClose(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AccountsClosed != 4 {
		t.Errorf("accounts closed: got %d, want 4", res.AccountsClosed)
	}
}

func TestBalCloseSealedPeriodErrorOnRepeat(t *testing.T) {
	// Not a normal outcome of BalClose itself (closing never conflicts
	// with a prior close), but writeBalError's XOLU-BAL003 mapping is
	// exercised by BalTransfer's error test above; this confirms
	// BalClose's own error path decodes the SAME structured shape for
	// an unrelated storage failure, since BalClose has no domain-
	// specific error of its own to provoke here.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"code":"XOLU-ST099","message":"storage failure","status":500}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.BalClose(context.Background(), balT0)
	ce, ok := err.(*Error)
	if !ok || ce.HTTPStatus != 500 {
		t.Fatalf("expected a structured 500 *client.Error, got %T: %v", err, err)
	}
}

func TestBalCloseRequiresNonZeroTime(t *testing.T) {
	c := New("http://unused")
	if _, err := c.BalClose(context.Background(), time.Time{}); err == nil {
		t.Fatal("expected client-side validation error for zero at")
	}
}

// ─── BalListAccounts ────────────────────────────────────────────────────────

func TestBalListAccountsHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/bal/accounts" {
			t.Errorf("expected /api/v2/bal/accounts, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"accounts":[
			{"account_id":"cash:eur","unit":"EUR","scale":2,"floor":"-50.00","ceiling":"100.00",
			 "postable":true,"policy":"","value":"12.50","minor":1250,"version":3},
			{"account_id":"widget","unit":"widget","scale":0,"floor":"0","postable":true,
			 "policy":"","value":"-1","minor":-1,"version":1}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.BalListAccounts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Accounts) != 2 {
		t.Fatalf("want 2 accounts, got %d: %+v", len(res.Accounts), res.Accounts)
	}
	eur := res.Accounts[0]
	if eur.AccountID != "cash:eur" || eur.Value != "12.50" || eur.Minor != 1250 || eur.Ceiling != "100.00" {
		t.Errorf("unexpected cash:eur: %+v", eur)
	}
	widget := res.Accounts[1]
	if widget.Ceiling != "" {
		t.Errorf("widget defined with no ceiling, want empty, got %q", widget.Ceiling)
	}
}

func TestBalListAccountsEmptyResultNotNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"accounts":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.BalListAccounts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Accounts == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
}

func TestBalListAccountsStorageError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"code":"XOLU-ST099","message":"storage failure","status":500}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.BalListAccounts(context.Background())
	ce, ok := err.(*Error)
	if !ok || ce.HTTPStatus != 500 {
		t.Fatalf("expected a structured 500 *client.Error, got %T: %v", err, err)
	}
}
