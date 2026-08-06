package client

import (
	"context"
	"net/http"
)

// ComponentStatus mirrors the ComponentStatus schema.
type ComponentStatus struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// SystemStatus is the GET /api/v1/system/status response (SystemStatus
// schema) - off-plan web UI dashboard work, per-component operational
// status for the Dashboard's status tiles.
type SystemStatus struct {
	Database   ComponentStatus `json:"database"`
	Podman     ComponentStatus `json:"podman"`
	Supervisor ComponentStatus `json:"supervisor"`
}

// SystemStatus calls GET /api/v1/system/status. Any authenticated principal
// may call it - no specific permission required.
func (c *Client) SystemStatus(ctx context.Context) (SystemStatus, error) {
	var status SystemStatus
	err := c.do(ctx, http.MethodGet, "/api/v1/system/status", requestOptions{}, &status)
	return status, err
}

// Config mirrors the Config schema - DATABASE_URL/PODMAN_SOCKET as
// persisted in the config file internal/appconfig loads. DatabaseURL's
// password is masked ("***") on a GetConfig response.
type Config struct {
	DatabaseURL  string `json:"databaseUrl"`
	PodmanSocket string `json:"podmanSocket"`
}

// GetConfig calls GET /api/v1/config. Requires config:read.
func (c *Client) GetConfig(ctx context.Context) (Config, error) {
	var cfg Config
	err := c.do(ctx, http.MethodGet, "/api/v1/config", requestOptions{}, &cfg)
	return cfg, err
}

// PutConfig calls PUT /api/v1/config. Requires config:write. Neither
// cmd/api nor cmd/supervisor hot-reloads the config file - both processes
// must be restarted for the change to take effect.
func (c *Client) PutConfig(ctx context.Context, cfg Config) (Config, error) {
	var out Config
	err := c.do(ctx, http.MethodPut, "/api/v1/config", requestOptions{body: cfg}, &out)
	return out, err
}
