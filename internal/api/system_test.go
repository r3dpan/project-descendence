package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/store"
)

func newSystemStatusFixture(t *testing.T) (*APIServer, *store.Queries, context.Context) {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Skipf("cannot create a pool: %v", err)
	}
	t.Cleanup(pool.Close)

	queries := store.New(pool)
	if _, err := queries.Ping(ctx); err != nil {
		t.Skipf("database not reachable: %v", err)
	}

	// Every test starts from "no heartbeat has ever been recorded" - the
	// table is a true singleton (fixed id=1), so clearing it is the whole
	// reset.
	if _, err := pool.Exec(ctx, "DELETE FROM supervisor_heartbeat"); err != nil {
		t.Fatalf("clearing supervisor_heartbeat: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM supervisor_heartbeat")
	})

	// A deliberately-unreachable socket, not nil: SystemStatusHandler always
	// calls s.podman.Info, so a nil *podman.Client would panic. These tests
	// only assert on database/supervisor status - podman is expected "down".
	podmanClient := podman.NewClient(filepath.Join(t.TempDir(), "no-such.sock"))
	server := NewAPIServer("test", "test", "test", queries, podmanClient, t.TempDir(), nil, nil, "", nil, "")
	return server, queries, ctx
}

func systemStatusRequest(t *testing.T, server *APIServer, ctx context.Context) systemStatus {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	server.SystemStatusHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var status systemStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return status
}

// TestSystemStatusNoHeartbeatMeansSupervisorDown covers GetSupervisorHeartbeat
// returning pgx.ErrNoRows, which the handler must treat as "not running",
// not as a query failure that leaves the whole response broken.
func TestSystemStatusNoHeartbeatMeansSupervisorDown(t *testing.T) {
	server, _, ctx := newSystemStatusFixture(t)

	status := systemStatusRequest(t, server, ctx)

	if status.Database.Status != "up" {
		t.Errorf("expected database up, got %+v", status.Database)
	}
	if status.Supervisor.Status != "down" {
		t.Errorf("expected supervisor down with no heartbeat recorded, got %+v", status.Supervisor)
	}
}

// TestSystemStatusStaleHeartbeatMeansSupervisorDown covers a heartbeat row
// that exists but is older than heartbeatStaleAfter.
func TestSystemStatusStaleHeartbeatMeansSupervisorDown(t *testing.T) {
	server, queries, ctx := newSystemStatusFixture(t)

	stale := time.Now().Add(-2 * heartbeatStaleAfter)
	if err := queries.UpsertSupervisorHeartbeat(ctx, store.UpsertSupervisorHeartbeatParams{
		BeatAt:    pgtype.Timestamptz{Time: stale, Valid: true},
		StartedAt: pgtype.Timestamptz{Time: stale, Valid: true},
	}); err != nil {
		t.Fatalf("UpsertSupervisorHeartbeat: %v", err)
	}

	status := systemStatusRequest(t, server, ctx)

	if status.Supervisor.Status != "down" {
		t.Errorf("expected supervisor down with a stale heartbeat, got %+v", status.Supervisor)
	}
}

// TestSystemStatusFreshHeartbeatMeansSupervisorUp is the positive case.
func TestSystemStatusFreshHeartbeatMeansSupervisorUp(t *testing.T) {
	server, queries, ctx := newSystemStatusFixture(t)

	now := time.Now()
	if err := queries.UpsertSupervisorHeartbeat(ctx, store.UpsertSupervisorHeartbeatParams{
		BeatAt:    pgtype.Timestamptz{Time: now, Valid: true},
		StartedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		t.Fatalf("UpsertSupervisorHeartbeat: %v", err)
	}

	status := systemStatusRequest(t, server, ctx)

	if status.Supervisor.Status != "up" {
		t.Errorf("expected supervisor up with a fresh heartbeat, got %+v", status.Supervisor)
	}
}
