-- +goose Up

-- Task 3.1. Migration 00001 created repos and jobs as skeletons, both marked
-- "Fleshed out at task 3.1"; this is that. runs.job_id and runs.commit_sha
-- already exist from the same migration, so runs needs only an index here.
--
-- The model these columns encode (ARCHITECTURE.md §4.5, and the decision this
-- phase rests on): a job is a script's *interface*, authored in git and
-- versioned alongside the script it describes. This table is a **projection**
-- of the manifests found in a repo - derived state, regenerable at any time by
-- re-scanning. `enabled` is the only column here that git does not own.

-- --- Repos ---

-- Which ref a scan reads. Manifests are discovered at this branch's HEAD, and
-- a run pins the commit SHA it resolved to (task 3.5), so moving the branch
-- never changes what an already-recorded run executed.
ALTER TABLE repos ADD COLUMN default_branch text NOT NULL DEFAULT 'main';

-- Observability for the sync (task 3.4), not an input to it: a scan always
-- re-reads HEAD rather than diffing against what it saw last. Storing a
-- "last seen" and trusting it would make a failed half-sync invisible.
ALTER TABLE repos ADD COLUMN last_synced_at         timestamptz;
ALTER TABLE repos ADD COLUMN last_synced_commit_sha text;

-- --- Jobs ---

-- Every column below is copied from the manifest by the sync and overwritten
-- on the next one. None of them is editable through the API - editing a job
-- means committing a manifest (task 3.7). The NOT NULLs are safe without a
-- DEFAULT because nothing has ever written to this table: it has had no
-- queries and no handlers since 00001 created it.
ALTER TABLE jobs ADD COLUMN description text;
ALTER TABLE jobs ADD COLUMN script_path text NOT NULL;

-- NULL means "no explicit command" - the script is delivered executable and
-- argv is just its path, so the shebang chooses the interpreter. A manifest
-- may override with an explicit command; an empty array never means anything,
-- hence the cardinality check rather than allowing '{}'.
ALTER TABLE jobs ADD COLUMN command text[];

-- Nullable, and deliberately so. In Phase 3 a manifest names an image
-- directly. Phase 4 introduces runtimes, where a job names a runtime and the
-- image is whatever that runtime last built - so the invariant is "one of the
-- two", not "always an image". Encoding that now costs one constraint and
-- saves altering a NOT NULL column later.
ALTER TABLE jobs ADD COLUMN image_ref text;

-- NULL means the manifest did not say, and the platform default applies - the
-- same default an ad-hoc run gets. Held here so that a job can be described
-- without reading git, which is the whole reason this projection exists.
ALTER TABLE jobs ADD COLUMN timeout_seconds integer;

-- Which commit this row was built from. Not used to decide anything - it is
-- how you answer "is what I am looking at current?" without re-reading git.
ALTER TABLE jobs ADD COLUMN synced_commit_sha text NOT NULL;
ALTER TABLE jobs ADD COLUMN synced_at         timestamptz NOT NULL DEFAULT now();

-- A manifest that disappears from the repo soft-deletes its job rather than
-- deleting the row. runs.job_id is ON DELETE SET NULL, so a hard delete would
-- quietly sever every past run from the job it ran - directly against the
-- reproducibility principle (§2.4). Keeping the row also means a manifest that
-- comes back resurrects the *same* job id, and its run history with it,
-- because UNIQUE (repo_id, manifest_path) still matches it.
ALTER TABLE jobs ADD COLUMN deleted_at timestamptz;

ALTER TABLE jobs ADD CONSTRAINT jobs_command_check
    CHECK (command IS NULL OR cardinality(command) > 0);

ALTER TABLE jobs ADD CONSTRAINT jobs_image_or_runtime_check
    CHECK (image_ref IS NOT NULL OR runtime_id IS NOT NULL);

ALTER TABLE jobs ADD CONSTRAINT jobs_timeout_seconds_check
    CHECK (timeout_seconds IS NULL OR timeout_seconds > 0);

-- `descendence jobs run <name>` addresses a job by name, so a name must
-- identify exactly one live job. Scoped to live jobs only: a deleted job keeps
-- its name (its runs still refer to it) without blocking a new manifest from
-- claiming it. A collision between two repos is a sync error the operator
-- resolves by renaming, not something the platform can pick a winner for.
CREATE UNIQUE INDEX jobs_name_live_idx ON jobs (name) WHERE deleted_at IS NULL;

-- Keyset pagination on GET /api/v1/jobs. Ordered by name rather than by time:
-- a job list is a catalogue, not a timeline, which is the opposite of runs.
CREATE INDEX jobs_name_id_idx ON jobs (name, id);

-- --- Runs ---

-- "Runs of this job" - the one access path Phase 3 adds to a table that
-- already had four. job_id is NULL for every ad-hoc run, which is most of
-- them so far, so this is partial for the same reason runs_active_idx is.
CREATE INDEX runs_job_id_idx ON runs (job_id) WHERE job_id IS NOT NULL;

-- +goose Down

DROP INDEX runs_job_id_idx;
DROP INDEX jobs_name_id_idx;
DROP INDEX jobs_name_live_idx;

ALTER TABLE jobs DROP CONSTRAINT jobs_timeout_seconds_check;
ALTER TABLE jobs DROP CONSTRAINT jobs_image_or_runtime_check;
ALTER TABLE jobs DROP CONSTRAINT jobs_command_check;

ALTER TABLE jobs DROP COLUMN deleted_at;
ALTER TABLE jobs DROP COLUMN synced_at;
ALTER TABLE jobs DROP COLUMN synced_commit_sha;
ALTER TABLE jobs DROP COLUMN timeout_seconds;
ALTER TABLE jobs DROP COLUMN image_ref;
ALTER TABLE jobs DROP COLUMN command;
ALTER TABLE jobs DROP COLUMN script_path;
ALTER TABLE jobs DROP COLUMN description;

ALTER TABLE repos DROP COLUMN last_synced_commit_sha;
ALTER TABLE repos DROP COLUMN last_synced_at;
ALTER TABLE repos DROP COLUMN default_branch;
