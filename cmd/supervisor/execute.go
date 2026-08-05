package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/logstream"
	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/store"
)

// executeRun drives a single claimed (state=running) run to a terminal
// state: create the container, start it, wait for it, record the outcome,
// then remove the container. Every exit path writes a terminal state - a run
// never returns from here still "running" (except when the supervisor
// itself is shutting down; see waitFinishAndRemove).
func executeRun(ctx context.Context, queries *store.Queries, podmanClient *podman.Client, logDir string, run store.Run) {
	containerID, err := podmanClient.CreateContainer(ctx, podman.CreateContainerParams{
		RunID:   run.ID,
		Image:   run.ImageRef,
		Command: run.Argv,
	})
	if err != nil {
		finishRun(ctx, queries, run.ID, store.StateFailed, nil, "", fmt.Sprintf("creating container: %v", err))
		return
	}

	// Record the container before starting it, not just at the end. Phase 1e
	// found a running run reporting containerId: null - no way to reach the
	// container of a run that is still going, which is exactly when you want
	// it. Best-effort: failing to note it is not a reason to abandon the run.
	if err := queries.SetRunContainerID(ctx, store.SetRunContainerIDParams{
		ID:          run.ID,
		ContainerID: pgtype.Text{String: containerID, Valid: true},
	}); err != nil {
		log.Printf("run %d: failed recording container %s: %v", run.ID, containerID, err)
	}

	if err := podmanClient.StartContainer(ctx, containerID); err != nil {
		finishRun(ctx, queries, run.ID, store.StateFailed, nil, containerID, fmt.Sprintf("starting container: %v", err))
		removeContainer(nil, podmanClient, run.ID, containerID)
		return
	}

	// Attach to the output only once the container is started - and only
	// once per run (task 2.1). libpod replays from the beginning of the
	// container's life, so nothing printed between start and attach is
	// missed.
	capture := startLogCapture(ctx, queries, podmanClient, logDir, run.ID, containerID)

	waitFinishAndRemove(ctx, queries, podmanClient, run, containerID, capture)
}

// waitFinishAndRemove blocks until containerID exits (returning immediately
// if it already has), records the outcome, and removes the container.
// Shared between normal execution (called from executeRun, right after
// start) and the reconciler's adoption path (task 1.15) - a container the
// reconciler finds still running or already exited from before a crash goes
// through exactly this same tail.
//
// The wait is bounded by run.TimeoutSeconds measured from run.StartedAt, not
// from when this function is called - an adopted run that already used most
// of its budget before a crash only gets what's left, not a fresh clock
// (task 1.17). Timing out kills the container and records a clear failure
// reason. Separately, if ctx itself is cancelled for a reason other than
// that deadline (the supervisor shutting down), the run is deliberately left
// exactly as-is - still "running" - for the reconciler to pick up on the
// next start, rather than being recorded as failed or timed out.
//
// capture is the run's log capture (task 2.1), or nil if there is none. It is
// waited on before the container is removed, since removing a container out
// from under an open log stream loses whatever had not been read yet.
func waitFinishAndRemove(ctx context.Context, queries *store.Queries, podmanClient *podman.Client, run store.Run, containerID string, capture *logCapture) {
	deadline := run.StartedAt.Time.Add(time.Duration(run.TimeoutSeconds) * time.Second)
	waitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	exitCode, err := podmanClient.WaitContainer(waitCtx, containerID)
	if err != nil {
		switch {
		case errors.Is(waitCtx.Err(), context.DeadlineExceeded):
			handleTimeout(queries, podmanClient, run, containerID, capture)
		case waitCtx.Err() != nil:
			// ctx was cancelled before the deadline arrived - shutdown, not
			// a timeout. Leave the run running; don't touch the container.
			// Still wait for the capture: it is ending too (same ctx), and
			// letting the process exit mid-write would truncate the file.
			log.Printf("run %d: stopped waiting (%v); leaving it running for the reconciler", run.ID, waitCtx.Err())
			capture.wait()
		default:
			finishRun(ctx, queries, run.ID, store.StateFailed, nil, containerID, fmt.Sprintf("waiting for container: %v", err))
			removeContainer(capture, podmanClient, run.ID, containerID)
		}
		return
	}

	state := store.StateSucceeded
	failureReason := ""
	if exitCode != 0 {
		state = store.StateFailed
		failureReason = fmt.Sprintf("exit code %d", exitCode)
	}

	code := int32(exitCode)
	finishRun(ctx, queries, run.ID, state, &code, containerID, failureReason)
	removeContainer(capture, podmanClient, run.ID, containerID)
}

// handleTimeout kills a container whose run exceeded its timeout, confirms
// it actually stopped, and records the outcome. Uses a fresh context
// throughout - the context that just expired obviously can't be used for
// any of this.
func handleTimeout(queries *store.Queries, podmanClient *podman.Client, run store.Run, containerID string, capture *logCapture) {
	log.Printf("run %d exceeded its %ds timeout, killing container %s", run.ID, run.TimeoutSeconds, containerID)

	ctx := context.Background()

	if err := podmanClient.KillContainer(ctx, containerID); err != nil {
		log.Printf("run %d: failed killing timed-out container %s: %v", run.ID, containerID, err)
	}
	// Confirm it actually stopped before removing it; the exit code is
	// irrelevant here - it was killed, not a normal exit.
	if _, err := podmanClient.WaitContainer(ctx, containerID); err != nil {
		log.Printf("run %d: failed confirming kill of container %s: %v", run.ID, containerID, err)
	}

	finishRun(ctx, queries, run.ID, store.StateFailed, nil, containerID, fmt.Sprintf("exceeded timeout of %ds", run.TimeoutSeconds))
	removeContainer(capture, podmanClient, run.ID, containerID)
}

// finishRun records a terminal state. A terminal state is final (task
// 1.14), so the query refuses to overwrite one: zero rows affected means
// something else already finished this run, which is worth saying out loud
// rather than passing over silently - it means two things believed they
// owned the same run.
func finishRun(ctx context.Context, queries *store.Queries, runID int64, state string, exitCode *int32, containerID, failureReason string) {
	params := store.FinishRunParams{
		ID:    runID,
		State: state,
	}
	if exitCode != nil {
		params.ExitCode = pgtype.Int4{Int32: *exitCode, Valid: true}
	}
	if containerID != "" {
		params.ContainerID = pgtype.Text{String: containerID, Valid: true}
	}
	if failureReason != "" {
		params.FailureReason = pgtype.Text{String: failureReason, Valid: true}
	}

	rows, err := queries.FinishRun(ctx, params)
	if err != nil {
		log.Printf("run %d: failed recording terminal state %q: %v", runID, state, err)
		return
	}
	if rows == 0 {
		log.Printf("run %d: not recording %q - the run was already in a terminal state", runID, state)
		return
	}

	// Let anything streaming this run end promptly rather than waiting for a
	// poll to notice the run is over (task 2.3). Only on a real transition -
	// announcing a state this call did not actually write would tell
	// subscribers the run ended twice.
	notifyRunEvent(ctx, queries, logstream.Event{
		RunID: runID,
		Kind:  logstream.KindState,
		State: state,
	})
}

// removeContainer waits for the run's log capture to drain, then removes the
// container. The order matters: WaitContainer returns the moment the
// container exits, but the log stream can still have unread frames behind it,
// and a removed container's output is gone. Uses a fresh context - a
// cancelled supervisor shouldn't leave a container behind just because
// shutdown was in progress when the run finished.
func removeContainer(capture *logCapture, podmanClient *podman.Client, runID int64, containerID string) {
	capture.wait()

	if err := podmanClient.RemoveContainer(context.Background(), containerID); err != nil {
		log.Printf("run %d: failed removing container %s: %v", runID, containerID, err)
	}
}
