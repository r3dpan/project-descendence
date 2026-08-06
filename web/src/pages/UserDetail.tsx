import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useAuth } from '../auth'
import { getUser, revokeUser, updateUserRole, type User } from '../api/users'
import { listRoles, type Role } from '../api/roles'
import { APIError } from '../api/client'
import { Alert, Badge, Button, Group, Loader, Paper, Select, Stack, Text } from '@mantine/core'
import PageHeader from '../components/PageHeader'

export default function UserDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { principal } = useAuth()
  const canManage = principal?.permissions.includes('users:write') ?? false

  const [user, setUser] = useState<User | null>(null)
  const [roles, setRoles] = useState<Role[]>([])
  const [loadError, setLoadError] = useState<string | null>(null)

  const [selectedRole, setSelectedRole] = useState('')
  const [roleUpdating, setRoleUpdating] = useState(false)
  const [roleError, setRoleError] = useState<string | null>(null)

  const [revoking, setRevoking] = useState(false)
  const [revokeError, setRevokeError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    Promise.all([getUser(id), canManage ? listRoles() : Promise.resolve({ items: [] })])
      .then(([u, rolePage]) => {
        setUser(u)
        setSelectedRole(u.role)
        setRoles(rolePage.items)
      })
      .catch((err) => setLoadError(err instanceof APIError ? err.message : 'Failed loading user'))
  }, [id, canManage])

  async function handleRoleChange() {
    if (!user) return
    setRoleError(null)
    setRoleUpdating(true)
    try {
      const updated = await updateUserRole(user.id, selectedRole)
      setUser(updated)
    } catch (err) {
      setRoleError(err instanceof APIError ? err.message : 'Failed updating role')
    } finally {
      setRoleUpdating(false)
    }
  }

  // window.confirm rather than a modal library, matching this codebase's
  // minimal-dependency posture elsewhere in the SPA.
  async function handleRevoke() {
    if (!user) return
    if (!window.confirm(`Revoke ${user.name}'s access? This cannot be undone.`)) return
    setRevokeError(null)
    setRevoking(true)
    try {
      await revokeUser(user.id)
      navigate('/settings/users')
    } catch (err) {
      setRevokeError(err instanceof APIError ? err.message : 'Failed revoking user')
      setRevoking(false)
    }
  }

  if (loadError)
    return (
      <PageHeader title="User" backTo="/settings/users" backLabel="Users">
        <Alert color="red" role="alert">
          {loadError}
        </Alert>
      </PageHeader>
    )
  if (!user)
    return (
      <PageHeader title="User" backTo="/settings/users" backLabel="Users">
        <Loader />
      </PageHeader>
    )

  return (
    <PageHeader title={user.name} subtitle={`Created ${user.createdAt}`} backTo="/settings/users" backLabel="Users">
      <Stack gap="md" maw={480}>
        <Group gap="sm">
          <Badge variant={user.revokedAt ? 'filled' : 'light'} color={user.revokedAt ? 'dark' : 'accent'} tt="none">
            {user.revokedAt ? `Revoked ${user.revokedAt}` : 'Active'}
          </Badge>
        </Group>

        <Paper p="md" radius="md" bg="dark.6">
          {canManage && !user.revokedAt ? (
            <Stack gap="sm">
              <Group align="flex-end" gap="sm">
                <Select
                  label="Role"
                  id="role-select"
                  value={selectedRole}
                  onChange={(v) => setSelectedRole(v ?? '')}
                  data={roles.map((r) => r.name)}
                  allowDeselect={false}
                />
                <Button onClick={handleRoleChange} loading={roleUpdating} disabled={selectedRole === user.role}>
                  Save role
                </Button>
              </Group>
              {roleError && (
                <Alert color="red" role="alert">
                  {roleError}
                </Alert>
              )}
            </Stack>
          ) : (
            <Text size="sm">
              <Text component="span" c="dimmed">
                Role{' '}
              </Text>
              {user.role}
            </Text>
          )}
        </Paper>

        {canManage && !user.revokedAt && (
          <div>
            <Button color="red" variant="subtle" onClick={handleRevoke} loading={revoking}>
              Revoke access
            </Button>
            {revokeError && (
              <Alert color="red" role="alert" mt="sm">
                {revokeError}
              </Alert>
            )}
          </div>
        )}
      </Stack>
    </PageHeader>
  )
}
