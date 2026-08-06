import { request } from './client'
import type { components } from './schema'

export type Principal = components['schemas']['Principal']

// There is no login() or logout() fetch helper (Phase 9, task 9.11 plus a
// live-testing follow-up): both are browser navigations, not JSON calls.
// Login needs to follow a redirect to the IdP; logout needs to follow one
// too, through the IdP's end_session_endpoint, so the IdP's own SSO session
// ends instead of silently re-authenticating the next login click. An
// XHR/fetch can't follow either. See Login.tsx / Layout.tsx.

// Doubles as the SPA's session-check call on load - a 401 here means "not
// logged in", nothing more.
export function whoami(): Promise<Principal> {
  return request<Principal>('/api/v1/whoami')
}
