import { Container, Paper, Stack, Text } from '@mantine/core'
import { useAuth } from '../auth'

// Rendered by App.tsx's Protected wrapper for any authenticated principal
// with zero permissions - in practice, a JIT-provisioned OIDC principal
// (task 9.6) that has just logged in for the first time and has not yet
// been assigned a role. There is nothing this principal can do but wait and
// sign out; an admin assigns a role from Settings > Users (UserDetail).
export default function RolelessScreen() {
  const { principal } = useAuth()

  return (
    <Container size="sm" mt="10vh">
      <Paper withBorder p="xl" radius="md">
        <Stack>
          <Text fw={600} size="lg">
            No role assigned yet
          </Text>
          <Text c="dimmed">
            Signed in as <strong>{principal?.name}</strong>, but this account has no role yet, so
            there is nothing here to see. An admin needs to assign one from Settings &gt; Users
            before you can do anything.
          </Text>
        </Stack>
      </Paper>
    </Container>
  )
}
