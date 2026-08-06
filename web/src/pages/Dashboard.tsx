import { useEffect, useState, type ReactNode } from 'react'
import { Alert, Group, Paper, SimpleGrid, Text, Title } from '@mantine/core'
import { useAuth } from '../auth'
import { getRunStats, type RunStats } from '../api/runs'
import { getSystemStatus, type SystemStatus } from '../api/system'
import { APIError } from '../api/client'
import { statusColor } from '../statusColor'

// 24h, matching the server's own default window (RunStatsHandler) - kept in
// sync deliberately rather than omitting `since` and trusting the server's
// default, so the window shown in the "since" label is never a guess.
const STATS_WINDOW = '24h'

interface Tile {
  label: string
  value: ReactNode
  color: string
  caption?: string
}

function TileGrid({ tiles }: { tiles: Tile[] }) {
  return (
    <SimpleGrid cols={{ base: 2, sm: 3, lg: 5 }} mb="lg">
      {tiles.map((tile) => (
        <Paper key={tile.label} withBorder p="md">
          <Group gap="xs" mb={4}>
            <span
              style={{
                width: 8,
                height: 8,
                borderRadius: '50%',
                background: `var(--mantine-color-${tile.color}-6)`,
                display: 'inline-block',
              }}
            />
            <Text size="sm" c="dimmed">
              {tile.label}
            </Text>
          </Group>
          <Text size="28px" fw={600} lh={1.1}>
            {tile.value}
          </Text>
          {tile.caption && (
            <Text size="xs" c="dimmed" mt={2}>
              {tile.caption}
            </Text>
          )}
        </Paper>
      ))}
    </SimpleGrid>
  )
}

export default function Dashboard() {
  const { principal } = useAuth()
  const [stats, setStats] = useState<RunStats | null>(null)
  const [statsError, setStatsError] = useState<string | null>(null)
  const [status, setStatus] = useState<SystemStatus | null>(null)
  const [statusError, setStatusError] = useState<string | null>(null)

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
    <>
      <Title order={2} mb="md">
        Dashboard
      </Title>
      {statsError && (
        <Alert color="red" role="alert" mb="md">
          {statsError}
        </Alert>
      )}
      <TileGrid tiles={statsTiles} />

      <Title order={3} mb="md">
        System status
      </Title>
      {statusError && (
        <Alert color="red" role="alert" mb="md">
          {statusError}
        </Alert>
      )}
      <TileGrid tiles={statusTiles} />

      <Paper withBorder p="md" maw={360}>
        <Text size="sm" c="dimmed">
          Last login
        </Text>
        <Text>
          {principal?.lastLoginAt ? new Date(principal.lastLoginAt).toLocaleString() : 'This is your first login'}
        </Text>
      </Paper>
    </>
  )
}
