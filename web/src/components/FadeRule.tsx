// Nocturne signature: a rule that fades to transparent at both ends instead
// of stopping cleanly, rather than a plain <Divider>.
export default function FadeRule() {
  return (
    <div
      style={{
        height: 1,
        margin: 'var(--mantine-spacing-md) 0',
        background:
          'linear-gradient(to right, transparent, var(--mantine-color-dark-4) 48px, var(--mantine-color-dark-4) calc(100% - 48px), transparent)',
      }}
    />
  )
}
