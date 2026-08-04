package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/r3dpan/project-descendence/internal/store"
)

// Arbitrary but fixed - this number *is* the lock's identity. Never change
// it without understanding that doing so silently lets two supervisors run
// at once during the transition (an old build holding the old key, a new
// build holding a different one, neither seeing the other).
const supervisorLockKey = 8817001

// acquireSingletonLock enforces "only one supervisor may run at a time"
// (ARCHITECTURE.md §3/§4.1, task 1.16) via a session-level Postgres advisory
// lock. It acquires a connection dedicated to holding that lock for the
// process's entire lifetime - never returned to the pool - and tries to
// lock non-blockingly: if another supervisor already holds it, this process
// refuses to start rather than queueing up behind it.
//
// The returned release func unlocks and returns the connection to the pool;
// call it during shutdown, after the lock is no longer needed.
func acquireSingletonLock(ctx context.Context, pool *pgxpool.Pool) (release func(), err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring a dedicated connection for the advisory lock: %w", err)
	}

	lockQueries := store.New(conn)

	locked, err := lockQueries.TryAdvisoryLock(ctx, supervisorLockKey)
	if err != nil {
		conn.Release()
		return nil, fmt.Errorf("acquiring advisory lock: %w", err)
	}
	if !locked {
		conn.Release()
		return nil, fmt.Errorf("another supervisor instance is already running (advisory lock %d is held)", supervisorLockKey)
	}

	release = func() {
		// A fresh context: shutdown may already have cancelled ctx, and
		// unlocking should still happen.
		if _, err := lockQueries.AdvisoryUnlock(context.Background(), supervisorLockKey); err != nil {
			log.Printf("failed releasing advisory lock: %v", err)
		}
		conn.Release()
	}

	return release, nil
}
