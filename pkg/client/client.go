// Package client provides an HTTP client for the xolu REST API server.
//
// xolu is a graph-enhanced document store that provides:
//   - Entity CRUD operations
//   - Graph relationships via REF objects
//   - OQL (SQL-like) queries
//   - Sulpher (graph path) queries
//   - Full-text search
//
// This client wraps the HTTP API and provides a convenient Go interface.
//
// # Declared surface (v0.16.0 stability)
//
// The client covers xolu's data-plane and semantic-map surface: entity
// CRUD, Commit, Search, OQL, Sulpher, graph basics (neighbours, query,
// shortest path), schemas (get + write + list, including schemaless
// entity types via ListEntities, added 2026-08-04, T-151), named
// sequences and generators, the full FSM machine surface, the full FSM
// definition surface (added 2026-08-04: Create/Replace/Delete/Validate,
// alongside the existing List/Get -- previously read-only), event-
// definition reads, cal (check/openings/propose/confirm), health/
// availability, the native blob surface (added 2026-08-03, T-142:
// put/get/head/delete/list/usage), and async tenant-scoped export
// (added 2026-08-03, T-145: BlobExportStart/BlobExportStatus plus the
// Export convenience wrapper). This is the surface molu Parts 2–3
// consume, and it is version-tied to the server.
//
// Deliberately out of scope — documented exclusions, not omissions:
// timeseries, meta, admin, dynconfig, stats, async-query polling, and
// the deep graph analytics (pathExists, commonNeighbors, per-node
// inspection, edges, admin rebuild/verify). See docs/CLIENT_STAGE6_PLAN.md
// for the audit that drew this line; a consumer needing an excluded
// family reopens the scope decision rather than finding an accidental
// gap.
//
// Export specifically, for anyone reading history in the register: a
// synchronous streaming client method (T-145, first draft) was built
// against the old, non-tenant-scoped GET /api/v1/export, then
// deliberately shelved (2026-08-03) in favour of the async, tenant-
// scoped, blob-backed design this package now implements — see
// pkg/tenantexport's own doc comment for the full history. The
// requirement T-145 named (the client has to actually deliver export
// data to the caller, streamed) didn't change; only the mechanism did.
// The old, now-unused GET /api/v1/export endpoint is untouched
// server-side.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// AuthMode identifies which HTTP authentication mode the client uses when
// talking to xolu. See xolu's pkg/middleware/auth for the corresponding
// server-side handling.
type AuthMode int

const (
	// AuthNone sends no Authorization header. Used when the xolu server has
	// AuthType="" or when the client sits behind a trusted gateway.
	AuthNone AuthMode = iota
	// AuthAPIKey sends "Authorization: Bearer <key>" where the key is an
	// entry in the server's XOLU_API_KEYS list. See WithAPIKey.
	AuthAPIKey
	// AuthBearer sends "Authorization: Bearer <token>" where the token is a
	// server-issued opaque bearer token. See WithBearerToken.
	AuthBearer
	// AuthJWT sends "Authorization: Bearer <jwt>" where the JWT is signed
	// with the secret configured as XOLU_JWT_SECRET. See WithJWT.
	AuthJWT
)

// Client provides access to the xolu REST API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	tenantID   string

	// Authentication. Exactly one of these carries a value; authMode names
	// which one. authMode == AuthNone leaves all three empty and sends no
	// Authorization header.
	authMode AuthMode
	apiKey   string
	bearer   string
	jwt      string

	// Stage 4: operational-hardening state. All zero-valued by default so
	// pre-Stage-4 behaviour is preserved when the corresponding options
	// are not supplied.
	//
	// retry.MaxAttempts == 0 or 1 → no retries fire (the default).
	// logger == nil → no telemetry emitted.
	// callTimeout == 0 → no per-call timeout applied beyond what the
	//   caller's context imposes.
	retry       RetryPolicy
	logger      *slog.Logger
	callTimeout time.Duration
}

// ClientOption configures the Client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) ClientOption {
	return func(client *Client) {
		client.httpClient = c
	}
}

// WithTenant sets the default tenant ID for all requests.
func WithTenant(tenantID string) ClientOption {
	return func(client *Client) {
		client.tenantID = tenantID
	}
}

// WithTenantID sets the tenant for all requests, formatting the uint16 ID as
// the 4-digit uppercase hex prefix xolu requires (e.g. 1 -> "0001").
// Prefer this over WithTenant when working with numeric tenant IDs.
func WithTenantID(id uint16) ClientOption {
	return WithTenant(fmt.Sprintf("%04X", id))
}

// WithAPIKey sets the API key sent as "Authorization: Bearer <key>" on every
// request. Corresponds to XOLU_AUTH_TYPE=apikey on the server and to entries
// in the server's XOLU_API_KEYS list.
//
// Only one of WithAPIKey, WithBearerToken, WithJWT should be set. If more than
// one is set, the last one wins.
func WithAPIKey(key string) ClientOption {
	return func(client *Client) {
		client.authMode = AuthAPIKey
		client.apiKey = key
	}
}

// WithBearerToken sets a server-issued bearer token sent as
// "Authorization: Bearer <token>" on every request. Corresponds to
// XOLU_AUTH_TYPE=bearertoken on the server.
//
// Only one of WithAPIKey, WithBearerToken, WithJWT should be set. If more than
// one is set, the last one wins.
func WithBearerToken(token string) ClientOption {
	return func(client *Client) {
		client.authMode = AuthBearer
		client.bearer = token
	}
}

// WithJWT sets a JWT sent as "Authorization: Bearer <jwt>" on every request.
// The JWT must be signed with the secret configured as XOLU_JWT_SECRET on the
// server. Corresponds to XOLU_AUTH_TYPE=jwt on the server; JWT claims like
// tenants:[...] and tenant_admin:true are honoured by xolu's TenantAuthMode.
//
// Only one of WithAPIKey, WithBearerToken, WithJWT should be set. If more than
// one is set, the last one wins.
func WithJWT(token string) ClientOption {
	return func(client *Client) {
		client.authMode = AuthJWT
		client.jwt = token
	}
}

// ─── Stage 4 options: retry, telemetry, per-call timeout ────────────────────

// WithRetryPolicy enables automatic retries for idempotent requests. See the
// RetryPolicy documentation for the semantics.
//
// The default (no option supplied) is "no retries" — MaxAttempts=1 —
// matching pre-Stage-4 client behaviour. Callers must opt in explicitly.
//
// Only GET, HEAD, PUT, DELETE, and OPTIONS retry. POST and PATCH never
// retry regardless of policy, per RFC 9110 §9.2.2 idempotency guarantees.
// If a caller needs to retry a POST or PATCH they consider replay-safe,
// they wrap the call themselves.
func WithRetryPolicy(p RetryPolicy) ClientOption {
	return func(client *Client) {
		client.retry = p
	}
}

// WithLogger enables structured request telemetry via log/slog. Every
// completed HTTP attempt is logged at debug level (method, path, status,
// duration, attempt number); auth failures at info level; retries at warn
// level. Never any payload content.
//
// The default (no option supplied) is a discarding logger — the client
// emits no telemetry unless the caller explicitly opts in. This avoids
// polluting the caller's log stream through slog.Default().
//
// Passing nil is equivalent to omitting the option.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(client *Client) {
		if logger == nil {
			return
		}
		client.logger = logger
	}
}

// WithCallTimeout sets the default per-call timeout. Every request's
// context is wrapped with context.WithTimeout(timeout) before dispatch,
// unless the caller's context already carries a tighter deadline (in which
// case the caller's deadline wins).
//
// A zero timeout means "no per-call timeout" — the client relies on the
// caller's context deadline and on the httpClient's own Timeout field.
//
// This complements rather than replaces WithHTTPClient's Timeout field.
// The httpClient's Timeout is a hard ceiling on total request duration
// including body read; WithCallTimeout is a per-call deadline on the whole
// operation (retries included).
func WithCallTimeout(timeout time.Duration) ClientOption {
	return func(client *Client) {
		client.callTimeout = timeout
	}
}

// WithTimeout returns a shallow-copied client whose per-call timeout is
// set to the given value. Useful for one-off overrides:
//
//	err := client.WithTimeout(2*time.Second).Ready(ctx)
//
// The parent client is not modified.
func (c *Client) WithTimeout(timeout time.Duration) *Client {
	cp := *c
	cp.callTimeout = timeout
	return &cp
}

// New creates a new xolu client.
func New(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// WithTenantContext returns a new client with the specified tenant ID.
// This is useful for per-request tenant context.
func (c *Client) WithTenantContext(tenantID string) *Client {
	return &Client{
		baseURL:     c.baseURL,
		httpClient:  c.httpClient,
		tenantID:    tenantID,
		authMode:    c.authMode,
		apiKey:      c.apiKey,
		bearer:      c.bearer,
		jwt:         c.jwt,
		retry:       c.retry,
		logger:      c.logger,
		callTimeout: c.callTimeout,
	}
}

// Entity represents a document stored in xolu.
// Data holds the complete flat document as returned by the server, including
// the "id" field. ID is extracted for convenience.
//
// After Create, Data is nil — xolu does not echo the document on creation.
// Call Get if the full document is needed after a write.
// After Update or Patch, Data is also nil for the same reason.
type Entity struct {
	ID        int64
	Data      map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListParams configures list queries.
type ListParams struct {
	Limit  int
	Offset int
	Sort   string // field name, prefix with - for descending
}

// ListResult holds a page of entities and the pagination metadata returned
// by xolu's PagedResponse envelope.
type ListResult struct {
	Entities   []Entity
	Page       int
	PerPage    int
	TotalItems int
	TotalPages int
}

// SearchParams configures search queries.
// Entity is optional — when set, xolu scopes the search to that entity type.
type SearchParams struct {
	Query  string
	Entity string // optional entity type filter
	Limit  int
	Offset int
}

// OQLResult represents the result of an OQL query.
type OQLResult struct {
	Status string           `json:"status"`
	Data   []map[string]any `json:"data"`
	Stats  OQLStats         `json:"stats"`
}

// OQLStats contains OQL execution statistics.
type OQLStats struct {
	RowsScanned   int   `json:"rows_scanned"`
	RowsReturned  int   `json:"rows_returned"`
	RowsAffected  int   `json:"rows_affected,omitempty"`
	ExecutionTime int64 `json:"execution_time_ms"`
}

// GraphQueryResult represents the result of a Sulpher graph query.
// Previously named SulpherResult; renamed to match the xolu endpoint name.
type GraphQueryResult struct {
	Status string           `json:"status"`
	Result []map[string]any `json:"result"` // xolu uses "result", not "data"
	Stats  GraphQueryStats  `json:"stats"`
}

// GraphQueryStats contains Sulpher execution statistics.
type GraphQueryStats struct {
	NodesTraversed int   `json:"nodes_traversed"`
	PathsFound     int   `json:"paths_found"`
	ExecutionTime  int64 `json:"execution_time_ms"`
}

// SulpherResult is an alias for GraphQueryResult for backward compatibility.
// Deprecated: use GraphQueryResult.
type SulpherResult = GraphQueryResult

// PathResult is returned by GraphShortestPath.
type PathResult struct {
	From   string   `json:"from"`
	To     string   `json:"to"`
	Exists bool     `json:"exists"`
	Path   []string `json:"path"`
	Length int      `json:"length"`
}

// NeighborResult is returned by GraphNeighbors.
// Outgoing and Incoming map neighbour node ID to relationship label.
type NeighborResult struct {
	Outgoing map[string]string `json:"outgoing,omitempty"`
	Incoming map[string]string `json:"incoming,omitempty"`
}

// Error represents an error response from xolu.
//
// xolu's server writes structured errors in the shape:
//
//	{"error":{"code":"XOLU-ST001","message":"...","status":400}}
//
// The client parses that shape into Code, Message, and Status. When the server
// returns an error in the older flat shape ({"error":"message"}) or a
// non-JSON body, Code is left empty and Message carries the raw content.
//
// Callers can dispatch on the code using errors.As:
//
//	var xerr *client.Error
//	if errors.As(err, &xerr) && xerr.Code == "XOLU-ST001" {
//	    // entity not found
//	}
type Error struct {
	// Code is the XOLU-<AREA><NUM> error code (e.g. "XOLU-ST001"). Empty when
	// the server did not return a structured error body.
	Code string
	// HTTPStatus is the HTTP status code (e.g. 400, 404, 500).
	HTTPStatus int
	// Message is the human-readable error message.
	Message string
	// Detail is the raw JSON body of the error response, preserved verbatim
	// so callers can extract server-specific fields the client does not
	// model. Nil when the response body was empty or not valid JSON.
	Detail json.RawMessage

	// StatusCode is preserved as an alias for HTTPStatus for backwards
	// compatibility with earlier client releases. New code should use
	// HTTPStatus.
	//
	// Deprecated: use HTTPStatus.
	StatusCode int
	// Details is preserved as a decoded map for backwards compatibility.
	// New code should use Detail (raw JSON) and decode as needed.
	//
	// Deprecated: use Detail.
	Details map[string]any
}

func (e *Error) Error() string {
	// Prefer HTTPStatus but fall back to StatusCode when only the deprecated
	// field is populated (callers constructing Error literals from earlier
	// client versions).
	status := e.HTTPStatus
	if status == 0 {
		status = e.StatusCode
	}
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("xolu: %s: %s (status %d)", e.Code, e.Message, status)
	case e.Code != "":
		return fmt.Sprintf("xolu: %s (status %d)", e.Code, status)
	case e.Message != "":
		return fmt.Sprintf("xolu: %s (status %d)", e.Message, status)
	default:
		return fmt.Sprintf("xolu: request failed with status %d", status)
	}
}

// buildURL constructs the full URL for an API endpoint.
func (c *Client) buildURL(path string) string {
	if c.tenantID != "" {
		return fmt.Sprintf("%s/api/v1/tenant/%s%s", c.baseURL, c.tenantID, path)
	}
	return fmt.Sprintf("%s/api/v1%s", c.baseURL, path)
}

// buildURLRoot constructs a URL under /api/v1 that is NOT tenant-scoped.
// Used for the schema endpoints (GET/POST /api/v1/schema/{entity}, GET
// /api/v1/schemas) -- confirmed directly against the server's own route
// registration (pkg/server/server.go, "Schema operations
// (tenant-independent, always available)"): no tenant-scoped duplicate
// exists for these, by deliberate design, since a schema applies across
// the whole server, not per tenant.
//
// Added 2026-08-04 (a real bug reported by the xoluman team, reproduced
// directly): GetEntitySchema/DefineEntitySchema/ListEntityTypes
// previously went through plain buildURL, which unconditionally applies
// the tenant prefix whenever one is configured. A tenant-scoped client
// asking for a schema sent /api/v1/tenant/{id}/schema/{entity} -- a path
// that doesn't exist as such; chi's router matched it against the
// entity-by-id pattern instead (/tenant/{id}/{entity}/{id}), landing
// "schema" in {entity} and the real entity name in the numeric {id}
// slot, failing strconv.Atoi and returning XOLU-ST004 "Invalid ID". Not
// caught by any existing test: every test for these three methods,
// unit and integration, constructed its client via New(url) with no
// tenant configured -- the specific combination (tenant set + a schema
// call) had never been exercised. Mirrors buildURLv2Root's own,
// already-proven pattern for the identical problem on the v2 side
// (GET /api/v2/, the availability endpoint) rather than teaching the
// generic, shared buildURL about one endpoint's business, or inlining
// URL construction separately in each of the three affected methods.
func (c *Client) buildURLRoot(path string) string {
	return fmt.Sprintf("%s/api/v1%s", c.baseURL, path)
}

// buildURLv2 constructs the full URL for a /api/v2 endpoint. v2 routes are
// tenant-scoped exactly like v1: /api/v2/tenant/{id}/... when a tenant is
// set, /api/v2/... otherwise. The stateless generator endpoints
// (/api/v2/gen/uuid_v4 etc.) live outside the tenant scope on the server;
// callers hitting those should use buildURLv2Root instead.
func (c *Client) buildURLv2(path string) string {
	if c.tenantID != "" {
		return fmt.Sprintf("%s/api/v2/tenant/%s%s", c.baseURL, c.tenantID, path)
	}
	return fmt.Sprintf("%s/api/v2%s", c.baseURL, path)
}

// buildURLv2Root constructs a URL under /api/v2 that is NOT tenant-scoped.
// Used for the availability endpoint (GET /api/v2/) and the stateless
// generator endpoints (/api/v2/gen/uuid_v4 etc.).
func (c *Client) buildURLv2Root(path string) string {
	return fmt.Sprintf("%s/api/v2%s", c.baseURL, path)
}

// authHeader returns the value of the Authorization header for the client's
// current auth mode, or "" if no auth was configured.
//
// Not a uniform scheme across all three modes -- checked directly
// against the server's own pkg/authmw validators before writing this
// (2026-08-04, T-160): "bearertoken" and "jwt" both genuinely use
// "Bearer <token>", but "apikey" uses "ApiKey <key>" -- the server's
// own validateAPIKey never accepts a Bearer-prefixed key. This
// comment previously claimed all three used "Bearer" uniformly; that
// was wrong, and every AuthAPIKey-configured client was silently
// unauthenticated on every request until this was caught and fixed.
func (c *Client) authHeader() string {
	switch c.authMode {
	case AuthAPIKey:
		if c.apiKey == "" {
			return ""
		}
		// "ApiKey ", not "Bearer " -- confirmed directly against the
		// server's own pkg/authmw.validateAPIKey (2026-08-04, T-160,
		// reported by the xoluman team): it checks X-API-Key first,
		// then falls back to Authorization: ApiKey <key>, then a
		// ?api_key= query param -- it never accepts a Bearer-prefixed
		// Authorization header for apikey auth type. Every request
		// made with AuthAPIKey configured was silently rejected as
		// unauthenticated, regardless of how correct the key itself
		// was; this client's own doc comment claiming "Bearer" works
		// for all three configured auth modes was simply wrong for
		// this one, and was corrected alongside this fix.
		return "ApiKey " + c.apiKey
	case AuthBearer:
		if c.bearer == "" {
			return ""
		}
		return "Bearer " + c.bearer
	case AuthJWT:
		if c.jwt == "" {
			return ""
		}
		return "Bearer " + c.jwt
	}
	return ""
}

// do executes an HTTP request against a v1 API path (via buildURL) and
// handles the response. v2 methods construct their own URL and call doURL
// directly.
func (c *Client) do(ctx context.Context, method, path string, body any, result any) error {
	return c.doURL(ctx, method, c.buildURL(path), body, result)
}

// doURL is the underlying request pipeline: takes a fully-constructed URL,
// marshals the body, sets standard headers, executes the request (with
// retry-with-backoff for idempotent methods when a RetryPolicy is set),
// parses xolu's structured or flat error shape on non-2xx, and decodes the
// response body into result on 2xx.
//
// Stage 4 additions:
//   - Wraps ctx with c.callTimeout when non-zero. Caller's deadline still
//     wins if tighter, via context semantics.
//   - Retries idempotent methods (GET/HEAD/PUT/DELETE/OPTIONS) per the
//     RetryPolicy on transport errors and 5xx responses.
//   - Emits log/slog telemetry when c.logger is set.
func (c *Client) doURL(ctx context.Context, method, requestURL string, body any, result any) error {
	// Marshal the body once. The retry loop constructs a fresh
	// bytes.Reader over these bytes for each attempt; a used reader is
	// not rewindable.
	var jsonBody []byte
	if body != nil {
		var err error
		jsonBody, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	// Apply the per-call timeout. context.WithTimeout returns a context
	// whose deadline is the earlier of the parent's and the requested
	// timeout, so a caller who already set a tighter deadline is
	// unaffected.
	if c.callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.callTimeout)
		defer cancel()
	}

	// Path-only URL for telemetry — never the full URL with query string.
	urlPath := extractPath(requestURL)

	// The retry loop. Cap iterations at MaxAttempts (default 1); the
	// shouldRetry check inside the loop is the real termination
	// condition and also guards the case where policy.MaxAttempts < 1.
	maxAttempts := c.retry.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Fresh reader per attempt.
		var bodyReader io.Reader
		if jsonBody != nil {
			bodyReader = bytes.NewReader(jsonBody)
		}

		start := time.Now()
		respBody, status, transportErr := c.doOnce(ctx, method, requestURL, bodyReader)
		dur := time.Since(start)

		c.logRequest(ctx, method, urlPath, status, dur, attempt, transportErr)

		// Decide whether to retry.
		if c.retry.shouldRetry(method, attempt, syntheticResp(status), transportErr) {
			wait := c.retry.backoffFor(attempt)
			cause := "5xx response"
			if transportErr != nil {
				cause = transportErr.Error()
			}
			c.logRetry(ctx, method, urlPath, attempt, wait, cause)
			if err := c.retrySleep(ctx, wait); err != nil {
				return err
			}
			continue
		}

		// No more retries: return the outcome of this attempt.
		if transportErr != nil {
			return fmt.Errorf("request failed: %w", transportErr)
		}
		return c.decodeResponse(status, respBody, result)
	}
	// Unreachable in normal use: the loop always returns from the last
	// attempt. Kept as a safety net.
	return fmt.Errorf("request failed: retry loop exhausted without an outcome")
}

// doOnce executes a single HTTP attempt. Returns the response body bytes,
// the HTTP status (0 if the request never got a response), and the transport
// error (nil on any HTTP response, including 4xx and 5xx).
func (c *Client) doOnce(ctx context.Context, method, requestURL string, bodyReader io.Reader) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if h := c.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

// decodeResponse parses the response body per xolu's error shapes and the
// caller's result destination. Extracted from the retry loop so the retry
// path can compute status and body once and dispatch on them cleanly.
func (c *Client) decodeResponse(status int, respBody []byte, result any) error {
	if status >= 400 {
		xoluErr := &Error{
			HTTPStatus: status,
			StatusCode: status, // deprecated alias
		}
		if len(respBody) > 0 {
			xoluErr.Detail = append(json.RawMessage(nil), respBody...)
		}

		// Try the structured shape first:
		//   {"error":{"code":"XOLU-ST001","message":"...","status":400}}
		var structured struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Status  int    `json:"status"`
			} `json:"error"`
		}
		if err := json.Unmarshal(respBody, &structured); err == nil && structured.Error.Code != "" {
			xoluErr.Code = structured.Error.Code
			xoluErr.Message = structured.Error.Message
			return xoluErr
		}

		// Fall back to the legacy flat shape:
		//   {"error":"message","details":{...}}
		var flat struct {
			Error   string         `json:"error"`
			Details map[string]any `json:"details,omitempty"`
		}
		if err := json.Unmarshal(respBody, &flat); err == nil && flat.Error != "" {
			xoluErr.Message = flat.Error
			xoluErr.Details = flat.Details
			return xoluErr
		}

		// Non-JSON or unrecognised shape: use the raw body as the message.
		xoluErr.Message = string(respBody)
		return xoluErr
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}
	return nil
}

// retrySleep waits the given duration or returns the context's error if
// the context is cancelled first.
func (c *Client) retrySleep(ctx context.Context, wait time.Duration) error {
	// Tests inject a deterministic sleep via retry.sleep.
	if c.retry.sleep != nil {
		c.retry.sleep(wait)
		return nil
	}
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// syntheticResp is a helper for shouldRetry: it wants an *http.Response
// but the retry decision only needs the status code.
func syntheticResp(status int) *http.Response {
	if status == 0 {
		return nil
	}
	return &http.Response{StatusCode: status}
}

// extractPath returns just the path portion of a URL, or the URL as-is
// if parsing fails. Used for telemetry so query strings don't leak into
// log records.
func extractPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Path
}

// Create creates a new entity in the specified collection.
// xolu returns {"message":"…","id":N} on creation — the document is not echoed
// back. Data on the returned Entity is nil; call Get if the full document is needed.
func (c *Client) Create(ctx context.Context, entity string, data map[string]any) (*Entity, error) {
	path := fmt.Sprintf("/%s", entity)

	var resp struct {
		ID      int64  `json:"id"`
		Message string `json:"message"`
	}

	if err := c.do(ctx, http.MethodPost, path, data, &resp); err != nil {
		return nil, err
	}

	return &Entity{ID: resp.ID}, nil
}

// Get retrieves a single entity by ID.
// xolu returns the flat document directly: {"id":1,"name":"Alice",…}
// The full document is stored in Entity.Data; ID is also extracted for convenience.
func (c *Client) Get(ctx context.Context, entity string, id int64) (*Entity, error) {
	path := fmt.Sprintf("/%s/%d", entity, id)

	var doc map[string]any

	if err := c.do(ctx, http.MethodGet, path, nil, &doc); err != nil {
		return nil, err
	}

	e := &Entity{ID: id, Data: doc}

	// Extract timestamps from the document if present.
	if ts, ok := doc["created_at"].(string); ok {
		e.CreatedAt, _ = time.Parse(time.RFC3339, ts)
	}
	if ts, ok := doc["updated_at"].(string); ok {
		e.UpdatedAt, _ = time.Parse(time.RFC3339, ts)
	}

	return e, nil
}

// Update replaces an existing entity.
// xolu returns {"message":"…"} on success — no document echo.
// Data on the returned Entity is nil; call Get if the updated document is needed.
func (c *Client) Update(ctx context.Context, entity string, id int64, data map[string]any) (*Entity, error) {
	path := fmt.Sprintf("/%s/%d", entity, id)

	if err := c.do(ctx, http.MethodPut, path, data, nil); err != nil {
		return nil, err
	}

	return &Entity{ID: id}, nil
}

// Patch partially updates an existing entity.
// xolu returns {"message":"…"} on success — no document echo.
// Data on the returned Entity is nil; call Get if the updated document is needed.
func (c *Client) Patch(ctx context.Context, entity string, id int64, data map[string]any) (*Entity, error) {
	path := fmt.Sprintf("/%s/%d", entity, id)

	if err := c.do(ctx, http.MethodPatch, path, data, nil); err != nil {
		return nil, err
	}

	return &Entity{ID: id}, nil
}

// Delete removes an entity by ID.
func (c *Client) Delete(ctx context.Context, entity string, id int64) error {
	path := fmt.Sprintf("/%s/%d", entity, id)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// CommitUpdate describes the entity to upsert in a Commit operation.
// If Version is non-nil the write is conditional: it succeeds only when the
// stored _version matches *Version. A mismatch returns ErrConflict (409).
type CommitUpdate struct {
	Entity  string         `json:"entity"`
	ID      int64          `json:"id"`
	Version *int           `json:"version,omitempty"` // nil = unconditional
	Data    map[string]any `json:"data"`
}

// CommitAppend describes one record to insert in a Commit operation.
// If ID is nil xolu auto-assigns an ID. An explicit ID that already exists
// causes ErrAlreadyExists and rolls back the entire commit.
type CommitAppend struct {
	Entity string         `json:"entity"`
	ID     *int64         `json:"id,omitempty"` // nil = auto-assign
	Data   map[string]any `json:"data"`
}

// CommitRequest is the payload for Commit.
// Maximum 25 entries in Append.
type CommitRequest struct {
	Update CommitUpdate   `json:"update"`
	Append []CommitAppend `json:"append"`
}

// CommitResult is returned on a successful Commit.
type CommitResult struct {
	Update struct {
		Entity  string `json:"entity"`
		ID      int64  `json:"id"`
		Created bool   `json:"created"`
		Version int    `json:"version"`
	} `json:"update"`
	Appended []struct {
		Entity string `json:"entity"`
		ID     int64  `json:"id"`
	} `json:"appended"`
}

// Save upserts an entity with a caller-specified ID.
// xolu endpoint: POST /{entity}/save/{id}
// Returns created=true when a new record was inserted, false when an existing
// record was replaced. Use this for idempotent writes where the caller owns
// the ID (e.g. device registration keyed on hardware ID).
func (c *Client) Save(ctx context.Context, entity string, id int64, data map[string]any) (created bool, err error) {
	path := fmt.Sprintf("/%s/save/%d", entity, id)

	var resp struct {
		Created bool  `json:"created"`
		ID      int64 `json:"id"`
	}

	if err := c.do(ctx, http.MethodPost, path, data, &resp); err != nil {
		return false, err
	}

	return resp.Created, nil
}

// Commit performs an atomic upsert + one or more inserts in a single
// storage transaction. Use this when state-transition and audit-trail writes
// must land together or not at all.
// xolu endpoint: POST /commit (tenant-scoped: /api/v1/tenant/{t}/commit)
// Returns ErrConflict (wrapped) when the optimistic version check fails.
// Returns an error wrapping the xolu error message when an append entry uses
// an explicit ID that already exists.
// Maximum 25 entries in req.Append — enforced server-side (400 if exceeded).
func (c *Client) Commit(ctx context.Context, req CommitRequest) (*CommitResult, error) {
	var result CommitResult

	if err := c.do(ctx, http.MethodPost, "/commit", req, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// List retrieves entities from a collection with optional pagination.
// xolu returns a PagedResponse envelope:
//
//	{"data":[…],"pagination":{"page":N,"per_page":N,"total_items":N,"total_pages":N}}
//
// Pagination parameters: xolu uses page/per_page; Limit maps to per_page, Offset
// is converted to a page number (Offset/Limit + 1, floored at 1).
func (c *Client) List(ctx context.Context, entity string, params *ListParams) (*ListResult, error) {
	path := fmt.Sprintf("/%s", entity)

	query := url.Values{}
	if params != nil {
		if params.Limit > 0 {
			query.Set("per_page", strconv.Itoa(params.Limit))
		}
		if params.Offset > 0 && params.Limit > 0 {
			page := params.Offset/params.Limit + 1
			query.Set("page", strconv.Itoa(page))
		} else if params.Offset > 0 {
			query.Set("page", "1")
		}
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}

	var envelope struct {
		Data       []map[string]any `json:"data"`
		Pagination struct {
			Page       int `json:"page"`
			PerPage    int `json:"per_page"`
			TotalItems int `json:"total_items"`
			TotalPages int `json:"total_pages"`
		} `json:"pagination"`
	}

	if err := c.do(ctx, http.MethodGet, path, nil, &envelope); err != nil {
		return nil, err
	}

	entities := make([]Entity, len(envelope.Data))
	for i, doc := range envelope.Data {
		e := Entity{Data: doc}
		if idVal, ok := doc["id"].(float64); ok {
			e.ID = int64(idVal)
		}
		if ts, ok := doc["created_at"].(string); ok {
			e.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		}
		if ts, ok := doc["updated_at"].(string); ok {
			e.UpdatedAt, _ = time.Parse(time.RFC3339, ts)
		}
		entities[i] = e
	}

	return &ListResult{
		Entities:   entities,
		Page:       envelope.Pagination.Page,
		PerPage:    envelope.Pagination.PerPage,
		TotalItems: envelope.Pagination.TotalItems,
		TotalPages: envelope.Pagination.TotalPages,
	}, nil
}

// Search performs a full-text search on an entity collection.
// Search performs a full-text search across entities.
// xolu endpoint: GET /api/v1/search?q=…&entity={optional}
// Response: {"query":"…","entity":"…","count":N,"results":[flat docs…]}
// The entity argument is deprecated — populate SearchParams.Entity instead.
// If both are non-empty, SearchParams.Entity takes precedence.
func (c *Client) Search(ctx context.Context, entity string, params SearchParams) ([]Entity, error) {
	path := "/search"

	query := url.Values{}
	query.Set("q", params.Query)

	// Entity filter: SearchParams.Entity takes precedence over the positional arg.
	scope := params.Entity
	if scope == "" {
		scope = entity
	}
	if scope != "" {
		query.Set("entity", scope)
	}
	if params.Limit > 0 {
		query.Set("limit", strconv.Itoa(params.Limit))
	}
	if params.Offset > 0 {
		query.Set("offset", strconv.Itoa(params.Offset))
	}
	path += "?" + query.Encode()

	var resp struct {
		Query   string           `json:"query"`
		Entity  string           `json:"entity"`
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}

	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}

	entities := make([]Entity, len(resp.Results))
	for i, doc := range resp.Results {
		e := Entity{Data: doc}
		if idVal, ok := doc["id"].(float64); ok {
			e.ID = int64(idVal)
		}
		if ts, ok := doc["created_at"].(string); ok {
			e.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		}
		if ts, ok := doc["updated_at"].(string); ok {
			e.UpdatedAt, _ = time.Parse(time.RFC3339, ts)
		}
		entities[i] = e
	}

	return entities, nil
}

// OQL executes an OQL (SQL-like) query.
func (c *Client) OQL(ctx context.Context, query string) (*OQLResult, error) {
	path := "/oql/query"

	body := map[string]string{"query": query}

	var resp OQLResult

	if err := c.do(ctx, http.MethodPost, path, body, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// GraphQuery executes a Sulpher graph path query.
// Previously named Sulpher; renamed to match the xolu endpoint.
// xolu endpoint: POST /graph/query
// maxDepth of 0 uses xolu's server default.
func (c *Client) GraphQuery(ctx context.Context, query string, maxDepth int) (*GraphQueryResult, error) {
	body := map[string]any{"query": query}
	if maxDepth > 0 {
		body["max_depth"] = maxDepth
	}

	var result GraphQueryResult

	if err := c.do(ctx, http.MethodPost, "/graph/query", body, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// Sulpher is a backward-compatible alias for GraphQuery with maxDepth=0.
// Deprecated: use GraphQuery.
func (c *Client) Sulpher(ctx context.Context, query string) (*GraphQueryResult, error) {
	return c.GraphQuery(ctx, query, 0)
}

// Health checks if the xolu server is healthy.
//
// Hits GET /health. Returns nil on 200 (server process is alive; storage
// layer's Ping succeeded within the server's 2-second timeout). Returns a
// non-nil error on any non-2xx response, transport failure, or timeout.
//
// Health is appropriate for liveness probes (is the process alive?). For
// readiness probes (is the process ready to serve traffic?) use Ready instead.
//
// Health does NOT apply the client's configured auth header, and never
// will: confirmed directly against the server's own auth middleware
// (2026-08-04, T-161, reported by the xoluman team) -- /health is
// deliberately exempt from auth server-side, alongside /ready,
// /version, and /metrics, the standard convention for liveness/
// readiness probes (an orchestrator checking whether to restart a
// process shouldn't need a credential to ask). Sending an auth header
// here would be a pure no-op: the server ignores it for this route
// regardless of what the client sends, valid or not. A connection
// with a wrong or expired credential looks identical to a correctly
// configured one through Health alone -- that is inherent to what
// /health checks, not a client-side gap. Use TestConnection instead
// to verify a credential is actually accepted.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("xolu health check failed: status %d", resp.StatusCode)
	}

	return nil
}

// TestConnection verifies both that the server is reachable and that
// the client's configured credential is actually accepted -- the
// check Health cannot do (see Health's own doc comment for why).
//
// Hits GET /api/v1/schemas: authenticated like any other v1 request
// (goes through the normal request pipeline, unlike /health), cheap
// (a schema listing, no heavy work), and tenant-independent (works
// regardless of whether a tenant is configured on the client, so a
// connection can be tested before a tenant is even chosen).
//
// Returns nil only on a genuine 200. Returns *client.Error on non-2xx
// -- in particular HTTPStatus 401/403 for a rejected or missing
// credential, the exact distinction Health cannot make. Suited to a
// "Test connection" UI action: unlike Health, a wrong or expired
// credential here is reported, not silently accepted.
func (c *Client) TestConnection(ctx context.Context) error {
	var envelope struct {
		Schemas []EntityTypeSummary `json:"schemas"`
		Count   int                 `json:"count"`
	}
	return c.doURL(ctx, http.MethodGet, c.buildURLRoot("/schemas"), nil, &envelope)
}

// Ready checks if the xolu server is ready to serve traffic.
//
// Hits GET /ready. Returns nil on 200 (server is fully initialised and the
// storage layer's Ping succeeded). Returns a non-nil error on 503 (server
// is still initialising or storage is unreachable), any other non-2xx
// response, transport failure, or timeout.
//
// Ready is the correct endpoint for readiness probes and for consumers that
// want to gate their own traffic on xolu's ability to serve — for example,
// molu's health probe in its gated-dispatch design.
//
// Auth is not required to reach /ready; the endpoint is deliberately
// unauthenticated so probes work without credentials.
func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/ready", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		return nil
	}
	// 503 during initialisation or when storage is unreachable is the
	// expected "not ready yet" signal; surface it as a distinguishable error
	// so callers can retry rather than escalate.
	return fmt.Errorf("xolu readiness check failed: status %d", resp.StatusCode)
}

// GraphNeighbors retrieves neighbors of a node in the graph.
// direction must be "out", "in", or "both" (default: "out").
// xolu endpoint: POST /graph/neighbors with JSON body.
// Response: {"neighbors":{"outgoing":{node:label},"incoming":{node:label}}}
func (c *Client) GraphNeighbors(ctx context.Context, nodeID string, direction string) (*NeighborResult, error) {
	if direction == "" {
		direction = "out"
	}

	body := map[string]string{"node_id": nodeID, "direction": direction}

	var resp struct {
		Neighbors NeighborResult `json:"neighbors"`
	}

	if err := c.do(ctx, http.MethodPost, "/graph/neighbors", body, &resp); err != nil {
		return nil, err
	}

	return &resp.Neighbors, nil
}

// GraphShortestPath finds the shortest path between two nodes.
// maxDepth of 0 uses xolu's server default.
// xolu endpoint: POST /graph/shortestPath with JSON body.
// Returns PathResult with Exists=false and empty Path when no path exists.
func (c *Client) GraphShortestPath(ctx context.Context, from, to string, maxDepth int) (*PathResult, error) {
	body := map[string]any{"from": from, "to": to}
	if maxDepth > 0 {
		body["max_depth"] = maxDepth
	}

	var result PathResult

	if err := c.do(ctx, http.MethodPost, "/graph/shortestPath", body, &result); err != nil {
		return nil, err
	}

	return &result, nil
}
