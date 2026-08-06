import { Paper, Stack, Text } from '@mantine/core'
import { useAuth } from '../../auth'

// Read-only identity info - the one Settings tab every authenticated user
// can reach, unlike Users/Tokens/Configuration which are gated on
// permissions. Phase 9 (task 9.8) removed self password-change entirely:
// identity comes from the IdP now, there is no local password to change.
export default function AccountTab() {
  const { principal } = useAuth()

  return (
    <Paper p="md" radius="md" bg="dark.6" maw={440}>
      <Stack gap="xs">
        <Text size="sm">
          <Text component="span" c="dimmed">
            Name{' '}
          </Text>
          {principal?.name}
        </Text>
        <Text size="sm">
          <Text component="span" c="dimmed">
            Kind{' '}
          </Text>
          {principal?.kind}
        </Text>
        <Text size="sm">
          <Text component="span" c="dimmed">
            Role{' '}
          </Text>
          {principal?.role || '(none)'}
        </Text>
      </Stack>
    </Paper>
  )
}
