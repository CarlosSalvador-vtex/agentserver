import { useEffect, useState, useCallback } from 'react'
import { useParams, useNavigate, useSearchParams } from 'react-router-dom'
import { Save, Send, ArrowLeft, Loader2, Play, FileDiff, History } from 'lucide-react'
import {
  getPlaygroundSoul,
  patchPlaygroundSoul,
  publishPlaygroundSoul,
  dryRunPlaygroundSoul,
  listWorkspaces,
  PLAYGROUND_DRYRUN_MODELS,
  type PlaygroundSoulFull,
  type PlaygroundDryRunResponse,
  type Workspace,
} from '../lib/api'
import { deployAgent } from '../lib/deploy'
import { MarketplaceVisibilityToggle } from './MarketplaceVisibilityToggle'
import { SoulPromotedDiff } from './SoulPromotedDiff'
import { DraftAuditTimeline } from './DraftAuditTimeline'
import { PromotedPRBanner } from './PromotedPRBanner'

interface SoulFrontmatter {
  id?: string
  version?: string
  description?: string
  voice?: {
    language?: string
    formality?: 'high' | 'medium' | 'low'
    tone_examples?: string[]
  }
  constraints?: {
    max_turns?: number
    refuse_patterns?: string[]
    handoff_to_human_if?: string[]
  }
  compatible_skills?: string[]
}

export function PlaygroundSoulEditor({ isDevMode }: { isDevMode?: boolean }) {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const firstTime = searchParams.get('firstTime') === '1'
  const backTo = searchParams.get('from') === 'admin' ? '/admin/skills' : '/playground'

  const [draft, setDraft] = useState<PlaygroundSoulFull | null>(null)
  const [fm, setFm] = useState<SoulFrontmatter>({})
  const [body, setBody] = useState('')
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [dryRun, setDryRun] = useState<PlaygroundDryRunResponse | null>(null)
  const [running, setRunning] = useState(false)
  const [userMessage, setUserMessage] = useState('')
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [dryRunWorkspaceID, setDryRunWorkspaceID] = useState('')
  const [dryRunModel, setDryRunModel] = useState<string>(PLAYGROUND_DRYRUN_MODELS[0])
  const [view, setView] = useState<'edit' | 'diff' | 'audit'>('edit')
  const [promoteConfirm, setPromoteConfirm] = useState(false)
  // Simplified non-dev mode state
  const [draftName, setDraftName] = useState('')
  const [deploying, setDeploying] = useState(false)
  const [deployError, setDeployError] = useState<string | null>(null)
  const [deploySuccess, setDeploySuccess] = useState<string | null>(null)

  useEffect(() => {
    listWorkspaces()
      .then((ws) => {
        setWorkspaces(ws)
        if (ws.length > 0 && !dryRunWorkspaceID) setDryRunWorkspaceID(ws[0].id)
      })
      .catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const handleDryRun = async () => {
    if (!id) return
    setRunning(true)
    setError(null)
    try {
      const out = await dryRunPlaygroundSoul(id, {
        user_message: userMessage,
        workspace_id: dryRunWorkspaceID || undefined,
        model: dryRunModel || undefined,
      })
      setDryRun(out)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'dry-run failed')
    } finally {
      setRunning(false)
    }
  }

  const load = useCallback(async () => {
    if (!id) return
    try {
      const d = await getPlaygroundSoul(id)
      setDraft(d)
      setFm((d.frontmatter ?? {}) as SoulFrontmatter)
      setBody(d.body ?? '')
      setDirty(false)
      // Set draft name: strip -fork suffix when first time
      if (firstTime) {
        setDraftName((d.name ?? '').replace(/-fork$/, ''))
      } else {
        setDraftName(d.name ?? '')
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load')
    }
  }, [id, firstTime])

  useEffect(() => {
    load()
  }, [load])

  const update = (next: Partial<SoulFrontmatter>) => {
    setFm({ ...fm, ...next })
    setDirty(true)
  }
  const updateVoice = (next: Partial<NonNullable<SoulFrontmatter['voice']>>) => {
    setFm({ ...fm, voice: { ...(fm.voice ?? {}), ...next } })
    setDirty(true)
  }
  const updateConstraints = (next: Partial<NonNullable<SoulFrontmatter['constraints']>>) => {
    setFm({ ...fm, constraints: { ...(fm.constraints ?? {}), ...next } })
    setDirty(true)
  }

  const handleSave = async () => {
    if (!id) return
    setSaving(true)
    setError(null)
    try {
      await patchPlaygroundSoul(id, fm as Record<string, unknown>, body)
      setDirty(false)
      // TODO: persisting draftName requires a separate rename endpoint which is not yet
      // implemented in the backend. The name is shown locally but not saved to the DB.
      // A future backend change (PATCH /api/playground/souls/:id/rename or similar) is needed.
    } catch (e) {
      setError(e instanceof Error ? e.message : 'save failed')
    } finally {
      setSaving(false)
    }
  }

  const handlePublish = async () => {
    if (!id) return
    setPromoteConfirm(false)
    try {
      await publishPlaygroundSoul(id)
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'publish failed')
    }
  }

  const handleDeploy = async () => {
    if (!draft) return
    setDeploying(true)
    setDeployError(null)
    setDeploySuccess(null)
    try {
      const wsId = workspaces[0]?.id
      if (!wsId) throw new Error('Nenhum workspace encontrado')
      const result = await deployAgent(wsId, draft.id, [])
      setDeploySuccess(`Agente publicado com sucesso! ID: ${result.sandboxId}`)
    } catch (e) {
      setDeployError(e instanceof Error ? e.message : 'Erro ao publicar agente')
    } finally {
      setDeploying(false)
    }
  }

  if (!draft) {
    return <div className="p-6 text-[var(--muted-foreground)]">Loading…</div>
  }

  return (
    <div className="flex h-screen flex-col">
      {!isDevMode ? (
        <header className="flex items-center gap-3 border-b border-[var(--border)] bg-[var(--card)] px-5 py-3">
          <button onClick={() => navigate(backTo)} className="text-[var(--muted-foreground)] hover:text-[var(--foreground)]">
            <ArrowLeft size={16} />
          </button>
          <div className="flex-1 min-w-0">
            <div className="text-sm font-semibold text-[var(--foreground)]">Personalidade do Agente</div>
            <div className="text-xs text-[var(--muted-foreground)]">{draftName || draft.name}</div>
          </div>
          {dirty && <span className="text-xs text-yellow-400">● não salvo</span>}
          <button
            onClick={handleSave}
            disabled={!dirty || saving}
            className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--card)] px-3 py-1 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--secondary)] disabled:opacity-40"
          >
            {saving ? <Loader2 size={12} className="animate-spin" /> : <Save size={12} />}
            Salvar
          </button>
          <button
            onClick={handleDeploy}
            disabled={deploying || dirty}
            className="inline-flex items-center gap-1 rounded-md bg-orange-600 px-3 py-1 text-xs font-semibold text-white hover:bg-orange-500 disabled:opacity-40"
          >
            {deploying ? <Loader2 size={12} className="animate-spin" /> : null}
            {deploying ? 'Publicando...' : 'Publicar agente'}
          </button>
        </header>
      ) : (
        <header className="flex items-center gap-3 border-b border-[var(--border)] bg-[var(--card)] px-5 py-3">
          <button onClick={() => navigate(backTo)} className="text-[var(--muted-foreground)] hover:text-[var(--foreground)]">
            <ArrowLeft size={16} />
          </button>
          <span className="text-sm font-semibold text-[var(--foreground)]">{draft.name}</span>
          <span className="text-xs text-[var(--muted-foreground)]">({draft.status})</span>
          {dirty && <span className="text-xs text-yellow-400">● unsaved</span>}
          <div className="flex-1" />
          <MarketplaceVisibilityToggle
            kind="soul"
            draftID={draft.id}
            visibility={draft.visibility ?? 'private'}
            canSet={draft.can_set_visibility ?? false}
            onChanged={(v) => setDraft({ ...draft, visibility: v })}
          />
          <button
            onClick={handleSave}
            disabled={!dirty || saving}
            className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--card)] px-3 py-1 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--secondary)] disabled:opacity-40"
          >
            {saving ? <Loader2 size={12} className="animate-spin" /> : <Save size={12} />}
            Save
          </button>
          {promoteConfirm ? (
            <span className="inline-flex items-center gap-1">
              <span className="text-xs text-[var(--muted-foreground)]">Publish?</span>
              <button
                onClick={handlePublish}
                className="inline-flex items-center gap-1 rounded-md border border-green-500/30 bg-green-500/10 px-3 py-1 text-xs font-medium text-green-400 hover:bg-green-500/20"
              >
                <Send size={12} /> Yes
              </button>
              <button
                onClick={() => setPromoteConfirm(false)}
                className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--card)] px-3 py-1 text-xs font-medium text-[var(--muted-foreground)] hover:bg-[var(--secondary)]"
              >
                Cancel
              </button>
            </span>
          ) : (
            <button
              onClick={() => setPromoteConfirm(true)}
              disabled={dirty || draft.status === 'archived'}
              className="inline-flex items-center gap-1 rounded-md border border-green-500/30 bg-green-500/10 px-3 py-1 text-xs font-medium text-green-400 hover:bg-green-500/20 disabled:opacity-40"
              title={dirty ? 'Save first' : draft.status === 'published' ? 'Already published — republish to update' : ''}
            >
              <Send size={12} /> {draft.status === 'published' ? 'Republish' : 'Publish'}
            </button>
          )}
        </header>
      )}

      {error && (
        <div className="bg-red-500/10 px-5 py-2 text-xs text-red-400 border-b border-red-500/30">{error}</div>
      )}
      {deploySuccess && (
        <div className="bg-green-500/10 px-5 py-2 text-xs text-green-400 border-b border-green-500/30">{deploySuccess}</div>
      )}
      {deployError && (
        <div className="bg-red-500/10 px-5 py-2 text-xs text-red-400 border-b border-red-500/30">{deployError}</div>
      )}

      <div className="flex flex-1 overflow-hidden">
        {!isDevMode ? (
          /* Simplified aside for non-dev users */
          <aside className="w-80 shrink-0 overflow-auto border-r border-[var(--border)] bg-[var(--card)]/30 p-4 space-y-4">
            {firstTime && (
              <div className="rounded-md border border-orange-500/30 bg-orange-500/10 px-3 py-2 text-xs text-orange-400">
                Dê um nome ao seu agente e configure a personalidade abaixo.
              </div>
            )}
            <div>
              <label className="block text-[11px] text-[var(--muted-foreground)] mb-1">Nome do agente</label>
              <input
                type="text"
                value={draftName}
                onChange={(e) => { setDraftName(e.target.value); setDirty(true) }}
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
                placeholder="Ex: Agente de Cobrança"
                autoFocus={firstTime}
              />
            </div>
            <div>
              <label className="block text-[11px] text-[var(--muted-foreground)] mb-1">Tom do agente</label>
              <select
                value={fm.voice?.formality ?? ''}
                onChange={(e) => updateVoice({ formality: e.target.value ? (e.target.value as 'high' | 'medium' | 'low') : undefined })}
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
              >
                <option value="">Não definido</option>
                <option value="high">Formal</option>
                <option value="medium">Amigável</option>
                <option value="low">Firme</option>
              </select>
            </div>
          </aside>
        ) : (
          /* Full frontmatter form for dev users */
          <aside className="w-96 shrink-0 overflow-auto border-r border-[var(--border)] bg-[var(--card)]/30 p-4">
            <div className="text-[10px] uppercase tracking-wide text-[var(--muted-foreground)] mb-3">Frontmatter</div>
            <Field label="ID" value={fm.id ?? ''} onChange={(v) => update({ id: v })} />
            <Field label="Version" value={fm.version ?? ''} onChange={(v) => update({ version: v })} />
            <Field label="Description" value={fm.description ?? ''} onChange={(v) => update({ description: v })} />

            <div className="mt-4 text-[10px] uppercase tracking-wide text-[var(--muted-foreground)]">Voice</div>
            <Field label="Language" value={fm.voice?.language ?? ''} onChange={(v) => updateVoice({ language: v })} />
            <SelectField
              label="Formality"
              value={fm.voice?.formality ?? ''}
              options={['', 'high', 'medium', 'low']}
              onChange={(v) => updateVoice({ formality: v ? (v as 'high' | 'medium' | 'low') : undefined })}
            />
            <ListField
              label="Tone examples"
              value={fm.voice?.tone_examples ?? []}
              onChange={(v) => updateVoice({ tone_examples: v })}
            />

            <div className="mt-4 text-[10px] uppercase tracking-wide text-[var(--muted-foreground)]">Constraints</div>
            <NumberField
              label="Max turns"
              value={fm.constraints?.max_turns ?? 0}
              onChange={(v) => updateConstraints({ max_turns: v })}
            />
            <ListField
              label="Refuse patterns"
              value={fm.constraints?.refuse_patterns ?? []}
              onChange={(v) => updateConstraints({ refuse_patterns: v })}
            />
            <ListField
              label="Handoff if"
              value={fm.constraints?.handoff_to_human_if ?? []}
              onChange={(v) => updateConstraints({ handoff_to_human_if: v })}
            />

            <div className="mt-4 text-[10px] uppercase tracking-wide text-[var(--muted-foreground)]">Compatible</div>
            <ListField
              label="Skills"
              value={fm.compatible_skills ?? []}
              onChange={(v) => update({ compatible_skills: v })}
            />
          </aside>
        )}

        {/* Body editor */}
        <main className="flex flex-1 flex-col">
          {isDevMode && (
            <div className="flex items-center gap-1 border-b border-[var(--border)] bg-[var(--card)]/50 px-3 py-1.5">
              <button
                onClick={() => setView('edit')}
                className={`rounded px-2 py-0.5 text-[11px] font-medium ${
                  view === 'edit' ? 'bg-[var(--secondary)] text-[var(--foreground)]' : 'text-[var(--muted-foreground)] hover:bg-[var(--secondary)]/50'
                }`}
              >
                Edit
              </button>
              {draft.status === 'promoted' && draft.promoted_commit && (
                <button
                  onClick={() => setView('diff')}
                  className={`inline-flex items-center gap-1 rounded px-2 py-0.5 text-[11px] font-medium ${
                    view === 'diff' ? 'bg-[var(--secondary)] text-[var(--foreground)]' : 'text-[var(--muted-foreground)] hover:bg-[var(--secondary)]/50'
                  }`}
                  title={`Diff vs promoted commit ${draft.promoted_commit.slice(0, 7)}`}
                >
                  <FileDiff size={11} /> Diff vs promoted
                </button>
              )}
              <button
                onClick={() => setView('audit')}
                className={`inline-flex items-center gap-1 rounded px-2 py-0.5 text-[11px] font-medium ${
                  view === 'audit' ? 'bg-[var(--secondary)] text-[var(--foreground)]' : 'text-[var(--muted-foreground)] hover:bg-[var(--secondary)]/50'
                }`}
                title="Audit timeline"
              >
                <History size={11} /> Audit
              </button>
              <div className="ml-auto">
                <PromotedPRBanner
                  url={draft.promoted_pr_url}
                  state={draft.promoted_pr_state}
                />
              </div>
            </div>
          )}

          {isDevMode && view === 'diff' && draft.promoted_commit ? (
            <div className="flex-1 overflow-auto">
              <SoulPromotedDiff
                key={`${draft.promoted_commit}-${draft.name}`}
                commit={draft.promoted_commit}
                soulName={draft.name}
                body={body}
              />
            </div>
          ) : isDevMode && view === 'audit' ? (
            <div className="flex-1 overflow-auto">
              <DraftAuditTimeline kind="souls" draftID={draft.id} />
            </div>
          ) : (
            <>
              <div className="px-4 py-2 border-b border-[var(--border)] text-[10px] uppercase tracking-wide text-[var(--muted-foreground)]">
                {isDevMode ? 'Body (markdown)' : 'Instruções do agente'}
              </div>
              <textarea
                value={body}
                onChange={(e) => {
                  setBody(e.target.value)
                  setDirty(true)
                }}
                spellCheck={false}
                className="flex-1 resize-none bg-[var(--background)] p-4 font-mono text-sm text-[var(--foreground)] outline-none"
                placeholder="# Persona — descrição livre do agente"
              />
            </>
          )}
        </main>

        {/* Dry-run panel — dev mode only */}
        {isDevMode && (
          <aside className="w-96 shrink-0 border-l border-[var(--border)] bg-[var(--card)]/30 flex flex-col">
            <div className="px-4 py-3 border-b border-[var(--border)]">
              <div className="text-[10px] uppercase tracking-wide text-[var(--muted-foreground)] mb-2">Dry-run (soul only)</div>
              {workspaces.length > 1 && (
                <select
                  value={dryRunWorkspaceID}
                  onChange={(e) => setDryRunWorkspaceID(e.target.value)}
                  title="Workspace whose LLM quota / BYOK funds this dry-run"
                  className="mb-2 w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
                >
                  {workspaces.map((w) => (
                    <option key={w.id} value={w.id}>
                      {w.name} ({w.id.slice(0, 8)})
                    </option>
                  ))}
                </select>
              )}
              <select
                value={dryRunModel}
                onChange={(e) => setDryRunModel(e.target.value)}
                title="LLM model for completion (optional override)"
                className="mb-2 w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
              >
                {PLAYGROUND_DRYRUN_MODELS.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
              <textarea
                placeholder="User message"
                value={userMessage}
                onChange={(e) => setUserMessage(e.target.value)}
                rows={3}
                className="w-full resize-none rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)] font-mono"
              />
              <button
                onClick={handleDryRun}
                disabled={running || !userMessage.trim()}
                className="mt-2 inline-flex items-center gap-1 rounded-md bg-orange-600 px-3 py-1 text-xs font-medium text-white hover:bg-orange-500 disabled:opacity-40"
              >
                {running ? <Loader2 size={12} className="animate-spin" /> : <Play size={12} />}
                Run dry-run
              </button>
              <p className="mt-2 text-[10px] text-[var(--muted-foreground)]">
                Persona only — no skill prompt. Use for testing voice + constraints in isolation.
              </p>
            </div>
            <div className="flex-1 overflow-auto p-4 text-xs space-y-3">
              {dryRun && (
                <>
                  {dryRun.completion && (
                    <div>
                      <div className="text-[var(--muted-foreground)] mb-1">Completion ({dryRun.completion_model})</div>
                      <div className="rounded bg-[var(--background)] p-2 whitespace-pre-wrap text-[var(--foreground)]">
                        {dryRun.completion}
                      </div>
                    </div>
                  )}
                  {dryRun.completion_error && (
                    <div>
                      <div className="text-red-400 mb-1">Completion error</div>
                      <div className="rounded bg-red-500/10 p-2 whitespace-pre-wrap text-red-400">
                        {dryRun.completion_error}
                      </div>
                    </div>
                  )}
                  <div>
                    <div className="text-[var(--muted-foreground)] mb-1">System prompt</div>
                    <pre className="rounded bg-[var(--background)] p-2 whitespace-pre-wrap text-[var(--foreground)] font-mono">
                      {dryRun.system_prompt || '(empty)'}
                    </pre>
                  </div>
                </>
              )}
            </div>
          </aside>
        )}
      </div>

      <div className="border-t border-[var(--border)] bg-[var(--card)] px-5 py-2 text-[11px] text-[var(--muted-foreground)]">
        <button onClick={() => navigate(backTo)} className="hover:text-[var(--foreground)]">
          ← back to catalog
        </button>
      </div>
    </div>
  )
}

function Field({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <label className="block mb-2">
      <span className="block text-[11px] text-[var(--muted-foreground)] mb-1">{label}</span>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
      />
    </label>
  )
}

function NumberField({ label, value, onChange }: { label: string; value: number; onChange: (v: number) => void }) {
  return (
    <label className="block mb-2">
      <span className="block text-[11px] text-[var(--muted-foreground)] mb-1">{label}</span>
      <input
        type="number"
        value={value || ''}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
      />
    </label>
  )
}

function SelectField({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: string
  options: string[]
  onChange: (v: string) => void
}) {
  return (
    <label className="block mb-2">
      <span className="block text-[11px] text-[var(--muted-foreground)] mb-1">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
      >
        {options.map((o) => (
          <option key={o} value={o}>
            {o || '(unset)'}
          </option>
        ))}
      </select>
    </label>
  )
}

function ListField({ label, value, onChange }: { label: string; value: string[]; onChange: (v: string[]) => void }) {
  const [draft, setDraft] = useState('')
  return (
    <div className="mb-2">
      <span className="block text-[11px] text-[var(--muted-foreground)] mb-1">{label}</span>
      <div className="flex flex-wrap gap-1 mb-1">
        {value.map((v, i) => (
          <span key={i} className="inline-flex items-center gap-1 rounded bg-[var(--secondary)] px-2 py-0.5 text-[11px] text-[var(--foreground)]">
            {v}
            <button onClick={() => onChange(value.filter((_, j) => j !== i))} className="text-[var(--muted-foreground)] hover:text-red-400">
              x
            </button>
          </span>
        ))}
      </div>
      <div className="flex gap-1">
        <input
          type="text"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && draft.trim()) {
              onChange([...value, draft.trim()])
              setDraft('')
            }
          }}
          placeholder="add + Enter"
          className="flex-1 rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
        />
      </div>
    </div>
  )
}
