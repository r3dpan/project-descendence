-- name: GetPrincipalByTokenHash :one
SELECT id, kind, name, token_hash, token_hint, scopes, created_at, expires_at, revoked_at
FROM principals
WHERE token_hash = $1
  AND kind = 'token'
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

-- name: CreateTokenPrincipal :one
INSERT INTO principals (kind, name, token_hash, token_hint, scopes)
VALUES ('token', $1, $2, $3, $4)
RETURNING id, kind, name, token_hash, token_hint, scopes, created_at, expires_at, revoked_at;
