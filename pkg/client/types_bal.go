// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// types_bal.go — wire types for the /api/v2/bal/* surface (@B09).
// Shapes mirror pkg/server/v2_bal_handlers.go byte-for-byte per the
// Stage 2 convention. Amounts cross the wire as decimal STRINGS only
// (@B04) — never a JSON number — at the account's declared scale; the
// server refuses a numeric `amount` outright rather than silently
// floating it, and this client never constructs one either.

import "time"

// BalDefineRequest defines an account. Floor and Ceiling are decimal
// strings at Scale (e.g. "-1000000" for scale 0, "10.50" for scale
// 2) — omit either for no bound. Postable defaults to true server-side
// when nil; set false only for summary/hierarchy accounts that never
// receive a direct transfer (XOLU-BAL005 refuses one that tries).
type BalDefineRequest struct {
	AccountID string  `json:"account_id"`
	Unit      string  `json:"unit"`
	Scale     uint8   `json:"scale"`
	Floor     *string `json:"floor,omitempty"`
	Ceiling   *string `json:"ceiling,omitempty"`
	Postable  *bool   `json:"postable,omitempty"`
}

// BalAccount is the response of BalDefine.
type BalAccount struct {
	AccountID string `json:"account_id"`
	Unit      string `json:"unit"`
	Scale     uint8  `json:"scale"`
	Floor     string `json:"floor"`
	Postable  bool   `json:"postable"`
}

// BalTransferRequest moves Amount (a decimal string at Scale) from
// From to To. TransferID is the client idempotency key — a UUID is
// generated server-side when omitted, but supplying one lets a caller
// safely retry a request whose response was lost. At defaults to now
// when omitted; a strictly-earlier At than an account's latest entry
// is refused (XOLU-BAL006) unless that account's policy is backdated
// (not settable over this API today — accounts are always created
// append_only).
type BalTransferRequest struct {
	TransferID string `json:"transfer_id,omitempty"`
	From       string `json:"from"`
	To         string `json:"to"`
	Amount     string `json:"amount"`
	Scale      uint8  `json:"scale"`
	Memo       string `json:"memo,omitempty"`
	At         string `json:"at,omitempty"` // RFC3339; empty means "now" server-side
}

// BalTransferResult is the response of BalTransfer.
type BalTransferResult struct {
	TransferID string `json:"transfer_id"`
	From       string `json:"from"`
	To         string `json:"to"`
	Amount     string `json:"amount"`
}

// BalBalanceResult is the response of BalBalance. Value is the
// decimal-string rendering at the account's own declared scale; Minor
// is the same quantity as exact int64 minor units, useful when a
// caller wants to avoid decimal parsing entirely.
type BalBalanceResult struct {
	AccountID string `json:"account_id"`
	Value     string `json:"value"`
	Minor     int64  `json:"minor"`
	Version   int64  `json:"version"`
}

// BalEntry is one journal entry as BalEntries returns it.
type BalEntry struct {
	EntryID         int64     `json:"entry_id"`
	TransferID      string    `json:"transfer_id"`
	Amount          string    `json:"amount"`
	PreviousBalance string    `json:"previous_balance"`
	CurrentBalance  string    `json:"current_balance"`
	Version         int64     `json:"version"`
	Memo            string    `json:"memo,omitempty"`
	At              time.Time `json:"at"`
}

// BalEntriesResult is the response of BalEntries. The server currently
// returns at most the account's 100 most recent entries — there is no
// pagination parameter on the wire today; a caller needing the full
// history should read it before any retention policy prunes it
// (bal.Store.PruneJournal, item 16).
type BalEntriesResult struct {
	AccountID string     `json:"account_id"`
	Entries   []BalEntry `json:"entries"`
}

// BalAsOfResult is the response of BalAsOf. Source is always "rollup"
// today — the derived, fast-path plane; the exact/audit path (the
// journal chain) has no HTTP surface and is verified equal to this one
// by the rollup rebuild oracle, not exposed as a caller-selectable
// option.
type BalAsOfResult struct {
	AccountID string    `json:"account_id"`
	At        time.Time `json:"at"`
	Value     string    `json:"value"`
	Minor     int64     `json:"minor"`
	Source    string    `json:"source"`
}

// BalCloseResult is the response of BalClose. Sealing is tenant-wide
// (the whole account-set's seal frontier advances together, per T-64)
// — AccountsClosed is how many postable accounts received a closing
// checkpoint as part of this call, not a count the caller selects.
type BalCloseResult struct {
	SealedThrough  time.Time `json:"sealed_through"`
	AccountsClosed int       `json:"accounts_closed"`
}
