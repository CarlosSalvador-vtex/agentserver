import { useState, type FormEvent } from 'react'
import { X, Copy, Check } from 'lucide-react'
import { createInvite, type InviteResponse } from '../lib/api'

interface InviteMemberModalProps {
  workspaceId: string
  onClose: () => void
  onCreated?: (inv: InviteResponse) => void
}

export function InviteMemberModal({ workspaceId, onClose, onCreated }: InviteMemberModalProps) {
  const [email, setEmail] = useState('')
  const [role, setRole] = useState('developer')
  const [submitting, setSubmitting] = useState(false)
  const [created, setCreated] = useState<InviteResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      const inv = await createInvite(workspaceId, email.trim(), role)
      setCreated(inv)
      onCreated?.(inv)
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(
        msg.includes('409') || msg.toLowerCase().includes('already pending')
          ? 'An invite is already pending for that email. Revoke it first or wait for the user to accept.'
          : msg.includes('400')
            ? 'Invalid email.'
            : 'Failed to create invite.',
      )
    } finally {
      setSubmitting(false)
    }
  }

  async function handleCopy() {
    if (!created?.invite_url) return
    await navigator.clipboard.writeText(created.invite_url)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="w-full max-w-md rounded-lg border border-[var(--border)] bg-[var(--card)] p-6 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold text-[var(--foreground)]">
            {created ? 'Invite ready' : 'Invite member'}
          </h2>
          <button type="button" onClick={onClose} className="rounded p-1 hover:bg-[var(--secondary)]">
            <X size={16} />
          </button>
        </div>

        {created ? (
          <div className="flex flex-col gap-3">
            <p className="text-sm text-[var(--muted-foreground)]">
              Invite sent to <strong>{created.email}</strong> as <code>{created.role}</code>. The
              link below works once — share it via email if the mailer is offline.
            </p>
            <div className="flex items-center gap-2 rounded-md border border-[var(--border)] bg-[var(--background)] p-2">
              <code className="flex-1 truncate font-mono text-xs">{created.invite_url}</code>
              <button
                type="button"
                onClick={handleCopy}
                className="flex items-center gap-1 rounded px-2 py-1 text-xs hover:bg-[var(--secondary)]"
              >
                {copied ? <Check size={14} /> : <Copy size={14} />}
                {copied ? 'Copied' : 'Copy'}
              </button>
            </div>
            <p className="text-xs text-[var(--muted-foreground)]">
              Expires {new Date(created.expires_at).toLocaleString()}.
            </p>
            <div className="mt-2 flex justify-end">
              <button
                type="button"
                onClick={onClose}
                className="rounded-md bg-[var(--primary)] px-4 py-2 text-sm font-medium text-white hover:opacity-90"
              >
                Done
              </button>
            </div>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div>
              <label className="mb-1 block text-sm font-medium text-[var(--foreground)]">Email</label>
              <input
                type="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="alice@empresa.com"
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm"
                autoFocus
              />
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium text-[var(--foreground)]">Role</label>
              <select
                value={role}
                onChange={(e) => setRole(e.target.value)}
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm"
              >
                <option value="viewer">viewer</option>
                <option value="developer">developer</option>
                <option value="maintainer">maintainer</option>
                <option value="owner">owner</option>
              </select>
            </div>
            {error && <p className="text-sm text-red-500">{error}</p>}
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={onClose}
                className="rounded-md border border-[var(--border)] px-4 py-2 text-sm hover:bg-[var(--secondary)]"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={submitting || !email.trim()}
                className="rounded-md bg-[var(--primary)] px-4 py-2 text-sm font-medium text-white hover:opacity-90 disabled:opacity-50"
              >
                {submitting ? 'Sending…' : 'Send invite'}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  )
}
