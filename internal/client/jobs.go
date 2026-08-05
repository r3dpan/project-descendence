package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Job mirrors the Job schema. Every field but Enabled is owned by the
// manifest in git and rewritten by the next sync; there is deliberately no
// method here that changes any of them, because the only way to change a job
// is to commit a manifest (CreateRepoFile) and re-sync.
type Job struct {
	ID           int64   `json:"id"`
	RepoID       int64   `json:"repoId"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	ManifestPath string  `json:"manifestPath"`
	ScriptPath   string  `json:"scriptPath"`
	// Nil in the usual case, where argv is the script's own path and its
	// shebang chooses the interpreter.
	Command         []string   `json:"command"`
	ImageRef        *string    `json:"imageRef"`
	TimeoutSeconds  *int32     `json:"timeoutSeconds"`
	Params          []JobParam `json:"params"`
	Enabled         bool       `json:"enabled"`
	SyncedCommitSHA string     `json:"syncedCommitSha"`
	// Set when the manifest has been removed from the repository. Such a job
	// cannot be run, and is not returned by ListJobs - but it still exists,
	// so the runs that used it stay explainable.
	DeletedAt *string `json:"deletedAt"`
}

// IsDeleted reports whether the job's manifest has been removed from git.
func (j Job) IsDeleted() bool { return j.DeletedAt != nil }

// JobParam mirrors the JobParam schema - one entry of a job's parameter
// contract (task 6.1).
type JobParam struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Required bool    `json:"required"`
	Default  *string `json:"default"`
	Secret   bool    `json:"secret"`
}

type JobList struct {
	Items      []Job   `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// Repo mirrors the Repo schema.
type Repo struct {
	ID                  int64   `json:"id"`
	Name                string  `json:"name"`
	Path                string  `json:"path"`
	Kind                string  `json:"kind"`
	DefaultBranch       string  `json:"defaultBranch"`
	RemoteURL           *string `json:"remoteUrl"`
	LastSyncedAt        *string `json:"lastSyncedAt"`
	LastSyncedCommitSHA *string `json:"lastSyncedCommitSha"`
}

type RepoList struct {
	Items      []Repo  `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// SyncManifestError is one manifest a scan could not turn into a job.
type SyncManifestError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// SyncResult is what a scan did. A sync that reports errors still succeeded
// for every other manifest, which is why this is a normal response body and
// not an error.
type SyncResult struct {
	CommitSHA string              `json:"commitSha"`
	Added     []string            `json:"added"`
	Updated   []string            `json:"updated"`
	Removed   []string            `json:"removed"`
	Errors    []SyncManifestError `json:"errors"`
}

// RepoFileResult is the outcome of committing a file, including the sync that
// followed it.
type RepoFileResult struct {
	Path      string      `json:"path"`
	CommitSHA string      `json:"commitSha"`
	Sync      *SyncResult `json:"sync"`
}

// --- Parameter structs ---

type ListJobsParams struct {
	Cursor string
	Limit  int32
}

type ListReposParams struct {
	Cursor string
	Limit  int32
}

type CreateRepoParams struct {
	Name string `json:"name"`
	// Empty means the server's default.
	DefaultBranch string `json:"defaultBranch,omitempty"`
}

type CreateRepoFileParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Message string `json:"message,omitempty"`
}

// CreateJobRunParams carries no execution detail beyond parameter values -
// what to run is still the manifest's business. IdempotencyKey is a header,
// not a body field, which is why it's not part of createJobRunBody below.
//
// Params holds raw strings, matching --param name=value on the command
// line; the server, not this client, coerces them against the job's
// contract (task 6.2) so both callers of this method agree on what counts
// as a valid number or bool.
type CreateJobRunParams struct {
	JobID          int64
	IdempotencyKey string
	Params         map[string]string
}

type createJobRunBody struct {
	Params map[string]string `json:"params,omitempty"`
}

// --- Methods ---

func (c *Client) ListJobs(ctx context.Context, params ListJobsParams) (JobList, error) {
	query := url.Values{}
	if params.Cursor != "" {
		query.Set("cursor", params.Cursor)
	}
	if params.Limit > 0 {
		query.Set("limit", strconv.FormatInt(int64(params.Limit), 10))
	}

	var list JobList
	err := c.do(ctx, http.MethodGet, "/api/v1/jobs", requestOptions{query: query}, &list)
	return list, err
}

func (c *Client) GetJob(ctx context.Context, id int64) (Job, error) {
	var job Job
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/jobs/%d", id), requestOptions{}, &job)
	return job, err
}

// GetJobByName resolves a name the way the command line does.
//
// The API has no by-name lookup - job ids are what the URLs use - so this
// pages through the list. Names are unique among live jobs, so at most one
// match exists.
func (c *Client) GetJobByName(ctx context.Context, name string) (Job, error) {
	params := ListJobsParams{Limit: 200}
	for {
		list, err := c.ListJobs(ctx, params)
		if err != nil {
			return Job{}, err
		}
		for _, job := range list.Items {
			if job.Name == name {
				return job, nil
			}
		}
		if list.NextCursor == nil {
			return Job{}, fmt.Errorf("%w: no job called %q", ErrNotFound, name)
		}
		params.Cursor = *list.NextCursor
	}
}

// SetJobEnabled pauses or resumes a job. The only field of a job the API can
// change - everything else is defined by its manifest.
func (c *Client) SetJobEnabled(ctx context.Context, id int64, enabled bool) (Job, error) {
	body := struct {
		Enabled bool `json:"enabled"`
	}{Enabled: enabled}

	var job Job
	err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/api/v1/jobs/%d", id), requestOptions{body: body}, &job)
	return job, err
}

// CreateJobRun triggers a job, returning the queued run. The server resolves
// the repository's current commit and pins it onto the run, so the Run coming
// back already names the exact commit it will execute.
func (c *Client) CreateJobRun(ctx context.Context, params CreateJobRunParams) (Run, error) {
	options := requestOptions{}
	if params.IdempotencyKey != "" {
		options.header = http.Header{}
		options.header.Set("Idempotency-Key", params.IdempotencyKey)
	}
	if len(params.Params) > 0 {
		options.body = createJobRunBody{Params: params.Params}
	}

	var run Run
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/runs", params.JobID), options, &run)
	return run, err
}

func (c *Client) ListRepos(ctx context.Context, params ListReposParams) (RepoList, error) {
	query := url.Values{}
	if params.Cursor != "" {
		query.Set("cursor", params.Cursor)
	}
	if params.Limit > 0 {
		query.Set("limit", strconv.FormatInt(int64(params.Limit), 10))
	}

	var list RepoList
	err := c.do(ctx, http.MethodGet, "/api/v1/repos", requestOptions{query: query}, &list)
	return list, err
}

func (c *Client) GetRepo(ctx context.Context, id int64) (Repo, error) {
	var repo Repo
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/repos/%d", id), requestOptions{}, &repo)
	return repo, err
}

// GetRepoByName resolves a repository name, as the command line uses.
func (c *Client) GetRepoByName(ctx context.Context, name string) (Repo, error) {
	params := ListReposParams{Limit: 200}
	for {
		list, err := c.ListRepos(ctx, params)
		if err != nil {
			return Repo{}, err
		}
		for _, repo := range list.Items {
			if repo.Name == name {
				return repo, nil
			}
		}
		if list.NextCursor == nil {
			return Repo{}, fmt.Errorf("%w: no repository called %q", ErrNotFound, name)
		}
		params.Cursor = *list.NextCursor
	}
}

func (c *Client) CreateRepo(ctx context.Context, params CreateRepoParams) (Repo, error) {
	var repo Repo
	err := c.do(ctx, http.MethodPost, "/api/v1/repos", requestOptions{body: params, alsoOK: []int{http.StatusCreated}}, &repo)
	return repo, err
}

// SyncRepo rebuilds the job list from the repository's manifests.
func (c *Client) SyncRepo(ctx context.Context, id int64) (SyncResult, error) {
	var result SyncResult
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/repos/%d/sync", id), requestOptions{}, &result)
	return result, err
}

// CreateRepoFile commits one file into a repository and re-syncs, so a job
// uploaded this way is runnable the moment the call returns.
func (c *Client) CreateRepoFile(ctx context.Context, repoID int64, params CreateRepoFileParams) (RepoFileResult, error) {
	var result RepoFileResult
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/repos/%d/files", repoID),
		requestOptions{body: params, alsoOK: []int{http.StatusCreated}}, &result)
	return result, err
}
