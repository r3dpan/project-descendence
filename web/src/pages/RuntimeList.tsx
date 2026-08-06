import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import {
  Alert,
  Button,
  Code,
  LoadingOverlay,
  Paper,
  SegmentedControl,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core'
import { createRuntime, listRuntimes, type Runtime } from '../api/runtimes'
import { APIError } from '../api/client'
import PageHeader from '../components/PageHeader'
import StatusTag from '../components/StatusTag'

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
    <PageHeader title="Runtimes" subtitle="Execution environments">
      <Stack gap="xl" maw={900}>
        <div>
          {error && (
            <Alert color="red" role="alert" mb="md">
              {error}
            </Alert>
          )}
          <Table.ScrollContainer minWidth={600} pos="relative">
            <LoadingOverlay visible={loading && runtimes.length === 0} />
            <Table verticalSpacing="sm">
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
                  <Table.Tr key={runtime.id} style={{ cursor: 'pointer' }}>
                    <Table.Td>
                      <Text component={Link} to={`/runtimes/${runtime.id}`} c="accent.4" fw={500} style={{ textDecoration: 'none' }}>
                        {runtime.name}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Text c="dimmed">{runtime.lang}</Text>
                    </Table.Td>
                    <Table.Td>
                      <StatusTag status={runtime.buildStatus} />
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
            <Button variant="default" mt="md" onClick={() => setCursor(nextCursor)}>
              Load more
            </Button>
          )}
        </div>

        <div>
          <Text fw={500} size="17px" mb="sm">
            New runtime
          </Text>
          <Paper p="md" radius="md" bg="dark.6" maw={480} component="form" onSubmit={handleCreate}>
            <Stack gap="sm">
              <TextInput label="Name" id="rt-name" value={name} onChange={(e) => setName(e.target.value)} required />
              <div>
                <Text size="12px" mb={5} c="dimmed">
                  Language
                </Text>
                <SegmentedControl
                  value={lang}
                  onChange={(v) => setLang(v as typeof lang)}
                  data={['python', 'powershell', 'node']}
                />
              </div>
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
              <Button type="submit" w="fit-content" loading={creating}>
                Create runtime
              </Button>
            </Stack>
          </Paper>
        </div>
      </Stack>
    </PageHeader>
  )
}
