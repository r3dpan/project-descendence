-- +goose Up

-- Phase 9 (task 9.2). Supersedes decision #29 - local password auth (migration
-- 00008_web_auth.sql) is replaced outright by OIDC, not layered alongside it.
-- password_hash and its symmetric CHECK go away entirely; there is no
-- "either/or" login path to keep.

-- Data migration first, before the column is gone: task 9.1's audit found
-- eight kind='user' principals. Seven have zero rows in runs and are hard
-- deleted outright - runs.principal_id is ON DELETE RESTRICT, so this is only
-- safe for principals nothing references. The eighth (webui-716, id 8) is
-- referenced by 29 runs and was not yet revoked; it is soft-revoked here
-- instead, the same way any other principal is retired - its run history
-- stays queryable, it simply can never authenticate again (it has no
-- oidc_subject and password auth no longer exists).
DELETE FROM principals
    WHERE kind = 'user'
      AND id NOT IN (SELECT DISTINCT principal_id FROM runs WHERE principal_id IS NOT NULL);

UPDATE principals
    SET revoked_at = now()
    WHERE kind = 'user' AND revoked_at IS NULL;

ALTER TABLE principals DROP CONSTRAINT principals_password_hash_kind_check;
ALTER TABLE principals DROP COLUMN password_hash;

ALTER TABLE principals ADD COLUMN oidc_issuer  text;
ALTER TABLE principals ADD COLUMN oidc_subject text;

-- Unlike token_hash's CHECK (every kind='token' row *must* carry a hash),
-- this is one-directional: an oidc_subject may only be set on a kind='user'
-- row, but a kind='user' row is not required to carry one. webui-716 above
-- is exactly why - a legacy/revoked user kept only so runs.principal_id
-- keeps resolving has no oidc identity and will never be issued one; it
-- exists to satisfy the FK, not to authenticate.
ALTER TABLE principals ADD CONSTRAINT principals_oidc_kind_check
    CHECK ((oidc_issuer IS NULL AND oidc_subject IS NULL) OR kind = 'user');

-- A (NULL, NULL) pair never conflicts under a plain UNIQUE constraint -
-- Postgres treats each NULL as distinct - so every non-OIDC principal
-- (including webui-716) is unaffected by this.
ALTER TABLE principals ADD CONSTRAINT principals_oidc_issuer_subject_key
    UNIQUE (oidc_issuer, oidc_subject);

-- +goose Down

-- Down cannot restore password hashes - they were never in this migration's
-- Up in the first place (they existed before it and are unrecoverable once
-- gone). Any kind='user' principal revoked or deleted by this migration's Up
-- stays that way; this only reverses the schema shape, not the data.
ALTER TABLE principals DROP CONSTRAINT principals_oidc_issuer_subject_key;
ALTER TABLE principals DROP CONSTRAINT principals_oidc_kind_check;
ALTER TABLE principals DROP COLUMN oidc_subject;
ALTER TABLE principals DROP COLUMN oidc_issuer;

ALTER TABLE principals ADD COLUMN password_hash bytea;
ALTER TABLE principals ADD CONSTRAINT principals_password_hash_kind_check
    CHECK ((kind = 'user') = (password_hash IS NOT NULL));
