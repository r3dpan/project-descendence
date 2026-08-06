import { request } from './client'
import type { components } from './schema'

export type User = components['schemas']['User']
export type UserList = components['schemas']['UserList']
export type UserCreateResponse = components['schemas']['UserCreateResponse']

export function listUsers(): Promise<UserList> {
  return request<UserList>('/api/v1/users')
}

export function getUser(id: number | string): Promise<User> {
  return request<User>(`/api/v1/users/${id}`)
}

export interface CreateUserParams {
  name: string
  role: string
  password?: string
}

// Returns the generated plaintext password (if one wasn't supplied) - shown
// exactly once, the same "shown once" contract the CLI and cmd/seed use.
export function createUser(params: CreateUserParams): Promise<UserCreateResponse> {
  return request<UserCreateResponse>('/api/v1/users', { method: 'POST', body: params })
}

// Role reassignment only - name/password change elsewhere, matching the
// server's narrow UserPatch shape.
export function updateUserRole(id: number | string, role: string): Promise<User> {
  return request<User>(`/api/v1/users/${id}`, { method: 'PATCH', body: { role } })
}

// Soft-revoke, never a hard delete - runs.principal_id is ON DELETE
// RESTRICT, so this is the only "remove a user" operation the API exposes.
export function revokeUser(id: number | string): Promise<void> {
  return request<void>(`/api/v1/users/${id}`, { method: 'DELETE' })
}

export function changeOwnPassword(currentPassword: string, newPassword: string): Promise<void> {
  return request<void>('/api/v1/users/me/password', {
    method: 'PATCH',
    body: { currentPassword, newPassword },
  })
}
