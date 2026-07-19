// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_gen_handlers.go
//
// S4: stateless value generators.
//
// Endpoints (no tenant scope needed — pure functions):
//   GET /api/v2/gen/uuid_v4   — random UUID (RFC 4122 v4)
//   GET /api/v2/gen/uuid_v7   — time-ordered UUID (RFC 9562 v7)
//   GET /api/v2/gen/cuid      — collision-resistant unique identifier
//   GET /api/v2/gen/ulid      — lexicographically sortable unique identifier
//
// OQL scalar functions registered in init(): UUID_V4(), UUID_V7(),
// CUID(), ULID(). These are available in any OQL query regardless of the
// v2 flag because they are pure functions with no server-side state.
//
// S10 adds stateful generators (token, timestamp, random_int, pick, etc.)
// in a separate handler file.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/ha1tch/xolu/pkg/oql"
	cuidlib "github.com/lucsky/cuid"
	ulidlib "github.com/oklog/ulid/v2"
)

// ─── Generator functions ──────────────────────────────────────────────────────

func genUUIDv4() string {
	id, err := uuid.NewRandom()
	if err != nil {
		// Extremely unlikely; fall back to a nil UUID rather than panic.
		return uuid.Nil.String()
	}
	return id.String()
}

func genUUIDv7() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil.String()
	}
	return id.String()
}

func genCUID() string {
	return cuidlib.New()
}

func genULID() string {
	return ulidlib.Make().String()
}

// ─── OQL scalar registration ──────────────────────────────────────────────────

// init registers the four stateless generator functions in the OQL scalar
// function map. This runs unconditionally (not gated on APIV2Enabled) because
// the functions are pure and carry no server-side state — they are safe to
// expose in any OQL query.
func init() {
	oql.RegisterScalarFunc("UUID_V4", func(_ []interface{}) interface{} {
		return genUUIDv4()
	})
	oql.RegisterScalarFunc("UUID_V7", func(_ []interface{}) interface{} {
		return genUUIDv7()
	})
	oql.RegisterScalarFunc("CUID", func(_ []interface{}) interface{} {
		return genCUID()
	})
	oql.RegisterScalarFunc("ULID", func(_ []interface{}) interface{} {
		return genULID()
	})
}

// ─── HTTP handlers ────────────────────────────────────────────────────────────

type genResponse struct {
	Type        string    `json:"type"`
	Value       string    `json:"value"`
	GeneratedAt time.Time `json:"generated_at"`
}

func (s *Server) handleGenUUIDv4(w http.ResponseWriter, r *http.Request) {
	resp := genResponse{Type: "uuid_v4", Value: genUUIDv4(), GeneratedAt: time.Now().UTC()}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleGenUUIDv7(w http.ResponseWriter, r *http.Request) {
	resp := genResponse{Type: "uuid_v7", Value: genUUIDv7(), GeneratedAt: time.Now().UTC()}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleGenCUID(w http.ResponseWriter, r *http.Request) {
	resp := genResponse{Type: "cuid", Value: genCUID(), GeneratedAt: time.Now().UTC()}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleGenULID(w http.ResponseWriter, r *http.Request) {
	resp := genResponse{Type: "ulid", Value: genULID(), GeneratedAt: time.Now().UTC()}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
