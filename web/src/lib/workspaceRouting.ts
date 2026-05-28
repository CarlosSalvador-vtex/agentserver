// Non-workspace app routes that are valid deep links. On a cold load /
// reload of one of these, the authed-landing logic must PRESERVE the path
// instead of bouncing the user to /w/<id> — otherwise direct links to the
// playground editor, marketplace, admin, etc. never render (the auth effect
// fires navigate(landing) and overrides the URL the user actually opened).
const DEEP_LINK_PREFIXES = [
  '/playground',
  '/marketplace',
  '/admin',
  '/workspaces',
  '/oauth2',
]

function isPreservableDeepLink(path: string): boolean {
  return DEEP_LINK_PREFIXES.some((p) => path === p || path.startsWith(p + '/'))
}

export function resolveAuthedLandingPath(opts: {
  apex: boolean
  workspaces: { id: string }[]
  activeWorkspaceId?: string | null
  urlWorkspaceId?: string | null
  currentPath?: string | null
}): string {
  const { apex, workspaces, activeWorkspaceId, urlWorkspaceId, currentPath } = opts

  // Preserve a deep link the user explicitly opened (cold load / reload).
  if (currentPath && isPreservableDeepLink(currentPath)) {
    return currentPath
  }

  // Last-used workspace (active) wins, on apex or tenant host.
  if (urlWorkspaceId && workspaces.some((w) => w.id === urlWorkspaceId)) {
    return `/w/${urlWorkspaceId}`
  }
  if (activeWorkspaceId && workspaces.some((w) => w.id === activeWorkspaceId)) {
    return `/w/${activeWorkspaceId}`
  }
  // No active workspace: land on the first one and let the user switch via the
  // topbar dropdown — do NOT force the full-page picker. The picker stays a
  // fallback only when the user belongs to no workspace, and remains reachable
  // at /choose-workspace as an explicit menu action.
  if (workspaces.length > 0) {
    return `/w/${workspaces[0].id}`
  }
  return apex ? '/choose-workspace' : '/'
}
