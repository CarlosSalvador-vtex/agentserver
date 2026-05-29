import { useEffect, useState, useCallback } from 'react'
import { useParams, useNavigate, useSearchParams } from 'react-router-dom'
import { Save, Play, Send, ArrowLeft, Loader2, Plus, X, FileDiff, History, FlaskConical, RotateCw, ExternalLink } from 'lucide-react'
import {
  getPlaygroundSkill,
  patchPlaygroundSkill,
  publishPlaygroundSkill,
  dryRunPlaygroundSkill,
  listWorkspaces,
  spawnPlaygroundTestSandbox,
  PLAYGROUND_DRYRUN_MODELS,
  type PlaygroundSkillFull,
  type PlaygroundDryRunResponse,
  type PlaygroundTestSandboxResponse,
  type Workspace,
} from '../lib/api'
import { PromotedDiff } from './PromotedDiff'
import { PromotedPRBanner } from './PromotedPRBanner'
import { DraftAuditTimeline } from './DraftAuditTimeline'
import { MarketplaceVisibilityToggle } from './MarketplaceVisibilityToggle'

export function PlaygroundSkillEditor({ isDevMode }: { isDevMode?: boolean }) {
  const { id, workspaceId } = useParams<{ id: string; workspaceId: string }>()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const backTo = searchParams.get('from') === 'admin' ? '/admin/skills' : '/playground'
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
  const [dryRunModel, setDryRunModel] = useState<string>(PLAYGROUND_DRYRUN_MODELS[0])
  const [view, setView] = useState<'files' | 'diff' | 'audit'>('files')
  const [testSandbox, setTestSandbox] = useState<PlaygroundTestSandboxResponse | null>(null)
  const [spawningTest, setSpawningTest] = useState(false)
  const [testError, setTestError] = useState<string | null>(null)
  const [promoteConfirm, setPromoteConfirm] = useState(false)
  const [addingFile, setAddingFile] = useState(false)
  const [newFileName, setNewFileName] = useState('')
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null)

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
        model: dryRunModel || undefined,
      })
      setDryRun(out)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'dry-run failed')
    } finally {
      setRunning(false)
    }
  }

  const handlePublish = async () => {
    if (!id) return
    setPromoteConfirm(false)
    try {
      await publishPlaygroundSkill(id)
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'publish failed')
    }
  }

  // B3 — spawn an ephemeral test sandbox running this draft so authors can
  // exercise tools end-to-end without leaving the editor. B4 follow-up:
  // when a sandbox already exists for this draft, trigger a rolling
  // refresh instead of spawning a new one.
  const handleTestSandbox = async () => {
    if (!id) return
    if (!dryRunWorkspaceID) {
      setTestError('Pick a workspace in the dry-run panel first.')
      return
    }
    setSpawningTest(true)
    setTestError(null)
    try {
      const r = await spawnPlaygroundTestSandbox(id, {
        workspace_id: dryRunWorkspaceID,
        sandbox_type: 'openclaw',
      })
      setTestSandbox(r)
    } catch (e) {
      setTestError(e instanceof Error ? e.message : 'test sandbox failed')
    } finally {
      setSpawningTest(false)
    }
  }

  const handleAddFile = () => {
    if (!newFileName.trim()) return
    const name = newFileName.trim()
    setFiles({ ...files, [name]: '' })
    setActiveFile(name)
    setDirty(true)
    setNewFileName('')
    setAddingFile(false)
  }

  const handleDeleteFile = (path: string) => {
    setDeleteConfirm(null)
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
      {!isDevMode ? (
        <header className="flex items-center gap-3 border-b border-[var(--border)] bg-[var(--card)] px-5 py-3">
          <button onClick={() => navigate(backTo)} className="text-[var(--muted-foreground)] hover:text-[var(--foreground)]">
            <ArrowLeft size={16} />
          </button>
          <div className="flex-1 min-w-0">
            <div className="text-sm font-semibold text-[var(--foreground)]">Funcionalidade: {draft.name}</div>
          </div>
          {dirty && <span className="text-xs text-yellow-400">● unsaved</span>}
          <button
            onClick={handleSave}
            disabled={!dirty || saving}
            className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--card)] px-3 py-1 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--secondary)] disabled:opacity-40"
          >
            {saving ? <Loader2 size={12} className="animate-spin" /> : <Save size={12} />}
            Save
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
            kind="skill"
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
          <button
            onClick={handleTestSandbox}
            disabled={spawningTest || dirty}
            title={dirty ? 'Save first' : 'Spin up an ephemeral OpenClaw sandbox with this draft'}
            className="inline-flex items-center gap-1 rounded-md border border-blue-500/30 bg-blue-500/10 px-3 py-1 text-xs font-medium text-blue-400 hover:bg-blue-500/20 disabled:opacity-40"
          >
            {spawningTest ? (
              <Loader2 size={12} className="animate-spin" />
            ) : testSandbox ? (
              <RotateCw size={12} />
            ) : (
              <FlaskConical size={12} />
            )}
            {testSandbox ? 'Recreate test sandbox' : 'Open test sandbox'}
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

      <div className="flex flex-1 overflow-hidden">
        {/* File tree */}
        <aside className="w-56 shrink-0 border-r border-[var(--border)] bg-[var(--card)]/50 p-2">
          <div className="flex items-center justify-between px-2 py-1">
            <span className="text-[10px] uppercase tracking-wide text-[var(--muted-foreground)]">Files</span>
            <button onClick={() => { setAddingFile(true); setNewFileName('') }} className="rounded p-0.5 hover:bg-[var(--secondary)]" title="Add file">
              <Plus size={12} />
            </button>
          </div>
          {addingFile && (
            <div className="px-2 py-1 flex gap-1">
              <input
                autoFocus
                value={newFileName}
                onChange={(e) => setNewFileName(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') handleAddFile(); if (e.key === 'Escape') setAddingFile(false) }}
                placeholder="path/to/file.js"
                className="flex-1 rounded border border-[var(--border)] bg-[var(--background)] px-1 py-0.5 text-[10px] font-mono text-[var(--foreground)]"
              />
              <button onClick={handleAddFile} className="text-[10px] text-green-400 hover:text-green-300">Add</button>
              <button onClick={() => setAddingFile(false)} className="text-[10px] text-[var(--muted-foreground)]">✕</button>
            </div>
          )}
          <ul className="mt-1 space-y-0.5">
            {Object.keys(files).sort().map((path) => (
              <li
                key={path}
                onClick={() => { setActiveFile(path); setDeleteConfirm(null) }}
                className={`group flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer ${
                  path === activeFile ? 'bg-[var(--secondary)] text-[var(--foreground)]' : 'text-[var(--muted-foreground)] hover:bg-[var(--secondary)]/50'
                }`}
              >
                <span className="flex-1 truncate font-mono">{path}</span>
                {deleteConfirm === path ? (
                  <>
                    <button onClick={(e) => { e.stopPropagation(); handleDeleteFile(path) }} className="text-[9px] text-red-400 hover:text-red-300">rm</button>
                    <button onClick={(e) => { e.stopPropagation(); setDeleteConfirm(null) }} className="text-[9px] text-[var(--muted-foreground)]">✕</button>
                  </>
                ) : (
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    setDeleteConfirm(path)
                  }}
                  className="opacity-0 group-hover:opacity-100 hover:text-red-400"
                  title="Remove file"
                >
                  <X size={10} />
                </button>
                )}
              </li>
            ))}
          </ul>
        </aside>

        {/* Editor */}
        <main className="flex flex-1 flex-col">
          {isDevMode && (
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
              <PromotedDiff
                commit={draft.promoted_commit}
                skillName={draft.name}
                draftFiles={files}
              />
            </div>
          ) : isDevMode && view === 'audit' ? (
            <div className="flex-1 overflow-auto">
              <DraftAuditTimeline kind="skills" draftID={draft.id} />
            </div>
          ) : activeFile ? (
            <textarea
              value={files[activeFile] ?? ''}
              onChange={(e) => { if (isDevMode) updateActiveContent(e.target.value) }}
              readOnly={!isDevMode}
              spellCheck={false}
              className="flex-1 resize-none bg-[var(--background)] p-4 font-mono text-sm text-[var(--foreground)] outline-none"
            />
          ) : (
            <div className="flex flex-1 items-center justify-center text-[var(--muted-foreground)] text-sm">
              No file selected. Add one from the left panel.
            </div>
          )}
        </main>

        {/* Dry-run panel — dev mode only */}
        {isDevMode && <aside className="w-96 shrink-0 border-l border-[var(--border)] bg-[var(--card)]/30 flex flex-col">
          {(testSandbox || testError) && (
            <div className="border-b border-[var(--border)] bg-[var(--background)] px-4 py-3">
              <div className="text-[10px] uppercase tracking-wide text-[var(--muted-foreground)] mb-1">
                Test sandbox
              </div>
              {testError && <p className="text-xs text-red-500">{testError}</p>}
              {testSandbox && (
                <div className="space-y-1 text-xs">
                  <div className="flex items-center justify-between">
                    <code className="text-[var(--muted-foreground)]">{testSandbox.sandbox_id.slice(0, 8)}</code>
                    <a
                      href={`/w/${workspaceId}/sandboxes/${encodeURIComponent(testSandbox.sandbox_id)}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-1 text-blue-400 hover:underline"
                    >
                      Open <ExternalLink size={10} />
                    </a>
                  </div>
                  <div className="text-[var(--muted-foreground)]">
                    Expires {new Date(testSandbox.expires_at).toLocaleString()} ·
                    strategy {testSandbox.strategy}
                  </div>
                </div>
              )}
            </div>
          )}
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
                {(dryRun.tools?.length ?? 0) > 0 && (
                  <div>
                    <div className="text-[var(--muted-foreground)] mb-1">Tools</div>
                    <ul className="rounded-md border border-[var(--border)] bg-[var(--background)] p-2">
                      {dryRun.tools!.map((t) => (
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
        </aside>}
      </div>

      <div className="border-t border-[var(--border)] bg-[var(--card)] px-5 py-2 text-[11px] text-[var(--muted-foreground)]">
        <button onClick={() => navigate(backTo)} className="hover:text-[var(--foreground)]">
          ← back to catalog
        </button>
      </div>
    </div>
  )
}
