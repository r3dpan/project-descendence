package runlog

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// ErrNoLogFile means the run has no log file. Distinct from an I/O failure:
// it is the normal state of a run that has not started yet, and the expected
// state of one whose logs the retention sweep has deleted, so callers
// generally want to report it rather than treat it as broken.
var ErrNoLogFile = errors.New("runlog: no log file for this run")

// Reader reads back the lines a Writer produced, addressed by the byte
// offsets recorded in the run_logs index.
//
// Open one per request and Close it. It holds an open file handle and reads
// with ReadAt, so it never seeks and is safe to use while the supervisor is
// still appending to the same file - a growing file is the normal case, since
// the whole point is reading a run's output while it is still running.
type Reader struct {
	file *os.File
}

// Open opens run runID's log file for reading. Returns ErrNoLogFile if there
// is none.
func Open(dir string, runID int64) (*Reader, error) {
	file, err := os.Open(Path(dir, runID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNoLogFile
		}
		return nil, fmt.Errorf("runlog: opening log file for run %d: %w", runID, err)
	}

	return &Reader{file: file}, nil
}

// ReadLine returns the text of the line an index entry addresses, without its
// terminating newline.
//
// A short read is reported as an error rather than silently returning a
// truncated line. It should not happen - the writer flushes bytes before
// their index row is published, precisely so that a row never outruns the
// file - but if that invariant ever breaks, a loud error is far better than
// half a line presented as whole.
func (r *Reader) ReadLine(offset int64, length int32) (string, error) {
	if length == 0 {
		return "", nil
	}

	text := make([]byte, length)
	if _, err := r.file.ReadAt(text, offset); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return "", fmt.Errorf("runlog: log line at [%d,%d) is past the end of the file", offset, offset+int64(length))
		}
		return "", fmt.Errorf("runlog: reading log line at [%d,%d): %w", offset, offset+int64(length), err)
	}

	return string(text), nil
}

func (r *Reader) Close() error {
	return r.file.Close()
}
