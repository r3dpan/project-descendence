package podman

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrImageNotPresent reports that a container could not be created because its
// image is not in local storage. Distinguished from every other create failure
// so the supervisor can pull and retry exactly once, rather than treating a
// missing image as a permanently failed run.
var ErrImageNotPresent = errors.New("image not present locally")

// IsImageNotPresent reports whether err is libpod's "no such image".
//
// Matched on the message text because libpod answers a create for a missing
// image with a 404 whose body is its ordinary error shape - there is no
// machine-readable code to key on. Kept in one place so the string matching is
// auditable rather than scattered.
func IsImageNotPresent(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrImageNotPresent) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such image") || strings.Contains(message, "image not known")
}

// pullProgress is one line of libpod's pull stream. The stream ends with a
// line whose status is "success"; a failure that happens after the response
// headers have gone out arrives as an "error" field rather than a status code.
type pullProgress struct {
	Status string   `json:"status"`
	Stream string   `json:"stream"`
	Error  string   `json:"error"`
	Images []string `json:"images"`
	ID     string   `json:"id"`
}

// PullImage fetches an image into local storage
// (POST /libpod/images/pull) and returns its id.
//
// Deliberately minimal: no digest resolution, no authentication, no policy
// about when to re-pull. Runs pin image digests from Phase 4 (tasks 4.4/4.6);
// this exists only so that the first run of a job on a fresh machine is not an
// opaque "no such image" from container create.
//
// On longPollClient, not httpClient. Pulling a multi-hundred-megabyte image
// over a slow link takes far longer than the 10s request timeout, and a
// blanket timeout on a long-lived response is a bug this project has now shot
// itself with three times (podman /wait at task 1.19, podman log follow at
// 2.1, the API client's log follow at 2.9). HISTORY predicted the fourth
// instance would be "whatever long-lived endpoint comes next". This is it.
func (c *Client) PullImage(ctx context.Context, reference string) (string, error) {
	if reference == "" {
		return "", fmt.Errorf("podman: image reference is empty")
	}

	endpoint := "/libpod/images/pull?reference=" + url.QueryEscape(reference)

	resp, err := c.doRaw(ctx, c.longPollClient, http.MethodPost, endpoint, "", nil)
	if err != nil {
		return "", fmt.Errorf("podman: pulling %s: %w", reference, err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp, "pull image "+reference, http.StatusOK); err != nil {
		return "", err
	}

	// A 200 only means the pull started. The outcome is at the end of the
	// stream, so the body has to be read to completion - abandoning it early
	// would report a failed pull as a success.
	decoder := json.NewDecoder(resp.Body)
	imageID := ""
	for {
		var progress pullProgress
		if err := decoder.Decode(&progress); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("podman: reading pull progress for %s: %w", reference, err)
		}

		if progress.Error != "" {
			return "", fmt.Errorf("podman: pulling %s: %s", reference, progress.Error)
		}
		if progress.ID != "" {
			imageID = progress.ID
		}
		if len(progress.Images) > 0 && imageID == "" {
			imageID = progress.Images[0]
		}
	}

	if imageID == "" {
		return "", fmt.Errorf("podman: pulling %s: the stream ended without reporting an image", reference)
	}

	return imageID, nil
}

// ImageInspect is the subset of GET /libpod/images/{name}/json this codebase
// uses: enough to resolve a tag to the digest a run pins (task 4.6).
type ImageInspect struct {
	ID          string   `json:"Id"`
	RepoDigests []string `json:"RepoDigests"`
}

// InspectImage resolves nameOrID (a tag or an id) to its full record,
// notably RepoDigests - a locally built image's repo digest, since nothing
// pushed it, is scoped to this host's storage rather than a registry, but it
// is still the stable, content-addressed identifier a run should pin rather
// than a tag that a later build can silently move.
func (c *Client) InspectImage(ctx context.Context, nameOrID string) (ImageInspect, error) {
	if nameOrID == "" {
		return ImageInspect{}, fmt.Errorf("podman: image name is empty")
	}

	resp, err := c.do(ctx, http.MethodGet, "/libpod/images/"+url.PathEscape(nameOrID)+"/json", nil)
	if err != nil {
		return ImageInspect{}, fmt.Errorf("podman: inspecting %s: %w", nameOrID, err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp, "inspect image "+nameOrID, http.StatusOK); err != nil {
		return ImageInspect{}, err
	}

	var inspect ImageInspect
	if err := json.NewDecoder(resp.Body).Decode(&inspect); err != nil {
		return ImageInspect{}, fmt.Errorf("podman: decoding inspect of %s: %w", nameOrID, err)
	}
	return inspect, nil
}

// DeleteImage removes an image from local storage (DELETE
// /libpod/images/{name}), force=true so an image that still has a stopped
// container referencing it (e.g. one the reconciler hasn't reaped yet) is
// removed anyway - the prune sweep (task 4.7) already only selects images no
// run has referenced recently, so force here is cleanup, not data loss.
// A missing image is not an error: pruning is idempotent.
func (c *Client) DeleteImage(ctx context.Context, nameOrID string) error {
	if nameOrID == "" {
		return fmt.Errorf("podman: image name is empty")
	}

	resp, err := c.do(ctx, http.MethodDelete, "/libpod/images/"+url.PathEscape(nameOrID)+"?force=true", nil)
	if err != nil {
		return fmt.Errorf("podman: deleting image %s: %w", nameOrID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return checkStatus(resp, "delete image "+nameOrID, http.StatusOK)
}
