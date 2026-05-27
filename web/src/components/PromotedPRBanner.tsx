// Unified promote banner shared by skill + soul editors (Tier B item B5).
// Surfaces PR state (open/merged/closed/unknown) returned by the backend
// poller (improvements.md #8 / playground_promote_poll.go) so authors
// don't have to leave the editor to check GitHub.

import { GitPullRequest, GitMerge, X, CircleHelp } from 'lucide-react'

export interface PromotedPRBannerProps {
  /** PR URL — when empty, banner renders nothing */
  url?: string | null
  /** open | merged | closed | "" (poller hasn't classified yet) */
  state?: string | null
}

const STATE_STYLES: Record<string, {
  label: string
  className: string
  Icon: typeof GitPullRequest
}> = {
  open: {
    label: 'PR open',
    className: 'border-green-500/30 bg-green-500/10 text-green-400',
    Icon: GitPullRequest,
  },
  merged: {
    label: 'PR merged',
    className: 'border-purple-500/30 bg-purple-500/10 text-purple-400',
    Icon: GitMerge,
  },
  closed: {
    label: 'PR closed (not merged)',
    className: 'border-zinc-500/30 bg-zinc-500/10 text-zinc-400',
    Icon: X,
  },
}

export function PromotedPRBanner({ url, state }: PromotedPRBannerProps) {
  if (!url) return null
  const cfg = (state && STATE_STYLES[state]) || {
    label: 'PR status unknown',
    className: 'border-yellow-500/30 bg-yellow-500/10 text-yellow-400',
    Icon: CircleHelp,
  }
  const { label, className, Icon } = cfg

  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs font-medium transition-colors hover:opacity-80 ${className}`}
      title={`Open promote PR (${label})`}
    >
      <Icon size={12} />
      <span>{label}</span>
    </a>
  )
}
