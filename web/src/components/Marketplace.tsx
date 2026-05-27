import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { GitFork } from 'lucide-react'
import {
  listMarketplaceSkills,
  listMarketplaceSouls,
  forkMarketplaceSkill,
  forkMarketplaceSoul,
  type MarketplaceSkillSummary,
  type MarketplaceSoulSummary,
} from '../lib/api'

export function Marketplace() {
  const navigate = useNavigate()
  const [skills, setSkills] = useState<MarketplaceSkillSummary[]>([])
  const [souls, setSouls] = useState<MarketplaceSoulSummary[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const reload = async () => {
    try {
      const [sk, so] = await Promise.all([listMarketplaceSkills(), listMarketplaceSouls()])
      setSkills(sk)
      setSouls(so)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load')
    }
  }

  useEffect(() => {
    reload()
  }, [])

  const handleForkSkill = async (id: string) => {
    setBusy(true)
    setError(null)
    try {
      // empty workspace_id → server resolves to caller's default workspace
      const forked = await forkMarketplaceSkill(id, '')
      navigate(`/playground/skills/${encodeURIComponent(forked.id)}`)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'fork failed')
      setBusy(false)
    }
  }

  const handleForkSoul = async (id: string) => {
    setBusy(true)
    setError(null)
    try {
      const forked = await forkMarketplaceSoul(id, '')
      navigate(`/playground/souls/${encodeURIComponent(forked.id)}`)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'fork failed')
      setBusy(false)
    }
  }

  return (
    <div className="p-6 max-w-5xl">
      <div className="mb-6">
        <h1 className="text-xl font-semibold text-[var(--foreground)]">Marketplace</h1>
        <p className="mt-1 text-sm text-[var(--muted-foreground)]">
          Shared skill and soul templates. Fork any entry to your workspace to customize it.
        </p>
      </div>

      {error && (
        <div className="mb-4 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">{error}</div>
      )}
      <MarketplaceSection
        title="Skills"
        items={skills.map((s) => ({
          id: s.id,
          name: s.name,
          description: s.description,
          updatedAt: s.updated_at,
          authorWorkspaceID: s.author_workspace_id,
          tags: s.tags,
        }))}
        busy={busy}
        onFork={(id) => handleForkSkill(id)}
        emptyLabel="No shared skills yet."
      />

      <div className="mt-8">
        <MarketplaceSection
          title="Souls"
          items={souls.map((s) => ({
            id: s.id,
            name: s.name,
            description: s.description,
            updatedAt: s.updated_at,
            authorWorkspaceID: s.author_workspace_id,
            compatibleSkills: s.compatible_skills,
          }))}
          busy={busy}
          onFork={(id) => handleForkSoul(id)}
          emptyLabel="No shared souls yet."
        />
      </div>
    </div>
  )
}

interface MarketplaceItem {
  id: string
  name: string
  description: string
  updatedAt: string
  authorWorkspaceID?: string
  tags?: string[]
  compatibleSkills?: string[]
}

function MarketplaceSection({
  title,
  items,
  busy,
  onFork,
  emptyLabel,
}: {
  title: string
  items: MarketplaceItem[]
  busy: boolean
  onFork: (id: string) => void
  emptyLabel: string
}) {
  return (
    <div className="rounded-lg border border-[var(--border)] bg-[var(--card)]">
      <div className="border-b border-[var(--border)] px-5 py-3">
        <span className="text-sm font-medium text-[var(--foreground)]">
          {title} <span className="text-[var(--muted-foreground)]">({items.length})</span>
        </span>
      </div>
      <div className="divide-y divide-[var(--border)]">
        {items.length === 0 ? (
          <div className="py-8 text-center text-xs italic text-[var(--muted-foreground)]">{emptyLabel}</div>
        ) : (
          items.map((it) => (
            <div key={it.id} className="flex items-start gap-3 px-5 py-3 hover:bg-[var(--secondary)]/30">
              <div className="flex-1 min-w-0">
                <span className="text-sm font-medium text-[var(--foreground)]">{it.name}</span>
                <div className="text-xs text-[var(--muted-foreground)] truncate">
                  {it.description || 'No description'}
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[10px] text-[var(--muted-foreground)]">
                  <span>updated {new Date(it.updatedAt).toLocaleString()}</span>
                  {it.authorWorkspaceID && (
                    <span className="rounded bg-[var(--secondary)] px-1.5 py-0.5">
                      from ws {it.authorWorkspaceID.slice(0, 8)}…
                    </span>
                  )}
                  {it.tags?.map((tag) => (
                    <span key={tag} className="rounded border border-[var(--border)] px-1.5 py-0.5">
                      {tag}
                    </span>
                  ))}
                  {it.compatibleSkills?.map((sk) => (
                    <span key={sk} className="rounded border border-orange-500/30 bg-orange-500/5 px-1.5 py-0.5 text-orange-400/90">
                      +{sk}
                    </span>
                  ))}
                </div>
              </div>
              <button
                onClick={() => onFork(it.id)}
                disabled={busy}
                className="shrink-0 inline-flex items-center gap-1.5 rounded-md border border-orange-500/30 bg-orange-500/10 px-3 py-1 text-xs font-medium text-orange-400 hover:bg-orange-500/20 transition-colors disabled:opacity-50"
                title="Fork to my workspace and open in Playground"
              >
                <GitFork size={12} /> Fork
              </button>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
