import { useEffect, useState, type FormEvent } from 'react'
import { useParams } from 'react-router-dom'
import { Alert, Button, Loader, Stack, Text, TextInput, Textarea } from '@mantine/core'
import { getJob, type Job } from '../api/jobs'
import { createRepoFile, getRepoFile } from '../api/repos'
import { APIError } from '../api/client'
import { useAuth } from '../auth'
import PageHeader from '../components/PageHeader'

// A job is a manifest plus a sidecar script, addressed by job.scriptPath
// (repo-root-relative, resolved from the manifest's own directory -
// ARCHITECTURE.md's repo-layout section). ManifestEditor edits the YAML;
// this edits the script body it points at - same generic repo-file
// read/write endpoints (getRepoFile/createRepoFile), no manifest parsing.
export default function ScriptEditor() {
  const { id } = useParams<{ id: string }>()
  const { principal } = useAuth()
  const canWrite = principal?.permissions.includes('repos:write') ?? false

  const [job, setJob] = useState<Job | null>(null)
  const [content, setContent] = useState('')
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (!id) return
    let cancelled = false

    async function load() {
      try {
        const j = await getJob(id!)
        const file = await getRepoFile(j.repoId, j.scriptPath)
        if (!cancelled) {
          setJob(j)
          setContent(file.content)
        }
      } catch (err) {
        if (!cancelled) setLoadError(err instanceof APIError ? err.message : 'Failed loading script')
      } finally {
        if (!cancelled) setLoading(false)
      }
    }

    load()
    return () => {
      cancelled = true
    }
  }, [id])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!job) return
    setSubmitError(null)
    setSaved(false)
    setSubmitting(true)
    try {
      await createRepoFile(job.repoId, {
        path: job.scriptPath,
        content,
        message: message || undefined,
      })
      setSaved(true)
    } catch (err) {
      setSubmitError(err instanceof APIError ? err.message : 'Failed committing the script')
    } finally {
      setSubmitting(false)
    }
  }

  if (loading)
    return (
      <PageHeader title="Script" backTo={id ? `/jobs/${id}` : '/jobs'} backLabel="Job">
        <Loader />
      </PageHeader>
    )
  if (loadError || !job)
    return (
      <PageHeader title="Script" backTo={id ? `/jobs/${id}` : '/jobs'} backLabel="Job">
        <Alert color="red" role="alert">
          {loadError}
        </Alert>
      </PageHeader>
    )

  return (
    <PageHeader title={job.name} subtitle={job.scriptPath} backTo={`/jobs/${job.id}`} backLabel="Job">
      <Stack gap="sm" maw={720} component="form" onSubmit={handleSubmit}>
        {!canWrite && (
          <Alert color="yellow" role="alert">
            You have read-only access to repository files.
          </Alert>
        )}
        {saved && (
          <Alert color="green" role="alert">
            Script committed.
          </Alert>
        )}
        <TextInput
          label="Commit message"
          description="Optional"
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder={`Update ${job.scriptPath}`}
          disabled={!canWrite}
        />
        <Textarea
          label="Script"
          value={content}
          onChange={(e) => setContent(e.target.value)}
          rows={24}
          spellCheck={false}
          disabled={!canWrite}
          styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
        />
        {submitError && (
          <Alert color="red" role="alert">
            {submitError}
          </Alert>
        )}
        {canWrite && (
          <Button type="submit" w="fit-content" loading={submitting}>
            Commit
          </Button>
        )}
        {!canWrite && (
          <Text size="xs" c="dimmed">
            Ask an operator or admin for repos:write access to edit this file.
          </Text>
        )}
      </Stack>
    </PageHeader>
  )
}
