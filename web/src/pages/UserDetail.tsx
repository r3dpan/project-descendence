import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useAuth } from '../auth'
import { getUser, revokeUser, updateUserRole, type User } from '../api/users'
import { listRoles, type Role } from '../api/roles'
import { APIError } from '../api/client'

export default function UserDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { principal } = useAuth()
  const canManage = principal?.permissions.includes('users:write') ?? false

  const [user, setUser] = useState<User | null>(null)
  const [roles, setRoles] = useState<Role[]>([])
  const [loadError, setLoadError] = useState<string | null>(null)

  const [selectedRole, setSelectedRole] = useState('')
  const [roleUpdating, setRoleUpdating] = useState(false)
  const [roleError, setRoleError] = useState<string | null>(null)

  const [revoking, setRevoking] = useState(false)
  const [revokeError, setRevokeError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    Promise.all([getUser(id), canManage ? listRoles() : Promise.resolve({ items: [] })])
      .then(([u, rolePage]) => {
        setUser(u)
        setSelectedRole(u.role)
        setRoles(rolePage.items)
      })
      .catch((err) => setLoadError(err instanceof APIError ? err.message : 'Failed loading user'))
  }, [id, canManage])

  async function handleRoleChange() {
    if (!user) return
    setRoleError(null)
    setRoleUpdating(true)
    try {
      const updated = await updateUserRole(user.id, selectedRole)
      setUser(updated)
    } catch (err) {
      setRoleError(err instanceof APIError ? err.message : 'Failed updating role')
    } finally {
      setRoleUpdating(false)
    }
  }

  // window.confirm rather than a modal library, matching this codebase's
  // minimal-dependency posture elsewhere in the SPA.
  async function handleRevoke() {
    if (!user) return
    if (!window.confirm(`Revoke ${user.name}'s access? This cannot be undone.`)) return
    setRevokeError(null)
    setRevoking(true)
    try {
      await revokeUser(user.id)
      navigate('/users')
    } catch (err) {
      setRevokeError(err instanceof APIError ? err.message : 'Failed revoking user')
      setRevoking(false)
    }
  }

  if (loadError) return <p role="alert" style={{ color: 'crimson' }}>{loadError}</p>
  if (!user) return <p>Loading…</p>

  return (
    <main>
      <h1>{user.name}</h1>
      <p>
        {user.revokedAt ? (
          <span role="alert" style={{ color: 'crimson' }}>
            Revoked {user.revokedAt}
          </span>
        ) : (
          'Active'
        )}
      </p>
      <p>Created {user.createdAt}</p>

      {canManage && !user.revokedAt ? (
        <div style={{ marginTop: '1rem' }}>
          <label htmlFor="role-select">Role</label>
          <br />
          <select id="role-select" value={selectedRole} onChange={(e) => setSelectedRole(e.target.value)}>
            {roles.map((r) => (
              <option key={r.name} value={r.name}>
                {r.name}
              </option>
            ))}
          </select>{' '}
          <button type="button" onClick={handleRoleChange} disabled={roleUpdating || selectedRole === user.role}>
            {roleUpdating ? 'Saving…' : 'Save role'}
          </button>
          {roleError && (
            <p role="alert" style={{ color: 'crimson' }}>
              {roleError}
            </p>
          )}
        </div>
      ) : (
        <p>Role: {user.role}</p>
      )}

      {canManage && !user.revokedAt && (
        <div style={{ marginTop: '1.5rem' }}>
          <button type="button" onClick={handleRevoke} disabled={revoking} style={{ color: 'crimson' }}>
            {revoking ? 'Revoking…' : 'Revoke access'}
          </button>
          {revokeError && (
            <p role="alert" style={{ color: 'crimson' }}>
              {revokeError}
            </p>
          )}
        </div>
      )}
    </main>
  )
}
