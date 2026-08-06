import { Button, Paper, Stack, Text } from '@mantine/core'
import { IconSparkles } from '@tabler/icons-react'

// Phase 9 (task 9.11): logging in is a plain top-level navigation to
// GET /api/v1/auth/login, not a fetch - an XHR cannot follow the 302 to the
// IdP's authorization endpoint the way a real browser navigation can. This
// app is OIDC-only, so unlike the mockup's login card there are no
// username/password fields - just the chrome (logo, wordmark, tagline) and
// a single sign-in action.
export default function Login() {
  return (
    <div style={{ position: 'fixed', inset: 0, display: 'grid', placeItems: 'center' }}>
      <Paper w={380} p="xl" radius="lg" bg="dark.6" shadow="lg">
        <Stack align="center" gap="xs" mb="sm">
          <IconSparkles size={28} color="var(--mantine-color-accent-5)" />
          <Text fw={500} size="lg">
            Descendence
          </Text>
          <Text size="13px" c="dimmed">
            Automation for the homelab
          </Text>
        </Stack>
        <Button component="a" href="/api/v1/auth/login" fullWidth mt="lg">
          Sign in
        </Button>
      </Paper>
    </div>
  )
}
