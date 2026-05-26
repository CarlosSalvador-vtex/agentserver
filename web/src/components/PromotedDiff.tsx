import { useEffect, useMemo, useState } from 'react'
import ReactDiffViewer, { DiffMethod } from 'react-diff-viewer-continued'
import { Loader2 } from 'lucide-react'

// Sprint 2 PR-6 (improvements.md #7). Lazy-loads promoted files from
// GitHub Contents API and renders a side-by-side diff vs the current
// draft files.
//
// Repo + base path are fixed for now (skills live in
// deploy/helm/agentserver/skills/{name}/<path>). When tenant-scoped
// catalog (#17) lands, the prefix becomes per-workspace.

interface PromotedDiffProps {
  // Repo (owner/repo) that the promote PR landed in. Defaults match
  // the agentserver fork.
  repo?: string
  // Commit SHA the promote landed at (skill_drafts.promoted_commit).
  commit: string
  // Skill name — paths are deploy/helm/agentserver/skills/<name>/<file>.
  skillName: string
  // Current draft files (path → content).
  draftFiles: Record<string, string>
}

const DEFAULT_REPO = 'CarlosSalvador-vtex/agentserver'

export function PromotedDiff({ repo = DEFAULT_REPO, commit, skillName, draftFiles }: PromotedDiffProps) {
  const [promoted, setPromoted] = useState<Record<string, string> | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const paths = useMemo(() => Object.keys(draftFiles).sort(), [draftFiles])

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    Promise.all(
      paths.map(async (path) => {
        const repoPath = `deploy/helm/agentserver/skills/${skillName}/${path}`
        const url = `https://api.github.com/repos/${repo}/contents/${encodeURI(repoPath)}?ref=${encodeURIComponent(commit)}`
        const r = await fetch(url, { headers: { Accept: 'application/vnd.github.raw' } })
        if (r.status === 404) return [path, ''] as [string, string]
        if (!r.ok) throw new Error(`${repoPath}: HTTP ${r.status}`)
        const text = await r.text()
        return [path, text] as [string, string]
      }),
    )
      .then((pairs) => {
        if (cancelled) return
        setPromoted(Object.fromEntries(pairs))
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [commit, repo, skillName, paths])

  if (loading) {
    return (
      <div className="flex items-center gap-2 p-4 text-xs text-[var(--muted-foreground)]">
        <Loader2 size={14} className="animate-spin" />
        Loading promoted files at {commit.slice(0, 7)}…
      </div>
    )
  }
  if (error) {
    return <div className="p-4 text-xs text-red-400">Diff load failed: {error}</div>
  }
  if (!promoted) return null

  return (
    <div className="flex flex-col gap-4 p-4">
      {paths.map((path) => {
        const oldText = promoted[path] ?? ''
        const newText = draftFiles[path] ?? ''
        if (oldText === newText) {
          return (
            <details key={path} className="rounded border border-[var(--border)] bg-[var(--card)]/30 text-xs">
              <summary className="cursor-pointer px-3 py-2 text-[var(--muted-foreground)]">
                {path} <span className="text-[10px] italic">(unchanged)</span>
              </summary>
            </details>
          )
        }
        return (
          <div key={path} className="rounded border border-[var(--border)] bg-[var(--card)]/30 overflow-hidden text-xs">
            <div className="px-3 py-2 border-b border-[var(--border)] font-medium text-[var(--foreground)]">
              {path}
            </div>
            <ReactDiffViewer
              oldValue={oldText}
              newValue={newText}
              splitView
              compareMethod={DiffMethod.LINES}
              hideLineNumbers={false}
              useDarkTheme
              styles={{
                contentText: { fontFamily: 'ui-monospace, SFMono-Regular, monospace', fontSize: 11 },
              }}
            />
          </div>
        )
      })}
    </div>
  )
}
