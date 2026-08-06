import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import {
  Alert,
  Badge,
  Button,
  Code,
  LoadingOverlay,
  Select,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from '@mantine/core'
import { createRuntime, listRuntimes, type Runtime } from '../api/runtimes'
import { APIError } from '../api/client'
import { statusColor } from '../statusColor'

export default function RuntimeList() {
  const [runtimes, setRuntimes] = useState<Runtime[]>([])
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [nextCursor, setNextCursor] = useState<string | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const [name, setName] = useState('')
  const [lang, setLang] = useState<'python' | 'powershell' | 'node'>('python')
  const [baseImage, setBaseImage] = useState('')
  const [sysPackages, setSysPackages] = useState('')
  const [langManifest, setLangManifest] = useState('')
  const [createError, setCreateError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    setLoading(true)
    listRuntimes({ cursor })
      .then((page) => {
        setRuntimes((prev) => (cursor ? [...prev, ...page.items] : page.items))
        setNextCursor(page.nextCursor)
      })
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading runtimes'))
      .finally(() => setLoading(false))
  }, [cursor])

  async function handleCreate(e: FormEvent) {
    e.preventDefault()
    setCreateError(null)
    setCreating(true)
    try {
      const runtime = await createRuntime({
        name,
        lang,
        baseImage: baseImage || undefined,
        sysPackages: sysPackages
          ? sysPackages
              .split(',')
              .map((s) => s.trim())
              .filter(Boolean)
          : undefined,
        langManifest: langManifest || undefined,
      })
      setRuntimes((prev) => [runtime, ...prev])
      setName('')
      setBaseImage('')
      setSysPackages('')
      setLangManifest('')
    } catch (err) {
      setCreateError(err instanceof APIError ? err.message : 'Failed creating runtime')
    } finally {
      setCreating(false)
    }
  }

  return (
    <>
      <Title order={2} mb="md">
        Runtimes
      </Title>
      {error && (
        <Alert color="red" role="alert" mb="md">
          {error}
        </Alert>
      )}
      <Table.ScrollContainer minWidth={600} pos="relative">
        <LoadingOverlay visible={loading && runtimes.length === 0} />
        <Table highlightOnHover>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Name</Table.Th>
              <Table.Th>Lang</Table.Th>
              <Table.Th>Build status</Table.Th>
              <Table.Th>Image digest</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {runtimes.map((runtime) => (
              <Table.Tr key={runtime.id}>
                <Table.Td>
                  <Link to={`/runtimes/${runtime.id}`}>{runtime.name}</Link>
                </Table.Td>
                <Table.Td>{runtime.lang}</Table.Td>
                <Table.Td>
                  <Badge color={statusColor(runtime.buildStatus)}>{runtime.buildStatus}</Badge>
                </Table.Td>
                <Table.Td>
                  <Code>{runtime.imageDigest ? runtime.imageDigest.slice(0, 24) + '…' : ''}</Code>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {!loading && runtimes.length === 0 && <Text c="dimmed">No runtimes yet.</Text>}
      {!loading && nextCursor && (
        <Button variant="light" mt="md" onClick={() => setCursor(nextCursor)}>
          Load more
        </Button>
      )}

      <Title order={3} mt="xl" mb="md">
        New runtime
      </Title>
      <form onSubmit={handleCreate}>
        <Stack maw={480}>
          <TextInput label="Name" id="rt-name" value={name} onChange={(e) => setName(e.target.value)} required />
          <Select
            label="Language"
            id="rt-lang"
            value={lang}
            onChange={(v) => setLang((v as typeof lang) ?? 'python')}
            data={['python', 'powershell', 'node']}
            allowDeselect={false}
          />
          <TextInput
            label="Base image"
            description="Optional - defaults to a curated image"
            id="rt-base"
            value={baseImage}
            onChange={(e) => setBaseImage(e.target.value)}
          />
          <TextInput
            label="System packages"
            description="Comma-separated, optional"
            id="rt-sys"
            value={sysPackages}
            onChange={(e) => setSysPackages(e.target.value)}
          />
          <Textarea
            label="Language manifest"
            description="Optional - requirements.txt / PSResourceGet file / package.json, verbatim"
            id="rt-manifest"
            value={langManifest}
            onChange={(e) => setLangManifest(e.target.value)}
            rows={4}
            styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
          />
          {createError && (
            <Alert color="red" role="alert">
              {createError}
            </Alert>
          )}
          <Button type="submit" loading={creating}>
            {creating ? 'Creating…' : 'Create runtime'}
          </Button>
        </Stack>
      </form>
    </>
  )
}
