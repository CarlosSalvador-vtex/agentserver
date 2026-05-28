/** Apex / dev hosts where no workspace slug is inferred from the URL. */
const ROOT_HOSTS = new Set([
  'agentserver.analytics.vtex.com',
  'localhost',
  '127.0.0.1',
])

/**
 * Returns the workspace slug from a tenant subdomain (first label), or ""
 * on apex, sandbox (claw-*, hermes-*), or bare localhost.
 */
export function extractWorkspaceSlug(host: string): string {
  const bare = host.split(':')[0]
  if (ROOT_HOSTS.has(bare)) return ''
  if (!bare.includes('.')) return ''
  const first = bare.split('.')[0]
  if (first.startsWith('claw-') || first.startsWith('hermes-')) return ''
  return first
}

/** True when the UI is served on a workspace tenant subdomain. */
export function isTenantSubdomain(host: string = window.location.hostname): boolean {
  return extractWorkspaceSlug(host) !== ''
}

/** Client-side slug preview (mirrors server Slugify). */
export function slugifyName(name: string): string {
  const s = name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return s || 'workspace'
}

/** True on apex / marketing host (no workspace slug in the hostname). */
export function isApexHost(host: string = window.location.hostname): boolean {
  return extractWorkspaceSlug(host) === ''
}

function inferProtocolForHost(hostWithPort: string): string {
  const bare = hostWithPort.split(':')[0]
  if (bare === 'localhost' || bare === '127.0.0.1') return 'http:'
  return 'https:'
}

/**
 * Tenant login URL on the current browser host (protocol + host from window.location unless overridden).
 * Example: slug `acme` on `agentserver.analytics.vtex.com` → `https://acme.agentserver.analytics.vtex.com/login`
 */
export function buildTenantLoginUrl(workspaceSlug: string, host?: string): string {
  const slug = workspaceSlug.trim().toLowerCase()
  if (!slug) {
    throw new Error('workspace slug is required')
  }
  const resolvedHost = host ?? window.location.host
  const protocol = host ? inferProtocolForHost(resolvedHost) : window.location.protocol
  return `${protocol}//${slug}.${resolvedHost}/login`
}
