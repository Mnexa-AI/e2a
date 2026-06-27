# Building with @e2a/ui (Loft)

Loft is the e2a design system. Build UIs from its components and style your own
layout with its design tokens. There is **no provider or wrapper to set up** —
components are self-contained and styled from CSS variables, so they render
correctly as soon as the stylesheet is present (it is already bound for you).

## Theme & dark mode

All color/spacing/type comes from CSS custom properties defined on `:root`.
Dark mode is a single class on an ancestor: put `class="dark"` on a wrapping
element (or `<html>`) and every token — and therefore every component and any
layout you build with tokens — flips automatically. Never hardcode hex colors;
reference the tokens so light/dark both work.

## The styling idiom — use tokens, not utility classes

Loft is a **token system**, not a Tailwind-style utility-class kit. Components
carry their own styling internally (`loft-*` classes you don't author). For YOUR
own layout glue (wrappers, grids, spacing), write plain CSS/inline styles that
reference Loft tokens. The real vocabulary:

| Concern | Tokens (use `var(--…)`) |
|---|---|
| Surfaces | `--bg` (app), `--bg-panel` (cards), `--bg-elev` (raised), `--bg-sunken` |
| Text | `--fg`, `--fg-strong`, `--fg-muted`, `--fg-subtle` |
| Borders | `--border`, `--border-sub`, `--border-strong` |
| Accent (ember) | `--accent`, `--accent-fill`, `--accent-strong`, `--accent-soft`, `--accent-fg` |
| Semantic | `--success`, `--warn`, `--danger`, `--info` (+ `-bg`, `-strong` pairs) |
| Ink (agent console) | `--ink`, `--ink-elev`, `--ink-fg`, `--ink-fg-muted`, `--spectral`, `--machine` |
| Radii | `--r-sm` `--r-md` `--r-lg` `--r-xl` |
| Spacing (4px grid) | `--sp-1`…`--sp-8` |
| Type | `--f-ui`, `--f-mono`, `--f-editorial`; sizes `--fs-body` `--fs-small` `--fs-h1` `--fs-h2` … |

## Components

`Button` (variants `primary` / `ghost` / `mono`), `Chip` (tone
`success`/`warn`/`info`/`accent`/`danger`/`neutral`, plus `mono`), `Dot` (status
tone), `Eyebrow` (uppercase mono kicker), `ThemeToggle` (controlled — pass
`value` + `onChange`), `InkConsole` (dark code/console surface; takes `lines`),
`Logo` (the e2a brand mark — `variant` `wordmark`/`mark`, `tone`
`color`/`mono`/`ink`, themeable, sized by `height`), `Field` (labeled text
input; controlled `value`/`onChange`), `Avatar` (initials square, deterministic
`--av-*` color from `email`/`name`), `Collapsible` (disclosure with `label` +
`children`), `Card` (the panel surface container — wrap content blocks in it).
Read each component's `.prompt.md` for props and examples, and its `.d.ts` for
the exact API. The stylesheet (`styles.css`, which defines all the tokens above)
is the authority on the visual language — read it before inventing styling.

## Idiomatic example

```tsx
import { Button, Chip, Eyebrow } from "@e2a/ui";

function MessageCard() {
  return (
    <div
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
        padding: "var(--sp-4)",
        display: "flex",
        flexDirection: "column",
        gap: "var(--sp-3)",
      }}
    >
      <Eyebrow>Inbound mail</Eyebrow>
      <div style={{ display: "flex", alignItems: "center", gap: "var(--sp-2)" }}>
        <Chip tone="success">delivered</Chip>
        <span style={{ color: "var(--fg-muted)", fontSize: "var(--fs-small)" }}>
          2 min ago
        </span>
      </div>
      <Button variant="primary">Reply</Button>
    </div>
  );
}
```
