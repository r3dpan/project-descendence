import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Alert, Button, Group, Loader, Paper, Stack, Text } from '@mantine/core'
import { createJobRun, getJob, patchJob, type Job } from '../api/jobs'
import { APIError } from '../api/client'
import { ParamField } from '../paramField'
import PageHeader from '../components/PageHeader'
import StatusTag from '../components/StatusTag'
import FadeRule from '../components/FadeRule'

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

  if (loadError)
    return (
      <PageHeader title="Job" backTo="/jobs" backLabel="Jobs">
        <Alert color="red" role="alert">
          {loadError}
        </Alert>
      </PageHeader>
    )
  if (!job)
    return (
      <PageHeader title="Job" backTo="/jobs" backLabel="Jobs">
        <Loader />
      </PageHeader>
    )

  return (
    <PageHeader title={job.name} subtitle="Job detail" backTo="/jobs" backLabel="Jobs">
      <Stack gap="md" maw={480}>
        <Group gap="sm">
          <StatusTag status={job.enabled ? 'enabled' : 'disabled'} label={job.enabled ? 'Enabled' : 'Disabled'} />
        </Group>
        {job.description && (
          <Text c="dimmed" mt={-8}>
            {job.description}
          </Text>
        )}

        {job.deletedAt && (
          <Alert color="red" role="alert">
            This job's manifest has been removed from the repository and can no longer be run.
          </Alert>
        )}
        {!job.enabled && !job.deletedAt && (
          <Alert color="orange" role="alert">
            This job is disabled and cannot be run until it is re-enabled.
          </Alert>
        )}

        {!job.deletedAt && (
          <Group gap="sm">
            <Button size="xs" variant="default" onClick={handleToggle} disabled={toggling}>
              {toggling ? 'Updating…' : job.enabled ? 'Disable' : 'Enable'}
            </Button>
            <Button size="xs" variant="subtle" component={Link} to={`/jobs/${job.id}/edit`}>
              Edit manifest
            </Button>
            {toggleError && (
              <Text role="alert" c="red" size="sm">
                {toggleError}
              </Text>
            )}
          </Group>
        )}

        <FadeRule />

        <Paper p="md" radius="md" bg="dark.6" component="form" onSubmit={handleSubmit}>
          <Text size="10px" tt="uppercase" c="accent.4" fw={600} style={{ letterSpacing: '.1em' }} mb="xs">
            Parameters
          </Text>
          {job.params.length === 0 && (
            <Text size="13px" c="dimmed">
              This job has no declared parameters.
            </Text>
          )}
          {job.params.map((param) => (
            <ParamField
              key={param.name}
              param={param}
              value={values[param.name] ?? ''}
              onChange={(value) => setValues((prev) => ({ ...prev, [param.name]: value }))}
            />
          ))}

          {error && (
            <Alert color="red" role="alert" mt="md">
              {error}
            </Alert>
          )}

          <Button type="submit" mt="md" loading={submitting} disabled={!job.enabled || !!job.deletedAt}>
            Run
          </Button>
        </Paper>
      </Stack>
    </PageHeader>
  )
}
