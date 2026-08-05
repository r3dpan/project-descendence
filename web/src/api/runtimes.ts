import { request } from './client'
import type { components } from './schema'

export type Runtime = components['schemas']['Runtime']
export type RuntimeList = components['schemas']['RuntimeList']
export type RuntimeCreate = components['schemas']['RuntimeCreate']

export const TERMINAL_BUILD_STATUSES = new Set(['ready', 'failed'])

export interface ListRuntimesParams {
  cursor?: string
  limit?: number
  [key: string]: string | number | undefined
}

export function listRuntimes(params: ListRuntimesParams = {}): Promise<RuntimeList> {
  return request<RuntimeList>('/api/v1/runtimes', { query: params })
}

export function getRuntime(id: number | string): Promise<Runtime> {
  return request<Runtime>(`/api/v1/runtimes/${id}`)
}

// Creating a runtime queues its first build immediately - the returned
// Runtime's buildStatus is already "pending" (task 4.8).
export function createRuntime(params: RuntimeCreate): Promise<Runtime> {
  return request<Runtime>('/api/v1/runtimes', { method: 'POST', body: params })
}

// Rebuilding never changes what an already-created run executes (task 4.6):
// a run pins the image digest at creation and is never re-resolved. The
// 202 response body is empty (Location only) - re-fetch getRuntime to see
// the new buildStatus rather than trusting a body that isn't there.
export function buildRuntime(id: number | string): Promise<void> {
  return request<void>(`/api/v1/runtimes/${id}/build`, { method: 'POST' })
}
