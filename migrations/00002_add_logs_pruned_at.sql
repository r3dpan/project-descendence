-- +goose Up

-- Task 2.2, resolving the log-retention open question (ARCHITECTURE.md §8).
--
-- Log bodies and their index rows are deleted once a run is older than the
-- retention window; the run itself is kept forever. logs_pruned_at is what
-- lets the API tell "this run printed nothing" apart from "this run's output
-- has been deleted" - without it, both look like an empty log and the second
-- one silently lies to whoever is reading.
--
-- It is set on every run the sweep passes over, including runs that never
-- produced output. A run that printed nothing and is past the window will
-- therefore report its logs as expired rather than empty. That is the
-- deliberate trade: the alternative is re-scanning output-less runs on every
-- sweep, forever.
ALTER TABLE runs ADD COLUMN logs_pruned_at timestamptz;

-- The retention sweep's access path: unpruned runs, oldest first. Partial, so
-- it holds only the runs still awaiting a sweep and shrinks as they are
-- pruned, rather than growing with the whole table.
CREATE INDEX runs_logs_prunable_idx ON runs (finished_at) WHERE logs_pruned_at IS NULL;

-- +goose Down

DROP INDEX runs_logs_prunable_idx;
ALTER TABLE runs DROP COLUMN logs_pruned_at;
