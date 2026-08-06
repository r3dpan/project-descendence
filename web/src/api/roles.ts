import { request } from './client'
import type { components } from './schema'

export type Role = components['schemas']['Role']
export type RoleList = components['schemas']['RoleList']

// List-only - roles are fixed built-ins (ARCHITECTURE.md §6 decision #30),
// not admin-editable, so there is no create/update/delete here.
export function listRoles(): Promise<RoleList> {
  return request<RoleList>('/api/v1/roles')
}
