import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { createJobRun, getJob, patchJob, type Job } from '../api/jobs'
import { APIError } from '../api/client'
import { ParamField } from '../paramField'

export default function JobDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [job, setJob] = useState<Job | null>(null)
  const [values, setValues] = useState<Record<string, string>>({})
  const [error, setError] = useState<string | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [toggling, setToggling] = useState(false)
  const [toggleError, setToggleError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    getJob(id)
      .then((j) => {
        setJob(j)
        // Pre-fill with each param's declared default so an unmodified
        // submit reproduces "run with defaults" rather than sending empties.
        const initial: Record<string, string> = {}
        for (const param of j.params) {
          if (param.default !== null && param.default !== undefined) {
            initial[param.name] = param.default
          } else if (param.type === 'bool') {
            initial[param.name] = 'false'
          } else {
            initial[param.name] = ''
          }
        }
        setValues(initial)
      })
      .catch((err) => setLoadError(err instanceof APIError ? err.message : 'Failed loading job'))
  }, [id])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!job) return
    setError(null)
    setSubmitting(true)
    try {
      // Only send params with a non-empty value: an omitted key lets the
      // server apply its own default rather than this form duplicating it,
      // matching --param name=value's "only pass what you're overriding"
      // shape on the CLI.
      const params: Record<string, string> = {}
      for (const param of job.params) {
        const value = values[param.name]
        if (param.type === 'bool' || value !== '') {
          params[param.name] = value
        }
      }

      const run = await createJobRun(job.id, params)
      navigate(`/runs/${run.id}`)
    } catch (err) {
      setError(err instanceof APIError ? err.message : 'Failed triggering run')
    } finally {
      setSubmitting(false)
    }
  }

  async function handleToggle() {
    if (!job) return
    setToggleError(null)
    setToggling(true)
    try {
      const updated = await patchJob(job.id, !job.enabled)
      setJob(updated)
    } catch (err) {
      setToggleError(err instanceof APIError ? err.message : 'Failed updating job')
    } finally {
      setToggling(false)
    }
  }

  if (loadError) return <p role="alert" style={{ color: 'crimson' }}>{loadError}</p>
  if (!job) return <p>Loading…</p>

  return (
    <main>
      <h1>{job.name}</h1>
      {job.description && <p>{job.description}</p>}
      {job.deletedAt && (
        <p role="alert" style={{ color: 'crimson' }}>
          This job's manifest has been removed from the repository and can no longer be run.
        </p>
      )}
      {!job.enabled && !job.deletedAt && (
        <p role="alert" style={{ color: 'darkorange' }}>
          This job is disabled and cannot be run until it is re-enabled.
        </p>
      )}

      {!job.deletedAt && (
        <p>
          <button type="button" onClick={handleToggle} disabled={toggling}>
            {toggling ? 'Updating…' : job.enabled ? 'Disable job' : 'Enable job'}
          </button>
          {toggleError && (
            <span role="alert" style={{ color: 'crimson', marginLeft: '0.5rem' }}>
              {toggleError}
            </span>
          )}
        </p>
      )}

      <form onSubmit={handleSubmit}>
        {job.params.map((param) => (
          <ParamField
            key={param.name}
            param={param}
            value={values[param.name] ?? ''}
            onChange={(value) => setValues((prev) => ({ ...prev, [param.name]: value }))}
          />
        ))}

        {error && (
          <p role="alert" style={{ color: 'crimson' }}>
            {error}
          </p>
        )}

        <button type="submit" disabled={submitting || !job.enabled || !!job.deletedAt} style={{ marginTop: '1rem' }}>
          {submitting ? 'Triggering…' : 'Run'}
        </button>
      </form>
    </main>
  )
}
