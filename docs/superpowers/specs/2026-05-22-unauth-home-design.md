# Unauthenticated Homepage Design

**Date:** 2026-05-22
**Status:** Draft
**Scope:** `web/src` (frontend only; no backend or routing changes outside React Router)

## Problem

`agent.cs.ac.cn` currently shows unauthenticated visitors only a small centered login card (`web/src/components/Login.tsx`). A first-time visitor cannot tell what the product does, why they would want it, or how it differs from adjacent tools (OpenClaw, Claude Code on the web, Claude Code Agent Teams). The README has a clear pitch — _"Your Personal Computility — command devices anywhere, from your WeChat chat window"_ — but the web surface does not reflect it.

The goal is a single-page marketing homepage at `/` that turns drive-by visitors into registered users while preserving the existing `/login` route untouched.

## Solution

Add a `/` route rendering `<Home />` for unauthenticated visitors. The page is one continuous scrolling story — hero → quote → 3 pillars → comparison table → 7-step onboarding → final CTA → footer — in a dark terminal aesthetic that extends the README's voice into the web. The hero uses a three-pane animation — two stacked terminal panes on the left, a WeChat conversation on the right — showing **three devices collaborating to compute a result and deliver it back via WeChat** — a workflow that genuinely requires `agentserver`'s orchestration layer and that single-device tools (e.g., OpenClaw) cannot do.

`/login` keeps its current React component and behavior; the only change is a small "← agentserver" back link.

## Locked decisions (resolved during brainstorming)

| Topic | Decision | Rationale |
|---|---|---|
| Brand tagline | `Your Personal Computility.` / `你的个人算力网。` | New coinage: `compute` + `utility` — frames product as infrastructure. Replaces "Personal Compute Network" in this page (README rename is a separate task). |
| Visual tone | Geek / terminal (dark, monospace, terminal motif) — see brainstorm `tone.html` | Continues README voice; aligned with developer audience; avoids "self-promotional SaaS" aesthetic that suits a `cs.ac.cn` domain poorly. |
| Layout | "Story arc" — linear long-scroll narrative (`layout.html` option α) | Concept (`Personal Computility`) is new and needs explaining; dense-console layout (β) would bury the comparison table; try-it-now layout (γ) compresses the depth `cs.ac.cn` visitors tolerate. |
| Hero scenario | **V5 — multi-device data pipeline → WeChat delivery** | OpenClaw can do single-device remote dispatch (V4) and long-running tasks with notification (V2); only `agentserver` can route a workflow across heterogeneous devices and deliver the result through an IM channel. |
| Roadmap section | **Cut** | Roadmap is "what we are building" (self-focused); homepage is "what you get" (visitor-focused). Belongs in docs or post-login dashboard. |
| Language | Auto-detect (`navigator.language`) with manual toggle | Zero-friction default for `cs.ac.cn`'s mostly Chinese audience; explicit escape hatch for international visitors. |
| Login UX | Keep separate `/login` route; top-nav and hero CTA both link to it | Minimum coupling; preserves existing `Login.tsx` untouched. Modal would complicate OIDC redirect handoff. |

## Routing

```
/         → <Home />        (unauthenticated only; authenticated users redirect to their workspace)
/login    → <Login />       (unchanged; only the inline title becomes a "← agentserver" link back to /)
/*        → redirect to /   (unauthenticated; the existing OIDC/Workspace routes apply when authed)
```

`App.tsx`'s `authed === false` branch currently renders `<Login />` directly. Change it to render React Router's nested routes so the `/login` URL still works for direct links (e.g., OIDC callbacks and the codex-auth verify flow already use `?next=...`).

## File changes

**New:**

```
web/src/components/Home/
├── Home.tsx              # top-level shell; composes the sections
├── HomeNav.tsx           # sticky top bar: logo, jump-links, language toggle, Sign in
├── HomeHero.tsx          # dual-pane (terminal + WeChat) with one-shot animation
├── HomePillars.tsx       # three pillar cards + secondary feature pill strip
├── HomeCompare.tsx       # ASCII-styled differentiator table
├── HomeOnboarding.tsx    # 7-step horizontal timeline (vertical on mobile)
├── HomeFinalCTA.tsx      # full-width band with Sign in + Self-host CTAs
├── HomeFooter.tsx        # three-column footer + build version stamp
└── strings.ts            # i18n dictionary, namespaced per section
```

**Modified:**

- `web/src/App.tsx` — wire `/` to `<Home />` when `authed === false`; redirect `/` to workspace when `authed === true`.
- `web/src/components/Login.tsx` — replace inline `<h1>agentserver</h1>` with a small `← agentserver` link to `/`.
- `web/src/lib/i18n.ts` — **new** (despite being "modified" — the file is added here, not under `Home/`, so it is reusable later).
- `web/src/index.css` — add `--home-accent`, `--home-accent-fg`, `--home-grid`, `--home-term-bg`, `--home-term-border` CSS variables, scoped under `[data-theme="home"]` so they do not affect other pages.

**No new npm dependencies.** Tailwind v4 + CSS variables + hand-rolled `@keyframes` cover everything.

## Visual system

### Color tokens (additive)

```css
:root[data-theme="home"] {
  --home-accent:        #18181b;
  --home-accent-fg:     #fafafa;
  --home-grid:          rgba(0, 0, 0, 0.06);
  --home-term-bg:       #f4f4f5;
  --home-term-border:   #d4d4d8;
}
.dark[data-theme="home"], [data-theme="home"].dark {
  --home-accent:        #7fff7f;
  --home-accent-fg:     #000;
  --home-grid:          rgba(127, 255, 127, 0.04);
  --home-term-bg:       #000;
  --home-term-border:   #2a2a2a;
}
```

Contrast check: `#7fff7f` on `#0a0a0a` = 14.8:1 (AAA). `#18181b` on `#ffffff` = 17.6:1 (AAA).

### Typography

- **Body** — existing `system-ui, -apple-system, sans-serif`.
- **Brand / terminal panes / pillar labels** — `ui-monospace, 'SF Mono', Menlo, monospace`.
- **H1** — `font-weight: 600; letter-spacing: -0.02em; font-size: clamp(2rem, 5vw, 3.5rem);`
- **Code / terminal output** — mono; **WeChat bubbles** — sans. The mono/sans split is what visually separates "machine side" from "human side" in the hero.
- No webfonts. Emoji are unicode (`📱 🌐 🔌`), not SVG sprite.

### Section separators

Dark theme: `─ ─ ─ ✦ ─ ─ ─` (centered, `--muted-foreground`). Light theme: a single `--border` rule.

## Page sections

### 0 · TopNav (sticky)

- **Left**: `▸ agentserver` in mono, with a pulsing green dot.
- **Center**: `Why · How · Compare`  (anchor jumps).
- **Right**: `中 / EN` toggle, then `Sign in →` button.
- Scrolls with the page until reaching the viewport top, then sticks.

### 1 · Hero (V5 dual-pane)

**H1 zh:** `你的个人算力网。`
**H1 en:** `Your Personal Computility.`

**Subtitle zh:** `agentserver 把笔记本、云沙箱、家里的服务器编成一个工作区——从浏览器、命令行，或微信，一并指挥。`
**Subtitle en:** `agentserver weaves laptops, cloud sandboxes, and home servers into one workspace — commanded from your browser, your CLI, or your WeChat chat.`

**CTAs:** primary `▸ Sign in` → `/login`; secondary `View on GitHub` → repo URL.

**Visual — two stacked terminal panes + one WeChat pane, choreographed:**

```
┌─ office-macbook · codex ─────────────┐   ┌─ 我自己 ─────────────────┐
$ codex relay "把 experiment_0521         我:  把上周的实验数据
   .csv 在沙箱里画成图发我"                      画成图给我
▸ found experiment_0521.csv (124MB)
▸ relaying to cloud-sandbox-7…             AS: 📂 已从 office-macbook
                                                取到 experiment_0521.csv
┌─ cloud-sandbox-7 · codex ────────────┐
▸ pandas: 5 conditions × 12 trials         AS: 🔨 在沙箱里分析 +
▸ matplotlib: boxplot + error bars             画图中…
✓ saved plot.png (412KB)
▸ uploading via imbridge…                  AS: ✓ 完成
                                                [plot.png 缩略图]
```

Animation timeline (one-shot, no loop):

| t | Event |
|---|---|
| 0.0s | Both panes mounted, dimmed. |
| 0.2s | `office-macbook` header appears. |
| 0.4 – 2.8s | Macbook lines reveal one at a time (200–300ms each). |
| 3.0 – 5.5s | `cloud-sandbox-7` block reveals same way. |
| 5.5s | WeChat: user bubble fades + slides in (200ms). |
| 6.0s | Bot bubble "📂 已取到..." |
| 7.0s | Bot bubble "🔨 分析中..." |
| 8.5s | Bot bubble "✓ 完成" + `plot.png` thumbnail. |
| 8.5s onwards | Animation **stops**. Final state stays on screen. No fade-out, no loop. |

Implementation:

- Pure CSS `@keyframes` with `animation-delay` chains; `animation-fill-mode: forwards` to lock the final state.
- One `IntersectionObserver` to start the animation only when hero enters the viewport (cheap to start; do nothing if it never enters).
- `@media (prefers-reduced-motion: reduce)` — skip animation, render final state immediately on mount.
- A small `v0.64.14 · online` chip bottom-right of the hero card. Version comes from a `__APP_VERSION__` injected via Vite `define`.

### 2 · Quote

```
│ "Once you juggle 10+ agents across machines, you stop being a
│  conductor and become an orchestrator."
│
│  — Addy Osmani · Director, Google Gemini & Cloud AI
```

Left vertical bar in `--home-accent`. The two emphasized words (`conductor`, `orchestrator`) are accent-colored. Below, a one-line caption: `agentserver = 那个 orchestrator。` / `agentserver is that orchestrator.`

### 3 · Three Pillars

Three side-by-side cards, each: emoji + heading + 2-line body + a micro-mockup.

| Emoji | zh heading | en heading | Body (zh) | Micro-mockup |
|---|---|---|---|---|
| 📱 | 装在口袋里的指挥台 | Pocket-sized command line | 微信里一句中文，落到任意设备执行，结果推回同一个聊天框。Telegram 同样支持。 | A 3-bubble mini-chat |
| 🌐 | 一个工作区，所有设备 | One workspace, every device | 云沙箱、本地机器、IM-bound 代理——全在同一份注册表里，并排出现在 Web UI 上。 | A 4-item device list |
| 🔌 | 无公网 IP，照常入网 | No public IP. Still in the network. | 本地的 codex / Claude Code / opencode 通过 WebSocket 拨号入网，呈现为一个沙箱。 | A 2-node topology diagram |

Below the three cards, a single pill strip — small chips, low contrast — covering the secondary features that did not earn a full pillar:

`+ 暂停 / 恢复沙箱 · Jupyter 笔记本 · 多人协作 · 凭证代理 · SSO（GitHub / OIDC）· 自托管`

### 4 · Differentiator Table

**Heading zh:** `和已有的工具相比` **en:** `How it differs`

A real `<table>` (`<th scope="col">` / `<th scope="row">`) styled to look like ASCII art in the dark theme. Visual:

```
                   ┌─ local ──┬─ cloud ──┬─ peer ─┬─ chat ─┐
  OpenClaw / CCR   │    1     │    —     │   —    │   —    │
  Claude Code web  │    —     │    ✓     │   —    │   —    │
  CC Agent Teams   │    —     │  ✓(sub)  │   —    │   —    │
▸ agentserver      │ ✓ many   │    ✓     │   ✓    │   ✓    │ ◀
                   └──────────┴──────────┴────────┴────────┘
```

Highlighted row uses `background: var(--home-grid)` + `border-left: 2px solid var(--home-accent)`. Caption below: `唯一同时勾上四列的产品。` / `The only one with all four checked.`

Light theme: same table without the ASCII frame — plain rules between cells.

### 5 · 7-Step Onboarding

**Heading zh:** `7 步，把你的设备连上来` **en:** `7 steps to wire it all up`

Horizontal timeline (numbered circles connected by a line). Auto-flips vertical on `<640px`.

| # | zh title | One-line description |
|---|---|---|
| 1 | 注册 | 邮箱 / GitHub / SSO 任选 |
| 2 | 链接模型账号 | 自带 ChatGPT/Anthropic 凭证，或选平台托管账号 |
| 3 | 入网设备 | `brew install codex` → 粘贴注册码 |
| 4 | 选一台"指挥机" | 通常是你的主力笔电 |
| 5 | (可选) 开 Jupyter | 想手写代码？`ctx` 已预注入 kernel |
| **6** | **绑定微信** | 扫码即可，从此聊天框 = 终端 |
| 7 | 邀请协作者 | 角色：owner / maintainer / developer / guest |

Step 6 is bolded (matches the hero's WeChat punchline). Footer link: `完整文档 →` / `Full docs →`.

### 6 · Final CTA

Full-width band, `border-top` + `border-bottom` in `--home-accent`. Single line in H2 size:

- **zh:** `把你的设备，编进同一张算力网。`
- **en:** `Weave your devices into one Computility.`

Two buttons below: `▸ Sign in` (primary, goes to `/login`) + `Self-host on your own domain` (ghost, deep-links to README self-host section).

### 7 · Footer

Three columns + a bottom version strip:

- **agentserver** — agentserver.dev · GitHub · Docs · Changelog
- **社区 / Community** — WeChat 群 (hover: QR) · Telegram · Issues
- **法律 / Legal** — Apache-2.0 · 隐私 · 联系

Bottom rule: `v0.64.14 · <commit-short-hash> · built 2026-05-22`. Version + hash + date are Vite `define` injections; no runtime.

## i18n

Minimal, page-scoped:

```ts
// web/src/lib/i18n.ts
export type Locale = 'zh' | 'en'

const dicts: Record<Locale, Record<string, string>> = {
  zh: { /* ... */ },
  en: { /* ... */ },
}

export function detectLocale(): Locale {
  const stored = localStorage.getItem('locale')
  if (stored === 'zh' || stored === 'en') return stored
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

export function setLocale(l: Locale) {
  localStorage.setItem('locale', l)
  location.reload()
}

export function useT() {
  const locale = detectLocale()
  return (key: string): string => dicts[locale][key] ?? key
}
```

Dictionary content lives in `web/src/components/Home/strings.ts`, keys namespaced per section (`hero.h1`, `hero.subtitle`, `pillars.pocket.title`, …). Roughly 80 keys total.

Language switch = write `localStorage.locale` + `location.reload()`. No React context, no `react-i18next`, no Suspense. If a second page later needs i18n, the same `t()` is already reusable from `lib/`.

## Responsive

| Breakpoint | Hero | Pillars | Compare table |
|---|---|---|---|
| ≥1024px | Two columns: stacked terminals on left, WeChat on right | 3 columns | Full table, horizontal |
| 640–1023px | All three panes stack vertically | 2 + 1 or single column | Horizontal scroll |
| <640px | All stacked + only the first 4 hero lines + one bot bubble | 1 column | Card list (one product per row) |

On mobile the hero animation is shortened to ~5s and the loop policy is the same (one-shot).

## Accessibility

- Hero terminal: wrap each pane in `<div role="img" aria-label="agentserver 在三台设备间协同的演示" />` so screen readers do not narrate ASCII line by line.
- Hero WeChat pane: `role="log" aria-live="polite"` — bubble appearances are announced as they arrive (or all at once if `prefers-reduced-motion`).
- Compare: real `<table>` with `<caption>` (visually hidden), `<th scope>`.
- Onboarding: real `<ol>`.
- All CTAs are `<a href>` or `<button>`; focus ring uses `outline: 2px solid var(--ring); outline-offset: 2px`.
- Reduced motion: skip animation, render final state, drop pulsing dot in nav.
- Color contrast AA minimum on every text/background pair (`#7fff7f`/`#0a0a0a` = 14.8:1, body text already 4.5:1+ in existing tokens).

## Performance

- LCP element = hero H1 (plain text) — target < 1.5s.
- First-paint JS bundle increment < 8 KB gzip (no new deps; tree-shake unused).
- No third-party requests on this page: no analytics, no Google Fonts, no CDN icons.
- Hero animation is CSS-only; no JS frame ticking.
- Images: none above the fold. The pillars' micro-mockups are pure CSS / inline SVG.

## Open questions (non-blocking)

1. **README rename.** This spec assumes `Personal Compute Network → Personal Computility` and `个人计算网络 → 个人算力网` are also applied to the README and other surfaces. Out of scope here; flagged for a separate PR if not already in flight.
2. **"this many devices connected right now" live counter.** Mentioned in section 1 as nice-to-have if a `/api/public/stats` endpoint exists. If it does not exist today, ship without it; do not block on adding a backend endpoint.
3. **WeChat group QR in footer.** Image asset needs to be provided; if absent at implementation time, drop the hover tooltip and keep `WeChat 群` as plain text linking to a docs anchor.

## Non-goals

- No changes to backend, auth, OIDC, or workspace logic.
- No redesign of `Login.tsx` itself beyond the back-link.
- No redesign of post-login UI.
- No new third-party dependencies.
- No persisted analytics / telemetry on this page.
