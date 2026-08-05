// Hand-written thin fetch wrapper, mirroring internal/client's do()/send()
// shape (ARCHITECTURE.md decision #15/#11: the spec is generated as types
// only - see schema.ts - the request logic stays hand-written). Every
// resource file (auth.ts, runs.ts) is built on top of this one function.

export class APIError extends Error {
  status: number
  detail?: string

  constructor(status: number, detail?: string, title?: string) {
    super(detail || title || `unexpected status ${status}`)
    this.status = status
    this.detail = detail
  }
}

export interface RequestOptions {
  method?: string
  body?: unknown
  query?: Record<string, string | number | undefined>
  // Statuses besides 2xx that should still decode `out` rather than throw -
  // mirrors internal/client's alsoOK, currently unused here but kept for
  // parity since /healthz needs exactly this on the Go side.
  alsoOK?: number[]
}

// Same-origin only (ARCHITECTURE.md §4.11): the session cookie is
// HttpOnly/Secure/SameSite=Lax and never touched directly by this code,
// unlike a bearer-token client which would set an Authorization header here.
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const url = new URL(path, window.location.origin)
  if (opts.query) {
    for (const [key, value] of Object.entries(opts.query)) {
      if (value !== undefined) url.searchParams.set(key, String(value))
    }
  }

  const res = await fetch(url, {
    method: opts.method ?? 'GET',
    credentials: 'same-origin',
    headers: opts.body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  })

  const ok = (res.status >= 200 && res.status < 300) || (opts.alsoOK?.includes(res.status) ?? false)
  if (!ok) {
    let detail: string | undefined
    let title: string | undefined
    try {
      const problem = await res.json()
      detail = problem.detail
      title = problem.title
    } catch {
      // Not a problem+json body - a status-only APIError is still useful.
    }
    throw new APIError(res.status, detail, title)
  }

  // Not every 2xx carries a body (logout's 204, buildRuntime's empty 202) -
  // read as text first rather than assuming every success decodes as JSON.
  const text = await res.text()
  if (text === '') return undefined as T
  return JSON.parse(text) as T
}
