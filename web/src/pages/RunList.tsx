import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listRuns, type Run } from '../api/runs'
import { APIError } from '../api/client'

export default function RunList() {
  const [runs, setRuns] = useState<Run[]>([])
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [nextCursor, setNextCursor] = useState<string | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    listRuns({ cursor })
      .then((page) => {
        setRuns((prev) => (cursor ? [...prev, ...page.items] : page.items))
        setNextCursor(page.nextCursor)
      })
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading runs'))
      .finally(() => setLoading(false))
  }, [cursor])

  return (
    <main>
      <h1>Runs</h1>
      {error && (
        <p role="alert" style={{ color: 'crimson' }}>
          {error}
        </p>
      )}
      <table>
        <thead>
          <tr>
            <th>ID</th>
            <th>State</th>
            <th>Image</th>
            <th>Queued</th>
          </tr>
        </thead>
        <tbody>
          {runs.map((run) => (
            <tr key={run.id}>
              <td>
                <Link to={`/runs/${run.id}`}>{run.id}</Link>
              </td>
              <td>{run.state}</td>
              <td>{run.imageRef}</td>
              <td>{new Date(run.queuedAt).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {loading && <p>Loading…</p>}
      {!loading && nextCursor && (
        <button type="button" onClick={() => setCursor(nextCursor)}>
          Load more
        </button>
      )}
    </main>
  )
}
