# Unauthenticated Homepage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the bare login card on `agent.cs.ac.cn` (when unauthenticated) with a full marketing homepage at `/` that explains agentserver's value (Personal Computility, multi-device orchestration, WeChat channel) and routes visitors to `/login`.

**Architecture:** A single React component tree under `web/src/components/Home/`, composed by `<Home />`, mounted by `App.tsx` only when `authed === false`. Dark terminal aesthetic, page-scoped CSS variables under `[data-theme="home"]`. A minimal i18n primitive in `web/src/lib/i18n.ts` handles `zh`/`en` via `navigator.language` + localStorage override. No new npm dependencies; CSS-only hero animation with `IntersectionObserver` trigger and `prefers-reduced-motion` fallback.

**Tech Stack:** React 19, react-router-dom 7, TypeScript 5.9, Tailwind v4, Vite 7. No test runner is present in `web/` today — verification gates are `pnpm tsc -b` (typecheck), `pnpm lint`, and manual browser smoke via `pnpm dev` + Playwright snapshot. Do not introduce vitest as part of this plan.

**Spec:** `docs/superpowers/specs/2026-05-22-unauth-home-design.md` — every decision in that file is locked.

---

## File Structure

**New files (10):**

| Path | Responsibility |
|---|---|
| `web/src/lib/i18n.ts` | `detectLocale()`, `setLocale()`, `useT()` — the only i18n primitives used in this plan |
| `web/src/components/Home/Home.tsx` | Top-level shell. Sets `data-theme="home"`. Composes the eight sections in spec order |
| `web/src/components/Home/HomeNav.tsx` | Sticky top bar: logo, jump links, language toggle, Sign in button |
| `web/src/components/Home/HomeHero.tsx` | The three-pane hero (two terminals + one WeChat) with one-shot CSS animation |
| `web/src/components/Home/HomeQuote.tsx` | The Addy Osmani conductor → orchestrator quote section |
| `web/src/components/Home/HomePillars.tsx` | Three pillar cards + secondary pill strip |
| `web/src/components/Home/HomeCompare.tsx` | Differentiator table (real `<table>`, ASCII frame in dark theme) |
| `web/src/components/Home/HomeOnboarding.tsx` | Seven-step `<ol>` timeline |
| `web/src/components/Home/HomeFinalCTA.tsx` | Full-width Sign in / Self-host band |
| `web/src/components/Home/HomeFooter.tsx` | Three-column footer + version strip |
| `web/src/components/Home/strings.ts` | Full i18n dictionary (~80 keys), namespaced per section |

**Modified files (4):**

| Path | Change |
|---|---|
| `web/src/App.tsx` | Replace the `if (!authed) return <Login />` branch with nested routes: `/` → `<Home />`, `/login` → `<Login />`, `/oauth2/*` preserved, everything else redirects to `/`. |
| `web/src/components/Login.tsx` | Replace inline `<h1>agentserver</h1>` with a `<Link to="/">← agentserver</Link>` back-link. |
| `web/src/index.css` | Add `--home-accent`, `--home-accent-fg`, `--home-grid`, `--home-term-bg`, `--home-term-border` scoped to `[data-theme="home"]`. |
| `web/vite.config.ts` | Add `define: { __APP_VERSION__, __BUILD_COMMIT__, __BUILD_DATE__ }` so the footer can stamp build info. |

**Files NOT touched:** any non-`Home/` component, any backend Go code, `web/embed.go`, any OIDC / OAuth / workspace logic.

---

## Pre-flight (do once before Task 1)

- [ ] Run `pnpm install` in `web/` if `node_modules` is stale
- [ ] Confirm the dev server starts: `cd web && pnpm dev` → reachable at `http://localhost:5173`
- [ ] Confirm baseline passes: `cd web && pnpm tsc -b && pnpm lint` → both exit 0
- [ ] Kill the dev server (we'll restart it in Task 14)

---

### Task 1: Vite build-time constants + CSS tokens

**Files:**
- Modify: `web/vite.config.ts`
- Modify: `web/src/index.css`
- Create or modify: `web/src/vite-env.d.ts`

- [ ] **Step 1: Replace `web/vite.config.ts` with a version that injects `__APP_VERSION__`, `__BUILD_COMMIT__`, `__BUILD_DATE__`**

Use `execFileSync` (no shell, no injection surface) — not `exec` or `execSync`:

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'

function safeGit(args: string[], fallback: string): string {
  try {
    return execFileSync('git', args, { stdio: ['ignore', 'pipe', 'ignore'] }).toString().trim() || fallback
  } catch {
    return fallback
  }
}

const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf-8'))
const appVersion: string = pkg.version || '0.0.0'
const buildCommit: string = safeGit(['rev-parse', '--short', 'HEAD'], 'dev')
const buildDate: string = new Date().toISOString().slice(0, 10)

export default defineConfig({
  plugins: [react(), tailwindcss()],
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
    __BUILD_COMMIT__: JSON.stringify(buildCommit),
    __BUILD_DATE__: JSON.stringify(buildDate),
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
```

- [ ] **Step 2: Declare the build-time globals as TypeScript ambient types**

If `web/src/vite-env.d.ts` exists, append the three `declare const` lines. Otherwise create it with this content:

```ts
/// <reference types="vite/client" />

declare const __APP_VERSION__: string
declare const __BUILD_COMMIT__: string
declare const __BUILD_DATE__: string
```

- [ ] **Step 3: Append the `[data-theme="home"]` CSS variables to `web/src/index.css`**

Add the following block immediately after the existing `.dark { ... }` rule:

```css
[data-theme="home"] {
  --home-accent: #18181b;
  --home-accent-fg: #fafafa;
  --home-grid: rgba(0, 0, 0, 0.06);
  --home-term-bg: #f4f4f5;
  --home-term-border: #d4d4d8;
  --home-term-fg: #18181b;
  --home-term-dim: #71717a;
}

.dark[data-theme="home"],
[data-theme="home"].dark,
[data-theme="home"] .dark,
.dark [data-theme="home"] {
  --home-accent: #7fff7f;
  --home-accent-fg: #000000;
  --home-grid: rgba(127, 255, 127, 0.04);
  --home-term-bg: #000000;
  --home-term-border: #2a2a2a;
  --home-term-fg: #e6e6e6;
  --home-term-dim: #888888;
}
```

- [ ] **Step 4: Typecheck and lint**

```bash
cd web && pnpm tsc -b && pnpm lint
```

Expected: both exit 0.

- [ ] **Step 5: Commit**

```bash
git add web/vite.config.ts web/src/vite-env.d.ts web/src/index.css
git commit -m "feat(web): add home theme CSS vars and build-time constants"
```

---

### Task 2: i18n primitive

**Files:**
- Create: `web/src/lib/i18n.ts`

- [ ] **Step 1: Create `web/src/lib/i18n.ts` with locale detection, setter, and the `useT()` hook factory**

```ts
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
```

- [ ] **Step 2: Typecheck**

```bash
cd web && pnpm tsc -b
```

Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/i18n.ts
git commit -m "feat(web): add minimal i18n primitive (detectLocale/setLocale/useT)"
```

---

### Task 3: Full i18n dictionary

**Files:**
- Create: `web/src/components/Home/strings.ts`

This task front-loads all copy so later component tasks reference keys, not literal strings.

- [ ] **Step 1: Create the dictionary with every key the homepage needs**

```ts
import type { Dicts } from '../../lib/i18n'

export const homeStrings: Dicts = {
  zh: {
    // nav
    'nav.brand': 'agentserver',
    'nav.why': '卖点',
    'nav.how': '上手',
    'nav.compare': '对比',
    'nav.signin': '登录 →',
    'nav.lang.toggle': 'EN',
    'nav.online': '在线',

    // hero
    'hero.h1': '你的个人算力网。',
    'hero.sub': 'agentserver 把笔记本、云沙箱、家里的服务器编成一个工作区——从浏览器、命令行，或微信，一并指挥。',
    'hero.cta.primary': '▸ 登录',
    'hero.cta.secondary': '在 GitHub 上查看',
    'hero.term.macbook': 'office-macbook · codex',
    'hero.term.sandbox': 'cloud-sandbox-7 · codex',
    'hero.chat.title': '我自己',
    'hero.chat.user': '把上周的实验数据画成图给我',
    'hero.chat.bot1': '📂 已从 office-macbook 取到 experiment_0521.csv',
    'hero.chat.bot2': '🔨 在沙箱里分析 + 画图中…',
    'hero.chat.bot3': '✓ 完成',
    'hero.chip.online': '在线',

    // quote
    'quote.body': '"Once you juggle 10+ agents across machines, you stop being a conductor and become an orchestrator."',
    'quote.attrib': '— Addy Osmani · Director, Google Gemini & Cloud AI',
    'quote.caption': 'agentserver = 那个 orchestrator。',

    // pillars
    'pillars.heading': '为什么用 agentserver',
    'pillars.pocket.title': '装在口袋里的指挥台',
    'pillars.pocket.body': '微信里一句中文，落到任意设备执行，结果推回同一个聊天框。Telegram 同样支持。',
    'pillars.workspace.title': '一个工作区，所有设备',
    'pillars.workspace.body': '云沙箱、本地机器、IM-bound 代理——全在同一份注册表里，并排出现在 Web UI 上。',
    'pillars.tunnel.title': '无公网 IP，照常入网',
    'pillars.tunnel.body': '本地的 codex / Claude Code / opencode 通过 WebSocket 拨号入网，呈现为一个沙箱。',
    'pillars.also': '+ 暂停 / 恢复沙箱 · Jupyter 笔记本 · 多人协作 · 凭证代理 · SSO（GitHub / OIDC）· 自托管',

    // compare
    'compare.heading': '和已有的工具相比',
    'compare.col.tool': '产品',
    'compare.col.local': '本地代理',
    'compare.col.cloud': '云沙箱',
    'compare.col.peer': '跨设备组网',
    'compare.col.chat': 'IM 通道',
    'compare.caption': '唯一同时勾上四列的产品。',

    // onboarding
    'onb.heading': '7 步，把你的设备连上来',
    'onb.s1.title': '注册',
    'onb.s1.body': '邮箱 / GitHub / SSO 任选',
    'onb.s2.title': '链接模型账号',
    'onb.s2.body': '自带 ChatGPT / Anthropic 凭证，或选平台托管账号',
    'onb.s3.title': '入网设备',
    'onb.s3.body': 'brew install codex → 粘贴注册码',
    'onb.s4.title': '选一台"指挥机"',
    'onb.s4.body': '通常是你的主力笔电',
    'onb.s5.title': '(可选) 开 Jupyter',
    'onb.s5.body': '想手写代码？ctx 已预注入 kernel',
    'onb.s6.title': '绑定微信',
    'onb.s6.body': '扫码即可，从此聊天框 = 终端',
    'onb.s7.title': '邀请协作者',
    'onb.s7.body': '角色：owner / maintainer / developer / guest',
    'onb.docs': '完整文档 →',

    // final cta
    'cta.heading': '把你的设备，编进同一张算力网。',
    'cta.primary': '▸ 登录',
    'cta.secondary': '在自己的域名上自托管',

    // footer
    'footer.col1.title': 'agentserver',
    'footer.col2.title': '社区',
    'footer.col2.weixin': '微信群',
    'footer.col2.telegram': 'Telegram',
    'footer.col2.issues': 'Issue 跟踪',
    'footer.col3.title': '法律',
    'footer.col3.license': 'Apache-2.0',
    'footer.col3.privacy': '隐私',
    'footer.col3.contact': '联系',
  },
  en: {
    // nav
    'nav.brand': 'agentserver',
    'nav.why': 'Why',
    'nav.how': 'How',
    'nav.compare': 'Compare',
    'nav.signin': 'Sign in →',
    'nav.lang.toggle': '中',
    'nav.online': 'online',

    // hero
    'hero.h1': 'Your Personal Computility.',
    'hero.sub': 'agentserver weaves laptops, cloud sandboxes, and home servers into one workspace — commanded from your browser, your CLI, or your WeChat chat.',
    'hero.cta.primary': '▸ Sign in',
    'hero.cta.secondary': 'View on GitHub',
    'hero.term.macbook': 'office-macbook · codex',
    'hero.term.sandbox': 'cloud-sandbox-7 · codex',
    'hero.chat.title': 'me',
    'hero.chat.user': 'Plot last week’s experiment data and send it to me',
    'hero.chat.bot1': '📂 pulled experiment_0521.csv from office-macbook',
    'hero.chat.bot2': '🔨 analyzing + plotting in sandbox…',
    'hero.chat.bot3': '✓ done',
    'hero.chip.online': 'online',

    // quote
    'quote.body': '"Once you juggle 10+ agents across machines, you stop being a conductor and become an orchestrator."',
    'quote.attrib': '— Addy Osmani · Director, Google Gemini & Cloud AI',
    'quote.caption': 'agentserver is that orchestrator.',

    // pillars
    'pillars.heading': 'Why agentserver',
    'pillars.pocket.title': 'Pocket-sized command line',
    'pillars.pocket.body': 'One sentence in WeChat lands on the right device. The result comes back to the same chat. Telegram supported too.',
    'pillars.workspace.title': 'One workspace, every device',
    'pillars.workspace.body': 'Cloud sandboxes, local machines, IM-bound agents — all in one registry, side by side in the Web UI.',
    'pillars.tunnel.title': 'No public IP. Still in the network.',
    'pillars.tunnel.body': 'Your local codex / Claude Code / opencode dials home over WebSocket and shows up as a sandbox.',
    'pillars.also': '+ Pausable sandboxes · Jupyter notebook · Multi-user · Credential proxy · SSO (GitHub / OIDC) · Self-host',

    // compare
    'compare.heading': 'How it differs',
    'compare.col.tool': 'Tool',
    'compare.col.local': 'local',
    'compare.col.cloud': 'cloud',
    'compare.col.peer': 'peer',
    'compare.col.chat': 'chat',
    'compare.caption': 'The only one with all four checked.',

    // onboarding
    'onb.heading': '7 steps to wire it all up',
    'onb.s1.title': 'Register',
    'onb.s1.body': 'Email / GitHub / SSO — your pick',
    'onb.s2.title': 'Link a model account',
    'onb.s2.body': 'Bring your own ChatGPT / Anthropic credential, or use a managed one',
    'onb.s3.title': 'Enroll devices',
    'onb.s3.body': 'brew install codex → paste the registration code',
    'onb.s4.title': 'Pick a "command machine"',
    'onb.s4.body': 'Usually your daily-driver laptop',
    'onb.s5.title': '(Optional) open Jupyter',
    'onb.s5.body': 'Prefer hand-written code? ctx is pre-injected in every kernel',
    'onb.s6.title': 'Bind WeChat',
    'onb.s6.body': 'Scan the QR. From now on, the chat window is your terminal',
    'onb.s7.title': 'Invite collaborators',
    'onb.s7.body': 'Roles: owner / maintainer / developer / guest',
    'onb.docs': 'Full docs →',

    // final cta
    'cta.heading': 'Weave your devices into one Computility.',
    'cta.primary': '▸ Sign in',
    'cta.secondary': 'Self-host on your own domain',

    // footer
    'footer.col1.title': 'agentserver',
    'footer.col2.title': 'Community',
    'footer.col2.weixin': 'WeChat group',
    'footer.col2.telegram': 'Telegram',
    'footer.col2.issues': 'Issue tracker',
    'footer.col3.title': 'Legal',
    'footer.col3.license': 'Apache-2.0',
    'footer.col3.privacy': 'Privacy',
    'footer.col3.contact': 'Contact',
  },
}
```

- [ ] **Step 2: Typecheck**

```bash
cd web && pnpm tsc -b
```

Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/Home/strings.ts
git commit -m "feat(web): add home page i18n dictionary"
```

---

### Task 4: App.tsx routing — `/` → `<Home />` stub, keep `/login` working

This wires the new route before any UI is built, so we can verify nothing existing breaks.

**Files:**
- Modify: `web/src/App.tsx`
- Create: `web/src/components/Home/Home.tsx` (as a stub for now)

- [ ] **Step 1: Create a minimal `<Home />` stub**

Create `web/src/components/Home/Home.tsx`:

```tsx
export function Home() {
  return (
    <div data-theme="home" className="min-h-screen bg-[var(--background)] text-[var(--foreground)] p-8">
      <p className="font-mono text-sm">▸ agentserver · home stub</p>
    </div>
  )
}
```

- [ ] **Step 2: In `web/src/App.tsx`, add the import**

Find the existing line:

```tsx
import { Login } from './components/Login'
```

Add directly below:

```tsx
import { Home } from './components/Home/Home'
```

- [ ] **Step 3: Replace the unauthenticated branch with nested routes**

Locate the block starting `if (!authed) {` (around line 305 in current `App.tsx`). Replace the entire `if (!authed) { ... }` block (from `if (!authed) {` through its closing `}` and the `return ( ... )` it contains) with:

```tsx
if (!authed) {
  if (location.pathname === '/oauth2/consent') {
    const params = new URLSearchParams(location.search)
    const challenge = params.get('consent_challenge')
    if (challenge) {
      sessionStorage.setItem(PENDING_CONSENT_CHALLENGE_KEY, challenge)
    }
  }
  if (location.pathname === '/oauth2/device') {
    sessionStorage.setItem(PENDING_DEVICE_PARAMS_KEY, location.search)
  }

  const onLoginSuccess = () => {
    const params = new URLSearchParams(location.search)
    const next = params.get('next')
    if (next) {
      window.location.href = next
      return
    }
    setAuthed(true)
    listWorkspaces().then((ws) => {
      setWorkspaces(ws)
      if (ws.length > 0) setSelectedWorkspaceId(ws[0].id)
    }).catch(() => {})
    getMe().then(setUser).catch(() => {})
  }

  return (
    <Routes>
      <Route path="/" element={<Home />} />
      <Route path="/login" element={<Login onSuccess={onLoginSuccess} />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
```

Notes for the implementer:
- `Routes`, `Route`, `Navigate` are already imported at the top of the file.
- `PENDING_CONSENT_CHALLENGE_KEY`, `PENDING_DEVICE_PARAMS_KEY`, `listWorkspaces`, `getMe`, `setAuthed`, `setWorkspaces`, `setSelectedWorkspaceId`, `setUser` are all already in scope.
- The `/oauth2/login` short-circuit earlier in the function (around line 278) is **untouched** — `OAuthLoginRoute` still handles that path before the auth gate.
- The `/oauth2/consent` and `/oauth2/device` handling is preserved: stash the params, then render `<Routes>`. The user will land on `/` (or whatever wildcard match redirects to `/`); when they click Sign in and complete login, the `?next=` flow resumes from sessionStorage as before.

- [ ] **Step 4: Typecheck and lint**

```bash
cd web && pnpm tsc -b && pnpm lint
```

Expected: both exit 0.

- [ ] **Step 5: Manual smoke test in dev**

```bash
cd web && pnpm dev
```

In a browser at `http://localhost:5173`:
- `/` shows `▸ agentserver · home stub` on a white background (light theme) — PASS
- `/login` shows the existing login card unchanged — PASS
- `/anything-else` redirects to `/` — PASS

Stop the dev server.

- [ ] **Step 6: Commit**

```bash
git add web/src/App.tsx web/src/components/Home/Home.tsx
git commit -m "feat(web): route unauth / to Home stub, preserve /login"
```

---

### Task 5: Login.tsx back-link

**Files:**
- Modify: `web/src/components/Login.tsx`

- [ ] **Step 1: Add the `Link` import at the top of `Login.tsx`**

Find the existing import line:

```tsx
import { useState, useEffect, type FormEvent } from 'react'
```

Add directly below:

```tsx
import { Link } from 'react-router-dom'
```

- [ ] **Step 2: Replace the inline title with a back-link**

Find:

```tsx
<h1 className="mb-6 text-center text-xl font-semibold text-[var(--card-foreground)]">
  agentserver
</h1>
```

Replace with:

```tsx
<h1 className="mb-6 text-center text-xl font-semibold text-[var(--card-foreground)]">
  <Link to="/" className="hover:text-[var(--muted-foreground)] transition-colors">
    ← agentserver
  </Link>
</h1>
```

- [ ] **Step 3: Typecheck, lint, smoke**

```bash
cd web && pnpm tsc -b && pnpm lint
```

Then `pnpm dev`, visit `/login`, click `← agentserver`, verify it lands on `/`. Stop dev.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Login.tsx
git commit -m "feat(web): add back-link from /login to / on login page"
```

---

### Task 6: HomeNav

**Files:**
- Create: `web/src/components/Home/HomeNav.tsx`

- [ ] **Step 1: Create the component**

```tsx
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
```

- [ ] **Step 2: Mount it in `Home.tsx`**

Replace the entire body of `web/src/components/Home/Home.tsx` with:

```tsx
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
```

- [ ] **Step 3: Typecheck, lint, smoke**

```bash
cd web && pnpm tsc -b && pnpm lint
```

Then `pnpm dev`, visit `/`:
- Nav bar visible at top with brand, jump links (hidden on mobile width), language button, Sign in button
- Click `EN` (or `中`) → page reloads, language flips
- Click `Sign in →` → lands on `/login`
- Click `← agentserver` on `/login` → back to `/`

Stop dev.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Home/HomeNav.tsx web/src/components/Home/Home.tsx
git commit -m "feat(web): add HomeNav with language toggle and sign-in link"
```

---

### Task 7: HomeHero (the centerpiece)

**Files:**
- Create: `web/src/components/Home/HomeHero.tsx`

This is the largest single task. The hero comprises markup for three panes, a CSS animation timeline, an IntersectionObserver that starts the animation once, and a `prefers-reduced-motion` fallback.

- [ ] **Step 1: Create the component**

```tsx
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

export function HomeHero() {
  const t = useT(homeStrings)
  const ref = useRef<HTMLDivElement | null>(null)
  const [started, setStarted] = useState(false)

  useEffect(() => {
    if (started) return
    const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (reduce) {
      setStarted(true)
      return
    }
    const node = ref.current
    if (!node) return
    const obs = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (e.isIntersecting) {
          setStarted(true)
          obs.disconnect()
          break
        }
      }
    }, { threshold: 0.25 })
    obs.observe(node)
    return () => obs.disconnect()
  }, [started])

  return (
    <section ref={ref} className="pt-12 pb-20" data-started={started ? 'true' : 'false'}>
      <div className="grid lg:grid-cols-[1.1fr_0.9fr] gap-10 items-start">
        {/* Left: heading + CTAs */}
        <div>
          <h1 className="text-5xl lg:text-6xl font-semibold tracking-tight leading-[1.1]">
            {t('hero.h1')}
          </h1>
          <p className="mt-6 text-base lg:text-lg text-[var(--muted-foreground)] leading-relaxed max-w-xl">
            {t('hero.sub')}
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Link
              to="/login"
              className="font-mono text-sm px-4 py-2 rounded-md bg-[var(--home-accent)] text-[var(--home-accent-fg)] hover:opacity-90"
            >
              {t('hero.cta.primary')}
            </Link>
            <a
              href="https://github.com/agentserver/agentserver"
              target="_blank"
              rel="noopener noreferrer"
              className="font-mono text-sm px-4 py-2 rounded-md border border-[var(--border)] hover:bg-[var(--accent)]"
            >
              {t('hero.cta.secondary')}
            </a>
            <span className="font-mono text-[10px] px-2 py-1 rounded border border-[var(--home-accent)]/40 text-[var(--home-accent)]">
              v{__APP_VERSION__} · {t('hero.chip.online')}
            </span>
          </div>
        </div>

        {/* Right: three-pane animated demo */}
        <div
          role="img"
          aria-label={t('hero.sub')}
          className="hero-demo grid grid-cols-[1fr_1fr] gap-3"
        >
          {/* Two terminal panes stacked in the left column */}
          <div className="flex flex-col gap-3">
            <Term
              title={t('hero.term.macbook')}
              lines={[
                { delay: 400,  text: '$ codex relay "把 experiment_0521.csv', kind: 'cmd' },
                { delay: 700,  text: '   在沙箱里画成图发我"',                kind: 'cmd' },
                { delay: 1100, text: '▸ found experiment_0521.csv (124MB)',  kind: 'dim' },
                { delay: 1500, text: '▸ relaying to cloud-sandbox-7…',       kind: 'dim' },
              ]}
            />
            <Term
              title={t('hero.term.sandbox')}
              lines={[
                { delay: 3000, text: '▸ pandas: 5 conditions × 12 trials',  kind: 'dim' },
                { delay: 3400, text: '▸ matplotlib: boxplot + error bars',  kind: 'dim' },
                { delay: 4400, text: '✓ saved plot.png (412KB)',            kind: 'ok'  },
                { delay: 4900, text: '▸ uploading via imbridge…',           kind: 'dim' },
              ]}
            />
          </div>

          {/* WeChat pane */}
          <ChatPane t={t} />
        </div>
      </div>

      <style>{`
        .hero-demo .ln,
        .hero-demo .bubble {
          opacity: 0;
          transform: translateY(4px);
        }
        .hero-demo[data-started="true"] .ln {
          animation: heroFadeIn 280ms ease-out forwards;
        }
        .hero-demo[data-started="true"] .bubble {
          animation: heroBubble 220ms ease-out forwards;
        }
        @keyframes heroFadeIn {
          to { opacity: 1; transform: translateY(0); }
        }
        @keyframes heroBubble {
          to { opacity: 1; transform: translateY(0); }
        }
        @media (prefers-reduced-motion: reduce) {
          .hero-demo .ln,
          .hero-demo .bubble {
            opacity: 1 !important;
            transform: none !important;
            animation: none !important;
          }
        }
      `}</style>
    </section>
  )
}

type TermLine = { delay: number; text: string; kind: 'cmd' | 'dim' | 'ok' }

function Term({ title, lines }: { title: string; lines: TermLine[] }) {
  return (
    <div className="rounded-md border border-[var(--home-term-border)] bg-[var(--home-term-bg)] font-mono text-[11px] overflow-hidden">
      <div className="px-3 py-1.5 border-b border-[var(--home-term-border)] text-[var(--home-term-dim)]">
        {title}
      </div>
      <div className="px-3 py-2 space-y-1">
        {lines.map((ln, i) => (
          <div
            key={i}
            className="ln"
            style={{ animationDelay: `${ln.delay}ms` }}
          >
            <span className={
              ln.kind === 'ok'  ? 'text-[var(--home-accent)]' :
              ln.kind === 'dim' ? 'text-[var(--home-term-dim)]' :
                                  'text-[var(--home-term-fg)]'
            }>
              {ln.text}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

function ChatPane({ t }: { t: (k: string) => string }) {
  type Bubble = { delay: number; who: 'me' | 'bot'; text: string; thumb?: boolean }
  const bubbles: Bubble[] = [
    { delay: 5500, who: 'me',  text: t('hero.chat.user') },
    { delay: 6000, who: 'bot', text: t('hero.chat.bot1') },
    { delay: 7000, who: 'bot', text: t('hero.chat.bot2') },
    { delay: 8500, who: 'bot', text: t('hero.chat.bot3'), thumb: true },
  ]
  return (
    <div className="rounded-md border border-[var(--home-term-border)] bg-[#ededed] dark:bg-[#1a1a1a] overflow-hidden">
      <div className="px-3 py-1.5 border-b border-[var(--home-term-border)] text-xs font-medium text-[var(--home-term-fg)]">
        {t('hero.chat.title')}
      </div>
      <div role="log" aria-live="polite" className="px-3 py-3 space-y-2 min-h-[260px]">
        {bubbles.map((b, i) => (
          <div
            key={i}
            className={`bubble flex ${b.who === 'me' ? 'justify-end' : 'justify-start'}`}
            style={{ animationDelay: `${b.delay}ms` }}
          >
            <div className={
              'max-w-[80%] rounded-md px-3 py-1.5 text-xs leading-relaxed ' +
              (b.who === 'me'
                ? 'bg-[#95ec69] text-black'
                : 'bg-white text-black dark:bg-[#2a2a2a] dark:text-white')
            }>
              {b.text}
              {b.thumb && (
                <div className="mt-1.5 h-12 w-full rounded bg-gradient-to-br from-[var(--home-accent)]/30 to-[var(--home-accent)]/10 border border-[var(--home-accent)]/40 flex items-center justify-center text-[10px] text-[var(--home-term-dim)]">
                  plot.png · 412KB
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Mount it in `Home.tsx`**

Replace the `<main>` body with:

```tsx
<main className="mx-auto max-w-6xl px-6">
  <HomeHero />
</main>
```

And add the import: `import { HomeHero } from './HomeHero'`.

- [ ] **Step 3: Typecheck, lint, smoke**

```bash
cd web && pnpm tsc -b && pnpm lint
```

`pnpm dev`, visit `/`:
- Hero displays H1, sub, CTAs on the left, three panes on the right
- Animation runs once on load (~9s total)
- Refresh the page → animation runs again
- Open DevTools → Rendering → "Emulate CSS prefers-reduced-motion: reduce" → refresh → all panes appear fully populated immediately

Stop dev.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Home/HomeHero.tsx web/src/components/Home/Home.tsx
git commit -m "feat(web): add HomeHero with one-shot three-pane animation"
```

---

### Task 8: HomeQuote

**Files:**
- Create: `web/src/components/Home/HomeQuote.tsx`

- [ ] **Step 1: Create the component**

```tsx
import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

export function HomeQuote() {
  const t = useT(homeStrings)
  // Split the quote so 'conductor' and 'orchestrator' can be highlighted.
  const accent = (text: string) => (
    <span className="text-[var(--home-accent)] font-medium">{text}</span>
  )
  return (
    <section className="py-16 border-l-2 border-[var(--home-accent)] pl-6">
      <blockquote className="text-lg lg:text-xl text-[var(--foreground)] leading-relaxed max-w-3xl">
        "Once you juggle 10+ agents across machines, you stop being a {accent('conductor')} and become an {accent('orchestrator')}."
      </blockquote>
      <p className="mt-3 text-sm text-[var(--muted-foreground)] font-mono">
        {t('quote.attrib')}
      </p>
      <p className="mt-2 text-sm text-[var(--muted-foreground)]">
        {t('quote.caption')}
      </p>
    </section>
  )
}
```

Note: the quote body is identical in zh and en (it's a real English quote), so it is hardcoded here rather than going through `t('quote.body')`. The dictionary key `quote.body` exists for completeness but is not used by this component.

- [ ] **Step 2: Mount in `Home.tsx`**

Append `<HomeQuote />` after `<HomeHero />` and add the import.

- [ ] **Step 3: Typecheck, lint, smoke**

```bash
cd web && pnpm tsc -b && pnpm lint
```

`pnpm dev`, visit `/`, verify the quote appears below the hero with left accent bar, two highlighted words. Stop dev.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Home/HomeQuote.tsx web/src/components/Home/Home.tsx
git commit -m "feat(web): add HomeQuote section"
```

---

### Task 9: HomePillars

**Files:**
- Create: `web/src/components/Home/HomePillars.tsx`

- [ ] **Step 1: Create the component**

```tsx
import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

type Pillar = {
  emoji: string
  titleKey: string
  bodyKey: string
}

const pillars: Pillar[] = [
  { emoji: '📱', titleKey: 'pillars.pocket.title',    bodyKey: 'pillars.pocket.body' },
  { emoji: '🌐', titleKey: 'pillars.workspace.title', bodyKey: 'pillars.workspace.body' },
  { emoji: '🔌', titleKey: 'pillars.tunnel.title',    bodyKey: 'pillars.tunnel.body' },
]

export function HomePillars() {
  const t = useT(homeStrings)
  return (
    <section id="why" className="py-20">
      <h2 className="font-mono text-xs tracking-[0.2em] text-[var(--muted-foreground)] uppercase mb-8">
        {t('pillars.heading')}
      </h2>
      <div className="grid md:grid-cols-3 gap-4">
        {pillars.map((p) => (
          <article
            key={p.titleKey}
            className="rounded-lg border border-[var(--border)] p-6 bg-[var(--card)]"
          >
            <div className="text-3xl mb-3" aria-hidden="true">{p.emoji}</div>
            <h3 className="text-lg font-semibold mb-2">{t(p.titleKey)}</h3>
            <p className="text-sm text-[var(--muted-foreground)] leading-relaxed">{t(p.bodyKey)}</p>
          </article>
        ))}
      </div>
      <p className="mt-6 font-mono text-xs text-[var(--muted-foreground)] leading-relaxed">
        {t('pillars.also')}
      </p>
    </section>
  )
}
```

- [ ] **Step 2: Mount in `Home.tsx`**

Add `<HomePillars />` after `<HomeQuote />`, import accordingly.

- [ ] **Step 3: Typecheck, lint, smoke**

```bash
cd web && pnpm tsc -b && pnpm lint
```

`pnpm dev`, verify three cards render side-by-side at desktop width, stack at mobile, secondary chip strip appears below. Stop dev.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Home/HomePillars.tsx web/src/components/Home/Home.tsx
git commit -m "feat(web): add HomePillars (3 cards + secondary pill strip)"
```

---

### Task 10: HomeCompare

**Files:**
- Create: `web/src/components/Home/HomeCompare.tsx`

- [ ] **Step 1: Create the component**

```tsx
import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

type Row = { tool: string; local: string; cloud: string; peer: string; chat: string; us?: boolean }

const rows: Row[] = [
  { tool: 'OpenClaw / Claude Code Remote', local: '1',      cloud: '—',      peer: '—', chat: '—' },
  { tool: 'Claude Code on the web',        local: '—',      cloud: '✓',      peer: '—', chat: '—' },
  { tool: 'CC Agent Teams',                local: '—',      cloud: '✓(sub)', peer: '—', chat: '—' },
  { tool: 'agentserver',                   local: '✓ many', cloud: '✓',      peer: '✓', chat: '✓', us: true },
]

export function HomeCompare() {
  const t = useT(homeStrings)
  return (
    <section id="compare" className="py-20">
      <h2 className="font-mono text-xs tracking-[0.2em] text-[var(--muted-foreground)] uppercase mb-8">
        {t('compare.heading')}
      </h2>
      <div className="overflow-x-auto">
        <table className="w-full font-mono text-xs border border-[var(--home-term-border)]">
          <caption className="sr-only">{t('compare.heading')}</caption>
          <thead>
            <tr className="bg-[var(--card)] text-[var(--home-accent)]">
              <th scope="col" className="text-left p-3 font-medium">{t('compare.col.tool')}</th>
              <th scope="col" className="text-center p-3 font-medium">{t('compare.col.local')}</th>
              <th scope="col" className="text-center p-3 font-medium">{t('compare.col.cloud')}</th>
              <th scope="col" className="text-center p-3 font-medium">{t('compare.col.peer')}</th>
              <th scope="col" className="text-center p-3 font-medium">{t('compare.col.chat')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr
                key={r.tool}
                className={r.us
                  ? 'bg-[var(--home-grid)] border-l-2 border-[var(--home-accent)]'
                  : 'border-t border-[var(--home-term-border)]'
                }
              >
                <th scope="row" className={`text-left p-3 font-normal ${r.us ? 'text-[var(--home-accent)] font-medium' : ''}`}>
                  {r.us ? `▸ ${r.tool}` : r.tool}
                </th>
                <td className="text-center p-3">{r.local}</td>
                <td className="text-center p-3">{r.cloud}</td>
                <td className="text-center p-3">{r.peer}</td>
                <td className="text-center p-3">{r.chat}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-4 text-sm text-[var(--muted-foreground)]">{t('compare.caption')}</p>
    </section>
  )
}
```

- [ ] **Step 2: Mount in `Home.tsx`**

Add `<HomeCompare />` after `<HomePillars />`, import accordingly.

- [ ] **Step 3: Typecheck, lint, smoke**

```bash
cd web && pnpm tsc -b && pnpm lint
```

`pnpm dev`, verify table renders, agentserver row visually distinguished with left border + accent color, mobile width gets horizontal scroll. Stop dev.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Home/HomeCompare.tsx web/src/components/Home/Home.tsx
git commit -m "feat(web): add HomeCompare differentiator table"
```

---

### Task 11: HomeOnboarding

**Files:**
- Create: `web/src/components/Home/HomeOnboarding.tsx`

- [ ] **Step 1: Create the component**

```tsx
import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

const steps = [
  { n: 1, titleKey: 'onb.s1.title', bodyKey: 'onb.s1.body' },
  { n: 2, titleKey: 'onb.s2.title', bodyKey: 'onb.s2.body' },
  { n: 3, titleKey: 'onb.s3.title', bodyKey: 'onb.s3.body' },
  { n: 4, titleKey: 'onb.s4.title', bodyKey: 'onb.s4.body' },
  { n: 5, titleKey: 'onb.s5.title', bodyKey: 'onb.s5.body' },
  { n: 6, titleKey: 'onb.s6.title', bodyKey: 'onb.s6.body', emphasized: true },
  { n: 7, titleKey: 'onb.s7.title', bodyKey: 'onb.s7.body' },
]

export function HomeOnboarding() {
  const t = useT(homeStrings)
  return (
    <section id="how" className="py-20">
      <h2 className="font-mono text-xs tracking-[0.2em] text-[var(--muted-foreground)] uppercase mb-8">
        {t('onb.heading')}
      </h2>
      <ol className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {steps.map((s) => (
          <li
            key={s.n}
            className={
              'rounded-lg border p-5 ' +
              (s.emphasized
                ? 'border-[var(--home-accent)] bg-[var(--home-grid)]'
                : 'border-[var(--border)] bg-[var(--card)]')
            }
          >
            <div className={
              'inline-flex items-center justify-center h-7 w-7 rounded-full font-mono text-xs mb-3 ' +
              (s.emphasized
                ? 'bg-[var(--home-accent)] text-[var(--home-accent-fg)]'
                : 'bg-[var(--secondary)] text-[var(--secondary-foreground)]')
            }>
              {s.n}
            </div>
            <h3 className={`text-sm font-semibold mb-1 ${s.emphasized ? 'text-[var(--home-accent)]' : ''}`}>
              {t(s.titleKey)}
            </h3>
            <p className="text-xs text-[var(--muted-foreground)] leading-relaxed">{t(s.bodyKey)}</p>
          </li>
        ))}
      </ol>
      <a
        href="https://github.com/agentserver/agentserver#readme"
        target="_blank"
        rel="noopener noreferrer"
        className="inline-block mt-6 font-mono text-xs text-[var(--home-accent)] hover:underline"
      >
        {t('onb.docs')}
      </a>
    </section>
  )
}
```

- [ ] **Step 2: Mount in `Home.tsx`**

Add `<HomeOnboarding />` after `<HomeCompare />`, import accordingly.

- [ ] **Step 3: Typecheck, lint, smoke**

```bash
cd web && pnpm tsc -b && pnpm lint
```

`pnpm dev`, verify 7 numbered cards render in a 4-column grid at desktop (with last row having 3 cards), step 6 visually emphasized, vertical stack at mobile. Stop dev.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Home/HomeOnboarding.tsx web/src/components/Home/Home.tsx
git commit -m "feat(web): add HomeOnboarding 7-step timeline"
```

---

### Task 12: HomeFinalCTA

**Files:**
- Create: `web/src/components/Home/HomeFinalCTA.tsx`

- [ ] **Step 1: Create the component**

```tsx
import { Link } from 'react-router-dom'
import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

export function HomeFinalCTA() {
  const t = useT(homeStrings)
  return (
    <section className="my-20 py-16 border-y-2 border-[var(--home-accent)]">
      <div className="text-center">
        <h2 className="text-3xl lg:text-4xl font-semibold tracking-tight max-w-2xl mx-auto">
          {t('cta.heading')}
        </h2>
        <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
          <Link
            to="/login"
            className="font-mono text-sm px-5 py-2.5 rounded-md bg-[var(--home-accent)] text-[var(--home-accent-fg)] hover:opacity-90"
          >
            {t('cta.primary')}
          </Link>
          <a
            href="https://github.com/agentserver/agentserver#self-hosting"
            target="_blank"
            rel="noopener noreferrer"
            className="font-mono text-sm px-5 py-2.5 rounded-md border border-[var(--border)] hover:bg-[var(--accent)]"
          >
            {t('cta.secondary')}
          </a>
        </div>
      </div>
    </section>
  )
}
```

- [ ] **Step 2: Mount in `Home.tsx`**

Add `<HomeFinalCTA />` after `<HomeOnboarding />`, import accordingly.

- [ ] **Step 3: Typecheck, lint, smoke**

```bash
cd web && pnpm tsc -b && pnpm lint
```

`pnpm dev`, verify centered heading with two buttons, accent-colored top + bottom border. Stop dev.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Home/HomeFinalCTA.tsx web/src/components/Home/Home.tsx
git commit -m "feat(web): add HomeFinalCTA section"
```

---

### Task 13: HomeFooter + build stamp

**Files:**
- Create: `web/src/components/Home/HomeFooter.tsx`

- [ ] **Step 1: Create the component**

```tsx
import { useT } from '../../lib/i18n'
import { homeStrings } from './strings'

export function HomeFooter() {
  const t = useT(homeStrings)
  return (
    <footer className="border-t border-[var(--border)] py-10 mt-10">
      <div className="grid md:grid-cols-3 gap-8 text-sm">
        <div>
          <p className="font-mono font-semibold mb-3">{t('footer.col1.title')}</p>
          <ul className="space-y-1 text-[var(--muted-foreground)]">
            <li><a className="hover:text-[var(--foreground)]" href="https://agentserver.dev" target="_blank" rel="noopener noreferrer">agentserver.dev</a></li>
            <li><a className="hover:text-[var(--foreground)]" href="https://github.com/agentserver/agentserver" target="_blank" rel="noopener noreferrer">GitHub</a></li>
            <li><a className="hover:text-[var(--foreground)]" href="https://github.com/agentserver/agentserver#readme" target="_blank" rel="noopener noreferrer">Docs</a></li>
            <li><a className="hover:text-[var(--foreground)]" href="https://github.com/agentserver/agentserver/releases" target="_blank" rel="noopener noreferrer">Changelog</a></li>
          </ul>
        </div>
        <div>
          <p className="font-mono font-semibold mb-3">{t('footer.col2.title')}</p>
          <ul className="space-y-1 text-[var(--muted-foreground)]">
            <li><span>{t('footer.col2.weixin')}</span></li>
            <li><span>{t('footer.col2.telegram')}</span></li>
            <li><a className="hover:text-[var(--foreground)]" href="https://github.com/agentserver/agentserver/issues" target="_blank" rel="noopener noreferrer">{t('footer.col2.issues')}</a></li>
          </ul>
        </div>
        <div>
          <p className="font-mono font-semibold mb-3">{t('footer.col3.title')}</p>
          <ul className="space-y-1 text-[var(--muted-foreground)]">
            <li><a className="hover:text-[var(--foreground)]" href="https://github.com/agentserver/agentserver/blob/main/LICENSE" target="_blank" rel="noopener noreferrer">{t('footer.col3.license')}</a></li>
            <li><span>{t('footer.col3.privacy')}</span></li>
            <li><span>{t('footer.col3.contact')}</span></li>
          </ul>
        </div>
      </div>
      <p className="mt-8 text-center font-mono text-[10px] text-[var(--muted-foreground)]">
        v{__APP_VERSION__} · {__BUILD_COMMIT__} · built {__BUILD_DATE__}
      </p>
    </footer>
  )
}
```

Note: the spec lists a WeChat group QR-on-hover (Open Question #3). It is intentionally **omitted** here — text only — because no asset is in the repo. If/when an asset arrives, swap `<span>{t('footer.col2.weixin')}</span>` for a hover-popover component.

- [ ] **Step 2: Mount in `Home.tsx`**

Add `<HomeFooter />` after `<HomeFinalCTA />`. Final `Home.tsx`:

```tsx
import { HomeNav } from './HomeNav'
import { HomeHero } from './HomeHero'
import { HomeQuote } from './HomeQuote'
import { HomePillars } from './HomePillars'
import { HomeCompare } from './HomeCompare'
import { HomeOnboarding } from './HomeOnboarding'
import { HomeFinalCTA } from './HomeFinalCTA'
import { HomeFooter } from './HomeFooter'

export function Home() {
  return (
    <div data-theme="home" className="min-h-screen bg-[var(--background)] text-[var(--foreground)]">
      <HomeNav />
      <main className="mx-auto max-w-6xl px-6">
        <HomeHero />
        <HomeQuote />
        <HomePillars />
        <HomeCompare />
        <HomeOnboarding />
        <HomeFinalCTA />
      </main>
      <HomeFooter />
    </div>
  )
}
```

- [ ] **Step 3: Typecheck, lint, build**

```bash
cd web && pnpm tsc -b && pnpm lint && pnpm build
```

All three should exit 0. `pnpm build` is the first time we exercise the full Vite build with `__BUILD_*__` defines — verify the footer text in `web/dist/assets/*.js` contains the actual version/commit/date, not the literal `__APP_VERSION__`.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Home/HomeFooter.tsx web/src/components/Home/Home.tsx
git commit -m "feat(web): add HomeFooter with build stamp"
```

---

### Task 14: End-to-end visual verification + responsive sweep

This is the final manual gate. No code is written; the engineer verifies the assembled page across breakpoints, themes, and the reduced-motion fallback.

- [ ] **Step 1: Start dev server**

```bash
cd web && pnpm dev
```

- [ ] **Step 2: Desktop sweep (1280×800)**

In the browser, visit `http://localhost:5173/`:
- All sections render in order: nav, hero, quote, pillars, compare, onboarding, final CTA, footer
- Hero animation completes once, stops with all panes populated
- Refresh once — animation runs again
- Click each nav jump link (`Why`, `How`, `Compare`) — page scrolls to the right section
- Click `Sign in →` (in nav) → `/login`
- Click `← agentserver` on `/login` → back to `/`
- Click language toggle in nav — page reloads, content flips zh ↔ en
- Switch system to dark mode (or apply `class="dark"` to `<html>` via DevTools) — verify terminal panes go black with green accent, light-mode terminal goes light with neutral accent

- [ ] **Step 3: Tablet sweep (~900×1200)**

Resize the viewport. Verify:
- Hero panes stack vertically (still all three visible)
- Pillars become 2 columns or single column
- Compare table can be scrolled horizontally without overflowing the page

- [ ] **Step 4: Mobile sweep (~390×844)**

Verify:
- Nav center jump-links are hidden (only logo, language toggle, Sign in)
- All three hero panes stack
- Pillars become a single column
- Compare table scrolls horizontally
- Onboarding cards stack to a single column

- [ ] **Step 5: Reduced-motion fallback**

In DevTools → Rendering → "Emulate CSS prefers-reduced-motion: reduce". Refresh `/`. Verify:
- Hero panes render fully populated immediately (no fade-in)
- Pulsing dot in nav is steady (not pulsing)
- No animation jitter anywhere on the page

- [ ] **Step 6: Authenticated user does not regress**

Log in via `/login` (use any existing test account). Verify:
- Lands on the workspace UI as before (no Home page intercept)
- Sign out, return to `/` — Home page renders again

- [ ] **Step 7: Production build smoke**

```bash
cd web && pnpm build && pnpm preview
```

Visit the preview URL (typically `http://localhost:4173/`). Verify the same desktop sweep works against the production bundle.

- [ ] **Step 8: If everything passes, no commit required.** If anything regressed during the sweep, file the fix as a follow-up task in this plan and address before considering the work merged.

---

## Self-Review (already performed by plan author)

**Spec coverage check** — every locked decision in `2026-05-22-unauth-home-design.md` traced to a task:

| Spec section | Task |
|---|---|
| Routing (`/` → Home, `/login` preserved, `/oauth2/*` preserved, wildcard → `/`) | Task 4 |
| `Login.tsx` back-link | Task 5 |
| CSS tokens scoped to `[data-theme="home"]` | Task 1 |
| Build-time `__APP_VERSION__` / `__BUILD_COMMIT__` / `__BUILD_DATE__` | Task 1 (defines) + Tasks 7, 13 (consumers) |
| i18n primitive (`detectLocale` / `setLocale` / `useT`) | Task 2 |
| Full dictionary (~80 keys) | Task 3 |
| HomeNav (sticky, jump links, language toggle, Sign in) | Task 6 |
| HomeHero (three-pane, one-shot animation, IntersectionObserver, reduced-motion) | Task 7 |
| HomeQuote (with two highlighted words + zh caption) | Task 8 |
| HomePillars (3 cards + pill strip) | Task 9 |
| HomeCompare (real `<table>`, highlighted row) | Task 10 |
| HomeOnboarding (7 steps, step 6 emphasized) | Task 11 |
| HomeFinalCTA (full-width band, 2 buttons) | Task 12 |
| HomeFooter (3 cols + version strip) | Task 13 |
| Responsive across 3 breakpoints | Task 14 |
| Reduced-motion fallback | Task 14 |
| AA contrast / `<table>` / focus rings | Tasks 6–13 (markup), Task 14 (verify) |

**Open Questions from spec (intentionally deferred):**
- README rename — out of scope (separate PR).
- Live "devices connected" counter — not implemented; the hero chip just shows "online".
- WeChat group QR — text-only in footer (noted inline in Task 13).

**Placeholder scan:** none — every code step shows the exact code, every verification step shows the exact command and expected outcome.

**Type consistency:** `useT(dicts)` signature is defined in Task 2 and consumed identically in Tasks 6–13. `Locale = 'zh' | 'en'` is the only locale type. The build-time globals `__APP_VERSION__` / `__BUILD_COMMIT__` / `__BUILD_DATE__` are declared once in Task 1's `vite-env.d.ts` and referenced consistently in Tasks 7 and 13.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-22-unauth-home.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using `executing-plans`, batch execution with checkpoints.

Which approach?
