// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/timeseries"
)

// --- Request / response types ---

type tsProvisionResponse struct {
	TenantID   string `json:"tenant_id"`
	Timeseries string `json:"timeseries"`
}

// Timeline management

type tsDefineTimelineRequest struct {
	ID            int64  `json:"id"`
	Name          string `json:"name,omitempty"`
	Dims          int    `json:"dims"`
	RetentionDays int    `json:"retention_days,omitempty"`
}

type tsTimelineResponse struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name,omitempty"`
	Dims          int        `json:"dims"`
	RetentionDays int        `json:"retention_days"`
	CreatedAt     time.Time  `json:"created_at"`
	FirstWriteAt  *time.Time `json:"first_write_at,omitempty"`
}

// Write

type tsAppendRequest struct {
	Timeline int64     `json:"timeline"`
	Dims     []uint64  `json:"dims"`
	Time     string    `json:"time"`
	Nums     []float64 `json:"nums,omitempty"`
	Payload  any       `json:"payload,omitempty"`
}

type tsBatchAppendRequest struct {
	Events []tsAppendRequest `json:"events"`
}

type tsBatchAppendResponse struct {
	Total    int `json:"total"`
	Accepted int `json:"accepted"`
	Failed   int `json:"failed"`
}

// Query

type tsEventResponse struct {
	Timeline int64     `json:"timeline"`
	Dims     []uint64  `json:"dims"`
	Time     time.Time `json:"time"`
	Nums     []float64 `json:"nums,omitempty"`
	Payload  any       `json:"payload,omitempty"`
}

type tsRangeResponse struct {
	Count  uint64            `json:"count"`
	Events []tsEventResponse `json:"events"`
}

// Aggregate

type tsAggregateRequest struct {
	Timeline int64    `json:"timeline"`
	Dims     []uint64 `json:"dims"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	NumField int      `json:"num_field"`
	Function string   `json:"function"`
	Interval string   `json:"interval,omitempty"`
}

type tsBucketResponse struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
	Count uint64    `json:"count"`
}

type tsAggregateResponse struct {
	Timeline int64  `json:"timeline"`
	NumField int    `json:"num_field"`
	Function string `json:"function"`
	Interval string `json:"interval,omitempty"`
	// Bucketed result
	Buckets []tsBucketResponse `json:"buckets,omitempty"`
	// Scalar result
	Value *float64   `json:"value,omitempty"`
	Count *uint64    `json:"count,omitempty"`
	From  *time.Time `json:"from,omitempty"`
	To    *time.Time `json:"to,omitempty"`
}

// Retention

type tsRetentionResponse struct {
	DefaultRetentionDays int                   `json:"default_retention_days"`
	Timelines            []tsTimelineRetention `json:"timelines"`
}

type tsTimelineRetention struct {
	ID            int64  `json:"id"`
	Name          string `json:"name,omitempty"`
	RetentionDays int    `json:"retention_days"`
}

// Stats
type tsStatsResponse struct {
	TenantID  string `json:"tenant_id"`
	Timelines int    `json:"timelines"`
	DiskBytes int64  `json:"disk_bytes"`
}

type tsTimelineStatsResponse struct {
	TimelineID             int64      `json:"timeline_id"`
	Name                   string     `json:"name,omitempty"`
	TotalEvents            int64      `json:"total_events"`
	TotalEventsApproximate bool       `json:"total_events_approximate"`
	OldestEvent            *time.Time `json:"oldest_event,omitempty"`
	NewestEvent            *time.Time `json:"newest_event,omitempty"`
}

// --- Handler helpers ---

// tsStore retrieves the timeseries store for the current request's tenant.
// Returns nil and writes the appropriate error response if unavailable.
func (s *Server) tsStore(w http.ResponseWriter, r *http.Request, tenantIDStr string) timeseries.Store {
	if s.tsManager == nil {
		s.writeError(w, http.StatusForbidden, xoluerr.Code("XOLU-TS002"), "timeseries not enabled")
		return nil
	}
	tid := getTenantIDNumeric(r.Context())
	if tid == 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.Code("XOLU-TS003"), fmt.Sprintf("tenant %s not found", tenantIDStr))
		return nil
	}
	if !s.tsManager.IsProvisioned(tid) {
		s.writeError(w, http.StatusNotFound, xoluerr.Code("XOLU-TS003"), fmt.Sprintf("tenant %s not provisioned for timeseries", tenantIDStr))
		return nil
	}
	store, err := s.tsManager.StoreFor(tid)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.Code("XOLU-TS013"), err.Error())
		return nil
	}
	return store
}

// parseTSTime parses an ISO 8601 timestamp string into a UTC instant.
//
// The result is normalised to UTC. This does NOT change stored keys — the codec
// encodes uint64(ts.UnixNano()), which is zone-invariant, so an input carrying an
// offset (e.g. -03:00) already stored correctly. Normalisation makes the returned
// time.Time consistent on read-back and in comparisons, matching xolu's UTC-instant
// invariant (docs/TIME_HANDLING.md).
//
// NOTE (input policy): unlike cal's ot.Parse, this accepts a zone-naive string
// (no Z, no offset) and interprets it as UTC, for backward compatibility with
// existing ts clients. The divergence is recorded in docs/KNOWN_ISSUES.md; making
// ts reject zone-naive input like cal does would be a breaking API change.
func parseTSTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp %q (XOLU-TS005)", s)
	}
	t = t.UTC()
	if t.Before(time.Unix(0, 0).UTC()) {
		return time.Time{}, fmt.Errorf("timestamp before Unix epoch (XOLU-TS005)")
	}
	return t, nil
}

// parseInterval converts a human-readable interval string to time.Duration.
func parseInterval(s string) (time.Duration, error) {
	switch s {
	case "1m":
		return time.Minute, nil
	case "5m":
		return 5 * time.Minute, nil
	case "15m":
		return 15 * time.Minute, nil
	case "30m":
		return 30 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	case "6h":
		return 6 * time.Hour, nil
	case "12h":
		return 12 * time.Hour, nil
	case "1d":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid interval %q (XOLU-TS010): valid values are 1m 5m 15m 30m 1h 6h 12h 1d 7d", s)
}

// parseDims parses a comma-separated list of uint64 dimension values.
func parseDims(s string) ([]uint64, error) {
	parts := strings.Split(s, ",")
	out := make([]uint64, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.ParseUint(strings.TrimSpace(p), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid dimension value %q: %w", p, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// eventToResponse converts a timeseries.Event to the HTTP response type.
func eventToResponse(e timeseries.Event) tsEventResponse {
	r := tsEventResponse{
		Timeline: int64(e.Timeline),
		Dims:     e.Dims,
		Time:     e.Time,
		Nums:     e.Nums,
	}
	if len(e.Payload) > 0 {
		var v any
		if err := json.Unmarshal(e.Payload, &v); err == nil {
			r.Payload = v
		} else {
			r.Payload = string(e.Payload)
		}
	}
	return r
}

// timelineToResponse converts a TimelineConfig to the HTTP response type.
func timelineToResponse(id timeseries.TimelineID, cfg timeseries.TimelineConfig) tsTimelineResponse {
	r := tsTimelineResponse{
		ID:            int64(id),
		Name:          cfg.Name,
		Dims:          int(cfg.Dims),
		RetentionDays: cfg.RetentionDays,
		CreatedAt:     cfg.CreatedAt,
	}
	if !cfg.FirstWriteAt.IsZero() {
		fw := cfg.FirstWriteAt
		r.FirstWriteAt = &fw
	}
	return r
}

// encodePayload marshals the payload field from a write request to bytes.
func encodePayload(v any) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// classifyTSError maps timeseries error messages to XOLU-TS error codes.
func classifyTSError(err error) string {
	msg := err.Error()
	if !strings.Contains(msg, "XOLU-TS") {
		return string(xoluerr.ErrTSInternal)
	}
	// Walk the full set of typed TS codes; return the first one found in the
	// error message string. This is still string-based for errors produced by
	// fmt.Errorf (which embed the code in the message), but the codes are now
	// referenced via typed constants rather than bare literals.
	for _, code := range []xoluerr.Code{
		xoluerr.ErrTSNotAvailable, xoluerr.ErrTSNotEnabled, xoluerr.ErrTSNotProvisioned,
		xoluerr.ErrTSInvalidTrigger, xoluerr.ErrTSInvalidTimestamp, xoluerr.ErrTSBatchTooLarge,
		xoluerr.ErrTSMissingField, xoluerr.ErrTSInvalidAggFunc, xoluerr.ErrTSInvalidAggField,
		xoluerr.ErrTSInvalidInterval, xoluerr.ErrTSRangeTooWide, xoluerr.ErrTSLimitExceeded,
		xoluerr.ErrTSInternal, xoluerr.ErrTSRetentionFailed, xoluerr.ErrTSProvisionFailed,
		xoluerr.ErrTSDimsImmutable, xoluerr.ErrTSNaNValue, xoluerr.ErrTSReservedID,
		xoluerr.ErrTSBucketLimit, xoluerr.ErrTSSystemScopeID,
	} {
		if strings.Contains(msg, string(code)) {
			return string(code)
		}
	}
	return string(xoluerr.ErrTSInternal)
}

// --- Handlers ---

// --- Limit helpers ---

// tsLimits holds the resolved per-request guardrails for a timeseries query.
type tsLimits struct {
	timeout       time.Duration
	maxEvents     int
	maxScanEvents int
	maxRangeDays  int
	maxBatchSize  int
	maxRespBytes  int
	maxBuckets    int
}

// tsQueryLimits resolves effective limits from config, applying defaults for
// any zero values (matching the pattern used by OQL/Sulpher handlers).
func (s *Server) tsQueryLimits() tsLimits {
	cfg := s.config
	l := tsLimits{
		timeout:       time.Duration(cfg.TSQueryTimeoutSecs) * time.Second,
		maxEvents:     cfg.TSMaxQueryEvents,
		maxScanEvents: cfg.TSMaxScanEvents,
		maxRangeDays:  cfg.TSMaxRangeDays,
		maxBatchSize:  cfg.TSMaxBatchSize,
		maxRespBytes:  cfg.TSMaxResponseBytes,
		maxBuckets:    cfg.TSMaxAggregateBuckets,
	}
	if l.timeout <= 0 {
		l.timeout = 30 * time.Second
	}
	if l.maxEvents <= 0 {
		l.maxEvents = 10000
	}
	if l.maxScanEvents <= 0 {
		l.maxScanEvents = 500000
	}
	if l.maxRangeDays <= 0 {
		l.maxRangeDays = 366
	}
	if l.maxBatchSize <= 0 {
		l.maxBatchSize = 5000
	}
	if l.maxRespBytes <= 0 {
		l.maxRespBytes = 10 * 1024 * 1024
	}
	if l.maxBuckets <= 0 {
		l.maxBuckets = 10000
	}
	return l
}

// tsWriteJSON serialises data and enforces the response byte limit before
// writing to the client. Returns true if the response was written successfully.
func (s *Server) tsWriteJSON(w http.ResponseWriter, status int, data any, maxBytes int) bool {
	encoded, err := json.Marshal(data)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.Code("XOLU-TS013"), "failed to encode response")
		return false
	}
	if len(encoded) > maxBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, xoluerr.Code("XOLU-TS013"),
			fmt.Sprintf("response too large: %d bytes (max %d)", len(encoded), maxBytes))
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
	return true
}

// HandleTSProvision provisions timeseries for a tenant.
//
//	POST /api/v1/tenant/{tenant_id}/ts/provision
func (s *Server) HandleTSProvision(w http.ResponseWriter, r *http.Request) {
	if s.tsManager == nil {
		s.writeError(w, http.StatusForbidden, xoluerr.Code("XOLU-TS002"), "timeseries not enabled")
		return
	}
	tenantID := chi.URLParam(r, "tenant_id")
	tid := getTenantIDNumeric(r.Context())
	if tid == 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.Code("XOLU-TS003"), fmt.Sprintf("tenant %s not found", tenantID))
		return
	}
	if err := s.tsManager.Provision(r.Context(), tid, tenantID); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.Code("XOLU-TS015"), err.Error())
		return
	}
	s.writeJSON(w, http.StatusCreated, tsProvisionResponse{TenantID: tenantID, Timeseries: "enabled"})
}

// HandleTSDefineTimeline defines or updates a timeline.
//
//	POST /api/v1/tenant/{tenant_id}/ts/timelines
func (s *Server) HandleTSDefineTimeline(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	var req tsDefineTimelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS013"), "invalid request body")
		return
	}
	tlID, err := timeseries.TimelineIDFromJSON(req.ID)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSReservedID, err.Error())
		return
	}
	// D-006: validate dims against [MinDims, MaxDims] on the raw int BEFORE the
	// uint8 narrowing below. Otherwise an out-of-range value whose low byte
	// lands in [1,5] (e.g. 257→1 … 261→5) is silently accepted and the timeline
	// is created with a different dimension count than requested.
	if req.Dims < int(timeseries.MinDims) || req.Dims > int(timeseries.MaxDims) {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS013"),
			fmt.Sprintf("dims must be %d–%d, got %d", timeseries.MinDims, timeseries.MaxDims, req.Dims))
		return
	}
	cfg := timeseries.TimelineConfig{
		Name:          req.Name,
		Dims:          uint8(req.Dims),
		RetentionDays: req.RetentionDays,
	}
	if err := store.DefineTimeline(tlID, cfg); err != nil {
		code := classifyTSError(err)
		s.writeError(w, http.StatusConflict, xoluerr.Code(code), err.Error())
		return
	}
	cfg, _ = store.Timeline(tlID)
	s.writeJSON(w, http.StatusCreated, timelineToResponse(tlID, cfg))
}

// HandleTSListTimelines returns all defined timelines.
//
//	GET /api/v1/tenant/{tenant_id}/ts/timelines
func (s *Server) HandleTSListTimelines(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	ids := store.Timelines()
	result := make([]tsTimelineResponse, 0, len(ids))
	for _, id := range ids {
		cfg, _ := store.Timeline(id)
		result = append(result, timelineToResponse(id, cfg))
	}
	s.writeJSON(w, http.StatusOK, result)
}

// HandleTSGetTimeline returns a single timeline.
//
//	GET /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}
func (s *Server) HandleTSGetTimeline(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	tidStr := chi.URLParam(r, "timeline_id")
	tidInt, err := strconv.ParseUint(tidStr, 10, 32)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"), fmt.Sprintf("invalid timeline_id %q", tidStr))
		return
	}
	tid := timeseries.TimelineID(tidInt)
	cfg, ok := store.Timeline(tid)
	if !ok {
		s.writeError(w, http.StatusNotFound, xoluerr.Code("XOLU-TS004"), fmt.Sprintf("timeline %d not defined", tidInt))
		return
	}
	s.writeJSON(w, http.StatusOK, timelineToResponse(tid, cfg))
}

// HandleTSUpdateTimeline updates a timeline's mutable fields.
//
//	PATCH /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}
func (s *Server) HandleTSUpdateTimeline(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	tidStr := chi.URLParam(r, "timeline_id")
	tidInt, err := strconv.ParseUint(tidStr, 10, 32)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"), fmt.Sprintf("invalid timeline_id %q", tidStr))
		return
	}
	var req tsDefineTimelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS013"), "invalid request body")
		return
	}
	tid := timeseries.TimelineID(tidInt)
	cfg := timeseries.TimelineConfig{
		Name:          req.Name,
		RetentionDays: req.RetentionDays,
	}
	if err := store.UpdateTimeline(tid, cfg); err != nil {
		code := classifyTSError(err)
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "XOLU-TS004") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), string(xoluerr.ErrTSDimsImmutable)) {
			status = http.StatusConflict
		}
		s.writeError(w, status, xoluerr.Code(code), err.Error())
		return
	}
	cfg, _ = store.Timeline(tid)
	s.writeJSON(w, http.StatusOK, timelineToResponse(tid, cfg))
}

// HandleTSAppend appends a single event.
//
//	POST /api/v1/tenant/{tenant_id}/ts/events
func (s *Server) HandleTSAppend(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	var req tsAppendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS013"), "invalid request body")
		return
	}
	tlID, tlErr := timeseries.TimelineIDFromJSON(req.Timeline)
	if tlErr != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSReservedID, tlErr.Error())
		return
	}
	ts, err := parseTSTime(req.Time)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), err.Error())
		return
	}
	e := timeseries.Event{
		Timeline: tlID,
		Dims:     req.Dims,
		Time:     ts,
		Nums:     req.Nums,
		Payload:  encodePayload(req.Payload),
	}
	if err := store.Append(r.Context(), e); err != nil {
		code := classifyTSError(err)
		s.writeError(w, http.StatusBadRequest, xoluerr.Code(code), err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// HandleTSBatchAppend appends a batch of events atomically.
//
//	POST /api/v1/tenant/{tenant_id}/ts/events/batch
func (s *Server) HandleTSBatchAppend(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	var req tsBatchAppendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS013"), "invalid request body")
		return
	}
	limits := s.tsQueryLimits()
	if len(req.Events) > limits.maxBatchSize {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS006"),
			fmt.Sprintf("batch size %d exceeds max %d", len(req.Events), limits.maxBatchSize))
		return
	}
	events := make([]timeseries.Event, 0, len(req.Events))
	for i, re := range req.Events {
		ts, err := parseTSTime(re.Time)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), fmt.Sprintf("event[%d]: %s", i, err))
			return
		}
		reTlID, reTlErr := timeseries.TimelineIDFromJSON(re.Timeline)
		if reTlErr != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSReservedID, fmt.Sprintf("event[%d]: %s", i, reTlErr))
			return
		}
		events = append(events, timeseries.Event{
			Timeline: reTlID,
			Dims:     re.Dims,
			Time:     ts,
			Nums:     re.Nums,
			Payload:  encodePayload(re.Payload),
		})
	}
	accepted, err := store.AppendBatch(r.Context(), events, limits.maxBatchSize)
	if err != nil {
		code := classifyTSError(err)
		s.writeError(w, http.StatusBadRequest, xoluerr.Code(code), err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, tsBatchAppendResponse{
		Total:    len(events),
		Accepted: accepted,
		Failed:   len(events) - accepted,
	})
}

// HandleTSQueryRange returns events in a time range.
//
//	GET /api/v1/tenant/{tenant_id}/ts/events
func (s *Server) HandleTSQueryRange(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	q := r.URL.Query()

	tidInt, err := strconv.ParseUint(q.Get("timeline"), 10, 32)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"), "missing or invalid timeline parameter")
		return
	}
	dims, err := parseDims(q.Get("dims"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS007"), err.Error())
		return
	}
	from, err := parseTSTime(q.Get("from"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), err.Error())
		return
	}
	to, err := parseTSTime(q.Get("to"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), err.Error())
		return
	}
	limits := s.tsQueryLimits()

	if to.Sub(from) > time.Duration(limits.maxRangeDays)*24*time.Hour {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS011"),
			fmt.Sprintf("query range exceeds %d days", limits.maxRangeDays))
		return
	}

	limit := 1000
	if ls := q.Get("limit"); ls != "" {
		limit, err = strconv.Atoi(ls)
		if err != nil || limit < 1 {
			limit = 1000
		}
	}
	if limit > limits.maxEvents {
		limit = limits.maxEvents
	}
	order := q.Get("order")
	if order == "" {
		order = "asc"
	}

	ctx, cancel := context.WithTimeout(r.Context(), limits.timeout)
	defer cancel()

	rq := timeseries.RangeQuery{
		Timeline:      timeseries.TimelineID(tidInt),
		Dims:          dims,
		From:          from,
		To:            to,
		Limit:         limit,
		Order:         order,
		MaxScanEvents: limits.maxScanEvents,
	}
	events, err := store.QueryRange(ctx, rq)
	if err != nil {
		code := classifyTSError(err)
		status := http.StatusBadRequest
		if ctx.Err() != nil {
			status = http.StatusGatewayTimeout
			code = "XOLU-TS013"
		}
		s.writeError(w, status, xoluerr.Code(code), err.Error())
		return
	}
	resp := tsRangeResponse{Count: uint64(len(events)), Events: make([]tsEventResponse, len(events))}
	for i, e := range events {
		resp.Events[i] = eventToResponse(e)
	}
	s.tsWriteJSON(w, http.StatusOK, resp, limits.maxRespBytes)
}

// HandleTSQueryRangePost is the POST equivalent of HandleTSQueryRange.
// It accepts the same parameters as a JSON body instead of query-string values,
// which is more ergonomic for complex queries and avoids URL-length limits.
//
//	POST /api/v1/tenant/{tenant_id}/ts/query/range
//
// Request body:
//
//	{
//	  "timeline": 1,
//	  "dims":     [42],
//	  "from":     "2026-01-01T00:00:00Z",
//	  "to":       "2026-01-02T00:00:00Z",
//	  "limit":    1000,   // optional; default 1000, max TSMaxQueryEvents
//	  "order":    "asc"   // optional; "asc" (default) or "desc"
//	}
func (s *Server) HandleTSQueryRangePost(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}

	var body struct {
		Timeline uint64   `json:"timeline"`
		Dims     []uint64 `json:"dims"`
		From     string   `json:"from"`
		To       string   `json:"to"`
		Limit    int      `json:"limit"`
		Order    string   `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"), "invalid JSON body: "+err.Error())
		return
	}

	if body.Timeline == 0 || body.Timeline > uint64(timeseries.MaxTimelineID) {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"), "missing or invalid timeline field")
		return
	}
	if len(body.Dims) == 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS007"), "dims must be a non-empty array")
		return
	}

	from, err := parseTSTime(body.From)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), "invalid from: "+err.Error())
		return
	}
	to, err := parseTSTime(body.To)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), "invalid to: "+err.Error())
		return
	}

	limits := s.tsQueryLimits()

	if to.Sub(from) > time.Duration(limits.maxRangeDays)*24*time.Hour {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS011"),
			fmt.Sprintf("query range exceeds %d days", limits.maxRangeDays))
		return
	}

	limit := body.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit > limits.maxEvents {
		limit = limits.maxEvents
	}

	order := body.Order
	if order == "" {
		order = "asc"
	}

	ctx, cancel := context.WithTimeout(r.Context(), limits.timeout)
	defer cancel()

	rq := timeseries.RangeQuery{
		Timeline:      timeseries.TimelineID(body.Timeline),
		Dims:          body.Dims,
		From:          from,
		To:            to,
		Limit:         limit,
		Order:         order,
		MaxScanEvents: limits.maxScanEvents,
	}
	events, err := store.QueryRange(ctx, rq)
	if err != nil {
		code := classifyTSError(err)
		status := http.StatusBadRequest
		if ctx.Err() != nil {
			status = http.StatusGatewayTimeout
			code = "XOLU-TS013"
		}
		s.writeError(w, status, xoluerr.Code(code), err.Error())
		return
	}
	resp := tsRangeResponse{Count: uint64(len(events)), Events: make([]tsEventResponse, len(events))}
	for i, e := range events {
		resp.Events[i] = eventToResponse(e)
	}
	s.tsWriteJSON(w, http.StatusOK, resp, limits.maxRespBytes)
}

// HandleTSLatest returns the N most recent events.
//
//	GET /api/v1/tenant/{tenant_id}/ts/events/latest
func (s *Server) HandleTSLatest(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	q := r.URL.Query()

	tidInt, err := strconv.ParseUint(q.Get("timeline"), 10, 32)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"), "missing or invalid timeline parameter")
		return
	}
	dims, err := parseDims(q.Get("dims"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS007"), err.Error())
		return
	}
	limits := s.tsQueryLimits()

	n := 10
	if ns := q.Get("n"); ns != "" {
		n, err = strconv.Atoi(ns)
		if err != nil || n < 1 {
			n = 10
		}
	}
	if n > limits.maxEvents {
		n = limits.maxEvents
	}

	ctx, cancel := context.WithTimeout(r.Context(), limits.timeout)
	defer cancel()

	lq := timeseries.LatestQuery{
		Timeline: timeseries.TimelineID(tidInt),
		Dims:     dims,
		N:        n,
	}
	// Optional time bounds — parse if present, ignore gracefully if absent.
	if fromStr := q.Get("from"); fromStr != "" {
		if t, err := parseTSTime(fromStr); err == nil {
			lq.From = t
		}
	}
	if toStr := q.Get("to"); toStr != "" {
		if t, err := parseTSTime(toStr); err == nil {
			lq.To = t
		}
	}
	events, err := store.Latest(ctx, lq)
	if err != nil {
		code := classifyTSError(err)
		status := http.StatusBadRequest
		if ctx.Err() != nil {
			status = http.StatusGatewayTimeout
			code = "XOLU-TS013"
		}
		s.writeError(w, status, xoluerr.Code(code), err.Error())
		return
	}
	resp := tsRangeResponse{Count: uint64(len(events)), Events: make([]tsEventResponse, len(events))}
	for i, e := range events {
		resp.Events[i] = eventToResponse(e)
	}
	s.tsWriteJSON(w, http.StatusOK, resp, limits.maxRespBytes)
}

// HandleTSAggregate computes an aggregate over a numeric field.
//
//	POST /api/v1/tenant/{tenant_id}/ts/aggregate
func (s *Server) HandleTSAggregate(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	var req tsAggregateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS013"), "invalid request body")
		return
	}
	from, err := parseTSTime(req.From)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), err.Error())
		return
	}
	to, err := parseTSTime(req.To)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), err.Error())
		return
	}

	var interval time.Duration
	if req.Interval != "" {
		interval, err = parseInterval(req.Interval)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS010"), err.Error())
			return
		}
	}

	limits := s.tsQueryLimits()

	if to.Sub(from) > time.Duration(limits.maxRangeDays)*24*time.Hour {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS011"),
			fmt.Sprintf("query range exceeds %d days", limits.maxRangeDays))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), limits.timeout)
	defer cancel()

	// D-006: validate num_field against its range on the raw int BEFORE the
	// uint8 narrowing below. Otherwise an out-of-range value whose low byte
	// lands in [0,6] (e.g. 256→0, 262→6) silently aliases an in-range field
	// instead of being rejected.
	// D-006: validate num_field against its range on the raw int BEFORE the
	// uint8 narrowing below. Otherwise an out-of-range value whose low byte
	// lands in [0,6] (e.g. 256→0, 262→6) silently aliases an in-range field
	// instead of being rejected.
	if req.NumField < 0 || req.NumField > 6 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidAggField,
			fmt.Sprintf("num_field must be 0–6, got %d", req.NumField))
		return
	}

	aqTlID, aqTlErr := timeseries.TimelineIDFromJSON(req.Timeline)
	if aqTlErr != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSReservedID, aqTlErr.Error())
		return
	}
	aq := timeseries.AggregateQuery{
		Timeline:      aqTlID,
		Dims:          req.Dims,
		From:          from,
		To:            to,
		NumField:      uint8(req.NumField),
		Function:      req.Function,
		Interval:      interval,
		MaxScanEvents: limits.maxScanEvents,
		MaxBuckets:    limits.maxBuckets,
	}
	buckets, err := store.Aggregate(ctx, aq)
	if err != nil {
		code := classifyTSError(err)
		status := http.StatusBadRequest
		if ctx.Err() != nil {
			status = http.StatusGatewayTimeout
			code = "XOLU-TS013"
		}
		s.writeError(w, status, xoluerr.Code(code), err.Error())
		return
	}

	resp := tsAggregateResponse{
		Timeline: req.Timeline,
		NumField: req.NumField,
		Function: req.Function,
		Interval: req.Interval,
	}

	if interval > 0 {
		resp.Buckets = make([]tsBucketResponse, len(buckets))
		for i, b := range buckets {
			resp.Buckets[i] = tsBucketResponse{Time: b.Time, Value: b.Value, Count: b.Count}
		}
	} else {
		// Scalar result.
		var val float64
		var count uint64
		if len(buckets) > 0 {
			val = buckets[0].Value
			count = buckets[0].Count
		}
		resp.Value = &val
		resp.Count = &count
		resp.From = &from
		resp.To = &to
	}
	s.tsWriteJSON(w, http.StatusOK, resp, limits.maxRespBytes)
}

// HandleTSGetRetention returns the retention configuration.
//
//	GET /api/v1/tenant/{tenant_id}/ts/retention
func (s *Server) HandleTSGetRetention(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	ids := store.Timelines()
	resp := tsRetentionResponse{
		DefaultRetentionDays: store.DefaultRetentionDays(),
		Timelines:            make([]tsTimelineRetention, 0, len(ids)),
	}
	for _, id := range ids {
		cfg, _ := store.Timeline(id)
		resp.Timelines = append(resp.Timelines, tsTimelineRetention{
			ID:            int64(id),
			Name:          cfg.Name,
			RetentionDays: cfg.RetentionDays,
		})
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// HandleTSStats returns store-level diagnostics.
//
//	GET /api/v1/tenant/{tenant_id}/ts/stats
func (s *Server) HandleTSStats(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant_id") // for response only
	store := s.tsStore(w, r, tenantIDStr)
	if store == nil {
		return
	}
	stats, err := store.Stats(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.Code("XOLU-TS013"), err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, tsStatsResponse{
		TenantID:  tenantIDStr,
		Timelines: stats.Timelines,
		DiskBytes: stats.DiskBytes,
	})
}

// HandleTSTimelineStats returns per-timeline diagnostics.
//
//	GET /api/v1/tenant/{tenant_id}/ts/stats/{timeline_id}
func (s *Server) HandleTSTimelineStats(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	tidStr := chi.URLParam(r, "timeline_id")
	tidInt, err := strconv.ParseUint(tidStr, 10, 32)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"), fmt.Sprintf("invalid timeline_id %q", tidStr))
		return
	}
	tid := timeseries.TimelineID(tidInt)
	cfg, ok := store.Timeline(tid)
	if !ok {
		s.writeError(w, http.StatusNotFound, xoluerr.Code("XOLU-TS004"), fmt.Sprintf("timeline %d not defined", tidInt))
		return
	}
	stats, err := store.TimelineStats(r.Context(), tid)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.Code("XOLU-TS013"), err.Error())
		return
	}
	resp := tsTimelineStatsResponse{
		TimelineID:             int64(tid),
		Name:                   cfg.Name,
		TotalEvents:            stats.TotalEvents,
		TotalEventsApproximate: stats.TotalEventsApproximate,
	}
	if !stats.OldestEvent.IsZero() {
		t := stats.OldestEvent
		resp.OldestEvent = &t
	}
	if !stats.NewestEvent.IsZero() {
		t := stats.NewestEvent
		resp.NewestEvent = &t
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Request / response types for the new endpoints
// ---------------------------------------------------------------------------

type tsPatchRetentionRequest struct {
	DefaultRetentionDays int `json:"default_retention_days"`
}

type tsRangeAggregateRequest struct {
	Timeline int64    `json:"timeline"`
	Dims     []uint64 `json:"dims"`
	From     string   `json:"from"`
	To       string   `json:"to"`
}

type tsRangeAggregateResponse struct {
	Timeline int64      `json:"timeline"`
	Count    uint64     `json:"count"`
	Fields   [7]bool    `json:"fields"`
	Sums     [7]float64 `json:"sums"`
	Avgs     [7]float64 `json:"avgs"`
	Mins     [7]float64 `json:"mins"`
	Maxs     [7]float64 `json:"maxs"`
}

type tsFullAggregateRequest struct {
	Timeline       int64     `json:"timeline"`
	Dims           []uint64  `json:"dims"`
	From           string    `json:"from"`
	To             string    `json:"to"`
	Quantiles      []float64 `json:"quantiles"`       // e.g. [0.5, 0.9, 0.99]
	QuantileFields []uint8   `json:"quantile_fields"` // nil = all fields
}

// tsFullAggregateResponse mirrors RangeFullResult for JSON serialisation.
// Quantiles[i] is the slice of quantile estimates for num field i, in the same
// order as the request's Quantiles array. Nil when field i had no events or
// was not requested. Fields, Sums, Avgs, Mins, Maxs follow the same
// seven-element convention as tsRangeAggregateResponse.
type tsFullAggregateResponse struct {
	Timeline  int64        `json:"timeline"`
	Count     uint64       `json:"count"`
	Fields    [7]bool      `json:"fields"`
	Sums      [7]float64   `json:"sums"`
	Avgs      [7]float64   `json:"avgs"`
	Mins      [7]float64   `json:"mins"`
	Maxs      [7]float64   `json:"maxs"`
	Quantiles [7][]float64 `json:"quantiles"` // nil inner slice = not requested or no events
}

// ---------------------------------------------------------------------------
// PATCH /api/v1/tenant/{tenant_id}/ts/retention
//
// Updates the store-level default retention for the tenant. Per-timeline
// retention is updated via PATCH /ts/timelines/{timeline_id}.
// ---------------------------------------------------------------------------

func (s *Server) HandleTSPatchRetention(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	var req tsPatchRetentionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"), "invalid request body")
		return
	}
	if err := store.SetDefaultRetentionDays(req.DefaultRetentionDays); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.Code("XOLU-TS013"),
			fmt.Sprintf("failed to update retention: %s", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"default_retention_days": req.DefaultRetentionDays,
		"status":                 "updated",
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/tenant/{tenant_id}/ts/range_aggregate
//
// Computes count, sum, avg, min, max for all seven numeric fields in a single
// Pebble scan pass. More efficient than multiple /aggregate calls when several
// fields are needed. Does not support time bucketing; use /aggregate for that.
// ---------------------------------------------------------------------------

func (s *Server) HandleTSRangeAggregate(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	var req tsRangeAggregateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"), "invalid request body")
		return
	}
	from, err := parseTSTime(req.From)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), err.Error())
		return
	}
	to, err := parseTSTime(req.To)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), err.Error())
		return
	}
	limits := s.tsQueryLimits()
	if to.Sub(from) > time.Duration(limits.maxRangeDays)*24*time.Hour {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS011"),
			fmt.Sprintf("query range exceeds %d days", limits.maxRangeDays))
		return
	}
	raTlID, raTlErr := timeseries.TimelineIDFromJSON(req.Timeline)
	if raTlErr != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSReservedID, raTlErr.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), limits.timeout)
	defer cancel()

	res, err := store.RangeAggregate(ctx, timeseries.RangeAllQuery{
		Timeline:      raTlID,
		Dims:          req.Dims,
		From:          from,
		To:            to,
		MaxScanEvents: limits.maxScanEvents,
	})
	if err != nil {
		code := classifyTSError(err)
		status := http.StatusBadRequest
		if ctx.Err() != nil {
			status = http.StatusGatewayTimeout
			code = "XOLU-TS013"
		}
		s.writeError(w, status, xoluerr.Code(code), err.Error())
		return
	}
	resp := tsRangeAggregateResponse{
		Timeline: req.Timeline,
		Count:    res.Count,
		Fields:   res.Fields,
		Sums:     res.Sums,
		Avgs:     res.Avgs,
		Mins:     res.Mins,
		Maxs:     res.Maxs,
	}
	s.tsWriteJSON(w, http.StatusOK, resp, limits.maxRespBytes)
}

// ---------------------------------------------------------------------------
// POST /api/v1/tenant/{tenant_id}/ts/full_aggregate
//
// Single-pass combination of exact statistics (sum/avg/min/max/count for all
// seven fields) and approximate quantile estimates for selected fields.
//
// If quantiles is empty or absent, this is equivalent to /ts/range_aggregate
// with no additional cost — no t-digest is allocated.
//
// quantile_fields selects which numeric fields (0–6) get quantile estimates.
// If absent or null, quantiles are computed for all seven fields.
// ---------------------------------------------------------------------------

func (s *Server) HandleTSFullAggregate(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	var req tsFullAggregateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"), "invalid request body")
		return
	}
	// Validate quantile values.
	for _, qv := range req.Quantiles {
		if qv < 0 || qv > 1 {
			s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS009"),
				fmt.Sprintf("quantile %g out of range [0, 1]", qv))
			return
		}
	}
	from, err := parseTSTime(req.From)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), err.Error())
		return
	}
	to, err := parseTSTime(req.To)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS005"), err.Error())
		return
	}
	limits := s.tsQueryLimits()
	if to.Sub(from) > time.Duration(limits.maxRangeDays)*24*time.Hour {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS011"),
			fmt.Sprintf("query range exceeds %d days", limits.maxRangeDays))
		return
	}
	rfTlID, rfTlErr := timeseries.TimelineIDFromJSON(req.Timeline)
	if rfTlErr != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSReservedID, rfTlErr.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), limits.timeout)
	defer cancel()

	res, err := store.RangeFullAggregate(ctx, timeseries.RangeFullQuery{
		RangeAllQuery: timeseries.RangeAllQuery{
			Timeline:      rfTlID,
			Dims:          req.Dims,
			From:          from,
			To:            to,
			MaxScanEvents: limits.maxScanEvents,
		},
		Quantiles:      req.Quantiles,
		QuantileFields: req.QuantileFields,
	})
	if err != nil {
		code := classifyTSError(err)
		status := http.StatusBadRequest
		if ctx.Err() != nil {
			status = http.StatusGatewayTimeout
			code = "XOLU-TS013"
		}
		s.writeError(w, status, xoluerr.Code(code), err.Error())
		return
	}
	agg := res.Aggregate
	resp := tsFullAggregateResponse{
		Timeline:  req.Timeline,
		Count:     agg.Count,
		Fields:    agg.Fields,
		Sums:      agg.Sums,
		Avgs:      agg.Avgs,
		Mins:      agg.Mins,
		Maxs:      agg.Maxs,
		Quantiles: res.Quantiles,
	}
	s.tsWriteJSON(w, http.StatusOK, resp, limits.maxRespBytes)
}

// --- Per-timeline sync configuration ---

// tsSyncResponse is the body returned by GET /ts/timelines/{timeline_id}/sync.
type tsSyncResponse struct {
	TimelineID int64 `json:"timeline_id"`
	NoSync     bool `json:"nosync"`
}

func (s *Server) tsParseSyncTimeline(w http.ResponseWriter, r *http.Request) (timeseries.Store, timeseries.TimelineID, bool) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return nil, 0, false
	}
	tidInt, err := strconv.ParseUint(chi.URLParam(r, "timeline_id"), 10, 32)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"),
			fmt.Sprintf("invalid timeline_id %q", chi.URLParam(r, "timeline_id")))
		return nil, 0, false
	}
	tid := timeseries.TimelineID(tidInt)
	if _, ok := store.Timeline(tid); !ok {
		s.writeError(w, http.StatusNotFound, xoluerr.Code("XOLU-TS004"),
			fmt.Sprintf("timeline %d not defined", tidInt))
		return nil, 0, false
	}
	return store, tid, true
}

// HandleTSSyncGet returns the current nosync setting for a timeline.
//
//	GET /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/sync
func (s *Server) HandleTSSyncGet(w http.ResponseWriter, r *http.Request) {
	store, tid, ok := s.tsParseSyncTimeline(w, r)
	if !ok {
		return
	}
	cfg := store.WriteConfig(tid)
	s.writeJSON(w, http.StatusOK, tsSyncResponse{
		TimelineID: int64(tid),
		NoSync:     cfg.NoSync,
	})
}

// HandleTSSyncOn restores synchronous write mode for a timeline (NoSync=false).
// AppendBatch will again wait for WAL fsync before returning. This is the
// default mode and provides crash durability.
//
//	POST /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/sync/on
func (s *Server) HandleTSSyncOn(w http.ResponseWriter, r *http.Request) {
	store, tid, ok := s.tsParseSyncTimeline(w, r)
	if !ok {
		return
	}
	cur := store.WriteConfig(tid)
	cur.NoSync = false
	if err := store.SetWriteConfig(tid, cur); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrTSWriteConfigSaveFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, tsSyncResponse{TimelineID: int64(tid), NoSync: false})
}

// HandleTSSyncOff enables nosync mode for a timeline (NoSync=true).
// AppendBatch will no longer wait for WAL fsync before returning. Data loss
// is possible if the process crashes before the OS flushes the WAL to disk.
// The loss window is bounded by the kernel dirty-page writeback interval,
// typically under one second.
//
//	POST /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/sync/off
func (s *Server) HandleTSSyncOff(w http.ResponseWriter, r *http.Request) {
	store, tid, ok := s.tsParseSyncTimeline(w, r)
	if !ok {
		return
	}
	cur := store.WriteConfig(tid)
	cur.NoSync = true
	if err := store.SetWriteConfig(tid, cur); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrTSWriteConfigSaveFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, tsSyncResponse{TimelineID: int64(tid), NoSync: true})
}
