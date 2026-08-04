package main

import (
	"context"
	"log"
	"strconv"

	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/store"
)

// reconcile runs once at startup, before the claim loop (task 1.15,
// ARCHITECTURE.md §4.2). It finds every container this application has ever
// created (labelled run_id) and compares that against runs still in a
// non-terminal state. A container that's still alive, or already finished
// but never recorded (the supervisor crashed after WaitContainer returned
// but before FinishRun ran), is adopted - waited on (or, if already exited,
// resolved immediately) and finished exactly like a normal execution.
// Everything else is marked lost. Without this, a crash mid-run leaves the
// row running forever and, if the container is still alive, leaks it too.
//
// This blocks the claim loop from starting until every adoption finishes -
// acceptable for now since nothing in this codebase runs runs concurrently
// yet anyway, but worth revisiting if a long-running adopted run ever needs
// to not hold up newly queued ones.
func reconcile(ctx context.Context, queries *store.Queries, podmanClient *podman.Client) {
	containers, err := podmanClient.ListContainersByRunIDLabel(ctx)
	if err != nil {
		log.Printf("reconcile: listing containers: %v", err)
		return
	}

	byRunID := make(map[int64]podman.ContainerSummary, len(containers))
	for _, c := range containers {
		runID, err := strconv.ParseInt(c.Labels["run_id"], 10, 64)
		if err != nil {
			log.Printf("reconcile: container %s has unparseable run_id label %q: %v", c.ID, c.Labels["run_id"], err)
			continue
		}
		byRunID[runID] = c
	}

	runs, err := queries.ListNonTerminalRuns(ctx)
	if err != nil {
		log.Printf("reconcile: listing non-terminal runs: %v", err)
		return
	}

	for _, run := range runs {
		if run.State != "running" {
			// A queued run was never claimed, so it never got a container -
			// nothing to reconcile. The claim loop picks it up normally.
			continue
		}

		container, found := byRunID[run.ID]
		switch {
		case !found:
			log.Printf("reconcile: run %d has no matching container, marking lost", run.ID)
			finishRun(ctx, queries, run.ID, "lost", nil, "", "supervisor restarted; no container found for this run")

		case container.State == "created":
			// Crashed between create and start - the container never ran,
			// so there's no outcome to adopt. Clean up the stale container
			// rather than leaving it behind.
			log.Printf("reconcile: run %d's container %s was created but never started, marking lost", run.ID, container.ID)
			finishRun(ctx, queries, run.ID, "lost", nil, container.ID, "supervisor restarted before the container started")
			removeContainer(podmanClient, run.ID, container.ID)

		default:
			log.Printf("reconcile: adopting run %d (container %s, state %s)", run.ID, container.ID, container.State)
			waitFinishAndRemove(ctx, queries, podmanClient, run, container.ID)
		}
	}
}
