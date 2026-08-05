package main

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/store"
)

// cancelPollInterval is how often an in-flight run is checked for a
// cancellation request.
//
// One second, matching the claim loop, because the phase's exit check asks for
// cancellation to take effect "within a second or two" and because the cost is
// one primary-key lookup per second per in-flight run - of which there is
// currently at most one.
const cancelPollInterval = 1 * time.Second

// cancelWatch polls for a cancellation request on a run that is already
// executing, and kills its container when one arrives (task 2.8).
//
// Polling, not notification, and that is the deliberate part. The api-to-
// supervisor direction has no channel: LISTEN/NOTIFY carries log watermarks
// the other way (decision #19) and its notifications are lossy by design,
// which is fine for "there is more output to read" - a later one supersedes
// it, and a missed one costs latency - and not fine for "stop this run",
// where a missed message means the cancel silently does nothing at all. A
// cancel that takes a second is a much better failure than a cancel that
// never happens. `runs.cancel_requested_at` is a fact in the database rather
// than a message in flight, so a poll cannot miss it.
//
// Killing the container is what ends the run: it makes the WaitContainer the
// executor is blocked in return, and the executor then sees requested() and
// records `cancelled` rather than reading the kill as a failure.
type cancelWatch struct {
	requestSeen atomic.Bool
	stopOnce    sync.Once
	stopped     chan struct{}
	done        chan struct{}
}

// watchForCancellation starts watching run runID in the background. Always
// call stop, which is idempotent and waits for the watcher to finish.
func watchForCancellation(ctx context.Context, queries *store.Queries, podmanClient *podman.Client, runID int64, containerID string) *cancelWatch {
	watch := &cancelWatch{
		stopped: make(chan struct{}),
		done:    make(chan struct{}),
	}

	go func() {
		defer close(watch.done)

		ticker := time.NewTicker(cancelPollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-watch.stopped:
				return
			case <-ticker.C:
			}

			requested, err := queries.IsRunCancelRequested(ctx, runID)
			if err != nil {
				// A failed poll is not a reason to stop watching: the next
				// tick may well succeed, and giving up would turn a blip into
				// a cancel that never happens. Suppressed during shutdown,
				// where a cancelled context is expected rather than a fault.
				if ctx.Err() == nil {
					log.Printf("run %d: checking for a cancellation request: %v", runID, err)
				}
				continue
			}

			if !requested {
				continue
			}

			log.Printf("run %d: cancellation requested, killing container %s", runID, containerID)
			watch.requestSeen.Store(true)

			// A fresh context: this is cleanup, and it has to work even when
			// the supervisor is the thing being shut down - the same reason
			// removeContainer uses one.
			if err := podmanClient.KillContainer(context.Background(), containerID); err != nil {
				log.Printf("run %d: failed killing cancelled container %s: %v", runID, containerID, err)
			}

			// Done either way. If the kill worked, WaitContainer is already
			// returning and the executor takes over; if it failed, repeating
			// it every second until the run ends on its own would just fill
			// the log. requestSeen is set regardless, so a run that finishes
			// despite a failed kill is still recorded as cancelled.
			return
		}
	}()

	return watch
}

// requested reports whether a cancellation was seen. Read by the executor
// after its wait returns, to tell a killed container apart from one that
// failed on its own.
func (w *cancelWatch) requested() bool {
	if w == nil {
		return false
	}
	return w.requestSeen.Load()
}

// stop ends the watch and waits for it. Safe to call more than once, so a
// caller can defer it and still stop early.
func (w *cancelWatch) stop() {
	if w == nil {
		return
	}

	w.stopOnce.Do(func() { close(w.stopped) })
	<-w.done
}
