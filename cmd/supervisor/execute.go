package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/store"
)

// executeRun drives a single claimed (state=running) run to a terminal
// state: create the container, start it, wait for it, record the outcome,
// then remove the container. Every exit path writes a terminal state - a run
// never returns from here still "running".
func executeRun(ctx context.Context, queries *store.Queries, podmanClient *podman.Client, run store.Run) {
	containerID, err := podmanClient.CreateContainer(ctx, podman.CreateContainerParams{
		RunID:   run.ID,
		Image:   run.ImageRef,
		Command: run.Argv,
	})
	if err != nil {
		finishRun(ctx, queries, run.ID, "failed", nil, "", fmt.Sprintf("creating container: %v", err))
		return
	}

	if err := podmanClient.StartContainer(ctx, containerID); err != nil {
		finishRun(ctx, queries, run.ID, "failed", nil, containerID, fmt.Sprintf("starting container: %v", err))
		removeContainer(podmanClient, run.ID, containerID)
		return
	}

	waitFinishAndRemove(ctx, queries, podmanClient, run.ID, containerID)
}

// waitFinishAndRemove blocks until containerID exits (returning immediately
// if it already has), records the outcome, and removes the container.
// Shared between normal execution (called from executeRun, right after
// start) and the reconciler's adoption path (task 1.15) - a container the
// reconciler finds still running or already exited from before a crash goes
// through exactly this same tail.
func waitFinishAndRemove(ctx context.Context, queries *store.Queries, podmanClient *podman.Client, runID int64, containerID string) {
	exitCode, err := podmanClient.WaitContainer(ctx, containerID)
	if err != nil {
		finishRun(ctx, queries, runID, "failed", nil, containerID, fmt.Sprintf("waiting for container: %v", err))
		removeContainer(podmanClient, runID, containerID)
		return
	}

	state := "succeeded"
	failureReason := ""
	if exitCode != 0 {
		state = "failed"
		failureReason = fmt.Sprintf("exit code %d", exitCode)
	}

	code := int32(exitCode)
	finishRun(ctx, queries, runID, state, &code, containerID, failureReason)
	removeContainer(podmanClient, runID, containerID)
}

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

	if err := queries.FinishRun(ctx, params); err != nil {
		log.Printf("run %d: failed recording terminal state %q: %v", runID, state, err)
	}
}

// removeContainer uses a fresh context - a cancelled supervisor shouldn't
// leave a container behind just because shutdown was in progress when the
// run finished.
func removeContainer(podmanClient *podman.Client, runID int64, containerID string) {
	if err := podmanClient.RemoveContainer(context.Background(), containerID); err != nil {
		log.Printf("run %d: failed removing container %s: %v", runID, containerID, err)
	}
}
