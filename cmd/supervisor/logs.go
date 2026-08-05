package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/logstream"
	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/runlog"
	"github.com/r3dpan/project-descendence/internal/store"
)

const (
	// captureGrace bounds how long the supervisor waits for a run's log
	// capture to drain after the container has exited. The stream ends by
	// itself when the container does, so this only matters when something has
	// gone wrong - and the claim loop is serial, so waiting forever on one
	// stuck capture would stall every queued run behind it.
	captureGrace = 5 * time.Second

	// captureStopGrace bounds the second wait, after the capture has been told
	// to stop. Reaching this one means a cancelled context did not unblock the
	// capture, which should be impossible; it is logged as loudly as it
	// deserves rather than hanging the supervisor on the strength of an
	// assumption.
	captureStopGrace = 5 * time.Second

	// lineQueueDepth is how many captured lines may be waiting to be indexed
	// before the capture loop blocks. Blocking is the intended behaviour: the
	// index is authoritative, so applying backpressure to the reader is
	// correct where dropping lines would not be. (Dropping is for SSE
	// subscribers - task 2.3 - who can always re-read the history.)
	lineQueueDepth = 1024

	// maxIndexBatch caps how many index rows go into a single COPY.
	maxIndexBatch = 500
)

// logCapture is one run's in-flight capture goroutine (task 2.1). Exactly one
// exists per run: the supervisor attaches to a container's output once, no
// matter how many clients are eventually watching it (ARCHITECTURE.md §4.2).
type logCapture struct {
	runID int64
	// cancel stops the capture. Its own context, derived from the
	// supervisor's, is what makes "stop waiting for this capture" mean
	// something: without it, wait could only ever walk away from a capture
	// that then went on running - reading a container the supervisor is about
	// to remove, writing a file for a run it has already finished.
	cancel context.CancelFunc
	done   chan struct{}
}

// startLogCapture attaches to containerID's output, writes it to run runID's
// log file and indexes it in Postgres, in the background. Call it once the
// container is started; call wait before removing the container.
//
// Capture failing never fails the run. Losing the output of a script that ran
// correctly is worth a loud log line, but it is not an execution failure and
// rewriting a successful run as failed because of it would be a lie.
func startLogCapture(ctx context.Context, queries *store.Queries, podmanClient *podman.Client, logDir string, runID int64, containerID string) *logCapture {
	captureCtx, cancel := context.WithCancel(ctx)
	capture := &logCapture{runID: runID, cancel: cancel, done: make(chan struct{})}

	go func() {
		defer close(capture.done)
		defer cancel()

		if err := captureLogs(captureCtx, queries, podmanClient, logDir, runID, containerID); err != nil {
			// Three ways to get here, and they are not the same event. The
			// supervisor shutting down is normal (the claim loop learned this
			// distinction in Phase 1e); being told to stop is wait giving up
			// and has already been logged; anything else is a real fault.
			switch {
			case ctx.Err() != nil:
				log.Printf("run %d: log capture stopped early (%v); the rest of the output is not recorded", runID, ctx.Err())
			case captureCtx.Err() != nil:
				// Already reported by wait, which asked for this.
			default:
				log.Printf("run %d: log capture: %v", runID, err)
			}
		}
	}()

	return capture
}

// captureLogs follows the container's output to EOF - which libpod produces
// when the container exits - splitting it into numbered lines in the run's
// log file and recording an index row for each in Postgres.
//
// The two halves are deliberately ordered: bytes are flushed to the file
// before the line they belong to is handed to the indexer, because the index
// row is what tells a reader those bytes exist. The reverse order would
// publish offsets pointing past the end of the file.
func captureLogs(ctx context.Context, queries *store.Queries, podmanClient *podman.Client, logDir string, runID int64, containerID string) error {
	writer, err := runlog.Create(logDir, runID)
	if err != nil {
		return err
	}

	// runlog.Create truncated the file, so any index rows from a previous
	// capture now address bytes that are gone. This is the reconciler's
	// adoption path (task 1.15): recapture is from scratch, both halves.
	if err := queries.DeleteRunLogs(ctx, runID); err != nil {
		log.Printf("run %d: clearing the previous log index: %v", runID, err)
	}

	lines := make(chan runlog.Line, lineQueueDepth)
	indexed := make(chan struct{})
	go func() {
		defer close(indexed)
		indexLines(queries, runID, lines)
	}()

	var lineCount int
	followErr := podmanClient.FollowContainerLogs(ctx, containerID, func(frame podman.LogFrame) error {
		written, err := writer.Write(frame.Stream, frame.Data)
		if err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}

		for _, line := range written {
			lines <- line
		}
		lineCount += len(written)

		return nil
	})

	// Close regardless of how the follow ended: a stream cut short still has
	// a trailing partial line worth keeping, and the file must be closed
	// either way.
	tail, closeErr := writer.Close()
	for _, line := range tail {
		lines <- line
	}
	lineCount += len(tail)

	close(lines)
	<-indexed

	if followErr != nil {
		return followErr
	}
	if closeErr != nil {
		return closeErr
	}

	log.Printf("run %d: captured %d lines of output", runID, lineCount)

	return nil
}

// indexLines writes index rows for captured lines until lines is closed,
// coalescing whatever is already queued into one COPY.
//
// That coalescing is the whole point: a script printing thousands of lines
// would otherwise cost thousands of round trips on the critical path of the
// run, while a quiet stream still commits each line as it arrives, which is
// what makes the output visible live.
//
// A failed insert is logged and skipped rather than retried. The lines are
// already safely in the file; losing an index row costs visibility of those
// lines, not the run.
func indexLines(queries *store.Queries, runID int64, lines <-chan runlog.Line) {
	batch := make([]store.InsertRunLogsParams, 0, maxIndexBatch)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// A fresh context deliberately: captured output should still be
		// recorded when the supervisor is shutting down, exactly as a
		// terminal state is (see finishRun).
		ctx := context.Background()

		if _, err := queries.InsertRunLogs(ctx, batch); err != nil {
			log.Printf("run %d: recording %d log lines: %v", runID, len(batch), err)
			batch = batch[:0]
			return
		}

		// Announce only what is actually readable now - after the COPY, and
		// after the file flush that preceded it. The watermark is the last
		// sequence in the batch.
		notifyRunEvent(ctx, queries, logstream.Event{
			RunID: runID,
			Kind:  logstream.KindLogs,
			Seq:   batch[len(batch)-1].Seq,
		})

		batch = batch[:0]
	}

	for line := range lines {
		batch = append(batch, toIndexRow(runID, line))

		for draining := true; draining && len(batch) < maxIndexBatch; {
			select {
			case next, ok := <-lines:
				if !ok {
					flush()
					return
				}
				batch = append(batch, toIndexRow(runID, next))
			default:
				draining = false
			}
		}

		flush()
	}

	flush()
}

// notifyRunEvent tells anything streaming this run in the API that there is
// something new to read (task 2.3).
//
// Best-effort by design. A notification that never arrives costs a subscriber
// latency until its next safety-net poll, and nothing else - the index and
// the file are already durable by the time this is called. Failing a run, or
// even retrying, over a missed wake-up would be out of all proportion.
func notifyRunEvent(ctx context.Context, queries *store.Queries, event logstream.Event) {
	payload, err := event.Payload()
	if err != nil {
		log.Printf("run %d: encoding %s event: %v", event.RunID, event.Kind, err)
		return
	}

	if err := queries.NotifyRunEvent(ctx, store.NotifyRunEventParams{
		Channel: logstream.Channel,
		Payload: payload,
	}); err != nil {
		log.Printf("run %d: publishing %s event: %v", event.RunID, event.Kind, err)
	}
}

func toIndexRow(runID int64, line runlog.Line) store.InsertRunLogsParams {
	return store.InsertRunLogsParams{
		RunID:      runID,
		Seq:        line.Seq,
		Stream:     line.Stream,
		Ts:         pgtype.Timestamptz{Time: line.Ts, Valid: true},
		ByteOffset: line.ByteOffset,
		ByteLength: line.ByteLength,
	}
}

// wait blocks until the capture has drained, indexed and closed its file.
// Nil-safe, so callers with no capture in hand (the reconciler's
// never-started-container path) can call it unconditionally, and idempotent,
// so the paths that both wait and defer a wait are fine.
//
// A capture that has not finished within captureGrace is *stopped*, not
// abandoned. The difference is the whole point of this function: an abandoned
// capture keeps its connection to podman open and keeps writing to a run the
// supervisor has moved on from, and it never ends, because the thing it is
// blocked on is by definition the thing that was not going to happen. One was
// measured still blocked in a read two minutes after its run finished
// (decision #20 has the cause). Stopping it costs whatever output had not
// arrived - which is already lost either way - and gets the file closed, the
// last lines indexed and the goroutine ended.
func (c *logCapture) wait() {
	if c == nil {
		return
	}

	select {
	case <-c.done:
		return
	case <-time.After(captureGrace):
		log.Printf("run %d: log capture still running after %s; stopping it - the rest of the output will not be recorded", c.runID, captureGrace)
		c.cancel()
	}

	select {
	case <-c.done:
	case <-time.After(captureStopGrace):
		log.Printf("run %d: log capture did not stop within %s of being cancelled; abandoning it", c.runID, captureStopGrace)
	}
}
