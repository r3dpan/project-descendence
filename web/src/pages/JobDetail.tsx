import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Alert, Button, Fieldset, Group, Loader, Text, Title } from '@mantine/core'
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

  if (loadError) return <Alert color="red" role="alert">{loadError}</Alert>
  if (!job) return <Loader />

  return (
    <>
      <Title order={2} mb="xs">
        {job.name}
      </Title>
      {job.description && <Text mb="md">{job.description}</Text>}
      {job.deletedAt && (
        <Alert color="red" role="alert" mb="md">
          This job's manifest has been removed from the repository and can no longer be run.
        </Alert>
      )}
      {!job.enabled && !job.deletedAt && (
        <Alert color="orange" role="alert" mb="md">
          This job is disabled and cannot be run until it is re-enabled.
        </Alert>
      )}

      {!job.deletedAt && (
        <Group mb="md">
          <Button size="xs" variant="light" onClick={handleToggle} disabled={toggling}>
            {toggling ? 'Updating…' : job.enabled ? 'Disable job' : 'Enable job'}
          </Button>
          <Text component={Link} to={`/jobs/${job.id}/edit`} size="sm">
            Edit manifest
          </Text>
          {toggleError && (
            <Text role="alert" c="red" size="sm">
              {toggleError}
            </Text>
          )}
        </Group>
      )}

      <form onSubmit={handleSubmit}>
        <Fieldset legend="Parameters" maw={480}>
          {job.params.map((param) => (
            <ParamField
              key={param.name}
              param={param}
              value={values[param.name] ?? ''}
              onChange={(value) => setValues((prev) => ({ ...prev, [param.name]: value }))}
            />
          ))}
        </Fieldset>

        {error && (
          <Alert color="red" role="alert" mt="md">
            {error}
          </Alert>
        )}

        <Button type="submit" mt="md" loading={submitting} disabled={!job.enabled || !!job.deletedAt}>
          {submitting ? 'Triggering…' : 'Run'}
        </Button>
      </form>
    </>
  )
}
