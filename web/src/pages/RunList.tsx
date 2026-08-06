import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Alert, Badge, Button, LoadingOverlay, Table, Title } from '@mantine/core'
import { listRuns, type Run } from '../api/runs'
import { APIError } from '../api/client'
import { statusColor } from '../statusColor'

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
    <>
      <Title order={2} mb="md">
        Runs
      </Title>
      {error && (
        <Alert color="red" role="alert" mb="md">
          {error}
        </Alert>
      )}
      <Table.ScrollContainer minWidth={600} pos="relative">
        <LoadingOverlay visible={loading && runs.length === 0} />
        <Table highlightOnHover>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>ID</Table.Th>
              <Table.Th>State</Table.Th>
              <Table.Th>Image</Table.Th>
              <Table.Th>Queued</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {runs.map((run) => (
              <Table.Tr key={run.id}>
                <Table.Td>
                  <Link to={`/runs/${run.id}`}>{run.id}</Link>
                </Table.Td>
                <Table.Td>
                  <Badge color={statusColor(run.state)}>{run.state}</Badge>
                </Table.Td>
                <Table.Td>{run.imageRef}</Table.Td>
                <Table.Td>{new Date(run.queuedAt).toLocaleString()}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {!loading && nextCursor && (
        <Button variant="light" mt="md" onClick={() => setCursor(nextCursor)}>
          Load more
        </Button>
      )}
    </>
  )
}
