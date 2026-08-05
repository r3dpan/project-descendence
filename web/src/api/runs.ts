import { request } from './client'
import type { components } from './schema'

export type Run = components['schemas']['Run']
export type RunList = components['schemas']['RunList']
export type RunLogLine = components['schemas']['RunLogLine']
export type RunLogList = components['schemas']['RunLogList']

// Run states, as constrained by the Run schema's enum in api/openapi.yaml -
// same list internal/client's runs.go keeps for the CLI/TUI.
export const TERMINAL_STATES = new Set(['succeeded', 'failed', 'cancelled', 'lost'])

export function isTerminal(state: string): boolean {
  return TERMINAL_STATES.has(state)
}

export interface ListRunsParams {
  cursor?: string
  limit?: number
  [key: string]: string | number | undefined
}

// Keyset (cursor) pagination, never offset - ARCHITECTURE.md §4.9.
export function listRuns(params: ListRunsParams = {}): Promise<RunList> {
  return request<RunList>('/api/v1/runs', { query: params })
}

export function getRun(id: number | string): Promise<Run> {
  return request<Run>(`/api/v1/runs/${id}`)
}

export function cancelRun(id: number | string): Promise<Run> {
  return request<Run>(`/api/v1/runs/${id}/cancel`, { method: 'POST' })
}

// The JSON page of a run's history (default Accept). Live tailing uses
// runLogsStreamURL below instead, via the browser's native EventSource.
export function getRunLogs(id: number | string, after = 0): Promise<RunLogList> {
  return request<RunLogList>(`/api/v1/runs/${id}/logs`, { query: { after } })
}

// Same URL as getRunLogs - content negotiation on Accept picks the
// text/event-stream representation. Same-origin + cookie session is what
// makes native EventSource usable at all here (ARCHITECTURE.md §4.11):
// EventSource cannot set an Authorization header, so this only works because
// the session cookie rides along automatically.
export function runLogsStreamURL(id: number | string): string {
  return `/api/v1/runs/${id}/logs`
}
