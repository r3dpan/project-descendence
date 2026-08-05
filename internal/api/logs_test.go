package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/r3dpan/project-descendence/internal/logstream"
	"github.com/r3dpan/project-descendence/internal/store"
)

func TestWantsEventStream(t *testing.T) {
	cases := []struct {
		accept []string
		want   bool
	}{
		{nil, false},
		{[]string{"text/event-stream"}, true},
		{[]string{"text/event-stream; charset=utf-8"}, true},
		{[]string{"TEXT/EVENT-STREAM"}, true},
		{[]string{"application/json, text/event-stream;q=0.9"}, true},
		{[]string{"application/json", "text/event-stream"}, true},
		{[]string{"application/json"}, false},
		{[]string{"text/plain"}, false},
		// curl's default. Answering "I'll take anything" with an infinite
		// stream instead of a document would be a poor default, so it must
		// not match.
		{[]string{"*/*"}, false},
		{[]string{"text/*"}, false},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/runs/1/logs", nil)
		for _, value := range tc.accept {
			r.Header.Add("Accept", value)
		}

		if got := wantsEventStream(r); got != tc.want {
			t.Errorf("wantsEventStream(Accept: %v) = %v, want %v", tc.accept, got, tc.want)
		}
	}
}

func TestResumePositionPrefersLastEventIDOverAfter(t *testing.T) {
	cases := []struct {
		name       string
		stream     bool
		header     string
		after      int64
		want       int64
		wantStatus int
	}{
		{name: "no header keeps after", stream: true, after: 7, want: 7},
		{name: "header wins", stream: true, header: "42", after: 7, want: 42},
		{name: "zero header is still a header", stream: true, header: "0", after: 7, want: 0},
		// On the JSON path the header is meaningless; honouring it would
		// quietly change which page comes back.
		{name: "ignored without a stream", stream: false, header: "42", after: 7, want: 7},
		{name: "not a number", stream: true, header: "abc", wantStatus: http.StatusBadRequest},
		{name: "negative", stream: true, header: "-1", wantStatus: http.StatusBadRequest},
		{name: "not an integer", stream: true, header: "1.5", wantStatus: http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/runs/1/logs", nil)
			if tc.stream {
				r.Header.Set("Accept", "text/event-stream")
			}
			if tc.header != "" {
				r.Header.Set("Last-Event-ID", tc.header)
			}
			w := httptest.NewRecorder()

			got, ok := resumePosition(w, r, tc.after)

			if tc.wantStatus != 0 {
				if ok {
					t.Fatalf("resumePosition accepted %q, want a %d", tc.header, tc.wantStatus)
				}
				if w.Code != tc.wantStatus {
					t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
				}
				return
			}

			if !ok {
				t.Fatalf("resumePosition rejected a valid request (status %d)", w.Code)
			}
			if got != tc.want {
				t.Errorf("resume position = %d, want %d", got, tc.want)
			}
		})
	}
}

// --- Integration: a real database ---

func newTestServer(t *testing.T) (*APIServer, *logstream.Broker, context.Context) {
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

	queries := store.New(pool)
	if _, err := queries.Ping(ctx); err != nil {
		t.Skipf("database not reachable: %v", err)
	}

	broker := logstream.NewBroker()

	return NewAPIServer("test", "test", "test", queries, nil, t.TempDir(), broker, nil), broker, ctx
}

// A disconnecting client must cost nothing (task 2.7).
//
// This is the leak the task exists to prevent: a stream holds a goroutine, a
// subscription in the broker, and a slot the notification listener pushes into
// on every event for that run. A browser tab closed during a one-hour run must
// release all three immediately, not when the run happens to finish - and
// nothing in a normal test run would ever notice if it didn't, because the
// process exits before the leak matters.
//
// A run id no supervisor will ever produce keeps this independent of whatever
// else is using the same database, which the 1.14 lesson says to care about.
func TestAClosedStreamReleasesItsSubscription(t *testing.T) {
	server, broker, ctx := newTestServer(t)

	const runID = -424243

	// Driven below GetRunLogsHandler, which would 404 on an id no run has.
	// A queued run that never starts is the state a leak would hide in: the
	// stream has nothing to send and nothing to end it.
	requestCtx, disconnect := context.WithCancel(ctx)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/runs/-424243/logs", nil).WithContext(requestCtx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.streamRunLogs(w, r, store.Run{ID: runID, State: store.StateQueued}, 0)
	}()

	// Wait for the stream to actually be subscribed before disconnecting -
	// otherwise the test could pass by racing past the subscription entirely.
	if !eventually(func() bool { return broker.SubscriberCount(runID) == 1 }) {
		t.Fatal("the stream never subscribed")
	}

	disconnect()

	// Promptness is the actual claim, not just eventual release. Every read a
	// stream makes carries the request context, so a handler that never
	// selects on Done still unwinds - on its next safety-net poll, up to
	// logStreamPollInterval later. Verified by deleting the Done case: the
	// subscription assertions below still passed, and only this deadline
	// caught it. On a one-hour run with a poll interval measured in seconds
	// that difference is small; the reason to care is that it is the
	// difference between "the client leaving ends the stream" and "something
	// else eventually notices", and only the first stays true if the poll ever
	// gets slower.
	returned := time.Now()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler did not return after the client disconnected")
	}

	if took := time.Since(returned); took >= logStreamPollInterval {
		t.Errorf("the handler took %s to return, which is a poll cycle - it is not watching the request context", took)
	}

	if got := broker.SubscriberCount(runID); got != 0 {
		t.Errorf("broker still has %d subscriber(s) for run %d after the client left", got, runID)
	}

	if !strings.Contains(w.Body.String(), "event: state") {
		t.Errorf("stream sent no state event before ending; body was %q", w.Body.String())
	}
}

// The same thing at scale, and against goroutines rather than bookkeeping: a
// subscription count of zero would not catch a handler that returned while
// leaving something behind it running.
func TestManyClosedStreamsLeaveNoGoroutinesBehind(t *testing.T) {
	server, broker, ctx := newTestServer(t)

	const (
		runID   = -424244
		clients = 25
	)

	before := runtime.NumGoroutine()

	disconnects := make([]context.CancelFunc, 0, clients)
	finished := make([]chan struct{}, 0, clients)

	for i := 0; i < clients; i++ {
		requestCtx, disconnect := context.WithCancel(ctx)
		disconnects = append(disconnects, disconnect)

		r := httptest.NewRequest(http.MethodGet, "/api/v1/runs/-424244/logs", nil).WithContext(requestCtx)
		done := make(chan struct{})
		finished = append(finished, done)

		go func() {
			defer close(done)
			server.streamRunLogs(httptest.NewRecorder(), r, store.Run{ID: runID, State: store.StateRunning}, 0)
		}()
	}

	if !eventually(func() bool { return broker.SubscriberCount(runID) == clients }) {
		t.Fatalf("only %d of %d streams subscribed", broker.SubscriberCount(runID), clients)
	}

	for _, disconnect := range disconnects {
		disconnect()
	}
	for i, done := range finished {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("stream %d did not return after its client disconnected", i)
		}
	}

	if got := broker.SubscriberCount(runID); got != 0 {
		t.Errorf("broker still has %d subscriber(s) after all %d clients left", got, clients)
	}

	// Allow for goroutines the runtime and pgx keep around; the leak this
	// guards against is proportional to the number of clients, so a threshold
	// well below `clients` catches it without being flaky.
	if !eventually(func() bool { return runtime.NumGoroutine()-before < clients/2 }) {
		t.Errorf("goroutines went from %d to %d after %d streams opened and closed",
			before, runtime.NumGoroutine(), clients)
	}
}

// eventually polls cond for up to 10 seconds. Used instead of a sleep so the
// tests are neither flaky on a loaded machine nor slow on an idle one.
func eventually(cond func() bool) bool {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}
