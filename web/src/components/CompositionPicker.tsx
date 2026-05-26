import { useEffect, useMemo, useState } from 'react'
import { ChevronDown, ChevronRight, X } from 'lucide-react'
import {
  listPlaygroundSkills,
  listPlaygroundSouls,
  listTemplateSkills,
  listTemplateSouls,
  getPlaygroundSkill,
  type TemplateSkill,
  type TemplateSoul,
  type SandboxCompositionInput,
} from '../lib/api'

// Sprint 2 PR-7 — picker gaps a (git templates) + b (configSchema form).
//
// Merges playground drafts and git-pinned templates into the same select/list
// so users can pick either without thinking about the underlying ref grammar.
// When a skill is selected and exposes a configSchema with properties, a
// per-property form is rendered; its values ride composition.config[name].

interface CompositionPickerProps {
  value: SandboxCompositionInput | null
  onChange: (next: SandboxCompositionInput | null) => void
}

interface SkillOption {
  ref: string
  name: string
  source: 'draft' | 'template'
  status?: string
  configSchema?: Record<string, unknown>
}

interface SoulOption {
  ref: string
  name: string
  source: 'draft' | 'template'
  status?: string
}

export function CompositionPicker({ value, onChange }: CompositionPickerProps) {
  const [open, setOpen] = useState(false)
  const [skillOpts, setSkillOpts] = useState<SkillOption[]>([])
  const [soulOpts, setSoulOpts] = useState<SoulOption[]>([])
  const [skillSchemas, setSkillSchemas] = useState<Record<string, Record<string, unknown>>>({})
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!open || (skillOpts.length > 0 && soulOpts.length > 0)) return
    setLoading(true)
    setError(null)
    Promise.all([
      listPlaygroundSkills().catch(() => []),
      listPlaygroundSouls().catch(() => []),
      listTemplateSkills().catch(() => []),
      listTemplateSouls().catch(() => []),
    ])
      .then(([draftSkills, draftSouls, tplSkills, tplSouls]) => {
        const skills: SkillOption[] = [
          ...draftSkills
            .filter((s) => s.status === 'draft' || s.status === 'promoted')
            .map((s) => ({ ref: `draft:${s.id}`, name: s.name, source: 'draft' as const, status: s.status })),
          ...(tplSkills as TemplateSkill[]).map((t) => ({
            ref: t.ref,
            name: t.name,
            source: 'template' as const,
            configSchema: t.config_schema,
          })),
        ]
        const souls: SoulOption[] = [
          ...draftSouls
            .filter((s) => s.status === 'draft' || s.status === 'promoted')
            .map((s) => ({ ref: `draft:${s.id}`, name: s.name, source: 'draft' as const, status: s.status })),
          ...(tplSouls as TemplateSoul[]).map((t) => ({
            ref: t.ref,
            name: t.name,
            source: 'template' as const,
          })),
        ]
        setSkillOpts(skills)
        setSoulOpts(souls)
        // Pre-seed schemas for template skills (they already carry it).
        const seeded: Record<string, Record<string, unknown>> = {}
        for (const t of tplSkills as TemplateSkill[]) {
          if (t.config_schema) seeded[t.ref] = t.config_schema
        }
        setSkillSchemas(seeded)
      })
      .catch((e) => setError(e instanceof Error ? e.message : 'failed to load'))
      .finally(() => setLoading(false))
  }, [open, skillOpts.length, soulOpts.length])

  const composition = value ?? {}
  const currentSoul = composition.soul ?? ''
  const currentSkills = composition.skills ?? []
  const currentConfig = composition.config ?? {}
  const currentTrack = composition.track_upstream ?? false

  const update = (next: Partial<SandboxCompositionInput>) => {
    const merged: SandboxCompositionInput = { ...composition, ...next }
    if (!merged.soul && (!merged.skills || merged.skills.length === 0)) {
      onChange(null)
    } else {
      onChange(merged)
    }
  }

  const toggleSkill = (ref: string) => {
    const set = new Set(currentSkills)
    if (set.has(ref)) {
      set.delete(ref)
      // Drop config entries for de-selected skills so the body stays tight.
      const opt = skillOpts.find((o) => o.ref === ref)
      if (opt && currentConfig[opt.name]) {
        const next = { ...currentConfig }
        delete next[opt.name]
        update({ skills: Array.from(set), config: Object.keys(next).length ? next : undefined })
        return
      }
    } else {
      set.add(ref)
      // Lazy-fetch configSchema for newly selected draft skill (templates
      // are already pre-seeded). Fire-and-forget; UI re-renders when state
      // lands.
      if (ref.startsWith('draft:') && !skillSchemas[ref]) {
        const id = ref.slice('draft:'.length)
        getPlaygroundSkill(id)
          .then((d) => {
            const manifestStr = d.files?.['openclaw.plugin.json']
            if (!manifestStr) return
            try {
              const manifest = JSON.parse(manifestStr) as { configSchema?: Record<string, unknown> }
              if (manifest.configSchema) {
                setSkillSchemas((prev) => ({ ...prev, [ref]: manifest.configSchema! }))
              }
            } catch {
              // Ignore malformed JSON — draft author will see validation
              // errors on promote anyway.
            }
          })
          .catch(() => {})
      }
    }
    update({ skills: Array.from(set) })
  }

  const setSkillConfig = (skillName: string, prop: string, value: unknown) => {
    const merged = { ...currentConfig }
    const inner = { ...(merged[skillName] ?? {}) }
    if (value === '' || value === undefined) delete inner[prop]
    else inner[prop] = value
    if (Object.keys(inner).length === 0) delete merged[skillName]
    else merged[skillName] = inner
    update({ config: Object.keys(merged).length ? merged : undefined })
  }

  const visibleConfigForms = useMemo(
    () =>
      currentSkills.flatMap((ref) => {
        const opt = skillOpts.find((o) => o.ref === ref)
        const schema = skillSchemas[ref]
        if (!opt || !schema) return []
        const props = (schema.properties as Record<string, { type?: string; description?: string; enum?: string[] }> | undefined) ?? {}
        const propNames = Object.keys(props)
        if (propNames.length === 0) return []
        return [{ ref, name: opt.name, props, propNames }]
      }),
    [currentSkills, skillOpts, skillSchemas],
  )

  return (
    <div className="rounded-md border border-[var(--border)] bg-[var(--background)]">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between px-3 py-2 text-sm font-medium text-[var(--foreground)]"
      >
        <span className="flex items-center gap-1.5">
          {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
          Composition <span className="text-xs font-normal text-[var(--muted-foreground)]">(optional — drafts + git templates)</span>
        </span>
        {value && (currentSoul || currentSkills.length > 0) && (
          <span className="rounded bg-[var(--secondary)] px-2 py-0.5 text-[10px] font-medium text-[var(--foreground)]">
            {currentSoul ? '1 soul' : ''}
            {currentSoul && currentSkills.length > 0 ? ' + ' : ''}
            {currentSkills.length > 0 ? `${currentSkills.length} skill${currentSkills.length > 1 ? 's' : ''}` : ''}
          </span>
        )}
      </button>

      {open && (
        <div className="border-t border-[var(--border)] px-3 py-3 flex flex-col gap-3">
          {loading && <div className="text-xs italic text-[var(--muted-foreground)]">Loading drafts + templates…</div>}
          {error && <div className="text-xs text-red-400">{error}</div>}

          {/* Soul */}
          <div className="flex flex-col gap-1">
            <span className="text-xs font-medium text-[var(--foreground)]">Soul (identity)</span>
            <select
              value={currentSoul}
              onChange={(e) => update({ soul: e.target.value || undefined })}
              className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
            >
              <option value="">— none —</option>
              {soulOpts.map((s) => (
                <option key={s.ref} value={s.ref}>
                  {s.name} {s.source === 'draft' ? `(draft · ${s.status})` : '(template)'}
                </option>
              ))}
            </select>
          </div>

          {/* Skills */}
          <div className="flex flex-col gap-1">
            <span className="text-xs font-medium text-[var(--foreground)]">Skills (capabilities)</span>
            <div className="rounded-md border border-[var(--border)] bg-[var(--background)] max-h-32 overflow-auto">
              {skillOpts.length === 0 && !loading && (
                <div className="px-2 py-2 text-[11px] italic text-[var(--muted-foreground)]">
                  No skill drafts or templates available.
                </div>
              )}
              {skillOpts.map((s) => {
                const checked = currentSkills.includes(s.ref)
                return (
                  <label
                    key={s.ref}
                    className="flex items-center gap-2 px-2 py-1 text-xs text-[var(--foreground)] hover:bg-[var(--secondary)]/50 cursor-pointer"
                  >
                    <input type="checkbox" checked={checked} onChange={() => toggleSkill(s.ref)} className="rounded" />
                    <span className="flex-1 truncate">{s.name}</span>
                    <span className="text-[10px] text-[var(--muted-foreground)]">
                      {s.source === 'draft' ? s.status : 'template'}
                    </span>
                  </label>
                )
              })}
            </div>
            {currentSkills.length > 0 && (
              <div className="flex flex-wrap gap-1">
                {currentSkills.map((ref) => {
                  const opt = skillOpts.find((s) => s.ref === ref)
                  return (
                    <span
                      key={ref}
                      className="inline-flex items-center gap-1 rounded bg-[var(--secondary)] px-2 py-0.5 text-[11px] text-[var(--foreground)]"
                    >
                      {opt?.name ?? ref}
                      <button type="button" onClick={() => toggleSkill(ref)} className="text-[var(--muted-foreground)] hover:text-red-400">
                        <X size={10} />
                      </button>
                    </span>
                  )
                })}
              </div>
            )}
          </div>

          {/* Per-skill configSchema forms (gap b) */}
          {visibleConfigForms.length > 0 && (
            <div className="flex flex-col gap-2 rounded-md border border-[var(--border)] bg-[var(--card)]/30 px-3 py-2">
              <span className="text-[10px] uppercase tracking-wide text-[var(--muted-foreground)]">Skill config</span>
              {visibleConfigForms.map((form) => (
                <div key={form.ref} className="flex flex-col gap-1.5">
                  <span className="text-[11px] font-medium text-[var(--foreground)]">{form.name}</span>
                  {form.propNames.map((p) => {
                    const meta = form.props[p]
                    const propType = meta?.type ?? 'string'
                    const value = (currentConfig[form.name]?.[p] as string | number | boolean | undefined) ?? ''
                    if (propType === 'boolean') {
                      return (
                        <label key={p} className="flex items-center gap-2 text-[11px] text-[var(--foreground)]">
                          <input
                            type="checkbox"
                            checked={Boolean(value)}
                            onChange={(e) => setSkillConfig(form.name, p, e.target.checked)}
                            className="rounded"
                          />
                          <span>{p}</span>
                          {meta?.description && <span className="text-[10px] text-[var(--muted-foreground)]">— {meta.description}</span>}
                        </label>
                      )
                    }
                    if (meta?.enum && meta.enum.length > 0) {
                      return (
                        <label key={p} className="flex flex-col gap-0.5 text-[11px] text-[var(--foreground)]">
                          <span>
                            {p}
                            {meta.description ? <span className="text-[10px] text-[var(--muted-foreground)]"> — {meta.description}</span> : null}
                          </span>
                          <select
                            value={String(value)}
                            onChange={(e) => setSkillConfig(form.name, p, e.target.value)}
                            className="rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1"
                          >
                            <option value="">—</option>
                            {meta.enum.map((opt) => (
                              <option key={opt} value={opt}>
                                {opt}
                              </option>
                            ))}
                          </select>
                        </label>
                      )
                    }
                    return (
                      <label key={p} className="flex flex-col gap-0.5 text-[11px] text-[var(--foreground)]">
                        <span>
                          {p}
                          {meta?.description ? <span className="text-[10px] text-[var(--muted-foreground)]"> — {meta.description}</span> : null}
                        </span>
                        <input
                          type={propType === 'number' || propType === 'integer' ? 'number' : 'text'}
                          value={String(value)}
                          onChange={(e) => {
                            const raw = e.target.value
                            const parsed = propType === 'number' || propType === 'integer' ? (raw === '' ? '' : Number(raw)) : raw
                            setSkillConfig(form.name, p, parsed)
                          }}
                          className="rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1"
                        />
                      </label>
                    )
                  })}
                </div>
              ))}
            </div>
          )}

          {/* Track upstream */}
          <label className="flex items-center gap-2 text-xs text-[var(--foreground)]">
            <input
              type="checkbox"
              checked={currentTrack}
              onChange={(e) => update({ track_upstream: e.target.checked })}
              className="rounded"
            />
            Track upstream (re-resolve git refs on each pod boot)
          </label>

          <p className="text-[10px] text-[var(--muted-foreground)]">
            Drafts and git-pinned templates merge into the same picker. Selecting a skill auto-loads its configSchema.
          </p>
        </div>
      )}
    </div>
  )
}
