import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  Alert,
  Button,
  Checkbox,
  Grid,
  Group,
  Loader,
  Paper,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { createJobRun, getJob, patchJob, type Job } from '../api/jobs'
import {
  createSchedule,
  deleteSchedule,
  listSchedulesByJob,
  patchSchedule,
  triggerSchedule,
  type Schedule,
} from '../api/schedules'
import { APIError } from '../api/client'
import { useAuth } from '../auth'
import { ParamField } from '../paramField'
import PageHeader from '../components/PageHeader'
import StatusTag from '../components/StatusTag'
import FadeRule from '../components/FadeRule'

function SchedulesPanel({ jobId }: { jobId: number }) {
  const { principal } = useAuth()
  const canWrite = principal?.permissions.includes('schedules:write') ?? false
  const canTrigger = principal?.permissions.includes('schedules:trigger') ?? false

  const [schedules, setSchedules] = useState<Schedule[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [busyId, setBusyId] = useState<number | null>(null)

  const [cronExpr, setCronExpr] = useState('')
  const [timezone, setTimezone] = useState('')
  const [catchUpPolicy, setCatchUpPolicy] = useState<'skip' | 'catch_up'>('skip')
  const [overlapPolicy, setOverlapPolicy] = useState<'skip' | 'queue' | 'concurrent'>('skip')
  const [enabled, setEnabled] = useState(true)
  const [createError, setCreateError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)

  function refresh() {
    setLoading(true)
    listSchedulesByJob(jobId)
      .then((page) => setSchedules(page.items))
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading schedules'))
      .finally(() => setLoading(false))
  }

  useEffect(refresh, [jobId])

  async function handleTrigger(s: Schedule) {
    setBusyId(s.id)
    try {
      const result = await triggerSchedule(s.id)
      notifications.show({
        color: result.skipped ? 'gray' : 'green',
        message: result.skipped ? `Skipped: ${result.reason}` : `Run #${result.run?.id} created`,
      })
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof APIError ? err.message : 'Failed triggering schedule' })
    } finally {
      setBusyId(null)
    }
  }

  async function handleToggle(s: Schedule) {
    setBusyId(s.id)
    try {
      const updated = await patchSchedule(s.id, { enabled: !s.enabled })
      setSchedules((prev) => prev.map((x) => (x.id === updated.id ? updated : x)))
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof APIError ? err.message : 'Failed updating schedule' })
    } finally {
      setBusyId(null)
    }
  }

  async function handleDelete(s: Schedule) {
    if (!window.confirm(`Delete the schedule "${s.cronExpr}"? This cannot be undone.`)) return
    setBusyId(s.id)
    try {
      await deleteSchedule(s.id)
      setSchedules((prev) => prev.filter((x) => x.id !== s.id))
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof APIError ? err.message : 'Failed deleting schedule' })
    } finally {
      setBusyId(null)
    }
  }

  async function handleCreate(e: FormEvent) {
    e.preventDefault()
    setCreateError(null)
    setCreating(true)
    try {
      const created = await createSchedule(jobId, {
        cronExpr,
        timezone: timezone || undefined,
        catchUpPolicy,
        overlapPolicy,
        enabled,
      })
      setSchedules((prev) => [...prev, created])
      setCronExpr('')
      setTimezone('')
    } catch (err) {
      setCreateError(err instanceof APIError ? err.message : 'Failed creating schedule')
    } finally {
      setCreating(false)
    }
  }

  return (
    <Paper p="md" radius="md" bg="dark.6">
      <Text size="10px" tt="uppercase" c="accent.4" fw={600} style={{ letterSpacing: '.1em' }} mb="sm">
        Schedules
      </Text>
      {error && (
        <Alert color="red" role="alert" mb="sm">
          {error}
        </Alert>
      )}
      {!loading && schedules.length === 0 && (
        <Text size="13px" c="dimmed" mb="sm">
          No schedules for this job.
        </Text>
      )}
      <Stack gap="xs" mb={canWrite ? 'md' : 0}>
        {schedules.map((s) => (
          <div key={s.id} style={{ borderBottom: '1px solid var(--mantine-color-dark-4)', paddingBottom: 8 }}>
            <Group justify="space-between" gap="sm" wrap="nowrap">
              <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
                <StatusTag status={s.enabled ? 'enabled' : 'disabled'} label={s.enabled ? 'On' : 'Off'} />
                <Text size="13px" fw={500} truncate>
                  {s.cronExpr}
                </Text>
                <Text size="11.5px" c="dimmed">
                  {s.timezone}
                </Text>
              </Group>
              <Group gap={4} wrap="nowrap">
                {canTrigger && (
                  <Button size="xs" variant="subtle" onClick={() => handleTrigger(s)} disabled={busyId === s.id}>
                    Trigger
                  </Button>
                )}
                {canWrite && (
                  <>
                    <Button size="xs" variant="subtle" onClick={() => handleToggle(s)} disabled={busyId === s.id}>
                      {s.enabled ? 'Disable' : 'Enable'}
                    </Button>
                    <Button size="xs" variant="subtle" color="red" onClick={() => handleDelete(s)} disabled={busyId === s.id}>
                      Delete
                    </Button>
                  </>
                )}
              </Group>
            </Group>
            <Text size="11px" c="dimmed" mt={2}>
              catch-up: {s.catchUpPolicy} · overlap: {s.overlapPolicy}
              {s.nextDueAt && ` · next ${new Date(s.nextDueAt).toLocaleString()}`}
            </Text>
          </div>
        ))}
      </Stack>

      {canWrite && (
        <Stack gap="sm" component="form" onSubmit={handleCreate}>
          <TextInput
            label="Cron expression"
            size="xs"
            placeholder="0 2 * * *"
            value={cronExpr}
            onChange={(e) => setCronExpr(e.target.value)}
            required
          />
          <TextInput
            label="Timezone"
            size="xs"
            description="Optional, defaults to UTC"
            placeholder="America/New_York"
            value={timezone}
            onChange={(e) => setTimezone(e.target.value)}
          />
          <div>
            <Text size="11px" c="dimmed" mb={4}>
              Catch-up policy
            </Text>
            <SegmentedControl
              size="xs"
              value={catchUpPolicy}
              onChange={(v) => setCatchUpPolicy(v as typeof catchUpPolicy)}
              data={[
                { label: 'Skip', value: 'skip' },
                { label: 'Catch up', value: 'catch_up' },
              ]}
            />
          </div>
          <div>
            <Text size="11px" c="dimmed" mb={4}>
              Overlap policy
            </Text>
            <SegmentedControl
              size="xs"
              value={overlapPolicy}
              onChange={(v) => setOverlapPolicy(v as typeof overlapPolicy)}
              data={[
                { label: 'Skip', value: 'skip' },
                { label: 'Queue', value: 'queue' },
                { label: 'Concurrent', value: 'concurrent' },
              ]}
            />
          </div>
          <Checkbox label="Enabled" checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} />
          {createError && (
            <Alert color="red" role="alert">
              {createError}
            </Alert>
          )}
          <Button type="submit" size="xs" w="fit-content" loading={creating}>
            Add schedule
          </Button>
        </Stack>
      )}
    </Paper>
  )
}

export default function JobDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { principal } = useAuth()
  const canSeeSchedules = principal?.permissions.includes('schedules:read') ?? false

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
      <Grid maw={1080}>
        <Grid.Col span={{ base: 12, md: 7 }}>
          <Stack gap="md">
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
                <Button size="xs" variant="subtle" component={Link} to={`/jobs/${job.id}/script`}>
                  Edit script
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
        </Grid.Col>

        {canSeeSchedules && (
          <Grid.Col span={{ base: 12, md: 5 }}>
            <SchedulesPanel jobId={job.id} />
          </Grid.Col>
        )}
      </Grid>
    </PageHeader>
  )
}
