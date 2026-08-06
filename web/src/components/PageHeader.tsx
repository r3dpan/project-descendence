import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Group, Text } from '@mantine/core'
import { IconChevronLeft } from '@tabler/icons-react'

// The mockup's per-page shell: a full-bleed 60px header bar (optional
// back-link, title + subtitle, optional right-aligned action like JobList's
// "New job" button) over a padded, independently-scrolling content area.
// Layout.tsx's AppShell.Main has zero padding of its own so this can sit
// edge-to-edge - every page renders one of these instead of a bare
// fragment/<Title>.
export default function PageHeader({
  title,
  subtitle,
  backTo,
  backLabel = 'Back',
  action,
  children,
}: {
  title: ReactNode
  subtitle?: ReactNode
  backTo?: string
  backLabel?: string
  action?: ReactNode
  children?: ReactNode
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minWidth: 0 }}>
      <Group
        h={60}
        gap="md"
        px="lg"
        wrap="nowrap"
        style={{ borderBottom: '1px solid var(--mantine-color-dark-4)', flex: 'none' }}
      >
        {backTo && (
          <Text
            component={Link}
            to={backTo}
            size="sm"
            c="dimmed"
            display="flex"
            style={{ alignItems: 'center', gap: 4, flex: 'none', textDecoration: 'none' }}
          >
            <IconChevronLeft size={12} />
            {backLabel}
          </Text>
        )}
        <div style={{ minWidth: 0 }}>
          <Text fw={500} size="lg" lh={1.15} truncate>
            {title}
          </Text>
          {subtitle && (
            <Text size="xs" c="dimmed">
              {subtitle}
            </Text>
          )}
        </div>
        {action && (
          <Group ml="auto" gap="sm" wrap="nowrap" style={{ flex: 'none' }}>
            {action}
          </Group>
        )}
      </Group>
      <div style={{ flex: 1, overflowY: 'auto', padding: 'var(--mantine-spacing-xl) var(--mantine-spacing-lg)' }}>
        {children}
      </div>
    </div>
  )
}
