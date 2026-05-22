# e2a · Brand kit

Production-ready brand artifacts for the Loft redesign. Drop the SVGs into `e2a/web/public/` (or wherever your asset pipeline expects them) and wire them into `layout.tsx` / `<head>`.

---

## 1. Logos

| File | Use | Size |
|---|---|---|
| `logomark.svg` | App icon, square contexts (favicons, GH org avatar, sidebar tile) | 256×256 viewBox, scales freely |
| `logo-wordmark.svg` | Default wordmark on cream surfaces — nav, footer, marketing | 640×220 |
| `logo-wordmark-ink.svg` | Wordmark on ink (dark) surfaces — ink consoles, dark-mode nav | 640×220 |
| `logo-wordmark-mono.svg` | Single-color contexts (invoices, embroidery, monochrome print). Uses `currentColor` | 640×220 |

### Color of the "2"
- On cream → ember `#C56C42` (`--accent-fill`)
- On ink → lighter ember `#E08A60` (`--accent`) for AA contrast
- Mono → inherits `currentColor`

### Letterforms
- **Geist** (Vercel) at weight **600**, lowercase, tracking **-0.06em** for the wordmark, **-0.07em** for the standalone logomark "2"
- Editorial *italic* moves live inside the product (page headlines, hero), not in the brand mark itself

### Clear space
Reserve **one cap-height** of empty space around the wordmark. For the logomark, reserve **8% of the tile size** on every side before placing anything beside it.

### Minimum size
- Logomark: 16×16 (use `favicon.svg` below that size)
- Wordmark: 80px wide on screen, 24mm in print

### Don't
- Don't recolor the "2" to anything other than ember / lighter-ember / mono
- Don't apply drop shadows, glows, or gradients to either mark
- Don't stretch — italic forms break fast under non-uniform scaling
- Don't put the wordmark on a busy photo without an ink or cream plate behind it

---

## 2. Favicon set

`favicon.svg` is a self-contained SVG favicon (uses a system-safe Georgia italic so it renders crisply at 16px without depending on Google Fonts). Wire it into `app/layout.tsx`:

```tsx
export const metadata: Metadata = {
  // …
  icons: {
    icon: [
      { url: '/favicon.svg', type: 'image/svg+xml' },
      { url: '/favicon.ico', sizes: 'any' },           // legacy fallback
    ],
    apple: '/apple-touch-icon.svg',
  },
};
```

`apple-touch-icon.svg` (180×180) handles iOS home-screen. For Safari pinned-tab, browsers will render `favicon.svg` directly.

> **For production polish:** convert the italic-2 glyph to paths in a vector editor before shipping. Georgia italic looks fine but the brand glyph is Instrument Serif — pathified glyphs render identically everywhere without bundling fonts.

---

## 3. Social card

`og-image.svg` — 1200×630, the standard OG / Twitter Card aspect ratio.

```tsx
export const metadata: Metadata = {
  openGraph: {
    images: ['/og-image.png'],   // ← convert SVG to PNG before shipping
    type: 'website',
  },
  twitter: {
    card: 'summary_large_image',
    images: ['/og-image.png'],
  },
};
```

> **Convert to PNG before deploy.** Some link-preview crawlers (Slack, iMessage, LinkedIn) don't render SVG. Run the SVG through ImageMagick or Figma at 1200×630 → `og-image.png`.

---

## 4. Palette

`palette.svg` — visual reference card showing the six anchor colors. Full token list lives in `redesign/loft-tokens.css`.

### Anchor colors
| Token | Hex | Use |
|---|---|---|
| `--bg` (Cream) | `#FAF7F2` | Default surface |
| `--bg-elev` (Cream elev) | `#F2ECE2` | Cards, raised surfaces |
| `--ink` | `#1A1714` | Agent-native console ground, primary text on cream |
| `--accent-fill` (Ember) | `#C56C42` | The "2" accent, primary buttons, active nav inset |
| `--spectral` | `#5FB6C6` | Strings in ink consoles |
| `--machine` | `#B6F36E` | `$` prompt + success-on-ink accents |

The ember is the signature. Use it sparingly — eyebrow labels, the `2`, the primary CTA, the 2px active-nav inset. Everywhere else is cream-and-ink.

---

## 5. Typography

| Role | Family | Weights |
|---|---|---|
| Wordmark / UI / body | **Geist** (Vercel) | 400, 500, 600, 700 |
| Editorial italic (product headlines, hero) | **Instrument Serif** | 400, italic |
| Code / mono / eyebrows | **JetBrains Mono** | 400, 500, 600, 700 |

Load via `next/font` in `layout.tsx`:

```tsx
import { Instrument_Serif, JetBrains_Mono } from 'next/font/google';
import localFont from 'next/font/local';

const geist = localFont({
  src: './fonts/Geist-Variable.woff2',
  variable: '--font-geist',
  weight: '100 900',
});
const instrumentSerif = Instrument_Serif({
  subsets: ['latin'],
  weight: '400',
  style: ['normal', 'italic'],
  variable: '--font-instrument',
});
const jetbrains = JetBrains_Mono({
  subsets: ['latin'],
  weight: ['400', '500', '600', '700'],
  variable: '--font-mono',
});
```

The token file (`loft-tokens.css`) wires `--f-ui`, `--f-mono`, and the editorial serif onto these variables.

---

## 6. Voice & copy

- **Editorial italic for the moves that matter** — page titles, hero, marketing. Don't italicize body.
- **Mono eyebrows** — uppercase, 0.08em tracking, ember. Use sparingly (one per page is plenty).
- **`//` and `$` are part of the brand** — comments and prompts in ink consoles use chartreuse `$` and grey `//`. Don't sub them for ASCII variants.
- **Avoid corporate hedge words** — "leverage", "robust", "world-class". e2a-voice is concrete: *"e2a delivers your agent's mail. SPF and DKIM checked on the way in, signed on the way out."*

---

## 7. File-naming convention

If you regenerate or add variants, follow:
- `logomark*.svg` — square mark only
- `logo-wordmark*.svg` — horizontal wordmark
- `logo-stacked*.svg` — vertical lockup (none shipped yet — add if needed)
- `*-ink.svg` — variant for dark surfaces
- `*-mono.svg` — single-color variant

---

## 8. What's still missing

- **PNG raster versions** of every SVG at 1×/2×/3× (for email signatures, slide decks, etc.) — generate with `rsvg-convert` or Figma export
- **Embroidery / single-line vector** of the wordmark (for merch) — needs the Instrument Serif glyph traced as a single closed path
- **A motion / loading mark** — currently no animated brand asset; if you want one, the italic 2 fading in stroke-by-stroke is the natural move
