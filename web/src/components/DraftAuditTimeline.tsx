import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { listDraftAudit, type DraftAuditEvent } from '../lib/api'

interface DraftAuditTimelineProps {
  kind: 'skills' | 'souls'
  draftID: string
}

// Sprint 3 PR-3 (improvements.md #14). Lazy-loads the per-draft audit log
// and renders it as a vertical timeline. Mounted from the editor on tab
// switch so the API call happens on demand, not on every editor visit.

export function DraftAuditTimeline({ kind, draftID }: DraftAuditTimelineProps) {
  const [events, setEvents] = useState<DraftAuditEvent[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    listDraftAudit(kind, draftID)
      .then((evts) => {
        if (!cancelled) setEvents(evts)
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [kind, draftID])

  if (loading) {
    return (
      <div className="flex items-center gap-2 p-4 text-xs text-[var(--muted-foreground)]">
        <Loader2 size={14} className="animate-spin" /> Loading audit timeline…
      </div>
    )
  }
  if (error) {
    return <div className="p-4 text-xs text-red-400">Audit load failed: {error}</div>
  }
  if (!events || events.length === 0) {
    return <div className="p-4 text-xs italic text-[var(--muted-foreground)]">No audit events yet.</div>
  }

  return (
    <ol className="flex flex-col gap-2 p-4 text-xs">
      {events.map((e) => (
        <li key={e.id} className="rounded border border-[var(--border)] bg-[var(--card)]/30 px-3 py-2">
          <div className="flex items-center gap-2">
            <ActionBadge action={e.action} />
            <span className="text-[var(--foreground)] font-medium">{e.actor_user_id || '<system>'}</span>
            <span className="ml-auto text-[10px] text-[var(--muted-foreground)]">
              {new Date(e.created_at).toLocaleString()}
            </span>
          </div>
          {e.payload_diff && Object.keys(e.payload_diff).length > 0 && (
            <pre className="mt-1 overflow-x-auto rounded bg-[var(--background)] p-2 text-[10px] font-mono text-[var(--muted-foreground)]">
              {JSON.stringify(e.payload_diff, null, 2)}
            </pre>
          )}
        </li>
      ))}
    </ol>
  )
}

function ActionBadge({ action }: { action: string }) {
  const styles: Record<string, string> = {
    created: 'bg-blue-500/10 text-blue-400',
    patched: 'bg-yellow-500/10 text-yellow-400',
    archived: 'bg-gray-500/10 text-gray-400',
    promoted: 'bg-emerald-500/20 text-emerald-300',
  }
  return (
    <span className={`inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium ${styles[action] ?? 'bg-gray-500/10 text-gray-400'}`}>
      {action}
    </span>
  )
}
