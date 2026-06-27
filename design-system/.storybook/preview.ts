import type { Preview } from "@storybook/react-vite";

// Load the design system's styles into every story so components render
// with real Loft tokens. `styles.css` @imports `tokens.css`.
import "../src/styles.css";

const preview: Preview = {
  parameters: {
    controls: { matchers: { color: /(background|color)$/i, date: /Date$/i } },
    backgrounds: {
      default: "loft",
      values: [
        { name: "loft", value: "#FAF7F2" },
        { name: "panel", value: "#FFFFFF" },
        { name: "ink", value: "#1A1714" },
      ],
    },
  },
};

export default preview;
