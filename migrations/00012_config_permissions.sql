-- +goose Up

-- Off-plan web UI work: a new Configuration page lets an admin view/edit the
-- DATABASE_URL/PODMAN_SOCKET config file (internal/appconfig). Split into
-- its own migration, separate from 00011's heartbeat table, matching this
-- repo's one-concern-per-migration convention.
--
-- 00009_rbac.sql's admin grant ("SELECT r.id, p.id FROM roles r, permissions
-- p WHERE r.name = 'admin'", no p.key filter) ran once at migration time -
-- it does not retroactively cover permissions inserted afterward, so the new
-- keys need their own explicit grant here. Admin-only, deliberately: this is
-- infrastructure connection state, not day-to-day operation.
INSERT INTO permissions (key, description) VALUES
    ('config:read',  'View the DATABASE_URL/PODMAN_SOCKET configuration file'),
    ('config:write', 'Edit the DATABASE_URL/PODMAN_SOCKET configuration file');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r, permissions p
WHERE r.name = 'admin' AND p.key IN ('config:read', 'config:write');

-- +goose Down

DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE key IN ('config:read', 'config:write')
);
DELETE FROM permissions WHERE key IN ('config:read', 'config:write');
