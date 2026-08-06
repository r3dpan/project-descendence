import { useState, type FormEvent } from 'react'
import { useAuth } from '../auth'
import { changeOwnPassword } from '../api/users'
import { APIError } from '../api/client'
import { Alert, Button, PasswordInput, Stack, Text, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'

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
  const [submitting, setSubmitting] = useState(false)

  const isTokenPrincipal = principal?.kind === 'token'

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (newPassword !== confirmPassword) {
      setError('New password and confirmation do not match')
      return
    }
    setSubmitting(true)
    try {
      await changeOwnPassword(currentPassword, newPassword)
      notifications.show({ color: 'green', message: 'Password changed.' })
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
    <>
      <Title order={2} mb="md">
        Settings
      </Title>
      <Title order={3} mb="md">
        Change password
      </Title>
      {isTokenPrincipal ? (
        <Text>Token principals have no password to change.</Text>
      ) : (
        <form onSubmit={handleSubmit}>
          <Stack maw={320}>
            <PasswordInput
              label="Current password"
              id="current-password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              autoComplete="current-password"
              required
            />
            <PasswordInput
              label="New password"
              id="new-password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              autoComplete="new-password"
              required
            />
            <PasswordInput
              label="Confirm new password"
              id="confirm-password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              autoComplete="new-password"
              required
            />
            {error && (
              <Alert color="red" role="alert">
                {error}
              </Alert>
            )}
            <Button type="submit" loading={submitting}>
              {submitting ? 'Saving…' : 'Change password'}
            </Button>
          </Stack>
        </form>
      )}
    </>
  )
}
