import { useState, type FormEvent } from 'react'
import { useAuth } from '../auth'
import { changeOwnPassword } from '../api/users'
import { APIError } from '../api/client'

// Self-service password change - the one page every authenticated user can
// reach, unlike /users and /tokens which are gated on users:write. Gated by
// "acting on self" server-side (decision #5's carve-out), not a permission
// key, so there is nothing to check client-side beyond being logged in.
export default function Settings() {
  const { principal } = useAuth()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  const isTokenPrincipal = principal?.kind === 'token'

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setSuccess(false)
    if (newPassword !== confirmPassword) {
      setError('New password and confirmation do not match')
      return
    }
    setSubmitting(true)
    try {
      await changeOwnPassword(currentPassword, newPassword)
      setSuccess(true)
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
    } catch (err) {
      setError(err instanceof APIError ? err.message : 'Failed changing password')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main>
      <h1>Settings</h1>
      <h2>Change password</h2>
      {isTokenPrincipal ? (
        <p>Token principals have no password to change.</p>
      ) : (
        <form onSubmit={handleSubmit} style={{ maxWidth: 320 }}>
          <div>
            <label htmlFor="current-password">Current password</label>
            <br />
            <input
              id="current-password"
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              autoComplete="current-password"
              required
            />
          </div>
          <div style={{ marginTop: '0.5rem' }}>
            <label htmlFor="new-password">New password</label>
            <br />
            <input
              id="new-password"
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              autoComplete="new-password"
              required
            />
          </div>
          <div style={{ marginTop: '0.5rem' }}>
            <label htmlFor="confirm-password">Confirm new password</label>
            <br />
            <input
              id="confirm-password"
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              autoComplete="new-password"
              required
            />
          </div>
          {error && (
            <p role="alert" style={{ color: 'crimson' }}>
              {error}
            </p>
          )}
          {success && <p style={{ color: 'seagreen' }}>Password changed.</p>}
          <button type="submit" disabled={submitting} style={{ marginTop: '1rem' }}>
            {submitting ? 'Saving…' : 'Change password'}
          </button>
        </form>
      )}
    </main>
  )
}
