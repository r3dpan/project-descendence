-- name: GetPrincipalPermissions :many
-- One indexed join per request (principal_roles' PK covers principal_id),
-- same cost class as the token/session lookups in principals.sql/sessions.sql
-- - not a new tier of expense. Called once per request by RequireAuth and
-- collected into a set; RequirePermission then does an in-memory lookup.
SELECT p.key
FROM principal_roles pr
JOIN role_permissions rp ON rp.role_id = pr.role_id
JOIN permissions p ON p.id = rp.permission_id
WHERE pr.principal_id = $1;

-- name: GetPrincipalRoleName :one
-- Powers whoami's role field and the "which role does this principal have"
-- lookups in the users/tokens API - a principal has exactly one role
-- (principal_roles.principal_id is UNIQUE).
SELECT r.name
FROM principal_roles pr
JOIN roles r ON r.id = pr.role_id
WHERE pr.principal_id = $1;

-- name: GetRoleByName :one
SELECT id, name, description, created_at FROM roles WHERE name = $1;

-- name: ListRoles :many
SELECT id, name, description, created_at FROM roles ORDER BY id;

-- name: ListRolePermissionKeys :many
-- Task 8.4's GET /api/v1/roles response includes each role's permission
-- list, read-only (decision #30: fixed built-in roles, no editor).
SELECT p.key
FROM role_permissions rp
JOIN permissions p ON p.id = rp.permission_id
WHERE rp.role_id = $1
ORDER BY p.key;

-- name: SetPrincipalRole :exec
-- Used both by cmd/seed (bootstrap, bypassing the API/RequirePermission by
-- construction the same way it already bypasses "you need users:write" by
-- not calling the API at all) and by the users/tokens API's create and
-- role-reassignment handlers. principal_id is UNIQUE, so this is an upsert:
-- assigning a new role to an already-role'd principal replaces it rather
-- than erroring, since "exactly one role" is the invariant, not "insert
-- once".
INSERT INTO principal_roles (principal_id, role_id)
VALUES ($1, $2)
ON CONFLICT (principal_id) DO UPDATE SET role_id = EXCLUDED.role_id;
