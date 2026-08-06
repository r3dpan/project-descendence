import { useEffect, useState } from 'react'
import { Alert, Group, Paper, SimpleGrid, Text, Title } from '@mantine/core'
import { useAuth } from '../auth'
import { getRunStats, type RunStats } from '../api/runs'
import { APIError } from '../api/client'
import { statusColor } from '../statusColor'

// 24h, matching the server's own default window (RunStatsHandler) - kept in
// sync deliberately rather than omitting `since` and trusting the server's
// default, so the window shown in the "since" label is never a guess.
const STATS_WINDOW = '24h'

interface Tile {
  label: string
  value: number
  color: string
  caption: string
}

export default function Dashboard() {
  const { principal } = useAuth()
  const [stats, setStats] = useState<RunStats | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    getRunStats(STATS_WINDOW)
      .then(setStats)
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading run stats'))
  }, [])

  const tiles: Tile[] = stats
    ? [
        { label: 'Queued', value: stats.queued, color: statusColor('queued'), caption: 'right now' },
        { label: 'Succeeded', value: stats.succeeded, color: statusColor('succeeded'), caption: `since ${STATS_WINDOW}` },
        { label: 'Failed', value: stats.failed, color: statusColor('failed'), caption: `since ${STATS_WINDOW}` },
        { label: 'Cancelled', value: stats.cancelled, color: statusColor('cancelled'), caption: `since ${STATS_WINDOW}` },
        { label: 'Lost', value: stats.lost, color: statusColor('lost'), caption: `since ${STATS_WINDOW}` },
      ]
    : []

  return (
    <>
      <Title order={2} mb="md">
        Dashboard
      </Title>
      {error && (
        <Alert color="red" role="alert" mb="md">
          {error}
        </Alert>
      )}
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
            <Text size="xs" c="dimmed" mt={2}>
              {tile.caption}
            </Text>
          </Paper>
        ))}
      </SimpleGrid>
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
