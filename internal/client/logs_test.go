package client

import (
	"errors"
	"strings"
	"testing"
)

// The decoder's job is to turn the server's wire format back into lines and
// states, and to know the difference between a stream that ended and a run
// that ended. Everything below is a pure function of its input, so this is
// where the format is pinned down rather than in an integration test.
func TestDecodeEventStream(t *testing.T) {
	const (
		line1 = `{"seq":1,"stream":"stdout","ts":"2026-08-05T12:00:00Z","text":"one"}`
		line2 = `{"seq":2,"stream":"stderr","ts":"2026-08-05T12:00:01Z","text":"two"}`
	)

	t.Run("delivers lines then ends on a terminal state", func(t *testing.T) {
		stream := "retry: 3000\n\n" +
			"event: state\ndata: {\"runState\":\"running\"}\n\n" +
			"id: 1\nevent: log\ndata: " + line1 + "\n\n" +
			": keep-alive\n\n" +
			"id: 2\nevent: log\ndata: " + line2 + "\n\n" +
			"event: state\ndata: {\"runState\":\"succeeded\"}\n\n"

		var lines []LogLine
		var states []string
		var progress int64

		err := decodeEventStream(strings.NewReader(stream), LogStream{
			OnLine:  func(l LogLine) { lines = append(lines, l) },
			OnState: func(s string) { states = append(states, s) },
		}, &progress)
		if err != nil {
			t.Fatalf("decodeEventStream: %v", err)
		}

		if len(lines) != 2 {
			t.Fatalf("got %d lines, want 2", len(lines))
		}
		if lines[0].Text != "one" || lines[0].Stream != "stdout" || lines[0].Seq != 1 {
			t.Errorf("line 1 = %+v", lines[0])
		}
		if lines[1].Text != "two" || lines[1].Stream != "stderr" {
			t.Errorf("line 2 = %+v", lines[1])
		}
		if want := []string{"running", "succeeded"}; !equal(states, want) {
			t.Errorf("states = %v, want %v", states, want)
		}
		if progress != 2 {
			t.Errorf("resume position = %d, want 2", progress)
		}
	})

	// The distinction the whole reconnect strategy rests on: a stream that
	// stops without a terminal state has not answered the question, so the
	// caller must resume rather than conclude the run is over.
	t.Run("a truncated stream is an error, not an ending", func(t *testing.T) {
		stream := "id: 1\nevent: log\ndata: " + line1 + "\n\n"

		var progress int64
		err := decodeEventStream(strings.NewReader(stream), LogStream{}, &progress)

		if !errors.Is(err, errStreamEndedEarly) {
			t.Errorf("err = %v, want errStreamEndedEarly", err)
		}
		// And it has to say how far it got, or the resume would replay.
		if progress != 1 {
			t.Errorf("resume position = %d, want 1 - a reconnect would repeat delivered lines", progress)
		}
	})

	// A non-terminal state is not an ending either: a stream opened on a
	// queued run reports "queued" straight away and keeps going.
	t.Run("a non-terminal state does not end the stream", func(t *testing.T) {
		stream := "event: state\ndata: {\"runState\":\"queued\"}\n\n"

		var progress int64
		err := decodeEventStream(strings.NewReader(stream), LogStream{}, &progress)

		if !errors.Is(err, errStreamEndedEarly) {
			t.Errorf("err = %v, want the stream to be treated as unfinished", err)
		}
	})

	t.Run("every terminal state ends the stream", func(t *testing.T) {
		for _, state := range []string{StateSucceeded, StateFailed, StateCancelled, StateLost} {
			var progress int64
			err := decodeEventStream(
				strings.NewReader("event: state\ndata: {\"runState\":\""+state+"\"}\n\n"),
				LogStream{}, &progress)
			if err != nil {
				t.Errorf("state %q did not end the stream: %v", state, err)
			}
		}
	})

	// Text is JSON-encoded precisely so a line containing a newline cannot
	// split into two SSE fields and silently become two lines.
	t.Run("a line containing a newline survives intact", func(t *testing.T) {
		var lines []LogLine
		var progress int64

		err := decodeEventStream(strings.NewReader(
			"event: log\ndata: {\"seq\":1,\"stream\":\"stdout\",\"text\":\"a\\nb\"}\n\n"+
				"event: state\ndata: {\"runState\":\"succeeded\"}\n\n"),
			LogStream{OnLine: func(l LogLine) { lines = append(lines, l) }}, &progress)
		if err != nil {
			t.Fatalf("decodeEventStream: %v", err)
		}
		if len(lines) != 1 || lines[0].Text != "a\nb" {
			t.Errorf("got %+v, want a single line %q", lines, "a\nb")
		}
	})

	t.Run("malformed data is reported", func(t *testing.T) {
		var progress int64
		err := decodeEventStream(strings.NewReader("event: log\ndata: not json\n\n"), LogStream{}, &progress)
		if err == nil || errors.Is(err, errStreamEndedEarly) {
			t.Errorf("err = %v, want a decoding error", err)
		}
	})
}

// Retrying a 404 forever would be a busy loop against a run that will never
// exist; retrying a dropped connection is the entire point of reconnecting.
func TestIsFatalStreamError(t *testing.T) {
	cases := map[int]bool{
		400: true, 401: true, 403: true, 404: true, 410: true,
		500: false, 502: false, 503: false,
	}

	for status, want := range cases {
		if got := isFatalStreamError(&APIError{StatusCode: status}); got != want {
			t.Errorf("isFatalStreamError(%d) = %v, want %v", status, got, want)
		}
	}

	if isFatalStreamError(errStreamEndedEarly) {
		t.Error("a truncated stream was treated as fatal; it is the case reconnecting exists for")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
