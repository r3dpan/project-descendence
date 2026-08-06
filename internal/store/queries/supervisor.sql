-- name: TryAdvisoryLock :one
-- Session-level, not transaction-scoped: the caller is expected to hold this
-- connection open (never return it to the pool) for as long as the lock
-- should be held, and explicitly AdvisoryUnlock before releasing it.
-- Postgres also releases it automatically if the connection just dies.
SELECT pg_try_advisory_lock(sqlc.arg(lock_key)::bigint) AS locked;

-- name: AdvisoryUnlock :one
SELECT pg_advisory_unlock(sqlc.arg(lock_key)::bigint) AS was_held;

-- name: UpsertSupervisorHeartbeat :exec
-- Called on a timer by whichever supervisor holds the advisory lock
-- (cmd/supervisor/heartbeat.go). started_at is only ever written by the
-- first INSERT for a process's lifetime - the ON CONFLICT update
-- deliberately omits it, so it survives every later beat unchanged.
INSERT INTO supervisor_heartbeat (id, last_beat_at, started_at)
VALUES (1, sqlc.arg(beat_at)::timestamptz, sqlc.arg(started_at)::timestamptz)
ON CONFLICT (id) DO UPDATE SET last_beat_at = EXCLUDED.last_beat_at;

-- name: GetSupervisorHeartbeat :one
-- pgx.ErrNoRows means no supervisor has ever beaten - callers (internal/api's
-- SystemStatusHandler) must treat that as "not running", not as a query
-- failure.
SELECT last_beat_at, started_at FROM supervisor_heartbeat WHERE id = 1;
