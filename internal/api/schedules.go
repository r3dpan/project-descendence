package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/robfig/cron/v3"

	"github.com/r3dpan/project-descendence/internal/scheduling"
	"github.com/r3dpan/project-descendence/internal/store"
)

const (
	defaultScheduleTimezone      = "UTC"
	defaultScheduleCatchUpPolicy = store.CatchUpPolicySkip
	defaultScheduleOverlapPolicy = store.OverlapPolicySkip
)

// --- Request/response objects ---

type scheduleCreateRequest struct {
	CronExpr      string  `json:"cronExpr"`
	Timezone      *string `json:"timezone"`
	CatchUpPolicy *string `json:"catchUpPolicy"`
	OverlapPolicy *string `json:"overlapPolicy"`
	Enabled       *bool   `json:"enabled"`
}

// schedulePatchRequest mirrors scheduleCreateRequest's optionality, but
// "unset" here means "leave this field as it is" rather than "use the
// default" - PATCH semantics, matching PatchJobHandler's pointer pattern.
type schedulePatchRequest struct {
	CronExpr      *string `json:"cronExpr"`
	Timezone      *string `json:"timezone"`
	CatchUpPolicy *string `json:"catchUpPolicy"`
	OverlapPolicy *string `json:"overlapPolicy"`
	Enabled       *bool   `json:"enabled"`
}

type scheduleResponse struct {
	ID            int64     `json:"id"`
	JobID         int64     `json:"jobId"`
	CronExpr      string    `json:"cronExpr"`
	Timezone      string    `json:"timezone"`
	CatchUpPolicy string    `json:"catchUpPolicy"`
	OverlapPolicy string    `json:"overlapPolicy"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	// Informational only, computed on the fly via robfig/cron - never
	// stored (decision #27: systemd, not this value, is what actually
	// fires). Null if the schedule's cron_expr/timezone somehow can't be
	// parsed right now, which validateScheduleFields already prevents at
	// write time.
	NextDueAt *time.Time `json:"nextDueAt"`
}

type scheduleListResponse struct {
	Items []scheduleResponse `json:"items"`
}

// scheduleTriggerResponse is POST /api/v1/schedules/{id}/trigger's body.
// Skipped is true exactly when the overlap policy chose not to create a
// run (task 5.6) - a 200, not an error, since the schedule fired exactly
// as designed (decision #27).
type scheduleTriggerResponse struct {
	Skipped bool         `json:"skipped"`
	Reason  string       `json:"reason,omitempty"`
	Run     *runResponse `json:"run,omitempty"`
}

func toScheduleResponse(sched store.Schedule) scheduleResponse {
	resp := scheduleResponse{
		ID:            sched.ID,
		JobID:         sched.JobID,
		CronExpr:      sched.CronExpr,
		Timezone:      sched.Timezone,
		CatchUpPolicy: sched.CatchUpPolicy,
		OverlapPolicy: sched.OverlapPolicy,
		Enabled:       sched.Enabled,
		CreatedAt:     sched.CreatedAt.Time,
		UpdatedAt:     sched.UpdatedAt.Time,
	}
	if next := nextDueAt(sched.CronExpr, sched.Timezone); next != nil {
		resp.NextDueAt = next
	}
	return resp
}

// nextDueAt computes an informational next-fire estimate via robfig/cron.
// It is not what actually drives firing (systemd is, per decision #27) and
// the two are independent implementations of "when does this fire next" -
// a rare disagreement at a DST boundary would be a display bug here, not a
// correctness bug, since nothing downstream reads this value.
func nextDueAt(cronExpr, timezone string) *time.Time {
	schedule, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return nil
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil
	}
	next := schedule.Next(time.Now().In(loc))
	return &next
}

// validateScheduleFields checks that cronExpr translates to a supported
// OnCalendar= expression, timezone is a real IANA zone, and both policy
// columns are one of their enum values - the same checks
// schedules_catch_up_policy_check/schedules_overlap_policy_check enforce in
// the database, run here first so a bad request gets a 400 with a specific
// reason rather than a opaque constraint-violation 500.
func validateScheduleFields(cronExpr, timezone, catchUpPolicy, overlapPolicy string) *problemError {
	if _, err := scheduling.CronToOnCalendar(cronExpr); err != nil {
		return &problemError{http.StatusBadRequest, err.Error()}
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return &problemError{http.StatusBadRequest, fmt.Sprintf("invalid timezone %q: %v", timezone, err)}
	}
	switch catchUpPolicy {
	case store.CatchUpPolicySkip, store.CatchUpPolicyCatchUp:
	default:
		return &problemError{http.StatusBadRequest, fmt.Sprintf("catchUpPolicy must be %q or %q", store.CatchUpPolicySkip, store.CatchUpPolicyCatchUp)}
	}
	switch overlapPolicy {
	case store.OverlapPolicySkip, store.OverlapPolicyQueue, store.OverlapPolicyConcurrent:
	default:
		return &problemError{http.StatusBadRequest, fmt.Sprintf("overlapPolicy must be %q, %q or %q", store.OverlapPolicySkip, store.OverlapPolicyQueue, store.OverlapPolicyConcurrent)}
	}
	return nil
}

// --- Handlers ---

// CreateScheduleHandler creates a schedule for a job (task 5.7). A plain
// Postgres write - the supervisor's schedule-sync loop (cmd/supervisor/
// schedule.go) picks up the new row asynchronously and renders its systemd
// units, per decision #27.
func (s *APIServer) CreateScheduleHandler(w http.ResponseWriter, r *http.Request) {
	job, ok := s.lookupJob(w, r)
	if !ok {
		return
	}
	if job.DeletedAt.Valid {
		writeProblem(w, http.StatusConflict, fmt.Sprintf("job %q cannot be scheduled: its manifest has been removed from the repository", job.Name))
		return
	}

	var req scheduleCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}
	if req.CronExpr == "" {
		writeProblem(w, http.StatusBadRequest, "cronExpr is required")
		return
	}

	timezone := defaultScheduleTimezone
	if req.Timezone != nil {
		timezone = *req.Timezone
	}
	catchUpPolicy := defaultScheduleCatchUpPolicy
	if req.CatchUpPolicy != nil {
		catchUpPolicy = *req.CatchUpPolicy
	}
	overlapPolicy := defaultScheduleOverlapPolicy
	if req.OverlapPolicy != nil {
		overlapPolicy = *req.OverlapPolicy
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	if problem := validateScheduleFields(req.CronExpr, timezone, catchUpPolicy, overlapPolicy); problem != nil {
		writeProblem(w, problem.status, problem.detail)
		return
	}

	sched, err := s.queries.CreateSchedule(r.Context(), store.CreateScheduleParams{
		JobID:         job.ID,
		CronExpr:      req.CronExpr,
		Timezone:      timezone,
		CatchUpPolicy: catchUpPolicy,
		OverlapPolicy: overlapPolicy,
		Enabled:       enabled,
	})
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed creating schedule")
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/schedules/%d", sched.ID))
	writeJSON(w, http.StatusCreated, toScheduleResponse(sched))
}

// ListSchedulesByJobHandler lists a job's schedules. No pagination - a
// job's schedule count is small at this scale (task 5.7).
func (s *APIServer) ListSchedulesByJobHandler(w http.ResponseWriter, r *http.Request) {
	job, ok := s.lookupJob(w, r)
	if !ok {
		return
	}

	schedules, err := s.queries.ListSchedulesByJob(r.Context(), job.ID)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed listing schedules")
		return
	}

	items := make([]scheduleResponse, len(schedules))
	for i, sched := range schedules {
		items[i] = toScheduleResponse(sched)
	}
	writeJSON(w, http.StatusOK, scheduleListResponse{Items: items})
}

// GetScheduleHandler returns one schedule by id.
func (s *APIServer) GetScheduleHandler(w http.ResponseWriter, r *http.Request) {
	sched, ok := s.lookupSchedule(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toScheduleResponse(sched))
}

// PatchScheduleHandler updates a schedule (task 5.7). Unlike PatchJobHandler
// (which has exactly one mutable field), a schedule has five - fields the
// caller omits keep their current value, filled in from the row this
// handler already loaded, since UpdateSchedule takes all five directly
// (no COALESCE, see schedules.sql's comment on why).
func (s *APIServer) PatchScheduleHandler(w http.ResponseWriter, r *http.Request) {
	sched, ok := s.lookupSchedule(w, r)
	if !ok {
		return
	}

	var req schedulePatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}

	cronExpr := sched.CronExpr
	if req.CronExpr != nil {
		cronExpr = *req.CronExpr
	}
	timezone := sched.Timezone
	if req.Timezone != nil {
		timezone = *req.Timezone
	}
	catchUpPolicy := sched.CatchUpPolicy
	if req.CatchUpPolicy != nil {
		catchUpPolicy = *req.CatchUpPolicy
	}
	overlapPolicy := sched.OverlapPolicy
	if req.OverlapPolicy != nil {
		overlapPolicy = *req.OverlapPolicy
	}
	enabled := sched.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	if problem := validateScheduleFields(cronExpr, timezone, catchUpPolicy, overlapPolicy); problem != nil {
		writeProblem(w, problem.status, problem.detail)
		return
	}

	updated, err := s.queries.UpdateSchedule(r.Context(), store.UpdateScheduleParams{
		ID:            sched.ID,
		CronExpr:      cronExpr,
		Timezone:      timezone,
		CatchUpPolicy: catchUpPolicy,
		OverlapPolicy: overlapPolicy,
		Enabled:       enabled,
	})
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed updating schedule")
		return
	}
	writeJSON(w, http.StatusOK, toScheduleResponse(updated))
}

// DeleteScheduleHandler hard-deletes a schedule (task 5.7) - operator-owned
// data, not a git projection, so there's no soft-delete/explainability
// concern the way jobs has one (runs.schedule_id is ON DELETE SET NULL, so
// a past run this schedule created stays explainable regardless).
func (s *APIServer) DeleteScheduleHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no schedule with this id")
		return
	}

	rows, err := s.queries.DeleteSchedule(r.Context(), id)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed deleting schedule")
		return
	}
	if rows == 0 {
		writeProblem(w, http.StatusNotFound, "no schedule with this id")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TriggerScheduleHandler is what a generated schedule's .service unit calls
// (via `descendence schedule trigger <id>`) when its .timer fires (task
// 5.3). Requires the "run" scope - the first endpoint in this codebase to
// actually enforce one, a deliberate, narrow exception rather than a
// general RBAC rollout (ARCHITECTURE.md §7 still defers that).
func (s *APIServer) TriggerScheduleHandler(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "no principal in request context")
		return
	}
	if !principalHasScope(principal, "run") {
		writeProblem(w, http.StatusForbidden, `principal is missing the "run" scope required to trigger a schedule`)
		return
	}

	sched, ok := s.lookupSchedule(w, r)
	if !ok {
		return
	}
	if !sched.Enabled {
		writeProblem(w, http.StatusConflict, "schedule is disabled")
		return
	}

	job, err := s.queries.GetJob(r.Context(), sched.JobID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusConflict, "schedule's job no longer exists")
			return
		}
		writeProblem(w, http.StatusInternalServerError, "failed loading schedule's job")
		return
	}

	// Task 5.6: skip creating a run if this schedule's overlap policy is
	// "skip" and the run it created last time hasn't reached a terminal
	// state yet. "queue" and "concurrent" both fall through to the same
	// create-a-run path below - they are behaviorally identical today
	// because the supervisor's claim loop executes runs strictly one at a
	// time (decision #27's documented caveat), so there is nothing yet
	// that would make them diverge.
	if sched.OverlapPolicy == store.OverlapPolicySkip {
		latest, err := s.queries.GetLatestRunForSchedule(r.Context(), pgtype.Int8{Int64: sched.ID, Valid: true})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusInternalServerError, "failed checking schedule's previous run")
			return
		}
		if err == nil && !store.IsTerminal(latest.State) {
			writeJSON(w, http.StatusOK, scheduleTriggerResponse{
				Skipped: true,
				Reason:  fmt.Sprintf("run %d from this schedule's previous fire is still %s", latest.ID, latest.State),
			})
			return
		}
	}

	run, problem := s.createJobRun(r.Context(), principal, job, "", &sched.ID)
	if problem != nil {
		writeProblem(w, problem.status, problem.detail)
		return
	}

	resp := toRunResponse(run)
	w.Header().Set("Location", fmt.Sprintf("/api/v1/runs/%d", run.ID))
	writeJSON(w, http.StatusAccepted, scheduleTriggerResponse{Run: &resp})
}

// lookupSchedule resolves the {id} path value, answering 404 itself when it
// cannot - the same pattern lookupJob already uses.
func (s *APIServer) lookupSchedule(w http.ResponseWriter, r *http.Request) (store.Schedule, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no schedule with this id")
		return store.Schedule{}, false
	}

	sched, err := s.queries.GetSchedule(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusNotFound, "no schedule with this id")
			return store.Schedule{}, false
		}
		writeProblem(w, http.StatusInternalServerError, "failed loading schedule")
		return store.Schedule{}, false
	}
	return sched, true
}

// principalHasScope reports whether principal was minted with scope -
// TriggerScheduleHandler's check, the first place in this codebase that
// looks at principals.scopes rather than relying on token possession alone.
func principalHasScope(principal store.Principal, scope string) bool {
	for _, s := range principal.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}
