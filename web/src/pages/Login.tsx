import { useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Alert, Button, Container, Paper, PasswordInput, Stack, Text, TextInput } from '@mantine/core'
import { useAuth } from '../auth'
import { APIError } from '../api/client'

export default function Login() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const from = (location.state as { from?: string } | null)?.from ?? '/'

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await login(username, password)
      navigate(from, { replace: true })
    } catch (err) {
      setError(err instanceof APIError ? err.message : 'Login failed')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Container size="xs" mt="10vh">
      <Paper withBorder p="xl" radius="md">
        <Text ta="center" fw={600} size="xl" mb="md">
          Descendence
        </Text>
        <form onSubmit={handleSubmit}>
          <Stack>
            <TextInput
              label="Username"
              id="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoFocus
              autoComplete="username"
            />
            <PasswordInput
              label="Password"
              id="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
            />
            {error && (
              <Alert color="red" role="alert">
                {error}
              </Alert>
            )}
            <Button type="submit" loading={submitting} fullWidth>
              {submitting ? 'Signing in…' : 'Sign in'}
            </Button>
          </Stack>
        </form>
      </Paper>
    </Container>
  )
}
