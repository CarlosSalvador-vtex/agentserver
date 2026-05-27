import { useEffect, useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { acceptInvite, getInviteInfo, type InviteInfo } from '../lib/api'

interface AcceptInviteProps {
  onSuccess: () => void
}

export function AcceptInvite({ onSuccess }: AcceptInviteProps) {
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''

  const [info, setInfo] = useState<InviteInfo | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)

  useEffect(() => {
    if (!token) {
      setLoadError('Missing token.')
      return
    }
    let cancelled = false
    getInviteInfo(token)
      .then((info) => {
        if (!cancelled) setInfo(info)
      })
      .catch((err) => {
        if (!cancelled) {
          const msg = err instanceof Error ? err.message : String(err)
          setLoadError(
            msg.includes('404') || msg.toLowerCase().includes('invalid')
              ? 'This invite link is invalid or has expired.'
              : 'Failed to load invite.',
          )
        }
      })
    return () => {
      cancelled = true
    }
  }, [token])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setSubmitting(true)
    setSubmitError(null)
    try {
      await acceptInvite(token, password)
      onSuccess()
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setSubmitError(
        msg.includes('401')
          ? 'Wrong password for the existing account on that email.'
          : msg.includes('404') || msg.toLowerCase().includes('invalid')
            ? 'Invite expired during submission. Ask for a new one.'
            : 'Failed to accept invite.',
      )
    } finally {
      setSubmitting(false)
    }
  }

  if (loadError) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--background)]">
        <div className="w-full max-w-sm rounded-lg border border-[var(--border)] bg-[var(--card)] p-6 text-center">
          <h1 className="mb-2 text-lg font-semibold">Invite unavailable</h1>
          <p className="text-sm text-[var(--muted-foreground)]">{loadError}</p>
        </div>
      </div>
    )
  }
  if (!info) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--background)]">
        <p className="text-sm text-[var(--muted-foreground)]">Loading…</p>
      </div>
    )
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-[var(--background)]">
      <div className="w-full max-w-sm rounded-lg border border-[var(--border)] bg-[var(--card)] p-6">
        <h1 className="mb-1 text-lg font-semibold text-[var(--foreground)]">
          Join {info.workspace_name}
        </h1>
        <p className="mb-4 text-sm text-[var(--muted-foreground)]">
          You're invited to <code>{info.workspace_slug}</code> as{' '}
          <code>{info.role}</code> for <strong>{info.email}</strong>.
        </p>
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <div>
            <label className="mb-1 block text-sm font-medium text-[var(--foreground)]">
              Password
            </label>
            <input
              type="password"
              required
              minLength={8}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm"
              autoFocus
              placeholder="Pick a strong password (new account) or your existing one"
            />
            <p className="mt-1 text-xs text-[var(--muted-foreground)]">
              If you already have an agentserver account for this email, enter your existing
              password. Otherwise, this sets your new password.
            </p>
          </div>
          {submitError && <p className="text-sm text-red-500">{submitError}</p>}
          <button
            type="submit"
            disabled={submitting || password.length < 8}
            className="rounded-md bg-[var(--primary)] px-4 py-2 text-sm font-medium text-white hover:opacity-90 disabled:opacity-50"
          >
            {submitting ? 'Joining…' : `Accept invite & enter ${info.workspace_slug}`}
          </button>
        </form>
      </div>
    </div>
  )
}
