package client

import (
	"context"
	"fmt"
	"net/http"
)

// Schedule mirrors the Schedule schema (task 5.7). Like Runtime, and unlike
// Job, every field here is owned by Postgres directly rather than by a
// manifest in git - operator-owned data, not a projection.
type Schedule struct {
	ID            int64   `json:"id"`
	JobID         int64   `json:"jobId"`
	CronExpr      string  `json:"cronExpr"`
	Timezone      string  `json:"timezone"`
	CatchUpPolicy string  `json:"catchUpPolicy"`
	OverlapPolicy string  `json:"overlapPolicy"`
	Enabled       bool    `json:"enabled"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
	NextDueAt     *string `json:"nextDueAt"`
}

type ScheduleList struct {
	Items []Schedule `json:"items"`
}

type CreateScheduleParams struct {
	CronExpr      string `json:"cronExpr"`
	Timezone      string `json:"timezone,omitempty"`
	CatchUpPolicy string `json:"catchUpPolicy,omitempty"`
	OverlapPolicy string `json:"overlapPolicy,omitempty"`
	Enabled       *bool  `json:"enabled,omitempty"`
}

// UpdateScheduleParams is a PATCH body - a nil field leaves that value
// unchanged, matching the server's SchedulePatch semantics.
type UpdateScheduleParams struct {
	CronExpr      *string `json:"cronExpr,omitempty"`
	Timezone      *string `json:"timezone,omitempty"`
	CatchUpPolicy *string `json:"catchUpPolicy,omitempty"`
	OverlapPolicy *string `json:"overlapPolicy,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

// ScheduleTriggerResult is what POST .../trigger returns - either a
// skipped fire (task 5.6's overlap policy) or the run it created.
type ScheduleTriggerResult struct {
	Skipped bool   `json:"skipped"`
	Reason  string `json:"reason,omitempty"`
	Run     *Run   `json:"run,omitempty"`
}

func (c *Client) ListSchedulesByJob(ctx context.Context, jobID int64) (ScheduleList, error) {
	var list ScheduleList
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/jobs/%d/schedules", jobID), requestOptions{}, &list)
	return list, err
}

func (c *Client) CreateSchedule(ctx context.Context, jobID int64, params CreateScheduleParams) (Schedule, error) {
	var sched Schedule
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/schedules", jobID), requestOptions{body: params}, &sched)
	return sched, err
}

func (c *Client) GetSchedule(ctx context.Context, id int64) (Schedule, error) {
	var sched Schedule
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/schedules/%d", id), requestOptions{}, &sched)
	return sched, err
}

func (c *Client) UpdateSchedule(ctx context.Context, id int64, params UpdateScheduleParams) (Schedule, error) {
	var sched Schedule
	err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/api/v1/schedules/%d", id), requestOptions{body: params}, &sched)
	return sched, err
}

func (c *Client) DeleteSchedule(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/schedules/%d", id), requestOptions{}, nil)
}

// TriggerSchedule fires a schedule the way a generated systemd unit does -
// mainly useful for testing a schedule without waiting for its timer.
func (c *Client) TriggerSchedule(ctx context.Context, id int64) (ScheduleTriggerResult, error) {
	var result ScheduleTriggerResult
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/schedules/%d/trigger", id), requestOptions{}, &result)
	return result, err
}
