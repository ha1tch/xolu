package server

// S7 / Batch B3 — FSM machine handlers.
//
//   POST   /fsm/machine                  create a machine from a definition
//   GET    /fsm/machine                  list machines (filter: definition, state, ref)
//   GET    /fsm/machine/{id}             retrieve a machine
//   PATCH  /fsm/machine/{id}             update machine-local guards / var defaults
//   DELETE /fsm/machine/{id}             delete a machine
//   GET    /fsm/machine/{id}/state       current state
//   GET    /fsm/machine/{id}/history     full transition history
//   GET    /fsm/machine/{id}/transitions available inputs from current state
//   GET    /fsm/machine/{id}/vars        current variable values
//
// /fsm/machine/{id}/walk is registered in v2_fsm_handlers.go and returns 501
// until S8.
//
// Inline entity creation (the `entity` field) is deferred to Part 2. Creation
// with `ref` binding to an existing entity, and creation with no `ref`, both
// work in S7.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/fsm/eval"
)

// fsmMachineParseID parses {id}, writing XOLU-FSM002 on a bad value.
func (s *Server) fsmMachineParseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMMachineNotFound,
			"machine id must be a positive integer")
		return 0, false
	}
	return id, true
}

// loadMachineSnapshot loads and unmarshals a machine's snapshot, current
// state, vars, ref, and definition lineage. Returns sql.ErrNoRows if absent.
func (s *Server) loadMachineSnapshot(r *http.Request, db *sql.DB, tenantID uint16, id int64) (
	snap fsmMachineSnapshot, defID int64, defName, state string, vars map[string]interface{}, ref *string, createdAt string, err error) {
	var snapJSON, varsJSON string
	var refNull sql.NullString
	err = db.QueryRowContext(r.Context(), `
		SELECT fsm_def_id, definition_name, snapshot_json, state, vars_json, ref, created_at
		FROM fsm_machines
		WHERE tenant_id = ? AND id = ?`, tenantID, id).
		Scan(&defID, &defName, &snapJSON, &state, &varsJSON, &refNull, &createdAt)
	if err != nil {
		return
	}
	if uerr := json.Unmarshal([]byte(snapJSON), &snap); uerr != nil {
		err = uerr
		return
	}
	if uerr := json.Unmarshal([]byte(varsJSON), &vars); uerr != nil {
		err = uerr
		return
	}
	if refNull.Valid {
		ref = &refNull.String
	}
	return
}

// definitionExists reports whether a definition with the given id still
// exists for the tenant (drives the definition_deleted flag).
func (s *Server) definitionExists(r *http.Request, db *sql.DB, tenantID uint16, defID int64) bool {
	var n int
	_ = db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM fsm_definitions WHERE tenant_id = ? AND id = ?`,
		tenantID, defID).Scan(&n)
	return n > 0
}

// ─── POST /fsm/machine — create ───────────────────────────────────────────────

func (s *Server) handleFSMMachineCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Definition int64           `json:"definition"`
		Ref        string          `json:"ref,omitempty"`
		Entity     json.RawMessage `json:"entity,omitempty"`
		Overrides  *fsmOverrides   `json:"overrides,omitempty"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.Definition <= 0 {
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrFSMValidation,
			"definition id is required")
		return
	}
	// Inline entity creation is deferred to Part 2.
	if len(req.Entity) > 0 {
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrFSMValidation,
			"inline entity creation is not yet implemented in the v2 preview; bind with ref instead")
		return
	}

	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	// Load the source definition spec.
	var specJSON, defName string
	err := db.QueryRowContext(r.Context(), `
		SELECT name, spec_json FROM fsm_definitions
		WHERE tenant_id = ? AND id = ?`, tenantID, req.Definition).Scan(&defName, &specJSON)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMDefNotFound, "definition not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	var spec fsmDefinitionSpec
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// Apply overrides onto the snapshot copy.
	if err := applyOverrides(&spec, req.Overrides); err != nil {
		if ve, ok := err.(*fsmValidationError); ok {
			s.writeFSMValidationError(w, ve)
			return
		}
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrFSMValidation, err.Error())
		return
	}

	// The post-override snapshot must still be valid as a unit.
	ev := eval.New()
	if _, verr := validateDefinition(&spec, ev); verr != nil {
		if ve, ok := verr.(*fsmValidationError); ok {
			s.writeFSMValidationError(w, ve)
			return
		}
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrFSMValidation, verr.Error())
		return
	}

	// Resolve linked-state children by ID, snapshotting each alongside.
	snap := fsmMachineSnapshot{Spec: spec}
	if len(spec.LinkedStates) > 0 {
		snap.Children = make(map[string]fsmDefinitionSpec)
		for state, childID := range spec.LinkedStates {
			var childJSON string
			cerr := db.QueryRowContext(r.Context(), `
				SELECT spec_json FROM fsm_definitions
				WHERE tenant_id = ? AND id = ?`, tenantID, childID).Scan(&childJSON)
			if cerr == sql.ErrNoRows {
				s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMChildNotFound,
					"linked-state child definition "+strconv.FormatInt(childID, 10)+" not found")
				return
			}
			if cerr != nil {
				s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, cerr.Error())
				return
			}
			var childSpec fsmDefinitionSpec
			if uerr := json.Unmarshal([]byte(childJSON), &childSpec); uerr != nil {
				s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, uerr.Error())
				return
			}
			snap.Children[state] = childSpec
		}
	}

	snapJSON, err := json.Marshal(&snap)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	vars := initialVars(&spec)
	varsJSON, _ := json.Marshal(vars)

	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()

	id, err := allocFSMID(r.Context(), tx, tenantID, "machine")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	var refArg interface{}
	if req.Ref != "" {
		refArg = req.Ref
	}
	var createdAt string
	err = tx.QueryRowContext(r.Context(), `
		INSERT INTO fsm_machines
			(tenant_id, id, fsm_def_id, definition_name, snapshot_json, state, vars_json, ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING created_at`,
		tenantID, id, req.Definition, defName, string(snapJSON), spec.Initial, string(varsJSON), refArg).
		Scan(&createdAt)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// Record terminal states for fast walk checks (S8) and the creation
	// history entry.
	for _, ts := range terminalStateList(&spec) {
		if _, err := tx.ExecContext(r.Context(), `
			INSERT OR IGNORE INTO fsm_terminal_states (tenant_id, machine_id, state)
			VALUES (?, ?, ?)`, tenantID, id, ts); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
	}
	histID, err := allocFSMID(r.Context(), tx, tenantID, "history")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO fsm_history (tenant_id, id, machine_id, from_state, to_state, input, vars_json, note)
		VALUES (?, ?, ?, NULL, ?, NULL, ?, ?)`,
		tenantID, histID, id, spec.Initial, string(varsJSON), "machine created"); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	resp := map[string]interface{}{
		"id":                 id,
		"definition":         req.Definition,
		"definition_name":    defName,
		"definition_deleted": false,
		"state":              spec.Initial,
		"vars":               vars,
		"created_at":         createdAt,
	}
	if req.Ref != "" {
		resp["ref"] = req.Ref
	}
	s.writeJSON(w, http.StatusCreated, resp)
}

// ─── GET /fsm/machine — list ──────────────────────────────────────────────────

func (s *Server) handleFSMMachineList(w http.ResponseWriter, r *http.Request) {
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	// Optional filters.
	q := r.URL.Query()
	where := "tenant_id = ?"
	args := []interface{}{tenantID}
	if d := q.Get("definition"); d != "" {
		if did, err := strconv.ParseInt(d, 10, 64); err == nil {
			where += " AND fsm_def_id = ?"
			args = append(args, did)
		}
	}
	if st := q.Get("state"); st != "" {
		where += " AND state = ?"
		args = append(args, st)
	}
	if ref := q.Get("ref"); ref != "" {
		where += " AND ref = ?"
		args = append(args, ref)
	}

	rows, err := db.QueryContext(r.Context(), `
		SELECT id, fsm_def_id, definition_name, state, ref, created_at
		FROM fsm_machines WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	type machineSummary struct {
		ID             int64   `json:"id"`
		Definition     int64   `json:"definition"`
		DefinitionName string  `json:"definition_name"`
		State          string  `json:"state"`
		Ref            *string `json:"ref"`
		CreatedAt      string  `json:"created_at"`
	}
	machines := []machineSummary{}
	for rows.Next() {
		var m machineSummary
		var refNull sql.NullString
		if err := rows.Scan(&m.ID, &m.Definition, &m.DefinitionName, &m.State, &refNull, &m.CreatedAt); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		if refNull.Valid {
			m.Ref = &refNull.String
		}
		machines = append(machines, m)
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"machines": machines})
}

// ─── GET /fsm/machine/{id} — retrieve ─────────────────────────────────────────

func (s *Server) handleFSMMachineGet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmMachineParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	_, defID, defName, state, vars, ref, createdAt, err := s.loadMachineSnapshot(r, db, tenantID, id)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMMachineNotFound, "machine not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	resp := map[string]interface{}{
		"id":                 id,
		"definition":         defID,
		"definition_name":    defName,
		"definition_deleted": !s.definitionExists(r, db, tenantID, defID),
		"state":              state,
		"vars":               vars,
		"created_at":         createdAt,
	}
	if ref != nil {
		resp["ref"] = *ref
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// ─── PATCH /fsm/machine/{id} — update local guards / var defaults ─────────────

func (s *Server) handleFSMMachinePatch(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmMachineParseID(w, r)
	if !ok {
		return
	}
	var req struct {
		Overrides *fsmOverrides `json:"overrides,omitempty"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}

	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	snap, defID, defName, state, vars, ref, createdAt, err := s.loadMachineSnapshot(r, db, tenantID, id)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMMachineNotFound, "machine not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// Apply overrides to the snapshot spec, then re-validate the whole
	// snapshot as a unit. Current state, history, and live vars are untouched.
	if err := applyOverrides(&snap.Spec, req.Overrides); err != nil {
		if ve, ok := err.(*fsmValidationError); ok {
			s.writeFSMValidationError(w, ve)
			return
		}
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrFSMValidation, err.Error())
		return
	}
	ev := eval.New()
	if _, verr := validateDefinition(&snap.Spec, ev); verr != nil {
		if ve, ok := verr.(*fsmValidationError); ok {
			s.writeFSMValidationError(w, ve)
			return
		}
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrFSMValidation, verr.Error())
		return
	}

	snapJSON, err := json.Marshal(&snap)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if _, err := db.ExecContext(r.Context(), `
		UPDATE fsm_machines SET snapshot_json = ? WHERE tenant_id = ? AND id = ?`,
		string(snapJSON), tenantID, id); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	resp := map[string]interface{}{
		"id":                 id,
		"definition":         defID,
		"definition_name":    defName,
		"definition_deleted": !s.definitionExists(r, db, tenantID, defID),
		"state":              state,
		"vars":               vars,
		"created_at":         createdAt,
	}
	if ref != nil {
		resp["ref"] = *ref
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// ─── DELETE /fsm/machine/{id} — delete (always permitted) ─────────────────────

func (s *Server) handleFSMMachineDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmMachineParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(r.Context(),
		`DELETE FROM fsm_machines WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMMachineNotFound, "machine not found")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM fsm_terminal_states WHERE tenant_id = ? AND machine_id = ?`, tenantID, id); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM fsm_history WHERE tenant_id = ? AND machine_id = ?`, tenantID, id); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── GET /fsm/machine/{id}/state ──────────────────────────────────────────────

func (s *Server) handleFSMMachineState(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmMachineParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}
	snap, _, _, state, _, _, _, err := s.loadMachineSnapshot(r, db, tenantID, id)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMMachineNotFound, "machine not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	terminal := false
	if ss, ok := snap.Spec.States[state]; ok {
		terminal = ss.Terminal
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"state":    state,
		"terminal": terminal,
	})
}

// ─── GET /fsm/machine/{id}/result ─────────────────────────────────────────────
//
// Returns the final result of a machine: whether it has reached a terminal
// (STOP) state, and if so its final state, final variable values, and the
// output emitted by the transition that reached the terminal state. This is a
// convenience over /state + /vars + /history: a single call to retrieve "what
// did this machine end up as".
//
// For a machine that has not yet stopped, terminal is false and final_output is
// omitted; state and vars still reflect the current (non-final) values, so a
// caller can poll this endpoint and act once terminal becomes true.
func (s *Server) handleFSMMachineResult(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmMachineParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}
	snap, _, _, state, vars, _, _, err := s.loadMachineSnapshot(r, db, tenantID, id)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMMachineNotFound, "machine not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	terminal := false
	if ss, ok := snap.Spec.States[state]; ok {
		terminal = ss.Terminal
	}

	resp := map[string]interface{}{
		"machine":  id,
		"state":    state,
		"terminal": terminal,
		"vars":     vars,
	}

	// The output of the transition that reached the current state is the last
	// history row's output. Only meaningful as a "final output" once terminal.
	if terminal {
		var outputJSON sql.NullString
		err := db.QueryRowContext(r.Context(), `
			SELECT output_json
			FROM fsm_history
			WHERE tenant_id = ? AND machine_id = ?
			ORDER BY id DESC
			LIMIT 1`, tenantID, id).Scan(&outputJSON)
		if err != nil && err != sql.ErrNoRows {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		if outputJSON.Valid {
			resp["final_output"] = json.RawMessage(outputJSON.String)
		} else {
			// Terminal but the final transition emitted no output.
			resp["final_output"] = []string{}
		}
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// ─── GET /fsm/machine/{id}/vars ───────────────────────────────────────────────

func (s *Server) handleFSMMachineVars(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmMachineParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}
	snap, _, _, _, vars, _, _, err := s.loadMachineSnapshot(r, db, tenantID, id)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMMachineNotFound, "machine not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	// Richer shape: value alongside declared type and default from snapshot.
	out := make(map[string]interface{}, len(vars))
	for name, val := range vars {
		entry := map[string]interface{}{"value": val}
		if decl, ok := snap.Spec.Variables[name]; ok {
			entry["type"] = decl.Type
			entry["default"] = decl.Default
		}
		out[name] = entry
	}
	s.writeJSON(w, http.StatusOK, out)
}

// ─── GET /fsm/machine/{id}/transitions ────────────────────────────────────────

func (s *Server) handleFSMMachineTransitions(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmMachineParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}
	snap, _, _, state, _, _, _, err := s.loadMachineSnapshot(r, db, tenantID, id)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMMachineNotFound, "machine not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	// Inputs for which a transition exists from the current state. Guards are
	// not pre-evaluated; an input is included if the transition exists.
	seen := make(map[string]struct{})
	inputs := []string{}
	for i := range snap.Spec.Transitions {
		ts := &snap.Spec.Transitions[i]
		froms, ferr := ts.fromStates()
		if ferr != nil {
			continue
		}
		for _, from := range froms {
			if from == state && ts.Input != "" {
				if _, dup := seen[ts.Input]; !dup {
					seen[ts.Input] = struct{}{}
					inputs = append(inputs, ts.Input)
				}
			}
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"state":  state,
		"inputs": inputs,
	})
}

// ─── GET /fsm/machine/{id}/history ────────────────────────────────────────────

func (s *Server) handleFSMMachineHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmMachineParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	// Confirm the machine exists for a clean 404.
	var exists int
	_ = db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM fsm_machines WHERE tenant_id = ? AND id = ?`, tenantID, id).Scan(&exists)
	if exists == 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMMachineNotFound, "machine not found")
		return
	}

	rows, err := db.QueryContext(r.Context(), `
		SELECT id, from_state, to_state, input, payload_json, vars_json, output_json, note, at
		FROM fsm_history
		WHERE tenant_id = ? AND machine_id = ?
		ORDER BY id`, tenantID, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	entries := []map[string]interface{}{}
	for rows.Next() {
		var (
			hid                                       int64
			fromState, input, payloadJSON, outputJSON sql.NullString
			note                                      sql.NullString
			toState, varsJSON, at                     string
		)
		if err := rows.Scan(&hid, &fromState, &toState, &input, &payloadJSON, &varsJSON, &outputJSON, &note, &at); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		entry := map[string]interface{}{
			"id": hid,
			"to": toState,
			"at": at,
		}
		entry["from"] = nullStr(fromState)
		entry["input"] = nullStr(input)
		entry["payload"] = nullRawJSON(payloadJSON)
		entry["vars"] = json.RawMessage(varsJSON)
		if outputJSON.Valid {
			entry["outputs"] = json.RawMessage(outputJSON.String)
		}
		if note.Valid {
			entry["note"] = note.String
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"machine": id,
		"entries": entries,
	})
}

// nullStr returns the string or nil for a NullString.
func nullStr(ns sql.NullString) interface{} {
	if ns.Valid {
		return ns.String
	}
	return nil
}

// nullRawJSON returns parsed JSON or nil for a NullString holding JSON.
func nullRawJSON(ns sql.NullString) interface{} {
	if ns.Valid {
		return json.RawMessage(ns.String)
	}
	return nil
}
