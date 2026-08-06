-- +goose Up

-- Off-plan web UI dashboard work: the SPA's new home page wants to show "you
-- last logged in at X". Only a password login (POST /api/v1/auth/login) is
-- a login event - token-bearer requests authenticate per-request and never
-- touch this column. NULL means "never logged in with a password" (e.g. a
-- kind='token' principal, or a user principal that has only ever used a
-- token so far).
ALTER TABLE principals ADD COLUMN last_login_at timestamptz;

-- +goose Down

ALTER TABLE principals DROP COLUMN last_login_at;
