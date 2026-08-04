package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/r3dpan/project-descendence/internal/store"
)

const pollInterval = 1 * time.Second

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("Failed creating database pool: %v", err)
	}
	defer pool.Close()

	queries := store.New(pool)

	log.Printf("Supervisor started, polling for queued runs every %s", pollInterval)
	runClaimLoop(ctx, queries)
	log.Println("Supervisor shutting down")
}

// runClaimLoop drains every currently queued run on each tick, then waits
// for the next tick (or shutdown). Execution (task 1.13) isn't wired in yet -
// for now a claimed run just sits in state=running.
func runClaimLoop(ctx context.Context, queries *store.Queries) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		claimAllQueued(ctx, queries)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func claimAllQueued(ctx context.Context, queries *store.Queries) {
	for {
		run, err := queries.ClaimNextQueuedRun(ctx)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				log.Printf("claim: %v", err)
			}
			return
		}

		log.Printf("claimed run %d (image=%s argv=%v)", run.ID, run.ImageRef, run.Argv)
	}
}
