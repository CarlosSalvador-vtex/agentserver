import { useCallback, useEffect, useState } from 'react'
import { ExternalLink, Plus } from 'lucide-react'
import {
  createWorkspace,
  getMe,
  listMembers,
  listWorkspaces,
  type Workspace,
} from '../lib/api'
import { buildTenantLoginUrl } from '../lib/hostname'
import { CreateWorkspaceModal } from './CreateWorkspaceModal'

type WorkspaceRow = Workspace & { role: string }

function sortWorkspaces(rows: WorkspaceRow[]): WorkspaceRow[] {
  return [...rows].sort((a, b) => {
    const ta = new Date(a.updated_at).getTime()
    const tb = new Date(b.updated_at).getTime()
    if (tb !== ta) return tb - ta
    return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })
  })
}

async function loadWorkspaceRows(): Promise<WorkspaceRow[]> {
  const [workspaces, me] = await Promise.all([listWorkspaces(), getMe()])
  const rows = await Promise.all(
    workspaces.map(async (ws) => {
      try {
        const members = await listMembers(ws.id)
        const mine = members.find((m) => m.user_id === me.id)
        return { ...ws, role: mine?.role ?? 'member' }
      } catch {
        return { ...ws, role: 'member' }
      }
    }),
  )
  return rows
}

export function ChooseWorkspace() {
  const [rows, setRows] = useState<WorkspaceRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)

  const refresh = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const next = sortWorkspaces(await loadWorkspaceRows())
      setRows(next)
      if (next.length === 1) {
        window.location.assign(buildTenantLoginUrl(next[0].slug))
        return
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(msg || 'Failed to load workspaces')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const handleOpen = (slug: string) => {
    window.location.assign(buildTenantLoginUrl(slug))
  }

  const handleCreate = async (name: string, slug: string) => {
    setCreating(true)
    setError(null)
    try {
      const ws = await createWorkspace(name, slug)
      setShowCreate(false)
      window.location.assign(buildTenantLoginUrl(ws.slug))
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(msg || 'Failed to create workspace')
      setCreating(false)
    }
  }

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--background)]">
        <p className="text-sm text-[var(--muted-foreground)]">Loading workspaces…</p>
      </div>
    )
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-[var(--background)] px-4 py-10">
      <div className="w-full max-w-lg rounded-lg border border-[var(--border)] bg-[var(--card)] p-6 shadow-sm">
        <h1 className="text-xl font-semibold text-[var(--foreground)]">Choose a workspace</h1>
        <p className="mt-1 text-sm text-[var(--muted-foreground)]">
          Open a workspace to sign in on its subdomain. You will log in again there with that workspace
          context.
        </p>

        {error && (
          <p className="mt-4 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 text-sm text-red-600 dark:text-red-400">
            {error}
          </p>
        )}

        {rows.length === 0 ? (
          <div className="mt-6 space-y-4">
            <p className="text-sm text-[var(--muted-foreground)]">
              You are not a member of any workspace yet. Create one to get started.
            </p>
            <button
              type="button"
              onClick={() => setShowCreate(true)}
              className="inline-flex items-center gap-2 rounded-md bg-[var(--primary)] px-4 py-2 text-sm font-medium text-white hover:opacity-90"
            >
              <Plus size={16} />
              Create workspace
            </button>
          </div>
        ) : (
          <ul className="mt-6 divide-y divide-[var(--border)] rounded-md border border-[var(--border)]">
            {rows.map((ws) => (
              <li key={ws.id} className="flex items-center justify-between gap-3 px-3 py-3">
                <div className="min-w-0">
                  <p className="truncate font-medium text-[var(--foreground)]">{ws.name}</p>
                  <p className="truncate font-mono text-xs text-[var(--muted-foreground)]">{ws.slug}</p>
                  <p className="mt-0.5 text-xs capitalize text-[var(--muted-foreground)]">{ws.role}</p>
                </div>
                <button
                  type="button"
                  onClick={() => handleOpen(ws.slug)}
                  className="inline-flex shrink-0 items-center gap-1.5 rounded-md border border-[var(--border)] px-3 py-1.5 text-sm hover:bg-[var(--secondary)]"
                >
                  <ExternalLink size={14} />
                  Open
                </button>
              </li>
            ))}
          </ul>
        )}

        {rows.length > 0 && (
          <div className="mt-4">
            <button
              type="button"
              onClick={() => setShowCreate(true)}
              className="text-sm text-[var(--primary)] hover:underline"
            >
              Create another workspace
            </button>
          </div>
        )}
      </div>

      {showCreate && (
        <CreateWorkspaceModal
          onConfirm={(name, slug) => {
            void handleCreate(name, slug)
          }}
          onCancel={() => !creating && setShowCreate(false)}
        />
      )}
    </div>
  )
}
