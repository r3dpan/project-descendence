import { request } from './client'
import type { Run } from './runs'
import type { components } from './schema'

export type Job = components['schemas']['Job']
export type JobList = components['schemas']['JobList']
export type JobParam = components['schemas']['JobParam']

export interface ListJobsParams {
  cursor?: string
  limit?: number
  [key: string]: string | number | undefined
}

export function listJobs(params: ListJobsParams = {}): Promise<JobList> {
  return request<JobList>('/api/v1/jobs', { query: params })
}

export function getJob(id: number | string): Promise<Job> {
  return request<Job>(`/api/v1/jobs/${id}`)
}

// Params are raw strings, matching --param name=value on the CLI - the
// server coerces them against the job's contract (task 6.2), so this client
// never needs to duplicate that typing.
export function createJobRun(id: number | string, params?: Record<string, string>): Promise<Run> {
  return request<Run>(`/api/v1/jobs/${id}/runs`, {
    method: 'POST',
    body: params && Object.keys(params).length > 0 ? { params } : undefined,
  })
}
