-- +goose Up

-- Task 6.1. The parameter contract (name, type, required, default, secret)
-- lives in the manifest and is authoritative (decision #23) - this column is
-- the same projection treatment jobsync already gives every other manifest
-- field, so GET /api/v1/jobs/{id} can answer "what params does this job
-- take" without the caller having to read git. jsonb rather than a child
-- table: the contract is small, always read and written whole, and never
-- queried by its contents - the same reasoning runs.params_json (00001)
-- already applied to a run's resolved values.
ALTER TABLE jobs ADD COLUMN params_json jsonb NOT NULL DEFAULT '[]';

-- +goose Down

ALTER TABLE jobs DROP COLUMN params_json;
