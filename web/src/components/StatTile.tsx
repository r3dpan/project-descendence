import type { ReactNode } from 'react'
import { Paper, SimpleGrid, Text } from '@mantine/core'

export interface Tile {
  label: string
  value: ReactNode
  color?: string
  caption?: string
}

// A single stat card: color dot + label, big value, small caption. Used for
// the dashboard's "Run activity" row and "System status" tiles - the one
// repeated shape the mockup uses for at-a-glance numbers.
function StatTile({ tile }: { tile: Tile }) {
  return (
    <Paper p="sm" radius="md" bg="dark.6">
      <Text size="xs" c="dimmed" display="flex" style={{ alignItems: 'center', gap: 8 }} mb={4}>
        <span
          style={{
            width: 7,
            height: 7,
            borderRadius: '50%',
            background: `var(--mantine-color-${tile.color ?? 'accent'}-5)`,
            display: 'inline-block',
            flex: 'none',
          }}
        />
        {tile.label}
      </Text>
      <Text size="27px" fw={600} lh={1.1}>
        {tile.value}
      </Text>
      {tile.caption && (
        <Text size="11px" c="dimmed" mt={2}>
          {tile.caption}
        </Text>
      )}
    </Paper>
  )
}

export default function StatTileGrid({ tiles, cols = 5 }: { tiles: Tile[]; cols?: number }) {
  return (
    <SimpleGrid cols={{ base: 2, sm: 3, lg: cols }}>
      {tiles.map((tile) => (
        <StatTile key={tile.label} tile={tile} />
      ))}
    </SimpleGrid>
  )
}
