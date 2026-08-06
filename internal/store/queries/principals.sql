-- name: GetPrincipalByTokenHash :one
SELECT id, kind, name, token_hash, token_hint, oidc_issuer, oidc_subject, created_at, expires_at, revoked_at, last_login_at
FROM principals
WHERE token_hash = $1
  AND kind = 'token'
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

-- name: CreateTokenPrincipal :one
INSERT INTO principals (kind, name, token_hash, token_hint, expires_at)
VALUES ('token', $1, $2, $3, $4)
RETURNING id, kind, name, token_hash, token_hint, oidc_issuer, oidc_subject, created_at, expires_at, revoked_at, last_login_at;

-- name: GetUserPrincipalByOIDCSubject :one
-- Task 9.6's callback lookup. Deliberately does not filter revoked_at -
-- unlike every other lookup here, the callback needs to tell "unknown
-- subject" (JIT-provision) apart from "known but revoked" (refuse, never
-- resurrect) rather than have both collapse into "not found".
SELECT id, kind, name, token_hash, token_hint, oidc_issuer, oidc_subject, created_at, expires_at, revoked_at, last_login_at
FROM principals
WHERE oidc_issuer = $1
  AND oidc_subject = $2
  AND kind = 'user';

-- name: CreateUserPrincipalOIDC :one
-- JIT provisioning (task 9.6): a first-time subject gets a row with no role
-- assigned yet - RequirePermission then denies everything until an admin
-- calls SetPrincipalRole, which is the "roleless principal" state task 9.11's
-- web UI has to render an explanatory screen for instead of a wall of 403s.
INSERT INTO principals (kind, name, oidc_issuer, oidc_subject)
VALUES ('user', $1, $2, $3)
RETURNING id, kind, name, token_hash, token_hint, oidc_issuer, oidc_subject, created_at, expires_at, revoked_at, last_login_at;

-- name: GetPrincipalByID :one
SELECT id, kind, name, token_hash, token_hint, oidc_issuer, oidc_subject, created_at, expires_at, revoked_at, last_login_at
FROM principals
WHERE id = $1
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

-- name: ListPrincipalsByKind :many
-- Task 8.2/8.3: the users/tokens list endpoints. Unpaginated - a homelab has
-- a handful of principals, not thousands, matching ListSchedulesByJob's
-- reasoning for skipping cursor pagination.
SELECT id, kind, name, token_hash, token_hint, oidc_issuer, oidc_subject, created_at, expires_at, revoked_at, last_login_at
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
