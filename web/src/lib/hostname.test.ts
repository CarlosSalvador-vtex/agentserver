import { describe, it, expect, vi } from 'vitest'
import { buildTenantLoginUrl, extractWorkspaceSlug, isApexHost, slugifyName } from './hostname'

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

describe('isApexHost', () => {
  it('returns true on apex host', () => {
    expect(isApexHost('agentserver.analytics.vtex.com')).toBe(true)
  })

  it('returns false on tenant subdomain', () => {
    expect(isApexHost('acme.agentserver.analytics.vtex.com')).toBe(false)
  })
})

describe('buildTenantLoginUrl', () => {
  it('uses explicit host when provided', () => {
    expect(buildTenantLoginUrl('acme', 'agentserver.analytics.vtex.com')).toBe(
      'https://acme.agentserver.analytics.vtex.com/login',
    )
  })

  it('preserves port on localhost host override', () => {
    expect(buildTenantLoginUrl('foo', 'localhost:5173')).toBe('http://foo.localhost:5173/login')
  })
})
