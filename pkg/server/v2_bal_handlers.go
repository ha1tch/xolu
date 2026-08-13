// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_bal_handlers.go — the conservation primitive's HTTP surface (@B09).
//
// Amounts cross this boundary as STRINGS only (@B04): request bodies use
// json.RawMessage decoded as string; responses render via
// bal.FormatAmount. No float64 touches an amount, including in transit.

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ha1tch/xolu/pkg/bal"
	"github.com/ha1tch/xolu/pkg/chronicle"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/storage"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
)

// setupV2BalRoutes registers the bal surface (gated on BalEnabled).
func (s *Server) setupV2BalRoutes(r chi.Router) {
	r.Post("/bal/def", s.handleBalDefine)
	r.Post("/bal/transfer", s.handleBalTransfer)
	r.Get("/bal/accounts", s.handleBalListAccounts)
	// Account ids are namespaced strings containing '/', so they travel
	// as a query parameter, never a path segment.
	r.Get("/bal/balance", s.handleBalBalance)
	r.Get("/bal/entries", s.handleBalEntries)
	r.Get("/bal/asof", s.handleBalAsOf)
	r.Post("/bal/close", s.handleBalClose)
}

// balStore resolves the tenant-scoped bal store over the shared writer
// DB, initialising the tenant's bal tables once per server instance
// (idempotent DDL). The once-map lives ON the Server — a package
// global would leak initialisation state across instances (caught by
// the handler tests: server two skipped DDL because server one's Once
// had fired).
//
// T-62: the rollup plane moved to Pebble, which means a genuinely
// different lifecycle from the SQL DDL it replaced. bal.Store is built
// fresh on every request (st := bal.NewStore(...) below), but a
// *pebble.DB handle holds an exclusive on-disk lock and cannot be
// reopened per request the way "CREATE TABLE IF NOT EXISTS" tolerated
// being re-run harmlessly. So balRollup caches ONE bal.RollupPebble per
// tenant on the Server (opened inside the same Once that used to just
// run DDL), and every request — first or hundredth — re-attaches that
// cached handle to its own freshly-built Store via SetRollupPebble.
// Mirrors dxp.MemCache/SetClaimsCache's own long-lived-resource
// pattern exactly, for the same underlying reason.
func (s *Server) balStore(r *http.Request) (*bal.Store, error) {
	db, tenantID := s.metaDB(r) // same writer-db + tenant resolution meta uses
	st := bal.NewStore(db, tenantID)
	onceI, _ := s.balInit.LoadOrStore(tenantID, &sync.Once{})
	var initErr error
	onceI.(*sync.Once).Do(func() {
		if initErr = st.Init(r.Context()); initErr != nil {
			return
		}
		rp, err := bal.OpenRollupPebble(sl.TenantBalRollupDir(s.config.BaseDir, tenantID))
		if err != nil {
			initErr = err
			return
		}
		s.balRollup.Store(tenantID, rp)
		st.SetRollupPebble(rp)
		// Derived rollup plane (@B05). Its absence would not break
		// admission (no guard reads it), but the as-of surface needs it.
		if initErr = st.InitRollup(r.Context()); initErr != nil {
			return
		}
		if initErr = st.InitSeal(r.Context()); initErr != nil {
			return
		}
		sealer, err := bal.LoadSealer(r.Context(), db, tenantID)
		if err != nil {
			initErr = err
			return
		}
		s.balSealer.Store(tenantID, sealer)
		st.SetSealer(sealer)
	})
	if initErr != nil {
		// A failed init must not poison the Once for the instance.
		s.balInit.Delete(tenantID)
		s.balRollup.Delete(tenantID)
		s.balSealer.Delete(tenantID)
		return st, initErr
	}
	// Every request needs the tenant's cached long-lived handles
	// attached to ITS OWN Store instance — the Once body above only
	// ran (and only attached them) on whichever single request first
	// initialised this tenant.
	if rpI, ok := s.balRollup.Load(tenantID); ok {
		st.SetRollupPebble(rpI.(*bal.RollupPebble))
	}
	if sealerI, ok := s.balSealer.Load(tenantID); ok {
		st.SetSealer(sealerI.(*chronicle.Sealer))
	}
	return st, nil
}

// writeBalError maps bal's typed errors to the XOLU-BAL family.
func (s *Server) writeBalError(w http.ResponseWriter, err error) {
	var be *bal.BoundsError
	var ua *bal.UnknownAccountError
	var as *bal.AmountScaleError
	var np *bal.NotPostableError
	var sp *bal.SealedPeriodError
	var bd *bal.BackdatedError
	var da *bal.DuplicateAccountError
	switch {
	case errors.As(err, &be):
		s.writeError(w, http.StatusConflict, xoluerr.ErrBalBounds, err.Error())
	case errors.As(err, &ua):
		s.writeError(w, http.StatusNotFound, xoluerr.ErrBalUnknownAccount, err.Error())
	case errors.As(err, &as):
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrBalAmountScale, err.Error())
	case errors.As(err, &np):
		s.writeError(w, http.StatusConflict, xoluerr.ErrBalNotPostable, err.Error())
	case errors.As(err, &sp):
		s.writeError(w, http.StatusConflict, xoluerr.ErrBalSealedPeriod, err.Error())
	case errors.As(err, &bd):
		s.writeError(w, http.StatusConflict, xoluerr.ErrBalBackdated, err.Error())
	case errors.As(err, &da):
		s.writeError(w, http.StatusConflict, xoluerr.ErrBalDuplicateAccount, err.Error())
	default:
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
	}
}

type balDefineReq struct {
	AccountID string  `json:"account_id"`
	Unit      string  `json:"unit"`
	Scale     uint8   `json:"scale"`
	Floor     *string `json:"floor,omitempty"`   // decimal string at scale
	Ceiling   *string `json:"ceiling,omitempty"` // decimal string at scale
	Postable  *bool   `json:"postable,omitempty"`
}

func (s *Server) handleBalDefine(w http.ResponseWriter, r *http.Request) {
	var req balDefineReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	def := bal.AccountDef{ID: req.AccountID, Unit: req.Unit, Scale: req.Scale, Postable: true}
	if req.Postable != nil {
		def.Postable = *req.Postable
	}
	if req.Floor != nil {
		v, err := bal.ParseAmount(*req.Floor, req.Scale)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrBalAmountScale, "floor: "+err.Error())
			return
		}
		def.Floor = v
	}
	if req.Ceiling != nil {
		v, err := bal.ParseAmount(*req.Ceiling, req.Scale)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrBalAmountScale, "ceiling: "+err.Error())
			return
		}
		def.Ceiling = &v
	}
	st, err := s.balStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if _, err := st.DefineAccount(r.Context(), def); err != nil {
		s.writeBalError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"account_id": def.ID, "unit": def.Unit, "scale": def.Scale,
		"floor": bal.FormatAmount(def.Floor, def.Scale), "postable": def.Postable,
	})
}

// handleBalListAccounts returns every account defined on the tenant,
// each with its own definition and current balance -- xoluman's own
// XM-2 report: no way existed to enumerate a tenant's own accounts at
// all, every other bal endpoint (BalBalance, BalEntries, BalDefine)
// requires already knowing an account's own id.
//
//	GET /api/v2/.../bal/accounts
func (s *Server) handleBalListAccounts(w http.ResponseWriter, r *http.Request) {
	st, err := s.balStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	accts, err := st.ListAccounts(r.Context())
	if err != nil {
		s.writeBalError(w, err)
		return
	}

	type accountWire struct {
		AccountID string `json:"account_id"`
		Unit      string `json:"unit"`
		Scale     uint8  `json:"scale"`
		Floor     string `json:"floor"`
		Ceiling   string `json:"ceiling,omitempty"`
		Postable  bool   `json:"postable"`
		Policy    string `json:"policy"`
		Value     string `json:"value"`
		Minor     int64  `json:"minor"`
		Version   int64  `json:"version"`
	}
	out := make([]accountWire, 0, len(accts))
	for _, a := range accts {
		aw := accountWire{
			AccountID: a.AccountID,
			Unit:      a.Unit,
			Scale:     a.Scale,
			Floor:     bal.FormatAmount(a.Floor, a.Scale),
			Postable:  a.Postable,
			Policy:    a.Policy,
			Value:     bal.FormatAmount(a.Value, a.Scale),
			Minor:     a.Value,
			Version:   a.Version,
		}
		if a.Ceiling != nil {
			aw.Ceiling = bal.FormatAmount(*a.Ceiling, a.Scale)
		}
		out = append(out, aw)
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"accounts": out})
}

type balTransferReq struct {
	TransferID string `json:"transfer_id,omitempty"` // client idempotency key; generated when absent
	From       string `json:"from"`
	To         string `json:"to"`
	Amount     string `json:"amount"` // decimal string — never a JSON number (@B04)
	Scale      uint8  `json:"scale"`
	Memo       string `json:"memo,omitempty"`
	At         string `json:"at,omitempty"` // RFC3339; defaults to now
}

func (s *Server) handleBalTransfer(w http.ResponseWriter, r *http.Request) {
	// Decode with UseNumber so a numeric `amount` is detectable and
	// refused rather than silently floated (@B04's smuggling test).
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	var raw map[string]interface{}
	if err := dec.Decode(&raw); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	if _, isNum := raw["amount"].(json.Number); isNum {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrBalAmountScale,
			"amount must be a decimal STRING, not a JSON number (@B04)")
		return
	}
	b, _ := json.Marshal(raw)
	var req balTransferReq
	if err := json.Unmarshal(b, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	amount, err := bal.ParseAmount(req.Amount, req.Scale)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrBalAmountScale, err.Error())
		return
	}
	at := time.Now().UTC()
	if req.At != "" {
		t, err := time.Parse(time.RFC3339, req.At)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "at: "+err.Error())
			return
		}
		at = t
	}
	if req.TransferID == "" {
		req.TransferID = uuid.NewString()
	}
	st, err := s.balStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := st.Transfer(r.Context(), req.TransferID, req.From, req.To, amount, req.Memo, at); err != nil {
		s.writeBalError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"transfer_id": req.TransferID,
		"from":        req.From,
		"to":          req.To,
		"amount":      bal.FormatAmount(amount, req.Scale),
	})
}

func (s *Server) handleBalBalance(w http.ResponseWriter, r *http.Request) {
	account := r.URL.Query().Get("account")
	if account == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "account query parameter required")
		return
	}
	st, err := s.balStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	v, ver, err := st.Balance(r.Context(), account)
	if err != nil {
		s.writeBalError(w, err)
		return
	}
	// Scale for rendering comes from the account row.
	scale := s.balAccountScale(r, st, account)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"account_id": account,
		"value":      bal.FormatAmount(v, scale),
		"minor":      v, // int64 minor units: exact, no decimal point involved
		"version":    ver,
	})
}

func (s *Server) balAccountScale(r *http.Request, st *bal.Store, account string) uint8 {
	scale, err := st.AccountScale(r.Context(), account)
	if err != nil {
		return 0
	}
	return scale
}

func (s *Server) handleBalEntries(w http.ResponseWriter, r *http.Request) {
	account := r.URL.Query().Get("account")
	if account == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "account query parameter required")
		return
	}
	st, err := s.balStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	entries, err := st.Entries(r.Context(), account, 0, 100)
	if err != nil {
		s.writeBalError(w, err)
		return
	}
	scale := s.balAccountScale(r, st, account)
	out := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]interface{}{
			"entry_id":         e.EntryID,
			"transfer_id":      e.TransferID,
			"amount":           bal.FormatAmount(e.Amount, scale),
			"previous_balance": bal.FormatAmount(e.PreviousBalance, scale),
			"current_balance":  bal.FormatAmount(e.CurrentBalance, scale),
			"version":          e.Version,
			"memo":             e.Memo,
			"at":               e.At,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"account_id": account,
		"entries":    out,
	})
}

// handleBalAsOf serves balance-as-of from the DERIVED rollup plane
// (@B05): nearest sealed checkpoint + intervening buckets. This is the
// fast path; the exact/audit path is the journal chain, and the rollup
// oracle proves the two agree. No guard reads this surface (@C04a).
func (s *Server) handleBalAsOf(w http.ResponseWriter, r *http.Request) {
	account := r.URL.Query().Get("account")
	if account == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "account query parameter required")
		return
	}
	atStr := r.URL.Query().Get("at")
	if atStr == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "at query parameter required (RFC3339)")
		return
	}
	at, err := time.Parse(time.RFC3339, atStr)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "at: "+err.Error())
		return
	}
	st, err := s.balStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	v, err := st.BalanceAsOf(r.Context(), account, at)
	if err != nil {
		s.writeBalError(w, err)
		return
	}
	scale := s.balAccountScale(r, st, account)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"account_id": account,
		"at":         at.UTC(),
		"value":      bal.FormatAmount(v, scale),
		"minor":      v,
		"source":     "rollup", // derived plane; exact path is the journal chain
	})
}

type balCloseReq struct {
	At string `json:"at"` // RFC3339 period boundary
}

// handleBalClose seals the tenant's account-set as of a period boundary
// (item 16 §7): advances the seal frontier and writes closing
// checkpoints for every postable account. Previously wrote a single
// account's checkpoint without enforcing anything -- account_id is no
// longer accepted; sealing is tenant-wide, not per-account, matching
// what this endpoint's own design (bal-conservation-primitive.md §7)
// always specified.
func (s *Server) handleBalClose(w http.ResponseWriter, r *http.Request) {
	var req balCloseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	at, err := time.Parse(time.RFC3339, req.At)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "at: "+err.Error())
		return
	}
	st, err := s.balStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	n, err := st.SealPeriod(r.Context(), at)
	if err != nil {
		s.writeBalError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"sealed_through":  at.UTC(),
		"accounts_closed": n,
		"status":          "closed",
	})
}

// Silence the unused-import guard if storage is only used transitively.
var _ = storage.ErrNotFound
