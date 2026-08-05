package main

import (
	"context"
	"log"
	"time"

	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/runlog"
)

// captureGrace bounds how long the supervisor waits for a run's log capture
// to drain after the container has exited. The stream ends by itself when the
// container does, so this only matters when something has gone wrong - and
// the claim loop is serial, so waiting forever on one stuck capture would
// stall every queued run behind it.
const captureGrace = 5 * time.Second

// logCapture is one run's in-flight capture goroutine (task 2.1). Exactly one
// exists per run: the supervisor attaches to a container's output once, no
// matter how many clients are eventually watching it (ARCHITECTURE.md §4.2).
type logCapture struct {
	runID int64
	done  chan struct{}
}

// startLogCapture attaches to containerID's output and writes it to run
// runID's log file, in the background. Call it once the container is started;
// call wait before removing the container.
//
// Capture failing never fails the run. Losing the output of a script that ran
// correctly is worth a loud log line, but it is not an execution failure and
// rewriting a successful run as failed because of it would be a lie.
func startLogCapture(ctx context.Context, podmanClient *podman.Client, logDir string, runID int64, containerID string) *logCapture {
	capture := &logCapture{runID: runID, done: make(chan struct{})}

	go func() {
		defer close(capture.done)

		if err := captureLogs(ctx, podmanClient, logDir, runID, containerID); err != nil {
			// A cancelled context here is the supervisor shutting down, not a
			// capture fault - the same distinction the claim loop learned to
			// make in Phase 1e.
			if ctx.Err() != nil {
				log.Printf("run %d: log capture stopped early (%v); the rest of the output is not recorded", runID, ctx.Err())
				return
			}
			log.Printf("run %d: log capture: %v", runID, err)
		}
	}()

	return capture
}

// captureLogs follows the container's output to EOF - which libpod produces
// when the container exits - splitting it into numbered lines in the run's
// log file. The file is flushed frame by frame so that a reader tailing it
// sees output as it happens rather than in 4KB steps.
func captureLogs(ctx context.Context, podmanClient *podman.Client, logDir string, runID int64, containerID string) error {
	writer, err := runlog.Create(logDir, runID)
	if err != nil {
		return err
	}

	var lineCount int
	followErr := podmanClient.FollowContainerLogs(ctx, containerID, func(frame podman.LogFrame) error {
		lines, err := writer.Write(frame.Stream, frame.Data)
		lineCount += len(lines)
		if err != nil {
			return err
		}
		return writer.Flush()
	})

	// Close regardless of how the follow ended: a stream cut short still has
	// a trailing partial line worth keeping, and the file must be closed
	// either way.
	final, closeErr := writer.Close()
	lineCount += len(final)

	if followErr != nil {
		return followErr
	}
	if closeErr != nil {
		return closeErr
	}

	log.Printf("run %d: captured %d lines of output", runID, lineCount)

	return nil
}

// wait blocks until the capture has drained and closed its file, or until
// captureGrace elapses. Nil-safe, so callers with no capture in hand (the
// reconciler's never-started-container path) can call it unconditionally, and
// idempotent, so the paths that both wait and defer a wait are fine.
func (c *logCapture) wait() {
	if c == nil {
		return
	}

	select {
	case <-c.done:
	case <-time.After(captureGrace):
		log.Printf("run %d: log capture still running after %s; continuing without it", c.runID, captureGrace)
	}
}
