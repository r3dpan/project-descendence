package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/r3dpan/project-descendence/internal/store"
)

const (
	defaultRunTimeoutSeconds = 3600
	defaultRunListLimit      = 50
	maxRunListLimit          = 200
)

// --- Request/response objects ---

type runCreateRequest struct {
	ImageRef       string   `json:"imageRef"`
	Argv           []string `json:"argv"`
	TimeoutSeconds *int32   `json:"timeoutSeconds"`
}

type runResponse struct {
	ID             int64    `json:"id"`
	State          string   `json:"state"`
	ImageRef       string   `json:"imageRef"`
	Argv           []string `json:"argv"`
	TimeoutSeconds int32    `json:"timeoutSeconds"`
	ContainerID    *string  `json:"containerId"`
	ExitCode       *int32   `json:"exitCode"`
	FailureReason  *string  `json:"failureReason"`
	// Set as soon as a cancellation is requested, which is before the run is
	// actually cancelled - the supervisor still has to stop the container. A
	// client watching a run needs to distinguish "still running" from "still
	// running, but on its way out" (task 2.8).
	CancelRequestedAt *time.Time `json:"cancelRequestedAt"`
	QueuedAt          time.Time  `json:"queuedAt"`
	StartedAt         *time.Time `json:"startedAt"`
	FinishedAt        *time.Time `json:"finishedAt"`
}

type runListResponse struct {
	Items      []runResponse `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

// encodeCursor and decodeCursor turn a (queuedAt, id) seek position into the
// opaque string clients pass back as ?cursor=. The internal shape (plain
// "<RFC3339Nano queuedAt>|<id>", base64'd) is not part of the API contract -
// clients must treat it as opaque.
func encodeCursor(queuedAt time.Time, id int64) string {
	raw := fmt.Sprintf("%s|%d", queuedAt.Format(time.RFC3339Nano), id)
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(encoded string) (queuedAt time.Time, id int64, err error) {
	raw, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		return time.Time{}, 0, err
	}

	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, fmt.Errorf("malformed cursor")
	}

	queuedAt, err = time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, 0, err
	}

	id, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, err
	}

	return queuedAt, id, nil
}

// toRunResponse converts a sqlc-generated Run (pgtype-heavy) into the
// wire shape described by the Run schema in api/openapi.yaml.
func toRunResponse(run store.Run) runResponse {
	resp := runResponse{
		ID:             run.ID,
		State:          run.State,
		ImageRef:       run.ImageRef,
		Argv:           run.Argv,
		TimeoutSeconds: run.TimeoutSeconds,
		QueuedAt:       run.QueuedAt.Time,
	}

	if run.ContainerID.Valid {
		resp.ContainerID = &run.ContainerID.String
	}
	if run.ExitCode.Valid {
		resp.ExitCode = &run.ExitCode.Int32
	}
	if run.FailureReason.Valid {
		resp.FailureReason = &run.FailureReason.String
	}
	if run.StartedAt.Valid {
		resp.StartedAt = &run.StartedAt.Time
	}
	if run.FinishedAt.Valid {
		resp.FinishedAt = &run.FinishedAt.Time
	}
	if run.CancelRequestedAt.Valid {
		resp.CancelRequestedAt = &run.CancelRequestedAt.Time
	}

	return resp
}

// --- Handlers ---

// Handles run creation. Validates the body, inserts a queued row stamped
// with the caller's principal, and returns 202 + Location - never blocks on
// the container actually running (that's the supervisor's job, Phase 1c).
func (s *APIServer) CreateRunHandler(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "no principal in request context")
		return
	}

	var req runCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}

	if strings.TrimSpace(req.ImageRef) == "" {
		writeProblem(w, http.StatusBadRequest, "imageRef is required")
		return
	}
	if len(req.Argv) == 0 {
		writeProblem(w, http.StatusBadRequest, "argv must have at least one element")
		return
	}

	timeoutSeconds := int32(defaultRunTimeoutSeconds)
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds <= 0 {
			writeProblem(w, http.StatusBadRequest, "timeoutSeconds must be positive")
			return
		}
		timeoutSeconds = *req.TimeoutSeconds
	}

	idempotencyKey := pgtype.Text{}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		idempotencyKey = pgtype.Text{String: key, Valid: true}
	}

	run, err := s.queries.CreateRun(r.Context(), store.CreateRunParams{
		PrincipalID:    principal.ID,
		ImageRef:       req.ImageRef,
		Argv:           req.Argv,
		TimeoutSeconds: timeoutSeconds,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		if err != pgx.ErrNoRows {
			writeProblem(w, http.StatusInternalServerError, "failed creating run")
			return
		}

		// ON CONFLICT DO NOTHING skipped the insert: idempotencyKey is only
		// ever non-NULL here (NULL never conflicts), so this is a genuine
		// replay - fetch and return the original run rather than erroring.
		run, err = s.queries.GetRunByIdempotencyKey(r.Context(), store.GetRunByIdempotencyKeyParams{
			PrincipalID:    principal.ID,
			IdempotencyKey: idempotencyKey,
		})
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "failed fetching original run for replayed Idempotency-Key")
			return
		}
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/runs/%d", run.ID))
	writeJSON(w, http.StatusAccepted, toRunResponse(run))
}

// Handles run lookup by id. Not scoped to the caller's principal - full RBAC
// is explicitly deferred (ARCHITECTURE.md §7), and this is a single-user tool
// for now.
func (s *APIServer) GetRunHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no run with this id")
		return
	}

	run, err := s.queries.GetRun(r.Context(), id)
	if err != nil {
		if err != pgx.ErrNoRows {
			writeProblem(w, http.StatusInternalServerError, "failed fetching run")
			return
		}
		writeProblem(w, http.StatusNotFound, "no run with this id")
		return
	}

	writeJSON(w, http.StatusOK, toRunResponse(run))
}

// Handles cancellation (task 2.8).
//
// Cancelling is two different operations behind one endpoint, because the two
// processes own different halves of a run and never talk to each other (§3):
//
//   - A **queued** run has no container. There is nothing to stop, so the API
//     cancels it outright and the run is terminal when this returns.
//   - A **running** run belongs to the supervisor. The API can only record the
//     request; the supervisor stops the container and writes the terminal
//     state. So this returns 202, not 200 - the run is still running when the
//     response is sent, and a client that needs the outcome polls or streams
//     for it, exactly as it does after creating a run.
//
// Both answers are 202 rather than one 200 and one 202. Which of the two paths
// a request took depends on a race the caller cannot see or control, and an
// API whose status code varies on that would be one clients have to handle
// both ways anyway. The response body carries the run, whose state says which
// happened.
//
// A run that has already finished is a 409: terminal states are final (task
// 1.14), and answering "cancel this" with 202 for a run that succeeded ten
// minutes ago would imply something is going to happen.
func (s *APIServer) CancelRunHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no run with this id")
		return
	}

	// Try the queued path first, unconditionally, rather than reading the run
	// and branching on what it says. Both this and the supervisor's claim
	// guard on state = 'queued' in a single statement, so exactly one of them
	// wins; reading first and then deciding would open the window between the
	// two where the claim lands and the cancel is written anyway.
	cancelled, err := s.queries.CancelQueuedRun(r.Context(), id)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed cancelling run")
		return
	}

	if cancelled == 0 {
		// Not queued: either running, already finished, or not a run at all.
		run, err := s.queries.GetRun(r.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeProblem(w, http.StatusNotFound, "no run with this id")
				return
			}
			writeProblem(w, http.StatusInternalServerError, "failed fetching run")
			return
		}

		if store.IsTerminal(run.State) {
			writeProblem(w, http.StatusConflict, fmt.Sprintf("this run has already finished (%s) and cannot be cancelled", run.State))
			return
		}

		// Running. Record the request and let the supervisor act on it.
		// Requesting twice is deliberately not an error - a client retrying a
		// cancel it is not sure landed should not have to care.
		if _, err := s.queries.RequestRunCancel(r.Context(), id); err != nil {
			writeProblem(w, http.StatusInternalServerError, "failed requesting cancellation")
			return
		}
	}

	// Re-read rather than returning what either statement wrote: the run has
	// just changed, and this is the one place a client learns whether it is
	// already cancelled or merely on its way there.
	run, err := s.queries.GetRun(r.Context(), id)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed fetching run")
		return
	}

	writeJSON(w, http.StatusAccepted, toRunResponse(run))
}

// Handles run listing: keyset (seek) pagination on (queued_at DESC, id DESC),
// never offset - the table grows forever and offset pagination skips rows
// under concurrent inserts (ARCHITECTURE.md §4.9). Not scoped to the caller's
// principal, matching GetRunHandler.
func (s *APIServer) ListRunsHandler(w http.ResponseWriter, r *http.Request) {
	limit := int32(defaultRunListLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 1 || parsed > maxRunListLimit {
			writeProblem(w, http.StatusBadRequest, fmt.Sprintf("limit must be an integer between 1 and %d", maxRunListLimit))
			return
		}
		limit = int32(parsed)
	}

	// Fetch one extra row to know whether a next page exists without a
	// separate count query.
	params := store.ListRunsParams{RowLimit: limit + 1}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		queuedAt, id, err := decodeCursor(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "malformed cursor")
			return
		}
		params.CursorQueuedAt = pgtype.Timestamptz{Time: queuedAt, Valid: true}
		params.CursorID = pgtype.Int8{Int64: id, Valid: true}
	}

	runs, err := s.queries.ListRuns(r.Context(), params)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed listing runs")
		return
	}

	var nextCursor *string
	if int32(len(runs)) > limit {
		runs = runs[:limit]
		last := runs[len(runs)-1]
		c := encodeCursor(last.QueuedAt.Time, last.ID)
		nextCursor = &c
	}

	items := make([]runResponse, len(runs))
	for i, run := range runs {
		items[i] = toRunResponse(run)
	}

	writeJSON(w, http.StatusOK, runListResponse{Items: items, NextCursor: nextCursor})
}
