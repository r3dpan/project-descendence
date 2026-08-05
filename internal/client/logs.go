package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// reconnectDelay is how long a broken stream waits before resuming.
//
// Matches the `retry:` the server advertises to browsers, so every client
// reconnects on the same cadence whatever it is written in.
const reconnectDelay = 3 * time.Second

// LogLine is one line of a run's output - the RunLogLine schema in
// api/openapi.yaml.
type LogLine struct {
	Seq    int64     `json:"seq"`
	Stream string    `json:"stream"`
	Ts     time.Time `json:"ts"`
	Text   string    `json:"text"`
}

// LogPage is one page of history - the RunLogList schema.
type LogPage struct {
	Items     []LogLine `json:"items"`
	NextAfter *int64    `json:"nextAfter"`
	RunState  string    `json:"runState"`
}

// GetRunLogs fetches one page of a run's output, starting strictly after the
// given sequence number. Pass 0 for the beginning.
//
// A run whose output has passed the retention window returns an *APIError
// with StatusGone - distinct from an empty page, which means the run really
// did print nothing.
func (c *Client) GetRunLogs(ctx context.Context, id int64, after int64, limit int32) (LogPage, error) {
	query := make(map[string][]string)
	if after > 0 {
		query["after"] = []string{strconv.FormatInt(after, 10)}
	}
	if limit > 0 {
		query["limit"] = []string{strconv.FormatInt(int64(limit), 10)}
	}

	var page LogPage
	err := c.do(ctx, http.MethodGet, "/api/v1/runs/"+strconv.FormatInt(id, 10)+"/logs",
		requestOptions{query: query}, &page)

	return page, err
}

// LogStream is what a caller wants told about a run it is following. Either
// callback may be nil.
type LogStream struct {
	// OnLine is called once per line of output, in sequence order.
	OnLine func(LogLine)
	// OnState is called with the run's state when the stream opens and on
	// every change after that, ending with a terminal one.
	OnState func(state string)
}

// FollowRunLogs streams a run's output until it reaches a terminal state,
// starting strictly after the given sequence number (task 2.9).
//
// It reconnects by itself. A stream can end early for reasons that have
// nothing to do with the run - the API restarting, a proxy timing out, a
// laptop's network dropping - and the server distinguishes those from a real
// ending precisely so a client can tell them apart: a terminal `state` event
// is the defined end, and anything else is a cue to resume. Resuming from the
// last sequence number seen loses and repeats nothing (task 2.6), so
// reconnecting is invisible to the caller apart from the pause.
//
// Errors that will not improve by retrying - an unknown run, a bad token,
// output past its retention window - are returned rather than retried
// forever.
func (c *Client) FollowRunLogs(ctx context.Context, id int64, after int64, handler LogStream) error {
	for {
		err := c.followOnce(ctx, id, after, handler, &after)

		switch {
		case err == nil:
			// Ended with a terminal state: the run is over.
			return nil
		case ctx.Err() != nil:
			return ctx.Err()
		case isFatalStreamError(err):
			return err
		}

		// Anything else is a broken connection rather than an answer. Wait,
		// then resume from where this attempt got to.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(reconnectDelay):
		}
	}
}

// isFatalStreamError reports whether retrying is pointless. Everything else -
// a dropped connection, a timeout, a 5xx - is worth another attempt.
func isFatalStreamError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone, http.StatusBadRequest:
		return true
	default:
		return false
	}
}

// followOnce runs one connection's worth of streaming. It returns the
// terminal state and a nil error when the run ended, and advances *after past
// every line it delivered so a reconnect resumes correctly even when the
// connection broke mid-run.
func (c *Client) followOnce(ctx context.Context, id int64, after int64, handler LogStream, progress *int64) error {
	header := http.Header{}
	header.Set("Accept", "text/event-stream")
	if after > 0 {
		header.Set("Last-Event-ID", strconv.FormatInt(after, 10))
	}

	// streamClient, never httpClient: a run may take an hour, and the
	// ordinary client's blanket timeout would cut the stream off partway
	// through and call it a network error. This is the same mistake found in
	// task 1.19 and again in 2.1, in a third place.
	resp, err := c.send(ctx, c.streamClient, http.MethodGet,
		"/api/v1/runs/"+strconv.FormatInt(id, 10)+"/logs", requestOptions{header: header})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}

	return decodeEventStream(resp.Body, handler, progress)
}

// decodeEventStream parses server-sent events off r until a terminal state
// arrives or the stream ends.
//
// The format is deliberately simple to read: `field: value` lines, a blank
// line ending each message, and `:` starting a comment. Only the fields this
// API actually sends are handled - a general-purpose SSE parser would also
// have to deal with multi-line data and `retry:` changes mid-stream, neither
// of which happens here.
func decodeEventStream(r io.Reader, handler LogStream, progress *int64) error {
	scanner := bufio.NewScanner(r)
	// Log lines can be long; the default 64KB limit would turn one into an
	// error rather than a line.
	scanner.Buffer(make([]byte, 0, 64*1024), maxEventSize)

	var event, data string

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case line == "":
			// End of a message: act on what was collected.
			done, err := dispatchEvent(event, data, handler, progress)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			event, data = "", ""

		case strings.HasPrefix(line, ":"):
			// A comment - the keep-alive heartbeat. Its only job is to have
			// arrived.

		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")

		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")

			// id: and retry: are deliberately ignored. The id always equals
			// the line's own seq, which is read from the data, and tracking
			// the resume point from the data means it cannot drift from what
			// was actually delivered to the caller.
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading event stream: %w", err)
	}

	// The stream ended without a terminal state: the connection broke rather
	// than the run finishing. Reported as an error so FollowRunLogs reconnects.
	return errStreamEndedEarly
}

// maxEventSize bounds one SSE message. Generous next to any real log line,
// and small enough that a desynchronised stream fails rather than allocating
// without limit.
const maxEventSize = 4 * 1024 * 1024

var errStreamEndedEarly = errors.New("event stream ended before the run did")

// dispatchEvent turns one decoded message into a callback, reporting whether
// it was the stream's defined ending.
func dispatchEvent(event, data string, handler LogStream, progress *int64) (done bool, err error) {
	switch event {
	case "log":
		var line LogLine
		if err := json.Unmarshal([]byte(data), &line); err != nil {
			return false, fmt.Errorf("decoding log event: %w", err)
		}
		if handler.OnLine != nil {
			handler.OnLine(line)
		}
		// Advanced only after the caller has been told, so a connection that
		// breaks here resumes at the line the caller actually saw.
		*progress = line.Seq

	case "state":
		var payload struct {
			RunState string `json:"runState"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return false, fmt.Errorf("decoding state event: %w", err)
		}
		if handler.OnState != nil {
			handler.OnState(payload.RunState)
		}
		if IsTerminalState(payload.RunState) {
			return true, nil
		}
	}

	return false, nil
}
