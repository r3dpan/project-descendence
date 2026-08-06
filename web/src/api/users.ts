import { request } from './client'
import type { components } from './schema'

export type User = components['schemas']['User']
export type UserList = components['schemas']['UserList']

export function listUsers(): Promise<UserList> {
  return request<UserList>('/api/v1/users')
}

export function getUser(id: number | string): Promise<User> {
  return request<User>(`/api/v1/users/${id}`)
}

// Role reassignment only, matching the server's narrow UserPatch shape.
// There is no createUser (Phase 9, task 9.8): a user principal is created
// by its first OIDC login (JIT-provisioned with no role), and this is how
// an admin assigns its first role.
export function updateUserRole(id: number | string, role: string): Promise<User> {
  return request<User>(`/api/v1/users/${id}`, { method: 'PATCH', body: { role } })
}

// Soft-revoke, never a hard delete - runs.principal_id is ON DELETE
// RESTRICT, so this is the only "remove a user" operation the API exposes.
export function revokeUser(id: number | string): Promise<void> {
  return request<void>(`/api/v1/users/${id}`, { method: 'DELETE' })
}
