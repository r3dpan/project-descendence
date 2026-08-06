import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import {
  Alert,
  Button,
  Checkbox,
  Code,
  Group,
  LoadingOverlay,
  NumberInput,
  Paper,
  SegmentedControl,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { createRuntime, listRuntimes, pruneRuntimes, type Runtime, type RuntimePruneResult } from '../api/runtimes'
import { APIError } from '../api/client'
import { useAuth } from '../auth'
import PageHeader from '../components/PageHeader'
import StatusTag from '../components/StatusTag'

function showPruneResult(result: RuntimePruneResult) {
  const parts: string[] = []
  if (result.pruned.length > 0) parts.push(`pruned: ${result.pruned.join(', ')}`)
  if (result.skipped.length > 0) parts.push(`skipped: ${result.skipped.join(', ')}`)
  if (result.errors.length > 0) parts.push(`errors: ${result.errors.join(', ')}`)
  notifications.show({
    color: result.errors.length > 0 ? 'red' : 'green',
    message: parts.length > 0 ? parts.join(' · ') : 'Nothing to prune.',
  })
}

export default function RuntimeList() {
  const { principal } = useAuth()
  const canPrune = principal?.permissions.includes('runtimes:write') ?? false

  const [runtimes, setRuntimes] = useState<Runtime[]>([])
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [nextCursor, setNextCursor] = useState<string | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [pruningSelected, setPruningSelected] = useState(false)
  const [olderThanDays, setOlderThanDays] = useState<number | ''>('')
  const [pruningByAge, setPruningByAge] = useState(false)

  const [name, setName] = useState('')
  const [lang, setLang] = useState<'python' | 'powershell' | 'node'>('python')
  const [baseImage, setBaseImage] = useState('')
  const [sysPackages, setSysPackages] = useState('')
  const [langManifest, setLangManifest] = useState('')
  const [createError, setCreateError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)

  function load(nextCursorArg?: string) {
    setLoading(true)
    listRuntimes({ cursor: nextCursorArg })
      .then((page) => {
        setRuntimes((prev) => (nextCursorArg ? [...prev, ...page.items] : page.items))
        setNextCursor(page.nextCursor)
      })
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading runtimes'))
      .finally(() => setLoading(false))
  }

  useEffect(() => load(cursor), [cursor])

  function toggleSelected(id: number) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  async function handlePruneSelected() {
    if (selected.size === 0) return
    setPruningSelected(true)
    try {
      const result = await pruneRuntimes({ ids: Array.from(selected) })
      showPruneResult(result)
      setSelected(new Set())
      setCursor(undefined)
      load(undefined)
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof APIError ? err.message : 'Failed pruning runtimes' })
    } finally {
      setPruningSelected(false)
    }
  }

  async function handlePruneByAge() {
    if (olderThanDays === '') return
    setPruningByAge(true)
    try {
      const result = await pruneRuntimes({ olderThanDays })
      showPruneResult(result)
      setCursor(undefined)
      load(undefined)
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof APIError ? err.message : 'Failed pruning runtimes' })
    } finally {
      setPruningByAge(false)
    }
  }

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
                  {canPrune && <Table.Th></Table.Th>}
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Lang</Table.Th>
                  <Table.Th>Build status</Table.Th>
                  <Table.Th>Image digest</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {runtimes.map((runtime) => (
                  <Table.Tr key={runtime.id}>
                    {canPrune && (
                      <Table.Td>
                        <Checkbox
                          checked={selected.has(runtime.id)}
                          onChange={() => toggleSelected(runtime.id)}
                          aria-label={`Select ${runtime.name}`}
                        />
                      </Table.Td>
                    )}
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

          {canPrune && (
            <Group gap="lg" mt="md" align="flex-end" wrap="wrap">
              <Button
                size="xs"
                variant="default"
                onClick={handlePruneSelected}
                disabled={selected.size === 0}
                loading={pruningSelected}
              >
                Prune selected ({selected.size})
              </Button>
              <Group gap="xs" align="flex-end">
                <NumberInput
                  label="Prune unused, older than"
                  size="xs"
                  w={90}
                  min={0}
                  value={olderThanDays}
                  onChange={(v) => setOlderThanDays(v === '' ? '' : Number(v))}
                />
                <Text size="xs" c="dimmed" mb={6}>
                  days
                </Text>
                <Button size="xs" variant="default" onClick={handlePruneByAge} disabled={olderThanDays === ''} loading={pruningByAge}>
                  Prune
                </Button>
              </Group>
            </Group>
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
