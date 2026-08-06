import { Stack, Text, Title } from '@mantine/core'
import { useAuth } from '../../auth'

// Read-only identity info - the one Settings tab every authenticated user
// can reach, unlike Users/Tokens/Configuration which are gated on
// permissions. Phase 9 (task 9.8) removed self password-change entirely:
// identity comes from the IdP now, there is no local password to change.
export default function AccountTab() {
  const { principal } = useAuth()

  return (
    <>
      <Title order={3} mb="md">
        Account
      </Title>
      <Stack gap="xs">
        <Text>
          <strong>Name:</strong> {principal?.name}
        </Text>
        <Text>
          <strong>Kind:</strong> {principal?.kind}
        </Text>
        <Text>
          <strong>Role:</strong> {principal?.role || '(none)'}
        </Text>
      </Stack>
    </>
  )
}
