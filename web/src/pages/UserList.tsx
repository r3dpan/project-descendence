import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listUsers, type User } from '../api/users'
import { APIError } from '../api/client'
import { Alert, Badge, LoadingOverlay, Table, Text } from '@mantine/core'

// There is no "New user" form (Phase 9, task 9.11): a user principal is
// created by its first OIDC login (JIT-provisioned with no role, task 9.6),
// not by an admin here - UserDetail's role reassignment is how an admin
// turns that roleless principal into one that can actually do anything.
export default function UserList() {
  const [users, setUsers] = useState<User[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    listUsers()
      .then((page) => setUsers(page.items))
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading users'))
      .finally(() => setLoading(false))
  }, [])

  return (
    <div>
      {error && (
        <Alert color="red" role="alert" mb="md">
          {error}
        </Alert>
      )}
      <Table.ScrollContainer minWidth={500} pos="relative">
        <LoadingOverlay visible={loading && users.length === 0} />
        <Table verticalSpacing="sm">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Username</Table.Th>
              <Table.Th>Role</Table.Th>
              <Table.Th>Created</Table.Th>
              <Table.Th></Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {users.map((u) => (
              <Table.Tr key={u.id}>
                <Table.Td>
                  <Text component={Link} to={`/settings/users/${u.id}`} fw={500} c="var(--mantine-color-text)" style={{ textDecoration: 'none' }}>
                    {u.name}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <Badge variant="light" color="gray" tt="none">
                    {u.role || '(none)'}
                  </Badge>
                </Table.Td>
                <Table.Td>
                  <Text c="dimmed">{u.createdAt}</Text>
                </Table.Td>
                <Table.Td style={{ textAlign: 'right' }}>
                  <Badge variant={u.revokedAt ? 'filled' : 'light'} color={u.revokedAt ? 'dark' : 'accent'} tt="none">
                    {u.revokedAt ? `revoked ${u.revokedAt}` : 'active'}
                  </Badge>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {!loading && users.length === 0 && <Text c="dimmed">No users yet.</Text>}
    </div>
  )
}
