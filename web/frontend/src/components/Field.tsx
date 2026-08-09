import type { ReactNode } from 'react'

interface FieldProps {
  label: string
  /** One line saying what the setting does — not what it is. */
  help: string
  /** Renders the control next to the label instead of below it (checkboxes). */
  inline?: boolean
  children: ReactNode
}

/** A labelled form control with permanent help text underneath. */
export default function Field({ label, help, inline, children }: FieldProps) {
  return (
    <div className={'field' + (inline ? ' field--inline' : '')}>
      <label className="field__label">
        {inline ? <>{children}<span>{label}</span></> : <><span>{label}</span>{children}</>}
      </label>
      <p className="field__help">{help}</p>
    </div>
  )
}
