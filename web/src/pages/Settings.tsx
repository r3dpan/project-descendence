import { Tabs, Title } from '@mantine/core'
import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'
import AccountTab from './settings/AccountTab'
import UserList from './UserList'
import TokenList from './TokenList'
import Configuration from './Configuration'

// Off-plan web UI work: Users/Tokens/Configuration used to be unrelated
// top-level nav entries even though they're all "administer this instance"
// concerns - folded here as Settings sub-tabs, alongside the pre-existing
// self-service Account tab. activeTab is derived from location.pathname
// (not internal state) so a deep-link or App.tsx's old-path redirects land
// on the right tab without a click ever happening.
export default function Settings() {
  const { principal } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const canSeeUsers = principal?.permissions.includes('users:read') ?? false
  const canSeeConfig = principal?.permissions.includes('config:read') ?? false

  const activeTab = location.pathname.split('/')[2] || 'account'

  return (
    <>
      <Title order={2} mb="md">
        Settings
      </Title>
      <Tabs value={activeTab} onChange={(value) => navigate(`/settings/${value}`)}>
        <Tabs.List mb="md">
          <Tabs.Tab value="account">Account</Tabs.Tab>
          {canSeeUsers && <Tabs.Tab value="users">Users</Tabs.Tab>}
          {canSeeUsers && <Tabs.Tab value="tokens">Tokens</Tabs.Tab>}
          {canSeeConfig && <Tabs.Tab value="configuration">Configuration</Tabs.Tab>}
        </Tabs.List>
        <Tabs.Panel value="account">
          <AccountTab />
        </Tabs.Panel>
        {canSeeUsers && (
          <Tabs.Panel value="users">
            <UserList />
          </Tabs.Panel>
        )}
        {canSeeUsers && (
          <Tabs.Panel value="tokens">
            <TokenList />
          </Tabs.Panel>
        )}
        {canSeeConfig && (
          <Tabs.Panel value="configuration">
            <Configuration />
          </Tabs.Panel>
        )}
      </Tabs>
    </>
  )
}
