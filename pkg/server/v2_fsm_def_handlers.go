package server

// S7 / Batch B2 — FSM definition handlers.
//
//   POST   /fsm/def            create a definition (prototype)
//   GET    /fsm/def            list definitions
//   GET    /fsm/def/{id}       retrieve a definition
//   PUT    /fsm/def/{id}       replace a definition (future machines only)
//   DELETE /fsm/def/{id}       delete a definition (always permitted)
//   POST   /fsm/def/validate   validate without storing

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/fsm/eval"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// fsmDB returns the writer DB and tenant ID, mirroring seqDB.
func (s *Server) fsmDB(r *http.Request) (*sql.DB, tenant.TenantID) {
	store := s.getStore(r.Context())
	tenantID := getTenantIDNumeric(r.Context())
	if wdp, ok := store.(storage.WriterDBProvider); ok {
		return wdp.WriterDB(), tenantID
	}
	return nil, tenantID
}

// allocFSMID atomically allocates the next monotonic ID for the given kind
// ('def', 'machine', 'history') within a tenant, using the same RETURNING
// upsert pattern as the node-sequence allocator. Must be called inside tx.
func allocFSMID(ctx context.Context, tx *sql.Tx, tenantID tenant.TenantID, kind string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO fsm_id_seq (tenant_id, kind, next_id)
		VALUES (?, ?, 1)
		ON CONFLICT(tenant_id, kind) DO UPDATE SET next_id = next_id + 1
		RETURNING next_id`, tenantID, kind).Scan(&id)
	return id, err
}

// fsmErrorStatus maps an XOLU-FSM code to its spec HTTP status (API_V2.md).
func fsmErrorStatus(code string) int {
	switch code {
	case "XOLU-FSM001", "XOLU-FSM002", "XOLU-FSM012":
		return http.StatusNotFound
	case "XOLU-FSM003", "XOLU-FSM005", "XOLU-FSM008":
		return http.StatusConflict
	case "XOLU-FSM004", "XOLU-FSM006", "XOLU-FSM009",
		"XOLU-FSM010", "XOLU-FSM011", "XOLU-FSM013":
		return http.StatusUnprocessableEntity
	default:
		return http.StatusUnprocessableEntity
	}
}

// writeFSMValidationError writes a *fsmValidationError using its code and the
// spec-mandated status.
func (s *Server) writeFSMValidationError(w http.ResponseWriter, ve *fsmValidationError) {
	s.writeError(w, fsmErrorStatus(ve.Code), xoluerr.Code(ve.Code), ve.Message)
}

// ─── POST /fsm/def — create ───────────────────────────────────────────────────

func (s *Server) handleFSMDefCreate(w http.ResponseWriter, r *http.Request) {
	var spec fsmDefinitionSpec
	if !s.decodeJSON(w, r, &spec) {
		return
	}

	ev := eval.New()
	analysis, verr := validateDefinition(&spec, ev)
	if verr != nil {
		if ve, ok := verr.(*fsmValidationError); ok {
			s.writeFSMValidationError(w, ve)
			return
		}
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrFSMValidation, verr.Error())
		return
	}

	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	specJSON, err := json.Marshal(&spec)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	analysisJSON, err := json.Marshal(analysis)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()

	id, err := allocFSMID(r.Context(), tx, tenantID, "def")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	var createdAt string
	err = tx.QueryRowContext(r.Context(), `
		INSERT INTO fsm_definitions (tenant_id, id, name, spec_json, analysis_json)
		VALUES (?, ?, ?, ?, ?)
		RETURNING created_at`,
		tenantID, id, spec.Name, string(specJSON), string(analysisJSON)).Scan(&createdAt)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":         id,
		"name":       spec.Name,
		"created_at": createdAt,
		"analysis":   analysis,
	})
}

// ─── GET /fsm/def — list ──────────────────────────────────────────────────────

func (s *Server) handleFSMDefList(w http.ResponseWriter, r *http.Request) {
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	rows, err := db.QueryContext(r.Context(), `
		SELECT id, name, created_at
		FROM fsm_definitions
		WHERE tenant_id = ?
		ORDER BY id`, tenantID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	type defSummary struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
	}
	defs := []defSummary{}
	for rows.Next() {
		var d defSummary
		if err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		defs = append(defs, d)
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{"definitions": defs})
}

// ─── GET /fsm/def/{id} — retrieve ─────────────────────────────────────────────

func (s *Server) handleFSMDefGet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	var specJSON, analysisJSON, createdAt string
	err := db.QueryRowContext(r.Context(), `
		SELECT spec_json, analysis_json, created_at
		FROM fsm_definitions
		WHERE tenant_id = ? AND id = ?`, tenantID, id).
		Scan(&specJSON, &analysisJSON, &createdAt)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMDefNotFound, "definition not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Compose the response without re-parsing spec_json/analysis_json.
	_, _ = w.Write([]byte(`{"id":` + strconv.FormatInt(id, 10) +
		`,"created_at":` + strconvQuote(createdAt) +
		`,"spec":` + specJSON +
		`,"analysis":` + analysisJSON + `}`))
}

// ─── PUT /fsm/def/{id} — replace ──────────────────────────────────────────────

func (s *Server) handleFSMDefReplace(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmParseID(w, r)
	if !ok {
		return
	}
	var spec fsmDefinitionSpec
	if !s.decodeJSON(w, r, &spec) {
		return
	}

	ev := eval.New()
	analysis, verr := validateDefinition(&spec, ev)
	if verr != nil {
		if ve, ok := verr.(*fsmValidationError); ok {
			s.writeFSMValidationError(w, ve)
			return
		}
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrFSMValidation, verr.Error())
		return
	}

	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	specJSON, _ := json.Marshal(&spec)
	analysisJSON, _ := json.Marshal(analysis)

	res, err := db.ExecContext(r.Context(), `
		UPDATE fsm_definitions
		SET name = ?, spec_json = ?, analysis_json = ?
		WHERE tenant_id = ? AND id = ?`,
		spec.Name, string(specJSON), string(analysisJSON), tenantID, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMDefNotFound, "definition not found")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       id,
		"name":     spec.Name,
		"analysis": analysis,
	})
}

// ─── DELETE /fsm/def/{id} — delete (always permitted) ─────────────────────────

func (s *Server) handleFSMDefDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.fsmParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 FSM")
		return
	}

	res, err := db.ExecContext(r.Context(), `
		DELETE FROM fsm_definitions WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMDefNotFound, "definition not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── POST /fsm/def/validate — validate without storing ────────────────────────

func (s *Server) handleFSMDefValidate(w http.ResponseWriter, r *http.Request) {
	var spec fsmDefinitionSpec
	if !s.decodeJSON(w, r, &spec) {
		return
	}
	ev := eval.New()
	analysis, verr := validateDefinition(&spec, ev)
	if verr != nil {
		if ve, ok := verr.(*fsmValidationError); ok {
			s.writeJSON(w, http.StatusOK, map[string]interface{}{
				"valid":  false,
				"errors": []map[string]string{{"code": ve.Code, "message": ve.Message}},
			})
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"valid":  false,
			"errors": []map[string]string{{"code": "XOLU-FSM006", "message": verr.Error()}},
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid":    true,
		"analysis": analysis,
	})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// fsmParseID parses the {id} path parameter, writing XOLU-FSM001 on a bad value.
func (s *Server) fsmParseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrFSMDefNotFound,
			"definition id must be a positive integer")
		return 0, false
	}
	return id, true
}

// strconvQuote JSON-quotes a string for manual response composition.
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
