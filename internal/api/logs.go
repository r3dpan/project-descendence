package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/r3dpan/project-descendence/internal/runlog"
	"github.com/r3dpan/project-descendence/internal/store"
)

const (
	defaultLogPageLimit = 500
	maxLogPageLimit     = 1000
)

const (
	// logStreamPollInterval is how often a live stream re-reads even though
	// nothing woke it.
	//
	// This is the safety net decision #19 insists on, not an optimisation.
	// Notifications are lossy by design - one published while the API's
	// listener is reconnecting is simply gone - so a stream that waits only on
	// events would hang for the rest of the run the first time that happened.
	// It doubles as the heartbeat that notices a client which vanished
	// without closing its connection.
	logStreamPollInterval = 2 * time.Second

	// logStreamPageSize is how many index rows a stream reads at a time. Only
	// visible as the size of the chunks a large backlog is delivered in - a
	// stream reads pages until it has caught up, then follows line by line.
	logStreamPageSize = 500
)

// The event names this endpoint emits.
const (
	// eventLog: one line of output. Carries an id (its sequence number), so
	// it is a position a client can resume from.
	eventLog = "log"
	// eventState: the run's state, sent when the stream starts and on every
	// change after that. Carries no id - it is not a position.
	eventState = "state"
)

// --- Request/response objects ---

type runLogLine struct {
	Seq    int64     `json:"seq"`
	Stream string    `json:"stream"`
	Ts     time.Time `json:"ts"`
	Text   string    `json:"text"`
}

type runLogListResponse struct {
	Items     []runLogLine `json:"items"`
	NextAfter *int64       `json:"nextAfter"`
	RunState  string       `json:"runState"`
}

// The data of an eventState message. An object rather than a bare string so
// the event can gain fields (an exit code, a failure reason) without every
// client's parser changing on the same day the server does.
type runStateEvent struct {
	RunState string `json:"runState"`
}

// --- Handlers ---

// GetRunLogsHandler serves a run's captured output, oldest first.
//
// Two representations behind one URL, chosen by Accept (task 2.5): a JSON page
// by default, a live `text/event-stream` for a client that asks for one. One
// endpoint rather than two because they answer the same question - "what did
// this run print, from position N" - and differ only in whether the answer
// stops at the end of the history or keeps going. A client that wants both
// (fetch the backlog, then follow) uses the same path twice.
//
// Paginated by sequence number rather than an opaque cursor, unlike the runs
// list. The difference is deliberate: a run's queued_at ordering is an
// implementation detail clients should not construct, but a log line's
// sequence number is public - it is the same number an SSE client sends back
// as Last-Event-ID to resume (task 2.6), so hiding it here and exposing it
// there would be incoherent.
func (s *APIServer) GetRunLogsHandler(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no run with this id")
		return
	}

	after, limit, ok := parseLogPageParams(w, r)
	if !ok {
		return
	}

	run, err := s.queries.GetRun(r.Context(), runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusNotFound, "no run with this id")
			return
		}
		writeProblem(w, http.StatusInternalServerError, "failed fetching run")
		return
	}

	// Checked before reading, not after. Pruning deletes the index rows as
	// well as the file, so a pruned run looks exactly like a run that printed
	// nothing - an empty 200 that quietly claims the run had no output. This
	// is the whole reason runs.logs_pruned_at exists (task 2.2).
	if run.LogsPrunedAt.Valid {
		writeLogsGone(w)
		return
	}

	// Every way this request can fail with a status code has now been ruled
	// out, which is why the branch is here and not at the top: an event stream
	// commits the response to 200 the moment its headers go out, and after
	// that a 404 can only be delivered by hanging up.
	if wantsEventStream(r) {
		s.streamRunLogs(w, r, run, after)
		return
	}

	page, err := s.readLogPage(r, runID, after, limit)
	if err != nil {
		s.writeLogPageError(w, r, runID, err)
		return
	}

	page.RunState = run.State
	writeJSON(w, http.StatusOK, page)
}

// parseLogPageParams reads and validates ?after and ?limit, answering the
// request itself and reporting false if either is unusable.
func parseLogPageParams(w http.ResponseWriter, r *http.Request) (after int64, limit int32, ok bool) {
	limit = int32(defaultLogPageLimit)

	if raw := r.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeProblem(w, http.StatusBadRequest, "after must be a non-negative integer")
			return 0, 0, false
		}
		after = parsed
	}

	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 1 || parsed > maxLogPageLimit {
			writeProblem(w, http.StatusBadRequest, fmt.Sprintf("limit must be an integer between 1 and %d", maxLogPageLimit))
			return 0, 0, false
		}
		limit = int32(parsed)
	}

	return after, limit, true
}

// wantsEventStream reports whether the client asked for SSE.
//
// An exact media type match only - `*/*` does not count. curl and every other
// "I'll take anything" client sends `*/*`, and answering those with an
// infinite stream instead of a JSON document would be a poor default. Asking
// for a stream should require asking for a stream.
func wantsEventStream(r *http.Request) bool {
	for _, value := range r.Header.Values("Accept") {
		for _, mediaRange := range strings.Split(value, ",") {
			// Drop any parameters (";q=0.9") before comparing.
			mediaType, _, _ := strings.Cut(mediaRange, ";")
			if strings.EqualFold(strings.TrimSpace(mediaType), "text/event-stream") {
				return true
			}
		}
	}

	return false
}

// streamRunLogs follows a run's output as server-sent events until the run
// ends or the client goes away (task 2.5).
//
// The shape of the loop is: read everything readable, report the run's state,
// stop if that state is terminal, otherwise wait to be woken and start again.
// Three details in it are load-bearing.
//
// **Subscribe before the first read.** An event that fires between the read
// and the subscription would be seen by neither, and the stream would sit idle
// until the next poll. Subscribing first can only cause a redundant wake-up,
// which costs one query.
//
// **Re-read the run's state before draining, never after.** A terminal state
// means the log index is complete (see the supervisor's waitFinishAndRemove),
// so a drain that happens after observing one is guaranteed to see every line.
// Draining first and then noticing the run had finished would race the last
// batch of output and silently truncate it.
//
// **Wake on either the notification or the timer, and never trust the
// notification's contents.** The event says only "something happened to this
// run"; what happened is discovered by reading. That is what makes lost
// notifications a latency problem rather than a correctness one (decision
// #19).
func (s *APIServer) streamRunLogs(w http.ResponseWriter, r *http.Request, run store.Run, after int64) {
	subscription := s.logEvents.Subscribe(run.ID)
	defer subscription.Close()

	stream, err := newSSEWriter(w)
	if err != nil {
		// The response is already committed to 200, so there is nothing to
		// report to the client but the end of the stream.
		log.Printf("run %d: starting log stream: %v", run.ID, err)
		return
	}

	ticker := time.NewTicker(logStreamPollInterval)
	defer ticker.Stop()

	// The state last announced to this client. Empty so that the run's current
	// state is always the stream's first state event, which tells a client
	// that connected to a queued run that it is connected.
	var announced string

	for {
		if err := s.streamPendingLines(r, stream, run.ID, &after); err != nil {
			s.endStream(r, run.ID, err)
			return
		}

		if run.State != announced {
			if err := stream.event(eventState, "", runStateEvent{RunState: run.State}); err != nil {
				s.endStream(r, run.ID, err)
				return
			}
			if err := stream.flush(); err != nil {
				s.endStream(r, run.ID, err)
				return
			}
			announced = run.State
		}

		// A terminal state event is the stream's defined ending. Anything else
		// that ends it - a dropped connection, a write timeout, a failed read
		// - ends it without one, which is a client's cue to reconnect and
		// resume from its last id (task 2.6).
		if store.IsTerminal(run.State) {
			return
		}

		select {
		case <-r.Context().Done():
			// The client hung up. Task 2.7: this is the return that keeps a
			// closed browser tab from costing a goroutine and a subscription
			// for the rest of the run.
			return

		case _, ok := <-subscription.Events():
			if !ok {
				return
			}

		case <-ticker.C:
			if err := stream.comment("keep-alive"); err != nil {
				s.endStream(r, run.ID, err)
				return
			}
		}

		refreshed, err := s.queries.GetRun(r.Context(), run.ID)
		if err != nil {
			s.endStream(r, run.ID, fmt.Errorf("refreshing run state: %w", err))
			return
		}
		run = refreshed
	}
}

// streamPendingLines emits every line after *after and advances it, reading
// as many pages as it takes to catch up.
func (s *APIServer) streamPendingLines(r *http.Request, stream *sseWriter, runID int64, after *int64) error {
	for {
		page, err := s.readLogPage(r, runID, *after, logStreamPageSize)
		if err != nil {
			return err
		}

		for _, line := range page.Items {
			if err := stream.event(eventLog, strconv.FormatInt(line.Seq, 10), line); err != nil {
				return err
			}
			// Advanced per line rather than per page, so a write that fails
			// halfway through a page does not lose the lines already sent
			// from a caller's point of view.
			*after = line.Seq
		}

		if err := stream.flush(); err != nil {
			return err
		}

		// readLogPage only sets NextAfter when it saw more rows than it
		// returned, so a nil one means the client is caught up.
		if page.NextAfter == nil {
			return nil
		}
	}
}

// endStream logs why a stream stopped, if the reason is worth logging.
//
// A client disconnecting is the ordinary way a stream ends - a browser tab
// closing, a CLI receiving Ctrl-C - and every read in flight fails with a
// cancelled context when it happens. Logging those as errors would fill the
// log with normal behaviour, the same mistake Phase 1e found in the claim
// loop.
func (s *APIServer) endStream(r *http.Request, runID int64, err error) {
	if r.Context().Err() != nil {
		return
	}

	log.Printf("run %d: log stream ended: %v", runID, err)
}

// readLogPage fetches one page of index rows and resolves each to its text.
//
// The index comes from Postgres and the bodies from the run's file, which is
// the split ARCHITECTURE.md §4.1 describes. The file is opened once per page
// rather than once per line - a 500-line page would otherwise be 500 opens.
func (s *APIServer) readLogPage(r *http.Request, runID int64, after int64, limit int32) (runLogListResponse, error) {
	// Fetch one extra row to tell "this page is full" from "this is
	// everything", without a second count query.
	entries, err := s.queries.ListRunLogs(r.Context(), store.ListRunLogsParams{
		RunID:    runID,
		AfterSeq: after,
		RowLimit: limit + 1,
	})
	if err != nil {
		return runLogListResponse{}, fmt.Errorf("listing log index: %w", err)
	}

	page := runLogListResponse{Items: []runLogLine{}}
	if len(entries) == 0 {
		return page, nil
	}

	if int32(len(entries)) > limit {
		entries = entries[:limit]
		next := entries[len(entries)-1].Seq
		page.NextAfter = &next
	}

	reader, err := runlog.Open(s.logDir, runID)
	if err != nil {
		return runLogListResponse{}, err
	}
	defer reader.Close()

	page.Items = make([]runLogLine, 0, len(entries))
	for _, entry := range entries {
		text, err := reader.ReadLine(entry.ByteOffset, entry.ByteLength)
		if err != nil {
			return runLogListResponse{}, err
		}

		page.Items = append(page.Items, runLogLine{
			Seq:    entry.Seq,
			Stream: entry.Stream,
			Ts:     entry.Ts.Time,
			Text:   text,
		})
	}

	return page, nil
}

func writeLogsGone(w http.ResponseWriter) {
	writeProblem(w, http.StatusGone, "this run's output has passed the retention window and been deleted")
}

// writeLogPageError turns a read failure into the right status.
//
// The only interesting case is the file having vanished between the pruned
// check and the read - the retention sweep landing in that gap. It is a
// narrow race, and only reachable for a run old enough to be swept, but
// re-reading the run once costs nothing and turns a misleading "failed
// reading run logs" into the truth. A file missing for any other reason is a
// server fault, not a 404: the *run* is right there.
func (s *APIServer) writeLogPageError(w http.ResponseWriter, r *http.Request, runID int64, err error) {
	if errors.Is(err, runlog.ErrNoLogFile) {
		if run, refetchErr := s.queries.GetRun(r.Context(), runID); refetchErr == nil && run.LogsPrunedAt.Valid {
			writeLogsGone(w)
			return
		}
	}

	log.Printf("run %d: reading logs: %v", runID, err)
	writeProblem(w, http.StatusInternalServerError, "failed reading run logs")
}
