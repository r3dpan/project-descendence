import { load } from 'js-yaml'
import type { JobParam } from './api/jobs'

export interface PreviewField {
  param: JobParam
  label?: string
  help?: string
}

export interface PreviewSection {
  title?: string
  help?: string
  fields: PreviewField[]
}

export interface ManifestPreview {
  params: JobParam[]
  sections: PreviewSection[]
  // Params form: doesn't mention - appended after, in contract order. form:
  // may be partial or absent entirely (task 7.8), so this is never empty
  // just because form: didn't cover everything.
  unplaced: PreviewField[]
}

const emptyPreview: ManifestPreview = { params: [], sections: [], unplaced: [] }

const paramTypes = new Set(['string', 'number', 'bool', 'mount'])

function paramType(raw: unknown): JobParam['type'] {
  return typeof raw === 'string' && paramTypes.has(raw) ? (raw as JobParam['type']) : 'string'
}

// parseManifestPreview turns raw manifest YAML into what ManifestEditor's
// preview pane renders. Deliberately lenient, unlike internal/manifest's own
// validate(): this is a live, keystroke-by-keystroke preview of a document
// that is expected to be mid-edit and momentarily wrong, not the thing that
// decides whether a commit is accepted - the server (Parse, called from
// createRepoFile's sync) remains sole authority on that. A param this parser
// can't make sense of, or a form: reference it can't resolve, is silently
// skipped rather than surfaced as an error - only genuinely unparsable YAML
// throws, since that is the one case with nothing left to render.
export function parseManifestPreview(yamlText: string): ManifestPreview {
  const doc = load(yamlText) as Record<string, unknown> | null | undefined
  if (!doc || typeof doc !== 'object') return emptyPreview

  const rawParams = Array.isArray(doc.params) ? doc.params : []
  const params: JobParam[] = []
  for (const raw of rawParams) {
    if (!raw || typeof raw !== 'object') continue
    const p = raw as Record<string, unknown>
    if (typeof p.name !== 'string' || p.name === '') continue

    const hasDefault = p.default !== undefined && p.default !== null
    const required = hasDefault ? false : p.required !== false

    params.push({
      name: p.name,
      type: paramType(p.type),
      required,
      default: hasDefault ? String(p.default) : null,
      secret: p.type === 'mount' || p.secret === true,
    })
  }

  const byName = new Map(params.map((p) => [p.name, p]))
  const placed = new Set<string>()
  const sections: PreviewSection[] = []

  const form = doc.form as Record<string, unknown> | undefined
  const rawSections = form && Array.isArray(form.sections) ? form.sections : []
  for (const rawSection of rawSections) {
    if (!rawSection || typeof rawSection !== 'object') continue
    const rs = rawSection as Record<string, unknown>

    const fields: PreviewField[] = []
    const rawFields = Array.isArray(rs.fields) ? rs.fields : []
    for (const rf of rawFields) {
      const name = typeof rf === 'string' ? rf : (rf as Record<string, unknown> | null)?.name
      if (typeof name !== 'string') continue
      const param = byName.get(name)
      if (!param || placed.has(param.name)) continue
      placed.add(param.name)

      const obj = typeof rf === 'object' && rf ? (rf as Record<string, unknown>) : undefined
      fields.push({
        param,
        label: typeof obj?.label === 'string' ? obj.label : undefined,
        help: typeof obj?.help === 'string' ? obj.help : undefined,
      })
    }
    if (fields.length > 0) {
      sections.push({
        title: typeof rs.title === 'string' ? rs.title : undefined,
        help: typeof rs.help === 'string' ? rs.help : undefined,
        fields,
      })
    }
  }

  const unplaced: PreviewField[] = params.filter((p) => !placed.has(p.name)).map((param) => ({ param }))

  return { params, sections, unplaced }
}
