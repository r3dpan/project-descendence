import { Checkbox, NumberInput, PasswordInput, TextInput } from '@mantine/core'
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
  const isOptional = !(param.required && !param.default)
  const fieldLabel = `${label || param.name}${isOptional ? '' : ' *'} (${param.type})`

  if (param.type === 'bool') {
    return (
      <Checkbox
        mt="sm"
        id={inputId}
        label={fieldLabel}
        description={help}
        checked={value === 'true'}
        onChange={(e) => onChange(e.currentTarget.checked ? 'true' : 'false')}
      />
    )
  }

  if (param.secret || param.type === 'mount') {
    return (
      <PasswordInput
        mt="sm"
        id={inputId}
        label={fieldLabel}
        description={help}
        value={value}
        required={!isOptional}
        onChange={(e) => onChange(e.target.value)}
      />
    )
  }

  if (param.type === 'number') {
    return (
      <NumberInput
        mt="sm"
        id={inputId}
        label={fieldLabel}
        description={help}
        value={value}
        required={!isOptional}
        onChange={(v) => onChange(String(v))}
      />
    )
  }

  return (
    <TextInput
      mt="sm"
      id={inputId}
      label={fieldLabel}
      description={help}
      value={value}
      required={!isOptional}
      onChange={(e) => onChange(e.target.value)}
    />
  )
}
