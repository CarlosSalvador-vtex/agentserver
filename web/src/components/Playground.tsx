import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Plus, Trash2, ExternalLink, Loader2 } from 'lucide-react'
import {
  listPlaygroundSkills,
  createPlaygroundSkill,
  archivePlaygroundSkill,
  listPlaygroundSouls,
  createPlaygroundSoul,
  archivePlaygroundSoul,
  listMarketplaceSouls,
  listMarketplaceSkills,
  forkMarketplaceSoul,
  forkMarketplaceSkill,
  listWorkspaces,
  type PlaygroundSkillSummary,
  type PlaygroundSoulSummary,
} from '../lib/api'
import type { UserInfo } from '../App'
import { CreateDraftModal } from './CreateDraftModal'

type ScopeFilter = 'all' | 'system' | 'mine'

export function Playground({ user }: { user: UserInfo | null }) {
  const navigate = useNavigate()
  const isDevMode = user?.role === 'admin'
  const [skills, setSkills] = useState<PlaygroundSkillSummary[]>([])
  const [souls, setSouls] = useState<PlaygroundSoulSummary[]>([])
  const [scope, setScope] = useState<ScopeFilter>(isDevMode ? 'all' : 'mine')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [createKind, setCreateKind] = useState<'skill' | 'soul' | null>(null)
  const [forking, setForking] = useState(false)
  const [forkError, setForkError] = useState<string | null>(null)

  const reload = async () => {
    try {
      const [s, so] = await Promise.all([listPlaygroundSkills(), listPlaygroundSouls()])
      setSkills(s)
      setSouls(so)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load')
    }
  }

  useEffect(() => {
    reload()
  }, [])

  const handleCreate = (kind: 'skill' | 'soul') => {
    setCreateKind(kind)
  }

  const handleCreateSubmit = async (name: string, workspaceId: string) => {
    setBusy(true)
    try {
      if (createKind === 'skill') await createPlaygroundSkill(name, '', workspaceId)
      else await createPlaygroundSoul(name, '', workspaceId)
      setCreateKind(null)
      await reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'create failed')
      throw e
    } finally {
      setBusy(false)
    }
  }

  const handleArchive = async (kind: 'skill' | 'soul', id: string) => {
    if (!confirm(`Archive this ${kind} draft? (cannot undo)`)) return
    if (kind === 'skill') await archivePlaygroundSkill(id)
    else await archivePlaygroundSoul(id)
    await reload()
  }

  const handleForkCobrana = async () => {
    setForking(true)
    setForkError(null)
    try {
      const [marketSouls, marketSkills, workspaceList] = await Promise.all([
        listMarketplaceSouls(),
        listMarketplaceSkills(),
        listWorkspaces(),
      ])
      const wsId = workspaceList[0]?.id
      if (!wsId) throw new Error('Nenhum workspace encontrado')
      const cobrancaSoul = marketSouls.find((s) => s.name.toLowerCase().includes('cobran'))
      const cobrancaSkill = marketSkills.find((s) => s.name.toLowerCase().includes('cobran') || s.name.toLowerCase().includes('negoci'))
      if (!cobrancaSoul) throw new Error('Modelo de cobrança não encontrado no marketplace. Peça ao administrador para publicar o template.')
      const [soulFork, skillFork] = await Promise.all([
        forkMarketplaceSoul(cobrancaSoul.id, wsId),
        cobrancaSkill ? forkMarketplaceSkill(cobrancaSkill.id, wsId) : Promise.resolve(null),
      ])
      await reload()
      navigate(`/playground/souls/${soulFork.id}?firstTime=1`)
      void skillFork // keep for future use
    } catch (e) {
      setForkError(e instanceof Error ? e.message : 'Erro ao usar modelo')
    } finally {
      setForking(false)
    }
  }

  const applyScope = <T extends { workspace_id?: string }>(items: T[]): T[] => {
    if (scope === 'system') return items.filter((i) => !i.workspace_id)
    if (scope === 'mine') return items.filter((i) => !!i.workspace_id)
    return items
  }

  const visibleSkills = useMemo(() => applyScope(skills), [skills, scope])
  const visibleSouls = useMemo(() => applyScope(souls), [souls, scope])

  return (
    <div className="p-6 max-w-5xl">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-xl font-semibold text-[var(--foreground)]">Configurar Agente</h1>
          <p className="text-xs text-[var(--muted-foreground)] mt-0.5">Personalize o agente de cobrança da sua empresa</p>
        </div>
        <div className="flex items-center gap-3">
          <button
            onClick={handleForkCobrana}
            disabled={forking}
            className="inline-flex items-center gap-1.5 rounded-md bg-orange-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-orange-500 disabled:opacity-50 transition-colors"
          >
            {forking ? <Loader2 size={12} className="animate-spin" /> : null}
            {forking ? 'Configurando...' : '+ Usar modelo de cobrança'}
          </button>
          {isDevMode && (
            <div className="flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--card)] p-0.5">
              {(['all', 'system', 'mine'] as ScopeFilter[]).map((s) => (
                <button
                  key={s}
                  onClick={() => setScope(s)}
                  className={`rounded px-3 py-1 text-xs font-medium transition-colors ${
                    scope === s
                      ? 'bg-orange-500/20 text-orange-400'
                      : 'text-[var(--muted-foreground)] hover:text-[var(--foreground)]'
                  }`}
                >
                  {s === 'all' ? 'All' : s === 'system' ? 'System' : 'My workspace'}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      {error && (
        <div className="mb-4 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">{error}</div>
      )}
      {forkError && (
        <div className="mb-4 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">{forkError}</div>
      )}

      <Section
        title="Funcionalidades"
        onCreate={() => handleCreate('skill')}
        items={visibleSkills.map((s) => ({
          id: s.id,
          name: s.name,
          status: s.status,
          workspaceId: s.workspace_id,
          prState: s.promoted_pr_state,
          to: `/playground/skills/${s.id}`,
          prURL: s.promoted_pr_url,
          updatedAt: s.updated_at,
          description: s.description,
        }))}
        busy={busy}
        onArchive={(id) => handleArchive('skill', id)}
      />

      <div className="mt-8">
        <Section
          title="Personalidade"
          onCreate={() => handleCreate('soul')}
          items={visibleSouls.map((s) => ({
            id: s.id,
            name: s.name,
            status: s.status,
            workspaceId: s.workspace_id,
            to: `/playground/souls/${s.id}`,
            prURL: s.promoted_pr_url,
            updatedAt: s.updated_at,
            description: s.description,
          }))}
          busy={busy}
          onArchive={(id) => handleArchive('soul', id)}
        />
      </div>

      {createKind && (
        <CreateDraftModal
          kind={createKind}
          onCancel={() => setCreateKind(null)}
          onSubmit={handleCreateSubmit}
        />
      )}
    </div>
  )
}

interface SectionItem {
  id: string
  name: string
  description: string
  status: string
  workspaceId?: string
  prState?: string
  to: string
  prURL?: string
  updatedAt: string
}

function Section({
  title,
  items,
  onCreate,
  busy,
  onArchive,
}: {
  title: string
  items: SectionItem[]
  onCreate: () => void
  busy: boolean
  onArchive: (id: string) => void
}) {
  return (
    <div className="rounded-lg border border-[var(--border)] bg-[var(--card)]">
      <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3">
        <span className="text-sm font-medium text-[var(--foreground)]">
          {title} <span className="text-[var(--muted-foreground)]">({items.length})</span>
        </span>
        <button
          onClick={onCreate}
          disabled={busy}
          className="inline-flex items-center gap-1.5 rounded-md border border-orange-500/30 bg-orange-500/10 px-3 py-1 text-xs font-medium text-orange-400 hover:bg-orange-500/20 transition-colors disabled:opacity-50"
        >
          <Plus size={12} /> New {title.slice(0, -1).toLowerCase()}
        </button>
      </div>
      <div className="divide-y divide-[var(--border)]">
        {items.length === 0 ? (
          <div className="py-8 text-center text-xs italic text-[var(--muted-foreground)]">
            No {title.toLowerCase()} drafts yet.
          </div>
        ) : (
          items.map((it) => (
            <div key={it.id} className="flex items-center gap-3 px-5 py-3 hover:bg-[var(--secondary)]/30">
              <Link to={it.to} className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm text-[var(--foreground)] font-medium">{it.name}</span>
                  <StatusBadge status={it.status} prState={it.prState} />
                  {!it.workspaceId && (
                    <span className="inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium bg-violet-500/10 text-violet-400">system</span>
                  )}
                </div>
                <div className="text-xs text-[var(--muted-foreground)] truncate">
                  {it.description || 'No description'} · updated {new Date(it.updatedAt).toLocaleString()}
                </div>
              </Link>
              {it.prURL && (
                <a
                  href={it.prURL}
                  target="_blank"
                  rel="noreferrer"
                  className="rounded p-1 text-[var(--muted-foreground)] hover:bg-[var(--secondary)] hover:text-[var(--foreground)]"
                  title="View promote PR"
                >
                  <ExternalLink size={14} />
                </a>
              )}
              <button
                onClick={() => onArchive(it.id)}
                className="rounded p-1 text-[var(--muted-foreground)] hover:bg-[var(--secondary)] hover:text-[var(--destructive)]"
                title="Archive draft"
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))
        )}
      </div>
    </div>
  )
}

function StatusBadge({ status, prState }: { status: string; prState?: string }) {
  // When status='promoted' and the background poller has observed the PR
  // state, surface that finer-grained label so users see when the PR was
  // merged or closed without re-opening the tab.
  const label = status === 'promoted' && prState ? `promoted-${prState}` : status
  const styles: Record<string, string> = {
    draft: 'bg-blue-500/10 text-blue-400',
    promoting: 'bg-yellow-500/10 text-yellow-400',
    promoted: 'bg-green-500/10 text-green-400',
    'promoted-open': 'bg-green-500/10 text-green-400',
    'promoted-merged': 'bg-emerald-500/20 text-emerald-300',
    'promoted-closed': 'bg-gray-500/10 text-gray-400',
    archived: 'bg-gray-500/10 text-gray-400',
  }
  return (
    <span className={`inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium ${styles[label] ?? 'bg-gray-500/10 text-gray-400'}`}>
      {label}
    </span>
  )
}
