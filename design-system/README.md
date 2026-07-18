# @e2a/ui — Loft design system

A standalone, buildable React component library for e2a's "Loft" design
language. This package is the source of truth for the shared UI atoms and
their design tokens, extracted from the web app so they can be reused,
documented in Storybook, and synced to claude.ai/design.

## What's here

```
design-system/
├── src/
│   ├── index.ts          # public API — re-exports every component
│   ├── tokens.css        # Loft design tokens (colors, type, spacing) — light + .dark
│   ├── styles.css        # @imports tokens.css + base + one class per component
│   ├── Button/           # Button.tsx + Button.stories.tsx
│   ├── Chip/
│   ├── Dot/
│   ├── Eyebrow/
│   ├── ThemeToggle/      # controlled segmented theme control
│   ├── InkConsole/       # agent-native dark code console
│   ├── Logo/             # e2a wordmark + boxed monogram (themeable)
│   ├── Field/            # labeled text input
│   ├── Avatar/           # deterministic-color initials avatar
│   ├── Collapsible/      # disclosure section
│   └── Card/             # surface container
├── .storybook/           # Storybook config (react-vite)
├── tsup.config.ts        # build → dist/
└── package.json
```

## Develop

```bash
npm ci                   # install locked deps
npm run storybook        # component workshop at http://localhost:6006
npm run build            # compile to dist/ (index.js + .d.ts + tokens.css + styles.css)
npm run typecheck        # tsc --noEmit
```

## Use it

```tsx
import { Button, Chip } from "@e2a/ui";
import "@e2a/ui/styles.css";   // once, at your app root

export function Example() {
  return (
    <div>
      <Chip tone="success">delivered</Chip>
      <Button variant="primary">Send email</Button>
    </div>
  );
}
```

Dark mode: add `class="dark"` to `<html>` — every token flips automatically.

## A note on styling

The original components in `web/src/app/components/loft/` mix CSS-variable
inline styles with Tailwind utility classes. To keep this package free of a
Tailwind build, the atoms here are adapted to be **fully CSS-driven**: they
emit semantic class names (`loft-btn`, `loft-chip`, …) defined in `styles.css`,
using the same Loft tokens, so the look is identical but the package stands
alone. If you'd rather copy components verbatim, add Tailwind 4 to this package
and import the token bridge instead.

## Adding a component

1. `src/Thing/Thing.tsx` — a prop-driven component using `loft-*` classes.
2. Add its styles to `src/styles.css`.
3. `src/Thing/Thing.stories.tsx` — one story per meaningful state.
4. Re-export it from `src/index.ts`.
5. `npm run build` and you're done.

Good next extraction candidates from the app: `PageShell` and `Topbar` (these
lean on responsive Tailwind utilities — easiest once this package adopts
Tailwind), and `Sidebar` (depends on Next.js routing + auth/pending-count
context — decouple those first). `ThemeToggle` was extracted as a *controlled*
component (it took its state from a React context in the app); wire its
`value`/`onChange` to your own theme state.
