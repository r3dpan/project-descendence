-- name: UpsertJob :one
-- The write half of a scan (task 3.4).
--
-- Every column set here is copied from a manifest and owned by git. `enabled`
-- is conspicuously absent from the UPDATE and that is the point: it is the one
-- fact about a job that this installation owns rather than the repository, so
-- a sync must never reset an operator's decision to pause a job. If `enabled`
-- ever appears in this statement, pausing a misbehaving job becomes something
-- the next sync silently undoes.
--
-- deleted_at is cleared, which is how a manifest that comes back resurrects
-- the *same* job row - and with it every past run that points at that id.
INSERT INTO jobs (repo_id, manifest_path, name, description, script_path,
                  command, image_ref, timeout_seconds, synced_commit_sha)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (repo_id, manifest_path) DO UPDATE
SET name              = EXCLUDED.name,
    description       = EXCLUDED.description,
    script_path       = EXCLUDED.script_path,
    command           = EXCLUDED.command,
    image_ref         = EXCLUDED.image_ref,
    timeout_seconds   = EXCLUDED.timeout_seconds,
    synced_commit_sha = EXCLUDED.synced_commit_sha,
    synced_at         = now(),
    deleted_at        = NULL
RETURNING id, repo_id, runtime_id, manifest_path, name, enabled, created_at,
          description, script_path, command, image_ref, timeout_seconds,
          synced_commit_sha, synced_at, deleted_at;

-- name: SoftDeleteJobsNotIn :many
-- The other half of a scan: manifests that were there last time and are not
-- there now.
--
-- Soft, because runs.job_id is ON DELETE SET NULL - a hard delete would
-- quietly sever every past run from the job it ran, which is the one thing
-- §2.4 (a past run must be explainable) does not allow. An empty keep_paths
-- array correctly deletes every job in the repository: a repository whose
-- manifests have all been removed has no jobs.
UPDATE jobs
SET deleted_at = now()
WHERE repo_id = $1
  AND deleted_at IS NULL
  AND manifest_path <> ALL(sqlc.arg(keep_paths)::text[])
RETURNING id, name, manifest_path;

-- name: ListJobsByRepo :many
-- Every job of a repository, soft-deleted ones included. Used by the scan to
-- work out what is new, what changed and what vanished before it writes
-- anything - which is also why it must not filter on deleted_at, since a
-- resurrected manifest has to be recognised as the row it already owns.
SELECT id, repo_id, runtime_id, manifest_path, name, enabled, created_at,
       description, script_path, command, image_ref, timeout_seconds,
       synced_commit_sha, synced_at, deleted_at
FROM jobs
WHERE repo_id = $1
ORDER BY manifest_path ASC;

-- name: GetJob :one
-- Does not filter soft-deleted jobs. A run keeps pointing at its job after the
-- manifest is gone, and "what did this run execute" must still answer.
SELECT id, repo_id, runtime_id, manifest_path, name, enabled, created_at,
       description, script_path, command, image_ref, timeout_seconds,
       synced_commit_sha, synced_at, deleted_at
FROM jobs
WHERE id = $1;

-- name: GetJobByName :one
-- Live jobs only: `descendence jobs run <name>` must not resolve to a job
-- whose manifest has been deleted. Names are unique among live jobs
-- (jobs_name_live_idx), so this returns at most one row.
SELECT id, repo_id, runtime_id, manifest_path, name, enabled, created_at,
       description, script_path, command, image_ref, timeout_seconds,
       synced_commit_sha, synced_at, deleted_at
FROM jobs
WHERE name = $1 AND deleted_at IS NULL;

-- name: ListJobs :many
-- Keyset pagination on (name, id) ASC, matching jobs_name_id_idx. Ordered by
-- name because a job list is a catalogue, not a timeline - the opposite of
-- ListRuns, whose newest row is the interesting one.
SELECT id, repo_id, runtime_id, manifest_path, name, enabled, created_at,
       description, script_path, command, image_ref, timeout_seconds,
       synced_commit_sha, synced_at, deleted_at
FROM jobs
WHERE deleted_at IS NULL
  AND (sqlc.narg(cursor_name)::text IS NULL
       OR (name, id) > (sqlc.narg(cursor_name)::text, sqlc.narg(cursor_id)::bigint))
ORDER BY name ASC, id ASC
LIMIT sqlc.arg(row_limit);

-- name: SetJobEnabled :execrows
-- The only mutation the API performs on a job, and the only column git does
-- not own. :execrows so the handler can tell "no such live job" (0 rows) from
-- a successful write, rather than reporting success for a job that is not
-- there.
UPDATE jobs
SET enabled = $2
WHERE id = $1 AND deleted_at IS NULL;
