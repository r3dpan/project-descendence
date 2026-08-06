import { request } from './client'
import type { components } from './schema'

export type Schedule = components['schemas']['Schedule']
export type ScheduleList = components['schemas']['ScheduleList']
export type ScheduleCreate = components['schemas']['ScheduleCreate']
export type SchedulePatch = components['schemas']['SchedulePatch']
export type ScheduleTriggerResult = components['schemas']['ScheduleTriggerResult']

// No pagination - a job's schedule count is small at homelab scale
// (matches the server's own listSchedulesByJob doc comment).
export function listSchedulesByJob(jobId: number | string): Promise<ScheduleList> {
  return request<ScheduleList>(`/api/v1/jobs/${jobId}/schedules`)
}

export function createSchedule(jobId: number | string, body: ScheduleCreate): Promise<Schedule> {
  return request<Schedule>(`/api/v1/jobs/${jobId}/schedules`, { method: 'POST', body })
}

export function patchSchedule(id: number | string, body: SchedulePatch): Promise<Schedule> {
  return request<Schedule>(`/api/v1/schedules/${id}`, { method: 'PATCH', body })
}

export function deleteSchedule(id: number | string): Promise<void> {
  return request<void>(`/api/v1/schedules/${id}`, { method: 'DELETE' })
}

// 200 with skipped:true when the overlap policy declined to create a run -
// not an error, just nothing to do (the schedule fired exactly as designed).
export function triggerSchedule(id: number | string): Promise<ScheduleTriggerResult> {
  return request<ScheduleTriggerResult>(`/api/v1/schedules/${id}/trigger`, { method: 'POST' })
}
