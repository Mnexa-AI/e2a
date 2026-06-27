import { defineConfig } from "tsup";

// Build the library to dist/ as ESM + type declarations.
//  - entry: the single public barrel (src/index.ts)
//  - the two CSS files are copied to dist/ verbatim so consumers (and
//    claude.ai/design) can `@import "@e2a/ui/styles.css"`.
//  - react/react-dom stay external — they're peer deps, not bundled.
export default defineConfig({
  entry: ["src/index.ts"],
  format: ["esm"],
  dts: true,
  // dist/ is committed (consumed by web via file: + verified fresh in CI), so
  // skip sourcemaps to keep the tracked artifact minimal.
  sourcemap: false,
  clean: true,
  external: ["react", "react-dom", "react/jsx-runtime"],
  // The bundle includes interactive components (InkConsole, Collapsible) that
  // use React hooks. esbuild strips per-file "use client" directives when it
  // bundles, so mark the whole entry a client module — this lets Next App
  // Router Server Components import and render the library. (No-op for the
  // design-sync esbuild bundle and Storybook, which ignore the directive.)
  banner: { js: '"use client";' },
  // Emit self-contained CSS: dist/styles.css with tokens inlined (no external
  // @import), plus dist/tokens.css. See scripts/flatten-css.mjs for why.
  onSuccess: "node scripts/flatten-css.mjs",
});
