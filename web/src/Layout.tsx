import type { ReactNode } from 'react'
import { NavLink as RouterNavLink, useLocation } from 'react-router-dom'
import { AppShell, Burger, Button, Group, NavLink, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'

// "Sign out" is a plain navigation to GET /api/v1/auth/logout, not a fetch
// (like Login.tsx's "Sign in") - it needs to follow a redirect through the
// IdP's end_session_endpoint so the IdP's own SSO session ends too, which
// an XHR/fetch can't do. useAuth's principal state resets naturally on the
// resulting full-page navigation back to "/", not via any explicit call
// here.
export default function Layout({ children }: { children: ReactNode }) {
  const location = useLocation()
  const [navbarOpen, { toggle: toggleNavbar }] = useDisclosure()

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
          <Button component="a" href="/api/v1/auth/logout" variant="light" size="xs">
            Sign out
          </Button>
        </Group>
      </AppShell.Header>
      <AppShell.Navbar p="md">
        <NavLink component={RouterNavLink} to="/" label="Dashboard" active={isActive('/', true)} />
        <NavLink component={RouterNavLink} to="/runs" label="Runs" active={isActive('/runs')} />
        <NavLink component={RouterNavLink} to="/jobs" label="Jobs" active={isActive('/jobs')} />
        <NavLink component={RouterNavLink} to="/runtimes" label="Runtimes" active={isActive('/runtimes')} />
        <NavLink component={RouterNavLink} to="/settings" label="Settings" active={isActive('/settings')} mt="auto" />
      </AppShell.Navbar>
      <AppShell.Main>{children}</AppShell.Main>
    </AppShell>
  )
}
