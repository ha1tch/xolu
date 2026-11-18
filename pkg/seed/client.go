// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package seed

// client.go -- the requests a load makes, through pkg/client.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ha1tch/xolu/pkg/client"
)

// DefaultPort is the port xolu listens on unless told another.
const DefaultPort = 9090

// DefaultBaseURL is XOLU_BASE_URL, else XOLU_HOST / XOLU_PORT / XOLU_ADDR, else http://127.0.0.1:9090.
func DefaultBaseURL() string {
	if explicit := os.Getenv("XOLU_BASE_URL"); explicit != "" {
		return strings.TrimRight(explicit, "/")
	}
	addrHost, addrPort := "", ""
	if addr := os.Getenv("XOLU_ADDR"); strings.Contains(addr, ":") {
		i := strings.LastIndex(addr, ":")
		addrHost, addrPort = addr[:i], addr[i+1:]
	}
	host, port := os.Getenv("XOLU_HOST"), os.Getenv("XOLU_PORT")
	if host == "" {
		host = addrHost
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = addrPort
	}
	if port == "" {
		port = fmt.Sprint(DefaultPort)
	}
	return "http://" + host + ":" + port
}

// Doer is the one method of pkg/client's Client a load needs. A program that
// already holds a connected client (with its own auth, transport and
// logging) hands it to NewClientFor.
type Doer interface {
	RawWithHeaders(ctx context.Context, method, path, contentType string, header http.Header, body io.Reader) (*client.RawResult, error)
}

// Client makes the requests of a load and counts them.
type Client struct {
	Base     string
	Requests int
	api      Doer
	headers  http.Header
	ctx      context.Context
}

// NewClient returns a client for the instance at base (http://host:port, with
// or without a path before the API). apiKey and token may be empty.
func NewClient(base, apiKey, token string, timeout time.Duration) (*Client, error) {
	trimmed := strings.TrimRight(base, "/")
	parts, err := url.Parse(trimmed)
	if err != nil || (parts.Scheme != "http" && parts.Scheme != "https") || parts.Hostname() == "" {
		return nil, errorf("\"%s\" is not a base URL (expected http://host:port)", base)
	}
	opts := []client.ClientOption{client.WithCallTimeout(timeout)}
	if token != "" {
		opts = append(opts, client.WithBearerToken(token))
	}
	c := NewClientFor(client.New(trimmed, opts...), trimmed)
	if apiKey != "" {
		c.headers.Set("X-API-Key", apiKey)
	}
	return c, nil
}

// NewClientFor returns a client that sends its requests through api, which
// is connected to the instance at base (base is only used in messages).
func NewClientFor(api Doer, base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), api: api, headers: http.Header{}, ctx: context.Background()}
}

// WithContext returns the client with the context its requests are made under.
func (c *Client) WithContext(ctx context.Context) *Client {
	c.ctx = ctx
	return c
}

// Req is what a request carries besides its method and path.
type Req struct {
	Body    any     // sent as JSON when it is not nil and there is no Raw
	Raw     []byte  // a body sent as it is
	HasRaw  bool    // there is a Raw body (which may be empty)
	Headers *Object // headers of the request's own
	Query   any     // an object of names and values; a list as a value repeats the name
}

func encodeQuery(query any) (string, error) {
	obj, ok := query.(*Object)
	if !ok {
		return "", errorf("a query must be an object of names and values, got %s", Repr(query))
	}
	var parts []string
	for _, k := range obj.Keys() {
		if list, isList := obj.Get(k).([]any); isList {
			for _, v := range list {
				parts = append(parts, quotePlus(k)+"="+quotePlus(Text(v)))
			}
			continue
		}
		parts = append(parts, quotePlus(k)+"="+quotePlus(Text(obj.Get(k))))
	}
	return strings.Join(parts, "&"), nil
}

// Request makes one request and returns the status and the body: a JSON
// value when the answer is JSON, its text when it is not, nil when it is empty.
func (c *Client) Request(method, path string, req Req) (int, any, error) {
	target := path
	if Truthy(req.Query) {
		q, err := encodeQuery(req.Query)
		if err != nil {
			return 0, nil, err
		}
		target += "?" + q
	}
	header := http.Header{}
	for name, values := range c.headers {
		header[name] = values
	}
	var body io.Reader
	switch {
	case req.HasRaw:
		body = bytes.NewReader(req.Raw)
		header["Content-Type"] = nil // none unless the request names one
	case req.Body != nil:
		body = strings.NewReader(DumpsASCII(req.Body))
	}
	for _, name := range req.Headers.Keys() {
		header[http.CanonicalHeaderKey(name)] = []string{Text(req.Headers.Get(name))}
	}
	res, err := c.api.RawWithHeaders(c.ctx, method, target, "", header, body)
	if err != nil {
		return 0, nil, errorf("%s %s%s: %s", method, c.Base, path, err)
	}
	c.Requests++
	if len(res.Body) == 0 {
		return res.StatusCode, nil, nil
	}
	text := strings.ToValidUTF8(string(res.Body), "�")
	if parsed, perr := ParseJSON(text); perr == nil {
		return res.StatusCode, parsed, nil
	}
	return res.StatusCode, text, nil
}

// WaitReady waits until the instance answers its health check, for as long as given.
func (c *Client) WaitReady(wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		last := ""
		status, _, err := c.Request("GET", "/health", Req{})
		switch {
		case err != nil:
			last = err.Error()
		case status >= 200 && status < 300:
			return nil
		default:
			last = fmt.Sprintf("HTTP %d", status)
		}
		if !time.Now().Before(deadline) {
			return errorf("xolu not ready at %s after %ss (%s)", c.Base, FloatText(wait.Seconds()), last)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
