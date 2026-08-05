import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { buildRuntime, getRuntime, TERMINAL_BUILD_STATUSES, type Runtime } from '../api/runtimes'
import { APIError } from '../api/client'

export default function RuntimeDetail() {
  const { id } = useParams<{ id: string }>()
  const [runtime, setRuntime] = useState<Runtime | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [rebuildError, setRebuildError] = useState<string | null>(null)
  const [rebuilding, setRebuilding] = useState(false)

  useEffect(() => {
    if (!id) return
    getRuntime(id)
      .then(setRuntime)
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading runtime'))
  }, [id])

  // No SSE endpoint for build status (unlike a run's logs) - poll while a
  // build is in flight and stop once it reaches a terminal status.
  useEffect(() => {
    if (!id || !runtime || TERMINAL_BUILD_STATUSES.has(runtime.buildStatus)) return

    const interval = setInterval(() => {
      getRuntime(id).then(setRuntime).catch(() => {})
    }, 2000)

    return () => clearInterval(interval)
  }, [id, runtime])

  async function handleRebuild() {
    if (!runtime) return
    setRebuildError(null)
    setRebuilding(true)
    try {
      await buildRuntime(runtime.id)
      const updated = await getRuntime(runtime.id)
      setRuntime(updated)
    } catch (err) {
      setRebuildError(err instanceof APIError ? err.message : 'Failed queuing rebuild')
    } finally {
      setRebuilding(false)
    }
  }

  if (error) return <p role="alert" style={{ color: 'crimson' }}>{error}</p>
  if (!runtime) return <p>Loading…</p>

  const rebuildDisabled = rebuilding || runtime.buildStatus === 'pending' || runtime.buildStatus === 'building'

  return (
    <main>
      <h1>{runtime.name}</h1>
      <dl>
        <dt>Language</dt>
        <dd>{runtime.lang}</dd>
        <dt>Base image</dt>
        <dd>{runtime.baseImage}</dd>
        <dt>System packages</dt>
        <dd>{runtime.sysPackages.length > 0 ? runtime.sysPackages.join(', ') : '(none)'}</dd>
        <dt>Build status</dt>
        <dd>{runtime.buildStatus}</dd>
        {runtime.buildError && (
          <>
            <dt>Build error</dt>
            <dd style={{ color: 'crimson' }}>{runtime.buildError}</dd>
          </>
        )}
        <dt>Image digest</dt>
        <dd>
          <code>{runtime.imageDigest ?? '(not built)'}</code>
        </dd>
        {runtime.imagePruned && (
          <>
            <dt>Image pruned</dt>
            <dd>Yes - a job naming this runtime cannot run until it is rebuilt.</dd>
          </>
        )}
        <dt>Language manifest</dt>
        <dd>
          <pre>{runtime.langManifest ?? '(none)'}</pre>
        </dd>
      </dl>

      <button type="button" onClick={handleRebuild} disabled={rebuildDisabled}>
        {rebuilding || runtime.buildStatus === 'building' || runtime.buildStatus === 'pending'
          ? 'Building…'
          : 'Rebuild'}
      </button>
      {rebuildError && (
        <p role="alert" style={{ color: 'crimson' }}>
          {rebuildError}
        </p>
      )}
    </main>
  )
}
