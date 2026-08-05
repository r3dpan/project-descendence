-- name: CreateSchedule :one
-- Task 5.7. cron_expr and timezone are validated by the caller
-- (scheduling.CronToOnCalendar, time.LoadLocation) before this ever runs -
-- this query trusts its inputs, the same posture CreateJobRun already takes
-- toward its caller's manifest resolution.
INSERT INTO schedules (job_id, cron_expr, timezone, catch_up_policy, overlap_policy, enabled)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, job_id, cron_expr, timezone, enabled, created_at, catch_up_policy,
          overlap_policy, updated_at;

-- name: GetSchedule :one
SELECT id, job_id, cron_expr, timezone, enabled, created_at, catch_up_policy,
       overlap_policy, updated_at
FROM schedules
WHERE id = $1;

-- name: ListSchedulesByJob :many
-- No pagination (task 5.7) - a job's schedule count is small at this scale;
-- see ListSchedules for the supervisor's across-all-jobs sweep.
SELECT id, job_id, cron_expr, timezone, enabled, created_at, catch_up_policy,
       overlap_policy, updated_at
FROM schedules
WHERE job_id = $1
ORDER BY id ASC;

-- name: ListSchedules :many
-- The supervisor's schedule-sync loop (task 5.3): every schedule, so the
-- loop can render the expected unit pair for each and remove units for any
-- id no longer present. No pagination for the same reason ListSchedulesByJob
-- has none - this is a full sweep by design, not a paged listing.
SELECT id, job_id, cron_expr, timezone, enabled, created_at, catch_up_policy,
       overlap_policy, updated_at
FROM schedules
ORDER BY id ASC;

-- name: UpdateSchedule :one
-- Task 5.7's PATCH. All five mutable fields are always supplied by the
-- handler (the API layer already fills in "unchanged" values from the
-- current row for a partial PATCH, matching PatchJobHandler's pattern) -
-- there is no COALESCE here, unlike UpsertJob, because this is a direct
-- operator edit rather than a re-sync that must leave one column alone.
UPDATE schedules
SET cron_expr      = $2,
    timezone       = $3,
    catch_up_policy = $4,
    overlap_policy = $5,
    enabled        = $6,
    updated_at     = now()
WHERE id = $1
RETURNING id, job_id, cron_expr, timezone, enabled, created_at, catch_up_policy,
          overlap_policy, updated_at;

-- name: DeleteSchedule :execrows
-- Hard delete - a schedule is operator-owned data, not a git projection
-- (unlike jobs), so there is no soft-delete/explainability concern the way
-- SoftDeleteJobsNotIn has one. runs.schedule_id is ON DELETE SET NULL, so a
-- past run this schedule created stays explainable regardless.
DELETE FROM schedules
WHERE id = $1;
