import { createTheme, type MantineColorsTuple } from '@mantine/core'

// "Nocturne" design tokens, ported from the Claude Design mockup
// ("Automation platform UI modernization" project, Descendence Webapp.dc.html)
// into Mantine's theming API rather than hand-rolled CSS, so existing
// Mantine components (AppShell, Table, Badge, ...) pick this up for free.
// The mockup's raw hex ramps live in its own styles.css; these are copied
// verbatim, not re-derived, to stay a faithful port.

const accent: MantineColorsTuple = [
  '#faf9ff', // synthesized: one step lighter than the mockup's own 100, Mantine needs 10 shades
  '#f5f4ff', // --color-accent-100
  '#e7e5fe', // --color-accent-200
  '#d2cefd', // --color-accent-300
  '#b5abfc', // --color-accent-400
  '#9184d9', // --color-accent (the mockup's actual accent, slotted at index 5)
  '#796cbf', // --color-accent-600
  '#5d5294', // --color-accent-700
  '#423a6a', // --color-accent-800
  '#2b2741', // --color-accent-900
]

// Mantine's "dark" palette drives every dark-scheme surface: index 7 is the
// default body background, 6 is Paper/Card, 5 is hover, 4 is the default
// border/divider color. Mapped onto the mockup's --color-bg/--color-surface/
// --color-divider/--color-text so unstyled Mantine components land on the
// mockup's ground without per-component overrides.
const dark: MantineColorsTuple = [
  '#e9e9ed', // --color-text
  '#cfd3e5', // --color-neutral-300
  '#b2b6ca', // --color-neutral-400
  '#9397ab', // --color-neutral-500 (used as some border/muted-text contexts)
  '#75798c', // --color-neutral-600 (default border - close to --color-divider's visual weight)
  '#3f424d', // --color-neutral-800 (hover)
  '#232532', // --color-surface (Paper/Card background)
  '#161826', // --color-bg (body background)
  '#121420', // one step below --color-bg, for popovers/dropdowns sitting above it
  '#0c0d15', // darkest, rarely used
]

export const theme = createTheme({
  colors: { accent, dark },
  primaryColor: 'accent',
  primaryShade: 5,
  autoContrast: true,

  fontFamily: 'Inter, system-ui, sans-serif',
  fontFamilyMonospace: 'ui-monospace, SFMono-Regular, Menlo, monospace',
  headings: {
    fontFamily: 'Inter, system-ui, sans-serif',
    fontWeight: '500',
  },

  // The mockup's --space-1..8 scale (px), mapped onto Mantine's xs..xl keys.
  spacing: {
    xs: '5.6px',
    sm: '8.4px',
    md: '11.2px',
    lg: '16.8px',
    xl: '22.4px',
  },

  // The mockup's --radius-sm/md/lg.
  radius: {
    xs: '4px',
    sm: '4px',
    md: '8px',
    lg: '14px',
    xl: '14px',
  },
  defaultRadius: 'md',
})
