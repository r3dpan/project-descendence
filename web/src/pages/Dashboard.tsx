import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Alert, Group, Paper, Stack, Text } from '@mantine/core'
import { IconArrowRight } from '@tabler/icons-react'
import { useAuth } from '../auth'
import { getRunStats, listRuns, type Run, type RunStats } from '../api/runs'
import { getSystemStatus, type SystemStatus } from '../api/system'
import { APIError } from '../api/client'
import { statusColor } from '../statusColor'
import PageHeader from '../components/PageHeader'
import StatTileGrid, { type Tile } from '../components/StatTile'
import StatusTag from '../components/StatusTag'

// 24h, matching the server's own default window (RunStatsHandler) - kept in
// sync deliberately rather than omitting `since` and trusting the server's
// default, so the window shown in the "since" label is never a guess.
const STATS_WINDOW = '24h'

export default function Dashboard() {
  const { principal } = useAuth()
  const [stats, setStats] = useState<RunStats | null>(null)
  const [statsError, setStatsError] = useState<string | null>(null)
  const [status, setStatus] = useState<SystemStatus | null>(null)
  const [statusError, setStatusError] = useState<string | null>(null)
  const [recentRuns, setRecentRuns] = useState<Run[]>([])
  const [recentError, setRecentError] = useState<string | null>(null)

  useEffect(() => {
    getRunStats(STATS_WINDOW)
      .then(setStats)
      .catch((err) => setStatsError(err instanceof APIError ? err.message : 'Failed loading run stats'))
  }, [])

  useEffect(() => {
    getSystemStatus()
      .then(setStatus)
      .catch((err) => setStatusError(err instanceof APIError ? err.message : 'Failed loading system status'))
  }, [])

  useEffect(() => {
    listRuns({ limit: 5 })
      .then((page) => setRecentRuns(page.items))
      .catch((err) => setRecentError(err instanceof APIError ? err.message : 'Failed loading recent runs'))
  }, [])

  const statsTiles: Tile[] = stats
    ? [
        { label: 'Queued', value: stats.queued, color: statusColor('queued'), caption: 'right now' },
        { label: 'Succeeded', value: stats.succeeded, color: statusColor('succeeded'), caption: `since ${STATS_WINDOW}` },
        { label: 'Failed', value: stats.failed, color: statusColor('failed'), caption: `since ${STATS_WINDOW}` },
        { label: 'Cancelled', value: stats.cancelled, color: statusColor('cancelled'), caption: `since ${STATS_WINDOW}` },
        { label: 'Lost', value: stats.lost, color: statusColor('lost'), caption: `since ${STATS_WINDOW}` },
      ]
    : []

  const statusTiles: Tile[] = status
    ? [
        {
          label: 'Database',
          value: status.database.status === 'up' ? 'Connected' : 'Not available',
          color: statusColor(status.database.status === 'up' ? 'succeeded' : 'failed'),
          caption: status.database.detail,
        },
        {
          label: 'Podman',
          value: status.podman.status === 'up' ? 'Reachable' : 'Not reachable',
          color: statusColor(status.podman.status === 'up' ? 'succeeded' : 'failed'),
          caption: status.podman.detail,
        },
        {
          label: 'Supervisor',
          value: status.supervisor.status === 'up' ? 'Running' : 'Not running',
          color: statusColor(status.supervisor.status === 'up' ? 'succeeded' : 'failed'),
          caption: status.supervisor.detail,
        },
      ]
    : []

  return (
    <PageHeader
      title="Dashboard"
      subtitle={
        principal?.lastLoginAt
          ? `Last login ${new Date(principal.lastLoginAt).toLocaleString()}`
          : 'Overview of your automations'
      }
    >
      <Stack gap="xl" maw={1080}>
        <div>
          <Text size="11px" tt="uppercase" c="dimmed" fw={600} style={{ letterSpacing: '.08em' }} mb="sm">
            Run activity
          </Text>
          {statsError && (
            <Alert color="red" role="alert" mb="md">
              {statsError}
            </Alert>
          )}
          <StatTileGrid tiles={statsTiles} />
        </div>

        <div>
          <Text size="11px" tt="uppercase" c="dimmed" fw={600} style={{ letterSpacing: '.08em' }} mb="sm">
            System status
          </Text>
          {statusError && (
            <Alert color="red" role="alert" mb="md">
              {statusError}
            </Alert>
          )}
          <StatTileGrid tiles={statusTiles} cols={3} />
        </div>

        <Paper p="md" radius="md" bg="dark.6">
          <Group justify="space-between" mb="sm">
            <Text fw={500} size="17px">
              Recent activity
            </Text>
            <Text
              component={Link}
              to="/runs"
              size="sm"
              c="accent.4"
              display="flex"
              style={{ alignItems: 'center', gap: 4, textDecoration: 'none' }}
            >
              View all runs
              <IconArrowRight size={12} />
            </Text>
          </Group>
          {recentError && (
            <Alert color="red" role="alert" mb="md">
              {recentError}
            </Alert>
          )}
          {recentRuns.length === 0 && !recentError && (
            <Text c="dimmed" size="sm">
              No runs yet.
            </Text>
          )}
          <Stack gap={0}>
            {recentRuns.map((run) => (
              <Link
                key={run.id}
                to={`/runs/${run.id}`}
                style={{ textDecoration: 'none', color: 'inherit' }}
              >
                <Group
                  gap="md"
                  py={10}
                  px={4}
                  wrap="nowrap"
                  style={{ borderBottom: '1px solid var(--mantine-color-dark-4)' }}
                >
                  <StatusTag status={run.state} />
                  <Text size="13.5px" fw={500} miw={150} truncate>
                    {run.imageRef}
                  </Text>
                  <Text size="12.5px" c="dimmed" style={{ flex: 1 }}>
                    #{run.id}
                  </Text>
                  <Text size="12.5px" c="dimmed">
                    {new Date(run.queuedAt).toLocaleString()}
                  </Text>
                </Group>
              </Link>
            ))}
          </Stack>
        </Paper>
      </Stack>
    </PageHeader>
  )
}

// Note: the mockup also shows a "Run volume, last 7 days" bar chart on this
// row. There is no day-bucketed run-count endpoint to drive it (RunStats is
// a live/24h snapshot, not a time series) and this rework is presentation-
// only - adding one is real backend work, out of scope here. Omitted rather
// than faked with placeholder numbers.
