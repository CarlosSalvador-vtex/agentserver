import { useState } from 'react'
import { X, MessageCircle, Loader2, CheckCircle2, Copy, Check } from 'lucide-react'
import { workspaceWhatsAppConfigure } from '../lib/api'

interface WhatsAppConfigModalProps {
  workspaceId: string
  onClose: () => void
  onConnected: () => void
}

export function WhatsAppConfigModal({ workspaceId, onClose, onConnected }: WhatsAppConfigModalProps) {
  const [phoneNumberID, setPhoneNumberID] = useState('')
  const [accessToken, setAccessToken] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [status, setStatus] = useState<'idle' | 'loading' | 'connected' | 'error'>('idle')
  const [error, setError] = useState('')
  const [connectedBotID, setConnectedBotID] = useState('')
  const [webhookCopied, setWebhookCopied] = useState(false)

  const webhookURL = typeof window !== 'undefined' ? `${window.location.origin}/webhook/whatsapp` : ''

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!phoneNumberID.trim() || !accessToken.trim()) return

    setStatus('loading')
    setError('')
    try {
      const result = await workspaceWhatsAppConfigure(
        workspaceId,
        phoneNumberID.trim(),
        accessToken.trim(),
        baseURL.trim() || undefined,
      )
      setConnectedBotID(result.bot_id)
      setStatus('connected')
      onConnected()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to configure WhatsApp')
      setStatus('error')
    }
  }

  const copyWebhook = async () => {
    try {
      await navigator.clipboard.writeText(webhookURL)
      setWebhookCopied(true)
      setTimeout(() => setWebhookCopied(false), 2000)
    } catch {
      /* ignore — clipboard blocked */
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm">
      <div className="relative w-full max-w-lg rounded-xl border border-[var(--border)] bg-[var(--card)] p-6 shadow-2xl">
        <button
          onClick={onClose}
          className="absolute right-4 top-4 text-[var(--muted-foreground)] hover:text-[var(--foreground)]"
        >
          <X size={16} />
        </button>

        <div className="flex items-center gap-2 mb-4">
          <MessageCircle size={20} className="text-emerald-500" />
          <h2 className="text-lg font-semibold text-[var(--foreground)]">Configure WhatsApp Cloud</h2>
        </div>

        {status === 'connected' ? (
          <div className="flex flex-col gap-3 py-4">
            <div className="flex items-center gap-2">
              <CheckCircle2 size={20} className="text-green-400" />
              <p className="text-sm text-[var(--foreground)]">
                WhatsApp number <span className="font-mono font-medium">{connectedBotID}</span> connected.
              </p>
            </div>
            <div className="rounded-md border border-[var(--border)] bg-[var(--background)] p-3 text-xs text-[var(--muted-foreground)]">
              <p className="mb-2 text-[var(--foreground)] font-medium">Webhook setup (one-time)</p>
              <p>In the Meta App Dashboard → Webhooks → WhatsApp Business Account, paste:</p>
              <div className="mt-2 flex items-center gap-2">
                <code className="flex-1 rounded bg-[var(--secondary)] px-2 py-1 font-mono text-[11px] text-[var(--foreground)]">
                  {webhookURL}
                </code>
                <button
                  onClick={copyWebhook}
                  className="rounded p-1 hover:bg-[var(--secondary)]"
                  aria-label="Copy webhook URL"
                  title="Copy webhook URL"
                >
                  {webhookCopied ? <Check size={13} className="text-green-400" /> : <Copy size={13} />}
                </button>
              </div>
              <p className="mt-2">
                Set the <span className="font-mono">Verify Token</span> field to the value of{' '}
                <span className="font-mono">WHATSAPP_WEBHOOK_VERIFY_TOKEN</span> configured on the agentserver.
              </p>
            </div>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="flex flex-col gap-3">
            <p className="text-xs text-[var(--muted-foreground)]">
              Paste the WhatsApp Business <span className="font-mono">phone_number_id</span> and a long-lived
              access token from your Meta App Dashboard. The same agentserver instance can serve multiple
              numbers via webhook routing on <span className="font-mono">phone_number_id</span>.
            </p>

            <label className="flex flex-col gap-1">
              <span className="text-xs text-[var(--muted-foreground)]">Phone Number ID</span>
              <input
                type="text"
                value={phoneNumberID}
                onChange={(e) => setPhoneNumberID(e.target.value)}
                placeholder="112233445566778"
                autoFocus
                disabled={status === 'loading'}
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm font-mono text-[var(--foreground)] placeholder:text-[var(--muted-foreground)] focus:outline-none focus:ring-1 focus:ring-[var(--primary)] disabled:opacity-50"
              />
            </label>

            <label className="flex flex-col gap-1">
              <span className="text-xs text-[var(--muted-foreground)]">Access Token</span>
              <input
                type="password"
                value={accessToken}
                onChange={(e) => setAccessToken(e.target.value)}
                placeholder="EAAGZ..."
                disabled={status === 'loading'}
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm font-mono text-[var(--foreground)] placeholder:text-[var(--muted-foreground)] focus:outline-none focus:ring-1 focus:ring-[var(--primary)] disabled:opacity-50"
              />
            </label>

            <label className="flex flex-col gap-1">
              <span className="text-xs text-[var(--muted-foreground)]">
                Graph API Base URL <span className="opacity-60">(optional)</span>
              </span>
              <input
                type="text"
                value={baseURL}
                onChange={(e) => setBaseURL(e.target.value)}
                placeholder="https://graph.facebook.com/v18.0"
                disabled={status === 'loading'}
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm font-mono text-[var(--foreground)] placeholder:text-[var(--muted-foreground)] focus:outline-none focus:ring-1 focus:ring-[var(--primary)] disabled:opacity-50"
              />
            </label>

            {error && <p className="text-xs text-red-400">{error}</p>}

            <button
              type="submit"
              disabled={!phoneNumberID.trim() || !accessToken.trim() || status === 'loading'}
              className="mt-2 w-full inline-flex items-center justify-center gap-2 rounded-md bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {status === 'loading' ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Connecting...
                </>
              ) : (
                'Connect WhatsApp'
              )}
            </button>
          </form>
        )}
      </div>
    </div>
  )
}
