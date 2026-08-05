package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration tests against a real Postgres, skipping cleanly when
// DATABASE_URL isn't set - the same pattern internal/podman and
// internal/client use. Run them with:
//
//	DATABASE_URL=postgres://... go test ./internal/store
func newTestQueries(t *testing.T) (*Queries, context.Context) {
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

	queries := New(pool)
	if _, err := queries.Ping(ctx); err != nil {
		t.Skipf("database not reachable: %v", err)
	}

	return queries, ctx
}

// testPrincipalID returns a principal to hang test runs off. Any one will
// do - these tests never look at it.
func testPrincipalID(t *testing.T, queries *Queries, ctx context.Context) int64 {
	t.Helper()

	runs, err := queries.ListRuns(ctx, ListRunsParams{RowLimit: 1})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) == 0 {
		t.Skip("no runs exist, so no principal to borrow; seed one first")
	}

	return runs[0].PrincipalID
}

func createTestRun(t *testing.T, queries *Queries, ctx context.Context) Run {
	t.Helper()

	run, err := queries.CreateRun(ctx, CreateRunParams{
		PrincipalID:    testPrincipalID(t, queries, ctx),
		ImageRef:       "test.invalid/never-pulled:latest",
		Argv:           []string{"true"},
		TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	return run
}

// A terminal state is final (task 1.14). The failure this guards against is
// concrete: a reconciler that was slow to notice a run had finished would
// otherwise rewrite a real `succeeded`, with its exit code, as `lost`.
//
// Note what this test deliberately does NOT do: call ClaimNextQueuedRun to
// get its run into `running`. That query takes whichever queued run is
// oldest, not the one you want, so a test using it steals runs belonging to
// anything else touching the same database and abandons them mid-flight.
// Finishing straight from `queued` exercises the same guard - FinishRun
// accepts either non-terminal state - without reaching outside its own row.
func TestFinishRunWillNotOverwriteATerminalState(t *testing.T) {
	queries, ctx := newTestQueries(t)

	run := createTestRun(t, queries, ctx)

	// A supervisor may be running against this same database and may reach
	// the run first. Either way one writer records a terminal state; which
	// one it was doesn't matter, only that the second cannot undo it.
	rows, err := queries.FinishRun(ctx, FinishRunParams{
		ID:       run.ID,
		State:    StateSucceeded,
		ExitCode: pgtype.Int4{Int32: 0, Valid: true},
	})
	if err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	settled, err := queries.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if !IsTerminal(settled.State) {
		t.Fatalf("run %d is %q after being finished (%d rows), want a terminal state", run.ID, settled.State, rows)
	}

	// The case that matters: a second writer recording a different outcome
	// for a run that is already done.
	rows, err = queries.FinishRun(ctx, FinishRunParams{
		ID:            run.ID,
		State:         StateLost,
		FailureReason: pgtype.Text{String: "a slow reconciler", Valid: true},
	})
	if err != nil {
		t.Fatalf("second FinishRun returned an error, want a clean zero-row no-op: %v", err)
	}
	if rows != 0 {
		t.Errorf("overwriting a terminal state affected %d rows, want 0", rows)
	}

	after, err := queries.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if after.State != settled.State {
		t.Errorf("run %d went from %q to %q; its real outcome was overwritten", run.ID, settled.State, after.State)
	}
	if after.ExitCode != settled.ExitCode {
		t.Errorf("run %d's exit code changed from %+v to %+v", run.ID, settled.ExitCode, after.ExitCode)
	}
	if after.FailureReason != settled.FailureReason {
		t.Errorf("run %d's failure reason changed from %+v to %+v", run.ID, settled.FailureReason, after.FailureReason)
	}
}

// ListNonTerminalRuns feeds the reconciler, so its idea of "non-terminal"
// has to be exactly IsTerminal's - a mismatch either strands runs forever
// or has the reconciler trample finished ones.
func TestListNonTerminalRunsAgreesWithIsTerminal(t *testing.T) {
	queries, ctx := newTestQueries(t)

	runs, err := queries.ListNonTerminalRuns(ctx)
	if err != nil {
		t.Fatalf("ListNonTerminalRuns: %v", err)
	}

	for _, run := range runs {
		if IsTerminal(run.State) {
			t.Errorf("run %d is %q, which IsTerminal calls terminal, yet it was listed as active", run.ID, run.State)
		}
	}
}
