import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { resolve, dirname } from 'node:path'
import { EVENT_LABELS } from './auditEvents'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../')
const goFile = resolve(repoRoot, 'internal/models/audit_event.go')

function extractBackendTypes(): string[] {
  const src = readFileSync(goFile, 'utf-8')
  const matches = [...src.matchAll(/AuditEventType\s*=\s*"([^"]+)"/g)]
  return matches.map(m => m[1])
}

describe('audit event type sync', () => {
  it('EVENT_LABELS covers every backend AuditEventType — no missing, no extra', () => {
    const backend = new Set(extractBackendTypes())
    const frontend = new Set(Object.keys(EVENT_LABELS))

    const missing = [...backend].filter(t => !frontend.has(t))
    const extra = [...frontend].filter(t => !backend.has(t))

    expect(missing, `Types in ${goFile} not in EVENT_LABELS: ${missing.join(', ')}`).toEqual([])
    expect(extra, `Types in EVENT_LABELS not in ${goFile}: ${extra.join(', ')}`).toEqual([])
  })
})
