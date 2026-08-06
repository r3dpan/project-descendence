package api

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// heartbeatStaleAfter must track cmd/supervisor/heartbeat.go's
// heartbeatInterval (3x it) - the two processes don't share Go code across
// the api/supervisor boundary (CLAUDE.md), so this constant is documented
// independently in both places rather than imported from one.
const heartbeatStaleAfter = 15 * time.Second

type componentStatus struct {
	Status string `json:"status"` // "up" | "down"
	Detail string `json:"detail,omitempty"`
}

type systemStatus struct {
	Database   componentStatus `json:"database"`
	Podman     componentStatus `json:"podman"`
	Supervisor componentStatus `json:"supervisor"`
}

// SystemStatusHandler backs the Dashboard's operational-status tiles. Unlike
// HealthHandler (unauthenticated, 503-on-unhealthy, meant for an infra
// prober), this always returns 200 - it is a UI status render, and a 5xx
// here would just fail the browser's fetch instead of rendering a red dot,
// which is strictly worse for this use case.
func (s *APIServer) SystemStatusHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := systemStatus{}

	if _, err := s.queries.Ping(ctx); err != nil {
		status.Database = componentStatus{Status: "down", Detail: err.Error()}
	} else {
		status.Database = componentStatus{Status: "up"}
	}

	if _, err := s.podman.Info(ctx); err != nil {
		status.Podman = componentStatus{Status: "down", Detail: err.Error()}
	} else {
		status.Podman = componentStatus{Status: "up"}
	}

	beat, err := s.queries.GetSupervisorHeartbeat(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		status.Supervisor = componentStatus{Status: "down", Detail: "no heartbeat recorded"}
	case err != nil:
		log.Printf("System status: reading supervisor heartbeat failed: %v", err)
		status.Supervisor = componentStatus{Status: "down", Detail: err.Error()}
	case time.Since(beat.LastBeatAt.Time) > heartbeatStaleAfter:
		status.Supervisor = componentStatus{Status: "down", Detail: "heartbeat stale"}
	default:
		status.Supervisor = componentStatus{Status: "up"}
	}

	writeJSON(w, http.StatusOK, status)
}
