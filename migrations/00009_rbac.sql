-- +goose Up

-- Phase 8. Replaces principals.scopes (a flat text[] capped to
-- {read,run,admin}, enforced by exactly one handler - TriggerScheduleHandler
-- - everywhere else was "any authenticated principal = full access", per
-- ARCHITECTURE.md §7's now-closed "Full RBAC" deferral) with a real
-- roles/permissions model. See ARCHITECTURE.md §6 decision #30 for why this
-- is fixed built-in roles (admin/operator/viewer) rather than an
-- admin-editable custom-role builder, exactly one role per principal rather
-- than a many-to-many assignment, and global (not per-resource-instance)
-- permissions.

CREATE TABLE roles (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text        NOT NULL UNIQUE,
    description text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE permissions (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key         text   NOT NULL UNIQUE,
    description text   NOT NULL
);

CREATE TABLE role_permissions (
    role_id       bigint NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    permission_id bigint NOT NULL REFERENCES permissions (id) ON DELETE CASCADE,

    PRIMARY KEY (role_id, permission_id)
);

-- One role per principal, enforced by the UNIQUE on principal_id (not just
-- the composite PK) - three fixed roles don't need multi-role assignment,
-- and a join table (rather than principals.role_id) keeps that migration
-- cheap later without paying for it now. role_id is ON DELETE RESTRICT: the
-- three built-in roles are never deleted by application code, so a
-- principal losing its role out from under it should never happen silently.
CREATE TABLE principal_roles (
    principal_id bigint NOT NULL UNIQUE REFERENCES principals (id) ON DELETE CASCADE,
    role_id      bigint NOT NULL REFERENCES roles (id) ON DELETE RESTRICT,

    PRIMARY KEY (principal_id, role_id)
);

INSERT INTO permissions (key, description) VALUES
    ('jobs:read',        'List and view jobs'),
    ('jobs:write',       'Enable/disable jobs'),
    ('runs:read',        'List and view runs and their logs'),
    ('runs:trigger',     'Create ad-hoc runs'),
    ('runs:cancel',      'Cancel a running run'),
    ('schedules:read',   'List and view schedules'),
    ('schedules:write',  'Create, edit and delete schedules'),
    ('schedules:trigger','Fire a schedule (what a generated .service unit calls)'),
    ('runtimes:read',    'List and view runtimes'),
    ('runtimes:write',   'Create, build and prune runtimes'),
    ('repos:read',       'List and view repos and their files'),
    ('repos:write',      'Create repos, sync them and write files'),
    ('users:read',       'List and view users, tokens and roles'),
    ('users:write',      'Create, edit and revoke users and tokens');

INSERT INTO roles (name, description) VALUES
    ('admin',    'Full access, including user and token management'),
    ('operator', 'Can run and watch jobs/schedules, cannot create/edit them or manage users'),
    ('viewer',   'Read-only access');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r, permissions p WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r, permissions p
WHERE r.name = 'operator'
  AND p.key IN (
    'jobs:read', 'runs:read', 'runs:trigger', 'runs:cancel',
    'schedules:read', 'schedules:trigger', 'runtimes:read', 'repos:read'
  );

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r, permissions p
WHERE r.name = 'viewer'
  AND p.key LIKE '%:read';

-- Clean cutover, not a compatibility shim - single-operator/homelab
-- deployment (ARCHITECTURE.md §6 decision #30), so there is no fleet of
-- principals whose scopes need a staged migration. Every existing principal
-- (including cmd/seed's bootstrap principal) gets exactly one role, mapped
-- from the highest scope it held: admin > run (now operator) > viewer.
INSERT INTO principal_roles (principal_id, role_id)
SELECT p.id,
    (SELECT id FROM roles WHERE name =
        CASE
            WHEN 'admin' = ANY(p.scopes) THEN 'admin'
            WHEN 'run'   = ANY(p.scopes) THEN 'operator'
            ELSE 'viewer'
        END)
FROM principals p;

ALTER TABLE principals DROP CONSTRAINT principals_scopes_check;
ALTER TABLE principals DROP COLUMN scopes;

-- +goose Down

ALTER TABLE principals ADD COLUMN scopes text[] NOT NULL DEFAULT '{}';

DROP TABLE principal_roles;
DROP TABLE role_permissions;
DROP TABLE permissions;
DROP TABLE roles;
