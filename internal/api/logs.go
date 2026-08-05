package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/r3dpan/project-descendence/internal/runlog"
	"github.com/r3dpan/project-descendence/internal/store"
)

const (
	defaultLogPageLimit = 500
	maxLogPageLimit     = 1000
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

// --- Handlers ---

// GetRunLogsHandler serves a run's captured output, oldest first (task 2.4).
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
