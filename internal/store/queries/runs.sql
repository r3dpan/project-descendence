-- name: CreateRun :one
-- Idempotency_key is NULL when the caller sent no Idempotency-Key header;
-- Postgres never treats NULL as conflicting with anything, so unkeyed runs
-- always insert. A repeated key for the same principal hits the unique index
-- and DO NOTHING skips the insert - :one then returns pgx.ErrNoRows, which is
-- the caller's signal to look the original up via GetRunByIdempotencyKey.
INSERT INTO runs (principal_id, image_ref, argv, timeout_seconds, idempotency_key)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (principal_id, idempotency_key) DO NOTHING
RETURNING id, principal_id, state, idempotency_key, image_ref, argv,
          timeout_seconds, container_id, exit_code, failure_reason,
          cancel_requested_at, queued_at, started_at, finished_at, job_id,
          commit_sha, runtime_id, image_digest, params_json, logs_pruned_at;

-- name: CreateJobRun :one
-- Task 3.5. The same insert as CreateRun, plus the columns that make a run
-- explainable: which job it is, the exact commit its definition and script
-- were read from, and - task 4.6, when the job names a runtime rather than
-- an image directly - which runtime and which digest of it.
--
-- image_ref, runtime_id and image_digest are all written onto the run rather
-- than looked up from the job at execution time, deliberately. A run records
-- what it will do, not a pointer to somewhere that might say something
-- different later - the same reason commit_sha is pinned here rather than
-- resolved by the supervisor, and the reason rebuilding a runtime after this
-- insert cannot change what this run executes.
INSERT INTO runs (principal_id, image_ref, argv, timeout_seconds, idempotency_key,
                  job_id, commit_sha, runtime_id, image_digest)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (principal_id, idempotency_key) DO NOTHING
RETURNING id, principal_id, state, idempotency_key, image_ref, argv,
          timeout_seconds, container_id, exit_code, failure_reason,
          cancel_requested_at, queued_at, started_at, finished_at, job_id,
          commit_sha, runtime_id, image_digest, params_json, logs_pruned_at;

-- name: ListRunsByJob :many
-- Runs of one job, newest first, matching runs_job_id_idx. Same keyset shape
-- as ListRuns.
SELECT id, principal_id, state, idempotency_key, image_ref, argv,
       timeout_seconds, container_id, exit_code, failure_reason,
       cancel_requested_at, queued_at, started_at, finished_at, job_id,
       commit_sha, runtime_id, image_digest, params_json, logs_pruned_at
FROM runs
WHERE job_id = sqlc.arg(job_id)::bigint
  AND (sqlc.narg(cursor_queued_at)::timestamptz IS NULL
       OR (queued_at, id) < (sqlc.narg(cursor_queued_at)::timestamptz, sqlc.narg(cursor_id)::bigint))
ORDER BY queued_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetRun :one
SELECT id, principal_id, state, idempotency_key, image_ref, argv,
       timeout_seconds, container_id, exit_code, failure_reason,
       cancel_requested_at, queued_at, started_at, finished_at, job_id,
       commit_sha, runtime_id, image_digest, params_json, logs_pruned_at
FROM runs
WHERE id = $1;

-- name: GetRunByIdempotencyKey :one
SELECT id, principal_id, state, idempotency_key, image_ref, argv,
       timeout_seconds, container_id, exit_code, failure_reason,
       cancel_requested_at, queued_at, started_at, finished_at, job_id,
       commit_sha, runtime_id, image_digest, params_json, logs_pruned_at
FROM runs
WHERE principal_id = $1 AND idempotency_key = $2;

-- name: ListRuns :many
-- Keyset (seek) pagination on (queued_at DESC, id DESC), matching
-- runs_queued_at_id_desc_idx. A NULL cursor means "first page"; otherwise
-- the row-wise comparison below is exactly the "strictly after the cursor,
-- in this DESC order" condition.
SELECT id, principal_id, state, idempotency_key, image_ref, argv,
       timeout_seconds, container_id, exit_code, failure_reason,
       cancel_requested_at, queued_at, started_at, finished_at, job_id,
       commit_sha, runtime_id, image_digest, params_json, logs_pruned_at
FROM runs
WHERE sqlc.narg(cursor_queued_at)::timestamptz IS NULL
   OR (queued_at, id) < (sqlc.narg(cursor_queued_at)::timestamptz, sqlc.narg(cursor_id)::bigint)
ORDER BY queued_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: ClaimNextQueuedRun :one
-- The supervisor's claim loop (task 1.12). The CTE's FOR UPDATE SKIP LOCKED
-- picks the oldest queued run not already locked by another supervisor (or
-- another iteration of this same loop, if ever run concurrently), and the
-- outer UPDATE atomically transitions it to running in the same statement -
-- no separate SELECT-then-UPDATE race window. Zero rows back (pgx.ErrNoRows)
-- just means the queue is empty right now, not an error.
WITH claimed AS (
    SELECT id FROM runs
    WHERE state = 'queued'
    ORDER BY queued_at ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE runs
SET state = 'running', started_at = now()
FROM claimed
WHERE runs.id = claimed.id
RETURNING runs.id, runs.principal_id, runs.state, runs.idempotency_key,
          runs.image_ref, runs.argv, runs.timeout_seconds, runs.container_id,
          runs.exit_code, runs.failure_reason, runs.cancel_requested_at,
          runs.queued_at, runs.started_at, runs.finished_at, runs.job_id,
          runs.commit_sha, runs.runtime_id, runs.image_digest,
          runs.params_json, runs.logs_pruned_at;

-- name: FinishRun :execrows
-- Task 1.13. state is always a terminal one here -
-- runs_state_timestamps_check requires finished_at whenever state is
-- terminal, which this always sets.
--
-- The state guard makes a terminal state final (task 1.14): a run that has
-- already succeeded must never be rewritten as lost by a reconciler that
-- was slow to notice, and an outcome must never be overwritten by a stale
-- process. :execrows so the caller can tell "recorded" from "someone else
-- already finished this run" instead of silently clobbering a real result.
UPDATE runs
SET state = $2,
    exit_code = $3,
    container_id = $4,
    failure_reason = $5,
    finished_at = now()
WHERE id = $1
  AND state IN ('queued', 'running');

-- name: ListNonTerminalRuns :many
-- The reconciler's input (task 1.15): every run that isn't in a terminal
-- state, matching runs_active_idx.
SELECT id, principal_id, state, idempotency_key, image_ref, argv,
       timeout_seconds, container_id, exit_code, failure_reason,
       cancel_requested_at, queued_at, started_at, finished_at, job_id,
       commit_sha, runtime_id, image_digest, params_json, logs_pruned_at
FROM runs
WHERE state IN ('queued', 'running');

-- name: SetRunContainerID :exec
-- Records the container as soon as it is created, rather than waiting for
-- FinishRun (found in Phase 1e: a running run showed containerId: null, so
-- there was no way to find the container of a run that was still going -
-- exactly when you most want it). Guarded on state so a run that has
-- somehow already finished is never touched.
UPDATE runs
SET container_id = $2
WHERE id = $1
  AND state = 'running';

-- name: CancelQueuedRun :execrows
-- Task 2.8, the half of cancellation the API can do alone: a queued run has
-- no container, so there is nothing to stop and nothing to ask the supervisor
-- for. State and outcome are written in one guarded statement.
--
-- The state guard is what makes this safe against the claim loop. Both this
-- and ClaimNextQueuedRun update the same row under `state = 'queued'`, so
-- exactly one of them wins: either the run is cancelled before it ever starts,
-- or it is claimed and the caller falls back to RequestRunCancel. There is no
-- window where both happen, and none where neither does.
--
-- cancel_requested_at is set here too even though nothing reads it for a
-- queued run - it is the record that this run was cancelled rather than
-- having failed on its own.
UPDATE runs
SET state = 'cancelled',
    cancel_requested_at = now(),
    finished_at = now()
WHERE id = $1
  AND state = 'queued';

-- name: RequestRunCancel :execrows
-- Task 2.8, the other half: a running run belongs to the supervisor, which
-- holds the container and is the only process allowed to stop it. This
-- records the request; the supervisor performs it and writes the terminal
-- state itself.
--
-- Guarded on 'running' so a request can never land on a run that has already
-- finished, which would leave a terminal run marked as cancellation-pending
-- forever. Setting it twice is harmless - the second call moves the timestamp
-- and changes nothing else - so a client retrying a cancel needs no special
-- handling.
UPDATE runs
SET cancel_requested_at = now()
WHERE id = $1
  AND state = 'running';

-- name: IsRunCancelRequested :one
-- The supervisor's poll while a run is in flight (task 2.8).
--
-- Polled rather than pushed. The api-to-supervisor direction has no
-- notification channel - LISTEN/NOTIFY carries log watermarks the other way
-- (decision #19) - and building one for this would inherit its lossiness,
-- which is tolerable for "there is more output to read" and not tolerable for
-- "stop this run". A cancel that silently does nothing is worse than a cancel
-- that takes a second. Reads the primary key, so the cost is a lookup per
-- second per in-flight run.
SELECT (cancel_requested_at IS NOT NULL)::boolean AS cancel_requested
FROM runs
WHERE id = $1;
