-- +goose Up

-- Off-plan web UI dashboard work: showing "is the supervisor running" needs
-- a signal the api process can actually read. The only existing liveness
-- primitive is the advisory lock in cmd/supervisor/lock.go, held on the
-- supervisor's own dedicated connection - not introspectable for "who holds
-- it" from a different connection/process. This table is that signal: a
-- true singleton row (enforced by the fixed id + CHECK, since there is no
-- natural business key), upserted periodically by the lock-holding
-- supervisor. last_beat_at moves every tick; started_at is set once per
-- process lifetime (the upsert never overwrites it) so "since when has the
-- current supervisor process been up" is available for free in the same
-- row.
CREATE TABLE supervisor_heartbeat (
    id           smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_beat_at timestamptz NOT NULL,
    started_at   timestamptz NOT NULL
);

-- +goose Down

DROP TABLE supervisor_heartbeat;
