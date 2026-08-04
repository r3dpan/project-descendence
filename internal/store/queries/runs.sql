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
          commit_sha, runtime_id, image_digest, params_json;

-- name: GetRun :one
SELECT id, principal_id, state, idempotency_key, image_ref, argv,
       timeout_seconds, container_id, exit_code, failure_reason,
       cancel_requested_at, queued_at, started_at, finished_at, job_id,
       commit_sha, runtime_id, image_digest, params_json
FROM runs
WHERE id = $1;

-- name: GetRunByIdempotencyKey :one
SELECT id, principal_id, state, idempotency_key, image_ref, argv,
       timeout_seconds, container_id, exit_code, failure_reason,
       cancel_requested_at, queued_at, started_at, finished_at, job_id,
       commit_sha, runtime_id, image_digest, params_json
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
       commit_sha, runtime_id, image_digest, params_json
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
          runs.params_json;
