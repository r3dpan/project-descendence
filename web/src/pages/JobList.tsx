import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listJobs, type Job } from '../api/jobs'
import { APIError } from '../api/client'

export default function JobList() {
  const [jobs, setJobs] = useState<Job[]>([])
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [nextCursor, setNextCursor] = useState<string | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    listJobs({ cursor })
      .then((page) => {
        setJobs((prev) => (cursor ? [...prev, ...page.items] : page.items))
        setNextCursor(page.nextCursor)
      })
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading jobs'))
      .finally(() => setLoading(false))
  }, [cursor])

  return (
    <main>
      <h1>Jobs</h1>
      {error && (
        <p role="alert" style={{ color: 'crimson' }}>
          {error}
        </p>
      )}
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Description</th>
            <th>Enabled</th>
            <th>Params</th>
          </tr>
        </thead>
        <tbody>
          {jobs.map((job) => (
            <tr key={job.id}>
              <td>
                <Link to={`/jobs/${job.id}`}>{job.name}</Link>
              </td>
              <td>{job.description ?? ''}</td>
              <td>{job.enabled ? 'yes' : 'no'}</td>
              <td>{job.params.length}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {loading && <p>Loading…</p>}
      {!loading && jobs.length === 0 && <p>No jobs yet.</p>}
      {!loading && nextCursor && (
        <button type="button" onClick={() => setCursor(nextCursor)}>
          Load more
        </button>
      )}
    </main>
  )
}
