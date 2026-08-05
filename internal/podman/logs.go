package podman

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// The two output streams a container log frame can carry. These strings are
// also what goes into run_logs.stream, so they match that column's CHECK
// constraint exactly.
const (
	StreamStdout = "stdout"
	StreamStderr = "stderr"
)

// Header byte values libpod uses to tag a frame's stream. 0 (stdin) exists in
// the format but never appears in a log stream.
const (
	frameStdout = 1
	frameStderr = 2
)

// frameHeaderSize is the fixed 8-byte prefix on every frame: one stream-type
// byte, three zero bytes, then a big-endian uint32 payload length.
const frameHeaderSize = 8

// maxFrameSize bounds a single frame's payload. Real frames are a line or two
// of text; anything this large means the stream has desynchronised, and
// trusting the length field at that point would allocate whatever it says.
const maxFrameSize = 1 << 20

// LogFrame is one demultiplexed chunk of container output. Data is a raw
// slice of the container's output - it is not guaranteed to be a whole line,
// or only one line. Splitting is the caller's problem (internal/runlog does
// it).
type LogFrame struct {
	Stream string
	Data   []byte
}

// FollowContainerLogs calls GET /libpod/containers/{id}/logs with follow=true
// and calls fn once per frame, in order, until the container exits (the
// stream then ends on its own), ctx is cancelled, or fn returns an error -
// which is returned as-is, so a caller can stop early with a sentinel.
//
// It replays from the beginning of the container's output every time, and it
// works just as well on a container that has already exited (verified against
// the real API): everything buffered comes back and the stream closes
// immediately. That is what lets the reconciler's adoption path (task 1.15)
// recapture an interrupted run's logs from scratch rather than trying to
// resume at an offset.
//
// Like WaitContainer this uses longPollClient, and for the same reason: it
// blocks for the container's entire lifetime. Putting it on the ordinary
// httpClient would silently truncate the logs of every run lasting longer
// than requestTimeout - the exact bug shape found in task 1.19.
func (c *Client) FollowContainerLogs(ctx context.Context, id string, fn func(LogFrame) error) error {
	query := url.Values{}
	query.Set("stdout", "true")
	query.Set("stderr", "true")
	query.Set("follow", "true")

	resp, err := c.doWith(ctx, c.longPollClient, http.MethodGet, "/libpod/containers/"+id+"/logs?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp, "follow container logs", http.StatusOK); err != nil {
		return err
	}

	return demultiplex(resp.Body, fn)
}

// demultiplex decodes the Docker/libpod multiplexed stream format that a
// container created without a TTY produces: repeated [1 byte stream type][3
// zero bytes][4 byte big-endian length][length bytes payload]. Confirmed
// against the real API rather than assumed.
//
// Reaching a clean EOF on a frame boundary is the normal way this ends and is
// not an error. EOF partway through a frame is, since it means the stream was
// cut mid-message and whatever we have is incomplete.
func demultiplex(r io.Reader, fn func(LogFrame) error) error {
	header := make([]byte, frameHeaderSize)

	for {
		if _, err := io.ReadFull(r, header); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("podman: reading log frame header: %w", err)
		}

		var stream string
		switch header[0] {
		case frameStdout:
			stream = StreamStdout
		case frameStderr:
			stream = StreamStderr
		default:
			return fmt.Errorf("podman: unknown log stream type %d", header[0])
		}

		size := binary.BigEndian.Uint32(header[4:frameHeaderSize])
		if size > maxFrameSize {
			return fmt.Errorf("podman: log frame of %d bytes exceeds the %d byte limit", size, maxFrameSize)
		}

		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return fmt.Errorf("podman: reading log frame payload: %w", err)
		}

		if err := fn(LogFrame{Stream: stream, Data: payload}); err != nil {
			return err
		}
	}
}
