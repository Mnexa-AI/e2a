import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    coverage: {
      provider: "v8",
      include: ["src/**/*.ts"],
      // src/v1/generated/ is OpenAPI-Generator output — not hand-written code.
      exclude: ["src/v1/generated/**"],
      // Floors sit a few points under current coverage (lines 90.0,
      // statements 86.4, functions 80.2, branches 84.9). Ratchet up, never down.
      thresholds: {
        lines: 85,
        statements: 82,
        functions: 76,
        branches: 80,
      },
    },
  },
});
