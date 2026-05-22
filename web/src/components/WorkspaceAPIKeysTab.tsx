import { useState, useEffect, useCallback } from 'react'
import { Key, Plus, Trash2 } from 'lucide-react'
import {
  listWorkspaceAPIKeys,
  revokeWorkspaceAPIKey,
  type WorkspaceAPIKey,
} from '../lib/api'
import { MintAPIKeyModal } from './MintAPIKeyModal'

function formatExpiresAt(expiresAt: string): { label: string; className: string } {
  const now = Date.now()
  const exp = new Date(expiresAt).getTime()
  const diffMs = exp - now
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))
  if (diffMs <= 0) {
    return { label: 'expired', className: 'text-red-400' }
  }
  if (diffDays <= 7) {
    return { label: `expires in ${diffDays}d`, className: 'text-amber-400' }
  }
  return { label: `expires in ${diffDays}d`, className: 'text-[var(--muted-foreground)]' }
}

interface WorkspaceAPIKeysTabProps {
  workspaceId: string
}

export function WorkspaceAPIKeysTab({ workspaceId }: WorkspaceAPIKeysTabProps) {
  const [keys, setKeys] = useState<WorkspaceAPIKey[]>([])
  const [loading, setLoading] = useState(true)
  const [showMint, setShowMint] = useState(false)

  const loadKeys = useCallback(() => {
    setLoading(true)
    listWorkspaceAPIKeys(workspaceId)
      // Revoked keys are dead weight — hide them from the list. The DB row
      // stays (audit trail / soft-delete semantics); the UI just doesn't
      // surface them. To resurrect a key, the user mints a new one.
      .then((all) => setKeys(all.filter((k) => !k.revoked_at)))
      .catch(() => setKeys([]))
      .finally(() => setLoading(false))
  }, [workspaceId])

  useEffect(() => { loadKeys() }, [loadKeys])

  const handleRevoke = async (key: WorkspaceAPIKey) => {
    if (!window.confirm(`Revoke the API key "${key.name}"? This cannot be undone — any integrations using this key will stop working immediately.`)) {
      return
    }
    await revokeWorkspaceAPIKey(workspaceId, key.id)
    loadKeys()
  }

  return (
    <>
      <div className="rounded-lg border border-[var(--border)] bg-[var(--card)]">
        <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3">
          <div className="flex items-center gap-2">
            <Key size={14} className="text-[var(--muted-foreground)]" />
            <span className="text-sm font-medium text-[var(--foreground)]">API Keys</span>
            {keys.length > 0 && (
              <span className="rounded-full bg-[var(--secondary)] px-2 py-0.5 text-[10px] text-[var(--muted-foreground)]">
                {keys.length}
              </span>
            )}
          </div>
          <button
            onClick={() => setShowMint(true)}
            className="inline-flex items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--card)] px-3 py-1.5 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--secondary)] transition-colors"
          >
            <Plus size={13} />
            Create new key
          </button>
        </div>

        <div className="px-5 py-4">
          {loading ? (
            <p className="text-sm text-[var(--muted-foreground)]">Loading...</p>
          ) : keys.length === 0 ? (
            <div className="rounded-md border border-dashed border-[var(--border)] py-8 text-center text-xs italic text-[var(--muted-foreground)]">
              No API keys yet. Create one to integrate with /api/turns.
            </div>
          ) : (
            <div className="overflow-hidden rounded-md border border-[var(--border)]">
              <table className="w-full border-collapse text-sm">
                <thead className="bg-[var(--secondary)] text-[var(--muted-foreground)]">
                  <tr>
                    <th className="px-3 py-2 text-left font-medium">Name</th>
                    <th className="px-3 py-2 text-left font-medium">Scopes</th>
                    <th className="w-36 px-3 py-2 text-left font-medium">Created</th>
                    <th className="w-36 px-3 py-2 text-left font-medium">Expires</th>
                    <th className="w-36 px-3 py-2 text-left font-medium">Last used</th>
                    <th className="w-20 px-3 py-2 text-left font-medium">Status</th>
                    <th className="w-16 px-3 py-2 text-right font-medium">Action</th>
                  </tr>
                </thead>
                <tbody>
                  {keys.map((k, i) => {
                    const isRevoked = !!k.revoked_at
                    return (
                      <tr
                        key={k.id}
                        className={`border-t border-[var(--border)] ${i % 2 === 1 ? 'bg-[var(--background)]/40' : ''} ${isRevoked ? 'opacity-60' : ''}`}
                      >
                        <td className="px-3 py-2 text-[var(--foreground)]">{k.name}</td>
                        <td className="px-3 py-2">
                          {k.scopes && k.scopes.length > 0 ? (
                            <div className="flex flex-wrap gap-1">
                              {k.scopes.map((s) => (
                                <span
                                  key={s}
                                  className="rounded bg-[var(--secondary)] px-1.5 py-0.5 text-[10px] font-mono text-[var(--muted-foreground)]"
                                >
                                  {s}
                                </span>
                              ))}
                            </div>
                          ) : (
                            <span className="text-[var(--muted-foreground)]">—</span>
                          )}
                        </td>
                        <td className="px-3 py-2 text-[11px] text-[var(--muted-foreground)]">
                          {new Date(k.created_at).toLocaleString()}
                        </td>
                        <td className="px-3 py-2 text-[11px]">
                          {k.expires_at ? (() => {
                            const { label, className } = formatExpiresAt(k.expires_at)
                            return <span className={className}>{label}</span>
                          })() : '—'}
                        </td>
                        <td className="px-3 py-2 text-[11px] text-[var(--muted-foreground)]">
                          {k.last_used_at ? new Date(k.last_used_at).toLocaleString() : '—'}
                        </td>
                        <td className="px-3 py-2">
                          {isRevoked ? (
                            <span className="inline-flex items-center gap-1 rounded-full border border-gray-500/20 bg-gray-500/10 px-2 py-0.5 text-[10px] font-medium text-[var(--muted-foreground)]">
                              Revoked
                            </span>
                          ) : (
                            <span className="inline-flex items-center gap-1 rounded-full border border-green-500/20 bg-green-500/10 px-2 py-0.5 text-[10px] font-medium text-green-400">
                              <span className="h-1.5 w-1.5 rounded-full bg-green-400" />
                              Active
                            </span>
                          )}
                        </td>
                        <td className="px-3 py-2 text-right">
                          {!isRevoked && (
                            <button
                              onClick={() => handleRevoke(k)}
                              className="rounded p-1 text-[var(--muted-foreground)] hover:bg-red-500/10 hover:text-red-400 transition-colors"
                              title="Revoke key"
                            >
                              <Trash2 size={13} />
                            </button>
                          )}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {showMint && (
        <MintAPIKeyModal
          workspaceId={workspaceId}
          onClose={() => setShowMint(false)}
          onCreated={() => {
            setShowMint(false)
            loadKeys()
          }}
        />
      )}
    </>
  )
}
