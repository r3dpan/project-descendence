import { Button, Container, Paper, Stack, Text } from '@mantine/core'

// Phase 9 (task 9.11): logging in is a plain top-level navigation to
// GET /api/v1/auth/login, not a fetch - an XHR cannot follow the 302 to the
// IdP's authorization endpoint the way a real browser navigation can.
export default function Login() {
  return (
    <Container size="xs" mt="10vh">
      <Paper withBorder p="xl" radius="md">
        <Stack align="center">
          <Text ta="center" fw={600} size="xl">
            Descendence
          </Text>
          <Button component="a" href="/api/v1/auth/login" fullWidth>
            Sign in
          </Button>
        </Stack>
      </Paper>
    </Container>
  )
}
