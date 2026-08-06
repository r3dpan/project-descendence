import type { ReactNode } from 'react'
import { NavLink as RouterNavLink, useLocation } from 'react-router-dom'
import { AppShell, Avatar, Box, Burger, Group, Text, UnstyledButton } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import {
  IconBriefcase,
  IconLayoutDashboard,
  IconLogout,
  IconPlayerPlay,
  IconSettings,
  IconSparkles,
  IconStack2,
} from '@tabler/icons-react'
import { useAuth } from './auth'

const NAV_GROUPS: { label: string; items: { to: string; label: string; icon: typeof IconSparkles; exact?: boolean }[] }[] = [
  {
    label: 'Overview',
    items: [{ to: '/', label: 'Dashboard', icon: IconLayoutDashboard, exact: true }],
  },
  {
    label: 'Automate',
    items: [
      { to: '/jobs', label: 'Jobs', icon: IconBriefcase },
      { to: '/runs', label: 'Runs', icon: IconPlayerPlay },
      { to: '/runtimes', label: 'Runtimes', icon: IconStack2 },
    ],
  },
  {
    label: 'Administer',
    items: [{ to: '/settings', label: 'Settings', icon: IconSettings }],
  },
]

function initials(name: string): string {
  const parts = name.split(/[.\s_-]+/).filter(Boolean)
  if (parts.length === 0) return '?'
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

// "Sign out" is a plain navigation to GET /api/v1/auth/logout, not a fetch
// (like Login.tsx's "Sign in") - it needs to follow a redirect through the
// IdP's end_session_endpoint so the IdP's own SSO session ends too, which
// an XHR/fetch can't do. useAuth's principal state resets naturally on the
// resulting full-page navigation back to "/", not via any explicit call
// here.
export default function Layout({ children }: { children: ReactNode }) {
  const location = useLocation()
  const { principal } = useAuth()
  const [navbarOpen, { toggle: toggleNavbar }] = useDisclosure()

  const isActive = (to: string, exact = false) =>
    exact ? location.pathname === to : location.pathname.startsWith(to)

  return (
    <AppShell
      navbar={{ width: 240, breakpoint: 'sm', collapsed: { mobile: !navbarOpen } }}
      padding={0}
      h="100%"
    >
      <AppShell.Navbar
        p="md"
        bg="dark.6"
        style={{
          borderRight: '1px solid var(--mantine-color-dark-4)',
          display: 'flex',
          flexDirection: 'column',
          height: '100%',
        }}
      >
        <Box mb="lg">
          <Group gap="xs" mb={6}>
            <Burger opened={navbarOpen} onClick={toggleNavbar} hiddenFrom="sm" size="sm" />
            <IconSparkles size={18} color="var(--mantine-color-accent-5)" />
            <Text fw={500} size="md">
              Descendence
            </Text>
          </Group>
          <Box w={22} h={2} bg="accent.5" ml={2} />
        </Box>

        <Box style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 'var(--mantine-spacing-lg)' }}>
          {NAV_GROUPS.map((group) => (
            <div key={group.label}>
              <Text size="10.5px" tt="uppercase" c="dimmed" fw={600} style={{ letterSpacing: '.09em' }} px="sm" mb={6}>
                {group.label}
              </Text>
              {group.items.map((item) => {
                const active = isActive(item.to, item.exact)
                const Icon = item.icon
                return (
                  <UnstyledButton
                    key={item.to}
                    component={RouterNavLink}
                    to={item.to}
                    display="flex"
                    px="sm"
                    py={8}
                    mb={2}
                    style={{
                      alignItems: 'center',
                      gap: 'var(--mantine-spacing-sm)',
                      borderRadius: 'var(--mantine-radius-md)',
                      color: active ? 'var(--mantine-color-white)' : 'var(--mantine-color-dark-1)',
                      background: active ? 'var(--mantine-color-dark-5)' : 'transparent',
                    }}
                  >
                    <Icon size={16} />
                    <Text size="sm">{item.label}</Text>
                  </UnstyledButton>
                )
              })}
            </div>
          ))}
        </Box>

        {principal && (
          <Group
            gap="sm"
            mt="auto"
            pt="md"
            wrap="nowrap"
            style={{ borderTop: '1px solid var(--mantine-color-dark-4)' }}
          >
            <Avatar radius="xl" size={30} color="accent" variant="filled">
              <Text size="11px" fw={600}>
                {initials(principal.name)}
              </Text>
            </Avatar>
            <div style={{ flex: 1, minWidth: 0 }}>
              <Text size="13px" fw={500} truncate>
                {principal.name}
              </Text>
              <Text size="11px" c="dimmed" tt="capitalize">
                {principal.role}
              </Text>
            </div>
            <UnstyledButton
              component="a"
              href="/api/v1/auth/logout"
              title="Sign out"
              c="dimmed"
              display="flex"
              style={{ flex: 'none' }}
            >
              <IconLogout size={15} />
            </UnstyledButton>
          </Group>
        )}
      </AppShell.Navbar>
      <AppShell.Main h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
        {children}
      </AppShell.Main>
    </AppShell>
  )
}
