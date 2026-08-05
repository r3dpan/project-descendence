package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/gitrepo"
	"github.com/r3dpan/project-descendence/internal/manifest"
	"github.com/r3dpan/project-descendence/internal/store"
)

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
	Command         []string `json:"command"`
	ImageRef        *string  `json:"imageRef"`
	TimeoutSeconds  *int32   `json:"timeoutSeconds"`
	Enabled         bool     `json:"enabled"`
	SyncedCommitSHA string   `json:"syncedCommitSha"`
	// Set when the manifest has been removed from the repository. Such a job
	// cannot be run and does not appear in listings, but it still exists so
	// that runs which used it remain explainable.
	DeletedAt *string `json:"deletedAt"`
}

type jobListResponse struct {
	Items      []jobResponse `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

type jobPatchRequest struct {
	// A pointer so that {} is "change nothing" rather than "disable".
	Enabled *bool `json:"enabled"`
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
	if job.TimeoutSeconds.Valid {
		resp.TimeoutSeconds = &job.TimeoutSeconds.Int32
	}
	if job.DeletedAt.Valid {
		formatted := job.DeletedAt.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		resp.DeletedAt = &formatted
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

	if job.DeletedAt.Valid {
		writeProblem(w, http.StatusConflict, fmt.Sprintf("job %q cannot be run: its manifest has been removed from the repository", job.Name))
		return
	}
	if !job.Enabled {
		writeProblem(w, http.StatusConflict, fmt.Sprintf("job %q is disabled", job.Name))
		return
	}

	repo, err := s.queries.GetRepo(r.Context(), job.RepoID)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed loading the job's repository")
		return
	}

	repository, err := s.repos.Open(repo.Name)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, fmt.Sprintf("failed opening repository %s", repo.Name))
		return
	}

	sha, err := repository.HeadCommit(repo.DefaultBranch)
	if errors.Is(err, gitrepo.ErrNoCommits) {
		writeProblem(w, http.StatusConflict, fmt.Sprintf("repository %s has no commits on %s", repo.Name, repo.DefaultBranch))
		return
	} else if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed resolving the repository's current commit")
		return
	}

	rawManifest, err := repository.ReadFile(sha, job.ManifestPath)
	if err != nil {
		// The projection said this manifest exists; at HEAD it does not. The
		// projection is simply stale - a sync has not run since the manifest
		// was removed.
		writeProblem(w, http.StatusConflict, fmt.Sprintf("manifest %s is not present at %s; re-sync the repository", job.ManifestPath, shortSHA(sha)))
		return
	}

	parsed, err := manifest.Parse(job.ManifestPath, rawManifest)
	if err != nil {
		writeProblem(w, http.StatusConflict, fmt.Sprintf("manifest is not valid at %s: %v", shortSHA(sha), err))
		return
	}
	if parsed.ImageRef == "" {
		writeProblem(w, http.StatusConflict, "manifest names no image to run in")
		return
	}

	timeoutSeconds := int32(defaultRunTimeoutSeconds)
	if parsed.TimeoutSeconds != nil {
		timeoutSeconds = *parsed.TimeoutSeconds
	}

	idempotencyKey := pgtype.Text{}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		idempotencyKey = pgtype.Text{String: key, Valid: true}
	}

	run, err := s.queries.CreateJobRun(r.Context(), store.CreateJobRunParams{
		PrincipalID:    principal.ID,
		ImageRef:       parsed.ImageRef,
		Argv:           parsed.Argv(),
		TimeoutSeconds: timeoutSeconds,
		IdempotencyKey: idempotencyKey,
		JobID:          pgtype.Int8{Int64: job.ID, Valid: true},
		CommitSha:      pgtype.Text{String: sha, Valid: true},
	})
	if err != nil {
		if err != pgx.ErrNoRows {
			writeProblem(w, http.StatusInternalServerError, "failed creating run")
			return
		}
		// Same replay path as an ad-hoc run: the insert was skipped by
		// ON CONFLICT, so return the original rather than erroring.
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
