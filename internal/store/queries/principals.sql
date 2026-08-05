-- name: GetPrincipalByTokenHash :one
SELECT id, kind, name, token_hash, token_hint, scopes, password_hash, created_at, expires_at, revoked_at
FROM principals
WHERE token_hash = $1
  AND kind = 'token'
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

-- name: CreateTokenPrincipal :one
INSERT INTO principals (kind, name, token_hash, token_hint, scopes)
VALUES ('token', $1, $2, $3, $4)
RETURNING id, kind, name, token_hash, token_hint, scopes, password_hash, created_at, expires_at, revoked_at;

-- name: GetUserPrincipalByName :one
SELECT id, kind, name, token_hash, token_hint, scopes, password_hash, created_at, expires_at, revoked_at
FROM principals
WHERE name = $1
  AND kind = 'user'
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

-- name: CreateUserPrincipal :one
INSERT INTO principals (kind, name, password_hash, scopes)
VALUES ('user', $1, $2, $3)
RETURNING id, kind, name, token_hash, token_hint, scopes, password_hash, created_at, expires_at, revoked_at;

-- name: GetPrincipalByID :one
SELECT id, kind, name, token_hash, token_hint, scopes, password_hash, created_at, expires_at, revoked_at
FROM principals
WHERE id = $1
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());
