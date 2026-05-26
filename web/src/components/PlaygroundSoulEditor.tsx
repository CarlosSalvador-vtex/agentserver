import { useEffect, useState, useCallback } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { Save, Send, ArrowLeft, Loader2 } from 'lucide-react'
import {
  getPlaygroundSoul,
  patchPlaygroundSoul,
  promotePlaygroundSoul,
  type PlaygroundSoulFull,
} from '../lib/api'

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

export function PlaygroundSoulEditor() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [draft, setDraft] = useState<PlaygroundSoulFull | null>(null)
  const [fm, setFm] = useState<SoulFrontmatter>({})
  const [body, setBody] = useState('')
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    if (!id) return
    try {
      const d = await getPlaygroundSoul(id)
      setDraft(d)
      setFm((d.frontmatter ?? {}) as SoulFrontmatter)
      setBody(d.body ?? '')
      setDirty(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load')
    }
  }, [id])

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
    } catch (e) {
      setError(e instanceof Error ? e.message : 'save failed')
    } finally {
      setSaving(false)
    }
  }

  const handlePromote = async () => {
    if (!id) return
    if (!confirm('Promote this soul? Opens a PR on the agentserver repo.')) return
    try {
      const r = await promotePlaygroundSoul(id)
      window.open(r.pr_url, '_blank')
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'promote failed')
    }
  }

  if (!draft) {
    return <div className="p-6 text-[var(--muted-foreground)]">Loading…</div>
  }

  return (
    <div className="flex h-screen flex-col">
      <header className="flex items-center gap-3 border-b border-[var(--border)] bg-[var(--card)] px-5 py-3">
        <button onClick={() => navigate('/playground')} className="text-[var(--muted-foreground)] hover:text-[var(--foreground)]">
          <ArrowLeft size={16} />
        </button>
        <span className="text-sm font-semibold text-[var(--foreground)]">{draft.name}</span>
        <span className="text-xs text-[var(--muted-foreground)]">({draft.status})</span>
        {dirty && <span className="text-xs text-yellow-400">● unsaved</span>}
        <div className="flex-1" />
        <button
          onClick={handleSave}
          disabled={!dirty || saving}
          className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--card)] px-3 py-1 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--secondary)] disabled:opacity-40"
        >
          {saving ? <Loader2 size={12} className="animate-spin" /> : <Save size={12} />}
          Save
        </button>
        <button
          onClick={handlePromote}
          disabled={dirty || draft.status !== 'draft'}
          className="inline-flex items-center gap-1 rounded-md border border-green-500/30 bg-green-500/10 px-3 py-1 text-xs font-medium text-green-400 hover:bg-green-500/20 disabled:opacity-40"
        >
          <Send size={12} /> Promote → PR
        </button>
      </header>

      {error && (
        <div className="bg-red-500/10 px-5 py-2 text-xs text-red-400 border-b border-red-500/30">{error}</div>
      )}

      <div className="flex flex-1 overflow-hidden">
        {/* Frontmatter form */}
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

        {/* Body editor */}
        <main className="flex flex-1 flex-col">
          <div className="px-4 py-2 border-b border-[var(--border)] text-[10px] uppercase tracking-wide text-[var(--muted-foreground)]">
            Body (markdown)
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
        </main>
      </div>

      <div className="border-t border-[var(--border)] bg-[var(--card)] px-5 py-2 text-[11px] text-[var(--muted-foreground)]">
        <Link to="/playground" className="hover:text-[var(--foreground)]">
          ← back to catalog
        </Link>
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
              ×
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
