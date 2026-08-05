import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { getJob } from '../api/jobs'
import { createRepoFile, getRepoFile, listRepos } from '../api/repos'
import { APIError } from '../api/client'

const PLACEHOLDER = `apiVersion: descendence/v1
name: my-job
description: What this job does
script: my-job.sh
image: docker.io/library/alpine:3.20
`

export interface ManifestEditorProps {
  mode: 'create' | 'edit'
}

// ManifestEditor is task 7.8's YAML editor: create a brand new manifest, or
// edit an existing job's, both committed through the same write path
// (createRepoFile) the CLI's `repos put` already uses - there is no other
// way to change a job's definition (decision #23). No preview pane yet
// (that is the next task); this gets create/edit/commit working end to end
// first.
export default function ManifestEditor({ mode }: ManifestEditorProps) {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()

  const [repoId, setRepoId] = useState<number | null>(null)
  const [path, setPath] = useState('')
  const [pathLocked, setPathLocked] = useState(mode === 'edit')
  const [message, setMessage] = useState('')
  const [content, setContent] = useState(mode === 'create' ? PLACEHOLDER : '')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false

    async function load() {
      try {
        if (mode === 'create') {
          // Homelab-scale, single-repo reality (see PLAN.md/7.8's plan): no
          // repo picker UI, just the one repository everything lives in.
          const repos = await listRepos({ limit: 2 })
          if (repos.items.length !== 1) {
            throw new Error(
              repos.items.length === 0
                ? 'No repository exists yet - create one first.'
                : 'More than one repository exists; this editor only supports the single-repository case.',
            )
          }
          if (!cancelled) setRepoId(repos.items[0].id)
        } else if (id) {
          const job = await getJob(id)
          const file = await getRepoFile(job.repoId, job.manifestPath)
          if (!cancelled) {
            setRepoId(job.repoId)
            setPath(job.manifestPath)
            setPathLocked(true)
            setContent(file.content)
          }
        }
      } catch (err) {
        if (!cancelled) setLoadError(err instanceof APIError ? err.message : (err as Error).message)
      } finally {
        if (!cancelled) setLoading(false)
      }
    }

    load()
    return () => {
      cancelled = true
    }
  }, [mode, id])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (repoId === null) return
    setSubmitError(null)
    setSubmitting(true)
    try {
      const result = await createRepoFile(repoId, {
        path,
        content,
        message: message || undefined,
      })
      if (result.sync?.errors?.length) {
        setSubmitError(
          `Committed as ${result.commitSha.slice(0, 12)}, but the sync that followed reported: ` +
            result.sync.errors.map((e) => `${e.path}: ${e.message}`).join('; '),
        )
        return
      }
      navigate('/jobs')
    } catch (err) {
      setSubmitError(err instanceof APIError ? err.message : 'Failed committing the manifest')
    } finally {
      setSubmitting(false)
    }
  }

  if (loading) return <p>Loading…</p>
  if (loadError) return <p role="alert" style={{ color: 'crimson' }}>{loadError}</p>

  return (
    <main>
      <h1>{mode === 'create' ? 'New job' : 'Edit manifest'}</h1>

      <form onSubmit={handleSubmit}>
        <div>
          <label htmlFor="me-path">Manifest path</label>
          <br />
          <input
            id="me-path"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            disabled={pathLocked}
            placeholder="jobs/my-job.job.yaml"
            required
            style={{ width: '100%', maxWidth: 480 }}
          />
        </div>
        <div style={{ marginTop: '0.5rem' }}>
          <label htmlFor="me-message">Commit message (optional)</label>
          <br />
          <input
            id="me-message"
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            placeholder={`Update ${path || '<path>'}`}
            style={{ width: '100%', maxWidth: 480 }}
          />
        </div>
        <div style={{ marginTop: '0.5rem' }}>
          <label htmlFor="me-content">Manifest YAML</label>
          <br />
          <textarea
            id="me-content"
            value={content}
            onChange={(e) => setContent(e.target.value)}
            rows={20}
            spellCheck={false}
            style={{ width: '100%', maxWidth: 720, fontFamily: 'monospace' }}
          />
        </div>

        {submitError && (
          <p role="alert" style={{ color: 'crimson' }}>
            {submitError}
          </p>
        )}

        <button type="submit" disabled={submitting} style={{ marginTop: '1rem' }}>
          {submitting ? 'Committing…' : 'Commit'}
        </button>
      </form>
    </main>
  )
}
