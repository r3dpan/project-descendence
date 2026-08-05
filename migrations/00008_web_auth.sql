-- +goose Up

-- Phase 7 (task 7.3). Migration 00001's principals comment called
-- kind='user' rows "placeholders for OIDC-backed browser sessions" - that
-- was accurate then, but ARCHITECTURE.md §4.10 always allowed "a login form
-- against a local account is fine first," and OIDC stays deferred past
-- Phase 7 (§7). A user principal now carries a bcrypt password hash, same
-- shape as a token principal's token_hash: hash-only storage, a symmetric
-- CHECK tying the column to kind, and nothing that lets a database dump
-- alone authenticate as the principal.
ALTER TABLE principals ADD COLUMN password_hash bytea;

ALTER TABLE principals DROP CONSTRAINT principals_token_hash_kind_check;
ALTER TABLE principals ADD CONSTRAINT principals_token_hash_kind_check
    CHECK ((kind = 'token') = (token_hash IS NOT NULL));
ALTER TABLE principals ADD CONSTRAINT principals_password_hash_kind_check
    CHECK ((kind = 'user') = (password_hash IS NOT NULL));

-- Browser sessions. A session cookie carries an opaque random value; only
-- its hash is ever stored, matching principals.token_hash's reasoning.
-- Logout deletes the row outright - a dead session has no history worth a
-- soft-delete for, unlike a run or a job.
CREATE TABLE sessions (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    principal_id bigint      NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    token_hash   bytea       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,

    CONSTRAINT sessions_token_hash_key UNIQUE (token_hash)
);

CREATE INDEX sessions_principal_id_idx ON sessions (principal_id);

-- +goose Down

DROP TABLE sessions;

ALTER TABLE principals DROP CONSTRAINT principals_password_hash_kind_check;
ALTER TABLE principals DROP CONSTRAINT principals_token_hash_kind_check;
ALTER TABLE principals ADD CONSTRAINT principals_token_hash_kind_check
    CHECK ((kind = 'token') = (token_hash IS NOT NULL));

ALTER TABLE principals DROP COLUMN password_hash;
