# `redesign/` — Loft redesign reference

This folder is **reference material** for the Loft visual redesign. Production code lives in `web/`.

Start with [`../REDESIGN.md`](../REDESIGN.md) — the master migration guide.

## What's here

- `loft-tokens.css` — token source of truth. Merge into `web/src/app/globals.css`.
- `shared.jsx` — React reference for the shared primitives (Sidebar, Topbar, Chip, Eyebrow, InkConsole, Button). Port to TypeScript components in `web/src/app/components/loft/`.
- `<page>.jsx` — one file per route in the new design. Each renders against `loft-tokens.css` variables using inline styles (they were authored in a design canvas). Translate inline styles to Tailwind utilities during PR 2.
- `brand/` — production-ready SVG + PNG assets, plus [`BRAND.md`](./brand/BRAND.md) usage guide.

## Don't import from `web/`

Files in this folder are specs, not source. After both PRs land, this folder can stay as historical reference or be removed — `web/` does not depend on it.
