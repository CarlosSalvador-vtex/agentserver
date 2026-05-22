import { HomeNav } from './HomeNav'

export function Home() {
  return (
    <div data-theme="home" className="min-h-screen bg-[var(--background)] text-[var(--foreground)]">
      <HomeNav />
      <main className="mx-auto max-w-6xl px-6 py-12">
        <p className="font-mono text-sm text-[var(--muted-foreground)]">sections coming…</p>
      </main>
    </div>
  )
}
