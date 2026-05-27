import { useEffect, useMemo, useState } from 'react'
import { GitFork, Search } from 'lucide-react'
import {
  listMarketplaceSkills,
  listMarketplaceSouls,
  forkMarketplaceSkill,
  forkMarketplaceSoul,
  type MarketplaceSkillSummary,
  type MarketplaceSoulSummary,
} from '../lib/api'

type SortKey = 'updated_desc' | 'updated_asc' | 'name_asc' | 'name_desc'

function sortItems<T extends { name: string; updated_at: string }>(items: T[], sort: SortKey): T[] {
  const copy = [...items]
  switch (sort) {
    case 'name_asc':
      return copy.sort((a, b) => a.name.localeCompare(b.name))
    case 'name_desc':
      return copy.sort((a, b) => b.name.localeCompare(a.name))
    case 'updated_asc':
      return copy.sort((a, b) => new Date(a.updated_at).getTime() - new Date(b.updated_at).getTime())
    default:
      return copy.sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
  }
}

function matchesQuery(name: string, description: string, q: string): boolean {
  const needle = q.trim().toLowerCase()
  if (!needle) return true
  return name.toLowerCase().includes(needle) || description.toLowerCase().includes(needle)
}

export function Marketplace() {
  const [skills, setSkills] = useState<MarketplaceSkillSummary[]>([])
  const [souls, setSouls] = useState<MarketplaceSoulSummary[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<SortKey>('updated_desc')

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

  const filteredSkills = useMemo(
    () => sortItems(skills.filter((s) => matchesQuery(s.name, s.description ?? '', search)), sort),
    [skills, search, sort],
  )
  const filteredSouls = useMemo(
    () => sortItems(souls.filter((s) => matchesQuery(s.name, s.description ?? '', search)), sort),
    [souls, search, sort],
  )

  const handleForkSkill = async (id: string, name: string) => {
    setBusy(true)
    setSuccess(null)
    setError(null)
    try {
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

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[200px]">
          <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--muted-foreground)]" />
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search by name or description…"
            className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] py-1.5 pl-8 pr-3 text-sm text-[var(--foreground)] placeholder:text-[var(--muted-foreground)]"
          />
        </div>
        <select
          value={sort}
          onChange={(e) => setSort(e.target.value as SortKey)}
          className="rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-1.5 text-sm text-[var(--foreground)]"
          aria-label="Sort marketplace entries"
        >
          <option value="updated_desc">Recently updated</option>
          <option value="updated_asc">Oldest updated</option>
          <option value="name_asc">Name A–Z</option>
          <option value="name_desc">Name Z–A</option>
        </select>
      </div>

      {error && (
        <div className="mb-4 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">{error}</div>
      )}
      {success && (
        <div className="mb-4 rounded-md border border-green-500/30 bg-green-500/10 px-3 py-2 text-sm text-green-400">{success}</div>
      )}

      <MarketplaceSection
        title="Skills"
        items={filteredSkills.map((s) => ({
          id: s.id,
          name: s.name,
          description: s.description,
          updatedAt: s.updated_at,
        }))}
        totalCount={skills.length}
        busy={busy}
        onFork={(id, name) => handleForkSkill(id, name)}
        emptyLabel={search.trim() ? 'No skills match your search.' : 'No shared skills yet.'}
      />

      <div className="mt-8">
        <MarketplaceSection
          title="Souls"
          items={filteredSouls.map((s) => ({
            id: s.id,
            name: s.name,
            description: s.description,
            updatedAt: s.updated_at,
          }))}
          totalCount={souls.length}
          busy={busy}
          onFork={(id, name) => handleForkSoul(id, name)}
          emptyLabel={search.trim() ? 'No souls match your search.' : 'No shared souls yet.'}
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
  totalCount,
  busy,
  onFork,
  emptyLabel,
}: {
  title: string
  items: MarketplaceItem[]
  totalCount: number
  busy: boolean
  onFork: (id: string, name: string) => void
  emptyLabel: string
}) {
  const countLabel =
    items.length === totalCount ? `${items.length}` : `${items.length} of ${totalCount}`

  return (
    <div className="rounded-lg border border-[var(--border)] bg-[var(--card)]">
      <div className="border-b border-[var(--border)] px-5 py-3">
        <span className="text-sm font-medium text-[var(--foreground)]">
          {title} <span className="text-[var(--muted-foreground)]">({countLabel})</span>
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
