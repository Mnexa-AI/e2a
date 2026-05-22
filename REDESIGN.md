# Loft redesign — migration guide

The Loft redesign replaces the current bare-Tailwind interface with a warm cream + ink + ember system, Geist letterforms, and editorial italic moments reserved for headlines. Two PRs.

---

## TL;DR for Claude Code

You are migrating `web/` from its current visual system to the Loft redesign. Reference specs and production-ready brand assets live in [`redesign/`](./redesign/) at this repo root.

**Do not** change routes, API contracts, or test contracts — restyle in place. Run `npm run lint && npm test && npm run build` from `web/` before declaring each PR done.

Read in order:

1. This document end-to-end
2. [`redesign/loft-tokens.css`](./redesign/loft-tokens.css) — the token source of truth
3. [`redesign/brand/BRAND.md`](./redesign/brand/BRAND.md) — brand usage guide
4. [`BACKEND_TODO.md`](./BACKEND_TODO.md) — backend gaps (separate work stream; UI ships first via graceful degradation)

---

## Two-PR plan

### PR 1 — Foundations

Visual reset without page-level layout changes. Reviewable on its own.

- Token migration: merge `redesign/loft-tokens.css` into `web/src/app/globals.css` and rewire the Tailwind `@theme inline` block
- Font wiring: Geist + Instrument Serif + JetBrains Mono via `next/font`
- Brand assets: copy SVGs + `og-image.png` into `web/public/`, wire into `app/layout.tsx` metadata
- Shared primitives: extract `Sidebar`, `Topbar`, `Chip`, `Eyebrow`, `Button`, `InkConsole` into `web/src/app/components/loft/` as TypeScript components (port from `redesign/shared.jsx`)
- **No page-level changes yet.** All existing pages should still render — they'll just look reskinned.

### PR 2 — Pages

Each of the 7 page mocks ported in place, plus the out-of-scope pages.

- All 7 dashboard/marketing pages restyled (mapping table below)
- Out-of-scope pages get the visual grammar applied (blog, docs, api-docs, magic-link landing, 404)
- Tests rewritten as pages are touched
- Mobile responsive added during port (guidance below)

---

## Working principles

1. **Preserve every route and API contract.** Don't rename files, don't change props, don't fold pages together. Routes, query params, request/response shapes are unchanged.
2. **Tests stay green.** Rewrite assertions against new markup as you port each page. Don't `.skip` — fix.
3. **Mobile responsive.** Mocks are desktop-only (1280 wide). Add Tailwind breakpoints during port. See §5.
4. **Graceful degradation for missing backend.** Where the UI shows data that doesn't exist server-side (dashboard stats, "last used" on keys, per-record DNS status, ⌘K search), render with zeros / "—" / hidden behind a flag. **Never crash, never fake data.** See `BACKEND_TODO.md` for the full list.
5. **No new dependencies.** Tailwind 4, `next/font`, the existing React/Next stack only. No animation libraries, no icon packages — the redesign uses inline SVG sparingly.
6. **TypeScript everything new.** Port `redesign/*.jsx` to `.tsx`.

---

## 1. Token migration (PR 1)

Current `web/src/app/globals.css` is bare — six color variables and a `@theme inline` block. Replace with Loft tokens while keeping existing Tailwind utility classes (`bg-background`, `text-foreground`, `border-border`) functional.

**Step 1.** Copy `redesign/loft-tokens.css` content into `web/src/app/globals.css` (replacing the current `:root` and `.dark` blocks). Drop the `@font-face Geist` block from the tokens file — `next/font` will handle Geist loading.

**Step 2.** Rewire `@theme inline` so existing Tailwind classes resolve to Loft tokens:

```css
@theme inline {
  --color-background: var(--bg);
  --color-foreground: var(--fg-strong);
  --color-accent:     var(--accent-fill);
  --color-muted:      var(--fg-muted);
  --color-border:     var(--border);
  --color-surface:    var(--bg-panel);

  /* New token surface for Tailwind */
  --color-ink:        var(--ink);
  --color-ink-fg:     var(--ink-fg);
  --color-accent-soft:    var(--accent-soft);
  --color-accent-strong:  var(--accent-strong);

  --font-sans:    var(--f-ui);
  --font-mono:    var(--f-mono);
  --font-serif:   'Instrument Serif', Georgia, serif;
}
```

**Step 3.** Verify by visiting `/` and `/dashboard` — colors and fonts should change site-wide; layouts should be unchanged.

---

## 2. Font wiring (PR 1)

Update `web/src/app/layout.tsx`:

```tsx
import { Geist, Geist_Mono, Instrument_Serif } from 'next/font/google';

const geist = Geist({
  subsets: ['latin'],
  variable: '--f-ui',
  weight: ['400', '500', '600', '700'],
});

const jetbrains = Geist_Mono({           // or: JetBrains_Mono
  subsets: ['latin'],
  variable: '--f-mono',
  weight: ['400', '500', '600', '700'],
});

const instrumentSerif = Instrument_Serif({
  subsets: ['latin'],
  variable: '--f-editorial',
  weight: '400',
  style: ['normal', 'italic'],
});

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${geist.variable} ${jetbrains.variable} ${instrumentSerif.variable}`}>
      <body className="bg-background text-foreground font-sans">{children}</body>
    </html>
  );
}
```

Note: the `loft-tokens.css` file defines `--f-ui` and `--f-mono` to use Geist + JetBrains Mono with system fallbacks. `next/font` overrides them with the loaded font stacks. Instrument Serif is exposed as `--f-editorial` — use `font-family: var(--f-editorial); font-style: italic;` for editorial headlines.

---

## 3. Brand assets (PR 1)

Copy from `redesign/brand/` to `web/public/`:

| Source | Destination | Used by |
|---|---|---|
| `favicon.svg` | `web/public/favicon.svg` | All pages |
| `apple-touch-icon.svg` | `web/public/apple-touch-icon.svg` | iOS home-screen |
| `og-image.png` | `web/public/og-image.png` | Social previews (don't use the .svg — many crawlers don't render SVG) |
| `logomark.svg` | `web/public/logomark.svg` | Sidebar logo block |
| `logo-wordmark.svg` | `web/public/logo-wordmark.svg` | Marketing nav |
| `logo-wordmark-ink.svg` | `web/public/logo-wordmark-ink.svg` | Dark surfaces, ink consoles |
| `logo-wordmark-mono.svg` | `web/public/logo-wordmark-mono.svg` | Invoices, monochrome contexts |

Update `app/layout.tsx` metadata:

```tsx
export const metadata: Metadata = {
  title: { default: 'e2a — email for AI agents', template: '%s · e2a' },
  description: 'An authenticated email gateway for AI agents.',
  metadataBase: new URL('https://e2a.dev'),
  openGraph: {
    type: 'website',
    images: [{ url: '/og-image.png', width: 1200, height: 630 }],
  },
  twitter: { card: 'summary_large_image', images: ['/og-image.png'] },
  icons: {
    icon: [{ url: '/favicon.svg', type: 'image/svg+xml' }],
    apple: '/apple-touch-icon.svg',
  },
};
```

Read `redesign/brand/BRAND.md` for clear-space rules, don'ts, and the path-conversion note (production polish step: convert italic glyphs to traced paths).

---

## 4. Shared primitives (PR 1)

Create `web/src/app/components/loft/` (new folder; leave existing `components/` untouched). Port from `redesign/shared.jsx`:

| Component | Notes |
|---|---|
| `Sidebar.tsx` | Logo block at top, nav in middle, user identity card + sign-out at bottom. **No workspace/org switcher** — until BACKEND_TODO #9 (orgs) ships, a static "workspace" card here would just duplicate the bottom user card. Reintroduce only when there are real orgs to switch between. |
| `Topbar.tsx` | ⌘K search affordance: render but make it a no-op or hide behind `<NEXT_PUBLIC_SEARCH_ENABLED>` flag (default off). See BACKEND_TODO #13. Shares `--chrome-h` with the sidebar logo block so the two bottom borders form one continuous horizontal divider. |
| `Chip.tsx` | Accepts `tone` and `mono` props matching the redesign mocks |
| `Dot.tsx` | 7px status dot |
| `Eyebrow.tsx` | Mono uppercase ember label |
| `InkConsole.tsx` | The agent-native code block. Lines are `{ c: 'comment'|'prompt'|'string'|'accent'\|'plain', text }` or React nodes. |
| `Button.tsx` | `primary`, `ghost`, `mono` variants matching `loftBtnPrimary`/`loftBtnGhost`/`loftBtnMono` in `shared.jsx` |

Translate inline styles to Tailwind utility classes using the new tokens. Where Tailwind doesn't cover a token (e.g., `var(--r-md)`), use `style={{ borderRadius: 'var(--r-md)' }}` — don't extend the Tailwind config for one-offs.

**Chrome height.** The sidebar logo block and the page `Topbar` both pull their height from a single CSS variable `--chrome-h` (defined in `globals.css`, currently `68px`). Use it for any other top-row chrome you add. Don't hard-code the height in either place — bump the variable and both move together.

**Acceptance:** the 7 primitives render in isolation and the existing pages still mount without errors after PR 1.

---

## 5. Page mapping (PR 2)

| Redesign mock | Target Next.js route | Component file(s) to touch |
|---|---|---|
| `redesign/landing.jsx` | `/` | `web/src/app/page.tsx` (+ extract sections into `_components/` if it grows past ~200 lines) |
| `redesign/dashboard.jsx` | `/dashboard` | `web/src/app/(app)/dashboard/page.tsx` + `_components/AgentCard.tsx`, `ActivityPanel.tsx`, etc. |
| `redesign/pending.jsx` | `/dashboard/pending` and `/dashboard/pending/review` | `web/src/app/(app)/dashboard/pending/page.tsx` (list) + a new `review/page.tsx` (detail with diff/approve/reject) |
| `redesign/get-started.jsx` | `/get-started` | `web/src/app/(app)/get-started/page.tsx` + the existing `_components/` |
| `redesign/api-keys.jsx` | `/api-keys` | `web/src/app/(app)/api-keys/page.tsx` |
| `redesign/domains.jsx` | `/domains` | `web/src/app/(app)/domains/page.tsx` + `_components/DomainCard.tsx`, `AddDomainForm.tsx` |
| `redesign/settings.jsx` | `/settings` (new) | New `web/src/app/(app)/settings/page.tsx`. Use the existing endpoints: `/api/auth/me`, `/api/v1/users/me/signing-secrets`, `/api/v1/users/me/export`, `DELETE /api/v1/users/me`. |

### Per-page degradation hints

- **Dashboard stats strip:** Render `—` for all four cards until `GET /api/dashboard/stats` exists (BACKEND_TODO #1). Don't fabricate values.
- **Agent card per-row stats:** Render `—` for `Inbound·7d`, `Outbound·7d`, `Pending`, `Last delivery` until enriched `/api/dashboard/agents` ships (BACKEND_TODO #2). The chips above the row (verified, mode, HITL) all work today.
- **API keys table:** Hide `Last used` and `Scopes` columns at first; show only Name / Prefix / Created / Revoke. Add `Last used` once BACKEND_TODO #3 ships. Don't ship Scopes column at all until #11.
- **Domains DNS check table:** Render only MX + SPF rows. Hide the DKIM row entirely until BACKEND_TODO #4 + #5 ship. Per-record status shows a single global "verified / pending" until #4.
- **Domains stats strip:** Same as dashboard — render `—` until backend exists.
- **Settings → Usage card:** Use the existing `usage_summaries` table via a new minimal endpoint, OR render `—` until BACKEND_TODO #1 includes per-day/per-month aggregates.
- **Settings → Profile edit-name:** Render the button as disabled with a tooltip until `PATCH /api/auth/me` exists (BACKEND_TODO #8).
- **Settings → Notifications:** Already designed as "Coming soon" — keep it that way.
- **Pending review "reviewed by":** Render `reviewed_at` only, not the reviewer name, until BACKEND_TODO #6 ships.

---

## 6. Out-of-scope pages (PR 2)

Pages without dedicated mocks but should inherit the visual system:

### `/blog` and `/blog/[slug]`

- Tokens apply automatically via `globals.css` (done in PR 1)
- Reading layout: `max-w-[720px]`, `leading-[1.6]`, generous paragraph spacing
- Headlines: `font-[var(--f-editorial)] italic` for h1 + h2 lead-ins (lowercase, ember on key word — see how the dashboard hero treats "now on Loft's surfaces")
- Code blocks in MDX: route through `<InkConsole>` via `mdx-components.tsx`
- Inline links: `text-[var(--accent-strong)] underline underline-offset-2`

### `/docs` and `/docs/python`

- Same reading layout as blog
- Add a left rail with section anchors using the `Sidebar` primitive (sticky, ≥ md)

### `/api-docs`

- Currently embeds Scalar via `web/public/scalar.html`. Pass Loft tokens to Scalar's theme config in the page wrapper.
- Wrap in an ink-bordered container — matches the rest of the system

### Magic-link approval landing (`/api/v1/approve`, `/api/v1/reject`)

- These render minimal HTML server-side from Go (`internal/agent/hitl_magic_api.go`). Update the Go templates to:
  - Cream surface, Geist headline, ember CTA button
  - Show the message preview in an ink-styled panel
  - Approve / Reject pair (primary ember + ghost)

### `not-found.tsx` and `error.tsx`

- Cream surface
- Italic "404" in Instrument Serif at `clamp(120px, 20vw, 200px)`
- Mono path readout
- Ember CTA back to `/dashboard`

---

## 7. Mobile responsive (PR 2)

Mocks are at 1280. Use Tailwind breakpoints during port:

- `sm:` 640px (large phone) · `md:` 768px (tablet) · `lg:` 1024px (laptop) · `xl:` 1280px (mock baseline)

| Element | ≥ md | < md |
|---|---|---|
| Sidebar | 248px fixed left | Hidden; hamburger in topbar opens a slide-in sheet (focus-trap, ESC closes, click backdrop closes) |
| Topbar search | Full bar with ⌘K hint | Icon button only; opens a fullscreen overlay if search ships |
| Stats strip (4 cards) | 4 cols → 2 cols at `md` | 1 col |
| Tables (API keys, Domains, Invoices) | As designed | Each row becomes a stacked card; column header → label, cell → value |
| Page header (h1 + buttons) | `flex-row` with buttons right | `flex-col`, buttons `w-full` below heading |
| Hero / page titles | Instrument Serif 44px | 32px (`text-[32px] md:text-[44px]`) |
| Agent cards | Side-by-side stats grid | Stats stack 2×2 |

**Touch targets:** Every interactive element ≥ 44px tap area. Use `min-h-[44px]` and adequate padding on chips and icon buttons.

**Acceptance:** every page in `web/src/app/` renders cleanly at 375×667 (iPhone SE), 414×896 (iPhone Pro Max), and 1280×800 (laptop). No horizontal scroll. Test with `@testing-library/react` resize at the breakpoints.

---

## 8. Tests (PR 2)

Update existing tests as you port each page:

- Replace selectors querying old class names (`text-2xl font-bold`) with role-based queries (`getByRole`, `getByText`) where possible
- Add at least one mobile-viewport assertion per page
- Don't skip — rewrite

The current test files are at `web/src/app/**/*.test.tsx`. They mock `fetch` against the API routes — those mocks stay valid since API contracts don't change.

---

## 9. Definition of done

### PR 1
- [ ] `npm run lint && npm test && npm run build` all green
- [ ] Visiting any existing page shows the Loft palette + fonts; layouts unchanged
- [ ] `web/public/` has the brand assets; favicon + OG show in browser tab and link previews
- [ ] `web/src/app/components/loft/` has the 7 primitives, each typed and exported

### PR 2
- [ ] All 7 mocked pages match their `redesign/<page>.jsx` counterparts visually
- [ ] Out-of-scope pages (blog, docs, api-docs, magic-link, 404) are visually consistent
- [ ] Mobile-responsive at 375 / 768 / 1280 — no horizontal scroll, all touch targets ≥ 44px
- [ ] `npm run lint && npm test && npm run build` all green
- [ ] No console errors on any page

---

## 10. Files in `redesign/`

| File | Purpose |
|---|---|
| `loft-tokens.css` | Token source of truth |
| `shared.jsx` | Primitives reference (Sidebar/Topbar/Chip/Eyebrow/InkConsole/Button) |
| `landing.jsx`, `dashboard.jsx`, `pending.jsx`, `get-started.jsx`, `api-keys.jsx`, `domains.jsx`, `settings.jsx` | Page mocks, one per route |
| `brand/*.svg`, `brand/og-image.png` | Production-ready brand assets |
| `brand/BRAND.md` | Brand usage guide — read this for clear space, don'ts, font notes |
| `README.md` | Pointer back to this document |

The `.jsx` files import from `loft-tokens.css` and render with inline styles (because they were authored in a design canvas). They are **reference specs**, not source code — translate them into TypeScript components using Tailwind utilities + new tokens during PR 2.
