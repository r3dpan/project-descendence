import { request } from './client'
import type { components } from './schema'

export type Config = components['schemas']['Config']

export function getConfig(): Promise<Config> {
  return request<Config>('/api/v1/config')
}

export function putConfig(cfg: Config): Promise<Config> {
  return request<Config>('/api/v1/config', { method: 'PUT', body: cfg })
}
