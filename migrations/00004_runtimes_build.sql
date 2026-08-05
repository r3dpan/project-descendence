-- +goose Up

-- Task 4.1. Migration 00001 created runtimes as a skeleton marked "Fleshed
-- out at task 4.1"; this is that. runtimes.image_digest and build_status
-- already exist from the same migration. Nothing has ever written to this
-- table (no query, no handler existed before this phase), so the NOT NULLs
-- below are safe without a DEFAULT - same reasoning as 00003's jobs columns.

-- Which install step to render (ARCHITECTURE.md §4.4's per-language table).
-- lang_manifest's content alone doesn't say which ecosystem it belongs to -
-- a requirements.txt and a package.json are both just text.
ALTER TABLE runtimes ADD COLUMN lang text NOT NULL;

ALTER TABLE runtimes ADD CONSTRAINT runtimes_lang_check
    CHECK (lang IN ('python', 'powershell', 'node'));

-- Hash of (base_image, sys_packages, lang, lang_manifest), computed at create
-- time. Doubles as the build tag, and is what "tag with a hash of the inputs
-- so identical definitions dedupe" (task 4.4) actually dedupes on: two
-- runtimes with identical inputs share a hash and therefore an image, even
-- if an operator created them under two different names.
ALTER TABLE runtimes ADD COLUMN input_hash text NOT NULL;

-- Failure detail, set when build_status = 'failed'. Mirrors runs.failure_reason.
ALTER TABLE runtimes ADD COLUMN build_error text;

-- Mirrors runs.logs_pruned_at (decision #18): the image bytes are reclaimed,
-- but the row - what was built, from which inputs, when - survives, so
-- "never built" and "pruned" stay distinguishable and a job that still
-- references this runtime keeps an explainable history.
ALTER TABLE runtimes ADD COLUMN image_pruned_at timestamptz;

-- The build claim loop (task 4.4/4.5): supervisor claims runtimes with
-- build_status = 'pending', same FOR UPDATE SKIP LOCKED shape as
-- runs_queued_at_queued_idx.
CREATE INDEX runtimes_build_status_pending_idx ON runtimes (created_at) WHERE build_status = 'pending';

-- The prune sweep (task 4.7): candidates are built, unpruned runtimes.
CREATE INDEX runtimes_prunable_idx ON runtimes (built_at) WHERE build_status = 'ready' AND image_pruned_at IS NULL;

-- +goose Down

DROP INDEX runtimes_prunable_idx;
DROP INDEX runtimes_build_status_pending_idx;

ALTER TABLE runtimes DROP COLUMN image_pruned_at;
ALTER TABLE runtimes DROP COLUMN build_error;
ALTER TABLE runtimes DROP COLUMN input_hash;

ALTER TABLE runtimes DROP CONSTRAINT runtimes_lang_check;
ALTER TABLE runtimes DROP COLUMN lang;
