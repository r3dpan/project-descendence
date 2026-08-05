package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/runlog"
	"github.com/r3dpan/project-descendence/internal/store"
)

// Log retention (task 2.2), resolving the open question in ARCHITECTURE.md
// §8. The policy in one sentence: **run records are kept forever, their
// output is kept for 30 days.**
//
// Why time and not size or count: the question a homelab operator actually
// asks is "can I still see what last month's backup printed", which is a
// question about time. A count-based policy answers it differently depending
// on how busy the month was, and a size-based one lets one chatty job evict
// everybody else's history.
//
// Why the run row survives its logs: the run is the audit trail - what ran,
// when, under whose token, and how it ended - and that is small, structured
// and worth keeping indefinitely. The output is the bulky part, and it is the
// part whose value decays.
//
// Deliberately not implemented: a per-run size cap. A runaway script can
// still fill the disk between sweeps. Revisit if that ever actually happens;
// the honest answer today is that it has not.
const (
	defaultLogRetention = 30 * 24 * time.Hour

	// pruneInterval is how often the sweep runs. Retention is measured in
	// days, so there is nothing to gain from sweeping more often than hourly.
	pruneInterval = 1 * time.Hour

	// pruneBatchSize bounds one sweep's work. A supervisor started after a
	// long outage should not spend an unbounded first pass clearing a year of
	// backlog before it starts claiming runs.
	pruneBatchSize = 500
)

// logRetention reads RUN_LOG_RETENTION (a Go duration, e.g. "720h") or falls
// back to the default. Unlike DATABASE_URL and friends this is optional: an
// unset connection string is a misconfiguration, but an unset retention just
// means "the standard policy".
func logRetention() time.Duration {
	raw := os.Getenv("RUN_LOG_RETENTION")
	if raw == "" {
		return defaultLogRetention
	}

	retention, err := time.ParseDuration(raw)
	if err != nil {
		log.Fatalf("RUN_LOG_RETENTION %q is not a duration: %v", raw, err)
	}
	if retention <= 0 {
		log.Fatalf("RUN_LOG_RETENTION %q must be positive", raw)
	}

	return retention
}

// runPruneLoop sweeps expired logs on startup and then hourly, until ctx is
// cancelled. It runs in the supervisor rather than the API because the
// supervisor holds the advisory lock (task 1.16), so there is exactly one of
// it - and deleting the same files from two processes at once is a race
// nobody needs.
func runPruneLoop(ctx context.Context, queries *store.Queries, logDir string, retention time.Duration) {
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()

	for {
		pruneExpiredLogs(ctx, queries, logDir, retention)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pruneExpiredLogs deletes the output of runs that finished longer ago than
// retention, up to pruneBatchSize of them.
//
// The order within a run matters. Index rows go first, so that at no point
// does a row address a file that has been deleted - a reader that arrives
// mid-sweep sees the logs as already gone rather than getting an I/O error
// from a half-pruned run. The run is marked last, so a crash part-way through
// leaves it to be swept again instead of recording it as done with its output
// still on disk.
func pruneExpiredLogs(ctx context.Context, queries *store.Queries, logDir string, retention time.Duration) {
	cutoff := time.Now().Add(-retention)

	runIDs, err := queries.ListRunsWithExpiredLogs(ctx, store.ListRunsWithExpiredLogsParams{
		Cutoff:   pgtype.Timestamptz{Time: cutoff, Valid: true},
		RowLimit: pruneBatchSize,
	})
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("prune: listing runs with expired logs: %v", err)
		}
		return
	}
	if len(runIDs) == 0 {
		return
	}

	var pruned int
	for _, runID := range runIDs {
		if err := queries.DeleteRunLogs(ctx, runID); err != nil {
			log.Printf("prune: run %d: deleting log index: %v", runID, err)
			continue
		}

		// A missing file is the normal case for a run that printed nothing,
		// not a failure.
		if err := os.Remove(runlog.Path(logDir, runID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("prune: run %d: deleting log file: %v", runID, err)
			continue
		}

		if err := queries.MarkRunLogsPruned(ctx, runID); err != nil {
			log.Printf("prune: run %d: marking pruned: %v", runID, err)
			continue
		}

		pruned++
	}

	log.Printf("prune: cleared the logs of %d run(s) finished before %s", pruned, cutoff.Format(time.RFC3339))
}
