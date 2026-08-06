-- name: CreateSession :one
INSERT INTO sessions (principal_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, principal_id, token_hash, created_at, expires_at;

-- name: GetPrincipalBySessionTokenHash :one
SELECT p.id, p.kind, p.name, p.token_hash, p.token_hint, p.password_hash, p.created_at, p.expires_at, p.revoked_at
FROM sessions s
JOIN principals p ON p.id = s.principal_id
WHERE s.token_hash = $1
  AND s.expires_at > now()
  AND p.revoked_at IS NULL;

-- name: DeleteSessionByTokenHash :exec
DELETE FROM sessions
WHERE token_hash = $1;
