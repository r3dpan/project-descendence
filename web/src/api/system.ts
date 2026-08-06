import { request } from './client'
import type { components } from './schema'

export type SystemStatus = components['schemas']['SystemStatus']

export function getSystemStatus(): Promise<SystemStatus> {
  return request<SystemStatus>('/api/v1/system/status')
}
