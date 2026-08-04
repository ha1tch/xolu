// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_handlers.go
//
// API v2 scaffold: route registration, X-API-Stability middleware, and
// the GET /api/v2 availability endpoint.
//
// All v2 routes are registered only when s.config.APIV2Enabled is true.
// When false, the /api/v2 prefix does not exist in the router — requests
// to it return 404, not 501. This is intentional: a 404 is unambiguous
// ("this server does not have v2") whereas a 501 could be confused with
// a v2 handler returning "not implemented".
//
// The X-API-Stability: experimental header is added by v2Middleware to
// every response from a v2 route. It signals to clients that the API
// surface may change before xolu 2.0 without the client needing to
// inspect the version string.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ha1tch/xolu/pkg/config"
)

// setupV2Routes registers all /api/v2 routes when APIV2Enabled is true.
// Called from setupRoutes after the /api/v1 block.
func (s *Server) setupV2Routes(r chi.Router) {
	if !s.config.APIV2Enabled {
		return
	}

	r.Route("/api/v2", func(v2 chi.Router) {
		// Stability header on every v2 response.
		v2.Use(s.v2Middleware)

		// Availability map — the only v2 endpoint that always exists.
		v2.Get("/", s.handleV2Availability)

		// S4: stateless generators — no tenant scope needed.
		v2.Get("/gen/uuid_v4", s.handleGenUUIDv4)
		v2.Get("/gen/uuid_v7", s.handleGenUUIDv7)
		v2.Get("/gen/cuid", s.handleGenCUID)
		v2.Get("/gen/ulid", s.handleGenULID)

		// Tenant-scoped v2 routes.
		v2.Route("/tenant/{tenant_id}", func(tr chi.Router) {
			tr.Use(s.tenantMiddleware)
			s.setupV2TenantRoutes(tr)
		})

		// Unscoped v2 routes (tenant 0), disabled in strict mode.
		if !s.config.Tenancy().Has(config.TenantRequireRoute) {
			s.setupV2TenantRoutes(v2)
		}
	})
}

// setupV2TenantRoutes registers the per-subsystem v2 routes on a router
// that is already scoped to a tenant (either via tenantMiddleware or
// defaulting to tenant 0). Called for both the scoped and unscoped paths.
func (s *Server) setupV2TenantRoutes(r chi.Router) {
	// S3: entity metadata sidecar.
	r.Get("/meta/{entity}/{id}", s.handleMetaList)
	r.Get("/meta/{entity}/{id}/{key}", s.handleMetaGet)
	r.Put("/meta/{entity}/{id}/{key}", s.handleMetaPut)
	r.Delete("/meta/{entity}/{id}/{key}", s.handleMetaDeleteKey)
	r.Delete("/meta/{entity}/{id}", s.handleMetaDeleteAll)

	// S5: named sequences (also accessible via /gen/seq).
	r.Post("/gen/seq", s.handleSeqDefine)
	r.Get("/gen/seq", s.handleSeqList)
	r.Get("/gen/seq/{name}", s.handleSeqGet)
	r.Get("/gen/seq/{name}/next", s.handleSeqNext)
	r.Post("/gen/seq/{name}/reset", s.handleSeqReset)
	r.Delete("/gen/seq/{name}", s.handleSeqDelete)

	// S10: named stateful generators. Registered after the static /gen/seq
	// routes so chi's static-over-wildcard precedence keeps sequences separate.
	r.Post("/gen/{type}", s.handleGenDefine)
	r.Get("/gen/{type}", s.handleGenList)
	r.Get("/gen/{type}/{name}", s.handleGenGet)
	r.Get("/gen/{type}/{name}/next", s.handleGenNext)
	r.Delete("/gen/{type}/{name}", s.handleGenDelete)
	// /seq alias — same handlers, permanent alias.
	r.Post("/seq", s.handleSeqDefine)
	r.Get("/seq", s.handleSeqList)
	r.Get("/seq/{name}", s.handleSeqGet)
	r.Get("/seq/{name}/next", s.handleSeqNext)
	r.Post("/seq/{name}/reset", s.handleSeqReset)
	r.Delete("/seq/{name}", s.handleSeqDelete)

	// S7: FSM definitions.
	r.Post("/fsm/def", s.handleFSMDefCreate)
	r.Get("/fsm/def", s.handleFSMDefList)
	r.Get("/fsm/def/{id}", s.handleFSMDefGet)
	r.Put("/fsm/def/{id}", s.handleFSMDefReplace)
	r.Delete("/fsm/def/{id}", s.handleFSMDefDelete)
	r.Post("/fsm/def/validate", s.handleFSMDefValidate)

	// S15: dxp definitions (item 20, wave 5). GET/list added T-101 —
	// delete still not built, remaining item-20 scope.
	r.Post("/dxp/def", s.handleDxpDefCreate)
	r.Get("/dxp/def", s.handleDxpDefList)
	r.Get("/dxp/def/{id}", s.handleDxpDefGet)
	r.Post("/dxp/txn", s.handleDxpTxnCreate)
	r.Get("/dxp/txn", s.handleDxpTxnList)
	r.Get("/dxp/txn/{id}", s.handleDxpTxnGet)

	// S7: FSM machines. /walk is registered but returns 501 until S8.
	r.Post("/fsm/machine", s.handleFSMMachineCreate)
	r.Get("/fsm/machine", s.handleFSMMachineList)
	r.Get("/fsm/machine/{id}", s.handleFSMMachineGet)
	r.Patch("/fsm/machine/{id}", s.handleFSMMachinePatch)
	r.Delete("/fsm/machine/{id}", s.handleFSMMachineDelete)
	r.Post("/fsm/machine/{id}/walk", s.handleFSMMachineWalk)
	r.Get("/fsm/machine/{id}/state", s.handleFSMMachineState)
	r.Get("/fsm/machine/{id}/result", s.handleFSMMachineResult)

	// S9: event subscriptions (management surface; dispatch wired in later batches).
	r.Post("/event/def", s.handleEventCreate)
	r.Get("/event/def", s.handleEventList)
	r.Get("/event/def/{id}", s.handleEventGet)
	r.Patch("/event/def/{id}", s.handleEventUpdate)
	r.Delete("/event/def/{id}", s.handleEventDelete)
	r.Get("/event/def/{id}/log", s.handleEventLog)
	r.Post("/event/def/{id}/test", s.handleEventTest)
	r.Get("/fsm/machine/{id}/history", s.handleFSMMachineHistory)
	r.Get("/fsm/machine/{id}/transitions", s.handleFSMMachineTransitions)
	r.Get("/fsm/machine/{id}/vars", s.handleFSMMachineVars)

	// T-18: cal subsystem. Routes registered only when CalEnabled=true;
	// otherwise the four /cal/* endpoints are absent from the router and
	// return the standard chi 404. This matches the pattern used for the
	// v2 tree as a whole and for the blob subsystem.
	if s.config.CalEnabled {
		s.setupV2CalRoutes(r)
	}

	// @B: bal subsystem, same opt-in gating pattern as cal.
	if s.config.BalEnabled {
		s.setupV2BalRoutes(r)
	}

	// loc (T-118, wave 9): unconditional, no enable flag — Stage 0's
	// own pinned decision found no reason for loc to be independently
	// optional the way cal/bal are (a manager-lifecycle need, an
	// absent-on-some-instances possibility); dxp's own registry
	// already wires loc unconditionally on the same reasoning.
	s.setupV2LocRoutes(r)

	// obj (T-119, wave 10): same unconditional reasoning as loc above.
	s.setupV2ObjRoutes(r)
}

// v2Middleware attaches the X-API-Stability: experimental header and a
// deprecation notice link to every v2 response. It runs before the handler
// so the header is present even on error responses.
func (s *Server) v2Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-API-Stability", "experimental")
		w.Header().Set("X-API-Docs", "https://github.com/ha1tch/xolu/blob/main/docs/API_V2.md")
		next.ServeHTTP(w, r)
	})
}

// v2SubsystemStatus describes a single v2 subsystem in the availability map.
type v2SubsystemStatus struct {
	Available bool   `json:"available"`
	Stage     string `json:"stage,omitempty"` // development plan stage that introduced it
	Note      string `json:"note,omitempty"`
}

// handleV2Availability handles GET /api/v2.
// Returns a JSON map of which v2 subsystems are available on this server,
// allowing clients to discover capability without probing individual endpoints.
//
// Example response (apiv2-patch001, scaffold only):
//
//	{
//	  "version":     "experimental",
//	  "enabled":     true,
//	  "as_of":       "2026-06-16T...",
//	  "warning":     "API v2 is experimental. Routes and behaviour may change before xolu 2.0.",
//	  "subsystems": {
//	    "meta":       { "available": false, "stage": "S3" },
//	    "gen":        { "available": false, "stage": "S4" },
//	    "seq":        { "available": false, "stage": "S5" },
//	    "fsm":        { "available": false, "stage": "S7" },
//	    "event":      { "available": false, "stage": "S9" }
//	  }
//	}
func (s *Server) handleV2Availability(w http.ResponseWriter, r *http.Request) {
	type availabilityResponse struct {
		Version    string                       `json:"version"`
		Enabled    bool                         `json:"enabled"`
		AsOf       time.Time                    `json:"as_of"`
		Warning    string                       `json:"warning"`
		Subsystems map[string]v2SubsystemStatus `json:"subsystems"`
	}

	resp := availabilityResponse{
		Version: "experimental",
		Enabled: true,
		AsOf:    time.Now().UTC(),
		Warning: "API v2 is experimental. Routes and behaviour may change before xolu 2.0.",
		Subsystems: map[string]v2SubsystemStatus{
			"meta":  {Available: true, Stage: "S3", Note: "entity metadata sidecar"},
			"gen":   {Available: true, Stage: "S4", Note: "value generators (UUID, CUID, ULID, token, etc.)"},
			"seq":   {Available: true, Stage: "S5", Note: "named monotonic sequences"},
			"fsm":   {Available: true, Stage: "S8", Note: "finite state machine definitions, machines, and walk"},
			"event": {Available: false, Stage: "S9", Note: "event subscriptions and reactive triggers"},
		},
	}

	// Each stage flips its subsystem to available: true here
	// when it is implemented.

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
