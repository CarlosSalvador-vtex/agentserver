import { Link } from 'react-router-dom'
import { detectLocale, setLocale, useT } from '../../lib/i18n'
import { homeStrings } from './strings'

export function HomeNav() {
  const t = useT(homeStrings)
  const locale = detectLocale()
  const next = locale === 'zh' ? 'en' : 'zh'

  return (
    <nav className="sticky top-0 z-50 backdrop-blur-md bg-[var(--background)]/85 border-b border-[var(--border)]">
      <div className="mx-auto max-w-6xl px-6 h-14 flex items-center justify-between">
        <Link to="/" className="flex items-center gap-2 font-mono text-sm">
          <span aria-hidden="true" className="inline-block h-2 w-2 rounded-full bg-[var(--home-accent)] animate-pulse motion-reduce:animate-none" />
          <span>▸ {t('nav.brand')}</span>
        </Link>

        <div className="hidden md:flex items-center gap-6 font-mono text-xs text-[var(--muted-foreground)]">
          <a href="#why" className="hover:text-[var(--foreground)]">{t('nav.why')}</a>
          <a href="#how" className="hover:text-[var(--foreground)]">{t('nav.how')}</a>
          <a href="#compare" className="hover:text-[var(--foreground)]">{t('nav.compare')}</a>
        </div>

        <div className="flex items-center gap-4">
          <button
            type="button"
            onClick={() => setLocale(next)}
            className="font-mono text-xs text-[var(--muted-foreground)] hover:text-[var(--foreground)]"
            aria-label={`Switch language to ${next === 'zh' ? '中文' : 'English'}`}
          >
            {t('nav.lang.toggle')}
          </button>
          <Link
            to="/login"
            className="font-mono text-xs px-3 py-1.5 rounded-md bg-[var(--home-accent)] text-[var(--home-accent-fg)] hover:opacity-90"
          >
            {t('nav.signin')}
          </Link>
        </div>
      </div>
    </nav>
  )
}
