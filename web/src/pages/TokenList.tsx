import { useEffect, useState, type FormEvent } from 'react'
import { useAuth } from '../auth'
import { createToken, listTokens, revokeToken, type Token } from '../api/tokens'
import { listRoles, type Role } from '../api/roles'
import { APIError } from '../api/client'

export default function TokenList() {
  const { principal } = useAuth()
  const canManage = principal?.permissions.includes('users:write') ?? false

  const [tokens, setTokens] = useState<Token[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [revokingId, setRevokingId] = useState<number | null>(null)

  const [name, setName] = useState('')
  const [role, setRole] = useState('')
  const [createError, setCreateError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  // Shown exactly once - never re-fetchable, same "shown once" contract as
  // cmd/seed and the CLI's plaintext token output.
  const [generatedToken, setGeneratedToken] = useState<string | null>(null)

  useEffect(() => {
    setLoading(true)
    Promise.all([listTokens(), canManage ? listRoles() : Promise.resolve({ items: [] })])
      .then(([tokenPage, rolePage]) => {
        setTokens(tokenPage.items)
        setRoles(rolePage.items)
        if (rolePage.items.length > 0) setRole((r) => r || rolePage.items[0].name)
      })
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading tokens'))
      .finally(() => setLoading(false))
  }, [canManage])

  async function handleCreate(e: FormEvent) {
    e.preventDefault()
    setCreateError(null)
    setGeneratedToken(null)
    setCreating(true)
    try {
      const created = await createToken({ name, role })
      setTokens((prev) => [...prev, created])
      setGeneratedToken(created.token)
      setName('')
    } catch (err) {
      setCreateError(err instanceof APIError ? err.message : 'Failed creating token')
    } finally {
      setCreating(false)
    }
  }

  async function handleRevoke(token: Token) {
    if (!window.confirm(`Revoke token "${token.name}"? This cannot be undone.`)) return
    setError(null)
    setRevokingId(token.id)
    try {
      await revokeToken(token.id)
      setTokens((prev) => prev.map((t) => (t.id === token.id ? { ...t, revokedAt: new Date().toISOString() } : t)))
    } catch (err) {
      setError(err instanceof APIError ? err.message : 'Failed revoking token')
    } finally {
      setRevokingId(null)
    }
  }

  return (
    <main>
      <h1>Tokens</h1>
      {error && (
        <p role="alert" style={{ color: 'crimson' }}>
          {error}
        </p>
      )}
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Role</th>
            <th>Hint</th>
            <th>Created</th>
            <th>Expires</th>
            <th>Status</th>
            {canManage && <th></th>}
          </tr>
        </thead>
        <tbody>
          {tokens.map((t) => (
            <tr key={t.id}>
              <td>{t.name}</td>
              <td>{t.role}</td>
              <td>
                <code>{t.tokenHint}</code>
              </td>
              <td>{t.createdAt}</td>
              <td>{t.expiresAt ?? '-'}</td>
              <td>{t.revokedAt ? `revoked ${t.revokedAt}` : 'active'}</td>
              {canManage && (
                <td>
                  {!t.revokedAt && (
                    <button type="button" onClick={() => handleRevoke(t)} disabled={revokingId === t.id}>
                      {revokingId === t.id ? 'Revoking…' : 'Revoke'}
                    </button>
                  )}
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
      {loading && <p>Loading…</p>}
      {!loading && tokens.length === 0 && <p>No tokens yet.</p>}

      {canManage && (
        <>
          <h2>New token</h2>
          <form onSubmit={handleCreate}>
            <div>
              <label htmlFor="token-name">Name</label>
              <br />
              <input id="token-name" value={name} onChange={(e) => setName(e.target.value)} required />
            </div>
            <div style={{ marginTop: '0.5rem' }}>
              <label htmlFor="token-role">Role</label>
              <br />
              <select id="token-role" value={role} onChange={(e) => setRole(e.target.value)}>
                {roles.map((r) => (
                  <option key={r.name} value={r.name}>
                    {r.name}
                  </option>
                ))}
              </select>
            </div>
            {createError && (
              <p role="alert" style={{ color: 'crimson' }}>
                {createError}
              </p>
            )}
            <button type="submit" disabled={creating} style={{ marginTop: '1rem' }}>
              {creating ? 'Creating…' : 'Create token'}
            </button>
          </form>
          {generatedToken && (
            <p style={{ background: '#333', padding: '0.75rem', marginTop: '0.5rem' }}>
              Token (shown once - copy it now): <code>{generatedToken}</code>
            </p>
          )}
        </>
      )}
    </main>
  )
}
