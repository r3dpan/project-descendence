import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Alert, Badge, Button, Group, LoadingOverlay, Table, Text, Title } from '@mantine/core'
import { listJobs, patchJob, type Job } from '../api/jobs'
import { APIError } from '../api/client'

export default function JobList() {
  const [jobs, setJobs] = useState<Job[]>([])
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [nextCursor, setNextCursor] = useState<string | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [togglingId, setTogglingId] = useState<number | null>(null)

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

  async function handleToggle(job: Job) {
    setError(null)
    setTogglingId(job.id)
    try {
      const updated = await patchJob(job.id, !job.enabled)
      setJobs((prev) => prev.map((j) => (j.id === updated.id ? updated : j)))
    } catch (err) {
      setError(err instanceof APIError ? err.message : 'Failed updating job')
    } finally {
      setTogglingId(null)
    }
  }

  return (
    <>
      <Group justify="space-between" mb="md">
        <Title order={2}>Jobs</Title>
        <Button component={Link} to="/jobs/new" size="xs">
          New job
        </Button>
      </Group>
      {error && (
        <Alert color="red" role="alert" mb="md">
          {error}
        </Alert>
      )}
      <Table.ScrollContainer minWidth={600} pos="relative">
        <LoadingOverlay visible={loading && jobs.length === 0} />
        <Table highlightOnHover>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Name</Table.Th>
              <Table.Th>Description</Table.Th>
              <Table.Th>Enabled</Table.Th>
              <Table.Th>Params</Table.Th>
              <Table.Th></Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {jobs.map((job) => (
              <Table.Tr key={job.id}>
                <Table.Td>
                  <Link to={`/jobs/${job.id}`}>{job.name}</Link>
                </Table.Td>
                <Table.Td>{job.description ?? ''}</Table.Td>
                <Table.Td>
                  <Badge color={job.enabled ? 'green' : 'gray'}>{job.enabled ? 'yes' : 'no'}</Badge>
                </Table.Td>
                <Table.Td>{job.params.length}</Table.Td>
                <Table.Td>
                  <Button
                    size="xs"
                    variant="light"
                    onClick={() => handleToggle(job)}
                    disabled={togglingId === job.id}
                  >
                    {job.enabled ? 'Disable' : 'Enable'}
                  </Button>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {!loading && jobs.length === 0 && <Text c="dimmed">No jobs yet.</Text>}
      {!loading && nextCursor && (
        <Button variant="light" mt="md" onClick={() => setCursor(nextCursor)}>
          Load more
        </Button>
      )}
    </>
  )
}
