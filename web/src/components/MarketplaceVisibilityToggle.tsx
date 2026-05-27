import { useState } from 'react'
import { Globe, Loader2 } from 'lucide-react'
import { setPlaygroundSkillVisibility, setPlaygroundSoulVisibility } from '../lib/api'

interface MarketplaceVisibilityToggleProps {
  kind: 'skill' | 'soul'
  draftID: string
  visibility: 'private' | 'shared'
  canSet: boolean
  onChanged: (visibility: 'private' | 'shared') => void
}

export function MarketplaceVisibilityToggle({
  kind,
  draftID,
  visibility,
  canSet,
  onChanged,
}: MarketplaceVisibilityToggleProps) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  if (!canSet) {
    return null
  }

  const next = visibility === 'shared' ? 'private' : 'shared'
  const label = visibility === 'shared' ? 'Shared on marketplace' : 'Private (workspace only)'

  const handleToggle = async () => {
    setBusy(true)
    setError(null)
    try {
      if (kind === 'skill') {
        await setPlaygroundSkillVisibility(draftID, next)
      } else {
        await setPlaygroundSoulVisibility(draftID, next)
      }
      onChanged(next)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'visibility update failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex items-center gap-2">
      {error && <span className="text-[10px] text-red-400">{error}</span>}
      <button
        type="button"
        onClick={handleToggle}
        disabled={busy}
        title={next === 'shared' ? 'Share on marketplace' : 'Remove from marketplace'}
        className={`inline-flex items-center gap-1 rounded-md border px-3 py-1 text-xs font-medium transition-colors disabled:opacity-40 ${
          visibility === 'shared'
            ? 'border-orange-500/40 bg-orange-500/10 text-orange-400 hover:bg-orange-500/20'
            : 'border-[var(--border)] bg-[var(--card)] text-[var(--muted-foreground)] hover:bg-[var(--secondary)]'
        }`}
      >
        {busy ? <Loader2 size={12} className="animate-spin" /> : <Globe size={12} />}
        {label}
      </button>
    </div>
  )
}
