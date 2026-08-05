-- +goose Up

-- Task 5.2. Migration 00001 created schedules as a skeleton marked "Fleshed
-- out at task 5.2"; this is that. schedules.id/job_id/cron_expr/timezone/
-- enabled/created_at already exist from that migration.
--
-- next_due_at, also from the 00001 skeleton, is deliberately NOT kept.
-- Phase 5 resolved ARCHITECTURE.md §8's open question in favour of
-- generated systemd timers (decision #27), not an in-process scheduler -
-- nothing ever computes or reads next_due_at as stored state under that
-- design, and keeping an unused nullable column that looks load-bearing
-- would invite a second, competing source of truth for "when does this
-- fire" alongside systemd itself. Display-only next-fire estimates are
-- computed on the fly via robfig/cron, never stored.
ALTER TABLE schedules DROP COLUMN next_due_at;

-- What happens after supervisor/systemd downtime causes a missed window
-- (task 5.4). Maps directly onto the generated .timer unit's Persistent=
-- directive: skip -> Persistent=false (default), catch_up -> Persistent=true
-- (fires once to catch up, not once per missed occurrence - see decision #27
-- for why that reading was chosen).
ALTER TABLE schedules ADD COLUMN catch_up_policy text NOT NULL DEFAULT 'skip';

ALTER TABLE schedules ADD CONSTRAINT schedules_catch_up_policy_check
    CHECK (catch_up_policy IN ('skip', 'catch_up'));

-- What happens when a fire is due but the schedule's previous run hasn't
-- reached a terminal state yet (task 5.6). Enforced in the trigger endpoint,
-- not the unit. queue and concurrent are behaviorally identical today - the
-- supervisor's run-claim loop already executes runs strictly one at a time -
-- see decision #27's note on this; the distinction is preserved as stored
-- data for when real concurrency exists, not built as two code paths now.
ALTER TABLE schedules ADD COLUMN overlap_policy text NOT NULL DEFAULT 'skip';

ALTER TABLE schedules ADD CONSTRAINT schedules_overlap_policy_check
    CHECK (overlap_policy IN ('skip', 'queue', 'concurrent'));

-- Schedule CRUD (task 5.7) mutates this row directly, unlike jobs' sync-only
-- projection - there's no "synced_at" concept here, just a plain updated_at.
ALTER TABLE schedules ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- "List schedules for a job" (task 5.7) and the supervisor's schedule-sync
-- loop's per-job lookups (task 5.3).
CREATE INDEX schedules_job_id_idx ON schedules (job_id);

-- Which run, if any, a schedule most recently created - the overlap-skip
-- check (task 5.6) asks "is that run still non-terminal". NULL for every
-- ad-hoc run, which is most of them, so partial for the same reason
-- runs_job_id_idx is.
ALTER TABLE runs ADD COLUMN schedule_id bigint REFERENCES schedules(id) ON DELETE SET NULL;

-- ON DELETE SET NULL, not CASCADE, mirroring job_id: deleting a schedule
-- must not sever a past run's explainability (decision #23's reasoning,
-- applied here too).
CREATE INDEX runs_schedule_id_idx ON runs (schedule_id) WHERE schedule_id IS NOT NULL;

-- +goose Down

DROP INDEX runs_schedule_id_idx;
ALTER TABLE runs DROP COLUMN schedule_id;

DROP INDEX schedules_job_id_idx;

ALTER TABLE schedules DROP COLUMN updated_at;

ALTER TABLE schedules DROP CONSTRAINT schedules_overlap_policy_check;
ALTER TABLE schedules DROP COLUMN overlap_policy;

ALTER TABLE schedules DROP CONSTRAINT schedules_catch_up_policy_check;
ALTER TABLE schedules DROP COLUMN catch_up_policy;

ALTER TABLE schedules ADD COLUMN next_due_at timestamptz;
