// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// obj.go — typed pkg/client coverage for /api/v2/.../obj/* (XOT210,
// wave 15). obj writes already worked via raw before this file
// existed (confirmed directly: POST .../obj/attach and .../obj/
// promote both observed succeeding against a live server during
// xolu's own test suite) -- this file is what lets a caller build
// real obj seed steps instead of only an untyped passthrough,
// matching bal.go/cal.go's own established shape exactly.
//
// Every method here takes or returns a plain "kind:key" subject
// string, never a split kind/key pair -- the server itself accepts
// both forms (parseObjSubject), and the shorthand is simpler for a
// caller to hold onto as one value. splitObjSubject below is the
// client-side mirror of the server's own strings.Cut split.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// splitObjSubject splits a "kind:key" shorthand into its two path
// segments, matching parseObjSubject's own shorthand branch
// server-side. Returns an error rather than silently sending a
// malformed URL when the subject has no ':' at all.
func splitObjSubject(subject string) (kind, key string, err error) {
	kind, key, ok := strings.Cut(subject, ":")
	if !ok {
		return "", "", fmt.Errorf("subject must be \"kind:key\": %q", subject)
	}
	if kind == "" || key == "" {
		return "", "", fmt.Errorf("subject must be \"kind:key\" with both parts non-empty: %q", subject)
	}
	return kind, key, nil
}

// objSubjectPath builds the /obj/{kind}/{key} path segment, each
// part escaped separately -- matching blob.go/schema.go's own
// established url.PathEscape convention for path parameters here.
func objSubjectPath(kind, key string) string {
	return "/obj/" + url.PathEscape(kind) + "/" + url.PathEscape(key)
}

// ObjAttach attaches obj capability to req.Subject ("kind:key"
// shorthand). A freshly-attached subject always starts unassigned
// (PositionKind == "").
//
// Hits POST /api/v2/.../obj/attach. Returns *client.Error on
// non-2xx -- notably XOLU-OBJ006 if the subject is already attached.
func (c *Client) ObjAttach(ctx context.Context, req ObjAttachRequest) (*ObjSubject, error) {
	if req.Subject == "" {
		return nil, fmt.Errorf("subject is required")
	}
	var sub ObjSubject
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/obj/attach"), req, &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

// ObjList returns every subject currently attached in the tenant --
// no way existed to enumerate obj data at all before this (XOT209,
// found by reading the full obj route table directly: every other
// route requires an already-known subject). An empty tenant returns
// an empty slice, not an error -- exactly the shape a caller doing a
// tenant-emptiness check needs.
//
// Hits GET /api/v2/.../obj/list. Returns *client.Error on non-2xx.
func (c *Client) ObjList(ctx context.Context) (*ObjListResult, error) {
	var res ObjListResult
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2("/obj/list"), nil, &res); err != nil {
		return nil, err
	}
	if res.Subjects == nil {
		res.Subjects = []ObjSubject{}
	}
	return &res, nil
}

// ObjGet returns subject's own current record.
//
// Hits GET /api/v2/.../obj/{kind}/{key}. Returns *client.Error on
// non-2xx -- notably 404 if subject was never attached.
func (c *Client) ObjGet(ctx context.Context, subject string) (*ObjSubject, error) {
	kind, key, err := splitObjSubject(subject)
	if err != nil {
		return nil, err
	}
	var sub ObjSubject
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(objSubjectPath(kind, key)), nil, &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

// ObjDetach removes obj capability from subject entirely -- distinct
// from Retire (the lifecycle terminal state, §6); this is plain
// bookkeeping cleanup. Refused (409) if subject is currently
// positioned anywhere other than unassigned.
//
// Hits DELETE /api/v2/.../obj/{kind}/{key}. Returns *client.Error on
// non-2xx.
func (c *Client) ObjDetach(ctx context.Context, subject string) error {
	kind, key, err := splitObjSubject(subject)
	if err != nil {
		return err
	}
	return c.doURL(ctx, http.MethodDelete, c.buildURLv2(objSubjectPath(kind, key)), nil, nil)
}

// ObjMove moves subject to target -- either a real /loc leaf
// (Kind: "loc_leaf", LocationID set) or containment inside another
// already-attached subject (Kind: "obj", Subject set to the
// container's own "kind:key"). Returns subject's own updated record.
//
// Hits PUT /api/v2/.../obj/{kind}/{key}/move. Returns *client.Error
// on non-2xx -- a move to a loc_leaf can surface /loc's own errors
// unwrapped (e.g. XOLU-LOC002 for a capacity refusal).
func (c *Client) ObjMove(ctx context.Context, subject string, target ObjMoveTarget) (*ObjSubject, error) {
	kind, key, err := splitObjSubject(subject)
	if err != nil {
		return nil, err
	}
	if target.Kind == "" {
		return nil, fmt.Errorf("target.Kind is required (\"loc_leaf\" or \"obj\")")
	}
	body := map[string]interface{}{"to": target}
	var sub ObjSubject
	if err := c.doURL(ctx, http.MethodPut, c.buildURLv2(objSubjectPath(kind, key)+"/move"), body, &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

// ObjReport records subject's own current point (lat/lon, alt
// optional) -- a raw position report, independent of /loc containment.
//
// Hits POST /api/v2/.../obj/{kind}/{key}/report. Returns
// *client.Error on non-2xx.
func (c *Client) ObjReport(ctx context.Context, subject string, lat, lon, alt float64) error {
	kind, key, err := splitObjSubject(subject)
	if err != nil {
		return err
	}
	body := map[string]interface{}{
		"point": map[string]interface{}{"lat": lat, "lon": lon, "alt": alt},
	}
	return c.doURL(ctx, http.MethodPost, c.buildURLv2(objSubjectPath(kind, key)+"/report"), body, nil)
}

// ObjPosition resolves subject's own current position, following a
// containment chain to its own root if subject is contained by
// another subject rather than placed directly on a /loc leaf. AsOf
// is always "live" today -- there is no historical-position query
// yet.
//
// Hits GET /api/v2/.../obj/{kind}/{key}/position. Returns
// *client.Error on non-2xx.
func (c *Client) ObjPosition(ctx context.Context, subject string) (*ObjPositionResult, error) {
	kind, key, err := splitObjSubject(subject)
	if err != nil {
		return nil, err
	}
	var res ObjPositionResult
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(objSubjectPath(kind, key)+"/position"), nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ObjRetire moves subject to its own terminal lifecycle state
// (obj-00-design.md §12) -- once set, never cleared. Distinct from
// Detach (bookkeeping removal); this is a permanent record.
//
// Hits POST /api/v2/.../obj/{kind}/{key}/retire. Returns
// *client.Error on non-2xx.
func (c *Client) ObjRetire(ctx context.Context, subject string) error {
	kind, key, err := splitObjSubject(subject)
	if err != nil {
		return err
	}
	return c.doURL(ctx, http.MethodPost, c.buildURLv2(objSubjectPath(kind, key)+"/retire"), nil, nil)
}

// ObjCapacityPatch updates subject's own capacity ceilings (any of
// the three dimensions may be omitted to leave it unchanged; the
// server's own current-usage totals are read-only and never set from
// here). Returns subject's own updated record.
//
// Hits PATCH /api/v2/.../obj/{kind}/{key}/capacity. Returns
// *client.Error on non-2xx.
func (c *Client) ObjCapacityPatch(ctx context.Context, subject string, capacity ObjCapacity) (*ObjSubject, error) {
	kind, key, err := splitObjSubject(subject)
	if err != nil {
		return nil, err
	}
	var sub ObjSubject
	if err := c.doURL(ctx, http.MethodPatch, c.buildURLv2(objSubjectPath(kind, key)+"/capacity"), capacity, &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

// ObjContents returns subject's own directly-contained subjects, or
// every subject in its transitive containment tree when transitive
// is true.
//
// Hits GET /api/v2/.../obj/{kind}/{key}/contents[?depth=all]. Returns
// *client.Error on non-2xx.
func (c *Client) ObjContents(ctx context.Context, subject string, transitive bool) (*ObjContentsResult, error) {
	kind, key, err := splitObjSubject(subject)
	if err != nil {
		return nil, err
	}
	u := c.buildURLv2(objSubjectPath(kind, key) + "/contents")
	if transitive {
		u += "?depth=all"
	}
	var res ObjContentsResult
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	if res.Contents == nil {
		res.Contents = []string{}
	}
	return &res, nil
}

// ObjPromote promotes an entity (either an existing one, by key, or
// a new one created inline) to obj capability together with a bal
// transfer, as one atomic dxp-orchestrated operation -- not two
// separate calls a caller sequences and hopes stay consistent.
// req.Position.Kind must be "obj" in this server release (containment
// into an already-attached subject); req.Entity must set exactly one
// of ExistingKey or Create.
//
// Hits POST /api/v2/.../obj/promote. Returns *client.Error on
// non-2xx -- notably XOLU-OBJ (entity-selection) errors when Entity
// sets both or neither of its own two fields.
func (c *Client) ObjPromote(ctx context.Context, req ObjPromoteRequest) (*ObjPromoteResult, error) {
	if req.BalAccount == "" || req.ToAccount == "" || req.Amount == "" {
		return nil, fmt.Errorf("bal_account, to_account, and amount are required")
	}
	if (req.Entity.ExistingKey == nil) == (req.Entity.Create == nil) {
		return nil, fmt.Errorf("exactly one of Entity.ExistingKey or Entity.Create must be set")
	}
	if req.Position.Kind != "obj" {
		return nil, fmt.Errorf("position.Kind must be \"obj\" in this server release, got %q", req.Position.Kind)
	}
	var res ObjPromoteResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/obj/promote"), req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ObjDemote reverses a promotion -- removes obj capability from
// req.Subject together with a bal transfer back out, the same atomic
// pairing ObjPromote makes going in.
//
// Hits POST /api/v2/.../obj/demote. Returns *client.Error on non-2xx.
func (c *Client) ObjDemote(ctx context.Context, req ObjDemoteRequest) (*ObjPromoteResult, error) {
	if req.Subject == "" || req.BalAccount == "" || req.FromAccount == "" || req.Amount == "" {
		return nil, fmt.Errorf("subject, bal_account, from_account, and amount are required")
	}
	var res ObjPromoteResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/obj/demote"), req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
