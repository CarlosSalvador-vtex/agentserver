import { useEffect, useState, useCallback } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { Save, Play, Send, ArrowLeft, Loader2, Plus, X, FileDiff, History } from 'lucide-react'
import {
  getPlaygroundSkill,
  patchPlaygroundSkill,
  promotePlaygroundSkill,
  dryRunPlaygroundSkill,
  listWorkspaces,
  type PlaygroundSkillFull,
  type PlaygroundDryRunResponse,
  type Workspace,
} from '../lib/api'
import { PromotedDiff } from './PromotedDiff'
import { DraftAuditTimeline } from './DraftAuditTimeline'

export function PlaygroundSkillEditor() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [draft, setDraft] = useState<PlaygroundSkillFull | null>(null)
  const [activeFile, setActiveFile] = useState<string>('')
  const [files, setFiles] = useState<Record<string, string>>({})
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [userMessage, setUserMessage] = useState('')
  const [dryRun, setDryRun] = useState<PlaygroundDryRunResponse | null>(null)
  const [soulRef, setSoulRef] = useState('')
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [dryRunWorkspaceID, setDryRunWorkspaceID] = useState('')
  const [view, setView] = useState<'files' | 'diff' | 'audit'>('files')

  useEffect(() => {
    listWorkspaces()
      .then((ws) => {
        setWorkspaces(ws)
        if (ws.length > 0 && !dryRunWorkspaceID) setDryRunWorkspaceID(ws[0].id)
      })
      .catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const load = useCallback(async () => {
    if (!id) return
    try {
      const d = await getPlaygroundSkill(id)
      setDraft(d)
      setFiles(d.files ?? {})
      const first = Object.keys(d.files ?? {})[0] ?? ''
      setActiveFile(first)
      setDirty(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load')
    }
  }, [id])

  useEffect(() => {
    load()
  }, [load])

  const handleSave = async () => {
    if (!id) return
    setSaving(true)
    setError(null)
    try {
      await patchPlaygroundSkill(id, files)
      setDirty(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'save failed')
    } finally {
      setSaving(false)
    }
  }

  const handleDryRun = async () => {
    if (!id) return
    setRunning(true)
    setError(null)
    try {
      const out = await dryRunPlaygroundSkill(id, {
        user_message: userMessage,
        soul_ref: soulRef || undefined,
        workspace_id: dryRunWorkspaceID || undefined,
      })
      setDryRun(out)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'dry-run failed')
    } finally {
      setRunning(false)
    }
  }

  const handlePromote = async () => {
    if (!id) return
    if (!confirm('Promote this draft? Opens a PR on the agentserver repo.')) return
    try {
      const r = await promotePlaygroundSkill(id)
      window.open(r.pr_url, '_blank')
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'promote failed')
    }
  }

  const handleAddFile = () => {
    const name = prompt('New file path (e.g. references/leads.json)')
    if (!name) return
    setFiles({ ...files, [name]: '' })
    setActiveFile(name)
    setDirty(true)
  }

  const handleDeleteFile = (path: string) => {
    if (!confirm(`Remove ${path} from draft?`)) return
    const next = { ...files }
    delete next[path]
    setFiles(next)
    if (activeFile === path) {
      setActiveFile(Object.keys(next)[0] ?? '')
    }
    setDirty(true)
  }

  const updateActiveContent = (content: string) => {
    setFiles({ ...files, [activeFile]: content })
    setDirty(true)
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
          title={dirty ? 'Save first' : ''}
        >
          <Send size={12} /> Promote → PR
        </button>
      </header>

      {error && (
        <div className="bg-red-500/10 px-5 py-2 text-xs text-red-400 border-b border-red-500/30">{error}</div>
      )}

      <div className="flex flex-1 overflow-hidden">
        {/* File tree */}
        <aside className="w-56 shrink-0 border-r border-[var(--border)] bg-[var(--card)]/50 p-2">
          <div className="flex items-center justify-between px-2 py-1">
            <span className="text-[10px] uppercase tracking-wide text-[var(--muted-foreground)]">Files</span>
            <button onClick={handleAddFile} className="rounded p-0.5 hover:bg-[var(--secondary)]" title="Add file">
              <Plus size={12} />
            </button>
          </div>
          <ul className="mt-1 space-y-0.5">
            {Object.keys(files).sort().map((path) => (
              <li
                key={path}
                onClick={() => setActiveFile(path)}
                className={`group flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer ${
                  path === activeFile ? 'bg-[var(--secondary)] text-[var(--foreground)]' : 'text-[var(--muted-foreground)] hover:bg-[var(--secondary)]/50'
                }`}
              >
                <span className="flex-1 truncate font-mono">{path}</span>
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    handleDeleteFile(path)
                  }}
                  className="opacity-0 group-hover:opacity-100 hover:text-red-400"
                  title="Remove file"
                >
                  <X size={10} />
                </button>
              </li>
            ))}
          </ul>
        </aside>

        {/* Editor */}
        <main className="flex flex-1 flex-col">
          <div className="flex items-center gap-1 border-b border-[var(--border)] bg-[var(--card)]/50 px-3 py-1.5">
            <button
              onClick={() => setView('files')}
              className={`rounded px-2 py-0.5 text-[11px] font-medium ${
                view === 'files' ? 'bg-[var(--secondary)] text-[var(--foreground)]' : 'text-[var(--muted-foreground)] hover:bg-[var(--secondary)]/50'
              }`}
            >
              Files
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
          </div>
          {view === 'diff' && draft.promoted_commit ? (
            <div className="flex-1 overflow-auto">
              <PromotedDiff
                commit={draft.promoted_commit}
                skillName={draft.name}
                draftFiles={files}
              />
            </div>
          ) : view === 'audit' ? (
            <div className="flex-1 overflow-auto">
              <DraftAuditTimeline kind="skills" draftID={draft.id} />
            </div>
          ) : activeFile ? (
            <textarea
              value={files[activeFile] ?? ''}
              onChange={(e) => updateActiveContent(e.target.value)}
              spellCheck={false}
              className="flex-1 resize-none bg-[var(--background)] p-4 font-mono text-sm text-[var(--foreground)] outline-none"
            />
          ) : (
            <div className="flex flex-1 items-center justify-center text-[var(--muted-foreground)] text-sm">
              No file selected. Add one from the left panel.
            </div>
          )}
        </main>

        {/* Dry-run panel */}
        <aside className="w-96 shrink-0 border-l border-[var(--border)] bg-[var(--card)]/30 flex flex-col">
          <div className="px-4 py-3 border-b border-[var(--border)]">
            <div className="text-[10px] uppercase tracking-wide text-[var(--muted-foreground)] mb-2">Dry-run</div>
            <input
              type="text"
              placeholder="Soul ref (optional, draft:xxx or git:name@sha)"
              value={soulRef}
              onChange={(e) => setSoulRef(e.target.value)}
              className="mb-2 w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
            />
            {workspaces.length > 1 && (
              <select
                value={dryRunWorkspaceID}
                onChange={(e) => setDryRunWorkspaceID(e.target.value)}
                title="Workspace whose LLM quota / BYOK config funds this dry-run"
                className="mb-2 w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-2 py-1 text-xs text-[var(--foreground)]"
              >
                {workspaces.map((w) => (
                  <option key={w.id} value={w.id}>
                    {w.name} ({w.id.slice(0, 8)})
                  </option>
                ))}
              </select>
            )}
            <textarea
              placeholder="User message (e.g. /cobranca lead L-001)"
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
          </div>
          <div className="flex-1 overflow-auto p-4 text-xs space-y-3">
            {dryRun && (
              <>
                {dryRun.completion && (
                  <div>
                    <div className="text-[var(--muted-foreground)] mb-1">Completion ({dryRun.completion_model})</div>
                    <div className="whitespace-pre-wrap rounded-md border border-green-500/30 bg-green-500/5 p-2 text-[var(--foreground)]">
                      {dryRun.completion}
                    </div>
                  </div>
                )}
                {dryRun.completion_error && (
                  <div>
                    <div className="text-[var(--muted-foreground)] mb-1">LLM error</div>
                    <div className="whitespace-pre-wrap rounded-md border border-red-500/30 bg-red-500/5 p-2 text-red-400">
                      {dryRun.completion_error}
                    </div>
                  </div>
                )}
                <div>
                  <div className="text-[var(--muted-foreground)] mb-1">Composed system prompt</div>
                  <pre className="whitespace-pre-wrap rounded-md border border-[var(--border)] bg-[var(--background)] p-2 font-mono text-[11px] text-[var(--foreground)]">
                    {dryRun.system_prompt || '(empty)'}
                  </pre>
                </div>
                {dryRun.tools.length > 0 && (
                  <div>
                    <div className="text-[var(--muted-foreground)] mb-1">Tools</div>
                    <ul className="rounded-md border border-[var(--border)] bg-[var(--background)] p-2">
                      {dryRun.tools.map((t) => (
                        <li key={t.name} className="font-mono text-[11px] text-[var(--foreground)]">
                          {t.name}
                          {t.description && <span className="text-[var(--muted-foreground)]"> — {t.description}</span>}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </>
            )}
          </div>
        </aside>
      </div>

      <div className="border-t border-[var(--border)] bg-[var(--card)] px-5 py-2 text-[11px] text-[var(--muted-foreground)]">
        <Link to="/playground" className="hover:text-[var(--foreground)]">
          ← back to catalog
        </Link>
      </div>
    </div>
  )
}
