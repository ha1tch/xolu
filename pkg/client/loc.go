// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// loc.go — typed pkg/client coverage for /api/v2/.../loc/* (XOT211,
// wave 15), the core surface: def/list/get/patch/delete/report/move.
// The server side already works today, confirmed directly (this
// client's own callers were already using GET /loc/list via raw
// before this file existed). Fences are a deliberate, separate
// follow-up -- see types_loc.go's own header comment.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// LocDefine creates or reconfigures a location. Set req.ParentID to
// nil for a root location, or to an existing location's own id to
// place this one under it.
//
// Hits POST /api/v2/.../loc/def. Returns *client.Error on non-2xx.
func (c *Client) LocDefine(ctx context.Context, req LocDefineRequest) (*LocLocation, error) {
	if req.LocationID == "" {
		return nil, fmt.Errorf("locationID is required")
	}
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	var loc LocLocation
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/loc/def"), req, &loc); err != nil {
		return nil, err
	}
	return &loc, nil
}

// LocList returns every location defined on the tenant. optParentID,
// when non-empty, filters to direct children of that location only
// (matching the server's own ?parent_id= query filter); pass "" for
// every location regardless of parent.
//
// Hits GET /api/v2/.../loc/list[?parent_id=...]. Returns
// *client.Error on non-2xx.
func (c *Client) LocList(ctx context.Context, optParentID string) (*LocListResult, error) {
	u := c.buildURLv2("/loc/list")
	if optParentID != "" {
		q := url.Values{}
		q.Set("parent_id", optParentID)
		u += "?" + q.Encode()
	}
	var res LocListResult
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	if res.Locations == nil {
		res.Locations = []LocLocation{}
	}
	return &res, nil
}

// LocGet returns locationID's own current record.
//
// Hits GET /api/v2/.../loc/{location_id}. Returns *client.Error on
// non-2xx -- notably 404 if locationID was never defined.
func (c *Client) LocGet(ctx context.Context, locationID string) (*LocLocation, error) {
	if locationID == "" {
		return nil, fmt.Errorf("locationID is required")
	}
	var loc LocLocation
	u := c.buildURLv2("/loc/" + url.PathEscape(locationID))
	if err := c.doURL(ctx, http.MethodGet, u, nil, &loc); err != nil {
		return nil, err
	}
	return &loc, nil
}

// LocPatch partially updates locationID. See LocPatchRequest's own
// doc comment for the tri-state Capacity semantics (nil outer pointer
// = leave unchanged; non-nil outer pointing at nil inner = clear to
// unlimited; non-nil outer pointing at a value = set that ceiling).
// Capacity is excluded from this struct's own JSON tags precisely
// because standard marshaling cannot distinguish those three states
// through one field the way a hand-built body can -- built manually
// here rather than relying on encoding/json for exactly that field.
//
// Hits PATCH /api/v2/.../loc/{location_id}. Returns *client.Error on
// non-2xx.
func (c *Client) LocPatch(ctx context.Context, locationID string, req LocPatchRequest) (*LocLocation, error) {
	if locationID == "" {
		return nil, fmt.Errorf("locationID is required")
	}
	body := map[string]interface{}{}
	if req.Name != nil {
		body["name"] = *req.Name
	}
	if req.Placement != nil {
		body["placement"] = *req.Placement
	}
	if req.Capacity != nil {
		body["capacity"] = *req.Capacity // *req.Capacity is *int64: nil -> JSON null, non-nil -> the value
	}
	var loc LocLocation
	u := c.buildURLv2("/loc/" + url.PathEscape(locationID))
	if err := c.doURL(ctx, http.MethodPatch, u, body, &loc); err != nil {
		return nil, err
	}
	return &loc, nil
}

// LocDelete removes locationID. force, when true, matches the
// server's own ?force=true query flag (behaviour for a non-empty
// location is a server-side policy this client does not second-guess
// or duplicate -- whatever LocDelete without force refuses, force is
// the caller's own explicit override).
//
// Hits DELETE /api/v2/.../loc/{location_id}[?force=true]. Returns
// *client.Error on non-2xx.
func (c *Client) LocDelete(ctx context.Context, locationID string, force bool) error {
	if locationID == "" {
		return fmt.Errorf("locationID is required")
	}
	u := c.buildURLv2("/loc/" + url.PathEscape(locationID))
	if force {
		u += "?force=true"
	}
	return c.doURL(ctx, http.MethodDelete, u, nil, nil)
}

// LocMove moves the entity identified by entityType/entityID to
// toLocationID, computing and reporting any fence boundaries crossed
// by the move.
//
// Hits POST /api/v2/.../loc/move. Returns *client.Error on non-2xx.
func (c *Client) LocMove(ctx context.Context, entityType string, entityID int64, toLocationID string) (*LocMoveResult, error) {
	if entityType == "" {
		return nil, fmt.Errorf("entityType is required")
	}
	if toLocationID == "" {
		return nil, fmt.Errorf("toLocationID is required")
	}
	body := map[string]interface{}{
		"subject": LocSubject{Entity: entityType, ID: entityID},
		"to":      toLocationID,
	}
	var res LocMoveResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/loc/move"), body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// LocReport records a raw lat/lon/alt point for the entity identified
// by entityType/entityID, independent of any /loc leaf placement, and
// reports whether the report itself crossed a fence boundary.
//
// Hits POST /api/v2/.../loc/report. Returns *client.Error on non-2xx.
func (c *Client) LocReport(ctx context.Context, entityType string, entityID int64, point LocPoint) (*LocReportResult, error) {
	if entityType == "" {
		return nil, fmt.Errorf("entityType is required")
	}
	body := map[string]interface{}{
		"subject": LocSubject{Entity: entityType, ID: entityID},
		"point":   point,
	}
	var res LocReportResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/loc/report"), body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// loc.go, continued (XOT214, wave 16 Stage 2): fences, subject
// position/history, contains/nearby, patterns.

// fenceLookupPath builds the /loc/fences/{kind}/{key} path from an
// already-explicit ref -- no parsing, no guessing, both segments
// known by construction. See LocFenceRef's own doc comment for why
// this replaced an earlier single-string design.
func fenceLookupPath(ref LocFenceRef) string {
	return "/loc/fences/" + url.PathEscape(ref.Kind) + "/" + url.PathEscape(ref.Key)
}

// LocFenceAttach defines a fence -- either obj-anchored
// (req.Subject, "kind:key" shorthand) or tree-aligned (req.AlignedTo,
// a plain location id) -- exactly one of the two set.
//
// Hits POST /api/v2/.../loc/fences/attach. Returns *client.Error on
// non-2xx -- notably XOLU-LOC022 if both Capacity and Pattern are set.
func (c *Client) LocFenceAttach(ctx context.Context, req LocFenceAttachRequest) (*LocFence, error) {
	if req.Subject == "" && req.AlignedTo == "" {
		return nil, fmt.Errorf("exactly one of Subject or AlignedTo is required")
	}
	if req.Capacity != nil && req.Pattern != nil {
		return nil, fmt.Errorf("capacity and pattern are mutually exclusive")
	}
	var fence LocFence
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/loc/fences/attach"), req, &fence); err != nil {
		return nil, err
	}
	return &fence, nil
}

// LocFenceList returns every fence defined on the tenant.
//
// Hits GET /api/v2/.../loc/fences/list. Returns *client.Error on
// non-2xx.
func (c *Client) LocFenceList(ctx context.Context) (*LocFenceListResult, error) {
	var res LocFenceListResult
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2("/loc/fences/list"), nil, &res); err != nil {
		return nil, err
	}
	if res.Fences == nil {
		res.Fences = []string{}
	}
	return &res, nil
}

// LocFenceGet returns ref's own current record. Construct ref via
// LocFenceRefByLocation or LocFenceRefBySubject.
//
// Hits GET /api/v2/.../loc/fences/{kind}/{key}. Returns *client.Error
// on non-2xx -- notably 404 if ref was never defined.
func (c *Client) LocFenceGet(ctx context.Context, ref LocFenceRef) (*LocFence, error) {
	if ref.Kind == "" || ref.Key == "" {
		return nil, fmt.Errorf("ref.Kind and ref.Key are both required")
	}
	var fence LocFence
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(fenceLookupPath(ref)), nil, &fence); err != nil {
		return nil, err
	}
	return &fence, nil
}

// LocFenceDelete removes ref.
//
// Hits DELETE /api/v2/.../loc/fences/{kind}/{key}. Returns
// *client.Error on non-2xx.
func (c *Client) LocFenceDelete(ctx context.Context, ref LocFenceRef) error {
	if ref.Kind == "" || ref.Key == "" {
		return fmt.Errorf("ref.Kind and ref.Key are both required")
	}
	return c.doURL(ctx, http.MethodDelete, c.buildURLv2(fenceLookupPath(ref)), nil, nil)
}

// LocFencePatch replaces ref's own geometry. Never touches capacity,
// pattern, or current membership -- only the stored shape changes;
// membership stays correct until the next report/move naturally
// revisits it, or until LocFenceReconcile is asked to look.
//
// Hits PATCH /api/v2/.../loc/fences/{kind}/{key}. Returns
// *client.Error on non-2xx.
func (c *Client) LocFencePatch(ctx context.Context, ref LocFenceRef, geometry LocFenceGeometry) (*LocFence, error) {
	if ref.Kind == "" || ref.Key == "" {
		return nil, fmt.Errorf("ref.Kind and ref.Key are both required")
	}
	body := map[string]interface{}{"geometry": geometry}
	var fence LocFence
	if err := c.doURL(ctx, http.MethodPatch, c.buildURLv2(fenceLookupPath(ref)), body, &fence); err != nil {
		return nil, err
	}
	return &fence, nil
}

// LocFenceReconcile reports ref's own recorded-vs-observed membership
// drift -- advisory only, never writes anything.
//
// Hits GET /api/v2/.../loc/fences/{kind}/{key}/reconcile. Returns
// *client.Error on non-2xx.
func (c *Client) LocFenceReconcile(ctx context.Context, ref LocFenceRef) (*LocFenceReconcileResult, error) {
	if ref.Kind == "" || ref.Key == "" {
		return nil, fmt.Errorf("ref.Kind and ref.Key are both required")
	}
	var res LocFenceReconcileResult
	u := c.buildURLv2(fenceLookupPath(ref) + "/reconcile")
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// LocSubjectPosition resolves the entity/id subject's own current
// canonical state -- leaf placement, fence membership, and last raw
// report, whichever of those this subject has actually been tracked
// by.
//
// Hits GET /api/v2/.../loc/subjects/{entity}/{id}/position. Returns
// *client.Error on non-2xx.
func (c *Client) LocSubjectPosition(ctx context.Context, entityType string, entityID int64) (*LocSubjectPosition, error) {
	if entityType == "" {
		return nil, fmt.Errorf("entityType is required")
	}
	u := c.buildURLv2(fmt.Sprintf("/loc/subjects/%s/%d/position", url.PathEscape(entityType), entityID))
	var pos LocSubjectPosition
	if err := c.doURL(ctx, http.MethodGet, u, nil, &pos); err != nil {
		return nil, err
	}
	return &pos, nil
}

// LocSubjectHistory returns the entity/id subject's own movement
// journal, newest first.
//
// Hits GET /api/v2/.../loc/subjects/{entity}/{id}/history. Returns
// *client.Error on non-2xx.
func (c *Client) LocSubjectHistory(ctx context.Context, entityType string, entityID int64) (*LocSubjectHistoryResult, error) {
	if entityType == "" {
		return nil, fmt.Errorf("entityType is required")
	}
	u := c.buildURLv2(fmt.Sprintf("/loc/subjects/%s/%d/history", url.PathEscape(entityType), entityID))
	var res LocSubjectHistoryResult
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	if res.Entries == nil {
		res.Entries = []LocHistoryEntry{}
	}
	return &res, nil
}

// LocContains returns every fence the given point currently falls
// inside.
//
// Hits GET /api/v2/.../loc/contains?lat=...&lon=.... Returns
// *client.Error on non-2xx.
func (c *Client) LocContains(ctx context.Context, lat, lon float64) (*LocContainsResult, error) {
	q := url.Values{}
	q.Set("lat", strconv.FormatFloat(lat, 'f', -1, 64))
	q.Set("lon", strconv.FormatFloat(lon, 'f', -1, 64))
	var res LocContainsResult
	u := c.buildURLv2("/loc/contains") + "?" + q.Encode()
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	if res.Fences == nil {
		res.Fences = []string{}
	}
	return &res, nil
}

// LocNearby returns locations and fences near the given point, within
// radiusM meters.
//
// Hits GET /api/v2/.../loc/nearby?lat=...&lon=...&radius_m=.... Returns
// *client.Error on non-2xx.
func (c *Client) LocNearby(ctx context.Context, lat, lon, radiusM float64) (*LocNearbyResult, error) {
	if radiusM <= 0 {
		return nil, fmt.Errorf("radiusM must be positive")
	}
	q := url.Values{}
	q.Set("lat", strconv.FormatFloat(lat, 'f', -1, 64))
	q.Set("lon", strconv.FormatFloat(lon, 'f', -1, 64))
	q.Set("radius_m", strconv.FormatFloat(radiusM, 'f', -1, 64))
	var res LocNearbyResult
	u := c.buildURLv2("/loc/nearby") + "?" + q.Encode()
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// LocPatternDefine defines a fence-type pattern -- not a fence or a
// location itself, addressed by a plain (tenant, name).
//
// Hits POST /api/v2/.../loc/patterns/def. Returns *client.Error on
// non-2xx.
func (c *Client) LocPatternDefine(ctx context.Context, name string, capacity int64) (*LocPattern, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	body := LocPattern{Name: name, Capacity: capacity}
	var p LocPattern
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/loc/patterns/def"), body, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// LocPatternList returns every pattern defined on the tenant.
//
// Hits GET /api/v2/.../loc/patterns/list. Returns *client.Error on
// non-2xx.
func (c *Client) LocPatternList(ctx context.Context) (*LocPatternListResult, error) {
	var res LocPatternListResult
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2("/loc/patterns/list"), nil, &res); err != nil {
		return nil, err
	}
	if res.Patterns == nil {
		res.Patterns = []LocPattern{}
	}
	return &res, nil
}

// LocPatternGet returns id's own current record.
//
// Hits GET /api/v2/.../loc/patterns/{id}. Returns *client.Error on
// non-2xx -- notably 404 if id was never defined.
func (c *Client) LocPatternGet(ctx context.Context, id string) (*LocPattern, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	var p LocPattern
	u := c.buildURLv2("/loc/patterns/" + url.PathEscape(id))
	if err := c.doURL(ctx, http.MethodGet, u, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// LocPatternDelete removes id.
//
// Hits DELETE /api/v2/.../loc/patterns/{id}. Returns *client.Error on
// non-2xx.
func (c *Client) LocPatternDelete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	u := c.buildURLv2("/loc/patterns/" + url.PathEscape(id))
	return c.doURL(ctx, http.MethodDelete, u, nil, nil)
}
