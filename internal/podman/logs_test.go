package podman

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// frame builds one multiplexed log frame, so the decoder's tests describe
// what they feed it rather than hard-coding byte soup.
func frame(streamType byte, payload string) []byte {
	header := make([]byte, frameHeaderSize)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))

	return append(header, payload...)
}

func collect(t *testing.T, stream io.Reader) ([]LogFrame, error) {
	t.Helper()

	var got []LogFrame
	err := demultiplex(stream, func(f LogFrame) error {
		got = append(got, LogFrame{Stream: f.Stream, Data: append([]byte(nil), f.Data...)})
		return nil
	})

	return got, err
}

func TestDemultiplexSplitsStdoutFromStderr(t *testing.T) {
	stream := bytes.NewReader(bytes.Join([][]byte{
		frame(frameStdout, "out-one\n"),
		frame(frameStderr, "err-one\n"),
		frame(frameStdout, "out-two\n"),
	}, nil))

	got, err := collect(t, stream)
	if err != nil {
		t.Fatalf("demultiplex: %v", err)
	}

	want := []LogFrame{
		{Stream: StreamStdout, Data: []byte("out-one\n")},
		{Stream: StreamStderr, Data: []byte("err-one\n")},
		{Stream: StreamStdout, Data: []byte("out-two\n")},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d frames, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Stream != want[i].Stream || !bytes.Equal(got[i].Data, want[i].Data) {
			t.Errorf("frame %d = {%s, %q}, want {%s, %q}", i, got[i].Stream, got[i].Data, want[i].Stream, want[i].Data)
		}
	}
}

// EOF exactly on a frame boundary is how a finished container's stream ends.
func TestDemultiplexEndsCleanlyAtEOF(t *testing.T) {
	got, err := collect(t, bytes.NewReader(frame(frameStdout, "only\n")))
	if err != nil {
		t.Fatalf("demultiplex: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d frames, want 1", len(got))
	}
}

// EOF *inside* a frame is not a clean end: the payload is incomplete, and
// silently returning what arrived would present a truncated line as a whole
// one.
func TestDemultiplexRejectsATruncatedFrame(t *testing.T) {
	truncated := frame(frameStdout, "a full line\n")[:10]

	if _, err := collect(t, bytes.NewReader(truncated)); err == nil {
		t.Fatal("demultiplex accepted a frame cut short, want an error")
	}
}

// A length field the stream cannot back up means the framing has desynced.
// Believing it would allocate whatever it claims.
func TestDemultiplexRejectsAnOversizedFrame(t *testing.T) {
	header := make([]byte, frameHeaderSize)
	header[0] = frameStdout
	binary.BigEndian.PutUint32(header[4:], maxFrameSize+1)

	_, err := collect(t, bytes.NewReader(header))
	if err == nil {
		t.Fatal("demultiplex accepted an oversized frame, want an error")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %q, want it to name the size limit", err)
	}
}

func TestDemultiplexRejectsAnUnknownStreamType(t *testing.T) {
	if _, err := collect(t, bytes.NewReader(frame(7, "who?\n"))); err == nil {
		t.Fatal("demultiplex accepted stream type 7, want an error")
	}
}

// An error from the callback stops the walk and comes back unchanged, so a
// caller can bail out on its own terms (a full disk, a cancelled subscriber)
// without that error being reinterpreted as a protocol fault.
func TestDemultiplexReturnsTheCallbacksError(t *testing.T) {
	sentinel := errors.New("stop here")

	stream := bytes.NewReader(bytes.Join([][]byte{
		frame(frameStdout, "first\n"),
		frame(frameStdout, "second\n"),
	}, nil))

	var seen int
	err := demultiplex(stream, func(LogFrame) error {
		seen++
		return sentinel
	})

	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want the callback's own error", err)
	}
	if seen != 1 {
		t.Errorf("callback ran %d times, want 1 - it should stop at the first error", seen)
	}
}

// --- Integration: the real socket ---

// Output separation, ordering and the unterminated tail, end to end against
// a real container. The tail matters: `printf` without a newline still
// printed something.
func TestFollowContainerLogsAgainstARealContainer(t *testing.T) {
	client, ctx := newTestClient(t)

	id, err := client.CreateContainer(ctx, CreateContainerParams{
		RunID:   999996,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{"sh", "-c", "echo out-one; echo err-one 1>&2; printf tail-no-newline"},
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: RemoveContainer: %v", err)
		}
	})

	if err := client.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err = client.FollowContainerLogs(ctx, id, func(f LogFrame) error {
		switch f.Stream {
		case StreamStdout:
			stdout.Write(f.Data)
		case StreamStderr:
			stderr.Write(f.Data)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("FollowContainerLogs: %v", err)
	}

	if got, want := stdout.String(), "out-one\ntail-no-newline"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "err-one\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// The reconciler's adoption path depends on this: a container that has
// already exited still replays its whole output, and the follow returns
// rather than blocking forever waiting for a container that will never
// produce anything again.
func TestFollowContainerLogsReplaysAnExitedContainer(t *testing.T) {
	client, ctx := newTestClient(t)

	id, err := client.CreateContainer(ctx, CreateContainerParams{
		RunID:   999995,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{"echo", "printed-before-anyone-attached"},
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: RemoveContainer: %v", err)
		}
	})

	if err := client.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	if _, err := client.WaitContainer(ctx, id); err != nil {
		t.Fatalf("WaitContainer: %v", err)
	}

	var out bytes.Buffer
	if err := client.FollowContainerLogs(ctx, id, func(f LogFrame) error {
		out.Write(f.Data)
		return nil
	}); err != nil {
		t.Fatalf("FollowContainerLogs on an exited container: %v", err)
	}

	if got, want := out.String(), "printed-before-anyone-attached\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// The same class of bug as TestWaitContainerOutlivesTheRequestTimeout: this
// call blocks for the container's whole life, so the ordinary
// http.Client.Timeout would cut the logs of every run longer than
// requestTimeout - and would do it silently, leaving a plausible-looking
// partial log rather than an obvious failure.
func TestFollowContainerLogsOutlivesTheRequestTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("sleeps past the request timeout by design")
	}

	client, ctx := newTestClient(t)

	sleepFor := requestTimeout + 3*time.Second

	id, err := client.CreateContainer(ctx, CreateContainerParams{
		RunID: 999994,
		Image: "docker.io/library/alpine:latest",
		Command: []string{"sh", "-c",
			"echo before; sleep " + strconv.Itoa(int(sleepFor.Seconds())) + "; echo after"},
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: RemoveContainer: %v", err)
		}
	})

	if err := client.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	started := time.Now()
	var out bytes.Buffer
	if err := client.FollowContainerLogs(ctx, id, func(f LogFrame) error {
		out.Write(f.Data)
		return nil
	}); err != nil {
		t.Fatalf("FollowContainerLogs across a %s container: %v", sleepFor, err)
	}

	if waited := time.Since(started); waited < requestTimeout {
		t.Errorf("follow returned after %s, before the container could have finished", waited)
	}
	// "after" is the line printed on the far side of the timeout boundary -
	// the one a blanket client timeout would have swallowed.
	if got, want := out.String(), "before\nafter\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// Cancelling the context must stop the follow promptly, rather than leaving
// the supervisor attached to a container it no longer cares about.
func TestFollowContainerLogsStopsOnContextCancel(t *testing.T) {
	client, ctx := newTestClient(t)

	id, err := client.CreateContainer(ctx, CreateContainerParams{
		RunID:   999993,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{"sh", "-c", "echo started; sleep 60"},
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	t.Cleanup(func() {
		if err := client.KillContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: KillContainer: %v", err)
		}
		if _, err := client.WaitContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: WaitContainer: %v", err)
		}
		if err := client.RemoveContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: RemoveContainer: %v", err)
		}
	})

	if err := client.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	followCtx, cancel := context.WithCancel(ctx)

	done := make(chan error, 1)
	go func() {
		done <- client.FollowContainerLogs(followCtx, id, func(LogFrame) error {
			// Stop as soon as the container has said anything, so the cancel
			// lands while the follow is genuinely blocked on a live stream.
			cancel()
			return nil
		})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("follow returned nil after cancellation, want a context error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("follow did not return within 15s of its context being cancelled")
	}
}

// A burst of output survives intact.
//
// This is the regression test for ARCHITECTURE.md decision #20. The host's
// default log driver is journald, which rate-limits (10000 messages per 30
// seconds as shipped) and silently discards everything past the limit for the
// rest of the window - so this exact test, before the driver was pinned to
// k8s-file, returned about 17500 of 20000 lines, and *zero* when run twice in
// quick succession. Nothing anywhere reported an error either time.
//
// 20000 lines is chosen to sit well past that limit. If this ever starts
// failing with a plausible-but-short count, suspect the log driver before
// suspecting the capture code: a line the driver dropped never reaches us at
// all, and no amount of care downstream can recover it.
func TestFollowContainerLogsCapturesABurstWithoutLoss(t *testing.T) {
	client, ctx := newTestClient(t)

	const want = 20000

	id, err := client.CreateContainer(ctx, CreateContainerParams{
		RunID:   999993,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{"seq", "1", strconv.Itoa(want)},
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: RemoveContainer: %v", err)
		}
	})

	if err := client.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	var stdout bytes.Buffer
	if err := client.FollowContainerLogs(ctx, id, func(f LogFrame) error {
		if f.Stream == StreamStdout {
			stdout.Write(f.Data)
		}
		return nil
	}); err != nil {
		t.Fatalf("FollowContainerLogs: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != want {
		t.Fatalf("captured %d lines, want %d - output was lost before it reached us", len(lines), want)
	}
	// Complete *and* in order: a limiter that dropped from the middle would
	// still leave the right count if something else duplicated lines.
	for i, line := range lines {
		if line != strconv.Itoa(i+1) {
			t.Fatalf("line %d = %q, want %q", i+1, line, strconv.Itoa(i+1))
		}
	}
}
