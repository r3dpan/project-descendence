package podman

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// buildProgress is one line of libpod's build stream. Shaped like Docker's
// build API (libpod imitates it here): "stream" carries human-readable
// progress text, "error"/"errormsg" carries a failure that arrives after the
// response headers are already sent - the same "200 just means it started"
// shape as PullImage's pullProgress.
type buildProgress struct {
	Stream   string `json:"stream"`
	Error    string `json:"error"`
	ErrorMsg string `json:"errorMsg"`
}

// BuildImage builds contextTar (a tar stream containing a Containerfile and
// whatever it COPYs in - see internal/runtimebuild.BuildContext) and tags
// the result as tag (POST /libpod/build).
//
// Returns only an error, not an image id: the id/digest a caller actually
// wants is read back afterwards via InspectImage(ctx, tag), which is also
// how a caller resolves the digest a run pins (task 4.6) - one lookup path
// instead of two ways of learning the same fact.
//
// On longPollClient, not httpClient, for the same reason as PullImage: a
// build can run for minutes, and a blanket request timeout would report a
// perfectly healthy build as an infrastructure failure.
func (c *Client) BuildImage(ctx context.Context, tag string, contextTar io.Reader) error {
	if tag == "" {
		return fmt.Errorf("podman: build tag is empty")
	}

	query := url.Values{}
	query.Set("t", tag)
	query.Set("dockerfile", "Containerfile")
	endpoint := "/libpod/build?" + query.Encode()

	resp, err := c.doRaw(ctx, c.longPollClient, http.MethodPost, endpoint, "application/x-tar", contextTar)
	if err != nil {
		return fmt.Errorf("podman: building %s: %w", tag, err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp, "build image "+tag, http.StatusOK); err != nil {
		return err
	}

	// As with PullImage, a 200 only means the build started; the body must be
	// drained to completion to see whether it actually succeeded.
	decoder := json.NewDecoder(resp.Body)
	for {
		var progress buildProgress
		if err := decoder.Decode(&progress); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("podman: reading build progress for %s: %w", tag, err)
		}
		if progress.Error != "" {
			return fmt.Errorf("podman: building %s: %s", tag, progress.Error)
		}
		if progress.ErrorMsg != "" {
			return fmt.Errorf("podman: building %s: %s", tag, progress.ErrorMsg)
		}
	}

	return nil
}
