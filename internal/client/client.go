// Package client is a hand-written Go client for the Descendence API,
// written directly from api/openapi.yaml rather than generated from it
// (ARCHITECTURE.md §6 decision #15 - the spec stays the contract, the code
// stays hand-written). Every exported type here mirrors a schema in that
// file; when one changes, the other must too.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Sentinels for the two error cases a caller usually wants to branch on.
// Match them with errors.Is - the concrete error is always an *APIError.
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrNotFound     = errors.New("not found")
)

// APIError is a non-2xx response, carrying the RFC 9457 problem details the
// server sent (Problem schema in api/openapi.yaml). Fields are best-effort:
// a response that isn't problem+json at all still produces an APIError, just
// with an empty Title/Detail.
type APIError struct {
	StatusCode int
	Type       string `json:"type"`
	Title      string `json:"title"`
	Status     int    `json:"status"`
	Detail     string `json:"detail"`
}

func (e *APIError) Error() string {
	switch {
	case e.Detail != "":
		return fmt.Sprintf("%s (status %d)", e.Detail, e.StatusCode)
	case e.Title != "":
		return fmt.Sprintf("%s (status %d)", e.Title, e.StatusCode)
	default:
		return fmt.Sprintf("unexpected status %d", e.StatusCode)
	}
}

// Is maps the two statuses callers branch on onto sentinels, so they can use
// errors.Is(err, client.ErrNotFound) without type-asserting.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	}
	return false
}

// Client talks to one API server as one principal.
type Client struct {
	baseURL string
	token   string

	httpClient *http.Client
	// streamClient has no timeout, for the endpoints whose duration is the
	// run's business rather than the HTTP layer's - currently only log
	// following, which lasts as long as the run does.
	//
	// A blanket http.Client.Timeout is wrong for any long-lived response, and
	// this project has now been bitten by that three times: podman's /wait in
	// task 1.19, podman's log follow in 2.1, and this. The symptom is always
	// the same and always misleading - the operation is cut off partway
	// through and reported as a network failure.
	streamClient *http.Client
}

// New returns a client for baseURL (e.g. "http://127.0.0.1:8080"),
// authenticating with token as a bearer token. A trailing slash on baseURL
// is tolerated.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		streamClient: &http.Client{},
	}
}

// requestOptions carries the per-request extras only some endpoints use.
type requestOptions struct {
	// body, if non-nil, is JSON-encoded as the request body.
	body any
	// query is appended to the path as a query string when non-empty.
	query url.Values
	// header entries are set on the request, e.g. Idempotency-Key.
	header http.Header
	// alsoOK lists non-2xx statuses that should still be decoded into out
	// rather than turned into an *APIError. Only /healthz needs this.
	alsoOK []int
}

// do issues an authenticated request and decodes a successful response into
// out (which may be nil to discard the body). A non-2xx response becomes an
// *APIError; the body is never decoded into out in that case.
func (c *Client) do(ctx context.Context, method, path string, opts requestOptions, out any) error {
	resp, err := c.send(ctx, c.httpClient, method, path, opts)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if !statusAcceptable(resp.StatusCode, opts.alsoOK) {
		return decodeAPIError(resp)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s %s response: %w", method, path, err)
	}

	return nil
}

// send builds and issues an authenticated request on the given http.Client,
// returning the raw response with its body unread. Split out of do so
// streaming endpoints can consume the body themselves instead of having it
// decoded and closed - the caller owns closing it.
func (c *Client) send(ctx context.Context, httpClient *http.Client, method, path string, opts requestOptions) (*http.Response, error) {
	var reader io.Reader
	if opts.body != nil {
		encoded, err := json.Marshal(opts.body)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	target := c.baseURL + path
	if len(opts.query) > 0 {
		target += "?" + opts.query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	for key, values := range opts.header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if opts.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	// Only when the caller has not asked for something else - a streaming
	// request sets text/event-stream and must keep it.
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	return httpClient.Do(req)
}

func statusAcceptable(status int, alsoOK []int) bool {
	if status >= 200 && status <= 299 {
		return true
	}
	for _, ok := range alsoOK {
		if status == ok {
			return true
		}
	}
	return false
}

// decodeAPIError builds an *APIError from a non-2xx response, filling in
// what it can from a problem+json body if one is present.
func decodeAPIError(resp *http.Response) error {
	apiErr := &APIError{StatusCode: resp.StatusCode}

	body, err := io.ReadAll(resp.Body)
	if err == nil {
		// Deliberately ignored: a server that answered with something other
		// than problem+json still produces a usable status-only error.
		_ = json.Unmarshal(body, apiErr)
	}

	return apiErr
}

// ServerInfo is the GET / response.
type ServerInfo struct {
	ProductName  string `json:"productName"`
	ProductBuild string `json:"productBuild"`
	APIVersion   string `json:"apiVersion"`
}

// Info calls GET /. Unauthenticated.
func (c *Client) Info(ctx context.Context) (ServerInfo, error) {
	var info ServerInfo
	err := c.do(ctx, http.MethodGet, "/", requestOptions{}, &info)
	return info, err
}

// ServerHealth is the GET /healthz response (ServerHealth schema).
type ServerHealth struct {
	HealthStatus string `json:"healthStatus"`
	DatabaseUp   bool   `json:"databaseUp"`
	PodmanUp     bool   `json:"podmanUp"`
}

// Health calls GET /healthz. Unlike every other endpoint, an unhealthy
// server answers 503 with a *valid* ServerHealth body rather than a problem
// document - so that status is decoded like a success and the caller
// inspects the fields. Any other non-2xx is still an error.
func (c *Client) Health(ctx context.Context) (ServerHealth, error) {
	var health ServerHealth
	err := c.do(ctx, http.MethodGet, "/healthz", requestOptions{
		alsoOK: []int{http.StatusServiceUnavailable},
	}, &health)
	return health, err
}

// Principal is the GET /api/v1/whoami response (Principal schema).
type Principal struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Scopes []string `json:"scopes"`
}

// WhoAmI calls GET /api/v1/whoami, resolving the token to its principal.
func (c *Client) WhoAmI(ctx context.Context) (Principal, error) {
	var principal Principal
	err := c.do(ctx, http.MethodGet, "/api/v1/whoami", requestOptions{}, &principal)
	return principal, err
}
