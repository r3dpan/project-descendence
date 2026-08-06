package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/r3dpan/project-descendence/internal/appconfig"
)

func newConfigFixture(t *testing.T) *APIServer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.env")
	if err := appconfig.Save(path, appconfig.Config{
		DatabaseURL:  "postgres://alice:s3cret@localhost:5432/descendence?sslmode=disable",
		PodmanSocket: "/run/podman/podman.sock",
	}); err != nil {
		t.Fatalf("seeding config file: %v", err)
	}
	return NewAPIServer("test", "test", "test", nil, nil, "", nil, nil, path)
}

func putConfig(t *testing.T, server *APIServer, body configResponse) (int, configResponse) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling request body: %v", err)
	}
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	server.PutConfigHandler(w, r)

	var out configResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
	}
	return w.Code, out
}

// TestPutConfigPreservesPasswordWhenMaskResubmitted covers the one
// non-obvious piece of logic in this handler: GetConfigHandler masks the
// stored password as "***", and if a client edits only podmanSocket and
// resubmits that mask unchanged, PutConfigHandler must splice the real
// password back in rather than persisting the literal string "***".
func TestPutConfigPreservesPasswordWhenMaskResubmitted(t *testing.T) {
	server := newConfigFixture(t)

	code, resp := putConfig(t, server, configResponse{
		DatabaseURL:  "postgres://alice:***@localhost:5432/descendence?sslmode=disable",
		PodmanSocket: "/run/podman/new.sock",
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.PodmanSocket != "/run/podman/new.sock" {
		t.Errorf("expected podmanSocket to update, got %q", resp.PodmanSocket)
	}

	stored, err := appconfig.Load(server.appconfigPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.DatabaseURL != "postgres://alice:s3cret@localhost:5432/descendence?sslmode=disable" {
		t.Errorf("expected the real password to survive a resubmitted mask, got %q", stored.DatabaseURL)
	}
}

// TestPutConfigAcceptsANewPassword covers the opposite path: a client that
// deliberately overwrites the whole databaseUrl (a real password change)
// must have that value persisted verbatim, not confused for a mask.
func TestPutConfigAcceptsANewPassword(t *testing.T) {
	server := newConfigFixture(t)

	code, _ := putConfig(t, server, configResponse{
		DatabaseURL:  "postgres://alice:newpassword@localhost:5432/descendence?sslmode=disable",
		PodmanSocket: "/run/podman/podman.sock",
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	stored, err := appconfig.Load(server.appconfigPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.DatabaseURL != "postgres://alice:newpassword@localhost:5432/descendence?sslmode=disable" {
		t.Errorf("expected the new password to persist verbatim, got %q", stored.DatabaseURL)
	}
}

// TestPutConfigRejectsBadShape covers shape-only validation - no live
// connection attempt is made (see PutConfigHandler's comment).
func TestPutConfigRejectsBadShape(t *testing.T) {
	server := newConfigFixture(t)

	code, _ := putConfig(t, server, configResponse{DatabaseURL: "not-a-url", PodmanSocket: "/run/podman/podman.sock"})
	if code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for a non-postgres scheme, got %d", code)
	}

	code, _ = putConfig(t, server, configResponse{DatabaseURL: "postgres://localhost/db", PodmanSocket: ""})
	if code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for an empty podmanSocket, got %d", code)
	}
}

// TestGetConfigMasksPassword covers the read side.
func TestGetConfigMasksPassword(t *testing.T) {
	server := newConfigFixture(t)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	w := httptest.NewRecorder()
	server.GetConfigHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp configResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.DatabaseURL != "postgres://alice:***@localhost:5432/descendence?sslmode=disable" {
		t.Errorf("expected the password to be masked, got %q", resp.DatabaseURL)
	}
}
