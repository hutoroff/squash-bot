import type { ReactNode } from 'react'

export type BadgeVariant = 'success' | 'muted' | 'info' | 'warning' | 'outline'

interface BadgeProps {
  variant: BadgeVariant
  children: ReactNode
}

export default function Badge({ variant, children }: BadgeProps) {
  return (
    <span className={`badge badge--${variant}`}>{children}</span>
  )
}
