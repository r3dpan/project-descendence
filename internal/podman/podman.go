// Package podman is a thin HTTP client over the rootless Podman socket.
// Plain net/http with a custom DialContext, not the official bindings -
// see ARCHITECTURE.md §4.3/§6 decision #3 for why.
package podman

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const apiVersion = "v5.0.0"

// requestTimeout bounds the ordinary request/response calls, which libpod
// answers immediately. It deliberately does NOT apply to /wait - see
// longPollClient.
const requestTimeout = 10 * time.Second

type Client struct {
	// httpClient handles every endpoint that answers immediately.
	httpClient *http.Client
	// longPollClient handles /wait, which blocks for as long as the
	// container runs. A blanket http.Client.Timeout is exactly wrong there:
	// it would cap every run at requestTimeout and report a perfectly
	// healthy long run as an infrastructure failure. The wait is bounded by
	// the caller's context instead (the supervisor derives one from the
	// run's own timeout - task 1.17).
	longPollClient *http.Client
}

// NewClient dials socketPath (e.g. $XDG_RUNTIME_DIR/podman/podman.sock) for
// every request. The host in request URLs is a placeholder - it's ignored,
// since DialContext always connects to the same Unix socket regardless.
func NewClient(socketPath string) *Client {
	dialer := net.Dialer{}
	dialContext := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socketPath)
	}
	transport := &http.Transport{DialContext: dialContext}

	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
		},
		longPollClient: &http.Client{
			Transport: transport,
		},
	}
}

// do issues a request against the libpod API. body, if non-nil, is
// JSON-encoded as the request body.
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	return c.doWith(ctx, c.httpClient, method, path, body)
}

// doWith is do, with the http.Client made explicit - so /wait can opt out
// of the request timeout without every other endpoint losing it.
func (c *Client) doWith(ctx context.Context, httpClient *http.Client, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	contentType := ""
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("podman: encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
		contentType = "application/json"
	}

	return c.doRaw(ctx, httpClient, method, path, contentType, reader)
}

// doRaw sends a body libpod does not want as JSON, which until task 3.5 was
// nothing at all - every endpoint this client used took JSON or took no body.
// The archive endpoint takes a tar stream, so the encoding had to become the
// caller's decision rather than something doWith did unconditionally.
//
// contentType may be empty for a request with no body.
func (c *Client) doRaw(ctx context.Context, httpClient *http.Client, method, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://d/"+apiVersion+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return httpClient.Do(req)
}

// apiError is libpod's JSON error shape, e.g.
// {"cause":"image not known","message":"no such image: ...","response":404}.
type apiError struct {
	Cause    string `json:"cause"`
	Message  string `json:"message"`
	Response int    `json:"response"`
}

// checkStatus returns nil if resp's status is one of want, otherwise an
// error built from libpod's JSON error body when present.
func checkStatus(resp *http.Response, op string, want ...int) error {
	for _, w := range want {
		if resp.StatusCode == w {
			return nil
		}
	}

	body, _ := io.ReadAll(resp.Body)

	var apiErr apiError
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Message != "" {
		return fmt.Errorf("podman: %s: %s (status %d)", op, apiErr.Message, resp.StatusCode)
	}

	return fmt.Errorf("podman: %s: unexpected status %s", op, resp.Status)
}

// Info is the subset of GET /libpod/info this codebase currently uses.
type Info struct {
	Host struct {
		Arch string `json:"arch"`
		OS   string `json:"os"`
	} `json:"host"`
	Version struct {
		APIVersion string `json:"APIVersion"`
		Version    string `json:"Version"`
	} `json:"version"`
}

// Info calls GET /libpod/info - the first Podman endpoint this client
// exercised, per PLAN.md task 1.9.
func (c *Client) Info(ctx context.Context) (Info, error) {
	resp, err := c.do(ctx, http.MethodGet, "/libpod/info", nil)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp, "get info", http.StatusOK); err != nil {
		return Info{}, err
	}

	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return Info{}, fmt.Errorf("podman: decoding /libpod/info: %w", err)
	}

	return info, nil
}
