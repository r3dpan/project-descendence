package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/store"
)

// heartbeatInterval: liveness for a dashboard tile needs no sub-5-second
// resolution, and beating on pollInterval (1s) would just add needless
// Postgres write load for no benefit - scheduleSyncInterval's cadence
// (low-urgency background work) is the closer precedent. internal/api's
// SystemStatusHandler treats a heartbeat older than 3x this as stale; that
// threshold is documented there since api and supervisor don't share Go
// code across the process boundary (CLAUDE.md).
const heartbeatInterval = 5 * time.Second

// runHeartbeatLoop is runClaimLoop's structural twin: it beats immediately,
// then on every tick, until ctx is cancelled. Only ever launched after
// acquireSingletonLock succeeds, so a beat in the table always means the
// lock-holding supervisor is alive, not just any process that once started.
func runHeartbeatLoop(ctx context.Context, queries *store.Queries) {
	startedAt := time.Now()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	beat := func() {
		if err := queries.UpsertSupervisorHeartbeat(ctx, store.UpsertSupervisorHeartbeatParams{
			BeatAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			StartedAt: pgtype.Timestamptz{Time: startedAt, Valid: true},
		}); err != nil {
			log.Printf("heartbeat: %v", err)
		}
	}

	beat()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beat()
		}
	}
}
