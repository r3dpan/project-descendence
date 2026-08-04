// Package podman is a thin HTTP client over the rootless Podman socket.
// Plain net/http with a custom DialContext, not the official bindings -
// see ARCHITECTURE.md §4.3/§6 decision #3 for why.
package podman

import (
	"context"
	"encoding/json"
	"fmt"
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
// exercises, per PLAN.md task 1.9.
func (c *Client) Info(ctx context.Context) (Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://d/"+apiVersion+"/libpod/info", nil)
	if err != nil {
		return Info{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("podman: GET /libpod/info: unexpected status %s", resp.Status)
	}

	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return Info{}, fmt.Errorf("podman: decoding /libpod/info: %w", err)
	}

	return info, nil
}
