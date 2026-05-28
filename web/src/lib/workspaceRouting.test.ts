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
})
