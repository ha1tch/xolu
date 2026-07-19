package server

// S8 — FSM walk (standalone endpoint).
//
//   POST /api/v2/fsm/machine/{id}/walk
//
// The walk's transaction-scoped logic lives in pkg/storage (FsmWalkInTx),
// the same method the /commit path calls inside its own transaction. This
// handler opens a transaction, delegates to the store, and commits — so the
// standalone walk and the commit-embedded walk share one implementation and
// cannot drift. Bulk walk via @FSM() against _api_v2_machine_ is a separate
// path (S14).

import (
	"fmt"
	"net/http"

	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/fsm/eval"
	"github.com/ha1tch/xolu/pkg/storage"
)

// runWalkPrequery looks up an OQL query associated with the given input on the
// machine's definition, runs it read-only (before any walk transaction), and
// returns its first result row as bindings prefixed with "query.". Returns
// (nil, nil) when no query is associated with the input.
//
// Semantics (deliberately chosen, not inherited):
//   - 0 rows  -> no bindings; query.<col> references resolve to NULL.
//   - 1 row   -> each scalar column bound as query.<col>.
//   - N rows  -> the first row is bound; queries should be written to return at
//     most one row. Ordering follows the query; an unordered query
//     returning multiple rows has an unspecified first row.
func (s *Server) runWalkPrequery(r *http.Request, wdp storage.WriterDBProvider,
	tenantID uint16, machineID int64, input string) (map[string]interface{}, error) {

	snap, _, _, _, _, _, _, err := s.loadMachineSnapshot(r, wdp.WriterDB(), tenantID, machineID)
	if err != nil {
		return nil, nil
	}
	if len(snap.Spec.InputQueries) == 0 {
		return nil, nil
	}
	query, ok := snap.Spec.InputQueries[input]
	if !ok || query == "" {
		return nil, nil
	}
	if s.oqlJobs == nil {
		return nil, fmt.Errorf("query execution is not available")
	}

	// Force TOP 1 onto the query before execution. Only the first row is used,
	// so retrieving more is wasteful; the bound is imposed by the engine rather
	// than trusted to the author. The author still controls which row via
	// ORDER BY.
	bounded, terr := eval.ForceTop1(query)
	if terr != nil {
		return nil, fmt.Errorf("invalid pre-query: %w", terr)
	}

	store := s.getStore(r.Context())
	result, err := s.oqlJobs.ExecuteSyncWithStore(r.Context(), bounded, store)
	if err != nil {
		return nil, err
	}
	bindings := map[string]interface{}{}
	if result != nil && len(result.Rows) > 0 {
		for col, val := range result.Rows[0] {
			switch val.(type) {
			case string, int, int64, float64, bool, nil:
				bindings[col] = val
			}
		}
	}
	return bindings, nil
}
func (s *Server) handleFSMMachineWalk(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmMachineParseID(w, r)
	if !ok {
		return
	}
	var req struct {
		Input   string                 `json:"input"`
		Payload map[string]interface{} `json:"payload,omitempty"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}

	store := s.getStore(r.Context())
	walker, ok := store.(storage.FsmWalker)
	wdp, ok2 := store.(storage.WriterDBProvider)
	if !ok || !ok2 {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM walk")
		return
	}
	tenantID := getTenantIDNumeric(r.Context())

	// Pre-query: if the machine's definition associates an OQL SELECT with this
	// input, run it (read-only, before the walk transaction) and bind its first
	// result row into the guard/set evaluator under the "query." prefix. Guards
	// and set clauses can then read query.<column> alongside payload.<field>.
	queryBindings, perr := s.runWalkPrequery(r, wdp, tenantID, id, req.Input)
	if perr != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrQueryFailed,
			"transition query failed: "+perr.Error())
		return
	}

	tx, err := wdp.WriterDB().BeginTx(r.Context(), nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result, werr := walker.FsmWalkInTx(r.Context(), tx, tenantID, id, req.Input, req.Payload, queryBindings)
	if werr != nil {
		if we, ok := werr.(*storage.FsmWalkError); ok {
			s.writeError(w, fsmErrorStatus(we.Code), xoluerr.Code(we.Code), we.Message)
			return
		}
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, werr.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	committed = true

	s.fireFSMOutputEvents(r, id, req.Input, result.Previous, result.Current, result.Outputs, result.Definition)
	s.fireFSMStepEvent(r, id, req.Input, result.Previous, result.Current, result.Terminal, result.Vars, nil, result.Definition)

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"previous":   result.Previous,
		"current":    result.Current,
		"terminal":   result.Terminal,
		"outputs":    result.Outputs,
		"vars":       result.Vars,
		"history_id": result.HistoryID,
	})
}
