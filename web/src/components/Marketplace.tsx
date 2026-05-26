import { useEffect, useState } from 'react'
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
  const [skills, setSkills] = useState<MarketplaceSkillSummary[]>([])
  const [souls, setSouls] = useState<MarketplaceSoulSummary[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)

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

  const handleForkSkill = async (id: string, name: string) => {
    setBusy(true)
    setSuccess(null)
    setError(null)
    try {
      // empty workspace_id → server resolves to caller's default workspace
      await forkMarketplaceSkill(id, '')
      setSuccess(`Skill "${name}" forked to your workspace.`)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'fork failed')
    } finally {
      setBusy(false)
    }
  }

  const handleForkSoul = async (id: string, name: string) => {
    setBusy(true)
    setSuccess(null)
    setError(null)
    try {
      await forkMarketplaceSoul(id, '')
      setSuccess(`Soul "${name}" forked to your workspace.`)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'fork failed')
    } finally {
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
      {success && (
        <div className="mb-4 rounded-md border border-green-500/30 bg-green-500/10 px-3 py-2 text-sm text-green-400">{success}</div>
      )}

      <MarketplaceSection
        title="Skills"
        items={skills.map((s) => ({
          id: s.id,
          name: s.name,
          description: s.description,
          updatedAt: s.updated_at,
        }))}
        busy={busy}
        onFork={(id, name) => handleForkSkill(id, name)}
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
          }))}
          busy={busy}
          onFork={(id, name) => handleForkSoul(id, name)}
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
  onFork: (id: string, name: string) => void
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
            <div key={it.id} className="flex items-center gap-3 px-5 py-3 hover:bg-[var(--secondary)]/30">
              <div className="flex-1 min-w-0">
                <span className="text-sm font-medium text-[var(--foreground)]">{it.name}</span>
                <div className="text-xs text-[var(--muted-foreground)] truncate">
                  {it.description || 'No description'} · updated {new Date(it.updatedAt).toLocaleString()}
                </div>
              </div>
              <button
                onClick={() => onFork(it.id, it.name)}
                disabled={busy}
                className="inline-flex items-center gap-1.5 rounded-md border border-orange-500/30 bg-orange-500/10 px-3 py-1 text-xs font-medium text-orange-400 hover:bg-orange-500/20 transition-colors disabled:opacity-50"
                title="Fork to my workspace"
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
