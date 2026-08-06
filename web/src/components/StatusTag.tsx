import { Badge } from '@mantine/core'
import { statusKind, statusPulses } from '../statusColor'

// The mockup's "tag" convention: a solid accent chip for positive states, an
// outlined accent chip for in-progress ones (pulsing for running/building),
// and a solid neutral chip for negative ones - same shape everywhere a
// run/job/runtime status appears (lists, detail pages, dashboard feed).
export default function StatusTag({ status, label }: { status: string; label?: string }) {
  const kind = statusKind(status)
  const text = label ?? status

  const variant = kind === 'progress' ? 'outline' : kind === 'neutral' ? 'light' : 'filled'
  const color = kind === 'negative' ? 'dark' : kind === 'neutral' ? 'gray' : 'accent'

  return (
    <Badge
      variant={variant}
      color={color}
      radius="sm"
      tt="none"
      fw={500}
      style={statusPulses(status) ? { animation: 'pulseTag 1.6s ease-in-out infinite' } : undefined}
    >
      {text}
    </Badge>
  )
}
