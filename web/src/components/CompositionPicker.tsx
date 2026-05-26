import { useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, X } from 'lucide-react'
import {
  listPlaygroundSkills,
  listPlaygroundSouls,
  type PlaygroundSkillSummary,
  type PlaygroundSoulSummary,
  type SandboxCompositionInput,
} from '../lib/api'

interface CompositionPickerProps {
  value: SandboxCompositionInput | null
  onChange: (next: SandboxCompositionInput | null) => void
}

export function CompositionPicker({ value, onChange }: CompositionPickerProps) {
  const [open, setOpen] = useState(false)
  const [skills, setSkills] = useState<PlaygroundSkillSummary[]>([])
  const [souls, setSouls] = useState<PlaygroundSoulSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!open || (skills.length > 0 && souls.length > 0)) return
    setLoading(true)
    setError(null)
    Promise.all([listPlaygroundSkills(), listPlaygroundSouls()])
      .then(([sk, so]) => {
        setSkills(sk.filter((s) => s.status === 'draft' || s.status === 'promoted'))
        setSouls(so.filter((s) => s.status === 'draft' || s.status === 'promoted'))
      })
      .catch((e) => setError(e instanceof Error ? e.message : 'failed to load'))
      .finally(() => setLoading(false))
  }, [open, skills.length, souls.length])

  const composition = value ?? {}
  const currentSoul = composition.soul ?? ''
  const currentSkills = composition.skills ?? []
  const currentTrack = composition.track_upstream ?? false

  const update = (next: Partial<SandboxCompositionInput>) => {
    const merged: SandboxCompositionInput = { ...composition, ...next }
    // Clear when empty so the body doesn't carry an inert composition.
    if (!merged.soul && (!merged.skills || merged.skills.length === 0)) {
      onChange(null)
    } else {
      onChange(merged)
    }
  }

  const toggleSkill = (ref: string) => {
    const set = new Set(currentSkills)
    if (set.has(ref)) set.delete(ref)
    else set.add(ref)
    update({ skills: Array.from(set) })
  }

  return (
    <div className="rounded-md border border-[var(--border)] bg-[var(--background)]">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between px-3 py-2 text-sm font-medium text-[var(--foreground)]"
      >
        <span className="flex items-center gap-1.5">
          {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
          Composition <span className="text-xs font-normal text-[var(--muted-foreground)]">(optional — attach playground drafts)</span>
        </span>
        {value && (currentSoul || currentSkills.length > 0) && (
          <span className="rounded bg-[var(--secondary)] px-2 py-0.5 text-[10px] font-medium text-[var(--foreground)]">
            {currentSoul ? '1 soul' : ''}{currentSoul && currentSkills.length > 0 ? ' + ' : ''}{currentSkills.length > 0 ? `${currentSkills.length} skill${currentSkills.length > 1 ? 's' : ''}` : ''}
          </span>
        )}
      </button>

      {open && (
        <div className="border-t border-[var(--border)] px-3 py-3 flex flex-col gap-3">
          {loading && (
            <div className="text-xs italic text-[var(--muted-foreground)]">Loading playground drafts…</div>
          )}
          {error && (
            <div className="text-xs text-red-400">{error}</div>
          )}

          {/* Soul */}
          <div className="flex flex-col gap-1">
            <span className="text-xs font-medium text-[var(--foreground)]">Soul (identity)</span>
            <select
              value={currentSoul}
              onChange={(e) => update({ soul: e.target.value || undefined })}
              className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
            >
              <option value="">— none —</option>
              {souls.map((s) => (
                <option key={s.id} value={`draft:${s.id}`}>{s.name} ({s.status})</option>
              ))}
            </select>
          </div>

          {/* Skills */}
          <div className="flex flex-col gap-1">
            <span className="text-xs font-medium text-[var(--foreground)]">Skills (capabilities)</span>
            <div className="rounded-md border border-[var(--border)] bg-[var(--background)] max-h-32 overflow-auto">
              {skills.length === 0 && !loading && (
                <div className="px-2 py-2 text-[11px] italic text-[var(--muted-foreground)]">No skill drafts. Create one in the Playground.</div>
              )}
              {skills.map((s) => {
                const ref = `draft:${s.id}`
                const checked = currentSkills.includes(ref)
                return (
                  <label key={s.id} className="flex items-center gap-2 px-2 py-1 text-xs text-[var(--foreground)] hover:bg-[var(--secondary)]/50 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => toggleSkill(ref)}
                      className="rounded"
                    />
                    <span className="flex-1 truncate">{s.name}</span>
                    <span className="text-[10px] text-[var(--muted-foreground)]">{s.status}</span>
                  </label>
                )
              })}
            </div>
            {currentSkills.length > 0 && (
              <div className="flex flex-wrap gap-1">
                {currentSkills.map((ref) => {
                  const skill = skills.find((s) => `draft:${s.id}` === ref)
                  return (
                    <span key={ref} className="inline-flex items-center gap-1 rounded bg-[var(--secondary)] px-2 py-0.5 text-[11px] text-[var(--foreground)]">
                      {skill?.name ?? ref}
                      <button
                        type="button"
                        onClick={() => toggleSkill(ref)}
                        className="text-[var(--muted-foreground)] hover:text-red-400"
                      >
                        <X size={10} />
                      </button>
                    </span>
                  )
                })}
              </div>
            )}
          </div>

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
            Per-skill config inputs (configSchema) coming in a follow-up. For now, drafts boot with empty config.
          </p>
        </div>
      )}
    </div>
  )
}
