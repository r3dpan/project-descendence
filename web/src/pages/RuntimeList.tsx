import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { createRuntime, listRuntimes, type Runtime } from '../api/runtimes'
import { APIError } from '../api/client'

export default function RuntimeList() {
  const [runtimes, setRuntimes] = useState<Runtime[]>([])
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [nextCursor, setNextCursor] = useState<string | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const [name, setName] = useState('')
  const [lang, setLang] = useState<'python' | 'powershell' | 'node'>('python')
  const [baseImage, setBaseImage] = useState('')
  const [sysPackages, setSysPackages] = useState('')
  const [langManifest, setLangManifest] = useState('')
  const [createError, setCreateError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    setLoading(true)
    listRuntimes({ cursor })
      .then((page) => {
        setRuntimes((prev) => (cursor ? [...prev, ...page.items] : page.items))
        setNextCursor(page.nextCursor)
      })
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading runtimes'))
      .finally(() => setLoading(false))
  }, [cursor])

  async function handleCreate(e: FormEvent) {
    e.preventDefault()
    setCreateError(null)
    setCreating(true)
    try {
      const runtime = await createRuntime({
        name,
        lang,
        baseImage: baseImage || undefined,
        sysPackages: sysPackages
          ? sysPackages
              .split(',')
              .map((s) => s.trim())
              .filter(Boolean)
          : undefined,
        langManifest: langManifest || undefined,
      })
      setRuntimes((prev) => [runtime, ...prev])
      setName('')
      setBaseImage('')
      setSysPackages('')
      setLangManifest('')
    } catch (err) {
      setCreateError(err instanceof APIError ? err.message : 'Failed creating runtime')
    } finally {
      setCreating(false)
    }
  }

  return (
    <main>
      <h1>Runtimes</h1>
      {error && (
        <p role="alert" style={{ color: 'crimson' }}>
          {error}
        </p>
      )}
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Lang</th>
            <th>Build status</th>
            <th>Image digest</th>
          </tr>
        </thead>
        <tbody>
          {runtimes.map((runtime) => (
            <tr key={runtime.id}>
              <td>
                <Link to={`/runtimes/${runtime.id}`}>{runtime.name}</Link>
              </td>
              <td>{runtime.lang}</td>
              <td>{runtime.buildStatus}</td>
              <td>
                <code>{runtime.imageDigest ? runtime.imageDigest.slice(0, 24) + '…' : ''}</code>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {loading && <p>Loading…</p>}
      {!loading && runtimes.length === 0 && <p>No runtimes yet.</p>}
      {!loading && nextCursor && (
        <button type="button" onClick={() => setCursor(nextCursor)}>
          Load more
        </button>
      )}

      <h2>New runtime</h2>
      <form onSubmit={handleCreate}>
        <div>
          <label htmlFor="rt-name">Name</label>
          <br />
          <input id="rt-name" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div style={{ marginTop: '0.5rem' }}>
          <label htmlFor="rt-lang">Language</label>
          <br />
          <select id="rt-lang" value={lang} onChange={(e) => setLang(e.target.value as typeof lang)}>
            <option value="python">python</option>
            <option value="powershell">powershell</option>
            <option value="node">node</option>
          </select>
        </div>
        <div style={{ marginTop: '0.5rem' }}>
          <label htmlFor="rt-base">Base image (optional - defaults to a curated image)</label>
          <br />
          <input id="rt-base" value={baseImage} onChange={(e) => setBaseImage(e.target.value)} />
        </div>
        <div style={{ marginTop: '0.5rem' }}>
          <label htmlFor="rt-sys">System packages (comma-separated, optional)</label>
          <br />
          <input id="rt-sys" value={sysPackages} onChange={(e) => setSysPackages(e.target.value)} />
        </div>
        <div style={{ marginTop: '0.5rem' }}>
          <label htmlFor="rt-manifest">
            Language manifest (optional - requirements.txt / PSResourceGet file / package.json, verbatim)
          </label>
          <br />
          <textarea
            id="rt-manifest"
            value={langManifest}
            onChange={(e) => setLangManifest(e.target.value)}
            rows={4}
            style={{ width: '100%', maxWidth: 480 }}
          />
        </div>
        {createError && (
          <p role="alert" style={{ color: 'crimson' }}>
            {createError}
          </p>
        )}
        <button type="submit" disabled={creating} style={{ marginTop: '1rem' }}>
          {creating ? 'Creating…' : 'Create runtime'}
        </button>
      </form>
    </main>
  )
}
