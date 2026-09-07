// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// tenant_summary.go — typed pkg/client coverage for GET
// /tenant-summary (XOT213, wave 15), requested by the xoluman team so
// its own tenant-emptiness safety check stops making several separate
// round trips for one yes/no question, and stops hand-maintaining its
// own, independently-drifting notion of "what counts as tenant data."

import (
	"context"
	"net/http"
)

// TenantSummary returns a single-round-trip data-presence summary for
// the connected tenant -- every store a tenant's own data can live
// in, derived server-side from the same table inventory
// pkg/tenantexport's own backup path uses. res.Empty is the boolean
// answer; the full per-store breakdown is included in the same
// response for a caller that wants more than yes/no.
//
// Hits GET /api/v1/.../tenant-summary. Returns *client.Error on
// non-2xx.
func (c *Client) TenantSummary(ctx context.Context) (*TenantSummary, error) {
	var res TenantSummary
	if err := c.doURL(ctx, http.MethodGet, c.buildURL("/tenant-summary"), nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
