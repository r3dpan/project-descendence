import { useEffect, useState, type FormEvent } from 'react'
import { useAuth } from '../auth'
import { createToken, listTokens, revokeToken, type Token } from '../api/tokens'
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

export default function TokenList() {
  const { principal } = useAuth()
  const canManage = principal?.permissions.includes('users:write') ?? false

  const [tokens, setTokens] = useState<Token[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [revokingId, setRevokingId] = useState<number | null>(null)

  const [name, setName] = useState('')
  const [role, setRole] = useState('')
  const [createError, setCreateError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  // Shown exactly once - never re-fetchable, same "shown once" contract as
  // cmd/seed and the CLI's plaintext token output.
  const [generatedToken, setGeneratedToken] = useState<string | null>(null)

  useEffect(() => {
    setLoading(true)
    Promise.all([listTokens(), canManage ? listRoles() : Promise.resolve({ items: [] })])
      .then(([tokenPage, rolePage]) => {
        setTokens(tokenPage.items)
        setRoles(rolePage.items)
        if (rolePage.items.length > 0) setRole((r) => r || rolePage.items[0].name)
      })
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading tokens'))
      .finally(() => setLoading(false))
  }, [canManage])

  async function handleCreate(e: FormEvent) {
    e.preventDefault()
    setCreateError(null)
    setCreating(true)
    try {
      const created = await createToken({ name, role })
      setTokens((prev) => [...prev, created])
      setGeneratedToken(created.token)
      setName('')
    } catch (err) {
      setCreateError(err instanceof APIError ? err.message : 'Failed creating token')
    } finally {
      setCreating(false)
    }
  }

  async function handleRevoke(token: Token) {
    if (!window.confirm(`Revoke token "${token.name}"? This cannot be undone.`)) return
    setError(null)
    setRevokingId(token.id)
    try {
      await revokeToken(token.id)
      setTokens((prev) => prev.map((t) => (t.id === token.id ? { ...t, revokedAt: new Date().toISOString() } : t)))
    } catch (err) {
      setError(err instanceof APIError ? err.message : 'Failed revoking token')
    } finally {
      setRevokingId(null)
    }
  }

  return (
    <>
      <Title order={2} mb="md">
        Tokens
      </Title>
      {error && (
        <Alert color="red" role="alert" mb="md">
          {error}
        </Alert>
      )}
      <Table.ScrollContainer minWidth={700} pos="relative">
        <LoadingOverlay visible={loading && tokens.length === 0} />
        <Table highlightOnHover>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Name</Table.Th>
              <Table.Th>Role</Table.Th>
              <Table.Th>Hint</Table.Th>
              <Table.Th>Created</Table.Th>
              <Table.Th>Expires</Table.Th>
              <Table.Th>Status</Table.Th>
              {canManage && <Table.Th></Table.Th>}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {tokens.map((t) => (
              <Table.Tr key={t.id}>
                <Table.Td>{t.name}</Table.Td>
                <Table.Td>{t.role}</Table.Td>
                <Table.Td>
                  <Code>{t.tokenHint}</Code>
                </Table.Td>
                <Table.Td>{t.createdAt}</Table.Td>
                <Table.Td>{t.expiresAt ?? '-'}</Table.Td>
                <Table.Td>
                  <Badge color={t.revokedAt ? 'red' : 'green'}>{t.revokedAt ? `revoked ${t.revokedAt}` : 'active'}</Badge>
                </Table.Td>
                {canManage && (
                  <Table.Td>
                    {!t.revokedAt && (
                      <Button size="xs" color="red" variant="light" onClick={() => handleRevoke(t)} disabled={revokingId === t.id}>
                        {revokingId === t.id ? 'Revoking…' : 'Revoke'}
                      </Button>
                    )}
                  </Table.Td>
                )}
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {!loading && tokens.length === 0 && <Text c="dimmed">No tokens yet.</Text>}

      {canManage && (
        <>
          <Title order={3} mt="xl" mb="md">
            New token
          </Title>
          <Paper withBorder p="md" maw={400}>
            <form onSubmit={handleCreate}>
              <Stack>
                <TextInput label="Name" id="token-name" value={name} onChange={(e) => setName(e.target.value)} required />
                <Select
                  label="Role"
                  id="token-role"
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
                  {creating ? 'Creating…' : 'Create token'}
                </Button>
              </Stack>
            </form>
          </Paper>

          <Modal opened={generatedToken !== null} onClose={() => setGeneratedToken(null)} title="Token created" centered>
            <Text size="sm" c="dimmed" mb="sm">
              This is shown once and cannot be retrieved again - copy it now.
            </Text>
            <Group>
              <Code style={{ flex: 1, wordBreak: 'break-all' }}>{generatedToken}</Code>
              <CopyButton value={generatedToken ?? ''} timeout={2000}>
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
