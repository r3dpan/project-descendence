-- name: CreateRuntime :one
-- input_hash is computed by the caller (internal/runtimebuild) from
-- (base_image, sys_packages, lang, lang_manifest) - the same hash doubles as
-- the build tag, so it is owned by the renderer, not by SQL.
INSERT INTO runtimes (name, base_image, sys_packages, lang, lang_manifest, input_hash)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, name, base_image, sys_packages, lang_manifest, image_digest,
          build_status, built_at, created_at, lang, input_hash, build_error,
          image_pruned_at;

-- name: GetRuntime :one
SELECT id, name, base_image, sys_packages, lang_manifest, image_digest,
       build_status, built_at, created_at, lang, input_hash, build_error,
       image_pruned_at
FROM runtimes
WHERE id = $1;

-- name: GetRuntimeByName :one
SELECT id, name, base_image, sys_packages, lang_manifest, image_digest,
       build_status, built_at, created_at, lang, input_hash, build_error,
       image_pruned_at
FROM runtimes
WHERE name = $1;

-- name: ListRuntimes :many
-- Keyset pagination on (name, id) ASC, same shape as ListJobs - a runtime
-- list is a catalogue, not a timeline.
SELECT id, name, base_image, sys_packages, lang_manifest, image_digest,
       build_status, built_at, created_at, lang, input_hash, build_error,
       image_pruned_at
FROM runtimes
WHERE (sqlc.narg(cursor_name)::text IS NULL
       OR (name, id) > (sqlc.narg(cursor_name)::text, sqlc.narg(cursor_id)::bigint))
ORDER BY name ASC, id ASC
LIMIT sqlc.arg(row_limit);

-- name: ClaimNextPendingRuntimeBuild :one
-- The supervisor's build claim loop (task 4.4/4.5), same CTE shape as
-- ClaimNextQueuedRun: FOR UPDATE SKIP LOCKED picks the oldest pending
-- runtime not already locked, and the outer UPDATE atomically transitions it
-- to building in the same statement - no separate SELECT-then-UPDATE race
-- window. Zero rows back (pgx.ErrNoRows) just means nothing is queued.
WITH claimed AS (
    SELECT id FROM runtimes
    WHERE build_status = 'pending'
    ORDER BY created_at ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE runtimes
SET build_status = 'building'
FROM claimed
WHERE runtimes.id = claimed.id
RETURNING runtimes.id, runtimes.name, runtimes.base_image, runtimes.sys_packages,
          runtimes.lang_manifest, runtimes.image_digest, runtimes.build_status,
          runtimes.built_at, runtimes.created_at, runtimes.lang,
          runtimes.input_hash, runtimes.build_error, runtimes.image_pruned_at;

-- name: RequestRuntimeBuild :execrows
-- The API side of 4.5: queues a build by moving a runtime back to pending.
-- Guarded to runtimes not already pending or building, so a repeat POST
-- /runtimes/{id}/build while one is in flight is rejected (409) rather than
-- silently queuing a second build of the same row - there is only one build
-- slot per runtime, unlike runs which queue freely.
UPDATE runtimes
SET build_status = 'pending', build_error = NULL
WHERE id = $1
  AND build_status NOT IN ('pending', 'building');

-- name: MarkRuntimeReady :execrows
UPDATE runtimes
SET build_status = 'ready',
    image_digest = $2,
    built_at = now(),
    build_error = NULL,
    image_pruned_at = NULL
WHERE id = $1
  AND build_status = 'building';

-- name: MarkRuntimeFailed :execrows
UPDATE runtimes
SET build_status = 'failed',
    build_error = $2
WHERE id = $1
  AND build_status = 'building';

-- name: ListPrunableRuntimes :many
-- Candidates for the prune sweep (task 4.7): built, not yet pruned, and
-- built before the cutoff the caller supplies. "Unused" (not referenced by
-- any recent run) is checked separately in Go against ListRuntimeIDsInUseSince,
-- since expressing "no run in the last N days" and "no run ever, on a
-- still-live job" as one SQL predicate would bury two different questions in
-- one WHERE clause.
SELECT id, name, base_image, sys_packages, lang_manifest, image_digest,
       build_status, built_at, created_at, lang, input_hash, build_error,
       image_pruned_at
FROM runtimes
WHERE build_status = 'ready'
  AND image_pruned_at IS NULL
  AND built_at < $1;

-- name: ListRuntimeIDsInUseSince :many
-- Runtime ids referenced by a run started on or after the cutoff, plus every
-- runtime a live (non-deleted) job still names - the two ways a runtime is
-- "in use" that the prune sweep must not delete out from under.
SELECT runtime_id FROM runs
WHERE runtime_id IS NOT NULL AND queued_at >= $1
UNION
SELECT runtime_id FROM jobs
WHERE runtime_id IS NOT NULL AND deleted_at IS NULL;

-- name: MarkRuntimePruned :execrows
-- Row survives (decision #18's pattern via runs.logs_pruned_at): the image
-- bytes are gone but what was built, from which inputs, when, stays
-- answerable.
UPDATE runtimes
SET image_pruned_at = now()
WHERE id = $1
  AND image_pruned_at IS NULL;
