import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Alert, Button, Code, Group, Loader, Paper, SimpleGrid, Stack, Text } from '@mantine/core'
import { cancelRun, getRun, isTerminal, runLogsStreamURL, type Run, type RunLogLine } from '../api/runs'
import { APIError } from '../api/client'
import { useAuth } from '../auth'
import PageHeader from '../components/PageHeader'
import StatusTag from '../components/StatusTag'

function formatDuration(startedAt?: string | null, finishedAt?: string | null): string {
  if (!startedAt) return '—'
  const end = finishedAt ? new Date(finishedAt) : new Date()
  const seconds = Math.max(0, Math.round((end.getTime() - new Date(startedAt).getTime()) / 1000))
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return m > 0 ? `${m}m ${s}s` : `${s}s`
}

export default function RunDetail() {
  const { id } = useParams<{ id: string }>()
  const { principal } = useAuth()
  const canCancel = principal?.permissions.includes('runs:cancel') ?? false
  const [run, setRun] = useState<Run | null>(null)
  const [lines, setLines] = useState<RunLogLine[]>([])
  const [error, setError] = useState<string | null>(null)
  const [cancelling, setCancelling] = useState(false)
  const [cancelError, setCancelError] = useState<string | null>(null)
  const logRef = useRef<HTMLDivElement>(null)

  async function handleCancel() {
    if (!run) return
    if (!window.confirm(`Cancel run #${run.id}? This cannot be undone.`)) return
    setCancelError(null)
    setCancelling(true)
    try {
      const updated = await cancelRun(run.id)
      setRun(updated)
    } catch (err) {
      setCancelError(err instanceof APIError ? err.message : 'Failed cancelling run')
    } finally {
      setCancelling(false)
    }
  }

  useEffect(() => {
    if (!id) return
    getRun(id)
      .then(setRun)
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading run'))
  }, [id])

  // Native EventSource: no custom headers, cookie auth rides along because
  // this is same-origin (ARCHITECTURE.md §4.11) - the concrete point this
  // view exists to prove out. The browser handles Last-Event-ID on
  // reconnect by itself.
  useEffect(() => {
    if (!id) return

    const source = new EventSource(runLogsStreamURL(id))

    source.addEventListener('log', (evt) => {
      const line = JSON.parse((evt as MessageEvent).data) as RunLogLine
      setLines((prev) => [...prev, line])
    })

    source.addEventListener('state', (evt) => {
      const data = JSON.parse((evt as MessageEvent).data) as { runState: Run['state'] }
      setRun((prev) => (prev ? { ...prev, state: data.runState } : prev))
      if (isTerminal(data.runState)) {
        source.close()
      }
    })

    source.onerror = () => {
      // A non-terminal close is abnormal (network blip, server restart);
      // EventSource retries on its own using retry: 3000 from the server.
      // Nothing to do here but let it.
    }

    return () => source.close()
  }, [id])

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight })
  }, [lines])

  if (error)
    return (
      <PageHeader title="Run" backTo="/runs" backLabel="Runs">
        <Alert color="red" role="alert">
          {error}
        </Alert>
      </PageHeader>
    )
  if (!run)
    return (
      <PageHeader title="Run" backTo="/runs" backLabel="Runs">
        <Loader />
      </PageHeader>
    )

  return (
    <PageHeader
      title={`Run #${run.id}`}
      subtitle={run.imageRef}
      backTo="/runs"
      backLabel="Runs"
      action={
        canCancel &&
        !isTerminal(run.state) && (
          <Button size="xs" color="red" variant="subtle" onClick={handleCancel} loading={cancelling}>
            Cancel run
          </Button>
        )
      }
    >
      <Stack gap="lg" maw={1080}>
        {cancelError && (
          <Alert color="red" role="alert">
            {cancelError}
          </Alert>
        )}
        <Group gap="sm">
          <StatusTag status={run.state} />
          <Text
            component={Link}
            to={`/runs/${run.id}`}
            c="accent.4"
            fw={500}
            style={{ textDecoration: 'none' }}
          >
            {run.imageRef}
          </Text>
        </Group>

        <SimpleGrid cols={{ base: 2, sm: 4 }}>
          <Paper p="sm" radius="md" bg="dark.6">
            <Text size="10px" tt="uppercase" c="accent.4" fw={600} style={{ letterSpacing: '.1em' }} mb={2}>
              Started
            </Text>
            <Text size="13px">{run.startedAt ? new Date(run.startedAt).toLocaleString() : 'Not started'}</Text>
          </Paper>
          <Paper p="sm" radius="md" bg="dark.6">
            <Text size="10px" tt="uppercase" c="accent.4" fw={600} style={{ letterSpacing: '.1em' }} mb={2}>
              Duration
            </Text>
            <Text size="13px">{formatDuration(run.startedAt, run.finishedAt)}</Text>
          </Paper>
          <Paper p="sm" radius="md" bg="dark.6">
            <Text size="10px" tt="uppercase" c="accent.4" fw={600} style={{ letterSpacing: '.1em' }} mb={2}>
              Exit code
            </Text>
            <Text size="13px">{run.exitCode ?? '—'}</Text>
          </Paper>
          <Paper p="sm" radius="md" bg="dark.6">
            <Text size="10px" tt="uppercase" c="accent.4" fw={600} style={{ letterSpacing: '.1em' }} mb={2}>
              Timeout
            </Text>
            <Text size="13px">{run.timeoutSeconds}s</Text>
          </Paper>
        </SimpleGrid>

        <Paper p="md" radius="md" bg="dark.6">
          <Stack gap="xs">
            <Group align="flex-start" gap="md">
              <Text fw={500} w={120} size="sm">
                Argv
              </Text>
              <Code>{run.argv.join(' ')}</Code>
            </Group>
            {run.failureReason && (
              <Group align="flex-start" gap="md">
                <Text fw={500} w={120} size="sm">
                  Failure reason
                </Text>
                <Text size="sm">{run.failureReason}</Text>
              </Group>
            )}
            {run.cancelRequestedAt && (
              <Group align="flex-start" gap="md">
                <Text fw={500} w={120} size="sm">
                  Cancel requested
                </Text>
                <Text size="sm">{new Date(run.cancelRequestedAt).toLocaleString()}</Text>
              </Group>
            )}
          </Stack>
        </Paper>

        <Paper radius="md" bg="dark.6" style={{ overflow: 'hidden' }}>
          <Group justify="space-between" px="md" py="sm" style={{ borderBottom: '1px solid var(--mantine-color-dark-4)' }}>
            <Text fw={500} size="17px">
              Logs
            </Text>
          </Group>
          <div
            ref={logRef}
            style={{
              padding: 'var(--mantine-spacing-md)',
              fontFamily: 'var(--mantine-font-family-monospace)',
              fontSize: '12.5px',
              lineHeight: 1.9,
              maxHeight: 360,
              overflowY: 'auto',
              whiteSpace: 'pre-wrap',
            }}
          >
            {lines.length === 0 && (
              <Text c="dimmed" size="sm">
                No log output yet.
              </Text>
            )}
            {lines.map((line, i) => (
              <div key={i}>{line.text}</div>
            ))}
          </div>
        </Paper>
      </Stack>
    </PageHeader>
  )
}
