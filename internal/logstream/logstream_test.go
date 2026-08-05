package logstream

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/r3dpan/project-descendence/internal/store"
)

// receive waits for one event, failing rather than hanging if none arrives.
func receive(t *testing.T, sub *Subscription) Event {
	t.Helper()

	select {
	case event, ok := <-sub.Events():
		if !ok {
			t.Fatal("event channel closed while waiting for an event")
		}
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("no event arrived within 5s")
		return Event{}
	}
}

func TestEventRoundTripsThroughItsPayload(t *testing.T) {
	for _, want := range []Event{
		{RunID: 42, Kind: KindLogs, Seq: 900},
		{RunID: 7, Kind: KindState, State: "succeeded"},
	} {
		payload, err := want.Payload()
		if err != nil {
			t.Fatalf("Payload: %v", err)
		}

		// The 8000-byte NOTIFY limit is the reason events are watermarks
		// rather than log text; a payload anywhere near it means that
		// property has been lost.
		if len(payload) > 200 {
			t.Errorf("payload is %d bytes (%s) - events are meant to stay tiny", len(payload), payload)
		}

		got, err := ParseEvent(payload)
		if err != nil {
			t.Fatalf("ParseEvent(%s): %v", payload, err)
		}
		if got != want {
			t.Errorf("round trip gave %+v, want %+v", got, want)
		}
	}
}

func TestParseEventRejectsGarbage(t *testing.T) {
	if _, err := ParseEvent("not json"); err == nil {
		t.Error("ParseEvent accepted a non-JSON payload, want an error")
	}
}

// The core of the fan-out: one run, several subscribers, everybody gets it.
func TestPublishReachesEverySubscriberOfTheRun(t *testing.T) {
	broker := NewBroker()

	first := broker.Subscribe(42)
	second := broker.Subscribe(42)
	defer first.Close()
	defer second.Close()

	want := Event{RunID: 42, Kind: KindLogs, Seq: 5}
	broker.Publish(want)

	for i, sub := range []*Subscription{first, second} {
		if got := receive(t, sub); got != want {
			t.Errorf("subscriber %d got %+v, want %+v", i, got, want)
		}
	}
}

// Subscribers are per run: tailing one run must not deliver another run's
// output, which at this layer would mean leaking one client's logs to
// another.
func TestPublishIsScopedToItsRun(t *testing.T) {
	broker := NewBroker()

	watcher := broker.Subscribe(42)
	defer watcher.Close()

	broker.Publish(Event{RunID: 99, Kind: KindLogs, Seq: 1})
	broker.Publish(Event{RunID: 42, Kind: KindLogs, Seq: 2})

	got := receive(t, watcher)
	if got.RunID != 42 {
		t.Errorf("subscriber to run 42 received an event for run %d", got.RunID)
	}
}

// The requirement task 2.3 is explicit about: a subscriber that has stopped
// reading must never stall the publisher. The listener is one goroutine
// serving every run, so a single frozen client blocking it would stop log
// delivery for everybody.
func TestPublishNeverBlocksOnASlowSubscriber(t *testing.T) {
	broker := NewBroker()

	slow := broker.Subscribe(42)
	defer slow.Close()

	// Deliberately never read from `slow`. Overfilling its buffer several
	// times over is what a client frozen mid-stream looks like.
	const published = subscriberBuffer * 4

	done := make(chan struct{})
	go func() {
		defer close(done)
		for seq := 1; seq <= published; seq++ {
			broker.Publish(Event{RunID: 42, Kind: KindLogs, Seq: int64(seq)})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}

	if dropped := slow.Dropped(); dropped != published-subscriberBuffer {
		t.Errorf("dropped %d events, want %d - the buffer should fill and then discard", dropped, published-subscriberBuffer)
	}
}

// A stalled subscriber must not cost a keeping-up one its events: the drop is
// per subscriber, not per publish.
//
// Note what "keeping up" has to mean here. Dropping is not reserved for
// clients that have frozen - *any* subscriber momentarily behind the
// publisher loses events, because the buffer is deliberately small. That is
// by design (events are watermarks; the next one supersedes the last), and an
// earlier version of this test failed by asserting otherwise: a reader
// goroutine racing a tight publish loop simply does not get scheduled often
// enough. So the reader here consumes each event before the next is
// published, which is what a subscriber genuinely keeping pace looks like.
func TestAStalledSubscriberDoesNotStarveAKeepingUpOne(t *testing.T) {
	broker := NewBroker()

	stalled := broker.Subscribe(42)
	keepingUp := broker.Subscribe(42)
	defer stalled.Close()
	defer keepingUp.Close()

	// Deliberately never read from `stalled`.
	const published = subscriberBuffer * 4

	for seq := 1; seq <= published; seq++ {
		want := Event{RunID: 42, Kind: KindLogs, Seq: int64(seq)}
		broker.Publish(want)

		if got := receive(t, keepingUp); got != want {
			t.Fatalf("the keeping-up subscriber got %+v, want %+v - a stalled peer cost it an event", got, want)
		}
	}

	if got, want := stalled.Dropped(), int64(published-subscriberBuffer); got != want {
		t.Errorf("the stalled subscriber dropped %d events, want %d", got, want)
	}
	if keepingUp.Dropped() != 0 {
		t.Errorf("the keeping-up subscriber dropped %d events, want 0", keepingUp.Dropped())
	}
}

// Closing must both stop delivery and release the slot - a subscription left
// in the broker is a leak that grows with every client that ever connected.
func TestCloseRemovesTheSubscription(t *testing.T) {
	broker := NewBroker()

	sub := broker.Subscribe(42)
	if got := broker.SubscriberCount(42); got != 1 {
		t.Fatalf("subscriber count = %d, want 1", got)
	}

	sub.Close()

	if got := broker.SubscriberCount(42); got != 0 {
		t.Errorf("subscriber count = %d after Close, want 0", got)
	}

	// The channel closes, so a handler ranging over it exits cleanly rather
	// than hanging (which is what task 2.7 is about).
	if _, ok := <-sub.Events(); ok {
		t.Error("event channel still open after Close")
	}

	// And publishing afterwards must not panic on the closed channel.
	broker.Publish(Event{RunID: 42, Kind: KindLogs, Seq: 1})
}

func TestCloseIsIdempotent(t *testing.T) {
	broker := NewBroker()

	sub := broker.Subscribe(42)
	sub.Close()
	sub.Close()
}

// Publish and Close race constantly in the real thing: the listener publishes
// from one goroutine while HTTP handlers come and go on many others. Run with
// -race, this is the test that would catch a send on a closed channel.
func TestPublishAndCloseAreSafeConcurrently(t *testing.T) {
	broker := NewBroker()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for seq := 1; seq <= 2000; seq++ {
			broker.Publish(Event{RunID: 42, Kind: KindLogs, Seq: int64(seq)})
		}
	}()

	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := broker.Subscribe(42)
			<-time.After(time.Millisecond)
			sub.Close()
		}()
	}

	wg.Wait()

	if got := broker.SubscriberCount(42); got != 0 {
		t.Errorf("%d subscriptions survived their Close", got)
	}
}

// --- Integration: a real NOTIFY crossing a real connection ---

// The end-to-end claim this package makes: the supervisor's process emits a
// notification, the API's process receives it, and they share nothing but
// Postgres. Two separate connections here stand in for the two processes.
func TestListenDeliversANotifyFromAnotherConnection(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Skipf("cannot create a pool: %v", err)
	}
	defer pool.Close()

	queries := store.New(pool)
	if _, err := queries.Ping(ctx); err != nil {
		t.Skipf("database not reachable: %v", err)
	}

	listenCtx, stopListening := context.WithCancel(ctx)
	defer stopListening()

	broker := NewBroker()
	go Listen(listenCtx, databaseURL, broker)

	// A run id no other test or supervisor will emit events for, so a
	// database shared with a running supervisor cannot make this flaky.
	const runID = -424242

	sub := broker.Subscribe(runID)
	defer sub.Close()

	want := Event{RunID: runID, Kind: KindLogs, Seq: 12345}
	payload, err := want.Payload()
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}

	// Publish on a retry loop: LISTEN is issued asynchronously by the
	// listener goroutine, so the first notification can genuinely precede it.
	// Notifications sent before a listener attaches are dropped by Postgres -
	// which is exactly the lossiness this package is built around, so the
	// test accommodates it rather than pretending otherwise.
	deadline := time.After(15 * time.Second)
	for {
		if err := queries.NotifyRunEvent(ctx, store.NotifyRunEventParams{
			Channel: Channel,
			Payload: payload,
		}); err != nil {
			t.Fatalf("NotifyRunEvent: %v", err)
		}

		select {
		case got := <-sub.Events():
			if got != want {
				t.Errorf("received %+v, want %+v", got, want)
			}
			return
		case <-time.After(250 * time.Millisecond):
		case <-deadline:
			t.Fatal("no notification arrived within 15s")
		}
	}
}
