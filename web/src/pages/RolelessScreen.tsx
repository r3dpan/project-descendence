import { Paper, Stack, Text } from '@mantine/core'
import { useAuth } from '../auth'

// Rendered by App.tsx's Protected wrapper for any authenticated principal
// with zero permissions - in practice, a JIT-provisioned OIDC principal
// (task 9.6) that has just logged in for the first time and has not yet
// been assigned a role. There is nothing this principal can do but wait and
// sign out; an admin assigns a role from Settings > Users (UserDetail). Same
// centered-card chrome as Login.tsx, so it reads as part of the same shell
// rather than a bare error page.
export default function RolelessScreen() {
  const { principal } = useAuth()

  return (
    <div style={{ display: 'grid', placeItems: 'center', height: '100%' }}>
      <Paper w={440} p="xl" radius="lg" bg="dark.6" shadow="lg">
        <Stack gap="sm">
          <Text fw={500} size="lg">
            No role assigned yet
          </Text>
          <Text c="dimmed" size="sm">
            Signed in as <Text component="strong" c="var(--mantine-color-white)">{principal?.name}</Text>, but this
            account has no role yet, so there is nothing here to see. An admin needs to assign one
            from Settings &gt; Users before you can do anything.
          </Text>
        </Stack>
      </Paper>
    </div>
  )
}
