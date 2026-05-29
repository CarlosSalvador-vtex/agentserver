import { useState, useEffect, useCallback } from 'react'
import { Clock, Plus, Pencil, Trash2, Library } from 'lucide-react'
import {
  listWorkspaceAutomations,
  createWorkspaceAutomation,
  patchWorkspaceAutomation,
  deleteWorkspaceAutomation,
  listWorkspaceIMChannels,
  listPlaygroundSkills,
  getAutomationCatalog,
  type Automation,
  type AutomationCatalogEntry,
  type IMChannel,
  type PlaygroundSkillSummary,
} from '../lib/api'

function formatTime(iso: string | undefined | null): string {
  if (!iso) return '—'
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso ?? '—'
  }
}

function parsePrompt(config: string): string {
  if (!config) return ''
  try {
    const o = JSON.parse(config) as { prompt?: string }
    return o.prompt ?? ''
  } catch {
    return ''
  }
}

interface FormState {
  name: string
  skill_ref: string
  cron: string
  channel_id: string
  prompt: string
  enabled: boolean
}

const emptyForm = (): FormState => ({
  name: '',
  skill_ref: '',
  cron: '@daily',
  channel_id: '',
  prompt: '',
  enabled: true,
})

interface WorkspaceAutomationsTabProps {
  workspaceId: string
}

export function WorkspaceAutomationsTab({ workspaceId }: WorkspaceAutomationsTabProps) {
  const [items, setItems] = useState<Automation[]>([])
  const [channels, setChannels] = useState<IMChannel[]>([])
  const [skills, setSkills] = useState<PlaygroundSkillSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [showCatalog, setShowCatalog] = useState(false)
  const [catalog, setCatalog] = useState<AutomationCatalogEntry[]>([])
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [editing, setEditing] = useState<Automation | null>(null)
  const [form, setForm] = useState<FormState>(emptyForm())
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    setError(null)
    Promise.all([
      listWorkspaceAutomations(workspaceId),
      listWorkspaceIMChannels(workspaceId),
      listPlaygroundSkills(),
    ])
      .then(([autos, chResp, skillList]) => {
        setItems(autos)
        setChannels(chResp.channels ?? [])
        setSkills(skillList)
      })
      .catch(() => {
        setItems([])
        setChannels([])
        setSkills([])
        setError('Failed to load automations')
      })
      .finally(() => setLoading(false))
  }, [workspaceId])

  useEffect(() => {
    load()
  }, [load])

  const openCreate = () => {
    setEditing(null)
    setForm(emptyForm())
    if (channels.length > 0) {
      setForm((f) => ({ ...f, channel_id: channels[0].id }))
    }
    setShowForm(true)
  }

  const openCatalog = () => {
    setShowCatalog(true)
    setCatalogLoading(true)
    getAutomationCatalog()
      .then(setCatalog)
      .catch(() => {
        setCatalog([])
        setError('Failed to load automation catalog')
      })
      .finally(() => setCatalogLoading(false))
  }

  const applyCatalogTemplate = (tpl: AutomationCatalogEntry) => {
    setShowCatalog(false)
    setEditing(null)
    setForm({
      name: tpl.title ?? '',
      skill_ref: tpl.skill_ref ?? 'playground',
      cron: tpl.suggested_cron ?? '@daily',
      channel_id: channels[0]?.id ?? '',
      prompt: tpl.prompt_template ?? '',
      enabled: true,
    })
    setShowForm(true)
  }

  const openEdit = (row: Automation) => {
    setEditing(row)
    setForm({
      name: row.name,
      skill_ref: row.skill_ref,
      cron: row.cron,
      channel_id: row.channel_id ?? '',
      prompt: parsePrompt(row.config ?? ''),
      enabled: row.enabled ?? false,
    })
    setShowForm(true)
  }

  const closeForm = () => {
    setShowForm(false)
    setEditing(null)
    setForm(emptyForm())
  }

  const handleSave = async () => {
    if (!form.name.trim() || !form.skill_ref.trim() || !form.cron.trim() || !form.channel_id || !form.prompt.trim()) {
      setError('Name, skill, cron, channel, and prompt are required')
      return
    }
    setSaving(true)
    setError(null)
    try {
      if (editing) {
        await patchWorkspaceAutomation(workspaceId, editing.id, {
          name: form.name.trim(),
          skill_ref: form.skill_ref.trim(),
          cron: form.cron.trim(),
          channel_id: form.channel_id,
          prompt: form.prompt.trim(),
          enabled: form.enabled,
        })
      } else {
        await createWorkspaceAutomation(workspaceId, {
          name: form.name.trim(),
          skill_ref: form.skill_ref.trim(),
          cron: form.cron.trim(),
          channel_id: form.channel_id,
          enabled: form.enabled,
          prompt: form.prompt.trim(),
        })
      }
      closeForm()
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  const toggleEnabled = async (row: Automation) => {
    try {
      await patchWorkspaceAutomation(workspaceId, row.id, { enabled: !row.enabled })
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Update failed')
    }
  }

  const handleDelete = async (row: Automation) => {
    if (!window.confirm(`Delete automation "${row.name}"?`)) return
    try {
      await deleteWorkspaceAutomation(workspaceId, row.id)
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Delete failed')
    }
  }

  const channelLabel = (id: string) => {
    const ch = channels.find((c) => c.id === id)
    if (!ch) return id.slice(0, 8)
    const label = ch.bot_id || ch.user_id || ch.id
    return ch.provider ? `${ch.provider}: ${label}` : label
  }

  return (
    <>
      <div className="rounded-lg border border-[var(--border)] bg-[var(--card)]">
        <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3">
          <div className="flex items-center gap-2">
            <Clock size={14} className="text-[var(--muted-foreground)]" />
            <span className="text-sm font-medium text-[var(--foreground)]">Automations</span>
            {items.length > 0 && (
              <span className="rounded-full bg-[var(--secondary)] px-2 py-0.5 text-[10px] text-[var(--muted-foreground)]">
                {items.length}
              </span>
            )}
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={openCatalog}
              className="inline-flex items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--card)] px-3 py-1.5 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--secondary)] transition-colors"
            >
              <Library size={13} />
              Add from catalog
            </button>
            <button
              type="button"
              onClick={openCreate}
              className="inline-flex items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--card)] px-3 py-1.5 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--secondary)] transition-colors"
            >
              <Plus size={13} />
              New automation
            </button>
          </div>
        </div>

        <div className="px-5 py-4">
          {error && (
            <p className="mb-3 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">
              {error}
            </p>
          )}
          {loading ? (
            <p className="text-sm text-[var(--muted-foreground)]">Loading…</p>
          ) : items.length === 0 ? (
            <div className="rounded-md border border-dashed border-[var(--border)] py-8 text-center text-xs italic text-[var(--muted-foreground)]">
              No automations yet. Create one to schedule proactive messages on a channel.
            </div>
          ) : (
            <div className="overflow-hidden rounded-md border border-[var(--border)]">
              <table className="w-full border-collapse text-sm">
                <thead className="bg-[var(--secondary)] text-[var(--muted-foreground)]">
                  <tr>
                    <th className="px-3 py-2 text-left font-medium">Name</th>
                    <th className="px-3 py-2 text-left font-medium">Cron</th>
                    <th className="px-3 py-2 text-left font-medium">Channel</th>
                    <th className="px-3 py-2 text-left font-medium">Skill</th>
                    <th className="w-24 px-3 py-2 text-left font-medium">Enabled</th>
                    <th className="w-36 px-3 py-2 text-left font-medium">Last run</th>
                    <th className="w-36 px-3 py-2 text-left font-medium">Next run</th>
                    <th className="px-3 py-2 text-left font-medium">Last error</th>
                    <th className="w-28 px-3 py-2 text-right font-medium">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((row, i) => (
                    <tr
                      key={row.id}
                      className={`border-t border-[var(--border)] ${i % 2 === 1 ? 'bg-[var(--background)]/40' : ''} ${!row.enabled ? 'opacity-60' : ''}`}
                    >
                      <td className="px-3 py-2 text-[var(--foreground)]">{row.name}</td>
                      <td className="px-3 py-2 font-mono text-xs text-[var(--muted-foreground)]">{row.cron}</td>
                      <td className="px-3 py-2 text-xs text-[var(--foreground)]">{channelLabel(row.channel_id)}</td>
                      <td className="px-3 py-2 font-mono text-xs text-[var(--muted-foreground)]">{row.skill_ref}</td>
                      <td className="px-3 py-2">
                        <button
                          type="button"
                          role="switch"
                          aria-checked={row.enabled}
                          onClick={() => toggleEnabled(row)}
                          className={`relative inline-flex h-5 w-9 shrink-0 rounded-full border transition-colors ${
                            row.enabled
                              ? 'border-[var(--primary)] bg-[var(--primary)]'
                              : 'border-[var(--border)] bg-[var(--muted)]'
                          }`}
                        >
                          <span
                            className={`pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-[var(--primary-foreground)] transition-transform ${
                              row.enabled ? 'translate-x-4' : ''
                            }`}
                          />
                        </button>
                      </td>
                      <td className="px-3 py-2 text-xs text-[var(--muted-foreground)]">{formatTime(row.last_run_at)}</td>
                      <td className="px-3 py-2 text-xs text-[var(--muted-foreground)]">{formatTime(row.next_run_at)}</td>
                      <td className="px-3 py-2 text-xs">
                        {row.last_error ? (
                          <span className="text-red-400" title={row.last_error}>
                            {row.last_error.length > 40 ? `${row.last_error.slice(0, 40)}…` : row.last_error}
                          </span>
                        ) : (
                          <span className="text-[var(--muted-foreground)]">—</span>
                        )}
                      </td>
                      <td className="px-3 py-2 text-right">
                        <div className="flex justify-end gap-1">
                          <button
                            type="button"
                            onClick={() => openEdit(row)}
                            className="rounded p-1 text-[var(--muted-foreground)] hover:bg-[var(--secondary)] hover:text-[var(--foreground)]"
                            title="Edit"
                          >
                            <Pencil size={14} />
                          </button>
                          <button
                            type="button"
                            onClick={() => handleDelete(row)}
                            className="rounded p-1 text-[var(--muted-foreground)] hover:bg-red-500/10 hover:text-red-400"
                            title="Delete"
                          >
                            <Trash2 size={14} />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {showCatalog && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div className="w-full max-w-lg rounded-lg border border-[var(--border)] bg-[var(--card)] shadow-xl">
            <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3">
              <h3 className="text-sm font-medium text-[var(--foreground)]">Automation catalog</h3>
              <button
                type="button"
                onClick={() => setShowCatalog(false)}
                className="text-[var(--muted-foreground)] hover:text-[var(--foreground)]"
              >
                ×
              </button>
            </div>
            <div className="max-h-[60vh] overflow-y-auto px-5 py-4">
              {catalogLoading ? (
                <p className="text-sm text-[var(--muted-foreground)]">Loading templates…</p>
              ) : catalog.length === 0 ? (
                <p className="text-sm text-[var(--muted-foreground)]">No templates available.</p>
              ) : (
                <ul className="space-y-2">
                  {catalog.map((tpl) => (
                    <li key={tpl.key}>
                      <button
                        type="button"
                        onClick={() => applyCatalogTemplate(tpl)}
                        className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-4 py-3 text-left hover:border-[var(--primary)] hover:bg-[var(--secondary)]/50 transition-colors"
                      >
                        <div className="text-sm font-medium text-[var(--foreground)]">{tpl.title}</div>
                        <div className="mt-1 text-xs text-[var(--muted-foreground)]">{tpl.description}</div>
                        <div className="mt-2 font-mono text-[10px] text-[var(--muted-foreground)]">
                          {tpl.suggested_cron}
                          {tpl.skill_ref ? ` · ${tpl.skill_ref}` : ''}
                        </div>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </div>
      )}

      {showForm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div className="w-full max-w-lg rounded-lg border border-[var(--border)] bg-[var(--card)] shadow-xl">
            <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3">
              <h3 className="text-sm font-medium text-[var(--foreground)]">
                {editing ? 'Edit automation' : 'New automation'}
              </h3>
              <button type="button" onClick={closeForm} className="text-[var(--muted-foreground)] hover:text-[var(--foreground)]">
                ×
              </button>
            </div>
            <div className="space-y-4 px-5 py-4">
              <div>
                <label className="mb-1 block text-xs text-[var(--muted-foreground)]">Name</label>
                <input
                  type="text"
                  value={form.name}
                  onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                  className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-1.5 text-sm text-[var(--foreground)] outline-none focus:border-[var(--primary)]"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs text-[var(--muted-foreground)]">Cron</label>
                <input
                  type="text"
                  value={form.cron}
                  onChange={(e) => setForm((f) => ({ ...f, cron: e.target.value }))}
                  placeholder="0 9 * * 1-5 or @daily"
                  className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-1.5 text-sm text-[var(--foreground)] outline-none focus:border-[var(--primary)]"
                />
                <p className="mt-1 text-[10px] text-[var(--muted-foreground)]">Standard cron or @daily, @hourly, @every 1h</p>
              </div>
              <div>
                <label className="mb-1 block text-xs text-[var(--muted-foreground)]">IM channel</label>
                <select
                  value={form.channel_id}
                  onChange={(e) => setForm((f) => ({ ...f, channel_id: e.target.value }))}
                  className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-1.5 text-sm text-[var(--foreground)] outline-none focus:border-[var(--primary)]"
                >
                  <option value="">Select channel…</option>
                  {channels.map((ch) => (
                    <option key={ch.id} value={ch.id}>
                      {channelLabel(ch.id)}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="mb-1 block text-xs text-[var(--muted-foreground)]">Skill ref</label>
                <input
                  type="text"
                  value={form.skill_ref}
                  onChange={(e) => setForm((f) => ({ ...f, skill_ref: e.target.value }))}
                  placeholder="draft:&lt;id&gt; or git:path/to/SKILL.md"
                  list="playground-skills"
                  className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-1.5 text-sm text-[var(--foreground)] outline-none focus:border-[var(--primary)]"
                />
                {skills.length > 0 && (
                  <datalist id="automation-skills">
                    {skills.map((s) => (
                      <option key={s.id} value={`draft:${s.id}`} label={s.name} />
                    ))}
                  </datalist>
                )}
              </div>
              <div>
                <label className="mb-1 block text-xs text-[var(--muted-foreground)]">Prompt</label>
                <textarea
                  value={form.prompt}
                  onChange={(e) => setForm((f) => ({ ...f, prompt: e.target.value }))}
                  rows={3}
                  className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-1.5 text-sm text-[var(--foreground)] outline-none focus:border-[var(--primary)]"
                />
              </div>
              <label className="flex items-center gap-2 text-sm text-[var(--foreground)]">
                <input
                  type="checkbox"
                  checked={form.enabled}
                  onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
                  className="rounded border-[var(--border)]"
                />
                Enabled
              </label>
            </div>
            <div className="flex justify-end gap-2 border-t border-[var(--border)] px-5 py-3">
              <button
                type="button"
                onClick={closeForm}
                className="rounded-md border border-[var(--border)] bg-[var(--card)] px-4 py-1.5 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--secondary)]"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleSave}
                disabled={saving}
                className="rounded-md bg-[var(--primary)] px-4 py-1.5 text-xs font-medium text-[var(--primary-foreground)] hover:opacity-90 disabled:opacity-50"
              >
                {saving ? 'Saving…' : editing ? 'Save changes' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}
