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
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// setupV2BalRoutes registers the bal surface (gated on BalEnabled).
func (s *Server) setupV2BalRoutes(r chi.Router) {
	r.Post("/bal/def", s.handleBalDefine)
	r.Post("/bal/transfer", s.handleBalTransfer)
	// Account ids are namespaced strings containing '/', so they travel
	// as a query parameter, never a path segment.
	r.Get("/bal/balance", s.handleBalBalance)
	r.Get("/bal/entries", s.handleBalEntries)
}

// balStore resolves the tenant-scoped bal store over the shared writer
// DB, initialising the tenant's bal tables once per server instance
// (idempotent DDL). The once-map lives ON the Server — a package
// global would leak initialisation state across instances (caught by
// the handler tests: server two skipped DDL because server one's Once
// had fired).
func (s *Server) balStore(r *http.Request) (*bal.Store, error) {
	db, tenantID := s.metaDB(r) // same writer-db + tenant resolution meta uses
	st := bal.NewStore(db, tenant.TablePrefix(tenantID))
	onceI, _ := s.balInit.LoadOrStore(tenantID, &sync.Once{})
	var initErr error
	onceI.(*sync.Once).Do(func() { initErr = st.Init(r.Context()) })
	if initErr != nil {
		// A failed init must not poison the Once for the instance.
		s.balInit.Delete(tenantID)
	}
	return st, initErr
}

// writeBalError maps bal's typed errors to the XOLU-BAL family.
func (s *Server) writeBalError(w http.ResponseWriter, err error) {
	var be *bal.BoundsError
	var ua *bal.UnknownAccountError
	var as *bal.AmountScaleError
	var np *bal.NotPostableError
	switch {
	case errors.As(err, &be):
		s.writeError(w, http.StatusConflict, xoluerr.ErrBalBounds, err.Error())
	case errors.As(err, &ua):
		s.writeError(w, http.StatusNotFound, xoluerr.ErrBalUnknownAccount, err.Error())
	case errors.As(err, &as):
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrBalAmountScale, err.Error())
	case errors.As(err, &np):
		s.writeError(w, http.StatusConflict, xoluerr.ErrBalNotPostable, err.Error())
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

// Silence the unused-import guard if storage is only used transitively.
var _ = storage.ErrNotFound
