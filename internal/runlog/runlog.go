// Package runlog stores the bodies of container output on disk.
//
// Log bodies go to one file per run; the index that makes them addressable
// (sequence number, stream, timestamp, and where in the file each line lives)
// goes to Postgres, in run_logs. See ARCHITECTURE.md §4.1 - Postgres is the
// log *index*, not the log store. Postgres would handle this volume fine, but
// a table whose rows are mostly text bodies is a table you cannot cheaply
// vacuum, back up or prune, and files make the retention sweep a matter of
// deleting a file.
//
// The supervisor is the only writer. The API only ever reads.
//
// Sequence numbers record the order output *arrived*, which is not always the
// order the script printed it: stdout and stderr are buffered independently
// inside the container, so a line written to stderr can surface after a
// later stdout line. Observed for real - a script echoing to stderr between
// two stdout lines had the stderr line land one line late. That is the
// container's buffering, not a capture fault, and the alternative (reordering
// by timestamp) would invent an ordering nobody actually observed.
package runlog

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// The two streams a line can come from, matching run_logs.stream's CHECK
// constraint and internal/podman's frame stream names.
const (
	StreamStdout = "stdout"
	StreamStderr = "stderr"
)

// streamOrder fixes the order trailing partial lines are flushed in, so a run
// that ends mid-line on both streams produces the same file every time rather
// than whatever order a map range happened to give.
var streamOrder = []string{StreamStdout, StreamStderr}

// Line is one captured line of output: everything the run_logs index row
// needs, and nothing else. Text is deliberately absent - it lives in the
// file, at [ByteOffset, ByteOffset+ByteLength).
type Line struct {
	Seq        int64
	Stream     string
	Ts         time.Time
	ByteOffset int64
	ByteLength int32
}

// Path is where run runID's output lives inside dir. Both the supervisor
// (writing) and the API (reading) derive it from the run id alone, so neither
// has to store or look up a filename.
func Path(dir string, runID int64) string {
	return filepath.Join(dir, strconv.FormatInt(runID, 10)+".log")
}

// Writer appends captured output to one run's log file, splitting it into
// lines and numbering them.
//
// Not safe for concurrent use: one run has exactly one capture goroutine
// feeding it.
type Writer struct {
	file *os.File
	buf  *bufio.Writer

	// offset is where the next line's bytes will land, i.e. how many bytes
	// have been handed to buf so far. It is the ByteOffset the index rows
	// point at, so it counts bytes written, not bytes flushed.
	offset int64
	seq    int64

	// partial holds, per stream, the bytes of a line that has arrived but has
	// not been terminated yet. Container output arrives in frames, and a frame
	// boundary is not a line boundary - libpod usually happens to align them,
	// but nothing guarantees it, and a line longer than one frame would
	// otherwise be split in two.
	partial map[string][]byte

	now func() time.Time
}

// Create opens run runID's log file for writing, creating dir if needed.
//
// It **truncates** an existing file. That is the point rather than an
// oversight: the only way a log file already exists for a run being captured
// is the reconciler adopting a run after a crash (task 1.15), and libpod
// replays a container's whole output on every follow. Recapturing from zero
// is therefore complete and self-consistent, where resuming at an offset
// would have to guess where the previous capture stopped and would duplicate
// or drop lines around the seam. The caller must clear the run's existing
// index rows to match.
func Create(dir string, runID int64) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("runlog: creating log directory %s: %w", dir, err)
	}

	file, err := os.OpenFile(Path(dir, runID), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return nil, fmt.Errorf("runlog: opening log file for run %d: %w", runID, err)
	}

	return &Writer{
		file:    file,
		buf:     bufio.NewWriter(file),
		partial: make(map[string][]byte, len(streamOrder)),
		now:     time.Now,
	}, nil
}

// Write appends data (a chunk of stream's output) and returns one Line per
// *complete* line it contained. A trailing unterminated fragment is held back
// until the rest of it arrives, or until Close flushes it.
//
// The returned lines are already in the file's buffer but not necessarily in
// the file: call Flush before recording them anywhere a reader could follow
// the offsets.
func (w *Writer) Write(stream string, data []byte) ([]Line, error) {
	pending := append(w.partial[stream], data...)

	var lines []Line
	for {
		end := bytes.IndexByte(pending, '\n')
		if end < 0 {
			break
		}

		line, err := w.commit(stream, pending[:end])
		if err != nil {
			return lines, err
		}
		lines = append(lines, line)

		pending = pending[end+1:]
	}

	// Copy rather than retain: pending aliases either data (which the caller
	// may reuse) or the previous partial's array, which the next Write would
	// append over.
	w.partial[stream] = append([]byte(nil), pending...)

	return lines, nil
}

// commit writes one line's text, newline-terminated, and returns its index
// entry. ByteLength covers the text only - the terminator is a detail of the
// file's layout, not part of the line, so a reader that seeks to
// [ByteOffset, ByteOffset+ByteLength) gets exactly what was printed.
func (w *Writer) commit(stream string, text []byte) (Line, error) {
	if _, err := w.buf.Write(text); err != nil {
		return Line{}, fmt.Errorf("runlog: writing line: %w", err)
	}
	if err := w.buf.WriteByte('\n'); err != nil {
		return Line{}, fmt.Errorf("runlog: writing line terminator: %w", err)
	}

	w.seq++
	line := Line{
		Seq:        w.seq,
		Stream:     stream,
		Ts:         w.now().UTC(),
		ByteOffset: w.offset,
		ByteLength: int32(len(text)),
	}

	w.offset += int64(len(text)) + 1

	return line, nil
}

// Flush pushes buffered bytes to the file. It must be called before the lines
// Write returned are recorded in Postgres: the index is what tells a reader
// those bytes exist, so publishing an index row for a line still sitting in
// this buffer points that reader past the end of the file.
func (w *Writer) Flush() error {
	if err := w.buf.Flush(); err != nil {
		return fmt.Errorf("runlog: flushing log file: %w", err)
	}
	return nil
}

// Close flushes any trailing unterminated line - a script ending with
// `printf done` rather than `echo done` still printed something, and dropping
// it because it lacked a newline would be a silent hole - then flushes and
// closes the file. The lines it returns need recording exactly like Write's.
//
// Safe to call twice; the second call is a no-op.
func (w *Writer) Close() ([]Line, error) {
	if w.file == nil {
		return nil, nil
	}

	var lines []Line
	for _, stream := range streamOrder {
		text := w.partial[stream]
		if len(text) == 0 {
			continue
		}
		delete(w.partial, stream)

		line, err := w.commit(stream, text)
		if err != nil {
			return lines, err
		}
		lines = append(lines, line)
	}

	err := w.Flush()
	if closeErr := w.file.Close(); err == nil {
		err = closeErr
	}
	w.file = nil

	return lines, err
}

// LineCounter counts the lines a sequence of writes would produce, without
// producing them.
//
// It exists so the supervisor can ask "did that capture get everything?"
// cheaply: re-reading a container's output through a Writer would mean writing
// the whole file a second time just to find out it was already correct
// (ARCHITECTURE.md decision #21). Reading is the common case, rewriting the
// rare one, so the common case should not pay for the rare one.
//
// It follows Writer's rule exactly - split on '\n', and a trailing
// unterminated fragment on either stream still counts as a line - and a test
// asserts the two agree on the same input, because a counter that disagrees
// with the writer would either hide a truncated capture or trigger an endless
// rewrite of a complete one.
type LineCounter struct {
	lines   int
	partial map[string]bool
}

func NewLineCounter() *LineCounter {
	return &LineCounter{partial: make(map[string]bool)}
}

// Write counts the lines in one frame of a stream.
func (c *LineCounter) Write(stream string, data []byte) {
	c.lines += bytes.Count(data, []byte{'\n'})

	// Whether this stream now ends mid-line, which decides if there is a
	// trailing line to count at the end.
	if len(data) > 0 {
		c.partial[stream] = data[len(data)-1] != '\n'
	}
}

// Total is the line count, including any trailing unterminated line.
func (c *LineCounter) Total() int {
	total := c.lines
	for _, unterminated := range c.partial {
		if unterminated {
			total++
		}
	}
	return total
}
