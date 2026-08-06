import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Alert, Badge, Button, LoadingOverlay, SegmentedControl, Stack, Table, Text } from '@mantine/core'
import { listJobs, patchJob, type Job } from '../api/jobs'
import { APIError } from '../api/client'
import PageHeader from '../components/PageHeader'
import StatusTag from '../components/StatusTag'

type Filter = 'all' | 'enabled' | 'disabled'

export default function JobList() {
  const [jobs, setJobs] = useState<Job[]>([])
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [nextCursor, setNextCursor] = useState<string | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [togglingId, setTogglingId] = useState<number | null>(null)
  const [filter, setFilter] = useState<Filter>('all')

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

  const visibleJobs = jobs.filter((j) => filter === 'all' || (filter === 'enabled' ? j.enabled : !j.enabled))

  return (
    <PageHeader
      title="Jobs"
      subtitle={`${jobs.length} configured jobs`}
      action={
        <Button component={Link} to="/jobs/new">
          New job
        </Button>
      }
    >
      <Stack gap="md" maw={1080}>
        <SegmentedControl
          w="fit-content"
          value={filter}
          onChange={(v) => setFilter(v as Filter)}
          data={[
            { label: 'All', value: 'all' },
            { label: 'Enabled', value: 'enabled' },
            { label: 'Disabled', value: 'disabled' },
          ]}
        />
        {error && (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        )}
        <Table.ScrollContainer minWidth={700} pos="relative">
          <LoadingOverlay visible={loading && jobs.length === 0} />
          <Table verticalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Name</Table.Th>
                <Table.Th>Description</Table.Th>
                <Table.Th>Params</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {visibleJobs.map((job) => (
                <Table.Tr key={job.id}>
                  <Table.Td>
                    <Text component={Link} to={`/jobs/${job.id}`} c="accent.4" fw={500} style={{ textDecoration: 'none' }}>
                      {job.name}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Text c="dimmed" maw={280}>
                      {job.description ?? ''}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Badge variant="light" color="gray" tt="none">
                      {job.params.length === 0 ? 'No params' : `${job.params.length} param${job.params.length === 1 ? '' : 's'}`}
                    </Badge>
                  </Table.Td>
                  <Table.Td>
                    <StatusTag status={job.enabled ? 'enabled' : 'disabled'} label={job.enabled ? 'Enabled' : 'Disabled'} />
                  </Table.Td>
                  <Table.Td style={{ textAlign: 'right' }}>
                    <Button
                      size="xs"
                      variant="default"
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
          <Button variant="default" w="fit-content" onClick={() => setCursor(nextCursor)}>
            Load more
          </Button>
        )}
      </Stack>
    </PageHeader>
  )
}
