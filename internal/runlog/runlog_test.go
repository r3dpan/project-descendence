package runlog

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// newTestWriter returns a writer over a throwaway directory, with a
// deterministic clock so timestamps can be asserted rather than merely
// observed.
func newTestWriter(t *testing.T) (*Writer, string) {
	t.Helper()

	dir := t.TempDir()
	writer, err := Create(dir, 42)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tick := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	writer.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	return writer, dir
}

func readFile(t *testing.T, dir string) string {
	t.Helper()

	body, err := os.ReadFile(Path(dir, 42))
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}

	return string(body)
}

// The central invariant: every index entry addresses exactly its own line in
// the file. If this holds, a reader can serve any line without the writer
// having told it anything beyond the offsets.
func assertLinesAddressTheirText(t *testing.T, dir string, lines []Line, want []string) {
	t.Helper()

	body := readFile(t, dir)

	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(lines), len(want), lines)
	}

	for i, line := range lines {
		if line.Seq != int64(i+1) {
			t.Errorf("line %d has seq %d, want %d - sequence numbers must be dense and 1-based", i, line.Seq, i+1)
		}

		end := line.ByteOffset + int64(line.ByteLength)
		if end > int64(len(body)) {
			t.Fatalf("line %d addresses [%d,%d) but the file is only %d bytes", i, line.ByteOffset, end, len(body))
		}
		if got := body[line.ByteOffset:end]; got != want[i] {
			t.Errorf("line %d addresses %q, want %q", i, got, want[i])
		}
	}
}

func TestWriteSplitsFramesIntoLines(t *testing.T) {
	writer, dir := newTestWriter(t)

	lines, err := writer.Write(StreamStdout, []byte("first\nsecond\nthird\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertLinesAddressTheirText(t, dir, lines, []string{"first", "second", "third"})
}

// Frame boundaries are not line boundaries. libpod usually happens to align
// them, but a line longer than one frame must still come out as one line.
func TestWriteJoinsALineSplitAcrossFrames(t *testing.T) {
	writer, dir := newTestWriter(t)

	first, err := writer.Write(StreamStdout, []byte("one hal"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(first) != 0 {
		t.Fatalf("got %d lines from an unterminated fragment, want 0", len(first))
	}

	second, err := writer.Write(StreamStdout, []byte("f, other half\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertLinesAddressTheirText(t, dir, second, []string{"one half, other half"})
}

// A script ending in `printf done` rather than `echo done` still printed
// something. Dropping it for want of a newline would be a silent hole.
func TestCloseFlushesTheUnterminatedTail(t *testing.T) {
	writer, dir := newTestWriter(t)

	if _, err := writer.Write(StreamStdout, []byte("terminated\nno newline here")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	tail, err := writer.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(tail) != 1 {
		t.Fatalf("Close returned %d lines, want 1 (the unterminated tail)", len(tail))
	}
	if tail[0].Seq != 2 {
		t.Errorf("tail seq = %d, want 2 - it continues the run's numbering", tail[0].Seq)
	}
	if got := readFile(t, dir); got != "terminated\nno newline here\n" {
		t.Errorf("file = %q, want the tail present and newline-terminated on disk", got)
	}
}

// Sequence numbers are per run, not per stream: they are what orders the two
// streams against each other when they are merged back together for display.
func TestSequenceNumbersInterleaveTheStreams(t *testing.T) {
	writer, dir := newTestWriter(t)

	var lines []Line
	for _, step := range []struct {
		stream string
		data   string
	}{
		{StreamStdout, "out-one\n"},
		{StreamStderr, "err-one\n"},
		{StreamStdout, "out-two\n"},
	} {
		written, err := writer.Write(step.stream, []byte(step.data))
		if err != nil {
			t.Fatalf("Write(%s): %v", step.stream, err)
		}
		lines = append(lines, written...)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertLinesAddressTheirText(t, dir, lines, []string{"out-one", "err-one", "out-two"})

	wantStreams := []string{StreamStdout, StreamStderr, StreamStdout}
	for i, line := range lines {
		if line.Stream != wantStreams[i] {
			t.Errorf("line %d stream = %q, want %q", i, line.Stream, wantStreams[i])
		}
	}
}

// One stream holding a partial line must not swallow the other stream's
// completed lines - stderr should not be stuck behind an unterminated stdout
// write.
func TestAPartialLineOnOneStreamDoesNotBlockTheOther(t *testing.T) {
	writer, dir := newTestWriter(t)

	if _, err := writer.Write(StreamStdout, []byte("stdout is mid-line")); err != nil {
		t.Fatalf("Write(stdout): %v", err)
	}

	errLines, err := writer.Write(StreamStderr, []byte("stderr got through\n"))
	if err != nil {
		t.Fatalf("Write(stderr): %v", err)
	}
	if len(errLines) != 1 {
		t.Fatalf("got %d stderr lines, want 1", len(errLines))
	}

	tail, err := writer.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertLinesAddressTheirText(t, dir, append(errLines, tail...),
		[]string{"stderr got through", "stdout is mid-line"})
}

func TestEmptyLinesAreKept(t *testing.T) {
	writer, dir := newTestWriter(t)

	lines, err := writer.Write(StreamStdout, []byte("before\n\nafter\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertLinesAddressTheirText(t, dir, lines, []string{"before", "", "after"})

	if lines[1].ByteLength != 0 {
		t.Errorf("blank line has length %d, want 0", lines[1].ByteLength)
	}
}

// Flush is the contract that lets the index be published safely: once it
// returns, everything the writer has handed back is genuinely readable at the
// offsets it reported.
func TestFlushMakesReportedOffsetsReadable(t *testing.T) {
	writer, dir := newTestWriter(t)

	lines, err := writer.Write(StreamStdout, []byte("visible after flush\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	assertLinesAddressTheirText(t, dir, lines, []string{"visible after flush"})

	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// The reconciler recaptures an adopted run from scratch (task 1.15 meeting
// task 2.1), which only works if a second Create genuinely starts over rather
// than appending a second copy of the output to the first.
func TestCreateTruncatesAPreviousCapture(t *testing.T) {
	dir := t.TempDir()

	first, err := Create(dir, 42)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := first.Write(StreamStdout, []byte("from the crashed supervisor\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Create(dir, 42)
	if err != nil {
		t.Fatalf("Create (recapture): %v", err)
	}
	lines, err := second.Write(StreamStdout, []byte("recaptured\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := second.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := readFile(t, dir); got != "recaptured\n" {
		t.Errorf("file = %q, want only the recaptured output", got)
	}
	if lines[0].Seq != 1 || lines[0].ByteOffset != 0 {
		t.Errorf("recapture restarted at seq %d offset %d, want seq 1 offset 0", lines[0].Seq, lines[0].ByteOffset)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	writer, _ := newTestWriter(t)

	if _, err := writer.Write(StreamStdout, []byte("one\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	lines, err := writer.Close()
	if err != nil {
		t.Errorf("second Close: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("second Close returned %d lines, want 0", len(lines))
	}
}

// Write must not retain the caller's slice: internal/podman hands over a
// buffer it is free to reuse for the next frame.
func TestWriteDoesNotRetainTheCallersBuffer(t *testing.T) {
	writer, dir := newTestWriter(t)

	buffer := []byte("mid")
	if _, err := writer.Write(StreamStdout, buffer); err != nil {
		t.Fatalf("Write: %v", err)
	}
	copy(buffer, "XXX")

	lines, err := writer.Write(StreamStdout, []byte("-line\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertLinesAddressTheirText(t, dir, lines, []string{"mid-line"})
}

func TestPathIsDerivedFromTheRunID(t *testing.T) {
	if got, want := Path("/var/log/descendence", 1234), "/var/log/descendence/1234.log"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestCreateCreatesTheDirectory(t *testing.T) {
	dir := t.TempDir() + "/nested/logs"

	writer, err := Create(dir, 7)
	if err != nil {
		t.Fatalf("Create into a missing directory: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(Path(dir, 7)); err != nil {
		t.Errorf("log file not created: %v", err)
	}
}

func TestTimestampsAreRecordedPerLine(t *testing.T) {
	writer, _ := newTestWriter(t)

	lines, err := writer.Write(StreamStdout, []byte("one\ntwo\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !lines[1].Ts.After(lines[0].Ts) {
		t.Errorf("line timestamps %v and %v are not increasing", lines[0].Ts, lines[1].Ts)
	}
	if lines[0].Ts.Location() != time.UTC {
		t.Errorf("timestamp location = %v, want UTC", lines[0].Ts.Location())
	}
}

// Long lines are exactly where an off-by-one in the offset bookkeeping would
// show up, and where libpod would be most likely to split one line over
// several frames.
func TestLongLinesKeepTheirOffsets(t *testing.T) {
	writer, dir := newTestWriter(t)

	long := strings.Repeat("x", 100_000)

	var lines []Line
	for _, chunk := range []string{long[:40_000], long[40_000:], "\nshort\n"} {
		written, err := writer.Write(StreamStdout, []byte(chunk))
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		lines = append(lines, written...)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertLinesAddressTheirText(t, dir, lines, []string{long, "short"})
}

// --- Reader ---

// The round trip that the API depends on: whatever the writer reported as a
// line's address must read back as exactly that line.
func TestReaderReturnsExactlyTheWrittenLine(t *testing.T) {
	writer, dir := newTestWriter(t)

	lines, err := writer.Write(StreamStdout, []byte("first\nsecond\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	errLines, err := writer.Write(StreamStderr, []byte("on stderr\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, err := Open(dir, 42)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer reader.Close()

	want := []string{"first", "second", "on stderr"}
	for i, line := range append(lines, errLines...) {
		got, err := reader.ReadLine(line.ByteOffset, line.ByteLength)
		if err != nil {
			t.Fatalf("ReadLine(seq %d): %v", line.Seq, err)
		}
		if got != want[i] {
			t.Errorf("seq %d read back as %q, want %q", line.Seq, got, want[i])
		}
	}
}

// A run with no output yet has no file. That is the normal state of a queued
// run, and of one whose logs the retention sweep deleted, so it must be
// distinguishable from an I/O failure rather than lumped in with one.
func TestOpenReportsAMissingFileDistinctly(t *testing.T) {
	if _, err := Open(t.TempDir(), 404); !errors.Is(err, ErrNoLogFile) {
		t.Errorf("Open on a missing file gave %v, want ErrNoLogFile", err)
	}
}

// Reading a run that is still being written to is the whole point - a client
// tailing a live run reads lines the supervisor is still appending after.
func TestReaderSeesLinesWhileTheWriterIsStillOpen(t *testing.T) {
	writer, dir := newTestWriter(t)
	defer writer.Close()

	lines, err := writer.Write(StreamStdout, []byte("while running\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	reader, err := Open(dir, 42)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer reader.Close()

	got, err := reader.ReadLine(lines[0].ByteOffset, lines[0].ByteLength)
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if got != "while running" {
		t.Errorf("read %q, want %q", got, "while running")
	}

	// And a line appended after the reader was opened is visible too - the
	// reader must not have snapshotted the file's length at open time.
	later, err := writer.Write(StreamStdout, []byte("appended later\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got, err = reader.ReadLine(later[0].ByteOffset, later[0].ByteLength)
	if err != nil {
		t.Fatalf("ReadLine after append: %v", err)
	}
	if got != "appended later" {
		t.Errorf("read %q, want %q", got, "appended later")
	}
}

// An index row pointing past the end of the file means the flush-before-index
// invariant has broken. Half a line presented as a whole one would be far
// worse than a loud error.
func TestReadLineRejectsAnOffsetPastTheEndOfTheFile(t *testing.T) {
	writer, dir := newTestWriter(t)
	if _, err := writer.Write(StreamStdout, []byte("short\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, err := Open(dir, 42)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer reader.Close()

	if _, err := reader.ReadLine(0, 9999); err == nil {
		t.Error("ReadLine accepted a length running past the end of the file, want an error")
	}
}

func TestReadLineHandlesABlankLine(t *testing.T) {
	writer, dir := newTestWriter(t)

	lines, err := writer.Write(StreamStdout, []byte("\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, err := Open(dir, 42)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer reader.Close()

	got, err := reader.ReadLine(lines[0].ByteOffset, lines[0].ByteLength)
	if err != nil {
		t.Fatalf("ReadLine on a blank line: %v", err)
	}
	if got != "" {
		t.Errorf("blank line read back as %q, want empty", got)
	}
}

// LineCounter has to agree with Writer on every input, because the supervisor
// uses the difference between them to decide whether a capture was truncated
// (decision #21). Disagreement in one direction hides lost output; in the
// other it rewrites a complete capture on every single run, forever.
func TestLineCounterAgreesWithWriter(t *testing.T) {
	type write struct {
		stream string
		data   string
	}

	cases := []struct {
		name   string
		writes []write
	}{
		{"nothing at all", nil},
		{"one whole line", []write{{StreamStdout, "one\n"}}},
		{"unterminated tail", []write{{StreamStdout, "no newline here"}}},
		{"several lines in one frame", []write{{StreamStdout, "a\nb\nc\n"}}},
		{"a line split across frames", []write{{StreamStdout, "half"}, {StreamStdout, " and half\n"}}},
		{"both streams, both unterminated", []write{
			{StreamStdout, "out\nstill going"},
			{StreamStderr, "err\nalso going"},
		}},
		{"interleaved streams", []write{
			{StreamStdout, "o1\n"}, {StreamStderr, "e1\n"},
			{StreamStdout, "o2\n"}, {StreamStderr, "e2\ne3\n"},
		}},
		{"an empty frame changes nothing", []write{
			{StreamStdout, "a\n"}, {StreamStdout, ""}, {StreamStdout, "b\n"},
		}},
		{"a lone newline is an empty line", []write{{StreamStdout, "\n"}}},
		{"trailing newline after a partial", []write{
			{StreamStdout, "partial"}, {StreamStdout, "\n"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writer, err := Create(t.TempDir(), 1)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			counter := NewLineCounter()

			written := 0
			for _, w := range tc.writes {
				lines, err := writer.Write(w.stream, []byte(w.data))
				if err != nil {
					t.Fatalf("Write: %v", err)
				}
				written += len(lines)
				counter.Write(w.stream, []byte(w.data))
			}

			tail, err := writer.Close()
			if err != nil {
				t.Fatalf("Close: %v", err)
			}
			written += len(tail)

			if counted := counter.Total(); counted != written {
				t.Errorf("LineCounter counted %d lines, Writer produced %d", counted, written)
			}
		})
	}
}
