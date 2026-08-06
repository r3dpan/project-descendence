// Shared color mapping for run states and runtime build statuses, so the same
// status reads identically as a Badge on list pages and detail pages.
export function statusColor(status: string): string {
  switch (status) {
    case 'succeeded':
    case 'ready':
      return 'green'
    case 'failed':
    case 'lost':
    case 'cancelled':
      return 'red'
    case 'running':
    case 'building':
      return 'blue'
    case 'queued':
    case 'pending':
      return 'gray'
    default:
      return 'gray'
  }
}

export type StatusKind = 'positive' | 'progress' | 'negative' | 'neutral'

// Coarser grouping than statusColor, used by StatusTag.tsx to pick the
// Nocturne tag treatment (solid accent chip / outlined accent chip / solid
// neutral chip) rather than a per-status color. "progress" states also get
// the pulsing animation.
export function statusKind(status: string): StatusKind {
  switch (status) {
    case 'succeeded':
    case 'ready':
    case 'enabled':
      return 'positive'
    case 'running':
    case 'building':
    case 'queued':
    case 'pending':
      return 'progress'
    case 'failed':
    case 'lost':
    case 'cancelled':
    case 'disabled':
      return 'negative'
    default:
      return 'neutral'
  }
}

// "running"/"building" pulse (task in progress); "queued"/"pending" are
// progress-kind too but static - matches the mockup's TAGS table.
export function statusPulses(status: string): boolean {
  return status === 'running' || status === 'building'
}
