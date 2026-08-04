-- name: CreateRun :one
INSERT INTO runs (principal_id, image_ref, argv, timeout_seconds)
VALUES ($1, $2, $3, $4)
RETURNING id, principal_id, state, idempotency_key, image_ref, argv,
          timeout_seconds, container_id, exit_code, failure_reason,
          cancel_requested_at, queued_at, started_at, finished_at, job_id,
          commit_sha, runtime_id, image_digest, params_json;
