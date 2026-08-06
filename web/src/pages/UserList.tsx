import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useAuth } from '../auth'
import { createUser, listUsers, type User } from '../api/users'
import { listRoles, type Role } from '../api/roles'
import { APIError } from '../api/client'
import {
  Alert,
  Badge,
  Button,
  Code,
  CopyButton,
  Group,
  LoadingOverlay,
  Modal,
  Paper,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from '@mantine/core'

export default function UserList() {
  const { principal } = useAuth()
  const canManage = principal?.permissions.includes('users:write') ?? false

  const [users, setUsers] = useState<User[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const [name, setName] = useState('')
  const [role, setRole] = useState('')
  const [createError, setCreateError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  // Shown exactly once after a create - never re-fetchable, same contract
  // as cmd/seed's and the CLI's "shown once" password display.
  const [generatedPassword, setGeneratedPassword] = useState<string | null>(null)

  useEffect(() => {
    setLoading(true)
    Promise.all([listUsers(), canManage ? listRoles() : Promise.resolve({ items: [] })])
      .then(([userPage, rolePage]) => {
        setUsers(userPage.items)
        setRoles(rolePage.items)
        if (rolePage.items.length > 0) setRole((r) => r || rolePage.items[0].name)
      })
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading users'))
      .finally(() => setLoading(false))
  }, [canManage])

  async function handleCreate(e: FormEvent) {
    e.preventDefault()
    setCreateError(null)
    setCreating(true)
    try {
      const created = await createUser({ name, role })
      setUsers((prev) => [...prev, created].sort((a, b) => a.name.localeCompare(b.name)))
      setGeneratedPassword(created.password)
      setName('')
    } catch (err) {
      setCreateError(err instanceof APIError ? err.message : 'Failed creating user')
    } finally {
      setCreating(false)
    }
  }

  return (
    <>
      <Title order={2} mb="md">
        Users
      </Title>
      {error && (
        <Alert color="red" role="alert" mb="md">
          {error}
        </Alert>
      )}
      <Table.ScrollContainer minWidth={500} pos="relative">
        <LoadingOverlay visible={loading && users.length === 0} />
        <Table highlightOnHover>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Name</Table.Th>
              <Table.Th>Role</Table.Th>
              <Table.Th>Created</Table.Th>
              <Table.Th>Status</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {users.map((u) => (
              <Table.Tr key={u.id}>
                <Table.Td>
                  <Link to={`/users/${u.id}`}>{u.name}</Link>
                </Table.Td>
                <Table.Td>{u.role}</Table.Td>
                <Table.Td>{u.createdAt}</Table.Td>
                <Table.Td>
                  <Badge color={u.revokedAt ? 'red' : 'green'}>{u.revokedAt ? `revoked ${u.revokedAt}` : 'active'}</Badge>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {!loading && users.length === 0 && <Text c="dimmed">No users yet.</Text>}

      {canManage && (
        <>
          <Title order={3} mt="xl" mb="md">
            New user
          </Title>
          <Paper withBorder p="md" maw={400}>
            <form onSubmit={handleCreate}>
              <Stack>
                <TextInput label="Name" id="user-name" value={name} onChange={(e) => setName(e.target.value)} required />
                <Select
                  label="Role"
                  id="user-role"
                  value={role}
                  onChange={(v) => setRole(v ?? '')}
                  data={roles.map((r) => r.name)}
                  allowDeselect={false}
                />
                {createError && (
                  <Alert color="red" role="alert">
                    {createError}
                  </Alert>
                )}
                <Button type="submit" loading={creating}>
                  {creating ? 'Creating…' : 'Create user'}
                </Button>
              </Stack>
            </form>
          </Paper>

          <Modal opened={generatedPassword !== null} onClose={() => setGeneratedPassword(null)} title="User created" centered>
            <Text size="sm" c="dimmed" mb="sm">
              This is shown once and cannot be retrieved again - copy it now.
            </Text>
            <Group>
              <Code style={{ flex: 1, wordBreak: 'break-all' }}>{generatedPassword}</Code>
              <CopyButton value={generatedPassword ?? ''} timeout={2000}>
                {({ copied, copy }) => (
                  <Button size="xs" color={copied ? 'teal' : 'blue'} onClick={copy}>
                    {copied ? 'Copied' : 'Copy'}
                  </Button>
                )}
              </CopyButton>
            </Group>
          </Modal>
        </>
      )}
    </>
  )
}
