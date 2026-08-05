package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Runtime mirrors the Runtime schema (task 4.8). Unlike Job, every field
// here is owned by Postgres directly rather than by a manifest in git - a
// runtime is a built artifact, not a projection (ARCHITECTURE.md §4.4).
type Runtime struct {
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

type RuntimeList struct {
	Items      []Runtime `json:"items"`
	NextCursor *string   `json:"nextCursor"`
}

type ListRuntimesParams struct {
	Cursor string
	Limit  int32
}

type CreateRuntimeParams struct {
	Name         string   `json:"name"`
	BaseImage    string   `json:"baseImage,omitempty"`
	SysPackages  []string `json:"sysPackages,omitempty"`
	Lang         string   `json:"lang"`
	LangManifest string   `json:"langManifest,omitempty"`
}

// PruneRuntimesParams is exactly one of IDs or OlderThanDays - the same
// either/or the server enforces (PruneRuntimesHandler).
type PruneRuntimesParams struct {
	IDs           []int64
	OlderThanDays *int
}

type PruneRuntimesResult struct {
	Pruned  []string `json:"pruned"`
	Skipped []string `json:"skipped"`
	Errors  []string `json:"errors"`
}

func (c *Client) ListRuntimes(ctx context.Context, params ListRuntimesParams) (RuntimeList, error) {
	query := url.Values{}
	if params.Cursor != "" {
		query.Set("cursor", params.Cursor)
	}
	if params.Limit > 0 {
		query.Set("limit", strconv.FormatInt(int64(params.Limit), 10))
	}

	var list RuntimeList
	err := c.do(ctx, http.MethodGet, "/api/v1/runtimes", requestOptions{query: query}, &list)
	return list, err
}

func (c *Client) GetRuntime(ctx context.Context, id int64) (Runtime, error) {
	var runtime Runtime
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/runtimes/%d", id), requestOptions{}, &runtime)
	return runtime, err
}

// GetRuntimeByName resolves a name the way the command line does - the API
// has no by-name lookup, so this pages through the list, the same pattern as
// GetJobByName.
func (c *Client) GetRuntimeByName(ctx context.Context, name string) (Runtime, error) {
	params := ListRuntimesParams{Limit: 200}
	for {
		list, err := c.ListRuntimes(ctx, params)
		if err != nil {
			return Runtime{}, err
		}
		for _, runtime := range list.Items {
			if runtime.Name == name {
				return runtime, nil
			}
		}
		if list.NextCursor == nil {
			return Runtime{}, fmt.Errorf("%w: no runtime called %q", ErrNotFound, name)
		}
		params.Cursor = *list.NextCursor
	}
}

// CreateRuntime defines a runtime, which queues its first build immediately
// - the returned Runtime's BuildStatus is "pending" the moment this call
// returns.
func (c *Client) CreateRuntime(ctx context.Context, params CreateRuntimeParams) (Runtime, error) {
	var runtime Runtime
	err := c.do(ctx, http.MethodPost, "/api/v1/runtimes", requestOptions{body: params}, &runtime)
	return runtime, err
}

// BuildRuntime queues a rebuild of an existing runtime.
func (c *Client) BuildRuntime(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/runtimes/%d/build", id), requestOptions{}, nil)
}

// PruneRuntimes reclaims runtime image storage - see PruneRuntimesParams.
func (c *Client) PruneRuntimes(ctx context.Context, params PruneRuntimesParams) (PruneRuntimesResult, error) {
	body := struct {
		IDs           []int64 `json:"ids,omitempty"`
		OlderThanDays *int    `json:"olderThanDays,omitempty"`
	}{IDs: params.IDs, OlderThanDays: params.OlderThanDays}

	var result PruneRuntimesResult
	err := c.do(ctx, http.MethodPost, "/api/v1/runtimes/prune", requestOptions{body: body}, &result)
	return result, err
}
