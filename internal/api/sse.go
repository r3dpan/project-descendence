package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Server-sent events, the wire format behind every streaming endpoint here
// (task 2.5). Deliberately hand-written like the rest of the HTTP layer - the
// format is a handful of `field: value` lines and a blank line between
// messages, and writing it directly is the point of the exercise.
//
// Reference: MDN, "Using server-sent events", and the WHATWG HTML spec's
// event stream section.

const (
	// streamWriteTimeout bounds a single write to a streaming client.
	//
	// cmd/api sets a server-wide WriteTimeout, which is a deadline on the
	// *whole* response - fine for a JSON document, fatal for a stream, which
	// it would simply cut off mid-run. The obvious fix is to clear the
	// deadline entirely for streaming handlers, and that is what PLAN.md task
	// 2.5 called for, but a cleared deadline replaces one bug with a worse
	// one: a client that stops reading without closing its connection leaves
	// the handler blocked in Write forever, holding a goroutine and a
	// subscription that nothing will ever release. TCP keepalive would notice
	// eventually - in hours.
	//
	// So the deadline is re-armed before every write instead of removed. A
	// stream lives as long as it likes; a single stalled write still dies on
	// schedule, which is what lets task 2.7's cleanup actually run.
	streamWriteTimeout = 30 * time.Second

	// eventRetry is the reconnection delay advertised to EventSource clients,
	// in milliseconds. Sent once at the start of every stream. Browsers
	// default to about three seconds; saying so explicitly means the value is
	// ours to change, and it pairs with Last-Event-ID (task 2.6) - a client
	// that reconnects on this timer resumes exactly where it stopped.
	eventRetry = 3000
)

// sseWriter encodes server-sent events onto an HTTP response.
//
// Every method re-arms the write deadline first, so a stalled client fails a
// write rather than blocking the handler. Any error from any method means the
// stream is over: the status line is long gone by then, so there is no way to
// report a failure to the client except by ending the response.
type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// newSSEWriter writes the response headers for an event stream and returns a
// writer for its messages. Call it only once every other outcome (404, 410,
// 400) has been ruled out - it commits the response to 200.
func newSSEWriter(w http.ResponseWriter) (*sseWriter, error) {
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	// Nginx buffers proxied responses by default, which for a stream means the
	// client sees nothing until the buffer fills - i.e. the live view is not
	// live. This header turns that off for the reverse proxies that honour it.
	header.Set("X-Accel-Buffering", "no")

	stream := &sseWriter{w: w, rc: http.NewResponseController(w)}

	w.WriteHeader(http.StatusOK)

	if _, err := fmt.Fprintf(w, "retry: %d\n\n", eventRetry); err != nil {
		return nil, err
	}
	if err := stream.flush(); err != nil {
		return nil, err
	}

	return stream, nil
}

// event writes one message: an optional id, an event name, and a single data
// line holding data as JSON.
//
// JSON rather than raw text for a reason beyond consistency with the rest of
// the API: a data line ends at a newline, so any payload containing one would
// silently split into two fields. Encoding removes the question - and log
// lines are the one thing on this endpoint guaranteed to contain arbitrary
// bytes from a script nobody vetted.
//
// id is omitted when empty. That is not a formality: a client stores the id of
// the last message that carried one and sends it back as Last-Event-ID, so
// giving an id to a message that is not a resumable position would corrupt the
// resume point (task 2.6).
func (s *sseWriter) event(name string, id string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding %s event: %w", name, err)
	}

	if err := s.armDeadline(); err != nil {
		return err
	}

	if id != "" {
		if _, err := fmt.Fprintf(s.w, "id: %s\n", id); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, encoded); err != nil {
		return err
	}

	return nil
}

// comment writes a `: text` line, which every client ignores.
//
// This is the standard event-stream heartbeat. It exists to be written, not to
// be read: a write to a peer that has vanished without closing is how the
// server finds out, and without traffic on an idle stream - a run that prints
// nothing for an hour - it would never find out at all.
func (s *sseWriter) comment(text string) error {
	if err := s.armDeadline(); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		return err
	}

	return s.flush()
}

// flush pushes buffered bytes to the client. Required after every message:
// without it the response sits in net/http's buffer and the stream is not a
// stream.
func (s *sseWriter) flush() error {
	return s.rc.Flush()
}

// armDeadline gives the next write streamWriteTimeout to complete, replacing
// whatever the server-wide WriteTimeout left in place.
func (s *sseWriter) armDeadline() error {
	return s.rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
}
