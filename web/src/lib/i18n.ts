import { useMemo } from 'react'

export type Locale = 'zh' | 'en'

const STORAGE_KEY = 'locale'

export type Dict = Record<string, string>
export type Dicts = Record<Locale, Dict>

export function detectLocale(): Locale {
  if (typeof window === 'undefined') return 'en'
  const stored = window.localStorage.getItem(STORAGE_KEY)
  if (stored === 'zh' || stored === 'en') return stored
  const nav = window.navigator.language || ''
  return nav.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

export function setLocale(l: Locale): void {
  window.localStorage.setItem(STORAGE_KEY, l)
  window.location.reload()
}

// Returns a stable `t(key)` function for the active locale.
// Falls back to the en value if the key is missing in the active locale,
// then to the key itself.
export function useT(dicts: Dicts) {
  const locale = detectLocale()
  return useMemo(() => {
    const primary = dicts[locale]
    const fallback = dicts.en
    return (key: string): string => primary[key] ?? fallback[key] ?? key
  }, [locale, dicts])
}
