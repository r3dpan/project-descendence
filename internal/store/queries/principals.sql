-- name: GetPrincipalByTokenHash :one
SELECT id, kind, name, token_hash, token_hint, password_hash, created_at, expires_at, revoked_at, last_login_at
FROM principals
WHERE token_hash = $1
  AND kind = 'token'
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

-- name: CreateTokenPrincipal :one
INSERT INTO principals (kind, name, token_hash, token_hint, expires_at)
VALUES ('token', $1, $2, $3, $4)
RETURNING id, kind, name, token_hash, token_hint, password_hash, created_at, expires_at, revoked_at, last_login_at;

-- name: GetUserPrincipalByName :one
-- Returns last_login_at as it stood *before* this call - LoginHandler reads
-- it here, then overwrites it via TouchPrincipalLastLogin, so the value
-- returned to the client is genuinely "when you logged in last time", not
-- "now".
SELECT id, kind, name, token_hash, token_hint, password_hash, created_at, expires_at, revoked_at, last_login_at
FROM principals
WHERE name = $1
  AND kind = 'user'
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

-- name: CreateUserPrincipal :one
INSERT INTO principals (kind, name, password_hash)
VALUES ('user', $1, $2)
RETURNING id, kind, name, token_hash, token_hint, password_hash, created_at, expires_at, revoked_at, last_login_at;

-- name: GetPrincipalByID :one
SELECT id, kind, name, token_hash, token_hint, password_hash, created_at, expires_at, revoked_at, last_login_at
FROM principals
WHERE id = $1
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

-- name: ListPrincipalsByKind :many
-- Task 8.2/8.3: the users/tokens list endpoints. Unpaginated - a homelab has
-- a handful of principals, not thousands, matching ListSchedulesByJob's
-- reasoning for skipping cursor pagination.
SELECT id, kind, name, token_hash, token_hint, password_hash, created_at, expires_at, revoked_at, last_login_at
FROM principals
WHERE kind = $1
ORDER BY name;

-- name: TouchPrincipalLastLogin :execrows
-- Called by LoginHandler right after password verification succeeds - see
-- GetUserPrincipalByName's comment for why the two queries are sequenced
-- this way rather than combined into one.
UPDATE principals
SET last_login_at = now()
WHERE id = $1;

-- name: RevokePrincipal :execrows
-- Soft-revoke, never a hard delete: runs.principal_id is ON DELETE RESTRICT,
-- and revoked_at IS NULL is already the filter every lookup query above
-- applies, so a revoked principal simply stops authenticating - its run
-- history stays queryable and explainable.
UPDATE principals
SET revoked_at = now()
WHERE id = $1
  AND revoked_at IS NULL;

-- name: UpdatePrincipalPasswordHash :execrows
UPDATE principals
SET password_hash = $2
WHERE id = $1;
