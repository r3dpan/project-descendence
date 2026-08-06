import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useAuth } from '../auth'
import { createUser, listUsers, type User } from '../api/users'
import { listRoles, type Role } from '../api/roles'
import { APIError } from '../api/client'

export default function UserList() {
  const { principal } = useAuth()
  const canManage = principal?.permissions.includes('users:write') ?? false

  const [users, setUsers] = useState<User[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const [name, setName] = useState('')
  const [role, setRole] = useState('')
  const [createError, setCreateError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  // Shown exactly once after a create - never re-fetchable, same contract
  // as cmd/seed's and the CLI's "shown once" password display.
  const [generatedPassword, setGeneratedPassword] = useState<string | null>(null)

  useEffect(() => {
    setLoading(true)
    Promise.all([listUsers(), canManage ? listRoles() : Promise.resolve({ items: [] })])
      .then(([userPage, rolePage]) => {
        setUsers(userPage.items)
        setRoles(rolePage.items)
        if (rolePage.items.length > 0) setRole((r) => r || rolePage.items[0].name)
      })
      .catch((err) => setError(err instanceof APIError ? err.message : 'Failed loading users'))
      .finally(() => setLoading(false))
  }, [canManage])

  async function handleCreate(e: FormEvent) {
    e.preventDefault()
    setCreateError(null)
    setGeneratedPassword(null)
    setCreating(true)
    try {
      const created = await createUser({ name, role })
      setUsers((prev) => [...prev, created].sort((a, b) => a.name.localeCompare(b.name)))
      setGeneratedPassword(created.password)
      setName('')
    } catch (err) {
      setCreateError(err instanceof APIError ? err.message : 'Failed creating user')
    } finally {
      setCreating(false)
    }
  }

  return (
    <main>
      <h1>Users</h1>
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
            <th>Created</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id}>
              <td>
                <Link to={`/users/${u.id}`}>{u.name}</Link>
              </td>
              <td>{u.role}</td>
              <td>{u.createdAt}</td>
              <td>{u.revokedAt ? `revoked ${u.revokedAt}` : 'active'}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {loading && <p>Loading…</p>}
      {!loading && users.length === 0 && <p>No users yet.</p>}

      {canManage && (
        <>
          <h2>New user</h2>
          <form onSubmit={handleCreate}>
            <div>
              <label htmlFor="user-name">Name</label>
              <br />
              <input id="user-name" value={name} onChange={(e) => setName(e.target.value)} required />
            </div>
            <div style={{ marginTop: '0.5rem' }}>
              <label htmlFor="user-role">Role</label>
              <br />
              <select id="user-role" value={role} onChange={(e) => setRole(e.target.value)}>
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
              {creating ? 'Creating…' : 'Create user'}
            </button>
          </form>
          {generatedPassword && (
            <p style={{ background: '#333', padding: '0.75rem', marginTop: '0.5rem' }}>
              Password (shown once - copy it now): <code>{generatedPassword}</code>
            </p>
          )}
        </>
      )}
    </main>
  )
}
