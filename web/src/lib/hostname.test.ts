import { describe, it, expect } from 'vitest'
import { extractWorkspaceSlug, slugifyName } from './hostname'

describe('extractWorkspaceSlug', () => {
  it('returns slug for tenant subdomain', () => {
    expect(extractWorkspaceSlug('empresa-a.agentserver.analytics.vtex.com')).toBe('empresa-a')
  })
  it('returns empty for root host', () => {
    expect(extractWorkspaceSlug('agentserver.analytics.vtex.com')).toBe('')
  })
  it('returns empty for sandbox subdomain', () => {
    expect(extractWorkspaceSlug('claw-x1ya2cbn.agentserver.analytics.vtex.com')).toBe('')
    expect(extractWorkspaceSlug('hermes-foo.agentserver.analytics.vtex.com')).toBe('')
  })
  it('returns empty for localhost', () => {
    expect(extractWorkspaceSlug('localhost')).toBe('')
    expect(extractWorkspaceSlug('localhost:5173')).toBe('')
  })
})

describe('slugifyName', () => {
  it('kebab-cases names', () => {
    expect(slugifyName('Empresa de Teste')).toBe('empresa-de-teste')
  })
})
