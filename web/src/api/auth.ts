import { request } from './client'
import type { components } from './schema'

export type Principal = components['schemas']['Principal']

export function login(username: string, password: string): Promise<Principal> {
  return request<Principal>('/api/v1/auth/login', {
    method: 'POST',
    body: { username, password },
  })
}

export function logout(): Promise<void> {
  return request<void>('/api/v1/auth/logout', { method: 'POST' })
}

// Doubles as the SPA's session-check call on load - a 401 here means "not
// logged in", nothing more.
export function whoami(): Promise<Principal> {
  return request<Principal>('/api/v1/whoami')
}
