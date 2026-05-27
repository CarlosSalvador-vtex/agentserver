import { useState, type FormEvent } from 'react'
import { X } from 'lucide-react'
import { slugifyName } from '../lib/hostname'

interface CreateWorkspaceModalProps {
  onConfirm: (name: string, slug: string) => void
  onCancel: () => void
}

export function CreateWorkspaceModal({ onConfirm, onCancel }: CreateWorkspaceModalProps) {
  const [name, setName] = useState('New Workspace')
  const [slug, setSlug] = useState(slugifyName('New Workspace'))
  const [slugTouched, setSlugTouched] = useState(false)

  const handleNameChange = (value: string) => {
    setName(value)
    if (!slugTouched) {
      setSlug(slugifyName(value))
    }
  }

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault()
    const trimmedName = name.trim()
    const trimmedSlug = slug.trim()
    if (trimmedName && trimmedSlug) {
      onConfirm(trimmedName, trimmedSlug)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onCancel}>
      <div
        className="w-full max-w-sm rounded-lg border border-[var(--border)] bg-[var(--card)] p-6 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold text-[var(--foreground)]">New Workspace</h2>
          <button type="button" onClick={onCancel} className="rounded p-1 hover:bg-[var(--secondary)]">
            <X size={16} />
          </button>
        </div>
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <div>
            <label className="mb-1 block text-sm font-medium text-[var(--foreground)]">Workspace name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => handleNameChange(e.target.value)}
              className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm"
              autoFocus
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-[var(--foreground)]">URL slug</label>
            <input
              type="text"
              value={slug}
              onChange={(e) => {
                setSlugTouched(true)
                setSlug(e.target.value.toLowerCase())
              }}
              className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 font-mono text-sm"
              placeholder="my-workspace"
            />
            <p className="mt-1 text-xs text-[var(--muted-foreground)]">
              Used in subdomain login: <code>{slug || '…'}.&lt;base-domain&gt;</code>
            </p>
          </div>
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={onCancel}
              className="rounded-md border border-[var(--border)] px-4 py-2 text-sm hover:bg-[var(--secondary)]"
            >
              Cancel
            </button>
            <button
              type="submit"
              className="rounded-md bg-[var(--primary)] px-4 py-2 text-sm font-medium text-white hover:opacity-90"
            >
              Create
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
