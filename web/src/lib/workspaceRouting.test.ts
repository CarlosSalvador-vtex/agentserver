import { describe, it, expect } from 'vitest'
import { resolveAuthedLandingPath } from './workspaceRouting'

describe('resolveAuthedLandingPath', () => {
  const ws = [{ id: 'a' }, { id: 'b' }]

  it('prefers URL workspace when valid', () => {
    expect(
      resolveAuthedLandingPath({
        apex: true,
        workspaces: ws,
        activeWorkspaceId: 'b',
        urlWorkspaceId: 'a',
      }),
    ).toBe('/w/a')
  })

  it('uses active_workspace_id when set', () => {
    expect(
      resolveAuthedLandingPath({
        apex: true,
        workspaces: ws,
        activeWorkspaceId: 'b',
      }),
    ).toBe('/w/b')
  })

  it('sends apex without active workspace to choose-workspace', () => {
    expect(
      resolveAuthedLandingPath({
        apex: true,
        workspaces: ws,
        activeWorkspaceId: null,
      }),
    ).toBe('/choose-workspace')
  })

  it('falls back to first workspace on tenant host', () => {
    expect(
      resolveAuthedLandingPath({
        apex: false,
        workspaces: ws,
        activeWorkspaceId: null,
      }),
    ).toBe('/w/a')
  })

  it('apex with empty workspaces still lands on picker', () => {
    expect(
      resolveAuthedLandingPath({
        apex: true,
        workspaces: [],
        activeWorkspaceId: null,
      }),
    ).toBe('/choose-workspace')
  })

  it('preserves a playground deep link on cold load (does not bounce to /w/)', () => {
    expect(
      resolveAuthedLandingPath({
        apex: false,
        workspaces: ws,
        activeWorkspaceId: 'a',
        currentPath: '/playground/skills/3c629319',
      }),
    ).toBe('/playground/skills/3c629319')
  })

  it.each([
    '/playground',
    '/playground/souls/abc',
    '/marketplace',
    '/admin/users',
    '/workspaces',
    '/oauth2/device',
  ])('preserves deep link %s', (path) => {
    expect(
      resolveAuthedLandingPath({
        apex: true,
        workspaces: ws,
        activeWorkspaceId: 'b',
        currentPath: path,
      }),
    ).toBe(path)
  })

  it('does NOT treat /w/ or root as a preservable deep link', () => {
    expect(
      resolveAuthedLandingPath({
        apex: false,
        workspaces: ws,
        activeWorkspaceId: 'b',
        currentPath: '/',
      }),
    ).toBe('/w/b')
  })

  it('does not preserve a lookalike prefix (/playgrounds)', () => {
    expect(
      resolveAuthedLandingPath({
        apex: false,
        workspaces: ws,
        activeWorkspaceId: 'a',
        currentPath: '/playgrounds-fake',
      }),
    ).toBe('/w/a')
  })
})
