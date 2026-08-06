import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { Alert, Button, Code, Group, Loader, Paper, Stack, Text } from '@mantine/core'
import { buildRuntime, getRuntime, TERMINAL_BUILD_STATUSES, type Runtime } from '../api/runtimes'
import { APIError } from '../api/client'
import PageHeader from '../components/PageHeader'
import StatusTag from '../components/StatusTag'

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Group justify="space-between" gap="md" align="flex-start" wrap="nowrap">
      <Text size="13px" c="dimmed" w={150} style={{ flex: 'none' }}>
        {label}
      </Text>
      <div style={{ flex: 1, minWidth: 0 }}>{children}</div>
    </Group>
  )
}

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

  if (error)
    return (
      <PageHeader title="Runtime" backTo="/runtimes" backLabel="Runtimes">
        <Alert color="red" role="alert">
          {error}
        </Alert>
      </PageHeader>
    )
  if (!runtime)
    return (
      <PageHeader title="Runtime" backTo="/runtimes" backLabel="Runtimes">
        <Loader />
      </PageHeader>
    )

  const rebuildDisabled = rebuilding || runtime.buildStatus === 'pending' || runtime.buildStatus === 'building'
  const rebuildLabel =
    rebuilding || runtime.buildStatus === 'building' || runtime.buildStatus === 'pending' ? 'Building…' : 'Rebuild'

  return (
    <PageHeader title={runtime.name} subtitle="Runtime detail" backTo="/runtimes" backLabel="Runtimes">
      <Stack gap="md" maw={640}>
        <Paper p="md" radius="md" bg="dark.6">
          <Stack gap="10px">
            <Row label="Language">
              <Text size="13px">{runtime.lang}</Text>
            </Row>
            <Row label="Base image">
              <Text size="13px">{runtime.baseImage}</Text>
            </Row>
            <Row label="System packages">
              <Text size="13px">{runtime.sysPackages.length > 0 ? runtime.sysPackages.join(', ') : '(none)'}</Text>
            </Row>
            <Row label="Build status">
              <StatusTag status={runtime.buildStatus} />
            </Row>
            {runtime.buildError && (
              <Row label="Build error">
                <Text size="13px" c="red.3">
                  {runtime.buildError}
                </Text>
              </Row>
            )}
            <Row label="Image digest">
              <Code style={{ fontSize: '12.5px' }}>{runtime.imageDigest ?? '(not built)'}</Code>
            </Row>
            {runtime.imagePruned && (
              <Row label="Image pruned">
                <Text size="13px">Yes — a job naming this runtime cannot run until it is rebuilt.</Text>
              </Row>
            )}
            <Row label="Language manifest">
              <Code block style={{ fontSize: '12px', whiteSpace: 'pre-wrap' }}>
                {runtime.langManifest ?? '(none)'}
              </Code>
            </Row>
          </Stack>
        </Paper>

        <Button w="fit-content" onClick={handleRebuild} disabled={rebuildDisabled}>
          {rebuildLabel}
        </Button>
        {rebuildError && (
          <Alert color="red" role="alert">
            {rebuildError}
          </Alert>
        )}
      </Stack>
    </PageHeader>
  )
}
