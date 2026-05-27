// Tier B item B6 — explicit workspace picker on draft create.
//
// Replaces the bare prompt() in Playground.tsx so authors must consciously
// pick the workspace when they belong to more than one. Single-workspace
// users skip the picker (default auto-selected).

import { useEffect, useState, type FormEvent } from 'react'
import { X } from 'lucide-react'
import { listWorkspaces, type Workspace } from '../lib/api'

interface Props {
  kind: 'skill' | 'soul'
  onCancel: () => void
  onSubmit: (name: string, workspaceId: string) => Promise<void>
}

export function CreateDraftModal({ kind, onCancel, onSubmit }: Props) {
  const [name, setName] = useState('')
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [workspaceId, setWorkspaceId] = useState('')
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    listWorkspaces()
      .then((ws) => {
        if (cancelled) return
        setWorkspaces(ws)
        if (ws.length > 0) setWorkspaceId(ws[0].id)
      })
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : String(err)))
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await onSubmit(name.trim(), workspaceId)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setSubmitting(false)
    }
  }

  const slugLike = /^[a-z0-9][a-z0-9-]*$/.test(name)
  const canSubmit = !submitting && !loading && slugLike && !!workspaceId

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onCancel}>
      <div
        className="w-full max-w-sm rounded-lg border border-[var(--border)] bg-[var(--card)] p-6 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold text-[var(--foreground)]">New {kind} draft</h2>
          <button onClick={onCancel} className="rounded p-1 hover:bg-[var(--secondary)]">
            <X size={16} />
          </button>
        </div>
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <div>
            <label className="mb-1 block text-sm font-medium text-[var(--foreground)]">Name</label>
            <input
              type="text"
              required
              value={name}
              onChange={(e) => setName(e.target.value.toLowerCase())}
              className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 font-mono text-sm"
              placeholder="lowercase-kebab-case"
              autoFocus
            />
            {name.length > 0 && !slugLike && (
              <p className="mt-1 text-xs text-red-500">
                Use lowercase letters, digits, and hyphens (no leading hyphen).
              </p>
            )}
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-[var(--foreground)]">Workspace</label>
            {loading ? (
              <div className="text-xs text-[var(--muted-foreground)]">Loading workspaces…</div>
            ) : workspaces.length === 0 ? (
              <div className="text-xs text-red-500">No workspaces available — create one first.</div>
            ) : workspaces.length === 1 ? (
              <div className="rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm">
                {workspaces[0].name}
              </div>
            ) : (
              <select
                value={workspaceId}
                onChange={(e) => setWorkspaceId(e.target.value)}
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm"
              >
                {workspaces.map((w) => (
                  <option key={w.id} value={w.id}>
                    {w.name}
                  </option>
                ))}
              </select>
            )}
            <p className="mt-1 text-xs text-[var(--muted-foreground)]">
              The draft belongs to this workspace. You can later toggle marketplace visibility.
            </p>
          </div>
          {error && <p className="text-sm text-red-500">{error}</p>}
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={onCancel}
              className="rounded-md border border-[var(--border)] px-4 py-2 text-sm hover:bg-[var(--secondary)]"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!canSubmit}
              className="rounded-md bg-[var(--primary)] px-4 py-2 text-sm font-medium text-white hover:opacity-90 disabled:opacity-50"
            >
              {submitting ? 'Creating…' : 'Create draft'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
