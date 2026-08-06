import type { ReactNode } from 'react'
import { NavLink as RouterNavLink, useLocation, useNavigate } from 'react-router-dom'
import { AppShell, Burger, Button, Group, NavLink, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useAuth } from './auth'

export default function Layout({ children }: { children: ReactNode }) {
  const { principal, logout } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [navbarOpen, { toggle: toggleNavbar }] = useDisclosure()
  // Hidden rather than shown-and-403'd on click, matching the CLI TUI's
  // menu-gating (task 8.9) - users:read is what governs the server side too.
  const canSeeUsers = principal?.permissions.includes('users:read') ?? false

  async function handleLogout() {
    await logout()
    navigate('/login', { replace: true })
  }

  const isActive = (path: string, exact = false) =>
    exact ? location.pathname === path : location.pathname.startsWith(path)

  return (
    <AppShell header={{ height: 60 }} navbar={{ width: 220, breakpoint: 'sm', collapsed: { mobile: !navbarOpen } }}>
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group>
            <Burger opened={navbarOpen} onClick={toggleNavbar} hiddenFrom="sm" size="sm" />
            <Text fw={600}>Descendence</Text>
          </Group>
          <Button variant="light" size="xs" onClick={handleLogout}>
            Sign out
          </Button>
        </Group>
      </AppShell.Header>
      <AppShell.Navbar p="md">
        <NavLink component={RouterNavLink} to="/" label="Dashboard" active={isActive('/', true)} />
        <NavLink component={RouterNavLink} to="/runs" label="Runs" active={isActive('/runs')} />
        <NavLink component={RouterNavLink} to="/jobs" label="Jobs" active={isActive('/jobs')} />
        <NavLink component={RouterNavLink} to="/runtimes" label="Runtimes" active={isActive('/runtimes')} />
        {canSeeUsers && (
          <>
            <NavLink component={RouterNavLink} to="/users" label="Users" active={isActive('/users')} />
            <NavLink component={RouterNavLink} to="/tokens" label="Tokens" active={isActive('/tokens')} />
          </>
        )}
        <NavLink component={RouterNavLink} to="/settings" label="Settings" active={isActive('/settings')} mt="auto" />
      </AppShell.Navbar>
      <AppShell.Main>{children}</AppShell.Main>
    </AppShell>
  )
}
