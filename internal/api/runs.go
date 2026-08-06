package api

import (
	"context"
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
	"github.com/r3dpan/project-descendence/internal/manifest"
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

	// Both NULL for an ad-hoc run, both set for a job run. The columns have
	// existed since migration 00001 but nothing wrote them until task 3.5;
	// exposing them is what makes a run explainable, since the pair
	// (job, commitSha) is enough to check out exactly what executed.
	JobID     *int64  `json:"jobId"`
	CommitSHA *string `json:"commitSha"`

	// Both NULL unless the job named a runtime rather than an image directly
	// (task 4.6). Set once, at creation, from the runtime's image digest at
	// that moment - rebuilding the runtime afterwards changes runtimeId's
	// row but never this run's imageDigest, which is what makes "what did
	// this run actually execute" answerable independent of what the runtime
	// looks like now.
	RuntimeID   *int64  `json:"runtimeId"`
	ImageDigest *string `json:"imageDigest"`

	// Set only for a run the schedule trigger endpoint created (task 5.6) -
	// NULL for both ad-hoc and ordinary job runs.
	ScheduleID *int64 `json:"scheduleId"`

	// The job's contract resolved against what was submitted (task 6.2):
	// defaults applied, types coerced, in contract order (task 6.4's Bash
	// shim turns this same order into positional arguments). Empty, never
	// null, for an ad-hoc run.
	Params []runParamResponse `json:"params"`
}

// runParamResponse is one resolved param - mirrors manifest.ResolvedParam,
// not reused directly, matching this package's convention of never exposing
// a manifest/store type as the wire shape.
type runParamResponse struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
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
		Params:         []runParamResponse{},
	}
	if len(run.ParamsJson) > 0 {
		// Written only by createJobRun (task 6.2), which only ever writes
		// what manifest.ResolveParams already validated - a decode error
		// here would mean that invariant broke, not something a caller can
		// act on, so it's surfaced as empty rather than failing the whole
		// response.
		_ = json.Unmarshal(run.ParamsJson, &resp.Params)
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
	if run.JobID.Valid {
		resp.JobID = &run.JobID.Int64
	}
	if run.CommitSha.Valid {
		resp.CommitSHA = &run.CommitSha.String
	}
	if run.RuntimeID.Valid {
		resp.RuntimeID = &run.RuntimeID.Int64
	}
	if run.ImageDigest.Valid {
		resp.ImageDigest = &run.ImageDigest.String
	}
	if run.ScheduleID.Valid {
		resp.ScheduleID = &run.ScheduleID.Int64
	}

	return resp
}

// redactedParamValue replaces a secret param's value in an API response
// (task 6.5). A fixed sentinel rather than omitting the entry: the caller
// still learns the param was supplied and what it's called, just not what
// it held.
const redactedParamValue = "***"

// redactRunResponse mutates resp.Params in place, replacing the value of
// any param whose contract entry is `secret` or of type `mount` (every
// mount-type value is secret regardless of the flag - task 6.6's mechanism)
// with redactedParamValue.
//
// The contract lives on the job, not the run, so this is a lookup by
// run.JobID rather than something toRunResponse could do on its own from
// run alone. Skipped entirely for an ad-hoc run (no job, no contract, no
// params to redact).
func (s *APIServer) redactRunResponse(ctx context.Context, run store.Run, resp *runResponse) {
	if !run.JobID.Valid || len(resp.Params) == 0 {
		return
	}

	job, err := s.queries.GetJob(ctx, run.JobID.Int64)
	if err != nil {
		// The job is gone or unreachable; nothing to redact against, but
		// also nothing to leak that a stale lookup couldn't explain -
		// erring toward showing the (already-resolved, already-stored)
		// values rather than failing the whole response.
		return
	}

	var contract []manifest.Param
	if len(job.ParamsJson) > 0 {
		_ = json.Unmarshal(job.ParamsJson, &contract)
	}

	secret := make(map[string]bool, len(contract))
	for _, p := range contract {
		if p.Secret || p.Type == manifest.ParamTypeMount {
			secret[p.Name] = true
		}
	}
	if len(secret) == 0 {
		return
	}

	for i := range resp.Params {
		if secret[resp.Params[i].Name] {
			resp.Params[i].Value = redactedParamValue
		}
	}
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
	resp := toRunResponse(run)
	s.redactRunResponse(r.Context(), run, &resp)
	writeJSON(w, http.StatusAccepted, resp)
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

	resp := toRunResponse(run)
	s.redactRunResponse(r.Context(), run, &resp)
	writeJSON(w, http.StatusOK, resp)
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

	resp := toRunResponse(run)
	s.redactRunResponse(r.Context(), run, &resp)
	writeJSON(w, http.StatusAccepted, resp)
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
		s.redactRunResponse(r.Context(), run, &items[i])
	}

	writeJSON(w, http.StatusOK, runListResponse{Items: items, NextCursor: nextCursor})
}

// defaultRunStatsWindow is used when the caller omits ?since. 24h matches a
// homelab-scale scheduler's typical single day of activity (off-plan web UI
// dashboard work - see docs/HISTORY.md).
const defaultRunStatsWindow = 24 * time.Hour

type runStatsResponse struct {
	Queued    int64     `json:"queued"`
	Succeeded int64     `json:"succeeded"`
	Failed    int64     `json:"failed"`
	Cancelled int64     `json:"cancelled"`
	Lost      int64     `json:"lost"`
	Since     time.Time `json:"since"`
}

// RunStatsHandler answers a handful of aggregate counts for the web UI's
// dashboard - never a list, so no pagination. ?since is a Go duration string
// (e.g. "24h", "7d" is not valid Go duration syntax - use "168h") measured
// back from now; queued is always a live count regardless of since, since
// "currently queued" has no time window (see GetRunStats's comment).
func (s *APIServer) RunStatsHandler(w http.ResponseWriter, r *http.Request) {
	window := defaultRunStatsWindow
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			writeProblem(w, http.StatusBadRequest, "since must be a positive Go duration string (e.g. 24h)")
			return
		}
		window = parsed
	}
	since := time.Now().Add(-window)

	stats, err := s.queries.GetRunStats(r.Context(), pgtype.Timestamptz{Time: since, Valid: true})
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed fetching run stats")
		return
	}

	writeJSON(w, http.StatusOK, runStatsResponse{
		Queued:    stats.Queued,
		Succeeded: stats.Succeeded,
		Failed:    stats.Failed,
		Cancelled: stats.Cancelled,
		Lost:      stats.Lost,
		Since:     since,
	})
}
