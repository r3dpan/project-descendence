import type { JobParam } from './api/jobs'

export interface ParamFieldProps {
  param: JobParam
  value: string
  onChange: (value: string) => void
  // Overrides from a manifest's form: block (task 7.8). "" falls back to
  // param.name / no help text, matching FormField's own Label/Help zero
  // values on the Go side.
  label?: string
  help?: string
}

// The one place a param's contract turns into an input, shared by the
// trigger form (JobDetail) and the manifest editor's preview pane - the
// latter's entire purpose is to show what the former will render, so the two
// must never be free to drift apart from each other.
export function ParamField({ param, value, onChange, label, help }: ParamFieldProps) {
  const inputId = `param-${param.name}`
  return (
    <div style={{ marginTop: '0.5rem' }}>
      <label htmlFor={inputId}>
        {label || param.name}
        {param.required && !param.default ? ' *' : ''} ({param.type})
      </label>
      <br />
      {help && (
        <small style={{ display: 'block', color: 'gray' }}>{help}</small>
      )}
      {param.type === 'bool' ? (
        <input
          id={inputId}
          type="checkbox"
          checked={value === 'true'}
          onChange={(e) => onChange(e.target.checked ? 'true' : 'false')}
        />
      ) : (
        <input
          id={inputId}
          type={param.secret || param.type === 'mount' ? 'password' : param.type === 'number' ? 'number' : 'text'}
          value={value}
          required={param.required && !param.default}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </div>
  )
}
