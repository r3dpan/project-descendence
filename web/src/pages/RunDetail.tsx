import { useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import { Alert, Badge, Code, Group, Loader, Paper, ScrollArea, Stack, Text, Title } from '@mantine/core'
import { getRun, isTerminal, runLogsStreamURL, type Run, type RunLogLine } from '../api/runs'
import { APIError } from '../api/client'
import { statusColor } from '../statusColor'

export default function RunDetail() {
  const { id } = useParams<{ id: string }>()
  const [run, setRun] = useState<Run | null>(null)
  const [lines, setLines] = useState<RunLogLine[]>([])
  const [error, setError] = useState<string | null>(null)
  const logRef = useRef<HTMLDivElement>(null)

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

  if (error) return <Alert color="red" role="alert">{error}</Alert>
  if (!run) return <Loader />

  return (
    <>
      <Title order={2} mb="md">
        Run {run.id}
      </Title>
      <Paper withBorder p="md" maw={640} mb="lg">
        <Stack gap="xs">
          <Group>
            <Text fw={500} w={120}>
              State
            </Text>
            <Badge color={statusColor(run.state)}>{run.state}</Badge>
          </Group>
          <Group>
            <Text fw={500} w={120}>
              Image
            </Text>
            <Text>{run.imageRef}</Text>
          </Group>
          <Group align="flex-start">
            <Text fw={500} w={120}>
              Argv
            </Text>
            <Code>{run.argv.join(' ')}</Code>
          </Group>
          {run.exitCode !== null && run.exitCode !== undefined && (
            <Group>
              <Text fw={500} w={120}>
                Exit code
              </Text>
              <Text>{run.exitCode}</Text>
            </Group>
          )}
          {run.failureReason && (
            <Group align="flex-start">
              <Text fw={500} w={120}>
                Failure reason
              </Text>
              <Text>{run.failureReason}</Text>
            </Group>
          )}
        </Stack>
      </Paper>
      <Title order={3} mb="sm">
        Logs
      </Title>
      <ScrollArea.Autosize mah="60vh" viewportRef={logRef} type="auto">
        <Code block style={{ whiteSpace: 'pre-wrap' }}>
          {lines.map((line) => `${line.text}\n`).join('')}
        </Code>
      </ScrollArea.Autosize>
    </>
  )
}
