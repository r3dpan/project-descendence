import { useEffect, useState, type FormEvent } from 'react'
import { Alert, Button, Paper, Stack, Text, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { getConfig, putConfig } from '../api/config'
import { APIError } from '../api/client'

// Off-plan web UI work: lets an admin edit DATABASE_URL/PODMAN_SOCKET
// (internal/appconfig's config file) without touching .env by hand. Neither
// cmd/api nor cmd/supervisor hot-reloads that file - a save just persists it
// and this page tells the admin to restart both processes.
export default function Configuration() {
  const [databaseUrl, setDatabaseUrl] = useState('')
  const [podmanSocket, setPodmanSocket] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    getConfig()
      .then((cfg) => {
        setDatabaseUrl(cfg.databaseUrl)
        setPodmanSocket(cfg.podmanSocket)
      })
      .catch((err) => setLoadError(err instanceof APIError ? err.message : 'Failed loading configuration'))
      .finally(() => setLoading(false))
  }, [])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setSaved(false)
    setSaving(true)
    try {
      const cfg = await putConfig({ databaseUrl, podmanSocket })
      setDatabaseUrl(cfg.databaseUrl)
      setPodmanSocket(cfg.podmanSocket)
      setSaved(true)
      notifications.show({ color: 'green', message: 'Configuration saved.' })
    } catch (err) {
      setError(err instanceof APIError ? err.message : 'Failed saving configuration')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Stack gap="md">
      {loadError && (
        <Alert color="red" role="alert">
          {loadError}
        </Alert>
      )}
      {saved && (
        <Alert color="yellow" role="alert">
          Saved. Restart the api and supervisor processes to apply these changes.
        </Alert>
      )}
      <Paper p="md" radius="md" bg="dark.6" maw={520} component="form" onSubmit={handleSubmit}>
        <Stack gap="sm">
          <TextInput
            label="Database URL"
            value={databaseUrl}
            onChange={(e) => setDatabaseUrl(e.target.value)}
            disabled={loading}
            required
          />
          <Text size="xs" c="dimmed">
            Shown with the password masked as "***". Leave it as-is to keep the current password, or
            overwrite the whole value to change it.
          </Text>
          <TextInput
            label="Podman socket path"
            value={podmanSocket}
            onChange={(e) => setPodmanSocket(e.target.value)}
            disabled={loading}
            required
          />
          {error && (
            <Alert color="red" role="alert">
              {error}
            </Alert>
          )}
          <Button type="submit" w="fit-content" loading={saving}>
            Save configuration
          </Button>
        </Stack>
      </Paper>
    </Stack>
  )
}
