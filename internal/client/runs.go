package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Run states, as constrained by the Run schema's enum in api/openapi.yaml.
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	StateLost      = "lost"
)

// Run mirrors the Run schema. Fields the server may omit or send as null
// (everything not in the schema's `required` list) are pointers, so "absent"
// is distinguishable from "zero" - an exitCode of 0 means success, and must
// not be confused with a run that hasn't finished.
type Run struct {
	ID             int64    `json:"id"`
	State          string   `json:"state"`
	ImageRef       string   `json:"imageRef"`
	Argv           []string `json:"argv"`
	TimeoutSeconds int32    `json:"timeoutSeconds"`
	ContainerID    *string  `json:"containerId"`
	ExitCode       *int32   `json:"exitCode"`
	FailureReason  *string  `json:"failureReason"`
	// Set as soon as cancellation is requested, which is before the run is
	// actually cancelled - a running run's container still has to be stopped.
	// A run with this set and State still "running" is on its way out.
	CancelRequestedAt *time.Time `json:"cancelRequestedAt"`
	QueuedAt          time.Time  `json:"queuedAt"`
	StartedAt         *time.Time `json:"startedAt"`
	FinishedAt        *time.Time `json:"finishedAt"`

	// Both nil for an ad-hoc run, both set for a job run. Together they are
	// what makes a run explainable: check out CommitSHA and the job's
	// manifest names exactly the script that executed.
	JobID     *int64  `json:"jobId"`
	CommitSHA *string `json:"commitSha"`

	// Both nil unless the job named a runtime rather than an image directly
	// (task 4.6). Set once, at creation - rebuilding the runtime afterwards
	// never changes ImageDigest on a run that already exists.
	RuntimeID   *int64  `json:"runtimeId"`
	ImageDigest *string `json:"imageDigest"`
}

// IsJobRun reports whether this run came from a job rather than an ad-hoc
// image and argv.
func (r Run) IsJobRun() bool { return r.JobID != nil }

// IsTerminal reports whether the run has reached a state it will never leave -
// the condition `cli run` polls until (task 1.19).
func (r Run) IsTerminal() bool {
	return IsTerminalState(r.State)
}

// IsTerminalState reports whether a state string is one a run never leaves.
// Same list as the server's store.IsTerminal, which a test checks.
func IsTerminalState(state string) bool {
	switch state {
	case StateSucceeded, StateFailed, StateCancelled, StateLost:
		return true
	default:
		return false
	}
}

// RunList mirrors the RunList schema. NextCursor is nil on the last page.
type RunList struct {
	Items      []Run   `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// CreateRunParams mirrors the RunCreate schema, plus the Idempotency-Key
// header the endpoint accepts.
type CreateRunParams struct {
	ImageRef string
	Argv     []string
	// TimeoutSeconds is omitted from the request when zero, letting the
	// server apply its own default rather than this client duplicating it.
	TimeoutSeconds int32
	// IdempotencyKey, when set, is sent as the Idempotency-Key header:
	// retrying with the same key returns the original run instead of
	// queueing a second one.
	IdempotencyKey string
}

// runCreateBody is the wire shape of CreateRunParams. Separate from the
// exported struct so the header field doesn't leak into the JSON body and
// timeoutSeconds can be genuinely omitted.
type runCreateBody struct {
	ImageRef       string   `json:"imageRef"`
	Argv           []string `json:"argv"`
	TimeoutSeconds *int32   `json:"timeoutSeconds,omitempty"`
}

// CreateRun calls POST /api/v1/runs. It returns as soon as the run is queued
// (the server answers 202 and never blocks on the container) - poll GetRun
// for the outcome.
func (c *Client) CreateRun(ctx context.Context, params CreateRunParams) (Run, error) {
	body := runCreateBody{
		ImageRef: params.ImageRef,
		Argv:     params.Argv,
	}
	if params.TimeoutSeconds > 0 {
		body.TimeoutSeconds = &params.TimeoutSeconds
	}

	opts := requestOptions{body: body}
	if params.IdempotencyKey != "" {
		opts.header = http.Header{"Idempotency-Key": []string{params.IdempotencyKey}}
	}

	var run Run
	err := c.do(ctx, http.MethodPost, "/api/v1/runs", opts, &run)
	return run, err
}

// GetRun calls GET /api/v1/runs/{id}. An unknown id is an *APIError matching
// errors.Is(err, ErrNotFound).
func (c *Client) GetRun(ctx context.Context, id int64) (Run, error) {
	var run Run
	err := c.do(ctx, http.MethodGet, "/api/v1/runs/"+strconv.FormatInt(id, 10), requestOptions{}, &run)
	return run, err
}

// ListRunsParams are the query parameters of GET /api/v1/runs. Both are
// optional; the zero value asks for the first page at the server's default
// page size.
type ListRunsParams struct {
	// Cursor is an opaque value from a previous response's NextCursor.
	// Never construct one.
	Cursor string
	Limit  int32
}

// ListRuns calls GET /api/v1/runs, returning one keyset-paginated page,
// newest first. Follow RunList.NextCursor for the next page; a nil cursor
// means this was the last one.
// CancelRun requests cancellation of a run and returns the run as it stands
// afterwards (task 2.8).
//
// The returned run's State is what says whether the cancellation has already
// happened: a queued run comes back "cancelled" and is finished, while a
// running one comes back still "running" with CancelRequestedAt set, because
// only the supervisor can stop a container and it has not done so yet. Poll or
// stream for the outcome in that case.
//
// A run that has already finished returns an APIError with StatusConflict -
// terminal states are final, so there is nothing left to cancel.
func (c *Client) CancelRun(ctx context.Context, id int64) (Run, error) {
	var run Run
	err := c.do(ctx, http.MethodPost, "/api/v1/runs/"+strconv.FormatInt(id, 10)+"/cancel", requestOptions{}, &run)
	return run, err
}

func (c *Client) ListRuns(ctx context.Context, params ListRunsParams) (RunList, error) {
	query := url.Values{}
	if params.Cursor != "" {
		query.Set("cursor", params.Cursor)
	}
	if params.Limit > 0 {
		query.Set("limit", strconv.FormatInt(int64(params.Limit), 10))
	}

	var list RunList
	err := c.do(ctx, http.MethodGet, "/api/v1/runs", requestOptions{query: query}, &list)
	return list, err
}

// PollRun polls GET /api/v1/runs/{id} every interval until the run reaches a
// terminal state, ctx is cancelled, or a request fails. onUpdate, if
// non-nil, is called with every observed run - including the first and the
// terminal one - so a caller can render progress as it changes.
//
// Transport errors are returned rather than retried: the CLI would rather
// say "the API went away" than silently hang (task 1.23 deliberately kills
// the API mid-poll, and the CLI's behaviour there should be legible).
func (c *Client) PollRun(ctx context.Context, id int64, interval time.Duration, onUpdate func(Run)) (Run, error) {
	if interval <= 0 {
		return Run{}, fmt.Errorf("poll interval must be positive")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		run, err := c.GetRun(ctx, id)
		if err != nil {
			return Run{}, err
		}
		if onUpdate != nil {
			onUpdate(run)
		}
		if run.IsTerminal() {
			return run, nil
		}

		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-ticker.C:
		}
	}
}
