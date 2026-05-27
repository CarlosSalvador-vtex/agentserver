import { useEffect, useState } from 'react'
import ReactDiffViewer, { DiffMethod } from 'react-diff-viewer-continued'
import { Loader2 } from 'lucide-react'

const DEFAULT_REPO = 'CarlosSalvador-vtex/agentserver'

interface SoulPromotedDiffProps {
  repo?: string
  commit: string
  soulName: string
  body: string
}

function parseSoulMarkdown(text: string): string {
  const trimmed = text.trimStart()
  if (!trimmed.startsWith('---')) {
    return trimmed
  }
  const closeIdx = trimmed.indexOf('\n---', 3)
  if (closeIdx === -1) {
    return trimmed
  }
  return trimmed.slice(closeIdx + 4).replace(/^\n+/, '')
}

export function SoulPromotedDiff({ repo = DEFAULT_REPO, commit, soulName, body }: SoulPromotedDiffProps) {
  const [promotedBody, setPromotedBody] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const repoPath = `deploy/helm/agentserver/souls/${soulName}/soul.md`
    const url = `https://api.github.com/repos/${repo}/contents/${encodeURI(repoPath)}?ref=${encodeURIComponent(commit)}`
    fetch(url, { headers: { Accept: 'application/vnd.github.raw' } })
      .then(async (r) => {
        if (r.status === 404) return ''
        if (!r.ok) throw new Error(`${repoPath}: HTTP ${r.status}`)
        return r.text()
      })
      .then((text) => {
        if (!cancelled) setPromotedBody(parseSoulMarkdown(text))
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
  }, [commit, repo, soulName])

  if (loading) {
    return (
      <div className="flex items-center gap-2 p-4 text-xs text-[var(--muted-foreground)]">
        <Loader2 size={14} className="animate-spin" />
        Loading promoted soul at {commit.slice(0, 7)}…
      </div>
    )
  }
  if (error) {
    return <div className="p-4 text-xs text-red-400">Diff load failed: {error}</div>
  }
  if (promotedBody === null) return null

  if (promotedBody === body) {
    return (
      <div className="p-4 text-xs text-[var(--muted-foreground)] italic">
        Body unchanged vs promoted commit {commit.slice(0, 7)}.
      </div>
    )
  }

  return (
    <div className="rounded border border-[var(--border)] bg-[var(--card)]/30 overflow-hidden text-xs m-4">
      <div className="px-3 py-2 border-b border-[var(--border)] font-medium text-[var(--foreground)]">Body</div>
      <ReactDiffViewer
        oldValue={promotedBody}
        newValue={body}
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
}
