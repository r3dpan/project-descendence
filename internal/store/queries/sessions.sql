-- name: CreateSession :one
INSERT INTO sessions (principal_id, token_hash, expires_at, id_token)
VALUES ($1, $2, $3, $4)
RETURNING id, principal_id, token_hash, created_at, expires_at, id_token;

-- name: GetPrincipalBySessionTokenHash :one
SELECT p.id, p.kind, p.name, p.token_hash, p.token_hint, p.oidc_issuer, p.oidc_subject, p.created_at, p.expires_at, p.revoked_at, p.last_login_at
FROM sessions s
JOIN principals p ON p.id = s.principal_id
WHERE s.token_hash = $1
  AND s.expires_at > now()
  AND p.revoked_at IS NULL;

-- name: DeleteSessionByTokenHash :one
-- Returns the deleted row's id_token so LogoutHandler can pass it as
-- id_token_hint to the IdP's end_session_endpoint (RP-Initiated Logout) in
-- the same request that ends the local session - no separate lookup query
-- needed first.
DELETE FROM sessions
WHERE token_hash = $1
RETURNING id_token;
