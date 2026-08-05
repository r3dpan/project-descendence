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

	"github.com/r3dpan/project-descendence/internal/podman"
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

	releaseLock, err := acquireSingletonLock(ctx, pool)
	if err != nil {
		log.Fatalf("Refusing to start: %v", err)
	}
	defer releaseLock()

	queries := store.New(pool)

	podmanSocket := os.Getenv("PODMAN_SOCKET")
	if podmanSocket == "" {
		log.Fatal("PODMAN_SOCKET is not set")
	}
	podmanClient := podman.NewClient(podmanSocket)

	log.Println("Reconciling non-terminal runs from a previous run")
	reconcile(ctx, queries, podmanClient)

	log.Printf("Supervisor started, polling for queued runs every %s", pollInterval)
	runClaimLoop(ctx, queries, podmanClient)
	log.Println("Supervisor shutting down")
}

// runClaimLoop drains every currently queued run on each tick - claiming and
// executing it to completion before claiming the next - then waits for the
// next tick (or shutdown). Runs within a single supervisor process execute
// one at a time; nothing here bounds or parallelizes them yet.
func runClaimLoop(ctx context.Context, queries *store.Queries, podmanClient *podman.Client) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		claimAndExecuteAllQueued(ctx, queries, podmanClient)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func claimAndExecuteAllQueued(ctx context.Context, queries *store.Queries, podmanClient *podman.Client) {
	for {
		run, err := queries.ClaimNextQueuedRun(ctx)
		if err != nil {
			// ErrNoRows just means the queue is empty. A cancelled context
			// means we are shutting down - Phase 1e showed that logging it
			// as "claim: context canceled" makes an ordinary SIGTERM look
			// like a fault in the logs.
			if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
				log.Printf("claim: %v", err)
			}
			return
		}

		log.Printf("claimed run %d (image=%s argv=%v)", run.ID, run.ImageRef, run.Argv)
		executeRun(ctx, queries, podmanClient, run)
	}
}
