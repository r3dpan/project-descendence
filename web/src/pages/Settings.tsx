import { SegmentedControl, Stack } from '@mantine/core'
import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'
import PageHeader from '../components/PageHeader'
import AccountTab from './settings/AccountTab'
import UserList from './UserList'
import TokenList from './TokenList'
import Configuration from './Configuration'

// Off-plan web UI work: Users/Tokens/Configuration used to be unrelated
// top-level nav entries even though they're all "administer this instance"
// concerns - folded here as Settings sub-tabs, alongside the pre-existing
// self-service Account tab. activeTab is derived from location.pathname
// (not internal state) so a deep-link or App.tsx's old-path redirects land
// on the right tab without a click ever happening. The tabs are rendered as
// a SegmentedControl (matching Jobs/Runs list filters) rather than Mantine
// Tabs, per the Nocturne mockup's convention.
export default function Settings() {
  const { principal } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const canSeeUsers = principal?.permissions.includes('users:read') ?? false
  const canSeeConfig = principal?.permissions.includes('config:read') ?? false

  const activeTab = location.pathname.split('/')[2] || 'account'

  const tabs = [
    { label: 'Account', value: 'account' },
    ...(canSeeUsers ? [{ label: 'Users', value: 'users' }] : []),
    ...(canSeeUsers ? [{ label: 'Tokens', value: 'tokens' }] : []),
    ...(canSeeConfig ? [{ label: 'Configuration', value: 'configuration' }] : []),
  ]

  return (
    <PageHeader title="Settings" subtitle="Account, users and configuration">
      <Stack gap="lg" maw={900}>
        <SegmentedControl w="fit-content" value={activeTab} onChange={(v) => navigate(`/settings/${v}`)} data={tabs} />

        {activeTab === 'account' && <AccountTab />}
        {activeTab === 'users' && canSeeUsers && <UserList />}
        {activeTab === 'tokens' && canSeeUsers && <TokenList />}
        {activeTab === 'configuration' && canSeeConfig && <Configuration />}
      </Stack>
    </PageHeader>
  )
}
