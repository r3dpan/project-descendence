package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// indexRows builds count consecutive index rows for a run, alternating
// streams, as a capture would.
func indexRows(runID int64, count int) []InsertRunLogsParams {
	rows := make([]InsertRunLogsParams, 0, count)

	// Literal stream names rather than a shared constant: these are what
	// run_logs_stream_check enforces, and a test that spells them out fails
	// loudly if that constraint ever drifts.
	var offset int64
	for i := 1; i <= count; i++ {
		stream := "stdout"
		if i%3 == 0 {
			stream = "stderr"
		}

		const length = 8
		rows = append(rows, InsertRunLogsParams{
			RunID:      runID,
			Seq:        int64(i),
			Stream:     stream,
			Ts:         pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			ByteOffset: offset,
			ByteLength: length,
		})
		offset += length + 1
	}

	return rows
}

func insertTestLogs(t *testing.T, queries *Queries, ctx context.Context, runID int64, count int) {
	t.Helper()

	inserted, err := queries.InsertRunLogs(ctx, indexRows(runID, count))
	if err != nil {
		t.Fatalf("InsertRunLogs: %v", err)
	}
	if inserted != int64(count) {
		t.Fatalf("InsertRunLogs wrote %d rows, want %d", inserted, count)
	}
}

func TestInsertAndListRunLogs(t *testing.T) {
	queries, ctx := newTestQueries(t)

	run := createTestRun(t, queries, ctx)
	insertTestLogs(t, queries, ctx, run.ID, 5)

	logs, err := queries.ListRunLogs(ctx, ListRunLogsParams{RunID: run.ID, AfterSeq: 0, RowLimit: 100})
	if err != nil {
		t.Fatalf("ListRunLogs: %v", err)
	}
	if len(logs) != 5 {
		t.Fatalf("got %d rows, want 5", len(logs))
	}

	for i, entry := range logs {
		if entry.Seq != int64(i+1) {
			t.Errorf("row %d has seq %d, want %d - ListRunLogs must return sequence order", i, entry.Seq, i+1)
		}
	}
}

// after_seq is what makes both pagination (task 2.4) and Last-Event-ID
// replay (task 2.6) work, and it must be *strictly* after: a client that has
// seen seq 3 asking for "after 3" must not be handed 3 again.
func TestListRunLogsPagesStrictlyAfterASequence(t *testing.T) {
	queries, ctx := newTestQueries(t)

	run := createTestRun(t, queries, ctx)
	insertTestLogs(t, queries, ctx, run.ID, 10)

	var seen []int64
	after := int64(0)
	for {
		page, err := queries.ListRunLogs(ctx, ListRunLogsParams{RunID: run.ID, AfterSeq: after, RowLimit: 3})
		if err != nil {
			t.Fatalf("ListRunLogs: %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, entry := range page {
			seen = append(seen, entry.Seq)
		}
		after = page[len(page)-1].Seq
	}

	if len(seen) != 10 {
		t.Fatalf("paging saw %d rows, want 10 - no gaps and no repeats", len(seen))
	}
	for i, seq := range seen {
		if seq != int64(i+1) {
			t.Fatalf("paged sequence %v is not 1..10 in order", seen)
		}
	}
}

// The reconciler recaptures an adopted run from scratch, truncating the file.
// If the old index rows survived that, they would address bytes that no
// longer exist - and worse, the recapture's own rows would collide with them
// on the (run_id, seq) primary key.
func TestDeleteRunLogsClearsTheIndexForRecapture(t *testing.T) {
	queries, ctx := newTestQueries(t)

	run := createTestRun(t, queries, ctx)
	insertTestLogs(t, queries, ctx, run.ID, 4)

	if err := queries.DeleteRunLogs(ctx, run.ID); err != nil {
		t.Fatalf("DeleteRunLogs: %v", err)
	}

	count, err := queries.CountRunLogs(ctx, run.ID)
	if err != nil {
		t.Fatalf("CountRunLogs: %v", err)
	}
	if count != 0 {
		t.Fatalf("run %d still has %d index rows after a delete", run.ID, count)
	}

	// Recapture must now succeed at seq 1 again, which is the point.
	insertTestLogs(t, queries, ctx, run.ID, 2)
}

// A run only enters the sweep once it is finished and past the cutoff. A
// still-running run must never have its logs deleted out from under it.
func TestListRunsWithExpiredLogsOnlyTakesFinishedRunsPastTheCutoff(t *testing.T) {
	queries, ctx := newTestQueries(t)

	unfinished := createTestRun(t, queries, ctx)

	finished := createTestRun(t, queries, ctx)
	if _, err := queries.FinishRun(ctx, FinishRunParams{
		ID:       finished.ID,
		State:    StateSucceeded,
		ExitCode: pgtype.Int4{Int32: 0, Valid: true},
	}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	// A cutoff in the future makes every finished run expired, so the test
	// does not depend on how old the database's contents happen to be.
	expired, err := queries.ListRunsWithExpiredLogs(ctx, ListRunsWithExpiredLogsParams{
		Cutoff:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		RowLimit: 1000,
	})
	if err != nil {
		t.Fatalf("ListRunsWithExpiredLogs: %v", err)
	}

	inExpired := func(id int64) bool {
		for _, candidate := range expired {
			if candidate == id {
				return true
			}
		}
		return false
	}

	if !inExpired(finished.ID) {
		t.Errorf("finished run %d was not listed as expired", finished.ID)
	}
	if inExpired(unfinished.ID) {
		t.Errorf("run %d is still queued, yet it was listed for log pruning", unfinished.ID)
	}

	// A cutoff in the past must exclude a run that finished just now.
	recent, err := queries.ListRunsWithExpiredLogs(ctx, ListRunsWithExpiredLogsParams{
		Cutoff:   pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
		RowLimit: 1000,
	})
	if err != nil {
		t.Fatalf("ListRunsWithExpiredLogs: %v", err)
	}
	for _, candidate := range recent {
		if candidate == finished.ID {
			t.Errorf("run %d finished seconds ago, yet a one-hour cutoff listed it as expired", finished.ID)
		}
	}
}

// Marking a run pruned takes it out of the sweep permanently - otherwise
// every sweep would revisit every old run forever.
func TestMarkRunLogsPrunedRemovesARunFromTheSweep(t *testing.T) {
	queries, ctx := newTestQueries(t)

	run := createTestRun(t, queries, ctx)
	if _, err := queries.FinishRun(ctx, FinishRunParams{
		ID:       run.ID,
		State:    StateSucceeded,
		ExitCode: pgtype.Int4{Int32: 0, Valid: true},
	}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	if err := queries.MarkRunLogsPruned(ctx, run.ID); err != nil {
		t.Fatalf("MarkRunLogsPruned: %v", err)
	}

	expired, err := queries.ListRunsWithExpiredLogs(ctx, ListRunsWithExpiredLogsParams{
		Cutoff:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		RowLimit: 1000,
	})
	if err != nil {
		t.Fatalf("ListRunsWithExpiredLogs: %v", err)
	}
	for _, candidate := range expired {
		if candidate == run.ID {
			t.Fatalf("run %d was already pruned, yet the sweep listed it again", run.ID)
		}
	}

	// And the marker is readable, which is what lets the API tell "printed
	// nothing" from "output was deleted".
	after, err := queries.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if !after.LogsPrunedAt.Valid {
		t.Error("logs_pruned_at is still null after the run was marked pruned")
	}
}

// run_logs rows must not outlive their run. Nothing deletes runs today, but
// the cascade is what stops a future retention policy for *runs* from
// leaving orphaned index rows behind.
func TestRunLogsCascadeWithTheirRun(t *testing.T) {
	queries, ctx := newTestQueries(t)

	run := createTestRun(t, queries, ctx)
	insertTestLogs(t, queries, ctx, run.ID, 3)

	if _, err := queries.db.Exec(ctx, "DELETE FROM runs WHERE id = $1", run.ID); err != nil {
		t.Fatalf("deleting the run: %v", err)
	}

	count, err := queries.CountRunLogs(ctx, run.ID)
	if err != nil {
		t.Fatalf("CountRunLogs: %v", err)
	}
	if count != 0 {
		t.Errorf("run %d was deleted but %d of its index rows survived", run.ID, count)
	}
}
