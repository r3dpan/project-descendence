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
