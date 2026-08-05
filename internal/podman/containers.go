package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CreateContainerParams is the input to CreateContainer.
type CreateContainerParams struct {
	// Stamped as the container's "run_id" label - every container this
	// client creates carries one, unconditionally. That label is what lets
	// the reconciler (task 1.15) find a container again after a crash, so
	// it is a required field here rather than left to Labels a caller might
	// forget to set.
	RunID int64
	Image string
	// Executed as-is inside the container - never joined into a shell
	// string (task 1.11).
	Command []string
}

// logDriver is the container log driver every run gets, set explicitly rather
// than inherited from the host (ARCHITECTURE.md decision #20).
//
// The host default here is journald, and journald rate-limits: the shipped
// setting is 10000 messages per 30 seconds, after which it discards the rest
// and keeps discarding for the remainder of the window. A script printing
// 20000 lines therefore loses several thousand of them, and a second run
// started inside the same window can lose *all* of its output - measured, not
// theorised, and neither podman nor the API reports anything wrong, because
// from their point of view nothing is: the lines never existed.
//
// That is fatal to the one thing this platform promises about logs, and no
// amount of care further down the pipeline can recover a line the log driver
// dropped. k8s-file writes to a file per container instead, with no limiter,
// and podman deletes it when the container is removed - which the supervisor
// does at the end of every run, after its own durable copy is safely written.
const logDriver = "k8s-file"

type createContainerRequest struct {
	Image     string            `json:"image"`
	Command   []string          `json:"command"`
	Labels    map[string]string `json:"labels"`
	LogConfig logConfig         `json:"log_configuration"`
}

type logConfig struct {
	Driver string `json:"driver"`
}

type createContainerResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

// CreateContainer calls POST /libpod/containers/create. The container is
// created but not started; call StartContainer next.
func (c *Client) CreateContainer(ctx context.Context, params CreateContainerParams) (string, error) {
	resp, err := c.do(ctx, http.MethodPost, "/libpod/containers/create", createContainerRequest{
		Image:     params.Image,
		Command:   params.Command,
		Labels:    map[string]string{"run_id": strconv.FormatInt(params.RunID, 10)},
		LogConfig: logConfig{Driver: logDriver},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp, "create container", http.StatusCreated); err != nil {
		return "", err
	}

	var created createContainerResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("podman: decoding create container response: %w", err)
	}

	return created.ID, nil
}

// StartContainer calls POST /libpod/containers/{id}/start.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/libpod/containers/"+id+"/start", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return checkStatus(resp, "start container", http.StatusNoContent, http.StatusOK)
}

// WaitContainer calls POST /libpod/containers/{id}/wait and blocks until the
// container reaches a terminal state, returning its exit code. Unlike every
// other libpod endpoint this client calls, the response body is plain text
// (Content-Type: text/plain), not JSON - just the exit code.
//
// This is the one long-polling call in this client, so it uses
// longPollClient: how long it blocks is the container's business, not the
// HTTP layer's. Bound it with ctx if you need a limit - the supervisor
// derives one from the run's own timeout (task 1.17).
func (c *Client) WaitContainer(ctx context.Context, id string) (int, error) {
	resp, err := c.doWith(ctx, c.longPollClient, http.MethodPost, "/libpod/containers/"+id+"/wait", nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp, "wait container", http.StatusOK); err != nil {
		return 0, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("podman: reading wait container response: %w", err)
	}

	exitCode, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return 0, fmt.Errorf("podman: parsing exit code %q: %w", body, err)
	}

	return exitCode, nil
}

// RemoveContainer calls DELETE /libpod/containers/{id}. The container is
// expected to already be stopped (i.e. called after WaitContainer returns).
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/libpod/containers/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return checkStatus(resp, "remove container", http.StatusOK, http.StatusNoContent)
}

// KillContainer calls POST /libpod/containers/{id}/kill, sending SIGKILL
// (libpod's default when no signal is specified) - used for timeouts (task
// 1.17), where the container has already had its chance to exit on its own.
func (c *Client) KillContainer(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/libpod/containers/"+id+"/kill", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return checkStatus(resp, "kill container", http.StatusNoContent, http.StatusOK)
}

// ContainerSummary is the subset of a GET /libpod/containers/json entry the
// reconciler (task 1.15) needs.
type ContainerSummary struct {
	ID     string            `json:"Id"`
	Labels map[string]string `json:"Labels"`
	// "created" (never started), "running", or "exited" - confirmed by
	// probing the real API with a container in each of those states (task
	// 1e). Treat this list as indicative rather than exhaustive: the
	// reconciler only special-cases "created" and adopts anything else, so
	// a state not seen here still behaves sensibly.
	State string `json:"State"`
}

// ListContainersByRunIDLabel calls GET /libpod/containers/json filtered to
// containers carrying a "run_id" label (any value), including
// stopped/never-started ones (all=true) - every container this application
// has ever created, which is exactly what the reconciler needs to compare
// against non-terminal runs in Postgres.
func (c *Client) ListContainersByRunIDLabel(ctx context.Context) ([]ContainerSummary, error) {
	filters, err := json.Marshal(map[string][]string{"label": {"run_id"}})
	if err != nil {
		return nil, fmt.Errorf("podman: encoding label filter: %w", err)
	}

	query := url.Values{}
	query.Set("all", "true")
	query.Set("filters", string(filters))

	resp, err := c.do(ctx, http.MethodGet, "/libpod/containers/json?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp, "list containers", http.StatusOK); err != nil {
		return nil, err
	}

	var containers []ContainerSummary
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("podman: decoding list containers response: %w", err)
	}

	return containers, nil
}
