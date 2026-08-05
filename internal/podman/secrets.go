package podman

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// SecretCreate stores value under name via POST /libpod/secrets/create,
// returning the secret's id.
//
// Used only for a run's mount-type params (task 6.6): a per-run secret,
// created just before the container that needs it and removed alongside it
// by the caller - never reused across runs, unlike the usual Podman secret
// workflow of naming a long-lived one in advance.
//
// Podman's secrets are unencrypted at rest with the default `file` driver
// (ARCHITECTURE.md §4.6, decision #9) - acceptable for a single-user
// homelab, the same posture this platform already takes with everything
// else it trusts its own host with.
func (c *Client) SecretCreate(ctx context.Context, name string, value []byte) (string, error) {
	endpoint := "/libpod/secrets/create?" + url.Values{"name": {name}}.Encode()

	resp, err := c.doRaw(ctx, c.httpClient, http.MethodPost, endpoint, "application/octet-stream", bytes.NewReader(value))
	if err != nil {
		return "", fmt.Errorf("podman: creating secret %s: %w", name, err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp, "create secret", http.StatusOK, http.StatusCreated); err != nil {
		return "", err
	}

	var created struct {
		ID string `json:"ID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("podman: decoding create secret response: %w", err)
	}
	return created.ID, nil
}

// SecretRemove deletes a secret by name or id
// (DELETE /libpod/secrets/{nameOrID}).
//
// A missing secret is not an error - a run's cleanup path (task 6.6) calls
// this unconditionally alongside container removal, including for a run
// that failed before the secret was ever created, or one whose cleanup ran
// twice (crash recovery retrying what a previous attempt already did).
func (c *Client) SecretRemove(ctx context.Context, nameOrID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/libpod/secrets/"+url.PathEscape(nameOrID), nil)
	if err != nil {
		return fmt.Errorf("podman: removing secret %s: %w", nameOrID, err)
	}
	defer resp.Body.Close()

	return checkStatus(resp, "remove secret", http.StatusOK, http.StatusNoContent, http.StatusNotFound)
}
