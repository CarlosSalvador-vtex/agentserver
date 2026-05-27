// Tier B item B2 — read-only marketplace preview.
//
// Lets authors inspect description, file inventory, and a bounded
// excerpt of the prompt body (skills) or soul body (souls) before
// committing to fork. Reduces "fork blind" risk.

import { useEffect, useState } from 'react'
import { X, GitFork, FileText } from 'lucide-react'
import {
  getMarketplaceSkillPreview,
  getMarketplaceSoulPreview,
  type MarketplaceSkillPreview,
  type MarketplaceSoulPreview,
} from '../lib/api'

interface Props {
  kind: 'skill' | 'soul'
  id: string
  name: string
  busy: boolean
  onFork: () => void
  onClose: () => void
}

export function MarketplacePreviewModal({ kind, id, name, busy, onFork, onClose }: Props) {
  const [skill, setSkill] = useState<MarketplaceSkillPreview | null>(null)
  const [soul, setSoul] = useState<MarketplaceSoulPreview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    const p =
      kind === 'skill' ? getMarketplaceSkillPreview(id) : getMarketplaceSoulPreview(id)
    p.then((d) => {
      if (cancelled) return
      if (kind === 'skill') setSkill(d as MarketplaceSkillPreview)
      else setSoul(d as MarketplaceSoulPreview)
    })
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : String(err)))
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [kind, id])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="flex w-full max-w-2xl max-h-[80vh] flex-col rounded-lg border border-[var(--border)] bg-[var(--card)] shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--border)] p-4">
          <div>
            <h2 className="text-lg font-semibold text-[var(--foreground)]">{name}</h2>
            <p className="text-xs text-[var(--muted-foreground)]">
              {kind === 'skill' ? 'Skill template' : 'Soul template'} preview
            </p>
          </div>
          <button onClick={onClose} className="rounded p-1 hover:bg-[var(--secondary)]">
            <X size={16} />
          </button>
        </div>

        <div className="flex-1 overflow-auto p-4 text-sm">
          {loading && <p className="text-[var(--muted-foreground)]">Loading…</p>}
          {error && <p className="text-red-500">Failed: {error}</p>}

          {skill && (
            <div className="flex flex-col gap-3">
              {skill.description && (
                <p className="text-[var(--foreground)]">{skill.description}</p>
              )}
              <Metadata
                fields={[
                  ['Updated', skill.updated_at],
                  ['Author workspace', skill.author_workspace_id],
                  ['Promoted commit', skill.promoted_commit?.slice(0, 7)],
                  ['Tags', skill.tags?.join(', ')],
                ]}
              />
              {skill.file_list && Object.keys(skill.file_list).length > 0 && (
                <section>
                  <h3 className="mb-1 flex items-center gap-1 text-xs font-medium uppercase tracking-wide text-[var(--muted-foreground)]">
                    <FileText size={11} /> Files
                  </h3>
                  <ul className="rounded border border-[var(--border)] text-xs">
                    {Object.entries(skill.file_list).map(([path, size]) => (
                      <li key={path} className="flex items-center justify-between border-b border-[var(--border)] px-2 py-1 last:border-0">
                        <code>{path}</code>
                        <span className="text-[var(--muted-foreground)]">{size} B</span>
                      </li>
                    ))}
                  </ul>
                </section>
              )}
              {skill.prompt_excerpt && (
                <section>
                  <h3 className="mb-1 text-xs font-medium uppercase tracking-wide text-[var(--muted-foreground)]">
                    prompt.md (excerpt)
                  </h3>
                  <pre className="max-h-72 overflow-auto rounded border border-[var(--border)] bg-[var(--background)] p-2 text-xs leading-relaxed whitespace-pre-wrap">
                    {skill.prompt_excerpt}
                  </pre>
                </section>
              )}
            </div>
          )}

          {soul && (
            <div className="flex flex-col gap-3">
              {soul.description && (
                <p className="text-[var(--foreground)]">{soul.description}</p>
              )}
              <Metadata
                fields={[
                  ['Updated', soul.updated_at],
                  ['Author workspace', soul.author_workspace_id],
                  ['Schema', soul.schema_version],
                  ['Promoted commit', soul.promoted_commit?.slice(0, 7)],
                  ['Compatible skills', soul.compatible_skills?.join(', ')],
                ]}
              />
              {soul.body_excerpt && (
                <section>
                  <h3 className="mb-1 text-xs font-medium uppercase tracking-wide text-[var(--muted-foreground)]">
                    Soul body (excerpt)
                  </h3>
                  <pre className="max-h-72 overflow-auto rounded border border-[var(--border)] bg-[var(--background)] p-2 text-xs leading-relaxed whitespace-pre-wrap">
                    {soul.body_excerpt}
                  </pre>
                </section>
              )}
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-[var(--border)] p-3">
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border border-[var(--border)] px-3 py-1.5 text-xs hover:bg-[var(--secondary)]"
          >
            Close
          </button>
          <button
            type="button"
            onClick={onFork}
            disabled={busy || loading || !!error}
            className="inline-flex items-center gap-1.5 rounded-md border border-orange-500/30 bg-orange-500/10 px-3 py-1.5 text-xs font-medium text-orange-400 hover:bg-orange-500/20 disabled:opacity-50"
          >
            <GitFork size={12} /> {busy ? 'Forking…' : 'Fork to my workspace'}
          </button>
        </div>
      </div>
    </div>
  )
}

function Metadata({ fields }: { fields: [string, string | undefined][] }) {
  const visible = fields.filter(([, v]) => v && v.length > 0)
  if (visible.length === 0) return null
  return (
    <dl className="grid grid-cols-[120px_1fr] gap-x-3 gap-y-1 text-xs">
      {visible.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-[var(--muted-foreground)]">{k}</dt>
          <dd className="font-mono text-[var(--foreground)]">{v}</dd>
        </div>
      ))}
    </dl>
  )
}
