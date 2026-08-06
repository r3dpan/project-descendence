import { request } from './client'
import type { components } from './schema'

export type Principal = components['schemas']['Principal']

// There is no login() fetch helper (Phase 9, task 9.11): logging in means
// navigating the browser to GET /api/v1/auth/login so it can follow the
// redirect to the IdP - an XHR/fetch can't do that. See Login.tsx.

export function logout(): Promise<void> {
  return request<void>('/api/v1/auth/logout', { method: 'POST' })
}

// Doubles as the SPA's session-check call on load - a 401 here means "not
// logged in", nothing more.
export function whoami(): Promise<Principal> {
  return request<Principal>('/api/v1/whoami')
}
