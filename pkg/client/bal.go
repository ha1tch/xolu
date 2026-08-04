// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// bal.go — the six bal methods against the /api/v2/bal/* surface
// (@B09). All writes are POST server-side (structured inputs); reads
// are GET with query parameters (account ids are namespaced strings
// containing '/', so they travel as a query parameter, never a path
// segment). Errors arrive as the XOLU-BAL001–006 family through the
// structured *Error type:
//
//	XOLU-BAL001  bounds (floor/ceiling refusal)     409
//	XOLU-BAL002  unknown account                    404
//	XOLU-BAL003  sealed period                       409
//	XOLU-BAL004  amount/scale invalid                400
//	XOLU-BAL005  not postable (summary account)      409
//	XOLU-BAL006  backdated (append_only account)      409

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// BalDefine creates or configures an account. Postable summary
// accounts (Postable: false) may parent postable leaves for
// hierarchical reporting but never receive a direct transfer.
//
// Hits POST /api/v2/.../bal/def. Returns *client.Error on non-2xx.
func (c *Client) BalDefine(ctx context.Context, req BalDefineRequest) (*BalAccount, error) {
	if req.AccountID == "" {
		return nil, fmt.Errorf("AccountID is required")
	}
	if req.Unit == "" {
		return nil, fmt.Errorf("unit is required")
	}
	var acct BalAccount
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/bal/def"), req, &acct); err != nil {
		return nil, err
	}
	return &acct, nil
}

// BalTransfer moves req.Amount (a decimal string at req.Scale) from
// req.From to req.To, both existing postable account ids. Supply
// req.TransferID for a caller-controlled idempotency key; a UUID is
// generated server-side when omitted.
//
// Hits POST /api/v2/.../bal/transfer. Returns *client.Error on
// non-2xx — notably XOLU-BAL001 when the transfer would breach a
// floor or ceiling, and XOLU-BAL006 for a backdated entry.
func (c *Client) BalTransfer(ctx context.Context, req BalTransferRequest) (*BalTransferResult, error) {
	if req.From == "" {
		return nil, fmt.Errorf("from is required")
	}
	if req.To == "" {
		return nil, fmt.Errorf("to is required")
	}
	if req.From == req.To {
		return nil, fmt.Errorf("from and to must be distinct accounts")
	}
	if req.Amount == "" {
		return nil, fmt.Errorf("amount is required (a decimal string, e.g. \"125\" or \"12.50\")")
	}
	var res BalTransferResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/bal/transfer"), req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// BalBalance returns accountID's current authoritative balance —
// the guard-plane value, always exact and always current, unlike
// BalAsOf's derived-plane fast path.
//
// Hits GET /api/v2/.../bal/balance?account={accountID}. Returns
// *client.Error on non-2xx — notably XOLU-BAL002 for an unknown
// account.
func (c *Client) BalBalance(ctx context.Context, accountID string) (*BalBalanceResult, error) {
	if accountID == "" {
		return nil, fmt.Errorf("accountID is required")
	}
	q := url.Values{}
	q.Set("account", accountID)
	u := c.buildURLv2("/bal/balance") + "?" + q.Encode()
	var res BalBalanceResult
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// BalEntries returns accountID's most recent journal entries (the
// server currently caps this at 100; see BalEntriesResult's doc).
//
// Hits GET /api/v2/.../bal/entries?account={accountID}. Returns
// *client.Error on non-2xx — notably XOLU-BAL002 for an unknown
// account.
func (c *Client) BalEntries(ctx context.Context, accountID string) (*BalEntriesResult, error) {
	if accountID == "" {
		return nil, fmt.Errorf("accountID is required")
	}
	q := url.Values{}
	q.Set("account", accountID)
	u := c.buildURLv2("/bal/entries") + "?" + q.Encode()
	var res BalEntriesResult
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	if res.Entries == nil {
		res.Entries = []BalEntry{}
	}
	return &res, nil
}

// BalAsOf returns accountID's balance as of instant at, read from the
// derived rollup plane (the fast path — nearest sealed checkpoint plus
// intervening buckets). Always agrees with the authoritative journal;
// the rollup rebuild oracle proves it server-side, not something a
// caller re-verifies per request.
//
// Hits GET /api/v2/.../bal/asof?account={accountID}&at={RFC3339}.
// Returns *client.Error on non-2xx — notably XOLU-BAL002 for an
// unknown account.
func (c *Client) BalAsOf(ctx context.Context, accountID string, at time.Time) (*BalAsOfResult, error) {
	if accountID == "" {
		return nil, fmt.Errorf("accountID is required")
	}
	if at.IsZero() {
		return nil, fmt.Errorf("at is required")
	}
	q := url.Values{}
	q.Set("account", accountID)
	q.Set("at", at.UTC().Format(time.RFC3339))
	u := c.buildURLv2("/bal/asof") + "?" + q.Encode()
	var res BalAsOfResult
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// BalClose seals the tenant's whole account-set as of instant at
// (item 16 §7): advances the seal frontier and writes a closing
// checkpoint for every postable account together. Sealing is
// tenant-wide, not per-account — there is no way to close only one
// account's period over this API, by design (bal-conservation-
// primitive.md §7's "account-set" is the whole tenant here).
//
// A closed period permanently refuses any future entry dated within
// it (XOLU-BAL003), regardless of an account's own backdated policy.
// This cannot be undone over the API or otherwise.
//
// Hits POST /api/v2/.../bal/close. Returns *client.Error on non-2xx.
func (c *Client) BalClose(ctx context.Context, at time.Time) (*BalCloseResult, error) {
	if at.IsZero() {
		return nil, fmt.Errorf("at is required")
	}
	body := map[string]interface{}{
		"at": at.UTC().Format(time.RFC3339),
	}
	var res BalCloseResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/bal/close"), body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
