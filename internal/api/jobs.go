package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/gitrepo"
	"github.com/r3dpan/project-descendence/internal/manifest"
	"github.com/r3dpan/project-descendence/internal/runtimeprune"
	"github.com/r3dpan/project-descendence/internal/store"
)

// problemError carries the RFC 9457 status/detail a failed run-creation
// wants reported, without writing the HTTP response itself (task 5.3) - so
// a second caller (the schedule trigger handler, internal/api/schedules.go)
// can decide its own response shape for cases that differ from the ordinary
// job-run endpoint's, while CreateJobRunHandler keeps writing exactly what
// it always has.
type problemError struct {
	status int
	detail string
}

func (e *problemError) Error() string { return e.detail }

const (
	defaultJobListLimit = 50
	maxJobListLimit     = 200
)

// --- Request/response objects ---

// jobResponse is a job as the API presents it.
//
// Almost every field here is owned by git and rewritten by the next sync; the
// exception is `enabled`. syncedCommitSha is included so a caller can see
// which commit the row was built from without reading the repository - the
// answer to "is what I am looking at current?".
type jobResponse struct {
	ID           int64   `json:"id"`
	RepoID       int64   `json:"repoId"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	ManifestPath string  `json:"manifestPath"`
	ScriptPath   string  `json:"scriptPath"`
	// Null when the manifest names no explicit command, which is the usual
	// case: argv is then the script's own path and its shebang decides.
	Command         []string           `json:"command"`
	ImageRef        *string            `json:"imageRef"`
	RuntimeID       *int64             `json:"runtimeId"`
	TimeoutSeconds  *int32             `json:"timeoutSeconds"`
	Params          []jobParamResponse `json:"params"`
	Enabled         bool               `json:"enabled"`
	SyncedCommitSHA string             `json:"syncedCommitSha"`
	// Set when the manifest has been removed from the repository. Such a job
	// cannot be run and does not appear in listings, but it still exists so
	// that runs which used it remain explainable.
	DeletedAt *string `json:"deletedAt"`
}

// jobParamResponse mirrors manifest.Param (task 6.1) - a separate type
// rather than reusing it directly, matching this package's convention of
// never exposing a store/manifest type as the wire shape.
type jobParamResponse struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Required bool    `json:"required"`
	Default  *string `json:"default"`
	Secret   bool    `json:"secret"`
}

type jobListResponse struct {
	Items      []jobResponse `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

type jobPatchRequest struct {
	// A pointer so that {} is "change nothing" rather than "disable".
	Enabled *bool `json:"enabled"`
}

// createJobRunRequest is the optional body of POST /api/v1/jobs/{id}/runs
// (task 6.2). Raw strings, matching --param name=value on the command line
// - manifest.ResolveParams coerces them against the job's contract.
type createJobRunRequest struct {
	Params map[string]string `json:"params"`
}

func toJobResponse(job store.Job) jobResponse {
	resp := jobResponse{
		ID:              job.ID,
		RepoID:          job.RepoID,
		Name:            job.Name,
		ManifestPath:    job.ManifestPath,
		ScriptPath:      job.ScriptPath,
		Command:         job.Command,
		Enabled:         job.Enabled,
		SyncedCommitSHA: job.SyncedCommitSha,
	}
	if job.Description.Valid {
		resp.Description = &job.Description.String
	}
	if job.ImageRef.Valid {
		resp.ImageRef = &job.ImageRef.String
	}
	if job.RuntimeID.Valid {
		resp.RuntimeID = &job.RuntimeID.Int64
	}
	if job.TimeoutSeconds.Valid {
		resp.TimeoutSeconds = &job.TimeoutSeconds.Int32
	}
	if job.DeletedAt.Valid {
		formatted := job.DeletedAt.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		resp.DeletedAt = &formatted
	}
	resp.Params = jobParamsResponse(job.ParamsJson)
	return resp
}

// jobParamsResponse decodes a jobs row's params_json projection (task 6.1)
// into the wire shape. Always returns a non-nil slice - the column's NOT
// NULL DEFAULT '[]' means empty is the only "no params" case, never null -
// so the JSON response is always a `[]`, never a `null`.
func jobParamsResponse(raw []byte) []jobParamResponse {
	var params []manifest.Param
	if len(raw) > 0 {
		// jobsync is the sole writer of this column and only ever writes
		// what validateParams already accepted, so a decode error here
		// would mean the projection and the parser have drifted - not
		// something a caller can act on, so it's surfaced as empty rather
		// than failing the whole job response.
		_ = json.Unmarshal(raw, &params)
	}
	resp := make([]jobParamResponse, len(params))
	for i, p := range params {
		resp[i] = jobParamResponse{
			Name:     p.Name,
			Type:     p.Type,
			Required: p.Required,
			Default:  p.Default,
			Secret:   p.Secret,
		}
	}
	return resp
}

// encodeNameCursor and decodeNameCursor are the (name, id) equivalent of
// runs' (queuedAt, id) cursor. A separate pair because jobs are ordered by
// name - a catalogue rather than a timeline - and reusing a timestamp-shaped
// cursor for it would only encode the wrong seek position convincingly.
func encodeNameCursor(name string, id int64) string {
	return base64.URLEncoding.EncodeToString([]byte(fmt.Sprintf("%s|%d", name, id)))
}

func decodeNameCursor(encoded string) (string, int64, error) {
	raw, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		return "", 0, fmt.Errorf("cursor is not valid base64: %w", err)
	}
	// A name may not contain "|" (manifest.namePattern forbids it), so the
	// last separator is unambiguous.
	separator := strings.LastIndex(string(raw), "|")
	if separator < 0 {
		return "", 0, errors.New("cursor is malformed")
	}
	id, err := strconv.ParseInt(string(raw)[separator+1:], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("cursor has a malformed id: %w", err)
	}
	return string(raw)[:separator], id, nil
}

// --- Handlers ---

// ListJobsHandler lists live jobs, keyset-paginated by name.
func (s *APIServer) ListJobsHandler(w http.ResponseWriter, r *http.Request) {
	limit := int32(defaultJobListLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 1 || parsed > maxJobListLimit {
			writeProblem(w, http.StatusBadRequest, fmt.Sprintf("limit must be an integer between 1 and %d", maxJobListLimit))
			return
		}
		limit = int32(parsed)
	}

	// One extra row tells us whether a next page exists without a count.
	params := store.ListJobsParams{RowLimit: limit + 1}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		name, id, err := decodeNameCursor(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "malformed cursor")
			return
		}
		params.CursorName = pgtype.Text{String: name, Valid: true}
		params.CursorID = pgtype.Int8{Int64: id, Valid: true}
	}

	jobs, err := s.queries.ListJobs(r.Context(), params)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed listing jobs")
		return
	}

	var nextCursor *string
	if int32(len(jobs)) > limit {
		jobs = jobs[:limit]
		last := jobs[len(jobs)-1]
		c := encodeNameCursor(last.Name, last.ID)
		nextCursor = &c
	}

	items := make([]jobResponse, len(jobs))
	for i, job := range jobs {
		items[i] = toJobResponse(job)
	}

	writeJSON(w, http.StatusOK, jobListResponse{Items: items, NextCursor: nextCursor})
}

// GetJobHandler returns one job by id, soft-deleted ones included - a run
// pointing at a deleted job still has to be explainable.
func (s *APIServer) GetJobHandler(w http.ResponseWriter, r *http.Request) {
	job, ok := s.lookupJob(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toJobResponse(job))
}

// PatchJobHandler updates the only column git does not own.
//
// There is no endpoint to change anything else about a job, and that is the
// design rather than an omission: a job is defined by its manifest, so
// changing one means committing a manifest (task 3.7) and re-syncing. An API
// that could edit a job directly would make git and Postgres disagree about
// what a job is, with no way to say which was right.
func (s *APIServer) PatchJobHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no job with this id")
		return
	}

	var req jobPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}
	if req.Enabled == nil {
		writeProblem(w, http.StatusBadRequest, "enabled is the only field of a job that can be changed through the API; everything else is defined by the manifest in git")
		return
	}

	rows, err := s.queries.SetJobEnabled(r.Context(), store.SetJobEnabledParams{ID: id, Enabled: *req.Enabled})
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed updating job")
		return
	}
	if rows == 0 {
		// Either no such job, or its manifest has been deleted. Both are
		// "there is no live job here to change".
		writeProblem(w, http.StatusNotFound, "no live job with this id")
		return
	}

	job, err := s.queries.GetJob(r.Context(), id)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed reloading job")
		return
	}
	writeJSON(w, http.StatusOK, toJobResponse(job))
}

// CreateJobRunHandler triggers a job (task 3.5).
//
// The run is pinned to a commit here, at creation, and everything downstream
// reads that SHA rather than the branch. That is the whole reproducibility
// story (§2.4): the branch can move a millisecond later and this run still
// executes, and still explains, exactly what it resolved to.
//
// The manifest is read at the resolved SHA rather than taken from the `jobs`
// projection, because the projection tracks HEAD and may already describe a
// newer definition. The supervisor reads the same manifest at the same SHA
// when it materialises the script, so the two cannot disagree.
func (s *APIServer) CreateJobRunHandler(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "no principal in request context")
		return
	}

	job, ok := s.lookupJob(w, r)
	if !ok {
		return
	}

	// The body is optional (task 6.2): a job with no required params, or a
	// caller happy with every default, sends no params at all. Only a
	// present-but-malformed body is an error - an empty one just means "no
	// params submitted", the same as omitting Idempotency-Key means "none".
	var req createJobRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))

	run, problem := s.createJobRun(r.Context(), principal, job, idempotencyKey, nil, req.Params)
	if problem != nil {
		writeProblem(w, problem.status, problem.detail)
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/runs/%d", run.ID))
	writeJSON(w, http.StatusAccepted, toRunResponse(run))
}

// createJobRun is CreateJobRunHandler's run-creation logic (task 3.5,
// extended at task 5.3 to be callable from the schedule trigger handler
// too): resolve the job's repository HEAD, read and parse its manifest at
// that exact commit, pin a runtime/image, and insert the run.
//
// idempotencyKey is "" when the caller has none to offer (the schedule
// trigger path never does - systemd fires a unit once per due window, and
// the overlap policy, not idempotency, is what decides whether a second run
// gets created). scheduleID is nil for an ordinary job-run request and set
// only when the trigger handler calls this. submittedParams (task 6.2) is
// nil for the trigger path too - a schedule fire has no submission of its
// own, so the job's contract resolves to defaults only.
func (s *APIServer) createJobRun(ctx context.Context, principal store.Principal, job store.Job, idempotencyKey string, scheduleID *int64, submittedParams map[string]string) (store.Run, *problemError) {
	if job.DeletedAt.Valid {
		return store.Run{}, &problemError{http.StatusConflict, fmt.Sprintf("job %q cannot be run: its manifest has been removed from the repository", job.Name)}
	}
	if !job.Enabled {
		return store.Run{}, &problemError{http.StatusConflict, fmt.Sprintf("job %q is disabled", job.Name)}
	}

	repo, err := s.queries.GetRepo(ctx, job.RepoID)
	if err != nil {
		return store.Run{}, &problemError{http.StatusInternalServerError, "failed loading the job's repository"}
	}

	repository, err := s.repos.Open(repo.Name)
	if err != nil {
		return store.Run{}, &problemError{http.StatusInternalServerError, fmt.Sprintf("failed opening repository %s", repo.Name)}
	}

	sha, err := repository.HeadCommit(repo.DefaultBranch)
	if errors.Is(err, gitrepo.ErrNoCommits) {
		return store.Run{}, &problemError{http.StatusConflict, fmt.Sprintf("repository %s has no commits on %s", repo.Name, repo.DefaultBranch)}
	} else if err != nil {
		return store.Run{}, &problemError{http.StatusInternalServerError, "failed resolving the repository's current commit"}
	}

	rawManifest, err := repository.ReadFile(sha, job.ManifestPath)
	if err != nil {
		// The projection said this manifest exists; at HEAD it does not. The
		// projection is simply stale - a sync has not run since the manifest
		// was removed.
		return store.Run{}, &problemError{http.StatusConflict, fmt.Sprintf("manifest %s is not present at %s; re-sync the repository", job.ManifestPath, shortSHA(sha))}
	}

	parsed, err := manifest.Parse(job.ManifestPath, rawManifest)
	if err != nil {
		return store.Run{}, &problemError{http.StatusConflict, fmt.Sprintf("manifest is not valid at %s: %v", shortSHA(sha), err)}
	}

	// Task 6.2: submitted values are resolved against the contract at the
	// same pinned commit the manifest itself was just read at, so a param
	// added or removed by a later commit can never be applied to a run
	// pinned to an earlier one.
	resolvedParams, err := manifest.ResolveParams(parsed.Params, submittedParams)
	if err != nil {
		return store.Run{}, &problemError{http.StatusBadRequest, err.Error()}
	}
	paramsJSON, err := json.Marshal(resolvedParams)
	if err != nil {
		return store.Run{}, &problemError{http.StatusInternalServerError, "failed encoding resolved params"}
	}

	// Task 4.6: the manifest names either an image directly or a runtime -
	// manifest.Parse already enforces exactly one is set. imageRef is what
	// this run actually pins; runtimeID/imageDigest are provenance, set only
	// on the runtime path.
	imageRef := parsed.ImageRef
	var runtimeID pgtype.Int8
	var imageDigest pgtype.Text
	if parsed.RuntimeName != "" {
		runtime, err := s.queries.GetRuntimeByName(ctx, parsed.RuntimeName)
		if err != nil {
			return store.Run{}, &problemError{http.StatusConflict, fmt.Sprintf("manifest names runtime %q, which is not defined", parsed.RuntimeName)}
		}
		if runtime.BuildStatus != store.BuildStatusReady {
			return store.Run{}, &problemError{http.StatusConflict, fmt.Sprintf("runtime %q is not built yet (status %s); build it before running this job", runtime.Name, runtime.BuildStatus)}
		}
		if runtime.ImagePrunedAt.Valid {
			return store.Run{}, &problemError{http.StatusConflict, fmt.Sprintf("runtime %q's image has been pruned; rebuild it before running this job", runtime.Name)}
		}
		// Pinned here, at creation - never re-resolved later - so a rebuild
		// of the runtime after this point cannot change what this run
		// executes (ARCHITECTURE.md §2.4's reproducibility principle,
		// applied to runtimes the same way it already applies to commits).
		imageRef = runtimeprune.ImageTag(runtime)
		runtimeID = pgtype.Int8{Int64: runtime.ID, Valid: true}
		imageDigest = runtime.ImageDigest
	}

	timeoutSeconds := int32(defaultRunTimeoutSeconds)
	if parsed.TimeoutSeconds != nil {
		timeoutSeconds = *parsed.TimeoutSeconds
	}

	idempotencyKeyCol := pgtype.Text{}
	if idempotencyKey != "" {
		idempotencyKeyCol = pgtype.Text{String: idempotencyKey, Valid: true}
	}

	scheduleIDCol := pgtype.Int8{}
	if scheduleID != nil {
		scheduleIDCol = pgtype.Int8{Int64: *scheduleID, Valid: true}
	}

	run, err := s.queries.CreateJobRun(ctx, store.CreateJobRunParams{
		PrincipalID:    principal.ID,
		ImageRef:       imageRef,
		Argv:           parsed.Argv(),
		TimeoutSeconds: timeoutSeconds,
		IdempotencyKey: idempotencyKeyCol,
		JobID:          pgtype.Int8{Int64: job.ID, Valid: true},
		CommitSha:      pgtype.Text{String: sha, Valid: true},
		RuntimeID:      runtimeID,
		ImageDigest:    imageDigest,
		ScheduleID:     scheduleIDCol,
		ParamsJson:     paramsJSON,
	})
	if err != nil {
		if err != pgx.ErrNoRows {
			return store.Run{}, &problemError{http.StatusInternalServerError, "failed creating run"}
		}
		// Same replay path as an ad-hoc run: the insert was skipped by
		// ON CONFLICT, so return the original rather than erroring. Only
		// reachable when idempotencyKeyCol is Valid - an unkeyed insert
		// never conflicts.
		run, err = s.queries.GetRunByIdempotencyKey(ctx, store.GetRunByIdempotencyKeyParams{
			PrincipalID:    principal.ID,
			IdempotencyKey: idempotencyKeyCol,
		})
		if err != nil {
			return store.Run{}, &problemError{http.StatusInternalServerError, "failed fetching original run for replayed Idempotency-Key"}
		}
	}

	return run, nil
}

// lookupJob resolves the {id} path value, answering 404 itself when it cannot.
func (s *APIServer) lookupJob(w http.ResponseWriter, r *http.Request) (store.Job, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no job with this id")
		return store.Job{}, false
	}

	job, err := s.queries.GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusNotFound, "no job with this id")
			return store.Job{}, false
		}
		writeProblem(w, http.StatusInternalServerError, "failed loading job")
		return store.Job{}, false
	}
	return job, true
}

// shortSHA trims a commit SHA for messages people read.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
