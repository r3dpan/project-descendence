package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/runtimebuild"
	"github.com/r3dpan/project-descendence/internal/runtimeprune"
	"github.com/r3dpan/project-descendence/internal/store"
)

const (
	defaultRuntimeListLimit = 50
	maxRuntimeListLimit     = 200
)

// --- Request/response objects ---

// runtimeResponse is a runtime as the API presents it. Unlike a job, every
// field here is owned by Postgres directly - a runtime is a built artifact,
// not a projection of something authored in git (ARCHITECTURE.md §4.4;
// decision #23 scopes the git-projection pattern to jobs specifically).
type runtimeResponse struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	BaseImage    string   `json:"baseImage"`
	SysPackages  []string `json:"sysPackages"`
	Lang         string   `json:"lang"`
	LangManifest *string  `json:"langManifest"`
	InputHash    string   `json:"inputHash"`
	BuildStatus  string   `json:"buildStatus"`
	ImageDigest  *string  `json:"imageDigest"`
	BuildError   *string  `json:"buildError"`
	BuiltAt      *string  `json:"builtAt"`
	ImagePruned  bool     `json:"imagePruned"`
	CreatedAt    string   `json:"createdAt"`
}

type runtimeListResponse struct {
	Items      []runtimeResponse `json:"items"`
	NextCursor *string           `json:"nextCursor"`
}

type runtimeCreateRequest struct {
	Name         string   `json:"name"`
	BaseImage    string   `json:"baseImage"` // optional; falls back to runtimebuild.CuratedBaseImages[lang]
	SysPackages  []string `json:"sysPackages"`
	Lang         string   `json:"lang"`
	LangManifest string   `json:"langManifest"`
}

func formatTimestamptz(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	formatted := ts.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	return &formatted
}

func toRuntimeResponse(runtime store.Runtime) runtimeResponse {
	resp := runtimeResponse{
		ID:          runtime.ID,
		Name:        runtime.Name,
		BaseImage:   runtime.BaseImage,
		SysPackages: runtime.SysPackages,
		Lang:        runtime.Lang,
		InputHash:   runtime.InputHash,
		BuildStatus: runtime.BuildStatus,
		BuiltAt:     formatTimestamptz(runtime.BuiltAt),
		ImagePruned: runtime.ImagePrunedAt.Valid,
		CreatedAt:   *formatTimestamptz(runtime.CreatedAt),
	}
	if runtime.LangManifest.Valid {
		resp.LangManifest = &runtime.LangManifest.String
	}
	if runtime.ImageDigest.Valid {
		resp.ImageDigest = &runtime.ImageDigest.String
	}
	if runtime.BuildError.Valid {
		resp.BuildError = &runtime.BuildError.String
	}
	return resp
}

// --- Handlers ---

// ListRuntimesHandler lists runtimes, keyset-paginated by name - the same
// (name, id) cursor shape as ListJobsHandler, since a runtime catalogue is
// also not a timeline.
func (s *APIServer) ListRuntimesHandler(w http.ResponseWriter, r *http.Request) {
	limit := int32(defaultRuntimeListLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 1 || parsed > maxRuntimeListLimit {
			writeProblem(w, http.StatusBadRequest, fmt.Sprintf("limit must be an integer between 1 and %d", maxRuntimeListLimit))
			return
		}
		limit = int32(parsed)
	}

	params := store.ListRuntimesParams{RowLimit: limit + 1}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		name, id, err := decodeNameCursor(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "malformed cursor")
			return
		}
		params.CursorName = pgtype.Text{String: name, Valid: true}
		params.CursorID = pgtype.Int8{Int64: id, Valid: true}
	}

	runtimes, err := s.queries.ListRuntimes(r.Context(), params)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed listing runtimes")
		return
	}

	var nextCursor *string
	if int32(len(runtimes)) > limit {
		runtimes = runtimes[:limit]
		last := runtimes[len(runtimes)-1]
		c := encodeNameCursor(last.Name, last.ID)
		nextCursor = &c
	}

	items := make([]runtimeResponse, len(runtimes))
	for i, runtime := range runtimes {
		items[i] = toRuntimeResponse(runtime)
	}

	writeJSON(w, http.StatusOK, runtimeListResponse{Items: items, NextCursor: nextCursor})
}

// GetRuntimeHandler returns one runtime by id. It also doubles as the
// build-status poll endpoint for task 4.5's async build: buildStatus and
// buildError live directly on the row, so there is no separate build
// resource to poll.
func (s *APIServer) GetRuntimeHandler(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.lookupRuntime(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toRuntimeResponse(runtime))
}

// CreateRuntimeHandler defines a new runtime (task 4.5) and, by leaving
// build_status at its table default of 'pending', immediately queues its
// first build - the supervisor's build claim loop (build.go) picks it up on
// its next tick. A later POST .../build (BuildRuntimeHandler) is for
// requesting a *rebuild* once this one has reached ready or failed.
func (s *APIServer) CreateRuntimeHandler(w http.ResponseWriter, r *http.Request) {
	var req runtimeCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}

	if req.Name == "" {
		writeProblem(w, http.StatusBadRequest, "name is required")
		return
	}
	switch req.Lang {
	case store.LangPython, store.LangPowerShell, store.LangNode:
	default:
		writeProblem(w, http.StatusBadRequest, fmt.Sprintf("lang must be one of %q, %q, %q", store.LangPython, store.LangPowerShell, store.LangNode))
		return
	}

	baseImage := req.BaseImage
	if baseImage == "" {
		baseImage = runtimebuild.CuratedBaseImages[req.Lang]
	}

	def := runtimebuild.Definition{
		BaseImage:    baseImage,
		SysPackages:  req.SysPackages,
		Lang:         req.Lang,
		LangManifest: req.LangManifest,
	}
	// Rendering here, before the insert, is validation: a definition that
	// cannot produce a Containerfile should never reach build_status =
	// 'pending' only to fail asynchronously for a reason the caller could
	// have been told immediately.
	if _, err := runtimebuild.Render(def); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}

	sysPackages := req.SysPackages
	if sysPackages == nil {
		// sys_packages is NOT NULL; a nil slice here would send SQL NULL
		// rather than falling back to the column's DEFAULT '{}', since the
		// column is named explicitly in every insert.
		sysPackages = []string{}
	}

	params := store.CreateRuntimeParams{
		Name:        req.Name,
		BaseImage:   baseImage,
		SysPackages: sysPackages,
		Lang:        req.Lang,
		InputHash:   runtimebuild.InputHash(def),
	}
	if req.LangManifest != "" {
		params.LangManifest = pgtype.Text{String: req.LangManifest, Valid: true}
	}

	runtime, err := s.queries.CreateRuntime(r.Context(), params)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "runtimes_name_key" {
			writeProblem(w, http.StatusConflict, fmt.Sprintf("a runtime named %q already exists", req.Name))
			return
		}
		writeProblem(w, http.StatusInternalServerError, "failed creating runtime")
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/runtimes/%d", runtime.ID))
	writeJSON(w, http.StatusAccepted, toRuntimeResponse(runtime))
}

// BuildRuntimeHandler queues a rebuild of an existing runtime (task 4.5).
// 409 if a build is already pending or building - there is one build slot
// per runtime, unlike runs, which queue freely, so a repeat POST while one
// is in flight is rejected rather than silently queuing a second build of
// the same row.
func (s *APIServer) BuildRuntimeHandler(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.lookupRuntime(w, r)
	if !ok {
		return
	}

	rows, err := s.queries.RequestRuntimeBuild(r.Context(), runtime.ID)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed queuing build")
		return
	}
	if rows == 0 {
		writeProblem(w, http.StatusConflict, fmt.Sprintf("runtime %q already has a build %s", runtime.Name, runtime.BuildStatus))
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/runtimes/%d", runtime.ID))
	writeJSON(w, http.StatusAccepted, map[string]any{"id": runtime.ID, "buildStatus": store.BuildStatusPending})
}

// --- Prune (task 4.7) ---

type pruneRuntimesRequest struct {
	// Exactly one of these two. IDs prunes specific runtimes right now,
	// regardless of age or use. OlderThanDays prunes every built,
	// not-yet-pruned runtime whose image is unreferenced by any run started
	// in that window and by any live job - the same rule the supervisor's
	// unattended sweep (build.go / prune.go's runtime half) applies on its
	// own cadence, so a manual call and the automatic one never disagree
	// about what "unused" means.
	IDs           []int64 `json:"ids"`
	OlderThanDays *int    `json:"olderThanDays"`
}

type pruneRuntimesResponse struct {
	Pruned  []string `json:"pruned"`
	Skipped []string `json:"skipped"`
	Errors  []string `json:"errors"`
}

// PruneRuntimesHandler reclaims runtime image storage (task 4.7), either for
// explicit ids or for everything unused past an age threshold. The runtime
// row always survives - only image_pruned_at is set and the image is
// deleted from Podman - mirroring decision #18's "row survives, bytes
// don't" pattern for run logs.
func (s *APIServer) PruneRuntimesHandler(w http.ResponseWriter, r *http.Request) {
	var req pruneRuntimesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}
	if len(req.IDs) == 0 && req.OlderThanDays == nil {
		writeProblem(w, http.StatusBadRequest, "specify either ids or olderThanDays")
		return
	}
	if len(req.IDs) > 0 && req.OlderThanDays != nil {
		writeProblem(w, http.StatusBadRequest, "specify only one of ids or olderThanDays, not both")
		return
	}

	resp := pruneRuntimesResponse{Pruned: []string{}, Skipped: []string{}, Errors: []string{}}

	var candidates []store.Runtime
	if len(req.IDs) > 0 {
		for _, id := range req.IDs {
			runtime, err := s.queries.GetRuntime(r.Context(), id)
			if err != nil {
				resp.Errors = append(resp.Errors, fmt.Sprintf("runtime %d: not found", id))
				continue
			}
			candidates = append(candidates, runtime)
		}
	} else {
		if *req.OlderThanDays < 0 {
			writeProblem(w, http.StatusBadRequest, "olderThanDays must not be negative")
			return
		}
		var err error
		candidates, err = runtimeprune.Candidates(r.Context(), s.queries, time.Duration(*req.OlderThanDays)*24*time.Hour)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "failed listing prunable runtimes")
			return
		}
	}

	for _, runtime := range candidates {
		if runtime.ImagePrunedAt.Valid {
			resp.Skipped = append(resp.Skipped, runtime.Name+": already pruned")
			continue
		}
		if runtime.BuildStatus != store.BuildStatusReady {
			resp.Skipped = append(resp.Skipped, fmt.Sprintf("%s: not built (build status %s)", runtime.Name, runtime.BuildStatus))
			continue
		}

		if err := runtimeprune.Prune(r.Context(), s.queries, s.podman, runtime); err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %v", runtime.Name, err))
			continue
		}
		resp.Pruned = append(resp.Pruned, runtime.Name)
	}

	writeJSON(w, http.StatusOK, resp)
}

// lookupRuntime resolves the {id} path value, answering 404 itself when it
// cannot - the same shape as lookupJob.
func (s *APIServer) lookupRuntime(w http.ResponseWriter, r *http.Request) (store.Runtime, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no runtime with this id")
		return store.Runtime{}, false
	}

	runtime, err := s.queries.GetRuntime(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusNotFound, "no runtime with this id")
			return store.Runtime{}, false
		}
		writeProblem(w, http.StatusInternalServerError, "failed loading runtime")
		return store.Runtime{}, false
	}
	return runtime, true
}
