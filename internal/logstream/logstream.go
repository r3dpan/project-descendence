// Package logstream carries "there is more to read" notifications from the
// supervisor to whoever is streaming a run in the API.
//
// It exists because of a deliberate constraint: api and supervisor never talk
// to each other (ARCHITECTURE.md §3). The supervisor captures a container's
// output, the API serves it, and the only thing between them is Postgres - so
// Postgres carries the wake-up too, via LISTEN/NOTIFY. That keeps the API
// stateless and restartable and needs no port, socket or service discovery
// between the two processes.
//
// **Events are watermarks, not data.** An event says "run 42 has output
// through sequence 900", never what that output was. Subscribers read the
// actual lines from the index and the file. Three things follow from that,
// and the rest of this package leans on all three:
//
//   - A later event supersedes an earlier one, so dropping events under load
//     is safe as long as one eventually arrives.
//   - A missed event costs latency, not correctness - which matters, because
//     notifications sent while the listener is reconnecting are simply gone.
//     Subscribers must poll on a slow timer as a safety net rather than
//     treating the stream as complete.
//   - Payloads stay tiny, well inside NOTIFY's 8000-byte limit, no matter how
//     chatty the run.
package logstream

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// Channel is the Postgres notification channel every run event travels on.
// One channel for all runs, filtered by subscribers: Postgres notifications
// are cheap, and a channel per run would mean issuing LISTEN and UNLISTEN as
// clients come and go.
const Channel = "run_events"

// The kinds of event a run emits.
const (
	// KindLogs: more output has been indexed, through Seq.
	KindLogs = "logs"
	// KindState: the run changed state. Sent when a run reaches a terminal
	// state, so a stream can end promptly instead of waiting for a poll to
	// notice.
	KindState = "state"
)

// subscriberBuffer is how many events may be queued for one subscriber before
// further events are dropped for it. Small on purpose: since events are
// watermarks, a subscriber that is behind gains nothing from a backlog of
// stale ones - it only ever needs the most recent.
const subscriberBuffer = 16

// Event is one notification about a run.
type Event struct {
	RunID int64  `json:"runId"`
	Kind  string `json:"kind"`
	// Seq is the highest log sequence number available, for KindLogs.
	Seq int64 `json:"seq,omitempty"`
	// State is the run's new state, for KindState.
	State string `json:"state,omitempty"`
}

// Payload encodes the event for pg_notify. JSON rather than a packed string
// so that adding a field later does not need a parser change on both sides at
// once - the two sides are separate processes and get restarted separately.
func (e Event) Payload() (string, error) {
	encoded, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("logstream: encoding event: %w", err)
	}
	return string(encoded), nil
}

// ParseEvent decodes a notification payload.
func ParseEvent(payload string) (Event, error) {
	var event Event
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return Event{}, fmt.Errorf("logstream: decoding event %q: %w", payload, err)
	}
	return event, nil
}

// Broker fans one run's events out to every subscriber watching that run.
//
// This is the fan-out ARCHITECTURE.md §4.2 describes, and it lives here
// rather than in the supervisor: there is one capture per run in the
// supervisor, but the *subscribers* are HTTP clients, and HTTP clients are
// the API's business. Any number of CLIs and browsers can tail the same run;
// none of them causes a second attach to the container.
type Broker struct {
	mu   sync.Mutex
	subs map[int64]map[*Subscription]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[int64]map[*Subscription]struct{})}
}

// Subscribe returns a subscription to runID's events. Always Close it -
// nothing else removes it from the broker, and a subscription left behind is
// a slot the broker keeps pushing into forever.
func (b *Broker) Subscribe(runID int64) *Subscription {
	sub := &Subscription{
		broker: b,
		runID:  runID,
		events: make(chan Event, subscriberBuffer),
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.subs[runID] == nil {
		b.subs[runID] = make(map[*Subscription]struct{})
	}
	b.subs[runID][sub] = struct{}{}

	return sub
}

// Publish delivers event to every subscriber of its run, without blocking.
//
// A subscriber whose buffer is full has the event dropped and its drop
// counter incremented - it is never waited on. That is the rule the plan
// insists on (task 2.3) and the reason is concrete: the notification listener
// is a single goroutine serving every run, so one client that has stopped
// reading - a browser tab on a frozen laptop, a CLI piped into `less` -
// would otherwise stall log delivery for everybody.
//
// Dropping is safe here only because events are watermarks: whatever the
// subscriber missed is described again by the next event, and by its own
// safety-net poll if no next event ever comes.
func (b *Broker) Publish(event Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for sub := range b.subs[event.RunID] {
		select {
		case sub.events <- event:
		default:
			sub.dropped.Add(1)
		}
	}
}

// SubscriberCount reports how many subscriptions runID currently has. For
// tests and diagnostics - a count that never returns to zero is a leak.
func (b *Broker) SubscriberCount(runID int64) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.subs[runID])
}

// Subscription is one client's view of a run's events.
type Subscription struct {
	broker *Broker
	runID  int64
	events chan Event

	dropped   atomic.Int64
	closeOnce sync.Once
}

// Events is the subscription's event channel. It is closed when the
// subscription is, so a range over it ends cleanly.
func (s *Subscription) Events() <-chan Event {
	return s.events
}

// Dropped reports how many events were discarded because this subscriber was
// not keeping up. Non-zero is not an error - it means the client is behind
// and will catch up from the index on its next read - but it is worth
// surfacing, since a number that climbs steadily means a consumer that never
// catches up.
func (s *Subscription) Dropped() int64 {
	return s.dropped.Load()
}

// Close removes the subscription from its broker and closes its channel.
// Idempotent, so a handler can defer it and still close early on an error
// path.
func (s *Subscription) Close() {
	s.closeOnce.Do(func() {
		s.broker.mu.Lock()
		defer s.broker.mu.Unlock()

		delete(s.broker.subs[s.runID], s)
		if len(s.broker.subs[s.runID]) == 0 {
			delete(s.broker.subs, s.runID)
		}

		// Safe to close here and only here: Publish sends under the same
		// mutex, and this subscription is already out of the map, so no send
		// on a closed channel is possible.
		close(s.events)
	})
}
