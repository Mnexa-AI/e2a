// Emit self-contained CSS into dist/.
//
// Source keeps tokens and component styles in separate files (styles.css
// pulls tokens via `@import "./tokens.css"`), which Vite/Storybook resolve
// fine. But a downstream consumer that only copies dist/styles.css (e.g. the
// design-sync converter) won't follow that relative import — so the tokens,
// and every var(--*) reference, would go missing.
//
// To stay robust, we FLATTEN: inline tokens.css into dist/styles.css so the
// shipped stylesheet has no external @import. We still emit dist/tokens.css
// separately for consumers who want only the tokens.
import { readFileSync, writeFileSync, copyFileSync } from "node:fs";

const tokens = readFileSync("src/tokens.css", "utf8");
const styles = readFileSync("src/styles.css", "utf8");

const flattened = styles.replace(
  /@import\s+["']\.\/tokens\.css["'];?/,
  `/* tokens.css inlined at build time — see scripts/flatten-css.mjs */\n${tokens}`,
);

if (flattened === styles) {
  throw new Error(
    "flatten-css: did not find `@import \"./tokens.css\"` in src/styles.css — " +
      "the inline anchor is gone; update this script.",
  );
}

writeFileSync("dist/styles.css", flattened);
copyFileSync("src/tokens.css", "dist/tokens.css");
console.log("flatten-css: wrote self-contained dist/styles.css + dist/tokens.css");
