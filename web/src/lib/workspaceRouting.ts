export function resolveAuthedLandingPath(opts: {
  apex: boolean
  workspaces: { id: string }[]
  activeWorkspaceId?: string | null
  urlWorkspaceId?: string | null
}): string {
  const { apex, workspaces, activeWorkspaceId, urlWorkspaceId } = opts

  if (urlWorkspaceId && workspaces.some((w) => w.id === urlWorkspaceId)) {
    return `/w/${urlWorkspaceId}`
  }
  if (activeWorkspaceId && workspaces.some((w) => w.id === activeWorkspaceId)) {
    return `/w/${activeWorkspaceId}`
  }
  if (apex && !activeWorkspaceId) {
    return '/choose-workspace'
  }
  if (workspaces.length > 0) {
    return `/w/${workspaces[0].id}`
  }
  return apex ? '/choose-workspace' : '/'
}
