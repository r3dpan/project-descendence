-- name: TryAdvisoryLock :one
-- Session-level, not transaction-scoped: the caller is expected to hold this
-- connection open (never return it to the pool) for as long as the lock
-- should be held, and explicitly AdvisoryUnlock before releasing it.
-- Postgres also releases it automatically if the connection just dies.
SELECT pg_try_advisory_lock(sqlc.arg(lock_key)::bigint) AS locked;

-- name: AdvisoryUnlock :one
SELECT pg_advisory_unlock(sqlc.arg(lock_key)::bigint) AS was_held;
