import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { Alert, Badge, Button, Code, Group, Loader, Paper, Stack, Text, Title } from '@mantine/core'
import { buildRuntime, getRuntime, TERMINAL_BUILD_STATUSES, type Runtime } from '../api/runtimes'
import { APIError } from '../api/client'
import { statusColor } from '../statusColor'

export default function RuntimeDetail() {
  const { id } = useParams<{ id: string }>()
  const [runtime, setRuntime] = useState<Runtime | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [rebuildError, setRebuildError] = useState<string | null>(null)
  const [rebuilding, setRebuilding] = useState(false)

  useEffect(() => {
    if (!id) return
    getRuntime(id)
      .then(setRuntime)
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading runtime'))
  }, [id])

  // No SSE endpoint for build status (unlike a run's logs) - poll while a
  // build is in flight and stop once it reaches a terminal status.
  useEffect(() => {
    if (!id || !runtime || TERMINAL_BUILD_STATUSES.has(runtime.buildStatus)) return

    const interval = setInterval(() => {
      getRuntime(id).then(setRuntime).catch(() => {})
    }, 2000)

    return () => clearInterval(interval)
  }, [id, runtime])

  async function handleRebuild() {
    if (!runtime) return
    setRebuildError(null)
    setRebuilding(true)
    try {
      await buildRuntime(runtime.id)
      const updated = await getRuntime(runtime.id)
      setRuntime(updated)
    } catch (err) {
      setRebuildError(err instanceof APIError ? err.message : 'Failed queuing rebuild')
    } finally {
      setRebuilding(false)
    }
  }

  if (error) return <Alert color="red" role="alert">{error}</Alert>
  if (!runtime) return <Loader />

  const rebuildDisabled = rebuilding || runtime.buildStatus === 'pending' || runtime.buildStatus === 'building'

  return (
    <>
      <Title order={2} mb="md">
        {runtime.name}
      </Title>
      <Paper withBorder p="md" maw={640}>
        <Stack gap="xs">
          <Group>
            <Text fw={500} w={160}>
              Language
            </Text>
            <Text>{runtime.lang}</Text>
          </Group>
          <Group>
            <Text fw={500} w={160}>
              Base image
            </Text>
            <Text>{runtime.baseImage}</Text>
          </Group>
          <Group>
            <Text fw={500} w={160}>
              System packages
            </Text>
            <Text>{runtime.sysPackages.length > 0 ? runtime.sysPackages.join(', ') : '(none)'}</Text>
          </Group>
          <Group>
            <Text fw={500} w={160}>
              Build status
            </Text>
            <Badge color={statusColor(runtime.buildStatus)}>{runtime.buildStatus}</Badge>
          </Group>
          {runtime.buildError && (
            <Group align="flex-start">
              <Text fw={500} w={160}>
                Build error
              </Text>
              <Text c="red">{runtime.buildError}</Text>
            </Group>
          )}
          <Group>
            <Text fw={500} w={160}>
              Image digest
            </Text>
            <Code>{runtime.imageDigest ?? '(not built)'}</Code>
          </Group>
          {runtime.imagePruned && (
            <Group>
              <Text fw={500} w={160}>
                Image pruned
              </Text>
              <Text>Yes - a job naming this runtime cannot run until it is rebuilt.</Text>
            </Group>
          )}
          <Group align="flex-start">
            <Text fw={500} w={160}>
              Language manifest
            </Text>
            <Code block>{runtime.langManifest ?? '(none)'}</Code>
          </Group>
        </Stack>
      </Paper>

      <Button mt="md" onClick={handleRebuild} disabled={rebuildDisabled}>
        {rebuilding || runtime.buildStatus === 'building' || runtime.buildStatus === 'pending'
          ? 'Building…'
          : 'Rebuild'}
      </Button>
      {rebuildError && (
        <Alert color="red" role="alert" mt="md">
          {rebuildError}
        </Alert>
      )}
    </>
  )
}
