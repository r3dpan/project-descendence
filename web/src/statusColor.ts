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
