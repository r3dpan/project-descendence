import { useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import { getRun, isTerminal, runLogsStreamURL, type Run, type RunLogLine } from '../api/runs'
import { APIError } from '../api/client'

export default function RunDetail() {
  const { id } = useParams<{ id: string }>()
  const [run, setRun] = useState<Run | null>(null)
  const [lines, setLines] = useState<RunLogLine[]>([])
  const [error, setError] = useState<string | null>(null)
  const logRef = useRef<HTMLPreElement>(null)

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
    logRef.current?.scrollTo(0, logRef.current.scrollHeight)
  }, [lines])

  if (error) return <p role="alert" style={{ color: 'crimson' }}>{error}</p>
  if (!run) return <p>Loading…</p>

  return (
    <main>
      <h1>Run {run.id}</h1>
      <dl>
        <dt>State</dt>
        <dd>{run.state}</dd>
        <dt>Image</dt>
        <dd>{run.imageRef}</dd>
        <dt>Argv</dt>
        <dd>
          <code>{run.argv.join(' ')}</code>
        </dd>
        {run.exitCode !== null && run.exitCode !== undefined && (
          <>
            <dt>Exit code</dt>
            <dd>{run.exitCode}</dd>
          </>
        )}
        {run.failureReason && (
          <>
            <dt>Failure reason</dt>
            <dd>{run.failureReason}</dd>
          </>
        )}
      </dl>
      <h2>Logs</h2>
      <pre
        ref={logRef}
        style={{ background: '#111', color: '#eee', padding: '1rem', maxHeight: '60vh', overflow: 'auto' }}
      >
        {lines.map((line) => `${line.text}\n`).join('')}
      </pre>
    </main>
  )
}
