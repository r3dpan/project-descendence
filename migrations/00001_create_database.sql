-- +goose Up
 
-- --- Principals ---
-- Auth identities. kind='token' rows carry a SHA-256 hash of an opaque bearer
-- token; kind='user' rows are placeholders for OIDC-backed browser sessions (Phase 7).
CREATE TABLE principals (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind       text        NOT NULL,
    name       text        NOT NULL,
    token_hash bytea,
    token_hint text,
    scopes     text[]      NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    revoked_at timestamptz,
 
    CONSTRAINT principals_name_key       UNIQUE (name),
    CONSTRAINT principals_token_hash_key UNIQUE (token_hash),
 
    CONSTRAINT principals_kind_check   CHECK (kind IN ('user', 'token')),
    CONSTRAINT principals_scopes_check CHECK (scopes <@ ARRAY['read', 'run', 'admin']::text[]),
 
    -- A token principal must carry a hash; a user principal must not.
    CONSTRAINT principals_token_hash_kind_check
        CHECK ((kind = 'token') = (token_hash IS NOT NULL))
);
 
-- --- Repos ---
-- Skeleton. Fleshed out at task 3.1.
CREATE TABLE repos (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text        NOT NULL,
    path       text        NOT NULL,
    kind       text        NOT NULL,
    remote_url text,
    created_at timestamptz NOT NULL DEFAULT now(),
 
    CONSTRAINT repos_name_key UNIQUE (name),
 
    CONSTRAINT repos_kind_check CHECK (kind IN ('local', 'external'))
);
 
-- --- Runtimes ---
-- Skeleton. Fleshed out at task 4.1.
CREATE TABLE runtimes (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text        NOT NULL,
    base_image    text        NOT NULL,
    sys_packages  text[]      NOT NULL DEFAULT '{}',
    lang_manifest text,
    image_digest  text,
    build_status  text        NOT NULL DEFAULT 'pending',
    built_at      timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
 
    CONSTRAINT runtimes_name_key UNIQUE (name),
 
    CONSTRAINT runtimes_build_status_check
        CHECK (build_status IN ('pending', 'building', 'ready', 'failed'))
);
 
-- --- Jobs ---
-- Skeleton. Fleshed out at task 3.1.
CREATE TABLE jobs (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repo_id       bigint      NOT NULL,
    runtime_id    bigint,
    manifest_path text        NOT NULL,
    name          text        NOT NULL,
    enabled       boolean     NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
 
    CONSTRAINT jobs_repo_id_manifest_path_key UNIQUE (repo_id, manifest_path),
 
    CONSTRAINT jobs_repo_id_fkey    FOREIGN KEY (repo_id)    REFERENCES repos(id)    ON DELETE RESTRICT,
    CONSTRAINT jobs_runtime_id_fkey FOREIGN KEY (runtime_id) REFERENCES runtimes(id) ON DELETE SET NULL
);
 
-- --- Runs ---
-- The Phase 1 work queue. A run is claimed by the supervisor with
-- SELECT ... FOR UPDATE SKIP LOCKED, executed as a container, and driven to a
-- terminal state.
CREATE TABLE runs (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    principal_id    bigint      NOT NULL,
    state           text        NOT NULL DEFAULT 'queued',
    idempotency_key text,
 
    -- What to execute. argv is an array, never a shell string (task 1.11).
    image_ref       text        NOT NULL,
    argv            text[]      NOT NULL,
    timeout_seconds integer     NOT NULL DEFAULT 3600,
 
    -- Execution result.
    container_id    text,
    exit_code       integer,
    failure_reason  text,
 
    -- Lifecycle. cancel_requested_at is the only channel from api to supervisor;
    -- the two processes never talk directly (ARCHITECTURE.md §3).
    cancel_requested_at timestamptz,
    queued_at           timestamptz NOT NULL DEFAULT now(),
    started_at          timestamptz,
    finished_at         timestamptz,
 
    -- Populated from Phase 3 onward. Null for ad-hoc Phase 1 runs.
    job_id       bigint,
    commit_sha   text,
    runtime_id   bigint,
    image_digest text,
    params_json  jsonb NOT NULL DEFAULT '{}',
 
    -- Scoped per principal so two clients cannot collide on the same key.
    -- Postgres treats NULLs as distinct, so unkeyed runs never conflict.
    CONSTRAINT runs_principal_id_idempotency_key_key UNIQUE (principal_id, idempotency_key),
 
    CONSTRAINT runs_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id) ON DELETE RESTRICT,
    CONSTRAINT runs_job_id_fkey       FOREIGN KEY (job_id)       REFERENCES jobs(id)       ON DELETE SET NULL,
    CONSTRAINT runs_runtime_id_fkey   FOREIGN KEY (runtime_id)   REFERENCES runtimes(id)   ON DELETE SET NULL,
 
    CONSTRAINT runs_state_check
        CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'lost')),
    CONSTRAINT runs_argv_check            CHECK (cardinality(argv) > 0),
    CONSTRAINT runs_timeout_seconds_check CHECK (timeout_seconds > 0),
 
    -- Catches a terminal state written without a finish timestamp.
    -- A run cancelled while still queued never started, so terminal states do
    -- not require started_at.
    CONSTRAINT runs_state_timestamps_check CHECK (
           (state = 'queued'
                AND started_at IS NULL
                AND finished_at IS NULL)
        OR (state = 'running'
                AND started_at IS NOT NULL
                AND finished_at IS NULL)
        OR (state IN ('succeeded', 'failed', 'cancelled', 'lost')
                AND finished_at IS NOT NULL)
    )
);
 
-- Claim loop (task 1.12): ORDER BY queued_at ... FOR UPDATE SKIP LOCKED.
CREATE INDEX runs_queued_at_queued_idx ON runs (queued_at) WHERE state = 'queued';
 
-- Reconciler (task 1.15) and cancel poll (task 2.8): non-terminal runs only.
CREATE INDEX runs_active_idx ON runs (id) WHERE state IN ('queued', 'running');
 
-- Keyset pagination on GET /api/v1/runs (ARCHITECTURE.md §4.9).
CREATE INDEX runs_queued_at_id_desc_idx ON runs (queued_at DESC, id DESC);
 
-- --- Run logs ---
-- Index only. Log bodies live in per-run files (ARCHITECTURE.md §4.1);
-- byte_offset and byte_length point into that file. Fleshed out at task 2.2.
CREATE TABLE run_logs (
    run_id      bigint      NOT NULL,
    seq         bigint      NOT NULL,
    stream      text        NOT NULL,
    ts          timestamptz NOT NULL,
    byte_offset bigint      NOT NULL,
    byte_length integer     NOT NULL,
 
    CONSTRAINT run_logs_pkey PRIMARY KEY (run_id, seq),
 
    CONSTRAINT run_logs_run_id_fkey FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE,
 
    CONSTRAINT run_logs_stream_check      CHECK (stream IN ('stdout', 'stderr')),
    CONSTRAINT run_logs_byte_length_check CHECK (byte_length >= 0)
);
 
-- --- Schedules ---
-- Skeleton. Fleshed out at task 5.2. next_due_at assumes an in-process
-- scheduler; it becomes dead weight under generated systemd timers
-- (open question, ARCHITECTURE.md §8).
CREATE TABLE schedules (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id      bigint      NOT NULL,
    cron_expr   text        NOT NULL,
    timezone    text        NOT NULL DEFAULT 'UTC',
    next_due_at timestamptz,
    enabled     boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
 
    CONSTRAINT schedules_job_id_fkey FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
 
-- --- Audit ---
-- Skeleton. principal_id nulls out on principal deletion rather than blocking
-- it, so the audit row survives.
CREATE TABLE audit (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    principal_id bigint,
    action       text        NOT NULL,
    target       text,
    ts           timestamptz NOT NULL DEFAULT now(),
    detail_json  jsonb       NOT NULL DEFAULT '{}',
 
    CONSTRAINT audit_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id) ON DELETE SET NULL
);
 
-- +goose Down
 
-- Reverse of the creation order. Indexes drop with their tables.
DROP TABLE audit;
DROP TABLE schedules;
DROP TABLE run_logs;
DROP TABLE runs;
DROP TABLE jobs;
DROP TABLE runtimes;
DROP TABLE repos;
DROP TABLE principals;