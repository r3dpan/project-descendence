-- name: InsertRunLogs :copyfrom
-- The index rows for captured output (task 2.2). :copyfrom so a batch is one
-- COPY rather than one round trip per line - a chatty script can produce
-- thousands of lines, and the supervisor's capture loop is on the critical
-- path of every run.
--
-- The bodies are NOT here: byte_offset/byte_length address the run's log file
-- (ARCHITECTURE.md §4.1, internal/runlog). Never insert a row for a line the
-- writer has not flushed - the row is what tells a reader those bytes exist.
INSERT INTO run_logs (run_id, seq, stream, ts, byte_offset, byte_length)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: DeleteRunLogs :exec
-- Clears a run's index, to match runlog.Create truncating its file. Used when
-- the reconciler adopts a run and recaptures its output from scratch (task
-- 1.15 meeting 2.1): the old rows address offsets in a file that no longer
-- exists, so leaving them behind would point readers at the wrong bytes.
DELETE FROM run_logs WHERE run_id = $1;

-- name: ListRunLogs :many
-- One page of a run's log index, in sequence order, starting strictly after
-- after_seq. after_seq = 0 means "from the beginning", which is also what an
-- SSE client reconnecting without a Last-Event-ID asks for (task 2.6).
-- Matches the (run_id, seq) primary key, so no extra index is needed.
SELECT run_id, seq, stream, ts, byte_offset, byte_length
FROM run_logs
WHERE run_id = $1
  AND seq > sqlc.arg(after_seq)
ORDER BY seq
LIMIT sqlc.arg(row_limit);

-- name: CountRunLogs :one
-- Whether a run has any index rows at all. Distinguishes "printed nothing"
-- from "output was pruned" when read alongside runs.logs_pruned_at.
SELECT count(*) FROM run_logs WHERE run_id = $1;

-- name: ListRunsWithExpiredLogs :many
-- The retention sweep's input (task 2.2): finished runs past the retention
-- window whose logs have not been pruned yet, oldest first. Matches
-- runs_logs_prunable_idx. Bounded by row_limit so one sweep does a bounded
-- amount of work rather than trying to clear a year's backlog in a single
-- pass.
SELECT id
FROM runs
WHERE logs_pruned_at IS NULL
  AND finished_at IS NOT NULL
  AND finished_at < sqlc.arg(cutoff)
ORDER BY finished_at
LIMIT sqlc.arg(row_limit);

-- name: MarkRunLogsPruned :exec
-- Records that the sweep has been over this run. Set last, after the rows and
-- the file are actually gone, so a crash mid-sweep leaves the run to be swept
-- again rather than marking it done with its logs still on disk.
UPDATE runs SET logs_pruned_at = now() WHERE id = $1;

-- name: NotifyRunEvent :exec
-- Wakes anything streaming a run in the API (task 2.3). pg_notify() rather
-- than a NOTIFY statement because the payload is a parameter here, not
-- something to be pasted into SQL text.
--
-- api and supervisor never talk to each other (ARCHITECTURE.md §3), so this
-- is the whole channel between "the supervisor captured a line" and "a
-- browser sees it". The payload is a watermark, never log text: see
-- internal/logstream.
SELECT pg_notify(@channel::text, @payload::text);
