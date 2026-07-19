// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// gc_admin_handlers.go
//
// Admin endpoints for the gc.Worker infrastructure.
// These live at /api/v1/admin/gc rather than /api/v2 because GC is
// operational infrastructure, not a versioned API feature. They are
// reachable regardless of XOLU_API_V2_ENABLED.
//
//   GET  /api/v1/admin/gc          — list registered workers and last report
//   POST /api/v1/admin/gc/{name}/run — trigger a synchronous sweep

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	gcpkg "github.com/ha1tch/xolu/pkg/gc"
)

// gcWorkerSummary is the JSON shape for one worker in the list response.
type gcWorkerSummary struct {
	Name        string        `json:"name"`
	LastReport  *gcReportJSON `json:"last_report,omitempty"`
	LastSweptAt *time.Time    `json:"last_swept_at,omitempty"`
}

// gcReportJSON is the JSON shape for a gc.Report.
type gcReportJSON struct {
	Examined    int   `json:"examined"`
	Collected   int   `json:"collected"`
	Quarantined int   `json:"quarantined,omitempty"`
	Errors      int   `json:"errors"`
	DurationMs  int64 `json:"duration_ms"`
}

func reportToJSON(r gcpkg.Report) gcReportJSON {
	return gcReportJSON{
		Examined:    r.Examined,
		Collected:   r.Collected,
		Quarantined: r.Quarantined,
		Errors:      r.Errors,
		DurationMs:  r.Duration.Milliseconds(),
	}
}

// handleGCList handles GET /api/v1/admin/gc.
// Returns a JSON array of all registered GC workers and their last report.
func (s *Server) handleGCList(w http.ResponseWriter, r *http.Request) {
	type response struct {
		Workers []gcWorkerSummary `json:"workers"`
	}

	workers := make([]gcWorkerSummary, 0, len(s.gcWorkers))
	for _, gw := range s.gcWorkers {
		rep, at := gw.LastReport()
		summary := gcWorkerSummary{Name: gw.Name()}
		if !at.IsZero() {
			rj := reportToJSON(rep)
			summary.LastReport = &rj
			summary.LastSweptAt = &at
		}
		workers = append(workers, summary)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response{Workers: workers})
}

// handleGCRun handles POST /api/v1/admin/gc/{name}/run.
// Finds the named worker and runs one sweep synchronously, returning the report.
func (s *Server) handleGCRun(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	var target *gcpkg.Worker
	for _, gw := range s.gcWorkers {
		if gw.Name() == name {
			target = gw
			break
		}
	}
	if target == nil {
		s.writeError(w, http.StatusNotFound, "XOLU-GC001",
			"no GC worker registered with name: "+name)
		return
	}

	rep, err := target.RunOnce(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "XOLU-GC002",
			"GC sweep error: "+err.Error())
		return
	}

	type response struct {
		Worker string       `json:"worker"`
		Report gcReportJSON `json:"report"`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response{
		Worker: name,
		Report: reportToJSON(rep),
	})
}
