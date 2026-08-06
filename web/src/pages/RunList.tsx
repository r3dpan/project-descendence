import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Alert, Button, LoadingOverlay, SegmentedControl, Stack, Table, Text } from '@mantine/core'
import { listRuns, type Run } from '../api/runs'
import { APIError } from '../api/client'
import PageHeader from '../components/PageHeader'
import StatusTag from '../components/StatusTag'

type Filter = 'all' | 'running' | 'failed' | 'lost'

export default function RunList() {
  const [runs, setRuns] = useState<Run[]>([])
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [nextCursor, setNextCursor] = useState<string | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState<Filter>('all')

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

  const visibleRuns = runs.filter((r) => filter === 'all' || r.state === filter)

  return (
    <PageHeader title="Runs" subtitle="History across all jobs">
      <Stack gap="md" maw={1080}>
        <SegmentedControl
          w="fit-content"
          value={filter}
          onChange={(v) => setFilter(v as Filter)}
          data={[
            { label: 'All', value: 'all' },
            { label: 'Running', value: 'running' },
            { label: 'Failed', value: 'failed' },
            { label: 'Lost', value: 'lost' },
          ]}
        />
        {error && (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        )}
        <Table.ScrollContainer minWidth={700} pos="relative">
          <LoadingOverlay visible={loading && runs.length === 0} />
          <Table verticalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Run</Table.Th>
                <Table.Th>Image</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th>Queued</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {visibleRuns.map((run) => (
                <Table.Tr key={run.id} style={{ cursor: 'pointer' }}>
                  <Table.Td>
                    <Text component={Link} to={`/runs/${run.id}`} c="accent.4" fw={500} style={{ textDecoration: 'none' }}>
                      #{run.id}
                    </Text>
                  </Table.Td>
                  <Table.Td>{run.imageRef}</Table.Td>
                  <Table.Td>
                    <StatusTag status={run.state} />
                  </Table.Td>
                  <Table.Td>
                    <Text c="dimmed">{new Date(run.queuedAt).toLocaleString()}</Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {!loading && nextCursor && (
          <Button variant="default" w="fit-content" onClick={() => setCursor(nextCursor)}>
            Load more
          </Button>
        )}
      </Stack>
    </PageHeader>
  )
}
