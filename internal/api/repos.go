package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/gitrepo"
	"github.com/r3dpan/project-descendence/internal/jobsync"
	"github.com/r3dpan/project-descendence/internal/store"
)

const (
	defaultRepoListLimit = 50
	maxRepoListLimit     = 200

	// defaultBranch is what a repository gets when the caller does not say.
	// Recorded per repository rather than assumed globally, because an
	// external repository (deferred, §7) brings its own convention.
	defaultBranch = "main"

	// maxUploadBytes bounds a single file upload. Scripts and manifests are
	// small; anything larger is a mistake or an attempt to fill the disk, and
	// a bare repository has no quota of its own.
	maxUploadBytes = 1 << 20
)

// --- Request/response objects ---

type repoCreateRequest struct {
	Name          string `json:"name"`
	DefaultBranch string `json:"defaultBranch"`
}

type repoResponse struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Path          string  `json:"path"`
	Kind          string  `json:"kind"`
	DefaultBranch string  `json:"defaultBranch"`
	RemoteURL     *string `json:"remoteUrl"`
	// Both null until the first sync. They report what the last scan saw and
	// are not an input to the next one - a scan always re-reads HEAD.
	LastSyncedAt        *string `json:"lastSyncedAt"`
	LastSyncedCommitSHA *string `json:"lastSyncedCommitSha"`
}

type repoListResponse struct {
	Items      []repoResponse `json:"items"`
	NextCursor *string        `json:"nextCursor"`
}

type repoFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Message string `json:"message"`
}

type repoFileResponse struct {
	Path      string          `json:"path"`
	CommitSHA string          `json:"commitSha"`
	Sync      *jobsync.Result `json:"sync"`
}

func toRepoResponse(repo store.Repo) repoResponse {
	resp := repoResponse{
		ID:            repo.ID,
		Name:          repo.Name,
		Path:          repo.Path,
		Kind:          repo.Kind,
		DefaultBranch: repo.DefaultBranch,
	}
	if repo.RemoteUrl.Valid {
		resp.RemoteURL = &repo.RemoteUrl.String
	}
	if repo.LastSyncedAt.Valid {
		formatted := repo.LastSyncedAt.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		resp.LastSyncedAt = &formatted
	}
	if repo.LastSyncedCommitSha.Valid {
		resp.LastSyncedCommitSHA = &repo.LastSyncedCommitSha.String
	}
	return resp
}

// --- Handlers ---

// CreateRepoHandler creates a bare repository on disk and records it.
//
// Disk first, row second. A row pointing at a directory that does not exist is
// a repository nothing can read and every later call has to defend against; a
// directory with no row is merely orphaned, visible, and harmless.
func (s *APIServer) CreateRepoHandler(w http.ResponseWriter, r *http.Request) {
	var req repoCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}

	name := strings.TrimSpace(req.Name)
	if err := gitrepo.ValidateRepoName(name); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}

	branch := strings.TrimSpace(req.DefaultBranch)
	if branch == "" {
		branch = defaultBranch
	}

	path, err := s.repos.Path(name)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}

	if _, err := s.repos.InitBare(name, branch); err != nil {
		if errors.Is(err, gitrepo.ErrRepoExists) {
			writeProblem(w, http.StatusConflict, fmt.Sprintf("a repository directory for %q already exists", name))
			return
		}
		writeProblem(w, http.StatusInternalServerError, "failed creating the repository on disk")
		return
	}

	repo, err := s.queries.CreateRepo(r.Context(), store.CreateRepoParams{
		Name:          name,
		Path:          path,
		Kind:          "local",
		DefaultBranch: branch,
	})
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed recording the repository")
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/repos/%d", repo.ID))
	writeJSON(w, http.StatusCreated, toRepoResponse(repo))
}

func (s *APIServer) ListReposHandler(w http.ResponseWriter, r *http.Request) {
	limit := int32(defaultRepoListLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 1 || parsed > maxRepoListLimit {
			writeProblem(w, http.StatusBadRequest, fmt.Sprintf("limit must be an integer between 1 and %d", maxRepoListLimit))
			return
		}
		limit = int32(parsed)
	}

	params := store.ListReposParams{RowLimit: limit + 1}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		name, id, err := decodeNameCursor(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "malformed cursor")
			return
		}
		params.CursorName = pgtype.Text{String: name, Valid: true}
		params.CursorID = pgtype.Int8{Int64: id, Valid: true}
	}

	repos, err := s.queries.ListRepos(r.Context(), params)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed listing repositories")
		return
	}

	var nextCursor *string
	if int32(len(repos)) > limit {
		repos = repos[:limit]
		last := repos[len(repos)-1]
		c := encodeNameCursor(last.Name, last.ID)
		nextCursor = &c
	}

	items := make([]repoResponse, len(repos))
	for i, repo := range repos {
		items[i] = toRepoResponse(repo)
	}

	writeJSON(w, http.StatusOK, repoListResponse{Items: items, NextCursor: nextCursor})
}

func (s *APIServer) GetRepoHandler(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.lookupRepo(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toRepoResponse(repo))
}

// SyncRepoHandler rebuilds the jobs projection from the repository (task 3.4).
//
// 200 even when individual manifests failed, with the failures in the body.
// A scan of ten manifests where one has a typo is not a failed request - nine
// jobs updated correctly, and the response says exactly which one did not and
// why. Answering 4xx or 5xx would throw that detail away and make the caller
// guess whether anything happened at all.
func (s *APIServer) SyncRepoHandler(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.lookupRepo(w, r)
	if !ok {
		return
	}

	result, err := jobsync.Sync(r.Context(), s.queries, s.repos, repo)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, fmt.Sprintf("failed syncing repository: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// CreateRepoFileHandler commits one file into a repository (task 3.7).
//
// This is what makes the API an *editor* for the files git owns (§4.5) rather
// than a reader of them, and it is the only way to change a job's definition -
// there is no endpoint that edits a job row's manifest-derived columns.
//
// The commit is attributed to the calling principal, so a script that appeared
// through the API is traceable to a token in the same way a run is.
//
// A sync follows immediately, because a commit that does not show up in the
// job list until someone remembers to scan is a footgun: the caller would see
// their upload succeed and the job still be missing or stale.
func (s *APIServer) CreateRepoFileHandler(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "no principal in request context")
		return
	}

	repo, ok := s.lookupRepo(w, r)
	if !ok {
		return
	}

	var req repoFileRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUploadBytes)).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body, or the file is larger than 1MiB")
		return
	}

	filePath := strings.TrimSpace(req.Path)
	if filePath == "" {
		writeProblem(w, http.StatusBadRequest, "path is required")
		return
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = fmt.Sprintf("Update %s", filePath)
	}

	repository, err := s.repos.Open(repo.Name)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, fmt.Sprintf("failed opening repository %s", repo.Name))
		return
	}

	sha, err := repository.CommitFile(
		repo.DefaultBranch,
		filePath,
		[]byte(req.Content),
		gitrepo.Author{
			Name: principal.Name,
			// Synthetic, and marked as such by the .local suffix: a principal
			// is a token, not a mailbox. It exists because git requires an
			// author email, not because anyone should write to it.
			Email: fmt.Sprintf("%s@descendence.local", principal.Name),
		},
		message,
	)
	if err != nil {
		// A path that escapes the repository is the caller's mistake, and
		// gitrepo rejects it before touching anything.
		writeProblem(w, http.StatusBadRequest, fmt.Sprintf("failed committing %s: %v", filePath, err))
		return
	}

	response := repoFileResponse{Path: filePath, CommitSHA: sha}

	// Reload: the sync writes last_synced_* onto the row we already hold.
	result, syncErr := jobsync.Sync(r.Context(), s.queries, s.repos, repo)
	if syncErr != nil {
		// The commit landed; only the projection is behind. Say so rather
		// than implying the upload failed, because retrying the upload would
		// commit the same file a second time.
		writeProblem(w, http.StatusInternalServerError, fmt.Sprintf(
			"committed %s as %s, but rebuilding the job list failed: %v - re-sync the repository",
			filePath, shortSHA(sha), syncErr))
		return
	}
	response.Sync = &result

	w.Header().Set("Location", fmt.Sprintf("/api/v1/repos/%d", repo.ID))
	writeJSON(w, http.StatusCreated, response)
}

// lookupRepo resolves the {id} path value, answering 404 itself when it cannot.
func (s *APIServer) lookupRepo(w http.ResponseWriter, r *http.Request) (store.Repo, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no repository with this id")
		return store.Repo{}, false
	}

	repo, err := s.queries.GetRepo(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusNotFound, "no repository with this id")
			return store.Repo{}, false
		}
		writeProblem(w, http.StatusInternalServerError, "failed loading repository")
		return store.Repo{}, false
	}
	return repo, true
}
