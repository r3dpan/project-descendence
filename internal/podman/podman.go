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

type Client struct {
	httpClient *http.Client
}

// NewClient dials socketPath (e.g. $XDG_RUNTIME_DIR/podman/podman.sock) for
// every request. The host in request URLs is a placeholder - it's ignored,
// since DialContext always connects to the same Unix socket regardless.
func NewClient(socketPath string) *Client {
	dialer := net.Dialer{}
	dialContext := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socketPath)
	}

	return &Client{
		httpClient: &http.Client{
			Transport: &http.Transport{DialContext: dialContext},
			Timeout:   10 * time.Second,
		},
	}
}

// do issues a request against the libpod API. body, if non-nil, is
// JSON-encoded as the request body.
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("podman: encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, "http://d/"+apiVersion+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
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
